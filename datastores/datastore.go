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
	"context"
	"log"
	"os"
	"strings"

	"github.com/7cav/api/proto"
	"github.com/7cav/api/referencecache"
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

// ParseBearerToken extracts the raw API token from an Authorization header
// value. Returns "" if no Bearer scheme is present, the token is empty,
// or the result exceeds maxLen. Scheme name is matched case-insensitively
// per RFC 7235.
func ParseBearerToken(raw string, maxLen int) string {
	raw = strings.TrimSpace(raw)
	const prefix = "Bearer "
	if len(raw) < len(prefix) || !strings.EqualFold(raw[:len(prefix)], prefix) {
		return ""
	}
	tok := strings.TrimSpace(raw[len(prefix):])
	if tok == "" || len(tok) > maxLen {
		return ""
	}
	return tok
}

type Datastore interface {
	// FindProfilesById and FindProfilesByUsername return a NON-EMPTY slice of
	// non-nil profiles on a nil error — no-match is gorm.ErrRecordNotFound,
	// never an empty slice. Handlers index [0] under this invariant (with a
	// defensive 500 guard for implementations that break it).
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
	FindProfileByGamertag(gamertag string) (*proto.Profile, error)
	ValidateApiKey(rawKey string) (*ApiKeyResult, error)

	// Tickets
	ListTickets(ctx context.Context, rc TicketReferenceCache, filter *ListTicketsFilter) (tickets []*proto.Ticket, nextCursor string, hasMore bool, err error)
	GetTicket(ctx context.Context, rc TicketReferenceCache, ticketID uint32, forumBaseURL string) (*proto.Ticket, error)
	GetTicketByRef(ctx context.Context, rc TicketReferenceCache, ref string, forumBaseURL string) (*proto.Ticket, error)
	GetTicketFirstMessages(ctx context.Context, ticketID uint32, n int, includeHidden bool) (msgs []*proto.Message, totalCount uint32, err error)
	ListTicketMessages(ctx context.Context, ticketID uint32, afterCursor string, perPage uint32, includeHidden bool) (msgs []*proto.Message, nextCursor string, hasMore bool, err error)
	ListCategories(ctx context.Context, rc TicketReferenceCache) ([]*proto.Category, error)
}

// TicketReferenceCache is the slice of referencecache.ReferenceCache that
// tickets datastore methods need. Defined here (not pulled in via dependency)
// so the Datastore interface stays self-describing and easy to mock in tests.
type TicketReferenceCache interface {
	StatusName(id uint32) string
	PriorityName(id uint32) string
	PrefixName(id uint32) string
	Category(id uint32) *referencecache.CategoryRecord
	CategoryAncestors(id uint32) []uint32
	CategoryTree() []*referencecache.CategoryRecord
	ExpandSubtree(ids []uint32) []uint32
}

// Conformance pin: the production cache satisfies the slice. Lives HERE (not
// next to the grpc server that also consumes the cache) so the check
// survives Phase 4's deletion of the grpc stack.
var _ TicketReferenceCache = (*referencecache.Cache)(nil)
