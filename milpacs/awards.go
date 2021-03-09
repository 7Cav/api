package milpacs

import (
	"fmt"
	"math"
)

type Award struct {
	AwardId uint64 `gorm:"primaryKey"`
	Title string
	AwardImage uint
	AwardGroupID uint64
	DisplayOrder uint
	MaterializedOrder uint
}

func (Award) TableName() string {
	return "xf_nf_rosters_award"
}

func (award *Award) ImageURL() string {
	imageGroup := math.Floor(float64(award.AwardId / 1000))
	return fmt.Sprintf("https://7cav.us/data/roster_awards/%d/%d.jpg?%d", int(imageGroup), award.AwardId, award.AwardImage)
}

type AwardRecord struct {
	RecordID uint64
	RelationID uint64
	AwardID uint64
	FromUserID uint64
	Details string
	AwardDate uint
	CitationDate uint
	Award Award `gorm:"foreignKey:AwardID;references:award_id"`
}

func (AwardRecord) TableName() string {
	return "xf_nf_rosters_user_award"
}