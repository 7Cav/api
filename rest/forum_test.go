package rest_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/7cav/api/rest"
	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The forum-group directory happy path: a valid read key gets a bare JSON
// array of {groupId, groupName} pairs, groupId a JSON number (32-bit), in the
// order the datastore returns them (groupId ascending, ADR 0007).
func TestNewStack_ForumGroupsHappy(t *testing.T) {
	h := rest.New(&fakeDatastore{findForumGroups: func() ([]*types.ForumGroup, error) {
		return []*types.ForumGroup{
			{GroupId: 2, GroupName: "Registered"},
			{GroupId: 3, GroupName: "Administrative"},
		}, nil
	}}, &stubReferenceCache{})

	rr := forumGet(t, h, "/api/v1/forum/groups", "cav7_readkey")

	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `[{"groupId":2,"groupName":"Registered"},{"groupId":3,"groupName":"Administrative"}]`, rr.Body.String())
}

// An empty directory must serialize as [], never null — the allocation
// discipline the goldens can only witness on a populated route. Both a nil and
// an empty slice from the datastore must produce the same [].
func TestNewStack_ForumGroupsEmptyIsEmptyArray(t *testing.T) {
	for _, tc := range []struct {
		name string
		ret  []*types.ForumGroup
	}{
		{"nil slice", nil},
		{"empty slice", []*types.ForumGroup{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := rest.New(&fakeDatastore{findForumGroups: func() ([]*types.ForumGroup, error) {
				return tc.ret, nil
			}}, &stubReferenceCache{})

			rr := forumGet(t, h, "/api/v1/forum/groups", "cav7_readkey")

			require.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, `[]`, strings.TrimSpace(rr.Body.String()))
		})
	}
}

// A datastore failure surfaces as the frozen Internal error shape with the
// handler-specific wrapped message.
func TestNewStack_ForumGroupsOutageIsInternalJSON(t *testing.T) {
	h := rest.New(&fakeDatastore{findForumGroups: func() ([]*types.ForumGroup, error) {
		return nil, errOutage
	}}, &stubReferenceCache{})

	rr := forumGet(t, h, "/api/v1/forum/groups", "cav7_readkey")

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.JSONEq(t, `{"code":13,"message":"error fetching forum groups: simulated datastore outage","details":[]}`, rr.Body.String())
}

// The route is gated by the read scope exactly like the milpac surface: a
// read:tickets key must 403 with the frozen scope-required body, not serve the
// directory.
func TestNewStack_ForumGroups403UnderTicketScopedKey(t *testing.T) {
	h := newStack(t)

	rr := forumGet(t, h, "/api/v1/forum/groups", "cav7_ticketskey")

	require.Equal(t, http.StatusForbidden, rr.Code)
	assert.JSONEq(t, `{"code":7,"message":"scope required: read","details":[]}`, rr.Body.String())
}

// A 200 forum-groups response carries the read-family freshness signal.
func TestNewStack_ForumGroupsCarriesCacheControl(t *testing.T) {
	h := newStack(t)

	rr := forumGet(t, h, "/api/v1/forum/groups", "cav7_readkey")

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "max-age=600", rr.Header().Get("Cache-Control"))
}

func forumGet(t *testing.T, h http.Handler, path, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
