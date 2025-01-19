package xenforo

type ForumPost struct {
	PostID   uint64 `gorm:"primaryKey;autoIncrement:false"`
	UserID   uint64
	PostDate uint
}

func (ForumPost) TableName() string {
	return "xf_post"
}
