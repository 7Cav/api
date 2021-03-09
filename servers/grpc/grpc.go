package grpc

import (
	"context"
	"errors"
	"github.com/7cav/api/datastore"
	"github.com/7cav/api/proto"
	"log"
)

type MilpacsService struct {
	Datastore datastore.Datastore
}

func (server *MilpacsService) GetProfile(ctx context.Context, request *proto.ProfileRequest) (*proto.Profile, error) {

	if request.Username != "" {
		log.Print("Requested via username")
	}

	if request.UserId != 0 {
		log.Print("Requested via userID")
	}

	profiles, err := server.Datastore.FindProfilesById(request.UserId)

	if err != nil {
		// TODO return premadae error var
	}

	return profiles[0], nil
}

func (server *MilpacsService) GetRoster(ctx context.Context, request *proto.RosterRequest) (*proto.Roster, error) {
	if request.Roster == proto.RosterType_ROSTER_NULL {
		return nil, errors.New("cannot request null roster type")
	}

	roster, err := server.Datastore.FindRosterByType(request.Roster)

	if err != nil {
		// TODO return premadae error var
	}

	return roster, nil
}