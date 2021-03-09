package milpacs

type Record struct {
	RecordID uint64 `gorm:"primaryKey"`
	RelationID uint64
	Details string
	RecordDate uint
	CitationDate uint
	RecordTypeId uint64
}

func (Record) TableName() string {
	return "xf_nf_rosters_service_record"
}

type RecordType struct {
	RecordTypeId uint64 `gorm:"primaryKey"`
	Title string
	DisplayOrder uint
}

func (RecordType) TableName() string {
	return "xf_nf_rosters_record_type"
}
