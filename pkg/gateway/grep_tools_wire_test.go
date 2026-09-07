// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCentralBuiltinRegistry_CarriesGrep — MV-7 / ADR-081 D11: "grep" must
// come out of buildCentralBuiltinRegistry, the SINGLE function both boot
// passes call, or GET /api/v1/tools cannot serve it and an operator can
// neither see nor govern it anywhere in the UI — while
// pkg/config/defaults.go seeds it an explicit global-ceiling allow, i.e. a
// granted posture over a tool no catalog offers (the exact ADR-067 D7
// failure shape TestCentralBuiltinRegistry_CarriesTheKnowledgeTools guards
// against for the knowledge family).
//
// This test exists because the wiring it asserts was, for one work-track
// window, deliberately absent: grep's metadata entry landed as a stub with
// a documented "not yet reachable from GET /api/v1/tools" gap, closed at
// integration by the register("grep", ...) line in
// buildCentralBuiltinRegistry. Assert on the function's OUTPUT, not on the
// source containing the call — a call whose result is discarded stays green
// under an AST scan, which is how the knowledge family shipped invisible.
func TestCentralBuiltinRegistry_CarriesGrep(t *testing.T) {
	for _, tc := range []struct {
		name string
		deps *systools.Deps
	}{
		{name: "pre-deps boot pass", deps: nil},
		{name: "live-deps boot pass", deps: &systools.Deps{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, counts := buildCentralBuiltinRegistry(tc.deps)

			got, ok := reg.Get("grep")
			require.True(t, ok,
				"\"grep\" is absent from the catalog GET /api/v1/tools serves — MV-7 unmet: "+
					"operators cannot see or govern the tool the global ceiling explicitly allows")
			assert.NotEmpty(t, got.Description(),
				"the catalog entry must describe the capability; the model and the Settings "+
					"tool-policy screen both read this text")
			assert.Equal(t, 1, counts.grep,
				"exactly one grep entry must be admitted; 0 means the registration was "+
					"silently skipped as a duplicate — the drift mode this registry exists to prevent")
		})
	}
}
