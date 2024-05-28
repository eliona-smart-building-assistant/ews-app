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

func (c *client) get(elionaID int32) (bookingResponse, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/bookings/%v", c.BaseURL, elionaID), nil)
	if err != nil {
		return bookingResponse{}, err
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return bookingResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		log.Error("booking", "booking %v not found while cancelling", elionaID)
	} else if resp.StatusCode != http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
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

func (c *client) Book(bookings map[string]model.UnifiedBooking) error {
	for _, booking := range bookings {
		convertedBooking := bookingRequest{
			BookingID:   booking.ElionaID,
			AssetIds:    booking.GetAssetIDs(),
			OrganizerID: booking.OrganizerEmail,
			Start:       booking.Start,
			End:         booking.End,
		}
		responseBooking, err := c.book(convertedBooking)
		if err != nil {
			return err
		}

		dbUB := appdb.UnifiedBooking{
			ExchangeUID:              null.StringFrom(booking.ExchangeUID),
			ExchangeOrganizerMailbox: null.StringFrom(booking.OrganizerEmail),
			BookingID:                null.Int32From(responseBooking.Id),
		}
		var dbRoomBookings []appdb.RoomBooking
		for _, specificEvent := range booking.RoomBookings {
			dbRoomBookings = append(dbRoomBookings, appdb.RoomBooking{
				ExchangeID: null.StringFrom(specificEvent.ExchangeIDInResourceMailbox),
			})
		}
		if err := conf.UpsertBooking(dbUB, dbRoomBookings); err != nil {
			return fmt.Errorf("upserting booking id %v: %v", dbUB.BookingID, err)
		}
	}
	return nil
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

func (c *client) CancelSlice(bookings []model.RoomBooking) error {
	for _, b := range bookings {
		if b.UnifiedBooking == nil {
			return fmt.Errorf("unifiedBooking is nil")
		}
		elionaBooking, err := c.get(b.UnifiedBooking.ElionaID)
		if err != nil {
			return fmt.Errorf("getting eliona booking for id %v: %v", b.UnifiedBooking.ElionaID, err)
		}
		elionaBooking.AssetIds = removeElement(elionaBooking.AssetIds, b.AssetID)
		if len(elionaBooking.AssetIds) != 0 {
			// We don't want to cancel the whole event in Eliona when just part of the rooms are removed from the event.
			_, err := c.book(bookingRequest{
				BookingID:   elionaBooking.Id,
				Start:       elionaBooking.Start,
				End:         elionaBooking.End,
				AssetIds:    elionaBooking.AssetIds,
				OrganizerID: elionaBooking.OrganizerID,
			})
			if err != nil {
				return fmt.Errorf("updating booking %v: %v", elionaBooking.Id, err)
			}
		} else {
			err := c.Cancel(b.UnifiedBooking.ElionaID, "cancelled")
			if err != nil {
				return fmt.Errorf("cancelling booking %v: %v", elionaBooking.Id, err)
			}
		}
	}
	return nil
}

func removeElement(slice []int32, element int32) []int32 {
	for i, v := range slice {
		if v == element {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
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

func (c *client) ListenForBookings(ctx context.Context, assetIDs []int) (<-chan model.UnifiedBooking, error) {
	conn, err := c.subscribeBookings(assetIDs)
	if err != nil {
		return nil, err
	}
	log.Debug("eliona-booking", "Subscribed")
	bookingsChan := make(chan model.UnifiedBooking)

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

			roomBookings := make([]model.RoomBooking, len(booking.AssetIds))
			for i, assetID := range booking.AssetIds {
				roomBookings[i] = model.RoomBooking{
					AssetID: assetID,
				}
			}
			bookingsChan <- model.UnifiedBooking{
				ElionaID:       booking.ID,
				RoomBookings:   roomBookings,
				OrganizerEmail: booking.OrganizerID,
				Start:          booking.Start,
				End:            booking.End,
				Cancelled:      booking.Cancelled,
			}
		}
	}()

	return bookingsChan, nil
}
