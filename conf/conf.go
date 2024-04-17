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

package conf

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"ews/apiserver"
	"ews/appdb"
	"fmt"

	"github.com/eliona-smart-building-assistant/go-eliona/frontend"
	"github.com/eliona-smart-building-assistant/go-utils/common"
	"github.com/eliona-smart-building-assistant/go-utils/log"
	"github.com/volatiletech/null/v8"
	"github.com/volatiletech/sqlboiler/v4/boil"
)

var ErrBadRequest = errors.New("bad request")
var ErrNotFound = errors.New("not found")

func InsertConfig(ctx context.Context, config apiserver.Configuration) (apiserver.Configuration, error) {
	dbConfig, err := dbConfigFromApiConfig(ctx, config)
	if err != nil {
		return apiserver.Configuration{}, fmt.Errorf("creating DB config from API config: %v", err)
	}
	if err := dbConfig.InsertG(ctx, boil.Infer()); err != nil {
		return apiserver.Configuration{}, fmt.Errorf("inserting DB config: %v", err)
	}
	return config, nil
}

func UpsertConfig(ctx context.Context, config apiserver.Configuration) (apiserver.Configuration, error) {
	dbConfig, err := dbConfigFromApiConfig(ctx, config)
	if err != nil {
		return apiserver.Configuration{}, fmt.Errorf("creating DB config from API config: %v", err)
	}
	if err := dbConfig.UpsertG(ctx, true, []string{"id"}, boil.Blacklist("id"), boil.Infer()); err != nil {
		return apiserver.Configuration{}, fmt.Errorf("inserting DB config: %v", err)
	}
	return config, nil
}

func GetConfig(ctx context.Context, configID int64) (*apiserver.Configuration, error) {
	dbConfig, err := appdb.Configurations(
		appdb.ConfigurationWhere.ID.EQ(configID),
	).OneG(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBadRequest
	}
	if err != nil {
		return nil, fmt.Errorf("fetching config from database: %v", err)
	}
	apiConfig, err := apiConfigFromDbConfig(dbConfig)
	if err != nil {
		return nil, fmt.Errorf("creating API config from DB config: %v", err)
	}
	return &apiConfig, nil
}

func DeleteConfig(ctx context.Context, configID int64) error {
	if _, err := appdb.Assets(
		appdb.AssetWhere.ConfigurationID.EQ(configID),
	).DeleteAllG(ctx); err != nil {
		return fmt.Errorf("deleting assets from database: %v", err)
	}
	count, err := appdb.Configurations(
		appdb.ConfigurationWhere.ID.EQ(configID),
	).DeleteAllG(ctx)
	if err != nil {
		return fmt.Errorf("deleting config from database: %v", err)
	}
	if count > 1 {
		return fmt.Errorf("shouldn't happen: deleted more (%v) configs by ID", count)
	}
	if count == 0 {
		return ErrBadRequest
	}
	return nil
}

func dbConfigFromApiConfig(ctx context.Context, apiConfig apiserver.Configuration) (dbConfig appdb.Configuration, err error) {
	dbConfig.ClientID = *apiConfig.ClientId
	dbConfig.ClientSecret = *apiConfig.ClientSecret
	dbConfig.TenantID = *apiConfig.TenantId
	dbConfig.ServiceUserUpn = *apiConfig.ServiceUserUPN
	dbConfig.RoomListUpn = *apiConfig.RoomListUPN

	dbConfig.ID = null.Int64FromPtr(apiConfig.Id).Int64
	dbConfig.Enable = null.BoolFromPtr(apiConfig.Enable)
	dbConfig.RefreshInterval = apiConfig.RefreshInterval
	if apiConfig.RequestTimeout != nil {
		dbConfig.RequestTimeout = *apiConfig.RequestTimeout
	}
	af, err := json.Marshal(apiConfig.AssetFilter)
	if err != nil {
		return appdb.Configuration{}, fmt.Errorf("marshalling assetFilter: %v", err)
	}
	dbConfig.AssetFilter = null.JSONFrom(af)
	dbConfig.Active = null.BoolFromPtr(apiConfig.Active)
	if apiConfig.ProjectIDs != nil {
		dbConfig.ProjectIds = *apiConfig.ProjectIDs
	}

	env := frontend.GetEnvironment(ctx)
	if env != nil {
		dbConfig.UserID = null.StringFrom(env.UserId)
	}

	return dbConfig, nil
}

func apiConfigFromDbConfig(dbConfig *appdb.Configuration) (apiConfig apiserver.Configuration, err error) {
	apiConfig.ClientId = &dbConfig.ClientID
	apiConfig.ClientSecret = &dbConfig.ClientSecret
	apiConfig.TenantId = &dbConfig.TenantID
	apiConfig.ServiceUserUPN = &dbConfig.ServiceUserUpn
	apiConfig.RoomListUPN = &dbConfig.RoomListUpn

	apiConfig.Id = &dbConfig.ID
	apiConfig.Enable = dbConfig.Enable.Ptr()
	apiConfig.RefreshInterval = dbConfig.RefreshInterval
	apiConfig.RequestTimeout = &dbConfig.RequestTimeout
	if dbConfig.AssetFilter.Valid {
		var af [][]apiserver.FilterRule
		if err := json.Unmarshal(dbConfig.AssetFilter.JSON, &af); err != nil {
			return apiserver.Configuration{}, fmt.Errorf("unmarshalling assetFilter: %v", err)
		}
		apiConfig.AssetFilter = af
	}
	apiConfig.Active = dbConfig.Active.Ptr()
	apiConfig.ProjectIDs = common.Ptr[[]string](dbConfig.ProjectIds)
	apiConfig.UserId = dbConfig.UserID.Ptr()
	return apiConfig, nil
}

func GetConfigs(ctx context.Context) ([]apiserver.Configuration, error) {
	dbConfigs, err := appdb.Configurations().AllG(ctx)
	if err != nil {
		return nil, err
	}
	var apiConfigs []apiserver.Configuration
	for _, dbConfig := range dbConfigs {
		ac, err := apiConfigFromDbConfig(dbConfig)
		if err != nil {
			return nil, fmt.Errorf("creating API config from DB config: %v", err)
		}
		apiConfigs = append(apiConfigs, ac)
	}
	return apiConfigs, nil
}

func SetConfigActiveState(ctx context.Context, config apiserver.Configuration, state bool) (int64, error) {
	return appdb.Configurations(
		appdb.ConfigurationWhere.ID.EQ(null.Int64FromPtr(config.Id).Int64),
	).UpdateAllG(ctx, appdb.M{
		appdb.ConfigurationColumns.Active: state,
	})
}

func ProjIds(config apiserver.Configuration) []string {
	if config.ProjectIDs == nil {
		return []string{}
	}
	return *config.ProjectIDs
}

func IsConfigActive(config apiserver.Configuration) bool {
	return config.Active == nil || *config.Active
}

func IsConfigEnabled(config apiserver.Configuration) bool {
	return config.Enable == nil || *config.Enable
}

func SetAllConfigsInactive(ctx context.Context) (int64, error) {
	return appdb.Configurations().UpdateAllG(ctx, appdb.M{
		appdb.ConfigurationColumns.Active: false,
	})
}

func InsertAsset(ctx context.Context, config apiserver.Configuration, projId string, globalAssetID string, assetId int32, providerId string) error {
	var dbAsset appdb.Asset
	dbAsset.ConfigurationID = null.Int64FromPtr(config.Id).Int64
	dbAsset.ProjectID = projId
	dbAsset.GlobalAssetID = globalAssetID
	dbAsset.AssetID = null.Int32From(assetId)
	dbAsset.ProviderID = providerId
	return dbAsset.InsertG(ctx, boil.Infer())
}

func GetAssetId(ctx context.Context, config apiserver.Configuration, projId string, globalAssetID string) (*int32, error) {
	dbAsset, err := appdb.Assets(
		appdb.AssetWhere.ConfigurationID.EQ(null.Int64FromPtr(config.Id).Int64),
		appdb.AssetWhere.ProjectID.EQ(projId),
		appdb.AssetWhere.GlobalAssetID.EQ(globalAssetID),
	).AllG(ctx)
	if err != nil || len(dbAsset) == 0 {
		return nil, err
	}
	return common.Ptr(dbAsset[0].AssetID.Int32), nil
}

func GetAssetById(assetId int32) (appdb.Asset, error) {
	asset, err := appdb.Assets(
		appdb.AssetWhere.AssetID.EQ(null.Int32From(assetId)),
	).OneG(context.Background())
	if err != nil {
		return appdb.Asset{}, fmt.Errorf("fetching asset: %v", err)
	}
	return *asset, nil
}

func GetAssets() ([]appdb.Asset, error) {
	assets, err := appdb.Assets().AllG(context.Background())
	if err != nil {
		return nil, fmt.Errorf("fetching assets: %v", err)
	}
	assetsSlice := make([]appdb.Asset, 0, len(assets))
	for _, asset := range assets {
		if asset == nil {
			log.Warn("conf", "Asset is nil in slice, shouldn't happen")
			continue
		}
		assetsSlice = append(assetsSlice, *asset)
	}
	return assetsSlice, nil
}

func GetWatchedAssetIDs() ([]int, error) {
	assets, err := GetAssets()
	if err != nil {
		return nil, err
	}
	assetIDs := make([]int, 0, len(assets))
	for _, a := range assets {
		if !a.AssetID.Valid {
			continue
		}
		assetIDs = append(assetIDs, int(a.AssetID.Int32))
	}
	return assetIDs, nil
}

func GetConfigForAsset(asset appdb.Asset) (apiserver.Configuration, error) {
	c, err := asset.Configuration().OneG(context.Background())
	if err != nil {
		return apiserver.Configuration{}, fmt.Errorf("fetching configuration: %v", err)
	}
	return apiConfigFromDbConfig(c)
}

func GetSyncState(assetID int64) (string, error) {
	dbConfig, err := appdb.Assets(
		appdb.AssetWhere.ID.EQ(assetID),
	).OneG(context.Background())
	if err != nil {
		return "", fmt.Errorf("fetching sync state %v from database: %v", assetID, err)
	}
	return dbConfig.SyncState, nil
}

func PersistSyncState(assetID int64, syncState string) error {
	_, err := appdb.Assets(
		appdb.AssetWhere.ID.EQ(assetID),
	).UpdateAllG(context.Background(), appdb.M{
		appdb.AssetColumns.SyncState: syncState,
	})
	return err
}

func InsertBooking(booking appdb.Booking) error {
	return booking.InsertG(context.Background(), boil.Infer())
}

func GetBookingByExchangeID(exchangeID string) (appdb.Booking, error) {
	booking, err := appdb.Bookings(
		appdb.BookingWhere.ExchangeID.EQ(null.StringFrom(exchangeID)),
	).OneG(context.Background())
	if errors.Is(err, sql.ErrNoRows) {
		return appdb.Booking{}, ErrNotFound
	} else if err != nil {
		return appdb.Booking{}, fmt.Errorf("fetching booking from database: %v", err)
	}
	return *booking, nil
}

func GetBookingByElionaID(bookingID int32) (appdb.Booking, error) {
	booking, err := appdb.Bookings(
		appdb.BookingWhere.BookingID.EQ(null.Int32From(bookingID)),
	).OneG(context.Background())
	if errors.Is(err, sql.ErrNoRows) {
		return appdb.Booking{}, ErrNotFound
	} else if err != nil {
		return appdb.Booking{}, fmt.Errorf("fetching booking from database: %v", err)
	}
	return *booking, nil
}

func UpsertBooking(booking appdb.Booking) error {
	return booking.UpsertG(
		context.Background(), true,
		[]string{appdb.BookingColumns.ExchangeID},
		boil.Whitelist(appdb.BookingColumns.BookingID),
		boil.Infer(),
	)
}
