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
	triggerResubscribe()

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
	var changedBookings []model.Booking

	for _, a := range assets {
		if !a.AssetID.Valid {
			continue
		}
		appointments, err := ewsHelper.GetRoomAppointments(a.ProviderID, time.Now().Add(-8*time.Hour), time.Now().Add(8*time.Hour))
		if err != nil {
			log.Error("EWS", "getting appointments: %v", err)
			return err
		}
		for i := range appointments {
			appointments[i].AssetID = a.AssetID.Int32
			a := appointments[i]

			booking, err := conf.GetBookingByExchangeID(a.ID)
			if err != nil && !errors.Is(err, conf.ErrNotFound) {
				log.Error("conf", "getting booking for exchange ID %s: %v", a.ID, err)
				return err
			} else if errors.Is(err, conf.ErrNotFound) {
				// Booking is new
				newBookings = append(newBookings, a)

				booking.ExchangeID = null.StringFrom(a.ID)
				booking.ExchangeChangeKey = null.StringFrom(a.ChangeKey)
				if err := conf.InsertBooking(booking); err != nil {
					log.Error("conf", "inserting booking: %v", err)
					return err
				}
				continue
			}

			if !booking.ExchangeChangeKey.Valid || booking.ExchangeChangeKey.String != a.ChangeKey {
				// Booking has changed.
				changedBookings = append(changedBookings, a)
				booking.ExchangeChangeKey = null.StringFrom(a.ChangeKey)
				err := conf.UpdateBooking(booking)
				if err != nil {
					log.Error("conf", "updating booking: %v", err)
					return err
				}
				continue
			}

			// Booking has not changed, skip.
			continue
		}
	}
	bc := booking.NewClient("http://localhost:3031/v1")
	if err := bc.Book(newBookings); err != nil {
		log.Error("Booking", "booking appointments: %v", err)
	}

	// todo: changedBookings

	appointment := ews.Appointment{
		Subject:   "Eliona booking",
		Start:     time.Now().Add(2 * time.Hour),
		End:       time.Now().Add(2*time.Hour + 10*time.Minute),
		Location:  "silent.room@z0vmd.onmicrosoft.com",
		Attendees: []string{"msgraph@z0vmd.onmicrosoft.com", "silent.room@z0vmd.onmicrosoft.com"},
	}
	err = ewsHelper.CreateAppointment(appointment)
	if err != nil {
		log.Error("EWS", "creating appointment: %v", err)
		return err
	}
	return nil
}

func listenForBookings(config apiserver.Configuration) {
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
		// todo: save the booking locally
		fmt.Println(book)
		asset, err := conf.GetAssetById(book.AssetID)
		if err != nil {
			log.Error("conf", "getting asset ID %v: %v", book.AssetID, err)
			continue
		}
		app := ews.Appointment{
			Subject:  "Eliona booking",
			Start:    book.Start,
			End:      book.End,
			Location: asset.ProviderID,
		}
		// We want to book on behalf of the organizer, thus we need to create a helper for each booking.
		ewsHelper := ews.NewEWSHelper(*config.ClientId, *config.TenantId, *config.ClientSecret, book.OrganizerEmail)
		if err := ewsHelper.CreateAppointment(app); err != nil {
			log.Error("ews", "Creating appointment: %v", err)
			continue
		}
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
