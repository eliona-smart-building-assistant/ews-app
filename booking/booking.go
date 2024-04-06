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
	"net/http"
	"time"

	"github.com/eliona-smart-building-assistant/go-utils/log"
	"github.com/gorilla/websocket"
	"github.com/volatiletech/null/v8"
)

type client struct {
	BaseURL string
}

func NewClient(baseURL string) *client {
	return &client{
		BaseURL: baseURL,
	}
}

func (c *client) Book(bookings []model.Booking) error {
	for _, b := range bookings {
		convertedBooking := bookingRequest{
			BookingID:   b.ElionaID,
			AssetIds:    []int{int(b.AssetID)},
			OrganizerID: b.OrganizerEmail,
			Start:       b.Start,
			End:         b.End,
		}
		responseBooking, err := c.book(convertedBooking)
		if err != nil {
			return err
		}
		if err := conf.UpsertBooking(appdb.Booking{
			ExchangeID:        null.StringFrom(b.ExchangeID),
			ExchangeChangeKey: null.StringFrom(b.ExchangeChangeKey),
			BookingID:         null.Int32From(responseBooking.Id),
		}); err != nil {
			return fmt.Errorf("upserting booking id: %v", err)
		}
	}
	return nil
}

type bookingRequest struct {
	BookingID   int32     `json:"bookingID"`
	AssetIds    []int     `json:"assetIds"`
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

	resp, err := http.Post(c.BaseURL+"/sync/bookings", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return bookingResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return bookingResponse{}, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var respBody bookingResponse
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return bookingResponse{}, fmt.Errorf("error parsing response body: %v", err)
	}

	return respBody, nil
}

type BookingsSubscriptionRequest struct {
	AssetIDs []int `json:"assetIDs"`
}

func (c *client) subscribeBookings(assetIDs []int) (*websocket.Conn, error) {
	wsURL := "ws" + c.BaseURL[len("http"):]

	conn, _, err := websocket.DefaultDialer.Dial(wsURL+"/sync/bookings-subscription", nil)
	if err != nil {
		return nil, err
	}

	subscriptionRequest := BookingsSubscriptionRequest{AssetIDs: assetIDs}
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
				AssetID:        booking.AssetIds[0],
				OrganizerEmail: booking.OrganizerID,
				Start:          booking.Start,
				End:            booking.End,
			}
		}
	}()

	return bookingsChan, nil
}
