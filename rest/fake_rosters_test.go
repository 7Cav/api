package rest_test

import (
	"github.com/7cav/api/types"
)

// Roster seeds for the golden replay, mirroring the recording seed
// (contract/fake_datastore_test.go, "Rosters" section) exactly: COMBAT
// carries the two seed profiles keyed by relation id, ARLINGTON is the
// injected outage on the FULL roster route, and every other roster is
// empty-but-present ({"profiles":{}} on the wire).

func (f *fakeDatastore) FindRosterByType(t types.RosterType) (*types.Roster, error) {
	if f.findRosterByType != nil {
		return f.findRosterByType(t)
	}
	switch t {
	case types.RosterTypeCombat:
		return &types.Roster{Profiles: map[uint64]*types.Profile{1: seedJarvis(), 2: seedDoe()}}, nil
	case types.RosterTypeArlington:
		return nil, errOutage
	default:
		return &types.Roster{Profiles: map[uint64]*types.Profile{}}, nil
	}
}

func (f *fakeDatastore) FindLiteRosterByType(t types.RosterType) (*types.LiteRoster, error) {
	if f.findLiteRosterByType != nil {
		return f.findLiteRosterByType(t)
	}
	switch t {
	case types.RosterTypeCombat:
		return &types.LiteRoster{Profiles: map[uint64]*types.LiteProfile{1: seedJarvisLite(), 2: seedDoeLite()}}, nil
	default:
		return &types.LiteRoster{Profiles: map[uint64]*types.LiteProfile{}}, nil
	}
}

func (f *fakeDatastore) FindS1UniformsRosterByType(t types.RosterType) (*types.S1UniformsRoster, error) {
	if f.findS1UniformsRosterByType != nil {
		return f.findS1UniformsRosterByType(t)
	}
	switch t {
	case types.RosterTypeCombat:
		return &types.S1UniformsRoster{Profiles: map[uint64]*types.S1UniformsProfile{
			1: seedJarvisS1Uniforms(),
			2: seedDoeS1Uniforms(),
		}}, nil
	default:
		return &types.S1UniformsRoster{Profiles: map[uint64]*types.S1UniformsProfile{}}, nil
	}
}

// seedJarvisLite mirrors the recording seed's lite view of the rich profile:
// the records/awards collections collapse to the two scalar summary dates.
func seedJarvisLite() *types.LiteProfile {
	j := seedJarvis()
	return &types.LiteProfile{
		User:              j.User,
		Rank:              j.Rank,
		RealName:          j.RealName,
		UniformUrl:        j.UniformUrl,
		Roster:            j.Roster,
		Primary:           j.Primary,
		Secondaries:       j.Secondaries,
		JoinDate:          j.JoinDate,
		PromotionDate:     j.PromotionDate,
		DiscordId:         j.DiscordId,
		AwardDate:         "2021-03-01",
		RecordDate:        "2020-10-17",
		LastForumPostDate: j.LastForumPostDate,
		Mos:               j.Mos,
	}
}

// seedDoeLite mirrors the recording seed's sparse lite profile: unset nested
// messages nil, collections empty, summary dates "".
func seedDoeLite() *types.LiteProfile {
	d := seedDoe()
	return &types.LiteProfile{
		User:            d.User,
		Rank:            d.Rank,
		RealName:        d.RealName,
		UniformUrl:      d.UniformUrl,
		Roster:          d.Roster,
		Secondaries:     []*types.Position{},
		JoinDate:        d.JoinDate,
		ConsoleGamertag: d.ConsoleGamertag,
	}
}

// seedJarvisS1Uniforms mirrors the recording seed's S1 uniforms view: the
// tool-specific shape (S1UniformsRank without rankId, title-only secondary
// positions, the uniform dates and area of responsibility).
func seedJarvisS1Uniforms() *types.S1UniformsProfile {
	return &types.S1UniformsProfile{
		User:                     &types.User{UserId: 3, Username: "Jarvis.A"},
		Rank:                     &types.S1UniformsRank{RankShort: "MG", RankFull: "Major General", RankImageUrl: "https://7cav.us/data/roster_ranks/0/4.jpg?1741364618"},
		RealName:                 "Adam Jarvis",
		UniformUrl:               "https://7cav.us/data/roster_uniforms/0/1.jpg",
		UniformDate:              "2025-11-02",
		UniformUpdateTriggerDate: "2025-12-01",
		Roster:                   types.RosterTypeCombat,
		PrimaryPositionTitle:     "Regimental Technical Aide",
		Secondaries:              []*types.S1UniformsPosition{{PositionTitle: "S6 Web Developer"}},
		JoinDate:                 "2014-02-08",
		PromotionDate:            "2020-10-17",
		AreaOfResponsibility:     "S6",
	}
}

func seedDoeS1Uniforms() *types.S1UniformsProfile {
	return &types.S1UniformsProfile{
		User:                 &types.User{UserId: 8, Username: "John.Doe"},
		Rank:                 &types.S1UniformsRank{RankShort: "PVT", RankFull: "Private", RankImageUrl: "https://7cav.us/data/roster_ranks/0/22.jpg"},
		RealName:             "John Doe",
		UniformUrl:           "https://7cav.us/data/roster_uniforms/0/2.jpg",
		Roster:               types.RosterTypeCombat,
		PrimaryPositionTitle: "Rifleman",
		Secondaries:          []*types.S1UniformsPosition{},
		JoinDate:             "2026-01-15",
	}
}
