//  This file is part of the eliona project.
//  Copyright © 2022 LEICOM iTEC AG. All Rights Reserved.
//  ______ _ _
// |  ____| (_)
// | |__  | |_  ___  _ __   __ _
// |  __| | | |/ _ \| '_ \ / _` |
// | |____| | | (_) | | | | (_| |
// |______|_|_|\___/|_| |_|\__,_|
//
//  THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING
//  BUT NOT LIMITED  TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
//  NON INFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM,
//  DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
//  OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

package ews

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"ews/apiserver"
	"ews/model"
	syncmodel "ews/model/sync"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/go-ntlmssp"
	"github.com/eliona-smart-building-assistant/go-utils/log"
	"golang.org/x/oauth2/clientcredentials"
)

var ErrDeclined = errors.New("resource has declined invitation")
var ErrNonExistentMailbox = errors.New("the SMTP address has no mailbox associated with it within this Exchange server")

var errNotFound = errors.New("entity not found")

type EWSHelper struct {
	Client       *http.Client
	EwsURL       string
	username     string
	password     string
	serviceUser  string
	addressCache map[string]string
}

// NewEWSHelper creates a new instance of EWSHelper with OAuth or NTLM authentication based on the provided configuration
func NewEWSHelper(config apiserver.Configuration, impersonationUser string) *EWSHelper {
	var httpClient *http.Client
	var ewsURL string
	var username, password string

	if filled(config.ClientId) && filled(config.ClientSecret) && filled(config.TenantId) {
		// Use OAuth
		oauth2Config := clientcredentials.Config{
			ClientID:     *config.ClientId,
			ClientSecret: *config.ClientSecret,
			TokenURL:     fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", *config.TenantId),
			Scopes:       []string{"https://outlook.office365.com/.default"},
		}
		httpClient = oauth2Config.Client(context.Background())
		ewsURL = "https://outlook.office365.com/EWS/Exchange.asmx"
	} else if filled(config.Username) && filled(config.Password) && filled(config.EwsURL) {
		// Use NTLM
		httpClient = &http.Client{
			Transport: ntlmssp.Negotiator{
				RoundTripper: &http.Transport{},
			},
		}
		ewsURL = *config.EwsURL
		username = *config.Username
		password = *config.Password
	} else {
		panic("Invalid configuration: either OAuth or NTLM credentials must be provided")
	}

	return &EWSHelper{
		Client:       httpClient,
		EwsURL:       ewsURL,
		username:     username,
		password:     password,
		serviceUser:  impersonationUser,
		addressCache: make(map[string]string),
	}
}

func filled(s *string) bool {
	return s != nil && *s != ""
}

// sendRequest sends an HTTP request with the specified XML body and returns the response body
func (h *EWSHelper) sendRequest(xmlBody string) ([]byte, error) {
	request, err := http.NewRequest("POST", h.EwsURL, bytes.NewBufferString(xmlBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	request.Header.Add("Content-Type", "text/xml; charset=utf-8")
	if h.username != "" && h.password != "" {
		request.SetBasicAuth(h.username, h.password) // Needed for NTLM
	}

	response, err := h.Client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	return responseBody, nil
}

type soapFault struct {
	Body struct {
		Fault struct {
			FaultCode   string `xml:"faultcode"`
			FaultString string `xml:"faultstring"`
			Detail      struct {
				ResponseCode string `xml:"ResponseCode"`
				Message      string `xml:"Message"`
			} `xml:"detail"`
		} `xml:"Fault"`
	} `xml:"Body"`
}

type roomsEnvelope struct {
	XMLName xml.Name  `xml:"Envelope"`
	Body    roomsBody `xml:"Body"`
}

type roomsBody struct {
	GetRoomsResponse getRoomsResponse `xml:"GetRoomsResponse"`
}

type getRoomsResponse struct {
	ResponseClass string `xml:"ResponseClass,attr"`
	ResponseCode  string `xml:"ResponseCode"`
	Rooms         rooms  `xml:"Rooms"`
}

type rooms struct {
	Rooms []room `xml:"Room"`
}

type room struct {
	Id roomId `xml:"Id"`
}

type roomId struct {
	Name         string `xml:"Name"`
	EmailAddress string `xml:"EmailAddress"`
	// RoutingType  string `xml:"RoutingType"`
	// MailboxType  string `xml:"MailboxType"`
}

func (h *EWSHelper) GetAssets(config apiserver.Configuration) (model.Root, error) {
	// We might fetch also all room lists and include them into asset tree, but
	// one room might belong to multiple room lists, which would make full
	// Eliona mapping impossible. So let's give the user opprotunity to specify
	// one room list to be synced from Exchange to Eliona.
	requestXML := fmt.Sprintf(`
<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"
                  xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"
                  xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages">
    <soapenv:Header>
        <t:RequestServerVersion Version="Exchange2013_SP1"/>
        <t:ExchangeImpersonation>
            <t:ConnectingSID>
                <t:PrincipalName>%s</t:PrincipalName>
            </t:ConnectingSID>
        </t:ExchangeImpersonation>
    </soapenv:Header>
    <soapenv:Body>
        <m:GetRooms>
            <m:RoomList>
                <t:EmailAddress>%s</t:EmailAddress>
           </m:RoomList>
        </m:GetRooms>
    </soapenv:Body>
</soapenv:Envelope>
`, h.serviceUser, *config.RoomListUPN)
	responseXML, err := h.sendRequest(requestXML)
	if err != nil {
		return model.Root{}, fmt.Errorf("requesting rooms: %v", err)
	}

	var env roomsEnvelope
	if err := xml.Unmarshal(responseXML, &env); err != nil {
		return model.Root{}, fmt.Errorf("unmarshaling XML: %v", err)
	}

	xmlRooms := env.Body.GetRoomsResponse.Rooms.Rooms
	modelRooms := make([]model.Room, 0, len(xmlRooms))
	for _, room := range xmlRooms {
		modelRooms = append(modelRooms, model.Room{
			Email:  room.Id.EmailAddress,
			Name:   room.Id.Name,
			Config: config,
		})
	}
	return model.Root{
		Rooms:  modelRooms,
		Config: config,
	}, nil
}

type roomEventsEnvelope struct {
	XMLName xml.Name       `xml:"Envelope"`
	Body    roomEventsBody `xml:"Body"`
}

type roomEventsBody struct {
	SyncFolderItemsResponse syncFolderItemsResponse `xml:"SyncFolderItemsResponse"`
}

type syncFolderItemsResponse struct {
	ResponseMessages responseMessages `xml:"ResponseMessages"`
}

type responseMessages struct {
	SyncFolderItemsResponseMessage syncFolderItemsResponseMessage `xml:"SyncFolderItemsResponseMessage"`
}

type syncFolderItemsResponseMessage struct {
	SyncState               string  `xml:"SyncState"`
	IncludesLastItemInRange bool    `xml:"IncludesLastItemInRange"`
	Changes                 changes `xml:"Changes"`
}

type changes struct {
	Create []createOrUpdate `xml:"Create"`
	Update []createOrUpdate `xml:"Update"`
	Delete []delete         `xml:"Delete"`
}

type createOrUpdate struct {
	CalendarItem *calendarItem `xml:"CalendarItem"`
}

type delete struct {
	ItemId itemId `xml:"ItemId"`
}

type calendarItem struct {
	ItemId           itemId `xml:"ItemId"`
	UID              string `xml:"UID"`
	InstanceIndex    int
	Subject          string    `xml:"Subject"`
	DateTimeReceived string    `xml:"DateTimeReceived"`
	Start            time.Time `xml:"Start"`
	End              time.Time `xml:"End"`
	Organizer        organizer `xml:"Organizer"`
	CalendarItemType string    `xml:"CalendarItemType"`
}

type itemId struct {
	Id        string `xml:"Id,attr"`        // Persistent
	ChangeKey string `xml:"ChangeKey,attr"` // Essentially a hash to notice changes
}

type organizer struct {
	Mailbox mailbox `xml:"Mailbox"`
}

type mailbox struct {
	Name         string `xml:"Name"`
	EmailAddress string `xml:"EmailAddress"` // This might be either email address, or Legacy DN.
}

func (h *EWSHelper) GetRoomAppointments(assetID int32, roomEmail string, syncState string) (new []syncmodel.BookingGroup, updated []syncmodel.BookingGroup, cancelled []string, newSyncState string, err error) {
	// Every synchronization, we will get a list of Create, Update and Delete events (and some cruft
	// amongst it). When there is no SyncState, we will get only Create events for all events
	// present on server. If that happens to be a lot of events, these will be created over time by
	// chunks of MaxChangesReturned until IncludesLastItemInRange will be true.
	requestXML := fmt.Sprintf(`
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types" xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages">
    <soap:Header>
        <t:RequestServerVersion Version="Exchange2013_SP1"/>
        <t:ExchangeImpersonation>
            <t:ConnectingSID>
                <t:SmtpAddress>%s</t:SmtpAddress>
            </t:ConnectingSID>
        </t:ExchangeImpersonation>
    </soap:Header>
    <soap:Body>
        <m:SyncFolderItems>
            <m:ItemShape>
                <t:BaseShape>IdOnly</t:BaseShape>
                <t:AdditionalProperties>
                    <t:FieldURI FieldURI="calendar:UID"/>
                    <t:FieldURI FieldURI="item:Subject"/>
                    <t:FieldURI FieldURI="item:DateTimeReceived"/>
                    <t:FieldURI FieldURI="calendar:Start"/>
                    <t:FieldURI FieldURI="calendar:End"/>
                    <t:FieldURI FieldURI="calendar:Organizer"/>
                    <t:FieldURI FieldURI="calendar:CalendarItemType"/>
                </t:AdditionalProperties>
            </m:ItemShape>
            <m:SyncFolderId>
                <t:DistinguishedFolderId Id="calendar">
                    <t:Mailbox>
                        <t:EmailAddress>%s</t:EmailAddress>
                    </t:Mailbox>
                </t:DistinguishedFolderId>
            </m:SyncFolderId>
            <m:SyncState>%s</m:SyncState>
            <m:MaxChangesReturned>256</m:MaxChangesReturned>
        </m:SyncFolderItems>
    </soap:Body>
</soap:Envelope>`, roomEmail, roomEmail, syncState)
	responseXML, err := h.sendRequest(requestXML)
	if err != nil {
		return nil, nil, nil, syncState, fmt.Errorf("getting room %v appointments: %v", roomEmail, err)
	}

	// First, try to unmarshal into SOAPFault to see if there was an error.
	var soapFault soapFault
	if err := xml.Unmarshal(responseXML, &soapFault); err == nil && soapFault.Body.Fault.FaultCode != "" {
		return nil, nil, nil, syncState, fmt.Errorf("SOAP fault: %s - %s", soapFault.Body.Fault.Detail.ResponseCode, soapFault.Body.Fault.Detail.Message)
	}

	var env roomEventsEnvelope
	if err := xml.Unmarshal(responseXML, &env); err != nil {
		return nil, nil, nil, syncState, fmt.Errorf("unmarshaling XML: %v", err)
	}
	changes := env.Body.SyncFolderItemsResponse.ResponseMessages.SyncFolderItemsResponseMessage.Changes
	for _, change := range changes.Create {
		if err := change.checkItem(); err != nil {
			log.Debug("ews", "skipped creating calendar item: %v", err)
			continue
		}
		item := change.CalendarItem
		organizerEmail, err := h.resolveDN(item.Organizer.Mailbox.EmailAddress)
		if err != nil {
			return nil, nil, nil, syncState, fmt.Errorf("resolving distinguished name '%s': %v", item.Organizer.Mailbox.EmailAddress, err)
		}

		items := []calendarItem{*item}
		if change.CalendarItem.CalendarItemType == "RecurringMaster" {
			recurringItems, err := h.expandRecurrence(item.ItemId.Id, roomEmail)
			if err != nil {
				return nil, nil, nil, syncState, fmt.Errorf("expanding recurrence for event %v: %v", item.ItemId.Id, err)
			}
			items = recurringItems // RecurringMaster is a redundant occurence, only the "Occurence"s should be booked
		}

		group := syncmodel.BookingGroup{
			ExchangeUID:    item.UID,
			OrganizerEmail: organizerEmail,
		}
		for _, item := range items {
			group.Occurrences = append(group.Occurrences, syncmodel.BookingOccurrence{
				InstanceIndex: item.InstanceIndex,
				Start:         item.Start,
				End:           item.End,
				Cancelled:     false,
				RoomBookings: []syncmodel.RoomBooking{{
					ExchangeIDInResourceMailbox: item.ItemId.Id,
					AssetID:                     assetID,
				}},
			})
		}

		new = append(new, group)
	}

	for _, change := range changes.Update {
		if err := change.checkItem(); err != nil {
			log.Debug("ews", "skipped updating calendar item: %v", err)
			continue
		}
		item := change.CalendarItem
		organizerEmail, err := h.resolveDN(item.Organizer.Mailbox.EmailAddress)
		if err != nil {
			return nil, nil, nil, syncState, fmt.Errorf("resolving distinguished name '%s': %v", item.Organizer.Mailbox.EmailAddress, err)
		}

		items := []calendarItem{*item}
		if change.CalendarItem.CalendarItemType == "RecurringMaster" {
			recurringItems, err := h.expandRecurrence(item.ItemId.Id, roomEmail)
			if err != nil {
				return nil, nil, nil, syncState, fmt.Errorf("expanding recurrence for event %v: %v", item.ItemId.Id, err)
			}
			items = recurringItems // RecurringMaster is a redundant occurence, only the "Occurence"s should be booked
		}

		group := syncmodel.BookingGroup{
			ExchangeUID:    item.UID,
			OrganizerEmail: organizerEmail,
		}
		for _, item := range items {
			group.Occurrences = append(group.Occurrences, syncmodel.BookingOccurrence{
				InstanceIndex: item.InstanceIndex,
				Start:         item.Start,
				End:           item.End,
				Cancelled:     false,
				RoomBookings: []syncmodel.RoomBooking{{
					ExchangeIDInResourceMailbox: item.ItemId.Id,
					AssetID:                     assetID,
				}},
			})
		}
		updated = append(updated, group)
	}

	for _, change := range changes.Delete {
		cancelled = append(cancelled, change.ItemId.Id)
	}

	newSyncState = env.Body.SyncFolderItemsResponse.ResponseMessages.SyncFolderItemsResponseMessage.SyncState

	return new, updated, cancelled, newSyncState, nil
}

func (cr createOrUpdate) checkItem() error {
	// Sometimes we can get information about non-calendarItems as well, like:
	//
	// <t:Create>
	// 	<t:Message>
	// 		<t:ItemId Id="AAMkADA0YjRlMDBiLTI5ZWQtNDhiYS1iYTRhLTU1NDcxMDA1YjlhZQBGAAAAAAD7L6rZT1EWT4zA7nKhCR2gBwA2JJ7TGEwETLI+6ZT89YaJAAAAAAENAAA2JJ7TGEwETLI+6ZT89YaJAAAGDBN1AAA=" ChangeKey="CQAAABYAAAA2JJ7TGEwETLI+6ZT89YaJAAAGC9oG" />
	// 		<t:Subject>Let's go for lunch</t:Subject>
	// 		<t:DateTimeReceived>2023-03-22T13:04:13Z</t:DateTimeReceived>
	// 	</t:Message>
	// </t:Create>
	//
	// Such entry has no value and we cannot process it.
	if cr.CalendarItem == nil {
		return fmt.Errorf("not a calendar item")
	}
	if cr.CalendarItem.Start.IsZero() || cr.CalendarItem.End.IsZero() {
		return fmt.Errorf("item %v has no start or end", cr.CalendarItem.ItemId.Id)
	}
	if cr.CalendarItem.Organizer.Mailbox.EmailAddress == "" {
		return fmt.Errorf("item %v has no organizer", cr.CalendarItem.ItemId.Id)
	}
	return nil
}

func (h *EWSHelper) expandRecurrence(eventID, roomEmail string) ([]calendarItem, error) {
	var items []calendarItem
	instanceIndex := 0

	for {
		instanceIndex++ // Starts with 1
		requestXML := fmt.Sprintf(`
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types" xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages">
    <soap:Header>
        <t:RequestServerVersion Version="Exchange2013_SP1"/>
        <t:ExchangeImpersonation>
            <t:ConnectingSID>
                <t:SmtpAddress>%s</t:SmtpAddress>
            </t:ConnectingSID>
        </t:ExchangeImpersonation>
    </soap:Header>
    <soap:Body>
        <m:GetItem>
            <m:ItemShape>
                <t:BaseShape>IdOnly</t:BaseShape>
                <t:AdditionalProperties>
                    <t:FieldURI FieldURI="calendar:UID"/>
                    <t:FieldURI FieldURI="item:Subject"/>
                    <t:FieldURI FieldURI="item:DateTimeReceived"/>
                    <t:FieldURI FieldURI="calendar:Start"/>
                    <t:FieldURI FieldURI="calendar:End"/>
                    <t:FieldURI FieldURI="calendar:Organizer"/>
                    <t:FieldURI FieldURI="calendar:CalendarItemType"/>
                </t:AdditionalProperties>
            </m:ItemShape>
            <m:ItemIds>
                <t:OccurrenceItemId RecurringMasterId="%s" InstanceIndex="%d" />
            </m:ItemIds>
        </m:GetItem>
    </soap:Body>
</soap:Envelope>`, roomEmail, eventID, instanceIndex)

		responseXML, err := h.sendRequest(requestXML)
		if err != nil {
			return nil, fmt.Errorf("expanding recurrence: %v", err)
		}
		// First, try to unmarshal into SOAPFault to see if there was an error.
		var soapFault soapFault
		if err := xml.Unmarshal(responseXML, &soapFault); err == nil && soapFault.Body.Fault.FaultCode != "" {
			return nil, fmt.Errorf("SOAP fault: %s - %s", soapFault.Body.Fault.Detail.ResponseCode, soapFault.Body.Fault.Detail.Message)
		}

		var response struct {
			Body struct {
				GetItemResponse struct {
					ResponseMessages struct {
						GetItemResponseMessage struct {
							ResponseClass string `xml:"ResponseClass,attr"`
							ResponseCode  string `xml:"ResponseCode"`
							Items         struct {
								CalendarItem calendarItem `xml:"CalendarItem"`
							} `xml:"Items"`
						} `xml:"GetItemResponseMessage"`
					} `xml:"ResponseMessages"`
				} `xml:"GetItemResponse"`
			} `xml:"Body"`
		}
		if err := xml.Unmarshal(responseXML, &response); err != nil {
			return nil, fmt.Errorf("unmarshaling XML: %v", err)
		}

		rm := response.Body.GetItemResponse.ResponseMessages.GetItemResponseMessage
		if rm.ResponseCode == "ErrorCalendarOccurrenceIndexIsOutOfRecurrenceRange" {
			// End of loop.
			break
		}
		if rm.ResponseCode == "ErrorCalendarOccurrenceIsDeletedFromRecurrence" {
			// This occurence was deleted, skip it.
			continue
		}
		if rm.ResponseClass != "Success" {
			return nil, fmt.Errorf("GetItem failed: %s", rm.ResponseCode)
		}

		item := rm.Items.CalendarItem
		item.InstanceIndex = instanceIndex

		items = append(items, item)
	}

	return items, nil
}

type Appointment struct {
	Organizer string
	Subject   string
	Start     time.Time
	End       time.Time
	Location  string
	Attendees []string
}

func (h *EWSHelper) CreateAppointment(appointment Appointment) (exchangeUID string, resourceEventIDs []string, err error) {
	requestXML := fmt.Sprintf(`
<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"
                  xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"
                  xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages">
    <soapenv:Header>
        <t:RequestServerVersion Version="Exchange2013_SP1"/>
        <t:ExchangeImpersonation>
            <t:ConnectingSID>
                <t:SmtpAddress>%s</t:SmtpAddress>
            </t:ConnectingSID>
        </t:ExchangeImpersonation>
    </soapenv:Header>
    <soapenv:Body>
        <m:CreateItem SendMeetingInvitations="SendToAllAndSaveCopy">
            <m:SavedItemFolderId>
                <t:DistinguishedFolderId Id="calendar"/>
            </m:SavedItemFolderId>
            <m:Items>
                <t:CalendarItem>
                    <t:Subject>%s</t:Subject>
                    <t:Start>%s</t:Start>
                    <t:End>%s</t:End>
                    <t:IsAllDayEvent>false</t:IsAllDayEvent>
                    <t:LegacyFreeBusyStatus>Busy</t:LegacyFreeBusyStatus>
                    <t:Location>%s</t:Location>
                    <t:RequiredAttendees>%s</t:RequiredAttendees>
                </t:CalendarItem>
            </m:Items>
        </m:CreateItem>
    </soapenv:Body>
</soapenv:Envelope>`,
		appointment.Organizer,
		appointment.Subject,
		appointment.Start.Format(time.RFC3339),
		appointment.End.Format(time.RFC3339),
		appointment.Location,
		formatAttendees(appointment.Attendees),
	)

	responseXML, err := h.sendRequest(requestXML)
	if err != nil {
		return "", nil, fmt.Errorf("requesting create appointment: %w", err)
	}

	// First, try to unmarshal into SOAPFault to see if there was an error.
	var soapFault soapFault
	if err := xml.Unmarshal(responseXML, &soapFault); err == nil && soapFault.Body.Fault.FaultCode != "" {
		if soapFault.Body.Fault.Detail.ResponseCode == "ErrorNonExistentMailbox" {
			return "", nil, ErrNonExistentMailbox
		}
		return "", nil, fmt.Errorf("SOAP fault: %s - %s", soapFault.Body.Fault.Detail.ResponseCode, soapFault.Body.Fault.Detail.Message)
	}

	var env appointmentCreated
	if err := xml.Unmarshal(responseXML, &env); err != nil {
		return "", nil, fmt.Errorf("unmarshaling XML: %v", err)
	}

	organizerEventID := env.Body.CreateItemResponse.ResponseMessages.CreateItemResponseMessage.Items.CalendarItem.ItemId.ID

	exchangeUID, err = h.getUIDFromItemId(appointment.Organizer, organizerEventID)
	if err != nil {
		return "", nil, fmt.Errorf("getting UID from ItemID: %v", err)
	}

	// Let's give the server some time to process the invitation. Sometimes it's
	// instant, sometimes 2 seconds aren't enough. This should be long enough
	// time.
	time.Sleep(15 * time.Second)
	for _, attendee := range appointment.Attendees {
		resourceEventID, _, err := h.findEventUIDInMailbox(attendee, exchangeUID)
		if errors.Is(err, errNotFound) {
			// The resource has probably declined the invitation.
			return exchangeUID, nil, ErrDeclined
		} else if err != nil {
			return exchangeUID, nil, fmt.Errorf("finding resource event ID: %v", err)
		}
		resourceEventIDs = append(resourceEventIDs, resourceEventID)
	}

	return exchangeUID, resourceEventIDs, nil
}

func formatAttendees(attendees []string) string {
	var attendeeXML strings.Builder
	for _, email := range attendees {
		attendeeXML.WriteString(fmt.Sprintf(`
            <t:Attendee>
                <t:Mailbox>
                    <t:EmailAddress>%s</t:EmailAddress>
                </t:Mailbox>
            </t:Attendee>`, email))
	}
	return attendeeXML.String()
}

type appointmentCreated struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		CreateItemResponse struct {
			ResponseMessages struct {
				CreateItemResponseMessage struct {
					ResponseClass string `xml:"ResponseClass,attr"`
					ResponseCode  string `xml:"ResponseCode"`
					Items         struct {
						CalendarItem struct {
							ItemId struct {
								ID        string `xml:"Id,attr"`
								ChangeKey string `xml:"ChangeKey,attr"`
							} `xml:"ItemId"`
						} `xml:"CalendarItem"`
					} `xml:"Items"`
				} `xml:"CreateItemResponseMessage"`
			} `xml:"ResponseMessages"`
		} `xml:"CreateItemResponse"`
	} `xml:"Body"`
}

func (h *EWSHelper) CancelEvent(event syncmodel.BookingGroup) error {
	// Find the organizer's eventId and changeKey using the UID
	eventID, changeKey, err := h.findEventUIDInMailbox(event.OrganizerEmail, event.ExchangeUID)
	if err != nil {
		return fmt.Errorf("finding organizer event ID: %v", err)
	}

	requestXML := fmt.Sprintf(`
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types" xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages">
    <soap:Header>
        <t:RequestServerVersion Version="Exchange2013_SP1"/>
        <t:ExchangeImpersonation>
            <t:ConnectingSID>
                <t:SmtpAddress>%s</t:SmtpAddress>
            </t:ConnectingSID>
        </t:ExchangeImpersonation>
    </soap:Header>
    <soap:Body>
    <m:CreateItem MessageDisposition="SendAndSaveCopy">
      <m:Items>
        <t:CancelCalendarItem>
          <t:ReferenceItemId Id="%s" ChangeKey="%s" />
          <t:NewBodyContent BodyType="HTML">Cancelled via Eliona</t:NewBodyContent>
        </t:CancelCalendarItem>
      </m:Items>
    </m:CreateItem>
  </soap:Body>
</soap:Envelope>`, event.OrganizerEmail, eventID, changeKey)

	responseXML, err := h.sendRequest(requestXML)
	if err != nil {
		return fmt.Errorf("requesting cancel event: %w", err)
	}

	// First, try to unmarshal into SOAPFault to see if there was an error.
	var soapFault soapFault
	if err := xml.Unmarshal(responseXML, &soapFault); err == nil && soapFault.Body.Fault.FaultCode != "" {
		if soapFault.Body.Fault.FaultCode == "ErrorNonExistentMailbox" {
			return ErrNonExistentMailbox
		}
		return fmt.Errorf("SOAP fault: %s - %s", soapFault.Body.Fault.Detail.ResponseCode, soapFault.Body.Fault.Detail.Message)
	}

	var response struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			CreateItemResponse struct {
				ResponseMessages struct {
					CreateItemResponseMessage struct {
						ResponseClass string `xml:"ResponseClass,attr"`
						ResponseCode  string `xml:"ResponseCode"`
					} `xml:"CreateItemResponseMessage"`
				} `xml:"ResponseMessages"`
			} `xml:"CreateItemResponse"`
		} `xml:"Body"`
	}

	if err := xml.Unmarshal(responseXML, &response); err != nil {
		return fmt.Errorf("unmarshalling XML: %v", err)
	}

	responseClass := response.Body.CreateItemResponse.ResponseMessages.CreateItemResponseMessage.ResponseClass
	responseCode := response.Body.CreateItemResponse.ResponseMessages.CreateItemResponseMessage.ResponseCode

	if responseClass != "Success" || responseCode != "NoError" {
		return fmt.Errorf("cancelling event resulted in %s - %s. Response: %s", responseClass, responseCode, string(responseXML))
	}

	return nil
}

func (h *EWSHelper) CancelOccurrence(group syncmodel.BookingGroup, occurrence syncmodel.BookingOccurrence) error {
	// Find the organizer's eventId using the UID
	eventID, _, err := h.findEventUIDInMailbox(group.OrganizerEmail, group.ExchangeUID)
	if err != nil {
		return fmt.Errorf("finding organizer event ID: %v", err)
	}

	requestXML := fmt.Sprintf(`
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types" xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages">
  <soap:Header>
      <t:RequestServerVersion Version="Exchange2013_SP1"/>
      <t:ExchangeImpersonation>
          <t:ConnectingSID>
              <t:SmtpAddress>%s</t:SmtpAddress>
          </t:ConnectingSID>
      </t:ExchangeImpersonation>
  </soap:Header>
  <soap:Body>
    <m:DeleteItem DeleteType="MoveToDeletedItems" SendMeetingCancellations="SendToAllAndSaveCopy">
      <m:ItemIds>
        <t:OccurrenceItemId RecurringMasterId="%s" InstanceIndex="%d" />
      </m:ItemIds>
    </m:DeleteItem>
  </soap:Body>
</soap:Envelope>`, group.OrganizerEmail, eventID, occurrence.InstanceIndex)

	responseXML, err := h.sendRequest(requestXML)
	if err != nil {
		return fmt.Errorf("requesting cancel event: %w", err)
	}

	// First, try to unmarshal into SOAPFault to see if there was an error.
	var soapFault soapFault
	if err := xml.Unmarshal(responseXML, &soapFault); err == nil && soapFault.Body.Fault.FaultCode != "" {
		if soapFault.Body.Fault.FaultCode == "ErrorNonExistentMailbox" {
			return ErrNonExistentMailbox
		}
		return fmt.Errorf("SOAP fault: %s - %s", soapFault.Body.Fault.Detail.ResponseCode, soapFault.Body.Fault.Detail.Message)
	}

	var response struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			CreateItemResponse struct {
				ResponseMessages struct {
					CreateItemResponseMessage struct {
						ResponseClass string `xml:"ResponseClass,attr"`
						ResponseCode  string `xml:"ResponseCode"`
					} `xml:"CreateItemResponseMessage"`
				} `xml:"ResponseMessages"`
			} `xml:"CreateItemResponse"`
		} `xml:"Body"`
	}

	if err := xml.Unmarshal(responseXML, &response); err != nil {
		return fmt.Errorf("unmarshalling XML: %v", err)
	}

	responseClass := response.Body.CreateItemResponse.ResponseMessages.CreateItemResponseMessage.ResponseClass
	responseCode := response.Body.CreateItemResponse.ResponseMessages.CreateItemResponseMessage.ResponseCode

	if responseClass != "Success" || responseCode != "NoError" {
		return fmt.Errorf("cancelling event resulted in %s - %s. Response: %s", responseClass, responseCode, string(responseXML))
	}

	return nil
}

func (h *EWSHelper) getUIDFromItemId(itemMailbox string, itemId string) (string, error) {
	requestXML := fmt.Sprintf(`
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types">
    <soap:Header>
        <t:RequestServerVersion Version="Exchange2013_SP1"/>
        <t:ExchangeImpersonation>
            <t:ConnectingSID>
                <t:SmtpAddress>%s</t:SmtpAddress>
            </t:ConnectingSID>
        </t:ExchangeImpersonation>
    </soap:Header>
    <soap:Body>
        <GetItem xmlns="http://schemas.microsoft.com/exchange/services/2006/messages">
            <ItemShape>
                <t:BaseShape>IdOnly</t:BaseShape>
                <t:AdditionalProperties>
                    <t:FieldURI FieldURI="calendar:UID"/>
                </t:AdditionalProperties>
            </ItemShape>
            <ItemIds>
                <t:ItemId Id="%s"/>
            </ItemIds>
        </GetItem>
    </soap:Body>
</soap:Envelope>`, itemMailbox, itemId)

	respBody, err := h.sendRequest(requestXML)
	if err != nil {
		return "", fmt.Errorf("sending SOAP request failed: %v", err)
	}

	var response struct {
		Body struct {
			GetItemResponse struct {
				ResponseMessages struct {
					GetItemResponseMessage struct {
						Items struct {
							CalendarItem struct {
								UID string `xml:"UID"`
							} `xml:"CalendarItem"`
						} `xml:"Items"`
					} `xml:"GetItemResponseMessage"`
				} `xml:"ResponseMessages"`
			} `xml:"GetItemResponse"`
		} `xml:"Body"`
	}

	// Unmarshal the response body into the struct
	if err := xml.Unmarshal(respBody, &response); err != nil {
		return "", fmt.Errorf("XML unmarshal failed: %v", err)
	}

	uid := response.Body.GetItemResponse.ResponseMessages.GetItemResponseMessage.Items.CalendarItem.UID
	if uid == "" {
		return "", fmt.Errorf("UID not found in response. Response: %v", string(respBody))
	}

	return uid, nil
}

func getObjectIdStringFromUid(id string) (string, error) {
	buf, err := hex.DecodeString(id)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

// findEventUIDInMailbox finds the event specified by UID in the specified mailbox
// and returns it's itemID and changeKey.
// Inspired by article [1].
// [1] http://www.infinitec.de/post/2009/04/13/Searching-a-meeting-with-a-specific-UID-using-Exchange-Web-Services-2007.aspx
//
// GlobalObjectId represents a unique binary identifier for calendar items in Exchange.
// It is derived from the calendar item's UID (Universal Identifier), which remains constant
// even if the calendar item is moved or modified, providing a stable identifier across mailboxes.
// This property is crucial for operations that require identifying a specific calendar event uniquely,
// such as finding corresponding events across different users' calendars.
//
// In EWS, the GlobalObjectId is not directly exposed as a first-class property but can be accessed
// through the use of extended properties. The specific PropertySetId PSETID_Meeting
// ("6ED8DA90-450B-101B-98DA-00AA003F1305") and PropertyId ("0x03") are used to query this property
// in FindItem operations. These identifiers are defined by MAPI (Messaging Application Programming
// Interface) and are utilized in EWS to perform operations that involve the GlobalObjectId, like
// searching for a meeting by its UID.
//
// The need to convert the UID from a hexadecimal string to a base64-encoded string before querying
// stems from the binary nature of the GlobalObjectId in EWS. This conversion ensures that the value
// is correctly formatted for inclusion in SOAP requests, enabling effective querying and manipulation
// of calendar items based on their universal identifier.
func (h *EWSHelper) findEventUIDInMailbox(mailbox, uid string) (itemID string, changeKey string, err error) {
	globalObjectID, err := getObjectIdStringFromUid(uid)
	if err != nil {
		return "", "", fmt.Errorf("error converting UID: %v", err)
	}

	requestXML := fmt.Sprintf(`
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types" xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages">
    <soap:Header>
        <t:RequestServerVersion Version="Exchange2013_SP1"/>
        <t:ExchangeImpersonation>
            <t:ConnectingSID>
                <t:SmtpAddress>%s</t:SmtpAddress>
            </t:ConnectingSID>
        </t:ExchangeImpersonation>
    </soap:Header>
    <soap:Body>
      <m:FindItem Traversal="Shallow">
        <m:ItemShape>
          <t:BaseShape>AllProperties</t:BaseShape>
        </m:ItemShape>
        <m:Restriction>
          <t:IsEqualTo>
            <t:ExtendedFieldURI PropertySetId="6ED8DA90-450B-101B-98DA-00AA003F1305" PropertyId="3" PropertyType="Binary"/>
            <t:FieldURIOrConstant>
              <t:Constant Value="%s"/>
            </t:FieldURIOrConstant>
          </t:IsEqualTo>
        </m:Restriction>
        <m:ParentFolderIds>
          <t:DistinguishedFolderId Id="calendar">
            <t:Mailbox>
              <t:EmailAddress>%s</t:EmailAddress>
            </t:Mailbox>
          </t:DistinguishedFolderId>
        </m:ParentFolderIds>
      </m:FindItem>
    </soap:Body>
</soap:Envelope>`, mailbox, globalObjectID, mailbox)

	respBody, err := h.sendRequest(requestXML)
	if err != nil {
		return "", "", fmt.Errorf("sending SOAP request failed: %v", err)
	}

	var response struct {
		Body struct {
			FindItemResponse struct {
				ResponseMessages struct {
					FindItemResponseMessage struct {
						RootFolder struct {
							Items struct {
								CalendarItem []struct {
									ItemId struct {
										ID        string `xml:"Id,attr"`
										ChangeKey string `xml:"ChangeKey,attr"`
									} `xml:"ItemId"`
								} `xml:"CalendarItem"`
							} `xml:"Items"`
						} `xml:"RootFolder"`
					} `xml:"FindItemResponseMessage"`
				} `xml:"ResponseMessages"`
			} `xml:"FindItemResponse"`
		} `xml:"Body"`
	}

	if err := xml.Unmarshal(respBody, &response); err != nil {
		return "", "", fmt.Errorf("XML unmarshal failed: %v", err)
	}

	if len(response.Body.FindItemResponse.ResponseMessages.FindItemResponseMessage.RootFolder.Items.CalendarItem) == 0 {
		return "", "", errNotFound
	}

	item := response.Body.FindItemResponse.ResponseMessages.FindItemResponseMessage.RootFolder.Items.CalendarItem[0].ItemId
	return item.ID, item.ChangeKey, nil
}

// resolveDN translates the distinguished name to a SMTP one.
func (h *EWSHelper) resolveDN(name string) (string, error) {
	if smtp, found := h.addressCache[name]; found {
		return smtp, nil
	}
	// Docs say the reply might contain SMTP address sometimes. No need to resolve that.
	if isSMTPAddress(name) {
		h.addressCache[name] = name
		return name, nil
	}
	requestXML := fmt.Sprintf(`
<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"
                  xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"
                  xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages">
    <soapenv:Header>
        <t:RequestServerVersion Version="Exchange2013_SP1"/>
        <t:ExchangeImpersonation>
            <t:ConnectingSID>
                <t:PrincipalName>%s</t:PrincipalName>
            </t:ConnectingSID>
        </t:ExchangeImpersonation>
    </soapenv:Header>
    <soapenv:Body>
        <m:ResolveNames ReturnFullContactData="true" SearchScope="ActiveDirectory">
            <m:UnresolvedEntry>%s</m:UnresolvedEntry>
        </m:ResolveNames>
    </soapenv:Body>
</soapenv:Envelope>
`, h.serviceUser, name)

	responseXML, err := h.sendRequest(requestXML)
	if err != nil {
		return "", fmt.Errorf("resolving Legacy DN: %v", err)
	}

	var resp resolveNamesResponse
	if err := xml.Unmarshal(responseXML, &resp); err != nil {
		return "", fmt.Errorf("error unmarshaling XML from ResolveNames response: %v", err)
	}
	responseMessages := resp.Body.ResolveNamesResponse.ResponseMessages.ResolveNamesResponseMessage
	if len(responseMessages) != 1 {
		log.Debug("ews", string(responseXML))
		return "", fmt.Errorf("EWS reported an error")
	}
	resolutionMessages := responseMessages[0].ResolutionSet.Resolution
	if rms := len(resolutionMessages); rms != 1 {
		log.Debug("ews", "%v", resolutionMessages)
		return "", fmt.Errorf("EWS returned %v != 1 resolutionMessages", rms)
	}

	smtpAddress := resolutionMessages[0].Mailbox.EmailAddress
	h.addressCache[name] = smtpAddress
	return smtpAddress, nil
}

func isSMTPAddress(s string) bool {
	// Naive check, just to recognize from Legacy DN.
	return strings.Contains(s, "@")
}

type resolveNamesResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		ResolveNamesResponse struct {
			ResponseMessages struct {
				ResolveNamesResponseMessage []struct {
					ResolutionSet struct {
						TotalItemsInView        string `xml:"TotalItemsInView,attr"`
						IncludesLastItemInRange string `xml:"IncludesLastItemInRange,attr"`
						Resolution              []struct {
							Mailbox struct {
								EmailAddress string `xml:"EmailAddress"`
							} `xml:"Mailbox"`
						} `xml:"Resolution"`
					} `xml:"ResolutionSet"`
				} `xml:"ResolveNamesResponseMessage"`
			} `xml:"ResponseMessages"`
		} `xml:"ResolveNamesResponse"`
	} `xml:"Body"`
}
