package datastores

import (
	"fmt"
	"github.com/7cav/api/milpacs"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/xenforo"
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
	result := ds.Db.Preload(clause.Associations).
		Preload("AwardRecords.Award").
		Joins(xenforo.ConnectedAccountJoin).
		First(&profile, userIds[0])

	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("no profile found for userid: %d", userIds)
		}
		return nil, result.Error
	}

	milpac, err := ds.generateProtoProfile(profile)

	if err != nil {
		return nil, fmt.Errorf("error generating profile")
	}

	return []*proto.Profile{milpac}, nil
}

func (ds Mysql) FindProfilesByUsername(username string) ([]*proto.Profile, error) {
	var profile milpacs.Profile

	Info.Println("Searching for user with username: ", username)

	result := ds.Db.Preload(clause.Associations).
		Preload("AwardRecords.Award").
		Joins("JOIN xf_user ON xf_user.user_id = xf_nf_rosters_user.user_id").
		Joins(xenforo.ConnectedAccountJoin).
		Where("xf_user.username = ?", username).
		First(&profile)

	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("no profile found for username: %s", username)
		}
		return nil, result.Error
	}

	milpac, err := ds.generateProtoProfile(profile)
	if err != nil {
		return nil, fmt.Errorf("error generating profile: %w", err)
	}

	return []*proto.Profile{milpac}, nil
}

func (ds Mysql) FindRosterByType(rosterType proto.RosterType) (*proto.Roster, error) {
	var rosterProfiles []milpacs.Profile

	Info.Println("Searching for roster: ", rosterType.String(), "id:", uint(rosterType.Number()))
	ds.Db.Preload(clause.Associations).
		Preload("AwardRecords.Award").
		Joins(xenforo.ConnectedAccountJoin).
		Where(map[string]interface{}{"roster_id": uint(rosterType.Number())}).
		Find(&rosterProfiles)

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

func (ds Mysql) FindProfileByKeycloakID(keycloakId string) (*proto.Profile, error) {
	var profile milpacs.Profile

	Info.Println("Searching for milpac profiles with keycloak IDs of: %s", keycloakId)

	query := map[string]interface{}{"xf_user_connected_account.provider_key": keycloakId, "xf_user_connected_account.provider": "keycloak"}

	result := ds.Db.Preload(clause.Associations).
		Preload("AwardRecords.Award").
		Joins(xenforo.ConnectedAccountJoin).
		Where(query).
		First(&profile)

	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("no profile found for KeycloakID: %s", keycloakId)
		}
		return nil, result.Error
	}

	milpac, err := ds.generateProtoProfile(profile)

	if err != nil {
		return nil, fmt.Errorf("error generating profile")
	}

	return milpac, nil
}

func (ds Mysql) FindProfileByDiscordID(discordId string) (*proto.Profile, error) {
	var profile milpacs.Profile

	Info.Println("Searching for milpac profiles with discord IDs of: %s", discordId)

	query := map[string]interface{}{"xf_user_connected_account.provider_key": discordId, "xf_user_connected_account.provider": "nfDiscord"}

	result := ds.Db.Preload(clause.Associations).
		Preload("AwardRecords.Award").
		Joins(xenforo.ConnectedAccountJoin).
		Where(query).
		First(&profile)

	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("no profile found for discordID: %s", discordId)
		}
		return nil, result.Error
	}

	milpac, err := ds.generateProtoProfile(profile)

	if err != nil {
		return nil, fmt.Errorf("error generating profile")
	}

	return milpac, nil
}

func (ds Mysql) generateProtoProfile(profile milpacs.Profile) (*proto.Profile, error) {
	milpac := &proto.Profile{
		User: &proto.User{
			UserId:   profile.XfUser.UserID,
			Username: profile.XfUser.Username,
		},
		Rank: &proto.Rank{
			RankId:       profile.RankID,
			RankShort:    strings.TrimPrefix(proto.RankType(profile.RankID).String(), "RANK_TYPE_"),
			RankFull:     profile.Rank.Title,
			RankImageUrl: profile.Rank.ImageURL(),
		},
		RealName:   profile.RealName,
		UniformUrl: profile.UniformUrl(),
		Roster:     proto.RosterType(profile.RosterId),
		Primary: &proto.Position{
			PositionTitle: profile.Primary.PositionTitle,
			PositionId:    profile.Primary.PositionId,
		},
		Secondaries:   ds.collectSecondaryPositions(profile.SecondaryPositionIds),
		Records:       collectRecords(profile.Records),
		Awards:        collectAwards(profile.AwardRecords),
		JoinDate:      profile.UnmarshalCustomFields().JoinDate,
		PromotionDate: profile.UnmarshalCustomFields().PromoDate,
		KeycloakId:    extractKeycloakID(profile),
		DiscordId:     extractDiscordID(profile),
	}

	return milpac, nil
}

func extractKeycloakID(profile milpacs.Profile) string {
	for _, connection := range profile.ConnectedAccount {
		if connection.Provider == "keycloak" {
			return connection.ProviderKey
		}
	}

	return ""
}

func extractDiscordID(profile milpacs.Profile) string {
	for _, connection := range profile.ConnectedAccount {
		if connection.Provider == "nfDiscord" {
			return connection.ProviderKey
		}
	}

	return ""
}

func (ds Mysql) collectSecondaryPositions(positionIds string) []*proto.Position {
	var positions []*proto.Position

	if positionIds == "" {
		return positions
	}

	for _, id := range strings.Split(positionIds, ",") {
		var position milpacs.Position
		ds.Db.First(&position, id)
		positions = append(positions, &proto.Position{
			PositionTitle: position.PositionTitle,
			PositionId:    position.PositionId,
		})
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
			RecordUid:     recordRow.RecordID,
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
			AwardUid:      awardRow.RecordID,
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

func (ds Mysql) FindLiteRosterByType(rosterType proto.RosterType) (*proto.LiteRoster, error) {
	var rosterProfiles []milpacs.Profile

	Info.Println("Searching for lite roster: ", rosterType.String(), "id:", uint(rosterType.Number()))
	ds.Db.Preload(clause.Associations).
		Omit("Records", "AwardRecords").
		Joins(xenforo.ConnectedAccountJoin).
		Where(map[string]interface{}{"roster_id": uint(rosterType.Number())}).
		Find(&rosterProfiles)

	var profiles = make(map[uint64]*proto.LiteProfile, len(rosterProfiles))
	for _, profile := range rosterProfiles {
		milpac, err := ds.generateLiteProtoProfile(profile)

		if err != nil {
			return nil, fmt.Errorf("error generating lite profile")
		}
		profiles[profile.RelationId] = milpac
	}

	protoRoster := &proto.LiteRoster{Profiles: profiles}

	return protoRoster, nil
}
func (ds Mysql) generateLiteProtoProfile(profile milpacs.Profile) (*proto.LiteProfile, error) {
	milpac := &proto.LiteProfile{
		User: &proto.User{
			UserId:   profile.XfUser.UserID,
			Username: profile.XfUser.Username,
		},
		Rank: &proto.Rank{
			RankId:       profile.RankID,
			RankShort:    strings.TrimPrefix(proto.RankType(profile.RankID).String(), "RANK_TYPE_"),
			RankFull:     profile.Rank.Title,
			RankImageUrl: profile.Rank.ImageURL(),
		},
		RealName:   profile.RealName,
		UniformUrl: profile.UniformUrl(),
		Roster:     proto.RosterType(profile.RosterId),
		Primary: &proto.Position{
			PositionTitle: profile.Primary.PositionTitle,
			PositionId:    profile.Primary.PositionId,
		},
		Secondaries:   ds.collectSecondaryPositions(profile.SecondaryPositionIds),
		JoinDate:      profile.UnmarshalCustomFields().JoinDate,
		PromotionDate: profile.UnmarshalCustomFields().PromoDate,
		KeycloakId:    extractKeycloakID(profile),
		DiscordId:     extractDiscordID(profile),
	}

	return milpac, nil
}

func (ds Mysql) FindProfilesByPosition(positionQuery string) ([]*proto.LiteProfile, error) {
	var profiles []milpacs.Profile

	Info.Printf("Searching for profiles with position matching: %s", positionQuery)

	escaped := strings.ReplaceAll(positionQuery, "%", "\\%")
	escaped = strings.ReplaceAll(escaped, "_", "\\_")
	likeQuery := "%" + escaped + "%"

	result := ds.Db.Preload(clause.Associations).
		Omit("Records", "AwardRecords").
		Joins(xenforo.ConnectedAccountJoin).
		Joins("LEFT JOIN xf_nf_rosters_position pos ON pos.position_id = xf_nf_rosters_user.position_id OR FIND_IN_SET(pos.position_id, xf_nf_rosters_user.secondary_position_ids)").
		Where("pos.position_title LIKE ? AND (pos.position_id = xf_nf_rosters_user.position_id OR pos.possible_secondary = ?)",
			likeQuery, true).
		Find(&profiles)

	if result.Error != nil {
		return nil, result.Error
	}

	var protoProfiles []*proto.LiteProfile
	for _, profile := range profiles {
		protoProfile, err := ds.generateLiteProtoProfile(profile)
		if err != nil {
			return nil, fmt.Errorf("error generating lite profile: %w", err)
		}
		protoProfiles = append(protoProfiles, protoProfile)
	}

	return protoProfiles, nil
}
