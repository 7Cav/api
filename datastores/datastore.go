package datastores

import (
	"github.com/7cav/api/proto"
	"log"
	"os"
)

var (
	Info  = log.New(os.Stdout, "INFO: ", 0)
	Warn  = log.New(os.Stdout, "WARNING: ", 0)
	Error = log.New(os.Stdout, "ERROR: ", 0)
)

type Datastore interface {
	FindProfilesById(userId ...uint64) ([]*proto.Profile, error)
	FindRosterByType(rosterType proto.RosterType) (*proto.Roster, error)
}