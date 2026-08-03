// Command interrupt_scope_required is a deliberately non-compiling fixture
// for ADR-057 test #27 (TestInterruptScope_RequiredByCompiler, BDD-45).
//
// It lives under testdata/ so `go build ./...` / `go vet ./...` never touch
// it (Go's tooling skips directories named "testdata" by convention). The
// test that owns this fixture invokes `go build` against this directory
// directly and asserts the build FAILS, naming Interrupt and its missing
// InterruptScope argument — proving FR-041's "mandatory explicit scope" at
// the compiler, not just by inspection: a caller that tries to omit the
// scope argument (the pre-ADR-057 InterruptSession(sessionID, hint) shape)
// gets a compile error, not a silently-defaulted scope.
//
// Do not fix the argument count below. That is the point of this file.
package main

import "github.com/elicify-ai/omnipus/pkg/agent"

func main() {
	var al *agent.AgentLoop

	// This line MUST fail to compile: Interrupt takes three arguments
	// (id, scope InterruptScope, hint) — the two-argument pre-ADR-057
	// InterruptSession(sessionID, hint) shape this collapse retires is no
	// longer callable, by construction, not by convention.
	_, _ = al.Interrupt("some-session-id", "hint")
}
