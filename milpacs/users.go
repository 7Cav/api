package milpacs

type XfUser struct {
	UserID uint64 `gorm:"primaryKey"`
	Username string
}
func (XfUser) TableName() string {
	return "xf_user"
}
