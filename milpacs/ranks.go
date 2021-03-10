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