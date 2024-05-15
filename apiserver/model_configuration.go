/*
 * EWS app API
 *
 * API to access and configure the app template
 *
 * API version: 1.0.0
 * Generated by: OpenAPI Generator (https://openapi-generator.tech)
 */

package apiserver

// Configuration - Each configuration defines access to provider's API.
type Configuration struct {

	// Internal identifier for the configured API (created automatically).
	Id *int64 `json:"id,omitempty"`

	// Client ID (for Exchange Online)
	ClientId *string `json:"clientId,omitempty"`

	// Client Secret (for Exchange Online)
	ClientSecret *string `json:"clientSecret,omitempty"`

	// Tenant ID (for Exchange Online)
	TenantId *string `json:"tenantId,omitempty"`

	// URL of EWS API (for Exchange Server NTLM auth)
	EwsURL *string `json:"ewsURL,omitempty"`

	// Username (for Exchange Server NTLM auth)
	Username *string `json:"username,omitempty"`

	// Password (for Exchange Server NTLM auth)
	Password *string `json:"password,omitempty"`

	// Service user email address.
	ServiceUserUPN *string `json:"serviceUserUPN,omitempty"`

	// Email address of the room list that will be imported to Eliona.
	RoomListUPN *string `json:"roomListUPN,omitempty"`

	// URL where the Eliona Booking app is reachable.
	BookingAppURL *string `json:"bookingAppURL,omitempty"`

	// Flag to enable or disable fetching from this API
	Enable *bool `json:"enable,omitempty"`

	// Interval in seconds for collecting data from API
	RefreshInterval int32 `json:"refreshInterval,omitempty"`

	// Timeout in seconds
	RequestTimeout *int32 `json:"requestTimeout,omitempty"`

	// Array of rules combined by logical OR
	AssetFilter [][]FilterRule `json:"assetFilter,omitempty"`

	// Set to `true` by the app when running and to `false` when app is stopped
	Active *bool `json:"active,omitempty"`

	// List of Eliona project ids for which this device should collect data. For each project id all smart devices are automatically created as an asset in Eliona. The mapping between Eliona is stored as an asset mapping in the KentixONE app.
	ProjectIDs *[]string `json:"projectIDs,omitempty"`

	// ID of the last Eliona user who created or updated the configuration
	UserId *string `json:"userId,omitempty"`
}

// AssertConfigurationRequired checks if the required fields are not zero-ed
func AssertConfigurationRequired(obj Configuration) error {
	if err := AssertRecurseInterfaceRequired(obj.AssetFilter, AssertFilterRuleRequired); err != nil {
		return err
	}
	return nil
}

// AssertConfigurationConstraints checks if the values respects the defined constraints
func AssertConfigurationConstraints(obj Configuration) error {
	return nil
}
