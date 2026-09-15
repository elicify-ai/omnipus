// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/assert"
)

// TestMemoryTools_AllInCoverageUniverseAndSeed is a targeted regression guard
// for the recall_conversation gap: there are FOUR agent memory tools
// (remember / recall_memory / run_retrospective / recall_conversation), but
// recall_conversation's executable impl lives in pkg/agent (registered
// per-agent) rather than the pkg/tools general catalog, so it was originally
// omitted from BOTH the coverage universe (buildKnownBuiltinToolNames) AND the
// seed literal (allStaticToolNames). Because it was absent from both, the
// ElementsMatch drift test above could not catch it — yet the tool was still
// registered and callable, so it had no seeded tool-policy and was
// denied-by-default (fail-closed) on every install. This test names all four
// memory tools explicitly so a future omission of any of them is caught even
// when both sides are (wrongly) blind to it.
func TestMemoryTools_AllInCoverageUniverseAndSeed(t *testing.T) {
	known := buildKnownBuiltinToolNames()
	seed := make(map[string]struct{}, len(coreagent.AllStaticToolNames()))
	for _, n := range coreagent.AllStaticToolNames() {
		seed[n] = struct{}{}
	}
	for _, mt := range []string{"remember", "recall_memory", "run_retrospective", "recall_conversation"} {
		_, inCatalog := known[mt]
		_, inSeed := seed[mt]
		assert.Truef(t, inCatalog,
			"memory tool %q must be in the coverage universe (buildKnownBuiltinToolNames) — "+
				"a registered-but-uncovered tool is denied-by-default (fail-closed) on every install", mt)
		assert.Truef(t, inSeed,
			"memory tool %q must be in coreagent.AllStaticToolNames() (the deny-by-default seed universe)", mt)
	}
}
