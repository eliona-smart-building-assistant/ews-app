# Exchange app

### Eliona App for Microsoft Exchange booking integration

> The Exchange app provides synchronization of Eliona asset bookings with Microsoft Exchange servers.

Create a room list in Exchange, have these rooms available as bookable assets in Eliona and allow users book the rooms directly from Eliona.

## Configuring Exchange Web Services (EWS)

Follow these steps for Exchange Online and hybrid installations having user emails stored in Exchange online. *For Exchange server local installation or hybrid configuration with local-first accounts, skip this chapter and just obtain NTLM credentials and EWS API URL*

> Please note that EWS for Exchange Online will be deprected on October 1, 2026. This does not affect local exchange servers and hybrid configurations. More details on the retirement can be found on the [Exchange Team Blog](https://techcommunity.microsoft.com/t5/exchange-team-blog/retirement-of-exchange-web-services-in-exchange-online/ba-p/3924440)

### Registering the Application in Microsoft Entra

To configure EWS with Exchange app, follow the steps below to register it in Microsoft Entra.

#### 1. Register the Application

Navigate to **Entra** and select **App registrations**, then choose **Register app**. You will need to enter the application details.

#### 2. Configuring Permissions

##### Application Authentication (Impersonation)

For application-level authentication that supports impersonation:

- Go to **API permissions**.
- Add the permission `full_access_as_app` and grant admin consent.

Here is an example of the required configuration in the application's manifest:

```json
"requiredResourceAccess": [
    {
        "resourceAppId": "00000002-0000-0ff1-ce00-000000000000",
        "resourceAccess": [
            {
                "id": "dc890d15-9560-4a4c-9b7f-a736ec74ec40",
                "type": "Role"
            }
        ]
    }
]
```

#### 3. Generating Secrets for Authentication

For the application to authenticate:

- Navigate to **Certificates & secrets** in Entra.
- Select **New client secret**.
- Store the generated secret securely as it will be needed for the application to authenticate with Microsoft services.

#### Configuring Impersonation via PowerShell

To configure impersonation and other settings that are not available through the Entra portal, you must use PowerShell. Note that an online PowerShell console is unavailable without a subscription. Local PowerShell installations on Windows, Linux, or macOS can manage these configurations:

- Ensure you have the necessary PowerShell modules installed for managing Exchange.
- Use scripts to configure impersonation rights or other Exchange-specific settings.

#### PowerShell Scripts for Configuring Impersonation

1. **Connect to Exchange Online PowerShell**:

```powershell
$UserCredential = Get-Credential
$Session = New-PSSession -ConfigurationName Microsoft.Exchange -ConnectionUri https://outlook.office365.com/powershell-liveid/ -Credential $UserCredential -Authentication Basic -AllowRedirection
Import-PSSession $Session -DisableNameChecking
```

2. **Assign Impersonation Rights**:

```powershell
New-ManagementRoleAssignment –Name:impersonationAssignmentName –Role:ApplicationImpersonation –User:serviceAccount
```

3. **Verify Impersonation Rights**:

```powershell
Get-ManagementRoleAssignment –RoleAssignee serviceAccount –Role ApplicationImpersonation –RoleAssigneeType User
```

Replace `serviceAccount` with the name of your service account or user that will perform impersonation.

#### Disconnecting the PowerShell Session

Remember to close the PowerShell session once your configuration tasks are completed:

```powershell
Remove-PSSession $Session
```

## Installation

The Exchange App is installed via the App Store in Eliona.

## Assets

The Exchange App automatically creates all the rooms in the configured room list. Once the room is created in Eliona, it will stay there even if removed from room list (but bookings will not be synchronized anymore). A room can be renamed or deleted from Eliona independently. Whenever a new room is added to the room list, it will be created in Eliona.

## Configuration

The Exchange App is configured by defining one or more authentication credentials:

| Attribute        | Description                                               |
|------------------|-----------------------------------------------------------|
| `clientID`  | ClientID obtained in Entra admin center. (Only for OAuth authentication) |
| `clientSecret` | ClientSecret obtained in Entra admin center. (Only for OAuth authentication) |
| `tenantID`   | ID of the Exchange Online organization (Only for OAuth authentication) |
| `ewsURL`     | URL of the EWS API (only for NTLM authentication)|
| `username`   | NTLM username (only for NTLM authentication)|
| `password`   | NTLM password (only for NTLM authentication)|
| `serviceUserUPN`   | Email address of the service user (for querying rooms, creating anonymous bookings, ...) |
| `roomListUPN`   | Email of the room list containing the rooms to be synchronized. CAC will be deactivated if left empty. |
| `bookingAppURL`   | URL of the booking app. Use the one from example below. |
| `enable`         | Flag to enable or disable fetching from this configuration.          |
| `refreshInterval`| Interval in seconds for room discovery. |
| `requestTimeout` | API query timeout in seconds                              |
| `projectIDs`     | List of Eliona project ids for which this app should collect data. For each project id, all assets are automatically created in Eliona. |

The configuration is done via a corresponding JSON structure. As an example, the following JSON structure can be used to define an endpoint for app permissions:

```
{
  "clientId": "01234567-89ab-cdef-0123-456789abcdef",
  "clientSecret": "random-cl13nt-s3cr3t",
  "tenantId": "01234567-89ab-cdef-0123-456789abcdef",

  "ewsURL": "https://outlook.office365.com/EWS/Exchange.asmx",
  "username": "username",
  "password": "password",

  "serviceUserUPN": "eliona@example.com",
  "roomListUPN": "first.floor@example.com",

  "bookingAppURL": "http://booking:3000/v1",
  "enable": true,
  "refreshInterval": 60,
  "requestTimeout": 120,
  "projectIDs": [
    "10"
  ]
}
```

Configurations can be created using this structure in Eliona under `Apps > Exchange app > Settings`. To do this, select the /configs endpoint with the POST method.

After completing configuration, the app starts Continuous Asset Creation. When all discovered rooms are created, user is notified about that in Eliona's notification system.

## Bookings synchronization

If the Exchange app and Booking app are properly configured, the bookings are synchronized both ways between Exchange server and Eliona. The bookings from Eliona must be done on the assets created by Continuous asset creation. Any changes and cancellations from either Exchange server or Eliona will be synchronized to the other service as well.

In case any error occurs during synchronization from Eliona to Exchange (typically that room wouldn't accept the invitation), the user is notified about the problem using Eliona notifications and the booking in Eliona is cancelled.

If the booking is made by a user without Exchange account (or an Ad-hoc booking), the booking is made by service user.

## Booking Timing

When creating or deleting a booking from Eliona, the booking will be visible in Outlook in a few seconds. Changes made in Outlook are synchronized to Eliona every `refreshInterval` seconds.

## Recurring events

Recurring events can be created in Outlook. All occurrences will be passed to Eliona and be kept synchronized. Users in Eliona can cancel specific occurrences.

Keep in mind that there is a limit of how far in advance can the resources be booked. The limit is configurable in Exchange administration for the resources.

## Booking multiple assets

While booking frontend does not allow booking multiple assets at once, Outlook allows it. The app synchronizes the multi-booking into Eliona and the event can be modified or cancelled.
