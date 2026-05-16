/*
 *  Copyright (C) 2021 7Cav.us
 *  This file is part of 7Cav-API <https://github.com/7cav/api>.
 *
 *  7Cav-API is free software: you can redistribute it and/or modify
 *  it under the terms of the GNU General Public License as published by
 *  the Free Software Foundation, either version 3 of the License, or
 *  (at your option) any later version.
 *
 *  7Cav-API is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 *  GNU General Public License for more details.
 *
 *  You should have received a copy of the GNU General Public License
 *  along with 7Cav-API. If not, see <http://www.gnu.org/licenses/>.
 */

package datastores

import (
	"github.com/7cav/api/proto"
	"github.com/7cav/api/xenforo"
	"log"
	"os"
)

var (
	Info  = log.New(os.Stdout, "INFO: ", log.LstdFlags)
	Warn  = log.New(os.Stdout, "WARNING: ", log.LstdFlags)
	Error = log.New(os.Stdout, "ERROR: ", log.LstdFlags)
)

type ApiKeyResult struct {
	KeyId  uint
	UserId uint
	Scopes map[string]struct{}
}

// HasScope reports whether the API key has been granted the named scope.
func (r *ApiKeyResult) HasScope(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.Scopes[name]
	return ok
}

type Datastore interface {
	FindProfilesById(userId ...uint64) ([]*proto.Profile, error)
	FindProfilesByUsername(username string) ([]*proto.Profile, error)
	FindRosterByType(rosterType proto.RosterType) (*proto.Roster, error)
	FindLiteRosterByType(rosterType proto.RosterType) (*proto.LiteRoster, error)
	FindProfileByKeycloakID(keycloakId string) (*proto.Profile, error)
	FindProfileByDiscordID(discordId string) (*proto.Profile, error)
	FindProfilesByPosition(positionQuery string) (*proto.LiteRoster, error)
	FindS1UniformsRosterByType(rosterType proto.RosterType) (*proto.S1UniformsRoster, error)
	FindAllRanks() ([]*proto.RankExpanded, error)
	FindAllPositionGroups() ([]*proto.PositionGroup, error)
	FindAwol() ([]*proto.Awol, error)
	GetTableUpdates() ([]xenforo.TableInfo, error)
	FindProfileByGamertag(gamertag string) (*proto.Profile, error)
	ValidateApiKey(rawKey string) (*ApiKeyResult, error)
}
