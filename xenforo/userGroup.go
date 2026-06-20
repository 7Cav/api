package xenforo

// ForumGroup models the XenForo permission-group directory table
// (xf_user_group). Only the id and its display title are needed for the
// forum-group directory (GET /api/v1/forum/groups); the rest of the table
// (user_title, banner/CSS, NF-server group ids, …) is out of scope (ADR 0007).
type ForumGroup struct {
	UserGroupID uint32 `gorm:"column:user_group_id;primaryKey"`
	Title       string `gorm:"column:title"`
}

func (ForumGroup) TableName() string {
	return "xf_user_group"
}
