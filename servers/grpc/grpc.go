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
	"log"
	"os"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"gorm.io/gorm"
)

type MilpacsService struct {
	Datastore datastores.Datastore
}

var (
	Info  = log.New(os.Stdout, "INFO: ", log.LstdFlags)
	Warn  = log.New(os.Stdout, "WARNING: ", log.LstdFlags)
	Error = log.New(os.Stdout, "ERROR: ", log.LstdFlags)
)

func (server *MilpacsService) GetProfile(ctx context.Context, request *proto.ProfileRequest) (*proto.Profile, error) {
	if err := RequireScope(ctx, "read"); err != nil {
		return nil, err
	}
	var (
		profiles []*proto.Profile
		err      error
	)
	if request.Username != "" {
		Info.Println("GetProfile, Requested via username")
		profiles, err = server.Datastore.FindProfilesByUsername(request.Username)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, status.Errorf(codes.NotFound, "no profile found for username: %s", request.Username)
			}
			return nil, status.Errorf(codes.Internal, "fetch profile by username: %v", err)
		}
	} else if request.UserId != 0 {
		Info.Println("GetProfile, requested via userid")
		profiles, err = server.Datastore.FindProfilesById(request.UserId)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, status.Errorf(codes.NotFound, "no profile found for user ID: %d", request.UserId)
			}
			return nil, status.Errorf(codes.Internal, "fetch profile by user id: %v", err)
		}
	} else {
		return nil, status.Errorf(codes.InvalidArgument, "no username or user ID provided")
	}

	return profiles[0], nil
}

func (server *MilpacsService) GetRoster(ctx context.Context, request *proto.RosterRequest) (*proto.Roster, error) {
	if err := RequireScope(ctx, "read"); err != nil {
		return nil, err
	}
	if request.Roster == proto.RosterType_ROSTER_TYPE_UNSPECIFIED {
		return nil, errors.New("cannot request null roster type")
	}

	roster, err := server.Datastore.FindRosterByType(request.Roster)

	if err != nil {
		return &proto.Roster{}, status.Errorf(codes.NotFound, "no roster found for %s", request.Roster)
	}

	return roster, nil
}

func (server *MilpacsService) GetUserViaKeycloakId(ctx context.Context, request *proto.KeycloakIdRequest) (*proto.Profile, error) {
	if err := RequireScope(ctx, "read"); err != nil {
		return nil, err
	}

	if request.GetKeycloakId() == "" {
		Warn.Println("Empty Keycloak ID provided, cannot return profile")
	}

	profile, err := server.Datastore.FindProfileByKeycloakID(request.GetKeycloakId())

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "no user found for keycloakid: %s", request.GetKeycloakId())
		}
		return nil, status.Errorf(codes.Internal, "fetch profile by keycloak id: %v", err)
	}

	return profile, nil
}

func (server *MilpacsService) GetUserViaDiscordId(ctx context.Context, request *proto.DiscordIdRequest) (*proto.Profile, error) {
	if err := RequireScope(ctx, "read"); err != nil {
		return nil, err
	}

	if request.GetDiscordId() == "" {
		Warn.Println("Empty Discord ID provided, cannot return profile")
	}

	profile, err := server.Datastore.FindProfileByDiscordID(request.GetDiscordId())

	if err != nil {
		return &proto.Profile{}, status.Errorf(codes.NotFound, "no user found for discordid: %s", request.GetDiscordId())
	}

	return profile, nil
}
func (server *MilpacsService) GetLiteRoster(ctx context.Context, request *proto.RosterRequest) (*proto.LiteRoster, error) {
	if err := RequireScope(ctx, "read"); err != nil {
		return nil, err
	}
	if request.Roster == proto.RosterType_ROSTER_TYPE_UNSPECIFIED {
		return nil, errors.New("cannot request null roster type")
	}

	roster, err := server.Datastore.FindLiteRosterByType(request.Roster)

	if err != nil {
		return &proto.LiteRoster{}, status.Errorf(codes.NotFound, "no roster found for %s", request.Roster)
	}

	return roster, nil
}
func (server *MilpacsService) SearchByPosition(ctx context.Context, request *proto.PositionSearchRequest) (*proto.LiteRoster, error) {
	if err := RequireScope(ctx, "read"); err != nil {
		return nil, err
	}
	if request.GetPositionQuery() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "position query cannot be empty")
	}

	roster, err := server.Datastore.FindProfilesByPosition(request.GetPositionQuery())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error searching profiles by position: %v", err)
	}

	return roster, nil
}

func (server *MilpacsService) GetS1UniformsRoster(ctx context.Context, request *proto.RosterRequest) (*proto.S1UniformsRoster, error) {
	if err := RequireScope(ctx, "read"); err != nil {
		return nil, err
	}
	if request.Roster == proto.RosterType_ROSTER_TYPE_UNSPECIFIED {
		return nil, errors.New("cannot request null roster type")
	}

	roster, err := server.Datastore.FindS1UniformsRosterByType(request.Roster)

	if err != nil {
		return &proto.S1UniformsRoster{}, status.Errorf(codes.NotFound, "no roster found for %s", request.Roster)
	}
	return roster, nil
}

func (server *MilpacsService) GetAllRanks(ctx context.Context, _ *emptypb.Empty) (*proto.RanksResponse, error) {
	if err := RequireScope(ctx, "read"); err != nil {
		return nil, err
	}
	ranks, err := server.Datastore.FindAllRanks()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error fetching ranks: %v", err)
	}

	return &proto.RanksResponse{
		Ranks: ranks,
	}, nil
}

func (server *MilpacsService) GetPositionGroups(ctx context.Context, _ *emptypb.Empty) (*proto.PositionGroupsResponse, error) {
	if err := RequireScope(ctx, "read"); err != nil {
		return nil, err
	}
	groups, err := server.Datastore.FindAllPositionGroups()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error fetching position groups: %v", err)
	}

	return &proto.PositionGroupsResponse{
		Groups: groups,
	}, nil
}

func (server *MilpacsService) GetAwol(ctx context.Context, _ *emptypb.Empty) (*proto.AwolResponse, error) {
	if err := RequireScope(ctx, "read"); err != nil {
		return nil, err
	}
	awols, err := server.Datastore.FindAwol()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error fetching AWOL list: %v", err)
	}

	return &proto.AwolResponse{
		Awols: awols,
	}, nil
}

func (server *MilpacsService) GetGamertagProfile(ctx context.Context, request *proto.GamertagRequest) (*proto.Profile, error) {
	if err := RequireScope(ctx, "read"); err != nil {
		return nil, err
	}
	if request.GetGamertag() == "" {
		Warn.Println("Empty Gamertag provided, cannot return profile")
		return &proto.Profile{}, status.Errorf(codes.InvalidArgument, "gamertag cannot be empty")
	}

	profile, err := server.Datastore.FindProfileByGamertag(request.GetGamertag())

	if err != nil {
		return &proto.Profile{}, status.Errorf(codes.NotFound, "no user found for gamertag: %v", request.GetGamertag())
	}
	return profile, nil
}
