package xenforo

// UsernameChange models the XenForo username change table,
// xf_username_change, which records each change a member asks for and its
// state. It maps only the columns the LOA route reads.
type UsernameChange struct {
	ChangeID    uint32 `gorm:"column:change_id;primaryKey"`
	UserID      uint32 `gorm:"column:user_id"`
	OldUsername string `gorm:"column:old_username"`
	ChangeState string `gorm:"column:change_state"`
	ChangeDate  uint32 `gorm:"column:change_date"`
}

func (UsernameChange) TableName() string { return "xf_username_change" }
