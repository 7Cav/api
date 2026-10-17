package types_test

import (
	"encoding/json"
	"testing"

	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A value with no name in its enum's catalog falls back to the bare number,
// the house rule every enum on the wire follows. Each LOA enum has its own
// MarshalJSON, so each gets its own case.
func TestLoaEnums_UnknownValueMarshalsAsNumber(t *testing.T) {
	for name, value := range map[string]any{
		"LoaForum":        types.LoaForum(99),
		"LoaState":        types.LoaState(99),
		"LoaReviewReason": types.LoaReviewReason(99),
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(value)
			require.NoError(t, err)
			assert.Equal(t, `99`, string(raw))
		})
	}
}
