package service

import (
	"context"
	milpacs "github.com/7cav/api/proto"
	"log"
)

type GrpcService struct {}

func New() *GrpcService {
	return &GrpcService{}
}

func (server *GrpcService) GetProfile(ctx context.Context, request *milpacs.ProfileRequest) (*milpacs.Profile, error) {

	if request.Username != "" {
		log.Print("Requested via username")
	}

	if request.UserId != 0 {
		log.Print("Requested via userID")
	}

	milpac := &milpacs.Profile{
		User: &milpacs.User{Username: "John Doe"},
		Rank: &milpacs.Rank{RankShort: "GOA", RankFull: "General of the Army"},
		UniformUrl: "https://some.path.to.image.com",
		Roster: milpacs.RosterType_COMBAT,
		Primary: &milpacs.Position{PositionTitle: "Regimental Commander"},
		Secondaries: []*milpacs.Position{{PositionTitle: "S3 1IC"}},
		Records: []*milpacs.Record{
			{
				RecordDate:    "2020-12-20",
				RecordDetails: "Had some cool stuff happen",
				RecordType:    milpacs.RecordType_GRADUATION,
			},
		}, Awards: []*milpacs.Award{
			{
				AwardDate:     "2020-11-16",
				AwardDetails:  "Some Award",
				AwardImageUrl: "https://some.image.path.com",
				AwardName:     "Bestest Dude",
			},
		},
	}

	return milpac, nil;
}

func (server *GrpcService) GetRoster(ctx context.Context, request *milpacs.RosterRequest) (*milpacs.Roster, error) {
	panic("implement me")
}