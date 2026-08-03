// Command ids_no_interconvert is a deliberately non-compiling fixture for
// ADR-057 test #3 (TestSessionIDTypes_DoNotInterconvert, BDD-05).
//
// It lives under testdata/ so `go build ./...` / `go vet ./...` never touch
// it (Go's tooling skips directories named "testdata" by convention). The
// test that owns this fixture invokes `go build` against this directory
// directly and asserts the build FAILS with a type error naming both
// session.SessionID and session.RoutingSessionID — proving FR-004's
// "distinct named types that do not interconvert implicitly" at the
// compiler, not just by inspection.
//
// Do not fix the type error below. That is the point of this file.
package main

import "github.com/elicify-ai/omnipus/pkg/session"

func main() {
	var routing session.RoutingSessionID = "root-session"

	// This line MUST fail to compile: RoutingSessionID does not implicitly
	// convert to SessionID even though both are defined as `string`.
	var real session.SessionID = routing

	_ = real
}
