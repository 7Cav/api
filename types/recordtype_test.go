package types_test

import (
	"encoding/json"
	"testing"

	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Enum convention (see RosterType, the worked example): the wire form is the
// NAME string, the zero value emits the _UNSPECIFIED name.
func TestRecordType_MarshalsAsNameString(t *testing.T) {
	cases := []struct {
		value types.RecordType
		want  string
	}{
		{types.RecordTypeUnspecified, `"RECORD_TYPE_UNSPECIFIED"`},
		{types.RecordType(0), `"RECORD_TYPE_UNSPECIFIED"`}, // zero value == unspecified
		{types.RecordTypePromotion, `"RECORD_TYPE_PROMOTION"`},
		{types.RecordTypeOperation, `"RECORD_TYPE_OPERATION"`},
		{types.RecordTypeTransfer, `"RECORD_TYPE_TRANSFER"`},
		{types.RecordTypeDisciplinary, `"RECORD_TYPE_DISCIPLINARY"`},
		{types.RecordTypeDischarge, `"RECORD_TYPE_DISCHARGE"`},
		{types.RecordTypeAssignment, `"RECORD_TYPE_ASSIGNMENT"`},
		{types.RecordTypeNameChange, `"RECORD_TYPE_NAME_CHANGE"`},
		{types.RecordTypeEloa, `"RECORD_TYPE_ELOA"`},
		{types.RecordTypeGraduation, `"RECORD_TYPE_GRADUATION"`},
	}
	for _, c := range cases {
		raw, err := json.Marshal(c.value)
		require.NoError(t, err)
		assert.Equal(t, c.want, string(raw))
	}
}

// Unnamed values fall back to the bare number (protojson behavior), so
// upstream data ahead of the catalog still round-trips.
func TestRecordType_UnknownValueMarshalsAsNumber(t *testing.T) {
	raw, err := json.Marshal(types.RecordType(99))
	require.NoError(t, err)
	assert.Equal(t, `99`, string(raw))
}
