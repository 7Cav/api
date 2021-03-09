package milpacs

type Position struct {
	PositionId uint64 `gorm:"primaryKey"`
	PositionTitle string
	PositionGroupId uint64
	DisplayOrder uint
	MaterializedOrder uint
	ExtraGroupIds string
	PossibleSecondary bool
}
func (Position) TableName() string {
	return "xf_nf_rosters_position"
}