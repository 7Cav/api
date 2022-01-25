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

package grpc

import (
	"context"
	"errors"
	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"log"
	"os"
)

type MilpacsService struct {
	Datastore datastores.Datastore
}

var (
	Info  = log.New(os.Stdout, "INFO: ", 0)
	Warn  = log.New(os.Stdout, "WARNING: ", 0)
	Error = log.New(os.Stdout, "ERROR: ", 0)
)

func (server *MilpacsService) GetProfile(ctx context.Context, request *proto.ProfileRequest) (*proto.Profile, error) {
	if request.Username != "" {
		Info.Println("GetProfile, Requested via username")
	}

	if request.UserId != 0 {
		Info.Println("GetProfile, requested via userid")
	}

	profiles, err := server.Datastore.FindProfilesById(request.UserId)

	if err != nil {
		return &proto.Profile{}, status.Errorf(codes.NotFound, "no profile found for %", request.UserId)
	}

	return profiles[0], nil
}

func (server *MilpacsService) GetRoster(ctx context.Context, request *proto.RosterRequest) (*proto.Roster, error) {
	if request.Roster == proto.RosterType_ROSTER_TYPE_UNSPECIFIED {
		return nil, errors.New("cannot request null roster type")
	}

	roster, err := server.Datastore.FindRosterByType(request.Roster)

	if err != nil {
		return &proto.Roster{}, status.Errorf(codes.NotFound, "no roster found for %", request.Roster)
	}

	return roster, nil
}

func (server *MilpacsService) GetUserViaKeycloakId(ctx context.Context, request *proto.KeycloakIdRequest) (*proto.Profile, error) {

	if request.GetKeycloakId() == "" {
		Warn.Println("Empty discord ID provided, cannot return profile")
	}

	profile, err := server.Datastore.FindProfileByKeycloakID(request.GetKeycloakId())

	if err != nil {
		return &proto.Profile{}, status.Errorf(codes.NotFound, "no user found for %", request.GetKeycloakId())
	}

	return profile, nil
}
