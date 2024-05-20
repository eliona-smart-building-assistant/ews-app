package booking

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"ews/appdb"
	"ews/conf"
	"ews/model"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/eliona-smart-building-assistant/go-utils/log"
	"github.com/gorilla/websocket"
	"github.com/volatiletech/null/v8"
)

const clientReference = "ews-app"

type client struct {
	BaseURL string
}

func NewClient(baseURL string) *client {
	return &client{
		BaseURL: baseURL,
	}
}

func (c *client) Book(bookings []model.Booking) error {
	bookingsMap := groupBookingsByID(bookings)
	for _, b := range bookingsMap {
		randomBooking := b[0]
		convertedBooking := bookingRequest{
			BookingID:   randomBooking.ElionaID,
			AssetIds:    randomBooking.AssetIDs,
			OrganizerID: randomBooking.OrganizerEmail,
			Start:       randomBooking.Start,
			End:         randomBooking.End,
		}
		responseBooking, err := c.book(convertedBooking)
		if err != nil {
			return err
		}
		for _, specificEvent := range b {
			if err := conf.UpsertBooking(appdb.Booking{
				ExchangeID:      null.StringFrom(specificEvent.ExchangeIDInResourceMailbox),
				ExchangeUID:     null.StringFrom(specificEvent.ExchangeUID),
				ExchangeMailbox: null.StringFrom(specificEvent.OrganizerEmail),
				BookingID:       null.Int32From(responseBooking.Id),
			}); err != nil {
				return fmt.Errorf("upserting booking id: %v", err)
			}
		}
	}
	return nil
}

// TODO: This is weird. We need better data structures to distinguish bookings and events.
func groupBookingsByID(bookings []model.Booking) map[int32][]model.Booking {
	bookingMap := make(map[int32][]model.Booking)

	for _, booking := range bookings {
		// Create a copy of the booking to avoid reference issues
		copyOfBooking := booking
		bookingMap[booking.ElionaID] = append(bookingMap[booking.ElionaID], copyOfBooking)
	}

	return bookingMap
}

type bookingRequest struct {
	BookingID   int32     `json:"bookingID"`
	AssetIds    []int32   `json:"assetIds"`
	OrganizerID string    `json:"organizerID"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
}

type bookingResponse struct {
	Id            int32     `json:"id"`
	AssetIds      []int32   `json:"assetIds"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	OrganizerID   string    `json:"organizerID"`
	OrganizerName string    `json:"organizerName"`
}

func (c *client) book(bookings bookingRequest) (bookingResponse, error) {
	body, err := json.Marshal(bookings)
	if err != nil {
		return bookingResponse{}, err
	}

	v := url.Values{}
	v.Add("clientReference", clientReference)
	resp, err := http.Post(c.BaseURL+"/bookings?"+v.Encode(), "application/json", bytes.NewBuffer(body))
	if err != nil {
		return bookingResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			// Handle error, possibly return an error indicating the body could not be read
			return bookingResponse{}, fmt.Errorf("error %v returned: failed to read response body: %v", resp.StatusCode, err)
		}
		return bookingResponse{}, fmt.Errorf("unexpected status code %d: %v", resp.StatusCode, string(bodyBytes))
	}

	var respBody bookingResponse
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return bookingResponse{}, fmt.Errorf("error parsing response body: %v", err)
	}

	return respBody, nil
}

func (c *client) CancelSlice(bookings []model.Booking) error {
	for _, b := range bookings {
		err := c.Cancel(b.ElionaID, "cancelled")
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *client) Cancel(elionaID int32, reason string) error {
	v := url.Values{}
	v.Add("clientReference", clientReference)
	v.Add("reason", reason)
	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/bookings/%v?%s", c.BaseURL, elionaID, v.Encode()), nil)
	if err != nil {
		return err
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	} else if resp.StatusCode == http.StatusNotFound {
		log.Error("booking", "booking %v not found while cancelling", elionaID)
	} else {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("error %v returned: failed to read response body: %v", resp.StatusCode, err)
		}
		return fmt.Errorf("unexpected status code %d: %v", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

type BookingsSubscriptionRequest struct {
	AssetIDs        []int  `json:"assetIDs"`
	ClientReference string `json:"clientReference"`
}

func (c *client) subscribeBookings(assetIDs []int) (*websocket.Conn, error) {
	wsURL := "ws" + c.BaseURL[len("http"):]

	conn, _, err := websocket.DefaultDialer.Dial(wsURL+"/sync/bookings-subscription", nil)
	if err != nil {
		return nil, err
	}

	subscriptionRequest := BookingsSubscriptionRequest{AssetIDs: assetIDs, ClientReference: clientReference}
	if err := conn.WriteJSON(subscriptionRequest); err != nil {
		conn.Close()
		return nil, err
	}

	return conn, nil
}

type Booking struct {
	ID          int32
	AssetIds    []int32   `json:"assetIds"`
	OrganizerID string    `json:"organizerID"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Cancelled   bool      `json:"cancelled"`
}

func (c *client) ListenForBookings(ctx context.Context, assetIDs []int) (<-chan model.Booking, error) {
	conn, err := c.subscribeBookings(assetIDs)
	if err != nil {
		return nil, err
	}
	log.Debug("eliona-booking", "Subscribed")
	bookingsChan := make(chan model.Booking)

	go func() {
		defer close(bookingsChan)
		defer conn.Close()

		for {
			message, err := func() ([]byte, error) {
				done := make(chan struct{})
				var message []byte
				var err error
				go func() {
					defer close(done)
					_, message, err = conn.ReadMessage()
				}()

				// Wait for message read, context cancellation, or a timeout
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-done:
					return message, err
				}
			}()
			if errors.Is(err, context.Canceled) {
				return
			}
			if err != nil {
				log.Error("eliona-booking", "Error reading from WebSocket: %v", err)
				return
			}

			var booking Booking
			err = json.Unmarshal(message, &booking)
			if err != nil {
				log.Error("eliona-booking", "Error unmarshaling booking: %v", err)
				continue // Skip this message and continue listening
			}

			if len(booking.AssetIds) != 1 {
				log.Error("eliona-booking", "The request contains %v != 1 assetIDs, which is currently unsupported.", len(booking.AssetIds))
				continue
			}

			bookingsChan <- model.Booking{
				ElionaID:       booking.ID,
				AssetIDs:       booking.AssetIds,
				OrganizerEmail: booking.OrganizerID,
				Start:          booking.Start,
				End:            booking.End,
				Cancelled:      booking.Cancelled,
			}
		}
	}()

	return bookingsChan, nil
}
