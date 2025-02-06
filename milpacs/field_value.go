package milpacs

const (
	FieldValueJoin = "LEFT JOIN xf_nf_rosters_field_value on xf_nf_rosters_field_value.relation_id = xf_nf_rosters_user.relation_id"
)

type FieldValue struct {
	RelationId uint64 `gorm:"primaryKey"`
	FieldId    uint64
	FieldValue string
}

func (ca FieldValue) TableName() string {
	return "xf_nf_rosters_field_value"
}
