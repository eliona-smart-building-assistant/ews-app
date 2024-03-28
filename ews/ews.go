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
	"encoding/xml"
	"ews/apiserver"
	"ews/model"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/eliona-smart-building-assistant/go-utils/log"
	"golang.org/x/oauth2/clientcredentials"
)

type EWSHelper struct {
	Client       *http.Client
	EwsURL       string
	serviceUser  string
	addressCache map[string]string
}

func NewEWSHelper(clientID, tenantID, clientSecret, serviceUser string) *EWSHelper {
	oauth2Config := clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantID),
		Scopes:       []string{"https://outlook.office365.com/.default"},
	}

	httpClient := oauth2Config.Client(context.Background())
	return &EWSHelper{
		Client:       httpClient,
		EwsURL:       "https://outlook.office365.com/EWS/Exchange.asmx",
		serviceUser:  serviceUser,
		addressCache: make(map[string]string),
	}
}

func (h *EWSHelper) sendRequest(xmlBody string) (string, error) {
	request, err := http.NewRequest("POST", h.EwsURL, bytes.NewBufferString(xmlBody))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	request.Header.Add("Content-Type", "text/xml; charset=utf-8")
	response, err := h.Client.Do(request)
	if err != nil {
		return "", fmt.Errorf("sending request: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("reading response body: %w", err)
	}

	return string(responseBody), nil
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
	if err := xml.Unmarshal([]byte(responseXML), &env); err != nil {
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

func (h *EWSHelper) GetRoomAppointments(roomEmail string, start, end time.Time) ([]model.Booking, error) {
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
        <m:FindItem Traversal="Shallow">
            <m:ItemShape>
                <t:BaseShape>IdOnly</t:BaseShape>
                <t:AdditionalProperties>
                    <t:FieldURI FieldURI="item:Subject"/>
                    <t:FieldURI FieldURI="item:DateTimeReceived"/>
                    <t:FieldURI FieldURI="calendar:Start"/>
                    <t:FieldURI FieldURI="calendar:End"/>
                    <t:FieldURI FieldURI="calendar:Organizer"/>
                    <t:FieldURI FieldURI="calendar:IsCancelled"/>
                </t:AdditionalProperties>
            </m:ItemShape>
            <m:CalendarView MaxEntriesReturned="50" StartDate="%s" EndDate="%s"/>
            <m:ParentFolderIds>
                <t:DistinguishedFolderId Id="calendar">
                    <t:Mailbox>
                        <t:EmailAddress>%s</t:EmailAddress>
                    </t:Mailbox>
                </t:DistinguishedFolderId>
            </m:ParentFolderIds>
        </m:FindItem>
    </soapenv:Body>
</soapenv:Envelope>
`, roomEmail, start.Format(time.RFC3339), end.Format(time.RFC3339), roomEmail)
	responseXML, err := h.sendRequest(requestXML)
	if err != nil {
		return nil, fmt.Errorf("getting room %v appointments: %v", roomEmail, err)
	}
	var env roomEventsEnvelope
	if err := xml.Unmarshal([]byte(responseXML), &env); err != nil {
		return nil, fmt.Errorf("unmarshaling XML: %v", err)
	}

	var bookings []model.Booking
	for _, message := range env.Body.FindItemResponse.ResponseMessages.FindItemResponseMessage {
		for _, item := range message.RootFolder.Items.CalendarItem {
			email, err := h.resolveDN(item.Organizer.Mailbox.EmailAddress)
			if err != nil {
				return nil, fmt.Errorf("resolving distinguished name '%s': %v", item.Organizer.Mailbox.EmailAddress, err)
			}
			bookings = append(bookings, model.Booking{
				ID:             item.ItemId.Id,
				ChangeKey:      item.ItemId.ChangeKey,
				Subject:        item.Subject,
				OrganizerEmail: email,
				Start:          item.Start,
				End:            item.End,
			})
		}
	}

	return bookings, nil
}

type roomEventsEnvelope struct {
	XMLName xml.Name       `xml:"Envelope"`
	Body    roomEventsBody `xml:"Body"`
}

type roomEventsBody struct {
	FindItemResponse findItemResponse `xml:"FindItemResponse"`
}

type findItemResponse struct {
	ResponseMessages responseMessages `xml:"ResponseMessages"`
}

type responseMessages struct {
	FindItemResponseMessage []findItemResponseMessage `xml:"FindItemResponseMessage"`
}

type findItemResponseMessage struct {
	ResponseClass string     `xml:"ResponseClass,attr"`
	ResponseCode  string     `xml:"ResponseCode"`
	RootFolder    rootFolder `xml:"RootFolder"`
}

type rootFolder struct {
	TotalItemsInView        string `xml:"TotalItemsInView,attr"`
	IncludesLastItemInRange string `xml:"IncludesLastItemInRange,attr"`
	Items                   items  `xml:"Items"`
}

type items struct {
	CalendarItem []calendarItem `xml:"CalendarItem"`
}

type calendarItem struct {
	ItemId           itemId    `xml:"ItemId"`
	Subject          string    `xml:"Subject"`
	DateTimeReceived string    `xml:"DateTimeReceived"`
	Start            time.Time `xml:"Start"`
	End              time.Time `xml:"End"`
	Organizer        organizer `xml:"Organizer"`
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
	RoutingType  string `xml:"RoutingType"`
	MailboxType  string `xml:"MailboxType"`
}

type Appointment struct {
	Subject   string
	Start     time.Time
	End       time.Time
	Location  string
	Attendees []string
}

func (h *EWSHelper) CreateAppointment(appointment Appointment) error {
	requestBody := fmt.Sprintf(`
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
		"msgraph@z0vmd.onmicrosoft.com",
		appointment.Subject,
		appointment.Start.Format(time.RFC3339),
		appointment.End.Format(time.RFC3339),
		appointment.Location,
		formatAttendees(appointment.Attendees),
	)

	req, err := http.NewRequest("POST", h.EwsURL, bytes.NewBufferString(requestBody))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Add("Content-Type", "text/xml; charset=utf-8")

	resp, err := h.Client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request to EWS: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body: %w", err)
	}

	fmt.Println(string(respBody))

	return nil
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
		return "", fmt.Errorf("error resolving Legacy DN: %v", err)
	}

	var resp resolveNamesResponse
	if err := xml.Unmarshal([]byte(responseXML), &resp); err != nil {
		return "", fmt.Errorf("error unmarshaling XML from ResolveNames response: %v", err)
	}
	responseMessages := resp.Body.ResolveNamesResponse.ResponseMessages.ResolveNamesResponseMessage
	if len(responseMessages) != 1 {
		log.Debug("ews", responseXML)
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
