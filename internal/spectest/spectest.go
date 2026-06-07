// Package spectest carries the cross-package registry that couples the
// spec's live-witnessed status carve-outs to the tests that witness them.
//
// Two test suites consume it, from opposite ends:
//
//   - contract/spec_test.go builds TestSpec_DeclaredStatusesAreCorpusWitnessed's
//     carve-out map from the registry: a declared status with a registry
//     entry is exempt from the corpus-witness rule.
//   - rest/spec_test.go DRIVES its live-witness subtests from the registry:
//     every entry must have a matching witness spec (fake override + request
//     path) or the suite fails before any witness runs.
//
// That closes the asymmetry a doc comment cannot: deleting a witness
// orphans its registry entry and fails rest's 1:1 meta-assertion; deleting
// a registry entry while the spec still declares the status fails contract's
// corpus-witness test; deleting both while the spec line stays fails the
// corpus-witness test too. There is no editing order that silently
// suppresses the invariant (ratified at the #127 review).
//
// The registry lives in internal/ deliberately: contract/'s public surface
// is frozen at five entries (Cases/RunCase/CompareGolden/SaveGolden/
// LoadGolden) and must not widen for test bookkeeping.
package spectest

// LiveWitness names one spec-declared status the FROZEN golden corpus never
// recorded, together with the live observation that witnesses it instead.
type LiveWitness struct {
	// Op is the spec operation key, "METHOD /spec/path/{template}".
	Op string
	// Status is the declared response code the corpus cannot witness.
	Status string
	// Witness is the rest/spec_test.go subtest name (under
	// TestNewStack_SpecValidation) that observes Status live against the
	// real stack and validates it under the explicit-status rule.
	Witness string
}

// LiveWitnessedStatuses is the registry. Constraint (ratified at the #127
// review): every entry's witness must (a) drive the status through the real
// stack (rest.New + contract.RunCase, not a replayed golden) and (b)
// validate the observed response via validateObserved, whose explicit-status
// rule makes the spec line load-bearing. Entries without an asserting
// witness are spec bugs, not carve-outs — carve-outs are never grandfathered.
var LiveWitnessedStatuses = []LiveWitness{
	{Op: "GET /api/v1/roster/{roster}/lite", Status: "500", Witness: "lite_roster_500_outage"},
	{Op: "GET /api/v1/s1/uniforms/{roster}", Status: "500", Witness: "s1_uniforms_500_outage"},
}
