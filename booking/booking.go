package booking

import (
	"bytes"
	"encoding/json"
	"ews/model"
	"fmt"
	"net/http"
	"time"

	"github.com/eliona-smart-building-assistant/go-utils/log"
	"github.com/gorilla/websocket"
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
	var convertedBookings []bookingRequest
	for _, b := range bookings {
		convertedBookings = append(convertedBookings, bookingRequest{
			AssetIds:    []int{1}, // todo: assetIDs
			OrganizerID: b.OrganizerEmail,
			Start:       b.Start,
			End:         b.End,
		})
	}
	return c.book(convertedBookings)
}

type bookingRequest struct {
	AssetIds    []int     `json:"assetIds"`
	OrganizerID string    `json:"organizerID"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
}

func (c *client) book(bookings []bookingRequest) error {
	body, err := json.Marshal(bookings)
	if err != nil {
		return err
	}

	resp, err := http.Post(c.BaseURL+"/sync/bookings", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
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
	AssetIds    []int  `json:"assetIds"`
	OrganizerID string `json:"organizerID"`
	Start       string `json:"start"`
	End         string `json:"end"`
	EventName   string `json:"eventName,omitempty"`
	Description string `json:"description,omitempty"`
}

func (c *client) ListenForBookings(assetIDs []int) (<-chan Booking, error) {
	conn, err := c.subscribeBookings(assetIDs)
	if err != nil {
		return nil, err
	}
	log.Debug("eliona-booking", "Subscribed")
	bookingsChan := make(chan Booking)

	go func() {
		defer close(bookingsChan)
		defer conn.Close()

		for {
			_, message, err := conn.ReadMessage()
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

			bookingsChan <- booking
		}
	}()

	return bookingsChan, nil
}
