package eliona

import (
	"context"
	"ews/apiserver"
	"ews/conf"
	"ews/model"
	"fmt"

	"github.com/eliona-smart-building-assistant/go-eliona/asset"
	"github.com/eliona-smart-building-assistant/go-utils/log"
)

const ClientReference string = "ews-app"

func UpsertAssetData(config apiserver.Configuration, assets []model.Room) error {
	for _, projectId := range *config.ProjectIDs {
		for _, a := range assets {
			a.Bookable = 1
			log.Debug("Eliona", "upserting data for asset: config %d and asset '%v'", config.Id, a.GetGAI())
			assetId, err := conf.GetAssetId(context.Background(), config, projectId, a.GetGAI())
			if err != nil {
				return err
			}
			if assetId == nil {
				// This might happen in case of filtered or newly added devices.
				log.Debug("conf", "unable to find asset ID for %v", a.GetGAI())
				continue
			}

			data := asset.Data{
				AssetId:         *assetId,
				Data:            a,
				ClientReference: ClientReference,
			}
			if err := asset.UpsertAssetDataIfAssetExists(data); err != nil {
				return fmt.Errorf("upserting data: %v", err)
			}
		}
	}
	return nil
}
