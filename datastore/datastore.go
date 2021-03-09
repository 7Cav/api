package datastore

import (
	"github.com/7cav/api/proto"
)

type Datastore interface {
	FindProfilesById(userId ...uint64) ([]*proto.Profile, error)
	FindRosterByType(rosterType proto.RosterType) (*proto.Roster, error)
}