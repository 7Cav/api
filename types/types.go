// Package types holds the hand-written wire types of the public HTTP API —
// the domain model that replaces the generated proto types in the stdlib
// net/http rewrite (PRD #112, Phase 3). The golden contract corpus
// (contract/) and the hand-owned OpenAPI spec (openapi/openapi.yaml) freeze
// the wire form these types must reproduce.
//
// # Wire conventions (house style — every type in this package follows them)
//
//   - JSON names are lowerCamelCase.
//   - Emit-everything: no `omitempty` anywhere. An absent key, "" and 0 are
//     distinct wire states; the contract requires presence.
//   - 64-bit integers serialize as JSON strings via the `,string` tag
//     (protojson convention); 32-bit integers stay JSON numbers.
//   - Unset nested messages are pointer fields and serialize as null.
//   - Empty collections serialize as []/{} — never null. encoding/json
//     renders a nil slice/map as null, so the ALLOCATION DISCIPLINE lives in
//     the handlers/mappers: always allocate, even for zero elements. The
//     goldens enforce it.
//   - Enums are integer-backed named types with custom marshalers that emit
//     the enum NAME as a JSON string; the zero value emits the _UNSPECIFIED
//     name, and values without a name fall back to the bare number (protojson
//     behavior). See RosterType for the worked example.
//   - Go field names keep the generated proto spelling (RankId, not RankID)
//     so the cutover datastore type-swap (#134) is import surgery, matching
//     the existing house style (datastores.ApiKeyResult.KeyId).
//
// Adding a type: copy the conventions above, then let the route's goldens and
// the spec replay loop (contract/spec_test.go) prove the wire form. The tag
// conventions (explicit lowerCamelCase json name, no omitempty, `,string` iff
// (u)int64) are enforced mechanically — every struct in the package is walked
// by the tag lint in wireconventions_test.go, no registration needed.
package types
