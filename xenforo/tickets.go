package xenforo

// Ticket maps to xf_nf_tickets_ticket. Field names follow the existing
// xenforo/* convention (CamelCase from snake_case columns).
type Ticket struct {
	TicketID            uint32 `gorm:"column:ticket_id;primaryKey"`
	TicketRef           string `gorm:"column:ticket_ref"`
	Title               string `gorm:"column:title"`
	UserID              uint32 `gorm:"column:user_id"`
	Username            string `gorm:"column:username"`
	StartDate           uint32 `gorm:"column:start_date"`
	FirstMessageID      uint32 `gorm:"column:first_message_id"`
	FirstMessageDate    uint32 `gorm:"column:first_message_date"`
	Priority            uint32 `gorm:"column:priority"`
	StatusID            uint32 `gorm:"column:status_id"`
	TicketState         string `gorm:"column:ticket_state"`
	TicketLocked        uint32 `gorm:"column:ticket_locked"`
	DiscussionState     string `gorm:"column:discussion_state"`
	AssignedUserID      uint32 `gorm:"column:assigned_user_id"`
	AssignedUsername    string `gorm:"column:assigned_username"`
	TicketCategoryID    uint32 `gorm:"column:ticket_category_id"`
	LastMessageID       uint32 `gorm:"column:last_message_id"`
	LastMessageDate     uint32 `gorm:"column:last_message_date"`
	LastMessageUserID   uint32 `gorm:"column:last_message_user_id"`
	LastMessageUsername string `gorm:"column:last_message_username"`
	LastModifiedDate    uint32 `gorm:"column:last_modified_date"`
	ReplyCount          uint32 `gorm:"column:reply_count"`
	PrefixID            uint32 `gorm:"column:prefix_id"`
	StarterUserID       uint32 `gorm:"column:starter_user_id"`
	StarterUsername     string `gorm:"column:starter_username"`
	// Custom fields come from the normalized FieldValues, not the blob.
	FieldValues  []TicketFieldValue  `gorm:"foreignKey:TicketID;references:TicketID"`
	Participants []TicketParticipant `gorm:"foreignKey:TicketID;references:TicketID"`
}

func (Ticket) TableName() string { return "xf_nf_tickets_ticket" }

type TicketMessage struct {
	MessageID    uint32 `gorm:"column:message_id;primaryKey"`
	TicketID     uint32 `gorm:"column:ticket_id"`
	UserID       uint32 `gorm:"column:user_id"`
	Username     string `gorm:"column:username"`
	MessageDate  uint32 `gorm:"column:message_date"`
	Message      string `gorm:"column:message"`
	MessageState string `gorm:"column:message_state"`
	Position     uint32 `gorm:"column:position"`
	AttachCount  uint32 `gorm:"column:attach_count"`
	LastEditDate uint32 `gorm:"column:last_edit_date"`
	EditCount    uint32 `gorm:"column:edit_count"`
}

func (TicketMessage) TableName() string { return "xf_nf_tickets_message" }

type TicketParticipant struct {
	TicketID     uint32 `gorm:"column:ticket_id;primaryKey"`
	UserID       uint32 `gorm:"column:user_id;primaryKey"`
	LastReadDate uint32 `gorm:"column:last_read_date"`
}

func (TicketParticipant) TableName() string { return "xf_nf_tickets_ticket_participant" }

type TicketFieldValue struct {
	TicketID   uint32 `gorm:"column:ticket_id;primaryKey"`
	FieldID    string `gorm:"column:field_id;primaryKey"`
	FieldValue string `gorm:"column:field_value"`
}

func (TicketFieldValue) TableName() string { return "xf_nf_tickets_ticket_field_value" }
