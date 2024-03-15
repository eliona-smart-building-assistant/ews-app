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
	EwsUrl string
}

// NewEWSHelper initializes the EWSHelper with OAuth2 authentication and sets the EWS URL.
func NewEWSHelper(clientId, tenantId, clientSecret string) *EWSHelper {
	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantId)
	ewsURL := "https://outlook.office365.com/EWS/Exchange.asmx"

	// Configure the OAuth2 client credentials flow
	config := clientcredentials.Config{
		ClientID:     clientId,
		ClientSecret: clientSecret,
		TokenURL:     tokenURL,
		Scopes:       []string{"https://outlook.office365.com/.default"},
	}

	ctx := context.Background()
	// Create an HTTP client using the OAuth2 config
	httpClient := config.Client(ctx)

	return &EWSHelper{
		Client: httpClient,
		EwsUrl: ewsURL,
	}
}

// GetRooms sends a SOAP request to EWS to get the list of room lists.
func (h *EWSHelper) GetRooms() error {
	requestBody := `
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

	req, err := http.NewRequest("POST", h.EwsUrl, bytes.NewBufferString(requestBody))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	// Set necessary headers, including Content-Type. Authorization is handled by the OAuth2 http client.
	req.Header.Add("Content-Type", "text/xml; charset=utf-8")

	req.Header.Add("X-AnchorMailbox", "app@z0vmd.onmicrosoft.com")

	resp, err := h.Client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request to EWS: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body: %w", err)
	}

	fmt.Printf("Status: %v\n", resp.Status)

	var env envelope
	if err := xml.Unmarshal(respBody, &env); err != nil {
		return fmt.Errorf("Error unmarshaling XML: %v", err)
	}

	for _, room := range env.Body.GetRoomsResponse.Rooms.Room {
		fmt.Printf("Room Name: %s, Email: %s\n", room.Id.Name, room.Id.EmailAddress)
	}
	return nil
}

type envelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    body     `xml:"Body"`
}

type body struct {
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

func (h *EWSHelper) GetRoomAppointments(roomEmailAddress string, start, end time.Time) error {
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
`, roomEmailAddress, start.Format(time.RFC3339), end.Format(time.RFC3339), roomEmailAddress)

	req, err := http.NewRequest("POST", h.EwsUrl, bytes.NewBufferString(requestBody))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Add("Content-Type", "text/xml; charset=utf-8")
	req.Header.Add("X-AnchorMailbox", roomEmailAddress)

	resp, err := h.Client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request to EWS: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body: %w", err)
	}

	fmt.Printf("Status: %v\n", resp.Status)
	var env roomEventsEnvelope
	if err := xml.Unmarshal(respBody, &env); err != nil {
		return fmt.Errorf("Error unmarshaling XML: %v\n", err)
	}

	for _, responseMessage := range env.Body.FindItemResponse.ResponseMessages.FindItemResponseMessage {
		for _, item := range responseMessage.RootFolder.Items.CalendarItem {
			fmt.Printf("Subject: %s, Start: %s, End: %s\n", item.Subject, item.Start, item.End)
		}
	}

	return nil
}
