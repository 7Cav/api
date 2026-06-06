package rest_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/7cav/api/proto"
	"github.com/7cav/api/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An empty AWOL list must serialize as {"awols":[]} — the allocation
// discipline (empty collections are [], never null) the golden can only
// witness populated. Empty is also this route's NORMAL state: nobody AWOL is
// the healthy regiment, so the empty form matters more here than anywhere.
func TestNewStack_EmptyAwolListIsEmptyArray(t *testing.T) {
	h := rest.New(&fakeDatastore{findAwol: func() ([]*proto.Awol, error) {
		return nil, nil
	}}, &stubReferenceCache{})

	rr := positionsGet(t, h, "/api/v1/milpacs/awol", "cav7_readkey")

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, `{"awols":[]}`, strings.TrimSpace(rr.Body.String()))
}
