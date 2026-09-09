// Omnipus — ToolSearch "ask"-policy signal wiring test (ADR-071 D2, §3.2)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// The unit-level exclusion logic (candidateQualifies narrowing out an
// "ask"-policy runner-up from the speculative cross-category clause) is
// tested directly against hand-crafted scores in
// pkg/tools/search_ambiguity_test.go. This file proves the PRODUCTION
// wiring that feeds it: pkg/agent/loop.go's canLoad closure must surface
// CanLoadAskPolicyPrefix for a tool whose resolved policy is "ask", using a
// real seeded agent (Ray, whose request_mount is seeded "ask" per
// coreAgentSeed) rather than a synthetic resolver.

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestCanLoad_AskPolicyToolStillLoadable proves that an "ask"-policy tool
// (request_mount, seeded ask for Ray) is still fully loadable via ToolSearch
// — both by exact name and by a query that ranks it uniquely — exercising
// the new policyVerdicts[name]=="ask" branch in loop.go's canLoad closure
// without regressing the underlying loadability the closure already
// guaranteed. ok=true either way; only the reason sentinel differs, and
// that sentinel is consumed only by execSearchAndLoad's speculative
// cross-category exclusion, never by canLoad's own ok/deny decision.
func TestCanLoad_AskPolicyToolStillLoadable(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	rayAgent, ok := al.registry.GetAgent("ray")
	require.True(t, ok)

	allTools := rayAgent.Tools.GetAll()
	_, policyVerdicts := tools.FilterToolsByPolicy(allTools, rayAgent.AgentType, rayAgent.LoadToolPolicy())
	require.Equal(t, "ask", policyVerdicts["request_mount"],
		"fixture defect: request_mount must resolve 'ask' for ray (coreAgentSeed) for this test to be meaningful")

	ctx := tools.WithAgentID(context.Background(), "ray")
	ctx = tools.WithTranscriptSessionID(ctx, "sess-ask-policy")

	toolsToolRaw, ok := rayAgent.Tools.Get("ToolSearch")
	require.True(t, ok, "ToolSearch infra tool must be registered for ray in compressed mode")
	tt, ok := toolsToolRaw.(*tools.ToolsTool)
	require.True(t, ok, "ToolSearch infra tool must be *tools.ToolsTool")

	// By exact name: ok=true despite "ask" — canLoad's ok/deny decision is
	// unaffected by the sentinel.
	loadResult := tt.Execute(ctx, map[string]any{"names": []any{"request_mount"}})
	assert.False(t, loadResult.IsError,
		"request_mount must be loadable by ray despite its 'ask' policy; got error: %s", loadResult.ForLLM)

	// By query, ranked uniquely: the confident-band clause (§3.2 rule 1) is
	// unrestricted, so a lone/dominant "ask"-policy hit is still auto-loaded
	// — the exclusion narrows only the speculative cross-category clause,
	// which a single dominant hit never reaches (fewer than 2 candidates,
	// no ambiguity test runs at all).
	queryResult := tt.Execute(ctx, map[string]any{"query": "ask the operator for folder access on their computer"})
	assert.False(t, queryResult.IsError, "query must succeed; got: %s", queryResult.ForLLM)
	assert.Contains(t, queryResult.ForLLM, "request_mount",
		"query ranking request_mount uniquely must still surface and auto-load it; got: %s", queryResult.ForLLM)
	assert.Contains(t, queryResult.ForLLM, `"loaded"`,
		"a dominant single hit must still auto-load even though its policy is 'ask'; got: %s", queryResult.ForLLM)
}
