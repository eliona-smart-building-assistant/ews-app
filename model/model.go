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

package model

import (
	"context"
	"ews/apiserver"
	"ews/conf"
	"fmt"

	"github.com/eliona-smart-building-assistant/go-eliona/asset"
	"github.com/eliona-smart-building-assistant/go-eliona/utils"
	"github.com/eliona-smart-building-assistant/go-utils/common"
)

type Room struct {
	Email    string `eliona:"email,filterable" subtype:"info"`
	Name     string `eliona:"name,filterable"`
	Bookable int8   `eliona:"bookable" subtype:"property"`

	Config apiserver.Configuration
}

func (r *Room) AdheresToFilter(filter [][]apiserver.FilterRule) (bool, error) {
	f := apiFilterToCommonFilter(filter)
	fp, err := utils.StructToMap(r)
	if err != nil {
		return false, fmt.Errorf("converting struct to map: %v", err)
	}
	adheres, err := common.Filter(f, fp)
	if err != nil {
		return false, err
	}
	return adheres, nil
}

func (r *Room) GetName() string {
	return r.Name
}

func (r *Room) GetDescription() string {
	return "Room resource managed in Microsoft Exchange server"
}

func (r *Room) GetAssetType() string {
	return "ews_room"
}

func (r *Room) GetGAI() string {
	return r.GetAssetType() + "_" + r.Email
}

func (r *Room) GetAssetID(projectID string) (*int32, error) {
	return conf.GetAssetId(context.Background(), r.Config, projectID, r.GetGAI())
}

func (r *Room) SetAssetID(assetID int32, projectID string) error {
	if err := conf.InsertAsset(context.Background(), r.Config, projectID, r.GetGAI(), assetID, r.Email); err != nil {
		return fmt.Errorf("inserting asset to config db: %v", err)
	}
	return nil
}

func (r *Room) GetLocationalChildren() []asset.LocationalNode {
	return []asset.LocationalNode{}
}

func (r *Room) GetFunctionalChildren() []asset.FunctionalNode {
	return []asset.FunctionalNode{}
}

type Root struct {
	Rooms []Room

	Config apiserver.Configuration
}

func (r *Root) GetName() string {
	return "ews"
}

func (r *Root) GetDescription() string {
	return "Root asset for ews resources"
}

func (r *Root) GetAssetType() string {
	return "ews_root"
}

func (r *Root) GetGAI() string {
	return r.GetAssetType()
}

func (r *Root) GetAssetID(projectID string) (*int32, error) {
	return conf.GetAssetId(context.Background(), r.Config, projectID, r.GetGAI())
}

func (r *Root) SetAssetID(assetID int32, projectID string) error {
	if err := conf.InsertAsset(context.Background(), r.Config, projectID, r.GetGAI(), assetID, ""); err != nil {
		return fmt.Errorf("inserting asset to config db: %v", err)
	}
	return nil
}

func (r *Root) GetLocationalChildren() []asset.LocationalNode {
	locationalChildren := make([]asset.LocationalNode, 0, len(r.Rooms))
	for i := range r.Rooms {
		locationalChildren = append(locationalChildren, &r.Rooms[i])
	}
	return locationalChildren
}

func (r *Root) GetFunctionalChildren() []asset.FunctionalNode {
	functionalChildren := make([]asset.FunctionalNode, 0, len(r.Rooms))
	for i := range r.Rooms {
		functionalChildren = append(functionalChildren, &r.Rooms[i])
	}
	return functionalChildren
}

//

func apiFilterToCommonFilter(input [][]apiserver.FilterRule) [][]common.FilterRule {
	result := make([][]common.FilterRule, len(input))
	for i := 0; i < len(input); i++ {
		result[i] = make([]common.FilterRule, len(input[i]))
		for j := 0; j < len(input[i]); j++ {
			result[i][j] = common.FilterRule{
				Parameter: input[i][j].Parameter,
				Regex:     input[i][j].Regex,
			}
		}
	}
	return result
}
