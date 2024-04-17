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

package main

import (
	"context"
	"errors"
	"ews/apiserver"
	"ews/apiservices"
	"ews/appdb"
	"ews/booking"
	"ews/conf"
	"ews/eliona"
	"ews/ews"
	"ews/model"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/eliona-smart-building-assistant/go-eliona/app"
	"github.com/eliona-smart-building-assistant/go-eliona/asset"
	"github.com/eliona-smart-building-assistant/go-eliona/dashboard"
	"github.com/eliona-smart-building-assistant/go-eliona/frontend"
	"github.com/eliona-smart-building-assistant/go-utils/common"
	"github.com/eliona-smart-building-assistant/go-utils/db"
	utilshttp "github.com/eliona-smart-building-assistant/go-utils/http"
	"github.com/eliona-smart-building-assistant/go-utils/log"
	"github.com/volatiletech/null/v8"
)

func initialization() {
	ctx := context.Background()

	// Necessary to close used init resources
	conn := db.NewInitConnectionWithContextAndApplicationName(ctx, app.AppName())
	defer conn.Close(ctx)

	// Init the app before the first run.
	app.Init(conn, app.AppName(),
		app.ExecSqlFile("conf/init.sql"),
		asset.InitAssetTypeFiles("resources/asset-types/*.json"),
		dashboard.InitWidgetTypeFiles("resources/widget-types/*.json"),
	)
}

var once sync.Once
var resubscribeTrigger = make(chan struct{}, 1)

func collectData() {
	configs, err := conf.GetConfigs(context.Background())
	if err != nil {
		log.Fatal("conf", "Couldn't read configs from DB: %v", err)
		return
	}
	if len(configs) == 0 {
		once.Do(func() {
			log.Info("conf", "No configs in DB. Please configure the app in Eliona.")
		})
		return
	}

	for _, config := range configs {
		if !conf.IsConfigEnabled(config) {
			if conf.IsConfigActive(config) {
				conf.SetConfigActiveState(context.Background(), config, false)
			}
			continue
		}

		if !conf.IsConfigActive(config) {
			conf.SetConfigActiveState(context.Background(), config, true)
			log.Info("conf", "Collecting initialized with Configuration %d:\n"+
				"Enable: %t\n"+
				"Refresh Interval: %d\n"+
				"Request Timeout: %d\n"+
				"Project IDs: %v\n",
				*config.Id,
				*config.Enable,
				config.RefreshInterval,
				*config.RequestTimeout,
				*config.ProjectIDs)
		}

		common.RunOnceWithParam(func(config apiserver.Configuration) {
			log.Info("main", "Subscription %d started.", *config.Id)

			listenForBookings(config)

			log.Info("main", "Subscription %d exited. Resubscribing...", *config.Id)
		}, config, fmt.Sprintf("subscription_%v", *config.Id))

		common.RunOnceWithParam(func(config apiserver.Configuration) {
			log.Info("main", "Collecting %d started.", *config.Id)
			if err := collectResources(config); err != nil {
				return // Error is handled in the method itself.
			}
			log.Info("main", "Collecting %d finished.", *config.Id)

			time.Sleep(time.Second * time.Duration(config.RefreshInterval))
		}, config, fmt.Sprintf("collection_%v", *config.Id))
	}
}

func triggerResubscribe() {
	// Non-blocking Send: This ensures that sending to the channel doesn't block if the channel buffer is full.
	select {
	case resubscribeTrigger <- struct{}{}:
	default:
	}
}

func collectResources(config apiserver.Configuration) error {
	// Note: EWSHelper has an address cache and this resets it in each sync.
	// If there is a need for optimization, create EWS helper only once per config.
	ewsHelper := ews.NewEWSHelper(*config.ClientId, *config.TenantId, *config.ClientSecret, *config.ServiceUserUPN)
	root, err := ewsHelper.GetAssets(config)
	if err != nil {
		log.Error("EWS", "getting EWS assets: %v", err)
		return err
	}

	if cnt, err := eliona.CreateAssets(config, &root); err != nil {
		log.Error("eliona", "creating assets in Eliona: %v", err)
		return err
	} else if cnt > 0 {
		// New assets are present, need to subscribe again to include these.
		triggerResubscribe()
	}

	assets, err := conf.GetAssets()
	if err != nil {
		log.Error("conf", "getting assets from DB: %v", err)
		return err
	}
	var newBookings []model.Booking
	var updatedBookings []model.Booking
	var cancelledBookings []model.Booking

	for _, ast := range assets {
		if !ast.AssetID.Valid {
			continue
		}
		if ast.ProviderID == "" {
			continue
		}

		syncState, err := conf.GetSyncState(ast.ID)
		if err != nil {
			log.Error("conf", "getting sync state: %v", err)
			return err
		}

		// See git blame here for filtering these events based on changeKey.
		// Now that Exchange provides the distinction, let's trust it and simplify
		// our logic.
		new, updated, cancelled, newSyncState, err := ewsHelper.GetRoomAppointments(ast.ProviderID, syncState)
		if err != nil {
			log.Error("EWS", "getting appointments for %s: %v", ast.ProviderID, err)
			return err
		}
		for i := range updated {
			updated[i].AssetID = ast.AssetID.Int32
			a := updated[i]
			booking, err := conf.GetBookingByExchangeID(a.ExchangeIDInResourceMailbox)
			if err != nil && !errors.Is(err, conf.ErrNotFound) {
				log.Error("conf", "getting booking for exchange ID %s: %v", a.ExchangeIDInResourceMailbox, err)
				return err
			} else if errors.Is(err, conf.ErrNotFound) {
				// Booking is new
				new = append(new, a)
			}

			if !booking.BookingID.Valid {
				// Booking not yet synced to Eliona
				new = append(new, a)
			}
			a.ElionaID = booking.BookingID.Int32
			updatedBookings = append(updatedBookings, a)
		}
		for i := range new {
			new[i].AssetID = ast.AssetID.Int32
			a := new[i]

			newBookings = append(newBookings, a)
			booking := appdb.Booking{
				ExchangeID:  null.StringFrom(a.ExchangeIDInResourceMailbox),
				ExchangeUID: null.StringFrom(a.ExchangeUID),
			}
			if err := conf.InsertBooking(booking); err != nil {
				log.Error("conf", "inserting booking: %v", err)
				return err
			}
		}
		for i := range cancelled {
			cancelled[i].AssetID = ast.AssetID.Int32
			a := cancelled[i]
			booking, err := conf.GetBookingByExchangeID(a.ExchangeIDInResourceMailbox)
			if err != nil && !errors.Is(err, conf.ErrNotFound) {
				log.Error("conf", "getting booking for exchange ID %s: %v", a.ExchangeIDInResourceMailbox, err)
				return err
			} else if errors.Is(err, conf.ErrNotFound) || !booking.BookingID.Valid {
				// Does not matter, cancelled anyways
				continue
			}
			a.ElionaID = booking.BookingID.Int32
			cancelledBookings = append(cancelledBookings, a)
		}
		if err := conf.PersistSyncState(ast.ID, newSyncState); err != nil {
			log.Error("conf", "persisting sync state for %v: %v", ast.ID, err)
			return err
		}
	}

	// todo: unhardcode
	bc := booking.NewClient("http://localhost:3031/v1")
	if err := bc.Book(newBookings); err != nil {
		log.Error("Booking", "booking: %v", err)
	}

	if err := bc.Book(updatedBookings); err != nil {
		log.Error("Booking", "updating bookings: %v", err)
	}

	if err := bc.Cancel(cancelledBookings); err != nil {
		log.Error("Booking", "cancelling bookings: %v", err)
	}

	return nil
}

func listenForBookings(config apiserver.Configuration) {
	// todo: unhardcode
	baseURL := "http://localhost:3031/v1"
	assetIDs, err := conf.GetWatchedAssetIDs()
	if err != nil {
		log.Error("conf", "getting list of assetIDs to watch: %v", err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-resubscribeTrigger:
			log.Info("main", "Resubscription trigerred.")
			cancel()
			return
		}
	}()

	bookingsClient := booking.NewClient(baseURL)
	bookingsChan, err := bookingsClient.ListenForBookings(ctx, assetIDs)
	if err != nil {
		log.Error("eliona-bookings", "listening for booking changes: %v", err)
		return
	}
	for book := range bookingsChan {
		if book.Cancelled {
			cancelInEWS(book, config)
			continue
		}
		bookInEWS(book, config)
	}
}

func cancelInEWS(book model.Booking, config apiserver.Configuration) {
	ewsHelper := ews.NewEWSHelper(*config.ClientId, *config.TenantId, *config.ClientSecret, book.OrganizerEmail)
	booking, err := conf.GetBookingByElionaID(book.ElionaID)
	if err != nil {
		log.Error("conf", "getting booking for Eliona ID %v: %v", book.ElionaID, err)
		return
	} else if !booking.ExchangeID.Valid || !booking.ExchangeUID.Valid {
		log.Error("db", "cancelling booking: booking %v does not have exchangeID or UID", booking.ID)
		return
	}
	book.ExchangeIDInResourceMailbox = booking.ExchangeID.String
	book.ExchangeUID = booking.ExchangeUID.String
	if err := ewsHelper.CancelEvent(book); err != nil {
		log.Error("ews", "cancelling event: %v", err)
		return
	}
}

func bookInEWS(book model.Booking, config apiserver.Configuration) {
	asset, err := conf.GetAssetById(book.AssetID)
	if err != nil {
		log.Error("conf", "getting asset ID %v: %v", book.AssetID, err)
		return
	}
	// We want to book on behalf of the organizer, thus we need to create a helper for each booking.
	ewsHelper := ews.NewEWSHelper(*config.ClientId, *config.TenantId, *config.ClientSecret, book.OrganizerEmail)
	app := ews.Appointment{
		Organizer: book.OrganizerEmail,
		Subject:   "Eliona booking",
		Start:     book.Start,
		End:       book.End,
		Location:  asset.ProviderID,
		Attendees: []string{asset.ProviderID},
	}
	booking, err := ewsHelper.CreateAppointment(app)
	if err != nil {
		log.Error("ews", "creating appointment: %v", err)
		return
	}
	log.Debug("ews", "created a booking for %v", book.OrganizerEmail)
	b := appdb.Booking{
		ExchangeID:  null.StringFrom(booking.ExchangeIDInResourceMailbox),
		ExchangeUID: null.StringFrom(booking.ExchangeUID),
		BookingID:   null.Int32From(book.ElionaID),
	}
	if err := conf.InsertBooking(b); err != nil {
		log.Error("conf", "inserting booking: %v", err)
		return
	}
}

// listenApi starts the API server and listen for requests
func listenApi() {
	err := http.ListenAndServe(":"+common.Getenv("API_SERVER_PORT", "3000"),
		frontend.NewEnvironmentHandler(
			utilshttp.NewCORSEnabledHandler(
				apiserver.NewRouter(
					apiserver.NewConfigurationAPIController(apiservices.NewConfigurationAPIService()),
					apiserver.NewVersionAPIController(apiservices.NewVersionAPIService()),
					apiserver.NewCustomizationAPIController(apiservices.NewCustomizationAPIService()),
				))))
	log.Fatal("main", "API server: %v", err)
}
