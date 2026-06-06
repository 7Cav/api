package types

// Tickets wire types — the TicketsService surface (GET /api/v1/tickets and
// friends), golden-pinned by contract/goldens/tickets. All integer fields on
// this surface are 32-bit and stay JSON numbers; the emit-everything and
// allocation disciplines from the package doc apply throughout (empty
// collections are []/{} on the wire, never null).

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
