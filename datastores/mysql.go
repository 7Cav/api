package datastores

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/7cav/api/milpacs"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/xenforo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
		return nil, result.Error
	}

	profiles, err := ds.processProfiles([]milpacs.Profile{profile})
	if err != nil {
		return nil, fmt.Errorf("error generating profile: %w", err)
	}

	return []*proto.Profile{profiles[profile.RelationId]}, nil

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
		return nil, result.Error
	}

	profiles, err := ds.processProfiles([]milpacs.Profile{profile})
	if err != nil {
		return nil, fmt.Errorf("error generating profile: %w", err)
	}

	return []*proto.Profile{profiles[profile.RelationId]}, nil

}

func (ds Mysql) FindRosterByType(rosterType proto.RosterType) (*proto.Roster, error) {
	var rosterProfiles []milpacs.Profile

	Info.Println("Searching for roster: ", rosterType.String(), "id:", uint(rosterType.Number()))
	ds.Db.Preload(clause.Associations).
		Preload("AwardRecords.Award").
		Joins(xenforo.ConnectedAccountJoin).
		Where(map[string]interface{}{"roster_id": uint(rosterType.Number())}).
		Find(&rosterProfiles)

	profiles, err := ds.processProfiles(rosterProfiles)
	if err != nil {
		return nil, fmt.Errorf("error generating profiles: %w", err)
	}

	return &proto.Roster{Profiles: profiles}, nil
}

func (ds Mysql) FindProfileByKeycloakID(keycloakId string) (*proto.Profile, error) {
	var profile milpacs.Profile

	Info.Println("Searching for milpac profiles with keycloak IDs of: ", keycloakId)

	// gorm 1.26+ qualifies map-keyed WHERE columns with the current model's table,
	// turning "xf_user_connected_account.provider" into a broken three-part qualifier.
	// Use placeholder SQL so the joined-table columns stay unqualified.
	result := ds.Db.Preload(clause.Associations).
		Preload("AwardRecords.Award").
		Joins(xenforo.ConnectedAccountJoin).
		Where("xf_user_connected_account.provider = ? AND xf_user_connected_account.provider_key = ?", "keycloak", keycloakId).
		First(&profile)

	if result.Error != nil {
		return nil, result.Error
	}

	profiles, err := ds.processProfiles([]milpacs.Profile{profile})
	if err != nil {
		return nil, fmt.Errorf("error generating profile")
	}

	return profiles[profile.RelationId], nil
}

func (ds Mysql) FindProfileByDiscordID(discordId string) (*proto.Profile, error) {
	var profile milpacs.Profile

	Info.Printf("Searching for milpac profiles with discord IDs of: %s", discordId)

	// See note in FindProfileByKeycloakID — gorm 1.26+ misqualifies dotted map keys.
	result := ds.Db.Preload(clause.Associations).
		Preload("AwardRecords.Award").
		Joins(xenforo.ConnectedAccountJoin).
		Where("xf_user_connected_account.provider = ? AND xf_user_connected_account.provider_key = ?", "nfDiscord", discordId).
		First(&profile)

	if result.Error != nil {
		return nil, result.Error
	}

	profiles, err := ds.processProfiles([]milpacs.Profile{profile})
	if err != nil {
		return nil, fmt.Errorf("error generating profile")
	}

	return profiles[profile.RelationId], nil
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
		RealName:   profile.UnmarshalCustomFields().RealName,
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
		Mos:           profile.UnmarshalCustomFields().Mos,
		ConsoleGamertag: profile.UnmarshalCustomFields().ConsoleGamertag,
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

	profiles, err := ds.processLiteProfiles(rosterProfiles)
	if err != nil {
		return nil, err
	}

	return &proto.LiteRoster{Profiles: profiles}, nil
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
		RealName:   profile.UnmarshalCustomFields().RealName,
		UniformUrl: profile.UniformUrl(),
		Roster:     proto.RosterType(profile.RosterId),
		Primary: &proto.Position{
			PositionTitle: profile.Primary.PositionTitle,
			PositionId:    profile.Primary.PositionId,
		},
		Secondaries:   ds.collectSecondaryPositions(profile.SecondaryPositionIds),
		JoinDate:      profile.UnmarshalCustomFields().JoinDate,
		PromotionDate: profile.UnmarshalCustomFields().PromoDate,
		Mos:           profile.UnmarshalCustomFields().Mos,
		ConsoleGamertag: profile.UnmarshalCustomFields().ConsoleGamertag,
		KeycloakId:    extractKeycloakID(profile),
		DiscordId:     extractDiscordID(profile),
		AwardDate:     getLatestAwardDate(profile),
		RecordDate:    getLatestServiceRecordDate(profile),
		// Last forum post timestamp generated externally to use batch processing
	}

	return milpac, nil
}

func (ds Mysql) FindProfilesByPosition(positionQuery string) (*proto.LiteRoster, error) {
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

	profileMap, err := ds.processLiteProfiles(profiles)
	if err != nil {
		return nil, err
	}

	return &proto.LiteRoster{Profiles: profileMap}, nil
}

func (ds Mysql) FindS1UniformsRosterByType(rosterType proto.RosterType) (*proto.S1UniformsRoster, error) {
	var rosterProfiles []milpacs.Profile

	Info.Println("Searching for S1 Uniforms roster: ", rosterType.String(), "id:", uint(rosterType.Number()))
	ds.Db.Preload(clause.Associations).
		Preload("Primary.Group").
		Preload("AwardRecords.Award").
		Joins(xenforo.ConnectedAccountJoin).
		Where(map[string]interface{}{"roster_id": uint(rosterType.Number())}).
		Find(&rosterProfiles)

	var profiles = make(map[uint64]*proto.S1UniformsProfile, len(rosterProfiles))
	for _, profile := range rosterProfiles {
		milpac, err := ds.generateS1UniformsProtoProfile(profile)

		if err != nil {
			return nil, fmt.Errorf("error generating profile")
		}
		profiles[profile.RelationId] = milpac
	}

	protoRoster := &proto.S1UniformsRoster{Profiles: profiles}

	return protoRoster, nil
}

func (ds Mysql) generateS1UniformsProtoProfile(profile milpacs.Profile) (*proto.S1UniformsProfile, error) {
	milpac := &proto.S1UniformsProfile{
		User: &proto.User{
			UserId:   profile.XfUser.UserID,
			Username: profile.XfUser.Username,
		},
		Rank: &proto.S1UniformsRank{
			RankShort:    strings.TrimPrefix(proto.RankType(profile.RankID).String(), "RANK_TYPE_"),
			RankFull:     profile.Rank.Title,
			RankImageUrl: profile.Rank.ImageURL(),
		},
		RealName:   profile.UnmarshalCustomFields().RealName,
		UniformUrl:               profile.UniformUrl(),
		UniformDate:              getUniformDate(profile),
		UniformUpdateTriggerDate: getUniformUpdateTriggerDate(profile),
		Roster:                   proto.RosterType(profile.RosterId),
		PrimaryPositionTitle:     profile.Primary.PositionTitle,
		Secondaries:              ds.collectS1UniformsSecondaryPositions(profile.SecondaryPositionIds),
		JoinDate:                 profile.UnmarshalCustomFields().JoinDate,
		PromotionDate:            profile.UnmarshalCustomFields().PromoDate,
		AreaOfResponsibility:     getPositionGroup(profile),
	}

	return milpac, nil
}

func (ds Mysql) collectS1UniformsSecondaryPositions(positionIds string) []*proto.S1UniformsPosition {
	var positions []*proto.S1UniformsPosition

	if positionIds == "" {
		return positions
	}

	for _, id := range strings.Split(positionIds, ",") {
		var position milpacs.Position
		ds.Db.First(&position, id)
		positions = append(positions, &proto.S1UniformsPosition{
			PositionTitle: position.PositionTitle,
		})
	}
	return positions
}

func getUniformDate(profile milpacs.Profile) string {
	if profile.UniformDate <= 0 {
		return ""
	}
	return time.Unix(int64(profile.UniformDate), 0).Format("2006-01-02 15:04:05")
}

func getUniformUpdateTriggerDate(profile milpacs.Profile) string {
	relevantRecordTypes := map[proto.RecordType]bool{
		proto.RecordType_RECORD_TYPE_PROMOTION:   true,
		proto.RecordType_RECORD_TYPE_ASSIGNMENT:  true,
		proto.RecordType_RECORD_TYPE_ELOA:        true,
		proto.RecordType_RECORD_TYPE_NAME_CHANGE: true,
		proto.RecordType_RECORD_TYPE_GRADUATION:  true,
	}

	var latestTimestamp int64

	for _, award := range profile.AwardRecords {
		if int64(award.AwardDate) > latestTimestamp {
			latestTimestamp = int64(award.AwardDate)
		}
	}

	for _, record := range profile.Records {
		if relevantRecordTypes[proto.RecordType(record.RecordTypeId)] && int64(record.RecordDate) > latestTimestamp {
			latestTimestamp = int64(record.RecordDate)
		}
	}

	if latestTimestamp == 0 {
		return ""
	}
	return time.Unix(latestTimestamp, 0).Format("2006-01-02 15:04:05")
}

func getPositionGroup(profile milpacs.Profile) string {
	primaryGroup := profile.Primary.Group.Title

	switch primaryGroup {
	case "Regimental HQ", "Support Attachment":
		return "HHQ"
	case "New Recruits":
		return "RTC"
	case "7th Cavalry Reservists":
		return "Reserve"
	case "Extended Leave Of Absence":
		return "ELOA"
	}

	if strings.Contains(primaryGroup, "Command") {
		return "HHQ"
	}

	return primaryGroup
}

func (ds Mysql) FindAllRanks() ([]*proto.RankExpanded, error) {
	Info.Println("Searching for all ranks")
	var ranks []milpacs.Rank

	result := ds.Db.Order("display_order").Find(&ranks)
	if result.Error != nil {
		return nil, fmt.Errorf("error fetching ranks: %w", result.Error)
	}

	protoRanks := make([]*proto.RankExpanded, len(ranks))
	for i, rank := range ranks {
		protoRanks[i] = &proto.RankExpanded{
			RankId:           rank.RankId,
			RankShort:        strings.TrimPrefix(proto.RankType(rank.RankId).String(), "RANK_TYPE_"),
			RankFull:         rank.Title,
			RankImageUrl:     rank.ImageURL(),
			RankDisplayOrder: uint32(rank.DisplayOrder),
		}
	}

	return protoRanks, nil
}

func (ds Mysql) FindAllPositionGroups() ([]*proto.PositionGroup, error) {
	Info.Println("Searching for all position groups")
	var groups []milpacs.PositionGroups

	result := ds.Db.Order("display_order").Find(&groups)
	if result.Error != nil {
		return nil, fmt.Errorf("error fetching position groups: %w", result.Error)
	}

	protoGroups := make([]*proto.PositionGroup, len(groups))

	for i, group := range groups {
		var positions []milpacs.Position

		posResult := ds.Db.Where("position_group_id = ? AND position_title NOT LIKE ?", group.PositionGroupId, "%----%").
			Order("display_order").
			Find(&positions)

		if posResult.Error != nil {
			return nil, fmt.Errorf("error fetching positions for group %d: %w",
				group.PositionGroupId, posResult.Error)
		}

		protoPositions := make([]*proto.PositionExpanded, len(positions))
		for j, pos := range positions {
			protoPositions[j] = &proto.PositionExpanded{
				PositionId:                pos.PositionId,
				PositionTitle:             pos.PositionTitle,
				PositionDisplayOrder:      uint32(pos.DisplayOrder),
				PositionPossibleSecondary: pos.PossibleSecondary,
			}
		}

		protoGroups[i] = &proto.PositionGroup{
			GroupId:           group.PositionGroupId,
			GroupTitle:        group.Title,
			GroupDisplayOrder: uint32(group.DisplayOrder),
			Positions:         protoPositions,
		}
	}

	return protoGroups, nil
}

func getLatestServiceRecordDate(profile milpacs.Profile) string {
	var latestTimestamp int64

	for _, record := range profile.Records {
		if int64(record.RecordDate) > latestTimestamp {
			latestTimestamp = int64(record.RecordDate)
		}
	}

	if latestTimestamp == 0 {
		return ""
	}
	return time.Unix(latestTimestamp, 0).Format("2006-01-02 15:04:05")
}

func getLatestAwardDate(profile milpacs.Profile) string {
	var latestTimestamp int64

	for _, award := range profile.AwardRecords {
		if int64(award.AwardDate) > latestTimestamp {
			latestTimestamp = int64(award.AwardDate)
		}
	}

	if latestTimestamp == 0 {
		return ""
	}
	return time.Unix(latestTimestamp, 0).Format("2006-01-02 15:04:05")
}

// bear witness to my despair, as i try to optimize queries to a table with a gazillion rows
func (ds Mysql) getLatestForumPostDates(profiles []milpacs.Profile) map[uint64]string {
	dates := make(map[uint64]string)

	var results []struct {
		UserID   uint64 `gorm:"column:user_id"`
		PostDate uint32 `gorm:"column:date"`
	}

	query := ds.Db.Table("xf_nf_rosters_user as milpacs").
		Select("milpacs.user_id, posts.date").
		Joins("LEFT JOIN (SELECT user_id, MAX(post_date) as date FROM xf_post GROUP BY user_id) as posts ON milpacs.user_id = posts.user_id").
		Where("milpacs.user_id IN ?", getUserIDs(profiles))

	if err := query.Find(&results).Error; err != nil {
		Error.Printf("Error fetching forum post dates: %v", err)
		return dates
	}

	for _, result := range results {
		if result.PostDate > 0 {
			dates[result.UserID] = time.Unix(int64(result.PostDate), 0).Format("2006-01-02 15:04:05")
		} else {
			dates[result.UserID] = ""
		}
	}

	return dates
}

func getUserIDs(profiles []milpacs.Profile) []uint64 {
	userIDs := make([]uint64, len(profiles))
	for i, profile := range profiles {
		userIDs[i] = profile.UserID
	}
	return userIDs
}

// ohgodwhy
func (ds Mysql) processLiteProfiles(profiles []milpacs.Profile) (map[uint64]*proto.LiteProfile, error) {
	forumPostDates := ds.getLatestForumPostDates(profiles)

	var profileMap = make(map[uint64]*proto.LiteProfile, len(profiles))
	for _, profile := range profiles {
		protoProfile, err := ds.generateLiteProtoProfile(profile)
		if err != nil {
			return nil, fmt.Errorf("error generating lite profile: %w", err)
		}

		protoProfile.LastForumPostDate = forumPostDates[profile.UserID]
		profileMap[profile.RelationId] = protoProfile
	}

	return profileMap, nil
}

func (ds Mysql) processProfiles(profiles []milpacs.Profile) (map[uint64]*proto.Profile, error) {
	forumPostDates := ds.getLatestForumPostDates(profiles)

	var profileMap = make(map[uint64]*proto.Profile, len(profiles))
	for _, profile := range profiles {
		protoProfile, err := ds.generateProtoProfile(profile)
		if err != nil {
			return nil, fmt.Errorf("error generating lite profile: %w", err)
		}

		protoProfile.LastForumPostDate = forumPostDates[profile.UserID]
		profileMap[profile.RelationId] = protoProfile
	}

	return profileMap, nil
}

func (ds Mysql) FindAwol() ([]*proto.Awol, error) {
	Info.Println("Searching for AWOL troopers")
	var awols []struct {
		GroupName  string `gorm:"column:group_name"`
		RankName   string `gorm:"column:rank_name"`
		Username   string `gorm:"column:username"`
		UserID     uint64 `gorm:"column:user_id"`
		Date       uint64 `gorm:"column:date"`
		PostID     uint64 `gorm:"column:post_id"`
		HumanDate  string `gorm:"column:human_date"`
		RelationID uint64 `gorm:"column:relation_id"`
	}

	sevenDaysAgo := time.Now().AddDate(0, 0, -7)
	cutoffTimestamp := sevenDaysAgo.Unix()

	result := ds.Db.Table("xf_nf_rosters_user as milpacs").
		Select(`
            pGroup.title as group_name,
            ranks.title as rank_name,
            users.username,
            milpacs.user_id,
			milpacs.relation_id,
            posts.date,
            posts.post_id,
            FROM_UNIXTIME(posts.date, '%Y-%m-%d') as human_date
        `).
		Joins("LEFT JOIN (SELECT user_id, MAX(post_date) as date, MAX(post_id) as post_id FROM xf_post GROUP BY user_id) as posts ON milpacs.user_id = posts.user_id").
		Joins("LEFT JOIN xf_user as users ON milpacs.user_id = users.user_id").
		Joins("INNER JOIN xf_nf_rosters_rank as ranks ON milpacs.rank_id = ranks.rank_id").
		Joins("INNER JOIN xf_nf_rosters_position position ON milpacs.position_id = position.position_id").
		Joins("INNER JOIN xf_nf_rosters_position_group pGroup ON position.position_group_id = pGroup.position_group_id").
		Where("milpacs.roster_id IN (?)", []int{1, 2}).
		Where("posts.date <= ?", cutoffTimestamp).
		Find(&awols)

	if result.Error != nil {
		return nil, fmt.Errorf("error finding AWOL users: %w", result.Error)
	}

	protoAwols := make([]*proto.Awol, len(awols))
	for i, awol := range awols {
		protoAwols[i] = &proto.Awol{
			GroupName: awol.GroupName,
			RankName:  awol.RankName,
			Username:  awol.Username,
			UserId:    awol.UserID,
			Timestamp: awol.Date,
			PostId:    awol.PostID,
			HumanDate: awol.HumanDate,
			MilpacId:  awol.RelationID,
		}
	}

	return protoAwols, nil
}

func (ds Mysql) GetTableUpdates() ([]xenforo.TableInfo, error) {
	var updates []xenforo.TableInfo
	ds.Db.Table("information_schema.tables").
		Select("table_name, update_time").
		Find(&updates)
	return updates, nil
}

func (ds Mysql) ValidateApiKey(rawKey string) (*ApiKeyResult, error) {
	var rows []struct {
		KeyId     uint   `gorm:"column:key_id"`
		UserId    uint   `gorm:"column:user_id"`
		ScopeName string `gorm:"column:scope_name"`
	}
	tx := ds.Db.Raw(`
		SELECT k.key_id, k.user_id, sd.scope_name
		FROM   xf_cav7_api_key k
		JOIN   xf_cav7_api_key_scope ks     ON ks.key_id   = k.key_id
		JOIN   xf_cav7_api_key_scope_def sd ON sd.scope_id = ks.scope_id
		WHERE  k.key_hash   = UNHEX(SHA2(?, 256))
		  AND  k.is_active  = 1
		  AND  sd.is_active = 1`, rawKey).Scan(&rows)
	if tx.Error != nil {
		return nil, tx.Error
	}
	if len(rows) == 0 {
		return nil, nil
	}
	scopes := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		scopes[r.ScopeName] = struct{}{}
	}
	keyId := rows[0].KeyId
	go ds.Db.Exec(`UPDATE xf_cav7_api_key SET last_used_date = UNIX_TIMESTAMP() WHERE key_id = ?`, keyId)
	return &ApiKeyResult{
		KeyId:  keyId,
		UserId: rows[0].UserId,
		Scopes: scopes,
	}, nil
}

func (ds Mysql) FindProfileByGamertag(gamertag string) (*proto.Profile, error) {
	var profile milpacs.Profile

	Info.Println("Searching for user with gamertag: ", gamertag)

	result := ds.Db.Preload(clause.Associations).
		Preload("AwardRecords.Award").
		Joins(xenforo.ConnectedAccountJoin).
		Joins(milpacs.FieldValueJoin).
		Where("xf_nf_rosters_field_value.field_id = ? AND xf_nf_rosters_field_value.field_value LIKE ?", "consoleGamertag", gamertag).
		First(&profile)

	if result.Error != nil {
		return nil, result.Error
	}

	profiles, err := ds.processProfiles([]milpacs.Profile{profile})
	if err != nil {
		return nil, fmt.Errorf("error generating profile: %w", err)
	}

	return profiles[profile.RelationId], nil
}
