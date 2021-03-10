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
	if request.Roster == proto.RosterType_ROSTER_NULL {
		return nil, errors.New("cannot request null roster type")
	}

	roster, err := server.Datastore.FindRosterByType(request.Roster)

	if err != nil {
		return &proto.Roster{}, status.Errorf(codes.NotFound, "no roster found for %", request.Roster)
	}

	return roster, nil
}