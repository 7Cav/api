package contract

import "regexp"

// publicRoutes is the single test-side truth for the 16 surviving public
// routes. Each entry carries a human label (battery coverage in
// corpus_test.go), the OpenAPI path template (spec coverage and golden
// replay in spec_test.go), and the regex that classifies battery case paths
// to the route.
//
// Ordered most-specific first: each case path (query string stripped) is
// classified to the FIRST matching entry, so a literal segment (categories,
// ref, messages) can never satisfy a path-parameter sibling. Prefix matching
// would let tickets/42/messages stand in for deleted ticket-by-id cases.
var publicRoutes = []struct {
	label    string
	specPath string
	pattern  *regexp.Regexp
}{
	{"ticket categories", "/api/v1/tickets/categories", regexp.MustCompile(`^/api/v1/tickets/categories$`)},
	{"ticket by ref", "/api/v1/tickets/ref/{ticketRef}", regexp.MustCompile(`^/api/v1/tickets/ref/[^/]+$`)},
	{"ticket messages", "/api/v1/tickets/{ticketId}/messages", regexp.MustCompile(`^/api/v1/tickets/[^/]+/messages$`)},
	{"ticket by id", "/api/v1/tickets/{ticketId}", regexp.MustCompile(`^/api/v1/tickets/[^/]+$`)},
	{"tickets list", "/api/v1/tickets", regexp.MustCompile(`^/api/v1/tickets$`)},
	{"lite roster", "/api/v1/roster/{roster}/lite", regexp.MustCompile(`^/api/v1/roster/[^/]+/lite$`)},
	{"roster", "/api/v1/roster/{roster}", regexp.MustCompile(`^/api/v1/roster/[^/]+$`)},
	{"s1 uniforms", "/api/v1/s1/uniforms/{roster}", regexp.MustCompile(`^/api/v1/s1/uniforms/[^/]+$`)},
	{"position groups", "/api/v1/milpacs/position/groups", regexp.MustCompile(`^/api/v1/milpacs/position/groups$`)},
	{"position search", "/api/v1/milpacs/position/search/{positionQuery}", regexp.MustCompile(`^/api/v1/milpacs/position/search(/.*)?$`)},
	{"ranks", "/api/v1/milpacs/ranks", regexp.MustCompile(`^/api/v1/milpacs/ranks$`)},
	{"awol", "/api/v1/milpacs/awol", regexp.MustCompile(`^/api/v1/milpacs/awol$`)},
	{"profile by id", "/api/v1/milpacs/profile/id/{userId}", regexp.MustCompile(`^/api/v1/milpacs/profile/id/[^/]+$`)},
	{"profile by username", "/api/v1/milpacs/profile/username/{username}", regexp.MustCompile(`^/api/v1/milpacs/profile/username/[^/]+$`)},
	{"discord lookup", "/api/v1/milpac/discord/{discordId}", regexp.MustCompile(`^/api/v1/milpac/discord/[^/]+$`)},
	{"gamertag lookup", "/api/v1/milpac/gamertag/{gamertag}", regexp.MustCompile(`^/api/v1/milpac/gamertag/[^/]+$`)},
}
