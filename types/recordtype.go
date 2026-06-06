package types

import "strconv"

// RecordType enumerates the categories of service-record entries. The
// numeric values match the proto enum being retired at Phase 4 exactly; the
// wire form is the NAME string (enum convention — see RosterType, the worked
// example).
type RecordType int32

const (
	RecordTypeUnspecified  RecordType = 0
	RecordTypePromotion    RecordType = 1
	RecordTypeOperation    RecordType = 2
	RecordTypeTransfer     RecordType = 3
	RecordTypeDisciplinary RecordType = 4
	RecordTypeDischarge    RecordType = 5
	RecordTypeAssignment   RecordType = 6
	RecordTypeNameChange   RecordType = 7
	RecordTypeEloa         RecordType = 8
	RecordTypeGraduation   RecordType = 9
)

// recordTypeNames is the wire-name catalog, keyed by enum value.
var recordTypeNames = map[RecordType]string{
	RecordTypeUnspecified:  "RECORD_TYPE_UNSPECIFIED",
	RecordTypePromotion:    "RECORD_TYPE_PROMOTION",
	RecordTypeOperation:    "RECORD_TYPE_OPERATION",
	RecordTypeTransfer:     "RECORD_TYPE_TRANSFER",
	RecordTypeDisciplinary: "RECORD_TYPE_DISCIPLINARY",
	RecordTypeDischarge:    "RECORD_TYPE_DISCHARGE",
	RecordTypeAssignment:   "RECORD_TYPE_ASSIGNMENT",
	RecordTypeNameChange:   "RECORD_TYPE_NAME_CHANGE",
	RecordTypeEloa:         "RECORD_TYPE_ELOA",
	RecordTypeGraduation:   "RECORD_TYPE_GRADUATION",
}

// MarshalJSON emits the enum name as a JSON string; values without a name
// emit the bare number (protojson behavior for unknown enum values).
func (rt RecordType) MarshalJSON() ([]byte, error) {
	if name, ok := recordTypeNames[rt]; ok {
		return strconv.AppendQuote(nil, name), nil
	}
	return strconv.AppendInt(nil, int64(rt), 10), nil
}
