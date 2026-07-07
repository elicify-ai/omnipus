//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog is a
// catalog-drift regression test: buildKnownBuiltinToolNames() (this
// package's live-derived tool-policy-coverage universe, walking the general +
// browser + sysagent tool catalogs) must enumerate EXACTLY the same
// tool-name set as pkg/coreagent's hand-maintained allStaticToolNames
// literal (the deny-by-default seed's universe, via denyAllThenOverride),
// read directly via the exported coreagent.AllStaticToolNames() accessor —
// there is no third, hand-copied literal anywhere for this comparison to
// silently go stale against.
//
// pkg/gateway already imports pkg/coreagent directly elsewhere (gateway.go,
// rest.go, rest_tool_registry.go, and several _test.go files), so there is
// no import-cycle risk here: the only reason this comparison previously used
// a hand-copied snapshot literal was that allStaticToolNames itself was an
// unexported package-level var, not any cross-package coupling concern. The
// new AllStaticToolNames() accessor (pkg/coreagent/core.go) resolves that by
// exposing a defensive copy.
//
// The two sides (this package's live registry walk, and coreagent's seed
// literal) are still independently maintained, so nothing else enforces
// they stay in sync — that is exactly what this test exists to catch. A
// failure here means pkg/coreagent's allStaticToolNames and the tool
// registry buildKnownBuiltinToolNames() walks (general + browser + sysagent
// catalogs) have drifted apart — investigate WHICH tool was added, renamed,
// or removed and update BOTH sides to match (never just silence this test):
// coreagent.denyAllThenOverride's deny-by-default seed and this function's
// coverage-validation universe (CLAUDE.md hard constraint 6) must agree on
// the exact same tool-name set, or an agent's seeded policy map ends up with
// either a dead entry or a real tool with no coverage.
func TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog(t *testing.T) {
	known := buildKnownBuiltinToolNames()
	liveNames := make([]string, 0, len(known))
	for name := range known {
		liveNames = append(liveNames, name)
	}
	assert.ElementsMatch(t, coreagent.AllStaticToolNames(), liveNames,
		"buildKnownBuiltinToolNames() (live tool registry, pkg/gateway/gateway.go) and "+
			"pkg/coreagent.AllStaticToolNames() (hand-maintained seed literal, pkg/coreagent/core.go) "+
			"must enumerate the exact same tool-name set — update whichever side is stale "+
			"after confirming which tool actually changed")
}
