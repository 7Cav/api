package types

// Tickets wire types — the TicketsService surface (GET /api/v1/tickets and
// friends), golden-pinned by contract/goldens/tickets. All integer fields on
// this surface are 32-bit and stay JSON numbers; the emit-everything and
// allocation disciplines from the package doc apply throughout (empty
// collections are []/{} on the wire, never null).

// Ticket is one forum ticket thread: identity, category, workflow state,
// people, activity timestamps, custom fields. forumUrl is populated when the
// API is configured with the public forum base URL.
type Ticket struct {
	TicketId            uint32               `json:"ticketId"`
	TicketRef           string               `json:"ticketRef"`
	Title               string               `json:"title"`
	CategoryId          uint32               `json:"categoryId"`
	CategoryTitle       string               `json:"categoryTitle"`
	CategoryAncestorIds []uint32             `json:"categoryAncestorIds"`
	TicketState         string               `json:"ticketState"`
	StatusId            uint32               `json:"statusId"`
	StatusName          string               `json:"statusName"`
	PriorityId          uint32               `json:"priorityId"`
	PriorityName        string               `json:"priorityName"`
	PrefixId            uint32               `json:"prefixId"`
	PrefixName          string               `json:"prefixName"`
	DiscussionState     string               `json:"discussionState"`
	TicketLocked        bool                 `json:"ticketLocked"`
	StarterUserId       uint32               `json:"starterUserId"`
	StarterUsername     string               `json:"starterUsername"`
	AssignedUserId      uint32               `json:"assignedUserId"`
	AssignedUsername    string               `json:"assignedUsername"`
	Participants        []*TicketParticipant `json:"participants"`
	StartDate           uint32               `json:"startDate"`
	LastMessageDate     uint32               `json:"lastMessageDate"`
	LastMessageUserId   uint32               `json:"lastMessageUserId"`
	LastMessageUsername string               `json:"lastMessageUsername"`
	LastModifiedDate    uint32               `json:"lastModifiedDate"`
	ReplyCount          uint32               `json:"replyCount"`
	TotalMessageCount   uint32               `json:"totalMessageCount"`
	CustomFields        map[string]string    `json:"customFields"`
	ForumUrl            string               `json:"forumUrl"`
}

// TicketParticipant is a member-to-ticket association.
type TicketParticipant struct {
	UserId       uint32 `json:"userId"`
	LastReadDate uint32 `json:"lastReadDate"`
}

// Message is one post within a ticket, addressed by position (0-indexed
// within the thread).
type Message struct {
	MessageId    uint32 `json:"messageId"`
	TicketId     uint32 `json:"ticketId"`
	UserId       uint32 `json:"userId"`
	Username     string `json:"username"`
	MessageDate  uint32 `json:"messageDate"`
	Message      string `json:"message"`
	MessageState string `json:"messageState"`
	Position     uint32 `json:"position"`
	AttachCount  uint32 `json:"attachCount"`
	LastEditDate uint32 `json:"lastEditDate"`
	EditCount    uint32 `json:"editCount"`
}

// GetTicketResponse is the envelope shared by GET /api/v1/tickets/{ticketId}
// and GET /api/v1/tickets/ref/{ticketRef}: the ticket plus its first
// messages (oldest first). The top-level TotalMessageCount duplicates
// ticket.totalMessageCount — deprecated on the old surface, frozen here.
type GetTicketResponse struct {
	Ticket            *Ticket    `json:"ticket"`
	FirstMessages     []*Message `json:"firstMessages"`
	TotalMessageCount uint32     `json:"totalMessageCount"`
}

// Category is one entry in the ticket category reference tree. Served by
// GET /api/v1/tickets/categories; reference data for the categoryId filter
// on the list route.
type Category struct {
	CategoryId       uint32 `json:"categoryId"`
	Title            string `json:"title"`
	Description      string `json:"description"`
	ParentCategoryId uint32 `json:"parentCategoryId"`
	Depth            uint32 `json:"depth"`
	DisplayOrder     uint32 `json:"displayOrder"`
	TicketCount      uint32 `json:"ticketCount"`
}

// ListCategoriesResponse is the GET /api/v1/tickets/categories envelope.
// Categories must be allocated even when empty.
type ListCategoriesResponse struct {
	Categories []*Category `json:"categories"`
}
