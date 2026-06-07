package types

// Roster is the full-roster response shape: the requested roster's complete
// member set as full Profile views, keyed by milpac relation id (uint64 —
// decimal string keys on the wire, the encoding/json integer-key form that
// matches protojson's map encoding; note encoding/json sorts the stringified
// keys LEXICALLY ("10" before "2") where protojson ordered them numerically,
// so responses are JSON-equal, not byte-equal, to the old stack). The
// profiles map is always allocated:
// an empty roster serializes as {"profiles":{}}, never null (the allocation
// discipline lives in the handler's mapper; the reserve_empty golden proves
// it).
type Roster struct {
	Profiles map[uint64]*Profile `json:"profiles"`
}
