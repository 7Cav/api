package rest

import (
	"net/http"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/types"
)

// listCategories serves GET /api/v1/tickets/categories: the category
// reference tree, golden-pinned by tickets/categories. Error message string
// frozen from the old handler (servers/grpc ListCategories).
func listCategories(ds datastores.Datastore, rc datastores.TicketReferenceCache) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cats, err := ds.ListCategories(r.Context(), rc)
		if err != nil {
			writeError(w, r, codeInternal, "list ticket categories: %v", err)
			return
		}
		writeJSON(w, r, types.ListCategoriesResponse{Categories: categoriesFromProto(cats)})
	})
}

// categoriesFromProto maps the datastore's proto-typed rows to the wire
// types (see ranksFromProto for the mapping-layer rationale and the
// allocation discipline).
func categoriesFromProto(in []*proto.Category) []*types.Category {
	out := make([]*types.Category, 0, len(in))
	for _, c := range in {
		out = append(out, &types.Category{
			CategoryId:       c.GetCategoryId(),
			Title:            c.GetTitle(),
			Description:      c.GetDescription(),
			ParentCategoryId: c.GetParentCategoryId(),
			Depth:            c.GetDepth(),
			DisplayOrder:     c.GetDisplayOrder(),
			TicketCount:      c.GetTicketCount(),
		})
	}
	return out
}
