package rest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/rest"
	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The LOA route answers keys holding read:loas. read alone or read:tickets
// alone get a 403 that names the scope they lack.
func TestNewStack_LoasScopeGate(t *testing.T) {
	h := newStack(t)

	rr := forumGet(t, h, "/api/v1/loas", "cav7_loaskey")
	assert.Equal(t, http.StatusOK, rr.Code, "read:loas key")

	for _, key := range []string{"cav7_readkey", "cav7_ticketskey"} {
		t.Run(key, func(t *testing.T) {
			rr := forumGet(t, h, "/api/v1/loas", key)

			require.Equal(t, http.StatusForbidden, rr.Code)
			var body struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
			assert.Equal(t, 7, body.Code, "permission-denied code")
			assert.Contains(t, body.Message, "read:loas", "the 403 names the missing scope")
		})
	}
}

// A window with no LOAs is an empty list, distinct from a failure.
func TestNewStack_LoasEmptyWindowIsEmptyList(t *testing.T) {
	h := rest.New(&fakeDatastore{findLoas: func(context.Context, datastores.LoaQuery) ([]*types.Loa, error) {
		return nil, nil
	}}, &stubReferenceCache{})

	rr := forumGet(t, h, "/api/v1/loas", "cav7_loaskey")

	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"loas":[]}`, rr.Body.String())
}

// A datastore failure is an error response, never an empty list.
func TestNewStack_LoasDatastoreFailureIs500(t *testing.T) {
	h := rest.New(&fakeDatastore{findLoas: func(context.Context, datastores.LoaQuery) ([]*types.Loa, error) {
		return nil, errOutage
	}}, &stubReferenceCache{})

	rr := forumGet(t, h, "/api/v1/loas", "cav7_loaskey")

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	var body struct {
		Code int `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, 13, body.Code, "internal-error code")
}

// LOA responses are stale immediately: no shared cache keeps limited-access
// data or serves an LOA from before a forum edit.
func TestNewStack_LoasCarryCacheControlMaxAgeZero(t *testing.T) {
	h := newStack(t)

	rr := forumGet(t, h, "/api/v1/loas", "cav7_loaskey")

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "max-age=0", rr.Header().Get("Cache-Control"))
}

// The datastore gets the request instant, from which it takes today. The
// check brackets the instant with wall-clock readings and does no date math,
// so it can't race midnight.
func TestNewStack_LoasPassTheRequestInstant(t *testing.T) {
	var got time.Time
	h := rest.New(&fakeDatastore{findLoas: func(_ context.Context, q datastores.LoaQuery) ([]*types.Loa, error) {
		got = q.Now
		return nil, nil
	}}, &stubReferenceCache{})

	before := time.Now()
	rr := forumGet(t, h, "/api/v1/loas", "cav7_loaskey")
	after := time.Now()

	require.Equal(t, http.StatusOK, rr.Code)
	assert.False(t, got.Before(before), "instant %v is before the request started at %v", got, before)
	assert.False(t, got.After(after), "instant %v is after the request ended at %v", got, after)
}
