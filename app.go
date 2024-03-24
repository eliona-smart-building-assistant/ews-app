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
	"ews/apiserver"
	"ews/apiservices"
	"ews/booking"
	"ews/conf"
	"ews/eliona"
	"ews/ews"
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
			log.Info("main", "Collecting %d started.", *config.Id)
			if err := collectResources(config); err != nil {
				return // Error is handled in the method itself.
			}
			log.Info("main", "Collecting %d finished.", *config.Id)

			time.Sleep(time.Second * time.Duration(config.RefreshInterval))
		}, config, *config.Id)
	}
}

func collectResources(config apiserver.Configuration) error {
	ewsHelper := ews.NewEWSHelper(*config.ClientId, *config.TenantId, *config.ClientSecret)
	root, err := ewsHelper.GetAssets(config)
	if err != nil {
		log.Error("EWS", "getting assets: %v", err)
		return err
	}

	if err := eliona.CreateAssets(config, &root); err != nil {
		log.Error("eliona", "creating assets: %v", err)
		return err
	}

	appointments, err := ewsHelper.GetRoomAppointments("silent.room@z0vmd.onmicrosoft.com", time.Now().Add(-8*time.Hour), time.Now().Add(8*time.Hour))
	if err != nil {
		log.Error("EWS", "getting appointments: %v", err)
		return err
	}
	fmt.Println(appointments)

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

func listenForBookings() {
	baseURL := "http://localhost:3031/v1"
	assetIDs := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for { // We want to restart listening in case something breaks.
		bookingsClient := booking.NewClient(baseURL)
		bookingsChan, err := bookingsClient.ListenForBookings(assetIDs)
		if err != nil {
			log.Error("eliona-bookings", "listening for booking changes: %v", err)
			return
		}
		for book := range bookingsChan {
			fmt.Println(book)
			// TODO: Actually use the booking.
		}
		time.Sleep(time.Second * 5) // Give the server a little break.
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
