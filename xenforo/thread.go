package xenforo

// Thread models the XenForo thread table, xf_thread. It maps only the columns
// the LOA route reads, which are the thread's forum, post time, visibility
// and opening post.
type Thread struct {
	ThreadID        uint32 `gorm:"column:thread_id;primaryKey"`
	NodeID          uint32 `gorm:"column:node_id"`
	PostDate        uint32 `gorm:"column:post_date"`
	DiscussionState string `gorm:"column:discussion_state"`
	FirstPostID     uint32 `gorm:"column:first_post_id"`
}

func (Thread) TableName() string { return "xf_thread" }
