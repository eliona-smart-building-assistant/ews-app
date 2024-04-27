# Exchange app

This app acts as a synchronization service between Booking app and Microsoft Exchange server local and hybrid installations using EWS (Exchange Web Services).

EWS is getting deprecated by Microsoft Graph, but for certain types of installations, it is still the only way to interface. Basically, when the exchange mailboxes are stored on an Exchange server and not Exchange online, MS Graph is not an option and only EWS can be used.

MS Graph is supported using our Microsoft 365 App, but that app does not support the booking app interface yet.

## Configuration

The app needs environment variables and database tables for configuration. To edit the database tables the app provides an own API access.


### Registration in Eliona ###

To start and initialize an app in an Eliona environment, the app has to be registered in Eliona. For this, entries in database tables `public.eliona_app` and `public.eliona_store` are necessary.

This initialization can be handled by the `reset.sql` script.


### Environment variables

- `CONNECTION_STRING`: configures the [Eliona database](https://github.com/eliona-smart-building-assistant/go-eliona/tree/main/db). Otherwise, the app can't be initialized and started (e.g. `postgres://user:pass@localhost:5432/iot`).

- `INIT_CONNECTION_STRING`: configures the [Eliona database](https://github.com/eliona-smart-building-assistant/go-eliona/tree/main/db) for app initialization like creating schema and tables (e.g. `postgres://user:pass@localhost:5432/iot`). Default is content of `CONNECTION_STRING`.

- `API_ENDPOINT`:  configures the endpoint to access the [Eliona API v2](https://github.com/eliona-smart-building-assistant/eliona-api). Otherwise, the app can't be initialized and started. (e.g. `http://api-v2:3000/v2`)

- `API_TOKEN`: defines the secret to authenticate the app and access the Eliona API.

- `API_SERVER_PORT`(optional): define the port the API server listens. The default value is Port `3000`.

- `LOG_LEVEL`(optional): defines the minimum level that should be [logged](https://github.com/eliona-smart-building-assistant/go-utils/blob/main/log/README.md). The default level is `info`.

### Database tables ###

The app requires configuration data that remains in the database. To do this, the app creates its own database schema `ews` during initialization. To modify and handle the configuration data the app provides an API access. Have a look at the [API specification](https://eliona-smart-building-assistant.github.io/open-api-docs/?https://raw.githubusercontent.com/eliona-smart-building-assistant/ews-app/develop/openapi.yaml) how the configuration tables should be used.

- `ews.configuration`: Contains configuration of the app. Editable through the API.

- `ews.asset`: Provides asset mapping. Maps broker's asset IDs to Eliona asset IDs. Deleting asset from this table will recreate it in Eliona.

- `ews.booking`: Maps different Exchange ID types with booking app IDs.

**Generation**: to generate access method to database see Generation section below.


## References

### App API ###

The app provides its own API to access configuration data and other functions. The full description of the API is defined in the `openapi.yaml` OpenAPI definition file.

- [API Reference](https://eliona-smart-building-assistant.github.io/open-api-docs/?https://raw.githubusercontent.com/eliona-smart-building-assistant/ews-app/develop/openapi.yaml) shows details of the API

**Generation**: to generate api server stub see Generation section below.


### Eliona assets ###

This app creates Eliona asset types and attribute sets during initialization.

The data is written for each device, structured into different subtypes of Elinoa assets. The following subtypes are defined:

- `Info`: Static data which provides information about a device like address and firmware info.
- `Status`: Device status information, like battery level.
- `Input`: Current values reported by sensors.
- `Output`: Values that are to be passed back to the provider.
- `Property`: Special properties, like if the asset is bookable.

### Continuous asset creation ###

Assets for all resources connected to the configured room list are created automatically when the configuration is added.

To select which assets to create, configure members of the specified room list in Exchange administration.

To avoid conflicts, the Global Asset Identifier is a manufacturer's ID prefixed with asset type name as a namespace.

**Only assets created using CAC will be synchronized with EWS**

### Dashboard ###

An example dashboard meant for a quick start or showcasing the apps abilities can be obtained by accessing the dashboard endpoint defined in the `openapi.yaml` file.

## Tools

### Generate API server stub ###

For the API server the [OpenAPI Generator](https://openapi-generator.tech/docs/generators/openapi-yaml) for go-server is used to generate a server stub. The easiest way to generate the server files is to use one of the predefined generation script which use the OpenAPI Generator Docker image.

```
.\generate-api-server.cmd # Windows
./generate-api-server.sh # Linux
```

### Generate Database access ###

For the database access [SQLBoiler](https://github.com/volatiletech/sqlboiler) is used. The easiest way to generate the database files is to use one of the predefined generation script which use the SQLBoiler implementation.

```
.\generate-db.cmd # Windows
./generate-db.sh # Linux
```

### Generate asset type descriptions ###

For generating asset type descriptions from field-tag-annotated structs, [asse-from-struct tool](https://github.com/eliona-smart-building-assistant/dev-utilities) can be used.
