package rest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/rest"
	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// endedSince binds in both spellings, like the other query parameters, and
// reaches the datastore as that UTC date.
func TestNewStack_LoasEndedSinceBindsBothSpellings(t *testing.T) {
	for _, path := range []string{
		"/api/v1/loas?endedSince=2026-10-01",
		"/api/v1/loas?ended_since=2026-10-01",
	} {
		t.Run(path, func(t *testing.T) {
			var got datastores.LoaQuery
			h := rest.New(&fakeDatastore{findLoas: func(_ context.Context, q datastores.LoaQuery) ([]*types.Loa, error) {
				got = q
				return nil, nil
			}}, &stubReferenceCache{})

			rr := forumGet(t, h, path, "cav7_loaskey")

			require.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, "2026-10-01", got.EndedSince.UTC().Format("2006-01-02"))
		})
	}
}

// A malformed endedSince is a 400, never a silently different window. That
// includes one whose percent-encoding is broken.
func TestNewStack_LoasMalformedEndedSinceIs400(t *testing.T) {
	h := newStack(t)

	for _, path := range []string{
		"/api/v1/loas?endedSince=bogus",
		"/api/v1/loas?endedSince=2026-10-0%",
	} {
		t.Run(path, func(t *testing.T) {
			rr := forumGet(t, h, path, "cav7_loaskey")

			require.Equal(t, http.StatusBadRequest, rr.Code)
			assert.Equal(t, 3, errorCode(t, rr), "invalid-argument code")
		})
	}
}

// The datastore's too-far-back error is the client's mistake, so it is a 400
// and not a 500.
func TestNewStack_LoasEndedSinceTooFarBackIs400(t *testing.T) {
	h := rest.New(&fakeDatastore{findLoas: func(context.Context, datastores.LoaQuery) ([]*types.Loa, error) {
		return nil, fmt.Errorf("%w: 2024-01-01 is before 2025-10-17", datastores.ErrEndedSinceTooFarBack)
	}}, &stubReferenceCache{})

	rr := forumGet(t, h, "/api/v1/loas?endedSince=2024-01-01", "cav7_loaskey")

	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, 3, errorCode(t, rr), "invalid-argument code")
}

// errorCode reads the code of an error body.
func errorCode(t *testing.T, rr *httptest.ResponseRecorder) int {
	t.Helper()
	var body struct {
		Code int `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	return body.Code
}
