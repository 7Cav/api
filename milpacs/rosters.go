package milpacs

type Roster struct {
	RosterID uint64 `gorm:"primaryKey"`
	Title string
	Description string
	CreateDate uint
	LastUpdateDate uint
	DisplayOrder uint
	Active bool
	PositionCache string
	FieldCache string
	DefaultPositionId uint
	ExtraGroupIds string

	Profiles []Profile
}
func (Roster) TableName() string {
	return "xf_nf_rosters"
}
