package datastores

import (
	"fmt"
	"github.com/7cav/api/milpacs"
	"github.com/7cav/api/proto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strconv"
	"strings"
	"time"
)

type Mysql struct {
	Db *gorm.DB
}

const (
	layoutISO = "2006-01-02"
)

func (ds Mysql) FindProfilesById(userIds ...uint64) ([]*proto.Profile, error) {

	var profile milpacs.Profile

	Info.Println("Searching for user: ", userIds[0])
	ds.Db.Preload(clause.Associations).First(&profile, userIds[0])

	milpac, err := ds.generateProtoProfile(profile)

	if err != nil {
		return nil, fmt.Errorf("error generating profile")
	}

	return []*proto.Profile{milpac}, nil
}

func (ds Mysql) FindRosterByType(rosterType proto.RosterType) (*proto.Roster, error) {
	var rosterProfiles []milpacs.Profile

	Info.Println("Searching for roster: ", rosterType.String(), "id:", uint(rosterType.Number()))
	ds.Db.Preload(clause.Associations).Preload("AwardRecords.Award").Where(map[string]interface{}{"roster_id": uint(rosterType.Number())}).Find(&rosterProfiles)

	var profiles = make(map[uint64]*proto.Profile, len(rosterProfiles))
	for _, profile := range rosterProfiles {
		milpac, err := ds.generateProtoProfile(profile)

		if err != nil {
			return nil, fmt.Errorf("error generating profile")
		}
		profiles[profile.RelationId] = milpac
	}

	protoRoster := &proto.Roster{Profiles: profiles}

	return protoRoster, nil
}

func (ds Mysql) generateProtoProfile(profile milpacs.Profile) (*proto.Profile, error) {
	milpac := &proto.Profile{
		User: &proto.User{
			UserId:   profile.XfUser.UserID,
			Username: profile.XfUser.Username,
		},
		Rank: &proto.Rank{
			RankShort:    proto.RankType(profile.RankID).String(),
			RankFull:     profile.Rank.Title,
			RankImageUrl: profile.Rank.ImageURL(),
		},
		RealName:   profile.RealName,
		UniformUrl: profile.UniformUrl(),
		Roster:     proto.RosterType(profile.RosterId),
		Primary: &proto.Position{
			PositionTitle: profile.Primary.PositionTitle,
		},
		Secondaries:   ds.collectSecondaryPositions(profile.SecondaryPositionIds),
		Records:       collectRecords(profile.Records),
		Awards:        collectAwards(profile.AwardRecords),
		JoinDate:      profile.UnmarshalCustomFields().JoinDate,
		PromotionDate: profile.UnmarshalCustomFields().PromoDate,
	}

	return milpac, nil
}

func (ds Mysql) collectSecondaryPositions(positionIds string) []*proto.Position {
	var positions []*proto.Position

	if positionIds == "" {
		return positions
	}

	for _, id := range strings.Split(positionIds, ",") {
		var position milpacs.Position
		ds.Db.First(&position, id)
		positions = append(positions, &proto.Position{PositionTitle: position.PositionTitle})
	}
	return positions
}

func collectRecords(recordRows []milpacs.Record) []*proto.Record {
	var records []*proto.Record

	for _, recordRow := range recordRows {
		record := &proto.Record{
			RecordDetails: recordRow.Details,
			RecordType:    proto.RecordType(recordRow.RecordTypeId),
			RecordDate:    stringToTime(strconv.Itoa(int(recordRow.RecordDate))).Format(layoutISO),
		}
		records = append(records, record)
	}

	return records
}

func collectAwards(awardRows []milpacs.AwardRecord) []*proto.Award {
	var awards []*proto.Award

	for _, awardRow := range awardRows {
		award := &proto.Award{
			AwardName:     awardRow.Award.Title,
			AwardDetails:  awardRow.Details,
			AwardDate:     stringToTime(strconv.Itoa(int(awardRow.AwardDate))).Format(layoutISO),
			AwardImageUrl: awardRow.Award.ImageURL(),
		}
		awards = append(awards, award)
	}

	return awards
}

func stringToTime(s string) time.Time {
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		Error.Println("error converting time", err)
		return time.Time{}
	}
	return time.Unix(sec, 0)
}
