package ews

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"

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
       <m:GetRooms/>
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

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body: %w", err)
	}

	fmt.Printf("Status: %v\n", resp.Status)
	fmt.Printf("Response: %s\n", string(respBody))
	// Here, you would parse the XML response to extract the room lists.
	// For simplicity, this example prints the raw XML response.

	return nil
}
