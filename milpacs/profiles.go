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
	"encoding/json"
	"fmt"
	"log"
	"math"
)

type Profile struct {
	RelationId uint64 `gorm:"primaryKey"`
	RosterId uint64
	UserID uint64
	Username string
	RealName string
	PositionID uint64
	SecondaryPositionIds string
	RankID uint64
	Bio string
	UniformDate int
	AddedDate int
	CustomFields string

	XfUser XfUser `gorm:"foreignKey:UserID;references:user_id"`
	Primary Position `gorm:"foreignKey:PositionID;references:position_id"`
	Rank Rank `gorm:"foreignKey:RankID;references:rank_id"`
	Records []Record `gorm:"foreignKey:RelationID"`
	AwardRecords []AwardRecord `gorm:"foreignKey:RelationID"`
}
func (Profile) TableName() string {
	return "xf_nf_rosters_user"
}

func (profile *Profile) UniformUrl() string {
	imageGroup := math.Floor(float64(profile.RelationId / 1000))
	return fmt.Sprintf("https://7cav.us/data/roster_uniforms/%d/%d.jpg", int(imageGroup), profile.RelationId)
}

type CustomFields struct {
	JoinDate  string `json:"joinDate"`
	PromoDate string `json:"promoDate"`
}

func (profile *Profile) UnmarshalCustomFields() CustomFields {
	s := profile.CustomFields
	fields := CustomFields{}
	err := json.Unmarshal([]byte(s), &fields)
	if err != nil {
		log.Print("Error unmarshalling profile custom fields")
	}
	return fields
}
