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

	"golang.org/x/oauth2/clientcredentials"
)

type EWSHelper struct {
	Client *http.Client
	EwsURL string
}

func NewEWSHelper(clientID, tenantID, clientSecret string) *EWSHelper {
	oauth2Config := clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantID),
		Scopes:       []string{"https://outlook.office365.com/.default"},
	}

	httpClient := oauth2Config.Client(context.Background())
	return &EWSHelper{
		Client: httpClient,
		EwsURL: "https://outlook.office365.com/EWS/Exchange.asmx",
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
`, *config.ServiceUserUPN, *config.RoomListUPN)
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

func (h *EWSHelper) GetRoomAppointments(roomEmail string, start, end time.Time) error {
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
		return err
	}

	return h.parseAndPrintAppointments(responseXML)
}

func (h *EWSHelper) parseAndPrintAppointments(xmlBody string) error {
	var env roomEventsEnvelope
	if err := xml.Unmarshal([]byte(xmlBody), &env); err != nil {
		return fmt.Errorf("unmarshaling XML: %v\n", err)
	}

	for _, message := range env.Body.FindItemResponse.ResponseMessages.FindItemResponseMessage {
		for _, item := range message.RootFolder.Items.CalendarItem {
			fmt.Printf("Subject: %s, Start: %s, End: %s\n", item.Subject, item.Start, item.End)
		}
	}
	return nil
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
	Start            string    `xml:"Start"`
	End              string    `xml:"End"`
	Organizer        organizer `xml:"Organizer"`
}

type itemId struct {
	Id        string `xml:"Id,attr"`
	ChangeKey string `xml:"ChangeKey,attr"`
}

type organizer struct {
	Mailbox mailbox `xml:"Mailbox"`
}

type mailbox struct {
	Name         string `xml:"Name"`
	EmailAddress string `xml:"EmailAddress"`
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
