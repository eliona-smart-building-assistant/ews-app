# Exchange App

### Eliona App for Microsoft Exchange Booking Integration

> The Exchange app provides synchronization of Eliona asset bookings with Microsoft Exchange servers.

This app is an extension to [Booking app](https://doc.eliona.io/collection/eliona-english/eliona-apps/apps/booking). Create a room list in Exchange, have these rooms available as bookable assets in Eliona, and allow users to book the rooms directly from Eliona.

## Configuring Exchange Web Services (EWS)

Follow these steps for Exchange Online and hybrid installations having user emails stored in Exchange Online. *For Exchange Server local installation or hybrid configuration with local-first accounts, skip this chapter and just obtain NTLM credentials and EWS API URL.*

> Please note that EWS for Exchange Online will be deprecated on October 1, 2026. This does not affect local Exchange servers and hybrid configurations. More details on the retirement can be found on the [Exchange Team Blog](https://techcommunity.microsoft.com/t5/exchange-team-blog/retirement-of-exchange-web-services-in-exchange-online/ba-p/3924440).

#### Permissions Limitation

This app is designed to work with very strict permission requirements. That means that the bookings created in Eliona will have a service user as an organizer and the booking user as an attendee. The users would then need special permissions to edit the bookings outside of Eliona (in Outlook).

If your company policy allows delegation or impersonation rights in Exchange, please contact us to allow the app to act on behalf of the users.

### Registering the Application in Microsoft Entra

To configure EWS with Exchange, follow the steps below to register it in Microsoft Entra.

#### 1. Register the Application

Navigate to **Entra** and select **App registrations**, then choose **New registration**. You will need to enter the application details.

#### 2. Configuring Permissions

##### Application Authentication (OAuth)

- Go to **API permissions**.
- Add the permission `full_access_as_app` (or use the manifest excerpt below) and **grant admin consent**:

1. **Click "Add a permission"**.
2. Select **"APIs my organization uses"**.
3. Search for **"Office 365 Exchange Online"**.
4. Choose **"Application permissions"**.
5. Scroll down and check the box for:
   - **`full_access_as_app` (Use Exchange Web Services with full access to all mailboxes)**
6. Click **Add permissions**.
7. Click **Grant admin consent for <Your Organization>** to approve the permissions.

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

#### 4. Configuring Application Access Policies via PowerShell

To configure application access policies and other settings that are not available through the Entra portal, you must use PowerShell. Note that an online PowerShell console is unavailable without a subscription. Local PowerShell installations on Windows, Linux, or macOS can manage these configurations.

##### Restrict Access

Since `full_access_as_app` gives access to **all mailboxes**, you should restrict access using **Application Access Policies** in Exchange Online PowerShell.

1. **Connect to Exchange Online**:
   ```powershell
   Connect-ExchangeOnline -UserPrincipalName admin@yourdomain.com
   ```

2. **Create a Security Group for Room Mailboxes**:
   ```powershell
   New-DistributionGroup -Name "RoomBookingAppAccess" -PrimarySmtpAddress "roomaccess@yourdomain.com" -Type Security
   ```
   - Add only the **room mailboxes** that the app should access to this group.

3. **Restrict the App’s Access to Only These Mailboxes**:
   ```powershell
   New-ApplicationAccessPolicy -AppId "<Application ID>" -PolicyScopeGroupId "RoomBookingAppAccess" -AccessRight RestrictAccess -Description "Restrict app to room calendars"
   ```
   - Replace `<Application ID>` with your Azure App’s **Application (Client) ID**.

4. **Verify Policy**:
   ```powershell
   Get-ApplicationAccessPolicy
   ```

### Configuring NTLM Authentication for Hybrid Exchange

For on-premises or hybrid Exchange environments, NTLM authentication must be used instead of OAuth. This requires configuring a service account with the correct permissions.

#### 1. Grant EWS Access to the Service Account

Ensure that the service account (e.g., `service-account@yourdomain.com`) has access to EWS.

- **Check if EWS is enabled for the service account:**
  ```powershell
  Get-CASMailbox -Identity "service-account@yourdomain.com" | Select EwsEnabled
  ```
  - If `EwsEnabled` is `False`, enable it:
    ```powershell
    Set-CASMailbox -Identity "service-account@yourdomain.com" -EwsEnabled $true
    ```

#### 2. Assign Required Permissions for Room Mailboxes

- **Assign Full Access (for room mailbox management):**
  ```powershell
  Add-MailboxPermission -Identity "room101@yourdomain.com" -User "service-account@yourdomain.com" -AccessRights FullAccess -InheritanceType All
  ```

- **Assign Editor Access to the Room’s Calendar Folder:**
  ```powershell
  Add-MailboxFolderPermission -Identity "room101@yourdomain.com:\Calendar" -User "service-account@yourdomain.com" -AccessRights Editor
  ```

  ```powershell
  # Define the room list and the user to grant permissions to
  $roomList = "rooms@yourdomain.com"
  $user = "serviceuser@yourdomain.com"
  
  # Get all members of the room list
  $roomMailboxes = Get-DistributionGroupMember -Identity $roomList
  
  # Loop through each room mailbox and assign Editor permissions to the calendar folder
  foreach ($roomMailbox in $roomMailboxes) {
      $mailboxIdentity = $roomMailbox.PrimarySmtpAddress
      Add-MailboxFolderPermission -Identity "$mailboxIdentity`:\Calendar" -User $user -AccessRights Editor
      Write-Output "Editor permissions granted to $user for calendar of $mailboxIdentity"
  }
  ```

### Creating a Room List Using PowerShell

If the option to create a room list is not available in the Exchange Admin Center, you can use PowerShell to create and manage a room list.

1. **Open the Exchange Management Shell**:
   - Ensure you have the necessary administrative permissions.

2. **Create a Room List**:
   - Use the following PowerShell commands to create a room list and add room mailboxes to it:

   ```powershell
   # Create a new distribution group to act as a room list
   New-DistributionGroup -Name "Conference Rooms" -RoomList

   # Add room mailboxes to the room list
   Add-DistributionGroupMember -Identity "Conference Rooms" -Member "Room1@forest.local"
   Add-DistributionGroupMember -Identity "Conference Rooms" -Member "Room2@forest.local"
   ```

## Installation

The Exchange App is installed via the App Store in Eliona.

## Assets

The Exchange App automatically creates all the rooms in the configured room list. Once the room is created in Eliona, it will stay there even if removed from the room list (but bookings will not be synchronized anymore). A room can be renamed or deleted from Eliona independently. Whenever a new room is added to the room list, it will be created in Eliona.

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

```json
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

After completing configuration, the app starts Continuous Asset Creation. When all discovered rooms are created, the user is notified about that in Eliona's notification system.

## Bookings Synchronization

If the Exchange app and Booking app are properly configured, the bookings are synchronized both ways between Exchange Server and Eliona. The bookings from Eliona must be done on the assets created by Continuous Asset Creation. Any changes and cancellations from either Exchange Server or Eliona will be synchronized to the other service as well.

In case any error occurs during synchronization from Eliona to Exchange (typically that room wouldn't accept the invitation), the user is notified about the problem using Eliona notifications and the booking in Eliona is canceled.

If the booking is made by a user without an Exchange account (or an ad-hoc booking), the booking is made by the service user.

## Booking Timing

When creating or deleting a booking from Eliona, the booking will be visible in Outlook in a few seconds. Changes made in Outlook are synchronized to Eliona every `refreshInterval` seconds.

## Recurring Events

Recurring events can be created in Outlook. All occurrences will be passed to Eliona and be kept synchronized. Users in Eliona can cancel specific occurrences.

Keep in mind that there is a limit on how far in advance resources can be booked. The limit is configurable in Exchange administration for the resources.

## Booking Multiple Assets

While the booking frontend does not allow booking multiple assets at once, Outlook allows it. The app synchronizes the multi-booking into Eliona, and the event can be modified or canceled.
