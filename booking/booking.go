package booking

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"ews/conf"
	syncmodel "ews/model/sync"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/eliona-smart-building-assistant/go-utils/log"
	"github.com/gorilla/websocket"
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
		return bookingResponse{}, fmt.Errorf(resp.Status)
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

func (c *client) Book(groups map[string]syncmodel.BookingGroup) error {
	for _, group := range groups {
		var convertedBookings []bookingRequest
		for _, booking := range group.Occurrences {
			convertedBookings = append(convertedBookings, bookingRequest{
				BookingID:   booking.ElionaID,
				AssetIds:    booking.GetAssetIDs(),
				OrganizerID: group.OrganizerEmail,
				Start:       booking.Start,
				End:         booking.End,
				Cancelled:   booking.Cancelled,
			})
		}
		convertedGroup := bookingGroupRequest{
			GroupID:     group.ElionaID,
			Occurrences: convertedBookings,
		}
		responseGroup, err := c.book(convertedGroup)
		if err != nil {
			return err
		}
		group.ElionaID = responseGroup.Id
		for i, responseBooking := range responseGroup.Bookings {
			// This works because the order of the bookings in response is kept
			// same as in the request.
			group.Occurrences[i].ElionaID = responseBooking.Id
		}

		if err := conf.UpsertBooking(group); err != nil {
			return fmt.Errorf("upserting group id %v: %v", group.ElionaID, err)
		}
	}
	return nil
}

type bookingGroupRequest struct {
	GroupID     int32            `json:"groupID,omitempty"`
	Occurrences []bookingRequest `json:"occurrences"`
}

type bookingRequest struct {
	BookingID   int32     `json:"bookingID"`
	AssetIds    []int32   `json:"assetIds"`
	OrganizerID string    `json:"organizerID"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Cancelled   bool      `json:"cancelled"`
}

type bookingGroupResponse struct {
	Id       int32             `json:"id"`
	Bookings []bookingResponse `json:"bookings"`
}

type bookingResponse struct {
	Id            int32     `json:"id"`
	AssetIds      []int32   `json:"assetIds"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	OrganizerID   string    `json:"organizerID"`
	OrganizerName string    `json:"organizerName"`
}

func (c *client) book(bookings bookingGroupRequest) (bookingGroupResponse, error) {
	body, err := json.Marshal(bookings)
	if err != nil {
		return bookingGroupResponse{}, err
	}

	v := url.Values{}
	v.Add("clientReference", clientReference)
	resp, err := http.Post(c.BaseURL+"/bookings/group?"+v.Encode(), "application/json", bytes.NewBuffer(body))
	if err != nil {
		return bookingGroupResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return bookingGroupResponse{}, fmt.Errorf("error %v returned: failed to read response body: %v", resp.StatusCode, err)
		}
		return bookingGroupResponse{}, fmt.Errorf("unexpected status code %d: %v", resp.StatusCode, string(bodyBytes))
	}

	var respBody bookingGroupResponse
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return bookingGroupResponse{}, fmt.Errorf("error parsing response body: %v", err)
	}

	return respBody, nil
}

func (c *client) CancelSlice(bookings []syncmodel.RoomBooking) error {
	for _, b := range bookings {
		if b.BookingOccurrence == nil {
			return fmt.Errorf("unifiedBooking is nil")
		}
		elionaBooking, err := c.get(b.BookingOccurrence.ElionaID)
		if err != nil {
			return fmt.Errorf("getting eliona booking for id %v: %v", b.BookingOccurrence.ElionaID, err)
		}
		elionaBooking.AssetIds = removeElement(elionaBooking.AssetIds, b.AssetID)
		if len(elionaBooking.AssetIds) != 0 {
			// We don't want to cancel the whole event in Eliona when just part of the rooms are removed from the event.
			_, err := c.book(bookingGroupRequest{
				Occurrences: []bookingRequest{
					{
						BookingID:   elionaBooking.Id,
						Start:       elionaBooking.Start,
						End:         elionaBooking.End,
						AssetIds:    elionaBooking.AssetIds,
						OrganizerID: elionaBooking.OrganizerID,
					},
				},
			})
			if err != nil {
				return fmt.Errorf("updating booking %v: %v", elionaBooking.Id, err)
			}
		} else {
			err := c.Cancel(b.BookingOccurrence.ElionaID, "cancelled")
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

type BookingGroup struct {
	Id       int32     `json:"id,omitempty"`
	Bookings []Booking `json:"bookings,omitempty"`
}

type Booking struct {
	ID          int32
	AssetIds    []int32   `json:"assetIds"`
	OrganizerID string    `json:"organizerID"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Cancelled   bool      `json:"cancelled"`
}

func (c *client) ListenForBookings(ctx context.Context, assetIDs []int) (<-chan syncmodel.BookingGroup, error) {
	conn, err := c.subscribeBookings(assetIDs)
	if err != nil {
		return nil, err
	}
	log.Debug("eliona-booking", "Subscribed")
	bookingsChan := make(chan syncmodel.BookingGroup)

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

			var bookingGroup BookingGroup
			if err = json.Unmarshal(message, &bookingGroup); err != nil {
				log.Error("eliona-booking", "Error unmarshaling bookingGroup: %v", err)
				continue // Skip this message and continue listening
			}

			organizer := ""
			occurrences := make([]syncmodel.BookingOccurrence, 0, len(bookingGroup.Bookings))
			for _, booking := range bookingGroup.Bookings {
				roomBookings := make([]syncmodel.RoomBooking, len(booking.AssetIds))
				for i, assetID := range booking.AssetIds {
					roomBookings[i] = syncmodel.RoomBooking{
						AssetID: assetID,
					}
				}
				occurrences = append(occurrences, syncmodel.BookingOccurrence{
					ElionaID:     booking.ID,
					RoomBookings: roomBookings,
					Start:        booking.Start,
					End:          booking.End,
					Cancelled:    booking.Cancelled,
				})
				if organizer == "" {
					organizer = booking.OrganizerID
				} else if organizer != booking.OrganizerID {
					log.Error("eliona-booking", "received booking group with different organizers. A: %s B: %s", organizer, booking.OrganizerID)
					continue
				}
			}
			bookingsChan <- syncmodel.BookingGroup{
				ElionaID:       bookingGroup.Id,
				Occurrences:    occurrences,
				OrganizerEmail: organizer,
			}
		}
	}()

	return bookingsChan, nil
}
