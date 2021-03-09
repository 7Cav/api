package milpacs

import (
	"fmt"
	"math"
)

type Rank struct {
	RankId uint64 `gorm:"primaryKey"`
	Title string
	RankImage uint
	DisplayOrder uint
	ExtraGroupIds string
}
func (Rank) TableName() string {
	return "xf_nf_rosters_rank"
}

func (rank *Rank) ImageURL() string {
	imageGroup := math.Floor(float64(rank.RankId / 1000))
	return fmt.Sprintf("https://7cav.us/data/roster_ranks/%d/%d.jpg?%d", int(imageGroup), rank.RankId, rank.RankImage)
}