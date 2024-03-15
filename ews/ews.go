package ews

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
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
	Room []room `xml:"Room"`
}

type room struct {
	Id roomId `xml:"Id"`
}

type roomId struct {
	Name         string `xml:"Name"`
	EmailAddress string `xml:"EmailAddress"`
	RoutingType  string `xml:"RoutingType"`
	MailboxType  string `xml:"MailboxType"`
}

func (h *EWSHelper) GetRooms() error {
	requestXML := `
<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"
                  xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"
                  xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages">
    <soapenv:Header>
        <t:RequestServerVersion Version="Exchange2013_SP1"/>
        <t:ExchangeImpersonation>
            <t:ConnectingSID>
                <t:PrincipalName>app@z0vmd.onmicrosoft.com</t:PrincipalName>
            </t:ConnectingSID>
        </t:ExchangeImpersonation>
    </soapenv:Header>
    <soapenv:Body>
        <m:GetRooms>
            <m:RoomList>
                <t:EmailAddress>first.floor@z0vmd.onmicrosoft.com</t:EmailAddress>
           </m:RoomList>
        </m:GetRooms>
    </soapenv:Body>
</soapenv:Envelope>
`
	responseXML, err := h.sendRequest(requestXML)
	if err != nil {
		return err
	}

	return h.parseAndPrintRooms(responseXML)
}

func (h *EWSHelper) parseAndPrintRooms(xmlBody string) error {
	var env roomsEnvelope
	if err := xml.Unmarshal([]byte(xmlBody), &env); err != nil {
		return fmt.Errorf("unmarshaling XML: %v", err)
	}

	for _, room := range env.Body.GetRoomsResponse.Rooms.Room {
		fmt.Printf("Room Name: %s, Email: %s\n", room.Id.Name, room.Id.EmailAddress)
	}
	return nil
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
