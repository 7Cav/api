package xenforo

const (
	ConnectedAccountJoin = "JOIN xf_user_connected_account on xf_user_connected_account.user_id = xf_nf_rosters_user.user_id"
)

type ConnectedAccount struct {
	UserID      uint64 `gorm:"primaryKey;autoIncrement:false"`
	Provider    string `gorm:"primaryKey;autoIncrement:false"`
	ProviderKey string
	ExtraData   []byte
}

func (ca ConnectedAccount) TableName() string {
	return "xf_user_connected_account"
}
