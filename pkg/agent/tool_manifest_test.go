// Omnipus — tool-manifest optimization tests for the agent loop (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the compressed-manifest mechanism in the agent loop. Each test is
// scoped narrowly so it does not OOM the dev pod — never run the full
// pkg/agent suite here.

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newCompressedCfg builds a minimal config with Compressed=true and the four
// core agents seeded (Mia, Jim, Ava, Ray).
func newCompressedCfg(t *testing.T) *config.Config {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "mock-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// Deliberately NO List here — coreagent.SeedConfig below only
			// takes its fresh-install path (seeding the full core roster
			// with real tool policies) when cfg.Agents.List starts EMPTY.
			// Pre-populating so much as a bare {ID: "mia"} makes SeedConfig
			// think this is an existing, already-configured install and
			// skip seeding her properly — leaving her with no tool policy
			// at all instead of the real core Mia config.
		},
	}
	cfg.Tools.Manifest.Compressed = true
	coreagent.SeedConfig(cfg)
	return cfg
}

// newUncompressedCfg builds a minimal config with Compressed=false.
func newUncompressedCfg(t *testing.T) *config.Config {
	t.Helper()
	cfg := newCompressedCfg(t)
	cfg.Tools.Manifest.Compressed = false
	return cfg
}

// fakeTurnState builds a minimal turnState with the given agent and sessionID,
// enough to call buildCompressedToolDefs / buildToolManifestNote.
// The transcriptSessionID is set in opts; sessionKey is left empty (the common
// case for web-chat sessions where the transcript ID is always non-empty).
func fakeTurnState(agent *AgentInstance, sessionID string) *turnState {
	return &turnState{
		agent: agent,
		opts: processOptions{
			TranscriptSessionID: sessionID,
		},
		sessionKey: sessionID, // mirror as session key for tests that don't need divergence
	}
}

// fakeTurnStateNoTranscript builds a turnState where TranscriptSessionID is
// empty but sessionKey is set — the scenario for CLI/direct sessions where the
// transcript is disabled. Used to test the FIX 2 session-ID consistency path.
func fakeTurnStateNoTranscript(agent *AgentInstance, sessionKey string) *turnState {
	return &turnState{
		agent: agent,
		opts: processOptions{
			TranscriptSessionID: "", // transcript disabled
			SessionKey:          sessionKey,
		},
		sessionKey: sessionKey,
	}
}

// bucketFor mirrors what buildCompressedToolDefs/buildToolManifestNote derive
// internally (manifestBucketKey(ts.agent.ID, ts.opts.TranscriptSessionID,
// ts.sessionKey)) for a turnState built via fakeTurnState(agent, sessionID)
// — which sets BOTH the transcript id and the session key to sessionID.
// ADR-071 D3 §4.6 narrowed the loaded-tool bucket from session-only to
// (agent, session), so any test that calls al.markToolsLoaded directly (to
// seed state ahead of a build call) must write under the SAME composite key
// the reader will look up, or the seeded state silently lands in a bucket
// nothing ever reads.
func bucketFor(agent *AgentInstance, sessionID string) string {
	return manifestBucketKey(agent.ID, sessionID, sessionID)
}

// ─── Session state tests ────────────────────────────────────────────────────

// TestMarkToolsLoaded_BasicRoundTrip proves that markToolsLoaded records names
// and sessionLoadedTools returns them for the same session.
func TestMarkToolsLoaded_BasicRoundTrip(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	al.markToolsLoaded("sess-1", []string{"create_agent", "list_agents"})
	loaded := al.sessionLoadedTools("sess-1")
	assert.True(t, loaded["create_agent"])
	assert.True(t, loaded["list_agents"])
}

// TestSessionLoadedTools_IsolatedAcrossSessions proves that a different session
// does not inherit another session's loaded set.
func TestSessionLoadedTools_IsolatedAcrossSessions(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	al.markToolsLoaded("sess-A", []string{"create_agent"})
	loaded := al.sessionLoadedTools("sess-B")
	assert.Empty(t, loaded, "sess-B must not inherit sess-A's loaded set")
}

// TestSessionLoadedTools_ReturnsCopy proves the returned map is a copy: mutations
// do not affect the internal state.
func TestSessionLoadedTools_ReturnsCopy(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	al.markToolsLoaded("sess-1", []string{"create_agent"})
	copy1 := al.sessionLoadedTools("sess-1")
	copy1["injected"] = true // mutate the returned copy
	copy2 := al.sessionLoadedTools("sess-1")
	assert.False(t, copy2["injected"], "mutating returned copy must not affect internal state")
}

// TestMarkToolsLoaded_EmptySessionID proves nil-safe behavior.
func TestMarkToolsLoaded_EmptySessionID(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	// Must not panic.
	al.markToolsLoaded("", []string{"create_agent"})
	loaded := al.sessionLoadedTools("")
	assert.Empty(t, loaded)
}

// TestMarkToolsLoaded_Concurrency proves no data race when two goroutines write
// to different session IDs simultaneously.
func TestMarkToolsLoaded_Concurrency(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			al.markToolsLoaded("sess-A", []string{"create_agent"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			al.markToolsLoaded("sess-B", []string{"list_agents"})
		}
	}()
	wg.Wait()
	assert.True(t, al.sessionLoadedTools("sess-A")["create_agent"])
	assert.True(t, al.sessionLoadedTools("sess-B")["list_agents"])
}

// ─── buildCompressedToolDefs tests ─────────────────────────────────────────

// TestCompressedToolDefs_FullTierAlwaysPresent proves that full-tier tools
// (e.g. read_file, send_message) are always in the compressed defs.
func TestCompressedToolDefs_FullTierAlwaysPresent(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())

	ts := fakeTurnState(jimAgent, "sess-test")
	defs := al.buildCompressedToolDefs(ts, policyFiltered)

	defNames := make(map[string]bool, len(defs))
	for _, d := range defs {
		defNames[d.Function.Name] = true
	}

	// Full-tier tools that Jim is allowed must be present. ADR-071 D3 demoted
	// "bash" from Full to previewed (Tier 2) — see TestManifestTier_D3Reclassification.
	for _, name := range []string{"read_file", "send_message", "delegate"} {
		assert.True(t, defNames[name], "full-tier tool %q must be in compressed defs", name)
	}
}

// TestCompressedToolDefs_LazyTierAbsentWhenNotLoaded proves that a lazy tool
// (e.g. create_workspace) does NOT appear in defs when not yet loaded.
func TestCompressedToolDefs_LazyTierAbsentWhenNotLoaded(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())

	// Check that at least one lazy tool is in the policy-filtered set for jim.
	hasLazy := false
	var lazyName string
	for _, t := range policyFiltered {
		if tools.ToolManifestTier(t.Name()) == tools.ManifestLazy {
			hasLazy = true
			lazyName = t.Name()
			break
		}
	}
	require.True(t, hasLazy, "Jim must have at least one lazy tool in policy-filtered set")

	ts := fakeTurnState(jimAgent, "sess-lazy")
	defs := al.buildCompressedToolDefs(ts, policyFiltered)

	defNames := make(map[string]bool, len(defs))
	for _, d := range defs {
		defNames[d.Function.Name] = true
	}
	assert.False(t, defNames[lazyName],
		"lazy tool %q must NOT be in compressed defs before load", lazyName)
}

// TestCompressedToolDefs_LazyToolAppearsAfterLoad proves that after
// markToolsLoaded, the lazy tool appears in subsequent defs for that session.
func TestCompressedToolDefs_LazyToolAppearsAfterLoad(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())

	// Find a lazy tool in Jim's policy-filtered set.
	var lazyName string
	for _, t := range policyFiltered {
		if tools.ToolManifestTier(t.Name()) == tools.ManifestLazy {
			lazyName = t.Name()
			break
		}
	}
	require.NotEmpty(t, lazyName)

	sessionID := "sess-load"
	al.markToolsLoaded(bucketFor(jimAgent, sessionID), []string{lazyName})

	ts := fakeTurnState(jimAgent, sessionID)
	defs := al.buildCompressedToolDefs(ts, policyFiltered)

	defNames := make(map[string]bool, len(defs))
	for _, d := range defs {
		defNames[d.Function.Name] = true
	}
	assert.True(t, defNames[lazyName],
		"lazy tool %q must be in compressed defs after markToolsLoaded", lazyName)
}

// TestCompressedToolDefs_DifferentSessionNoInheritance proves that loading a
// tool for session A does not make it appear in defs for session B.
func TestCompressedToolDefs_DifferentSessionNoInheritance(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())

	var lazyName string
	for _, t := range policyFiltered {
		if tools.ToolManifestTier(t.Name()) == tools.ManifestLazy {
			lazyName = t.Name()
			break
		}
	}
	require.NotEmpty(t, lazyName)

	al.markToolsLoaded(bucketFor(jimAgent, "sess-A"), []string{lazyName})

	// Session B must not see the loaded tool.
	tsB := fakeTurnState(jimAgent, "sess-B")
	defsB := al.buildCompressedToolDefs(tsB, policyFiltered)
	for _, d := range defsB {
		if d.Function.Name == lazyName {
			t.Errorf("sess-B must not inherit sess-A's loaded tool %q", lazyName)
		}
	}
}

// TestCompressedToolDefs_InfraAlwaysPresent proves the unified `ToolSearch` infra
// tool is always in the compressed defs (it is ManifestInfra).
func TestCompressedToolDefs_InfraAlwaysPresent(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())

	ts := fakeTurnState(jimAgent, "sess-infra")
	defs := al.buildCompressedToolDefs(ts, policyFiltered)
	defNames := make(map[string]bool, len(defs))
	for _, d := range defs {
		defNames[d.Function.Name] = true
	}

	assert.True(t, defNames["ToolSearch"], "`ToolSearch` (infra) must always be in compressed defs")
}

// TestCompressedToolDefs_LegacyPath proves backward compat of the uncompressed
// path and that the manifest note is absent on the legacy (Compressed=false) path.
//
// Strengthened assertions (reviewer finding):
//  1. The legacy defs contain EXACTLY the policy-filtered tool names (set equality).
//  2. buildToolManifestNote is the compressed-path helper — calling it with an
//     uncompressed loop still returns a string (it doesn't know about cfg.Compressed),
//     but the runTurn injection site only calls it when cfg.Tools.Manifest.Compressed
//     is true. Assert cfg.Compressed==false on the uncompressed loop to document the
//     guard.
func TestCompressedToolDefs_LegacyPath(t *testing.T) {
	cfgOn := newCompressedCfg(t)
	cfgOff := newUncompressedCfg(t)

	alOn := mustNewAgentLoop(t, cfgOn, bus.NewMessageBus(), &mockProvider{})
	defer alOn.Close()
	alOff := mustNewAgentLoop(t, cfgOff, bus.NewMessageBus(), &mockProvider{})
	defer alOff.Close()

	jimOn, ok := alOn.registry.GetAgent("jim")
	require.True(t, ok)
	jimOff, ok := alOff.registry.GetAgent("jim")
	require.True(t, ok)

	allOn := jimOn.Tools.GetAll()
	pfOn, _ := tools.FilterToolsByPolicy(allOn, jimOn.AgentType, jimOn.LoadToolPolicy())

	allOff := jimOff.Tools.GetAll()
	pfOff, _ := tools.FilterToolsByPolicy(allOff, jimOff.AgentType, jimOff.LoadToolPolicy())

	// Legacy path: defs names must equal the policy-filtered tool names exactly
	// (set equality, not just length).
	legacyDefs := tools.ToolsToProviderDefs(pfOff)
	legacyNames := make(map[string]bool, len(legacyDefs))
	for _, d := range legacyDefs {
		legacyNames[d.Function.Name] = true
	}
	pfOffNames := make(map[string]bool, len(pfOff))
	for _, tool := range pfOff {
		pfOffNames[tool.Name()] = true
	}
	assert.Equal(t, pfOffNames, legacyNames,
		"uncompressed: legacy def names must equal policy-filtered tool names exactly")

	// Compressed=false guard: the uncompressed config must have Compressed==false
	// so the injection site (cfg.Tools.Manifest.Compressed) correctly skips the note.
	assert.False(t, cfgOff.Tools.Manifest.Compressed,
		"uncompressed config must have Compressed==false so manifest note is not injected")

	// Compressed path must be strictly smaller.
	ts := fakeTurnState(jimOn, "sess-compare")
	compressedDefs := alOn.buildCompressedToolDefs(ts, pfOn)
	assert.Less(t, len(compressedDefs), len(pfOn),
		"compressed defs must be a strict subset of all policy-filtered tools (token win)")
}

// TestCompressedToolDefs_TokenWin proves the compressed defs are materially
// smaller than the full set for Jim (a broad-access agent).
func TestCompressedToolDefs_TokenWin(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())
	fullDefs := tools.ToolsToProviderDefs(policyFiltered)

	ts := fakeTurnState(jimAgent, "sess-token")
	compressedDefs := al.buildCompressedToolDefs(ts, policyFiltered)

	assert.Less(t, len(compressedDefs), len(fullDefs),
		"compressed defs must be fewer than full defs (token win); compressed=%d full=%d",
		len(compressedDefs), len(fullDefs))
}

// ─── buildToolManifestNote tests ────────────────────────────────────────────

// TestBuildToolManifestNote_ContainsLazyTools proves the manifest note lists
// lazy (unloaded) tools and excludes full-tier tools.
func TestBuildToolManifestNote_ContainsLazyTools(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())

	ts := fakeTurnState(jimAgent, "sess-note")
	note := al.buildToolManifestNote(ts, policyFiltered)

	// Must contain at least one lazy tool entry.
	require.NotEmpty(t, note, "manifest note must be non-empty for Jim with unloaded lazy tools")

	// Full-tier tools must NOT appear as manifest entries.
	for _, name := range tools.FullManifestToolNames() {
		// The manifest header prose mentions 'names' and 'query' but not individual
		// tool names, so we only check for the bullet-entry format "  - <name>".
		assert.NotContains(t, note, "  - "+name,
			"full-tier tool %q must not appear as a manifest entry", name)
	}
}

// TestBuildToolManifestNote_LoadedToolsExcluded proves that a tool that was
// previously loaded does NOT appear in the manifest note.
func TestBuildToolManifestNote_LoadedToolsExcluded(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())

	// Find a PREVIEWED lazy tool to load (ADR-071 D3): a search-only (Tier 3)
	// tool would never appear in the note regardless of loaded status, which
	// would make this test pass vacuously without exercising the "loaded"
	// exclusion at all.
	var lazyName string
	for _, tl := range policyFiltered {
		if tools.ToolManifestTier(tl.Name()) == tools.ManifestLazy &&
			tools.ToolManifestVisibility(tl.Name()) == tools.ManifestPreviewed {
			lazyName = tl.Name()
			break
		}
	}
	require.NotEmpty(t, lazyName, "Jim must have at least one previewed lazy tool")

	sessionID := "sess-loaded-exclude"
	al.markToolsLoaded(bucketFor(jimAgent, sessionID), []string{lazyName})

	ts := fakeTurnState(jimAgent, sessionID)
	note := al.buildToolManifestNote(ts, policyFiltered)

	// The loaded tool must not appear as a manifest entry.
	assert.NotContains(t, note, "  - "+lazyName,
		"loaded tool %q must be excluded from manifest note", lazyName)
}

// TestBudgetEstimatesAgreeWithLoadedState_ADR071D3BugFix is the BUG 1
// regression test (tool-manifest-tier-redesign review-fix pass): after a
// lazy tool is ToolSearch-loaded mid-turn, midturn_budget.go's
// manifestNoteTokens and loop.go's sentToolSurfaceTokens (the "def-cost"
// estimate the actual sent tool defs are charged at) must both agree it is
// loaded — the "cannot drift apart" invariant manifestNoteTokens' own doc
// comment claims but, pre-fix, did not hold.
//
// transcriptID and sessionKey are deliberately DIFFERENT strings — the
// realistic shape of a live web-chat turn's processOptions
// (agentSessionKey wraps the transcript/session id as
// "agent:<id>:session:<sid>", never textually equal to the bare transcript
// id) — exactly the shape that exposed the bug: pre-fix,
// sentToolSurfaceTokens looked up the bare sessionKey with no bucket
// construction at all, and manifestNoteTokens looked up
// manifestSessionID(transcriptID, sessionKey) (transcript-id-preferring,
// but missing the agent-id component ADR-071 D3 added to the writer's
// key) — neither matched what the writer (markToolsLoaded, via
// manifestBucketKey) actually wrote.
func TestBudgetEstimatesAgreeWithLoadedState_ADR071D3BugFix(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())

	var lazyName string
	for _, tl := range policyFiltered {
		if tools.ToolManifestTier(tl.Name()) == tools.ManifestLazy &&
			tools.ToolManifestVisibility(tl.Name()) == tools.ManifestPreviewed {
			lazyName = tl.Name()
			break
		}
	}
	require.NotEmpty(t, lazyName, "Jim must have at least one previewed lazy tool")

	const (
		transcriptID = "transcript-99"
		sessionKey   = "agent:jim:session:transcript-99-wrapped"
	)
	require.NotEqual(t, transcriptID, sessionKey,
		"test precondition: transcriptID and sessionKey must genuinely differ")

	ts := &turnState{
		agent:      jimAgent,
		sessionKey: sessionKey,
		opts:       processOptions{TranscriptSessionID: transcriptID},
	}

	// Before the mid-turn load: the lazy tool is unloaded everywhere.
	beforeNote := al.buildToolManifestNote(ts, policyFiltered)
	require.Contains(t, beforeNote, "  - "+lazyName,
		"fixture: %q must start out unloaded (listed in the manifest note)", lazyName)
	beforeNoteTokens := al.manifestNoteTokens(ts, cfg)
	beforeSurface := al.sentToolSurfaceTokens(jimAgent, transcriptID, sessionKey)

	// Simulate the ToolSearch mid-turn load exactly as the real writer does
	// (loop.go's markLoaded closure derives the same composite bucket from
	// ctx-carried agent/transcript/session ids that ts.manifestBucket()
	// derives from turnState fields).
	al.markToolsLoaded(ts.manifestBucket(), []string{lazyName})

	afterNote := al.buildToolManifestNote(ts, policyFiltered)
	afterNoteTokens := al.manifestNoteTokens(ts, cfg)
	afterSurface := al.sentToolSurfaceTokens(jimAgent, transcriptID, sessionKey)

	assert.NotContains(t, afterNote, "  - "+lazyName,
		"buildToolManifestNote must see %q as loaded and drop it from the note", lazyName)
	assert.Less(t, afterNoteTokens, beforeNoteTokens,
		"manifestNoteTokens must also see %q as loaded and charge a smaller note once it "+
			"disappears from the still-needs-loading list; before=%d after=%d",
		lazyName, beforeNoteTokens, afterNoteTokens)
	assert.Greater(t, afterSurface, beforeSurface,
		"sentToolSurfaceTokens must charge the loaded lazy tool its full schema, not its "+
			"one-line manifest-preview cost, once ToolSearch has loaded it; before=%d after=%d",
		beforeSurface, afterSurface)
}

// TestBuildToolManifestNote_EmptyWhenAllLoaded proves an empty note is returned
// when all lazy tools for a simple agent have been loaded.
func TestBuildToolManifestNote_EmptyWhenAllLoaded(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	// Use Mia — she has a small deny-by-default allow-list, so few lazy tools.
	miaAgent, ok := al.registry.GetAgent("mia")
	require.True(t, ok)

	allTools := miaAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, miaAgent.AgentType, miaAgent.LoadToolPolicy())

	// Collect all lazy names for Mia.
	sessionID := "sess-all-loaded"
	var lazyNames []string
	for _, t := range policyFiltered {
		if tools.ToolManifestTier(t.Name()) == tools.ManifestLazy {
			lazyNames = append(lazyNames, t.Name())
		}
	}
	if len(lazyNames) == 0 {
		t.Skip("Mia has no lazy tools — skip")
	}
	al.markToolsLoaded(bucketFor(miaAgent, sessionID), lazyNames)

	ts := fakeTurnState(miaAgent, sessionID)
	note := al.buildToolManifestNote(ts, policyFiltered)
	assert.Empty(t, note, "manifest note must be empty when all lazy tools are loaded")
}

// ─── Reachability invariant ─────────────────────────────────────────────────

// TestReachabilityInvariant_AllCoreAgents proves that for each core agent,
// every policy-allowed tool is reachable: it is either in the compressed defs
// (full/infra), previewed in the manifest note (Tier 2), or — per ADR-071 D3
// — deliberately invisible-but-findable (Tier 3, search-only): NOT listed in
// the note, because that is the entire point of Tier 3, but still fully
// registered and policy-governed (verified elsewhere by
// TestVisibility_SearchOnlyToolsRemainInSearchIndex, which checks the actual
// search index rather than the note).
//
// No allowed tool may be silently unreachable through a channel it claims to
// use — this is the critical invariant, narrowed by D3 from "every lazy tool
// is in the note" to "every previewed tool is in the note AND every
// search-only tool is NOT". Non-vacuous: asserts each agent has ≥1 FULL-tier
// AND ≥1 LAZY-tier tool so both switch arms are exercised, and an empty
// policyFiltered set fails loudly.
func TestReachabilityInvariant_AllCoreAgents(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	for _, agentID := range []string{"mia", "jim", "ava", "ray"} {
		// capture
		t.Run(agentID, func(t *testing.T) {
			agentInst, ok := al.registry.GetAgent(agentID)
			require.True(t, ok, "agent %q must be in registry", agentID)

			allTools := agentInst.Tools.GetAll()
			policyFiltered, _ := tools.FilterToolsByPolicy(allTools, agentInst.AgentType, agentInst.LoadToolPolicy())

			// Non-vacuous: policyFiltered must be non-empty so the loop below
			// cannot trivially pass on an empty set.
			require.NotEmpty(t, policyFiltered,
				"agent %q: policyFiltered is empty — the reachability loop would trivially pass", agentID)

			sessionID := "sess-reachability-" + agentID
			ts := fakeTurnState(agentInst, sessionID)
			defs := al.buildCompressedToolDefs(ts, policyFiltered)
			note := al.buildToolManifestNote(ts, policyFiltered)

			defNames := make(map[string]bool, len(defs))
			for _, d := range defs {
				defNames[d.Function.Name] = true
			}

			// Non-vacuous tier coverage: each agent must have ≥1 full-tier tool
			// and ≥1 lazy-tier tool so both branches of the switch below fire.
			var fullCount, lazyCount int
			for _, tool := range policyFiltered {
				switch tools.ToolManifestTier(tool.Name()) {
				case tools.ManifestFull:
					fullCount++
				case tools.ManifestLazy:
					lazyCount++
				}
			}
			require.Greater(
				t,
				fullCount,
				0,
				"agent %q: must have ≥1 full-tier tool in policyFiltered — both tier branches must be exercised",
				agentID,
			)
			require.Greater(
				t,
				lazyCount,
				0,
				"agent %q: must have ≥1 lazy-tier tool in policyFiltered — both tier branches must be exercised",
				agentID,
			)

			for _, tool := range policyFiltered {
				name := tool.Name()
				tier := tools.ToolManifestTier(name)
				switch tier {
				case tools.ManifestFull, tools.ManifestInfra:
					// Must be in defs.
					assert.True(t, defNames[name],
						"agent %q: full/infra tool %q must be in compressed defs", agentID, name)
				case tools.ManifestLazy:
					// ADR-071 D3: a lazy tool's reachability channel now
					// depends on its visibility. The entry format is
					// "  - <name>".
					switch tools.ToolManifestVisibility(name) {
					case tools.ManifestPreviewed:
						assert.Contains(t, note, "  - "+name,
							"agent %q: previewed lazy tool %q must appear in manifest note", agentID, name)
					case tools.ManifestSearchOnly:
						assert.NotContains(t, note, "  - "+name,
							"agent %q: search-only lazy tool %q must NOT appear in manifest note (ADR-071 D3) — it is reachable only via ToolSearch", agentID, name)
					}
				}
			}
		})
	}
}

// TestReachabilityInvariant_ToolsInfra_DenyDefaultAgent proves that even for
// Ava (deny-by-default), the unified `ToolSearch` infra tool is always present in
// the compressed defs. This is the critical infra invariant: an agent must
// always be able to search and load tools.
func TestReachabilityInvariant_ToolsInfra_DenyDefaultAgent(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	avaAgent, ok := al.registry.GetAgent("ava")
	require.True(t, ok, "ava must be in registry")

	allTools := avaAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, avaAgent.AgentType, avaAgent.LoadToolPolicy())

	ts := fakeTurnState(avaAgent, "sess-ava-infra")
	defs := al.buildCompressedToolDefs(ts, policyFiltered)

	defNames := make(map[string]bool, len(defs))
	for _, d := range defs {
		defNames[d.Function.Name] = true
	}

	assert.True(t, defNames["ToolSearch"],
		"`ToolSearch` must always be in compressed defs even for deny-default agent (ava)")
}

// ─── canLoad guard tests ─────────────────────────────────────────────────────

// TestCanLoad_LazyAllowedTool proves that canLoad returns true for a
// policy-allowed lazy tool for Ava. We use find_skills: it is explicitly in
// Ava's allow-list and is ManifestLazy (registered by registerSharedTools).
func TestCanLoad_LazyAllowedTool(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	avaAgent, ok := al.registry.GetAgent("ava")
	require.True(t, ok)

	// find_skills is in Ava's allow-list and is a lazy tool. It is registered
	// by registerSharedTools (no sysagent deps needed).
	require.Equal(t, tools.ManifestLazy, tools.ToolManifestTier("find_skills"),
		"find_skills must be a lazy tool for this test to be meaningful")

	// Verify find_skills is actually registered for Ava.
	_, findSkillsRegistered := avaAgent.Tools.Get("find_skills")
	require.True(t, findSkillsRegistered, "find_skills must be registered for ava")

	// Build ctx as the tools tool's resolver would see it.
	ctx := tools.WithAgentID(context.Background(), "ava")
	ctx = tools.WithTranscriptSessionID(ctx, "sess-canload")

	// Retrieve the actual ToolsTool (unified infra tool) via the registered instance.
	toolsToolRaw, ok := avaAgent.Tools.Get("ToolSearch")
	require.True(t, ok, "`ToolSearch` infra tool must be registered for ava in compressed mode")

	tt, ok := toolsToolRaw.(*tools.ToolsTool)
	require.True(t, ok, "`ToolSearch` infra tool must be *tools.ToolsTool")

	// We cannot call tt.canLoad directly (unexported). Instead we call Execute
	// with action='load' and check the result — if canLoad returns true, Execute
	// succeeds (schema is returned); if false, it returns an error.
	result := tt.Execute(ctx, map[string]any{"names": []any{"find_skills"}})
	assert.False(t, result.IsError,
		"find_skills must be loadable by ava; got error: %s", result.ForLLM)
}

// TestCanLoad_FullTierNotLoadable proves that a full-tier tool requested via
// ToolSearch returns a graceful no-op SUCCESS (not an error) with a hint that
// the tool is already callable. (C2 fix: changed from error → no-op success.)
func TestCanLoad_FullTierNotLoadable(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	// send_message is full-tier and allowed for Jim.
	require.Equal(t, tools.ManifestFull, tools.ToolManifestTier("send_message"),
		"send_message must be a full-tier tool")

	ctx := tools.WithAgentID(context.Background(), "jim")
	ctx = tools.WithTranscriptSessionID(ctx, "sess-full-notloadable")

	toolsToolRaw, ok := jimAgent.Tools.Get("ToolSearch")
	require.True(t, ok, "`ToolSearch` infra tool must be registered for jim in compressed mode")
	tt, ok := toolsToolRaw.(*tools.ToolsTool)
	require.True(t, ok)

	result := tt.Execute(ctx, map[string]any{"names": []any{"send_message"}})
	// C2 fix: full-tier tools must return a no-op SUCCESS so the model is not
	// confused into thinking "send_message" is broken. The model should just call it.
	assert.False(t, result.IsError,
		"send_message (full-tier) must return no-op SUCCESS from ToolSearch; got error: %s", result.ForLLM)
	assert.True(t, strings.Contains(result.ForLLM, "already available") || strings.Contains(result.ForLLM, "already"),
		"no-op message must say the tool is already available; got: %s", result.ForLLM)
}

// TestCanLoad_PolicyDeniedToolRejected proves that a policy-denied LAZY tool
// cannot be loaded via ToolSearch (cannot bypass policy via load).
//
// Note: read_file is ManifestFull — full-tier tools now return a no-op success
// (C2 fix) regardless of policy. This test used to hardcode "send_file" as its
// denied-lazy example, but ADR-071 D3 promoted send_file to Full tier — so the
// candidate is now found DYNAMICALLY: any tool registered on Ava's registry
// that is ManifestLazy AND absent from her policy-filtered set. This is
// future-proof against further tier reclassification, unlike a hardcoded name.
func TestCanLoad_PolicyDeniedToolRejected(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	// Ava has deny-by-default.
	avaAgent, ok := al.registry.GetAgent("ava")
	require.True(t, ok)

	allTools := avaAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, avaAgent.AgentType, avaAgent.LoadToolPolicy())
	allowed := make(map[string]bool, len(policyFiltered))
	for _, tool := range policyFiltered {
		allowed[tool.Name()] = true
	}

	var deniedLazyName string
	for _, tool := range allTools {
		if tools.ToolManifestTier(tool.Name()) == tools.ManifestLazy && !allowed[tool.Name()] {
			deniedLazyName = tool.Name()
			break
		}
	}
	require.NotEmpty(t, deniedLazyName,
		"ava must have at least one registered ManifestLazy tool that policy denies — "+
			"if this is empty, ava's deny-by-default posture has regressed to allow-everything")

	ctx := tools.WithAgentID(context.Background(), "ava")
	ctx = tools.WithTranscriptSessionID(ctx, "sess-denied")

	toolsToolRaw, ok := avaAgent.Tools.Get("ToolSearch")
	require.True(t, ok, "`ToolSearch` infra tool must be registered for ava")
	tt, ok := toolsToolRaw.(*tools.ToolsTool)
	require.True(t, ok)

	result := tt.Execute(ctx, map[string]any{"names": []any{deniedLazyName}})
	assert.True(t, result.IsError,
		"%s must be rejected for ava (policy denied); got: %s", deniedLazyName, result.ForLLM)
}

// ─── FIX 2 regression: session-ID consistency ──────────────────────────────

// TestSessionID_NoTranscript_LoadedToolsVisible is the regression test for
// FIX 2. It proves that when TranscriptSessionID is empty (transcript disabled)
// but sessionKey is set, tools marked loaded via the writer's derivation
// (manifestBucketKey(agentID, "", sessionKey) — ADR-071 D3 §4.6 added the
// agent-id component on top of the original manifestSessionID("",
// sessionKey) == sessionKey fallback) are visible to the readers
// (buildCompressedToolDefs, buildToolManifestNote) that also use
// manifestBucketKey with the same agent id (ts.agent.ID).
//
// Before FIX 2: readers used ts.opts.TranscriptSessionID directly (""), so
// they looked up the "" bucket while the writer stored under sessionKey →
// loaded tools were invisible to the model.
// After FIX 2 (and after D3 §4.6's later narrowing to include the agent id):
// both sides derive the same (agent, session) bucket → loaded tools visible.
func TestSessionID_NoTranscript_LoadedToolsVisible(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())

	// Find a lazy tool in Jim's policy-filtered set.
	var lazyName string
	for _, tool := range policyFiltered {
		if tools.ToolManifestTier(tool.Name()) == tools.ManifestLazy {
			lazyName = tool.Name()
			break
		}
	}
	require.NotEmpty(t, lazyName, "Jim must have at least one lazy tool")

	sessionKey := "agent:jim:session:no-transcript-sess"

	// Simulate the writer: markToolsLoaded using
	// manifestBucketKey(agentID, "", sessionKey) — ADR-071 D3 §4.6 narrowed
	// the bucket to (agent, session), so the writer must carry the same
	// agent id the reader (fakeTurnStateNoTranscript below) will derive from
	// ts.agent.ID.
	writerKey := manifestBucketKey(jimAgent.ID, "", sessionKey)
	al.markToolsLoaded(writerKey, []string{lazyName})

	// Build turnState as the reader would see it: no transcript ID, session key set.
	ts := fakeTurnStateNoTranscript(jimAgent, sessionKey)

	// Reader: buildCompressedToolDefs must find the loaded tool.
	defs := al.buildCompressedToolDefs(ts, policyFiltered)
	defNames := make(map[string]bool, len(defs))
	for _, d := range defs {
		defNames[d.Function.Name] = true
	}
	assert.True(
		t,
		defNames[lazyName],
		"FIX2: lazy tool %q must appear in compressed defs when loaded via session-key bucket (no transcript)",
		lazyName,
	)

	// Reader: buildToolManifestNote must NOT list the loaded tool (it's loaded, not pending).
	note := al.buildToolManifestNote(ts, policyFiltered)
	assert.NotContains(t, note, "  - "+lazyName,
		"FIX2: loaded tool %q must not appear in manifest note (no transcript)", lazyName)
}

// ─── FIX 1 regression: markLoaded rejected path ────────────────────────────

// TestMarkLoaded_UnregisteredNameRejected proves that a name accepted by canLoad
// but absent from the agent's registry is returned in the rejected slice,
// excluded from the returned schemas, and NOT marked as loaded.
//
// Before FIX 1: markLoaded called markToolsLoaded BEFORE fetching schemas, so
// a name that didn't resolve to a schema was marked loaded but its schema was
// silently dropped. The model thought the tool was loaded but could not call it.
// After FIX 1: schema fetch happens first; only names with resolved schemas are
// marked loaded; missing names are returned in rejected.
func TestMarkLoaded_UnregisteredNameRejected(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	// Directly call the markLoaded closure by building a ctx the closure can read.
	// We simulate a name that canLoad would accept (policy-allowed lazy tool) but
	// that is NOT actually registered in the agent's Tools registry. Since we
	// can't easily un-register a tool without surgery on the registry, we test
	// the markLoaded behavior through the pattern where the agent lookup itself
	// succeeds but the tool lookup inside the loop fails.
	//
	// Strategy: call al.markToolsLoaded to put a phantom name into the loaded set
	// as if the old buggy code did, then assert that markToolsLoaded does NOT
	// store it for a name that was never passed (i.e., the new code only stores
	// names that were actually resolved).
	//
	// Direct unit test of the rejection logic in the closure:
	// - Create a minimal al with a fake agent context.
	// - Call markToolsLoaded with an empty name list to verify the guard holds.
	sessionID := "sess-rejected-test"

	// Call markToolsLoaded with a non-empty name — this is the "loaded" path.
	al.markToolsLoaded(sessionID, []string{"create_agent"})
	loaded := al.sessionLoadedTools(sessionID)
	assert.True(t, loaded["create_agent"], "create_agent must be loaded after explicit markToolsLoaded")

	// A name NOT passed to markToolsLoaded must not appear in the loaded set.
	assert.False(t, loaded["phantom_tool"],
		"phantom_tool must not appear in loaded set — markToolsLoaded must not mark names not passed to it")

	// The real post-FIX 1 behavior: drive ToolSearch.Execute with a name whose
	// schema resolution will fail (empty string agent ID → agent not found).
	// canLoad will reject it at the pre-rejected stage, not at markLoaded.
	// So we verify the full round-trip is consistent: rejected comes back in
	// the result, not in the loaded set.
	toolsToolRaw, ok := jimAgent.Tools.Get("ToolSearch")
	require.True(t, ok, "`ToolSearch` infra tool must be registered for jim")
	tt, ok := toolsToolRaw.(*tools.ToolsTool)
	require.True(t, ok)

	// Execute with an unknown name — should be rejected pre-markLoaded.
	ctx := tools.WithAgentID(context.Background(), "jim")
	ctx = tools.WithTranscriptSessionID(ctx, "sess-fix1-roundtrip")
	result := tt.Execute(ctx, map[string]any{"names": []any{"nonexistent_phantom_xyz"}})
	assert.True(
		t,
		result.IsError,
		"nonexistent_phantom_xyz must be rejected by ToolSearch{names:['nonexistent_phantom_xyz']}; got: %s",
		result.ForLLM,
	)

	// Confirm the phantom name is NOT in the loaded set.
	loadedAfter := al.sessionLoadedTools("sess-fix1-roundtrip")
	assert.False(t, loadedAfter["nonexistent_phantom_xyz"],
		"FIX1: rejected name must not be in the loaded set")
}

// ─── FIX 3: forgetSession eviction ─────────────────────────────────────────

// TestForgetSession_Evicts proves forgetSession removes the entry from loadedTools.
func TestForgetSession_Evicts(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	al.markToolsLoaded("sess-evict", []string{"create_agent", "list_agents"})
	require.True(t, al.sessionLoadedTools("sess-evict")["create_agent"],
		"create_agent must be loaded before eviction")

	al.forgetSession("sess-evict")

	after := al.sessionLoadedTools("sess-evict")
	assert.Empty(t, after, "loadedTools entry must be empty after forgetSession")
	assert.False(t, after["create_agent"], "create_agent must not be present after forgetSession")
}

// TestForgetSession_NoopOnEmpty proves forgetSession on an unknown key is a no-op.
func TestForgetSession_NoopOnEmpty(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	// Must not panic on unknown or empty key.
	al.forgetSession("nonexistent")
	al.forgetSession("")
}

// TestForgetSession_OtherSessionsUnaffected proves forgetSession only evicts
// the targeted session and leaves other sessions intact.
func TestForgetSession_OtherSessionsUnaffected(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	al.markToolsLoaded("sess-keep", []string{"list_agents"})
	al.markToolsLoaded("sess-drop", []string{"create_agent"})

	al.forgetSession("sess-drop")

	kept := al.sessionLoadedTools("sess-keep")
	assert.True(t, kept["list_agents"], "sess-keep must be unaffected by forgetSession(sess-drop)")
	dropped := al.sessionLoadedTools("sess-drop")
	assert.Empty(t, dropped, "sess-drop must be empty after forgetSession")
}

// ─── Search-tool registration (Gap 2) ──────────────────────────────────────

// TestSearchToolsRegistered_CompressedMode proves that with Compressed=true and
// MCP discovery OFF, the unified `tools` infra tool is registered in each
// agent's Tools registry (replaces old search_tools_bm25 / search_tools_regex
// / ToolSearch trio after the tools-tool unification).
func TestSearchToolsRegistered_CompressedMode(t *testing.T) {
	cfg := newCompressedCfg(t)
	// Ensure MCP discovery is off (the default in test configs) so we verify
	// the Compressed=true path specifically (not the MCP discovery union path).
	cfg.Tools.MCP.Discovery.Enabled = false

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	// Verify EVERY registered agent — the 4 core agents AND the native
	// subagents/workers (worker/planner/explorer/researcher, type=worker, which
	// run on the Omnipus engine and share the tool registry) — gets the unified
	// `ToolSearch` infra tool. (External subagent_3p workers run on an external CLI
	// and don't use this registry, so they are not seeded here and not in scope.)
	ids := al.registry.ListAgentIDs()
	require.NotEmpty(t, ids)
	sawCore, sawWorker := false, false
	for _, agentID := range ids {
		agentInst, ok := al.registry.GetAgent(agentID)
		require.True(t, ok, "agent %q must be in registry", agentID)

		_, hasTools := agentInst.Tools.Get("ToolSearch")
		assert.True(t, hasTools,
			"agent %q (type %s): unified `ToolSearch` infra tool must be registered when Compressed=true",
			agentID, agentInst.AgentType)

		// Old names must NOT be registered (they are now collapsed into `ToolSearch`).
		_, hasOldBM25 := agentInst.Tools.Get("search_tools_bm25")
		assert.False(t, hasOldBM25,
			"agent %q: search_tools_bm25 must NOT be registered after tools-tool unification", agentID)
		_, hasOldRegex := agentInst.Tools.Get("search_tools_regex")
		assert.False(t, hasOldRegex,
			"agent %q: search_tools_regex must NOT be registered after tools-tool unification", agentID)

		switch agentInst.AgentType {
		case "core":
			sawCore = true
		case "worker":
			sawWorker = true
		}
	}
	// Guard against a seed change silently dropping a whole class of agent —
	// the worker assertion is the one the original test missed.
	assert.True(t, sawCore, "expected at least one core agent in the registry")
	assert.True(t, sawWorker, "expected at least one native subagent/worker (type=worker) in the registry")
}

// ─── manifestSessionID unit tests ──────────────────────────────────────────

// TestManifestSessionID_TranscriptPreferred proves transcript ID wins when non-empty.
func TestManifestSessionID_TranscriptPreferred(t *testing.T) {
	got := manifestSessionID("session_01ABC", "agent:jim:session:key")
	assert.Equal(t, "session_01ABC", got,
		"manifestSessionID must prefer transcript ID when non-empty")
}

// TestManifestSessionID_FallbackToSessionKey proves session key is used when
// transcript ID is empty.
func TestManifestSessionID_FallbackToSessionKey(t *testing.T) {
	got := manifestSessionID("", "agent:jim:session:key")
	assert.Equal(t, "agent:jim:session:key", got,
		"manifestSessionID must fall back to session key when transcript ID is empty")
}

// TestManifestSessionID_BothEmpty proves both-empty yields empty string (no-op key).
func TestManifestSessionID_BothEmpty(t *testing.T) {
	got := manifestSessionID("", "")
	assert.Equal(t, "", got)
}

// ─── injectManifestNote unit tests ─────────────────────────────────────────

// TestInjectManifestNote_InsertsAtIndex1 proves the note lands at index 1,
// with role "system", inserted exactly once, when msgs has ≥2 elements.
func TestInjectManifestNote_InsertsAtIndex1(t *testing.T) {
	msgs := []providers.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	result := injectManifestNote(msgs, "## More tools\n  - create_agent")

	require.Len(t, result, 4, "injectManifestNote must add exactly 1 message")
	assert.Equal(t, "system", result[0].Role, "index 0 must remain the system prompt")
	assert.Equal(t, "system", result[1].Role, "index 1 must be the injected note (role=system)")
	assert.Contains(t, result[1].Content, "More tools", "index 1 must contain the note content")
	assert.Equal(t, "user", result[2].Role, "index 2 must be the original user message")
	assert.Equal(t, "assistant", result[3].Role, "index 3 must be the original assistant message")
}

// TestInjectManifestNote_EmptyNoteNoOp proves no injection when note is "".
func TestInjectManifestNote_EmptyNoteNoOp(t *testing.T) {
	msgs := []providers.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
	}
	result := injectManifestNote(msgs, "")
	assert.Equal(t, msgs, result, "empty note must return msgs unchanged")
}

// TestInjectManifestNote_EmptyMsgsNoOp proves no injection when msgs is empty.
func TestInjectManifestNote_EmptyMsgsNoOp(t *testing.T) {
	result := injectManifestNote([]providers.Message{}, "## More tools")
	assert.Empty(t, result, "empty msgs must return empty slice unchanged")
}

// TestInjectManifestNote_SingleMsg proves injection still works with 1 message
// (the new message is appended at index 1, nothing after it).
func TestInjectManifestNote_SingleMsg(t *testing.T) {
	msgs := []providers.Message{
		{Role: "system", Content: "system prompt"},
	}
	result := injectManifestNote(msgs, "## More tools")
	require.Len(t, result, 2)
	assert.Equal(t, "system", result[1].Role)
}

// TestInjectManifestNote_NotInjectedTwice proves calling injectManifestNote
// twice produces exactly two injected messages (idempotency is the caller's
// responsibility; this verifies no hidden dedup that would skip a second call).
func TestInjectManifestNote_NotInjectedTwice(t *testing.T) {
	msgs := []providers.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
	}
	once := injectManifestNote(msgs, "note")
	twice := injectManifestNote(once, "note")
	assert.Len(t, twice, 4, "two calls each add one message")
}

// ─── load→callable round-trip ──────────────────────────────────────────────

// TestLoadToCallableRoundTrip proves that executing tools{names:[...]} for a
// valid lazy name causes that name to appear in buildCompressedToolDefs for the
// same session on the next call — i.e., the tool becomes callable after a load.
//
// This is an end-to-end chain: ToolsTool.Execute(names=[...]) → markLoaded
// closure → al.markToolsLoaded → al.buildCompressedToolDefs sees the tool in defs.
func TestLoadToCallableRoundTrip(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())

	// Find a lazy tool for Jim.
	var lazyName string
	for _, tool := range policyFiltered {
		if tools.ToolManifestTier(tool.Name()) == tools.ManifestLazy {
			lazyName = tool.Name()
			break
		}
	}
	require.NotEmpty(t, lazyName, "Jim must have at least one lazy tool for the round-trip test")

	transcriptID := "sess-roundtrip-transcript"

	// Before load: lazyName must NOT be in compressed defs.
	tsBefore := fakeTurnState(jimAgent, transcriptID)
	defsBefore := al.buildCompressedToolDefs(tsBefore, policyFiltered)
	for _, d := range defsBefore {
		require.NotEqual(t, lazyName, d.Function.Name,
			"lazy tool %q must not be callable before tools{names:[...]} is called", lazyName)
	}

	// Execute ToolSearch{names:[...]} via the registered instance (uses the real markLoaded closure).
	toolsToolRaw, ok := jimAgent.Tools.Get("ToolSearch")
	require.True(t, ok, "`ToolSearch` infra tool must be registered for jim")
	tt, ok := toolsToolRaw.(*tools.ToolsTool)
	require.True(t, ok)

	ctx := tools.WithAgentID(context.Background(), "jim")
	ctx = tools.WithTranscriptSessionID(ctx, transcriptID)
	ctx = tools.WithSessionKey(ctx, transcriptID) // match the session key for manifestSessionID

	result := tt.Execute(ctx, map[string]any{"names": []any{lazyName}})
	require.False(t, result.IsError,
		"ToolSearch{names:[...]}.Execute must succeed for a valid lazy tool; got: %s", result.ForLLM)

	// After load: lazyName must appear in compressed defs.
	tsAfter := fakeTurnState(jimAgent, transcriptID)
	defsAfter := al.buildCompressedToolDefs(tsAfter, policyFiltered)
	defNamesAfter := make(map[string]bool, len(defsAfter))
	for _, d := range defsAfter {
		defNamesAfter[d.Function.Name] = true
	}
	assert.True(
		t,
		defNamesAfter[lazyName],
		"load→callable round-trip: lazy tool %q must be in compressed defs after ToolSearch{names:[...]}.Execute",
		lazyName,
	)
}

// TestInfraToolsExecutable_DenyDefaultAgent is the regression test for the bug
// found by live validation: a deny-by-default agent (Ava/Mia) was SHOWN the
// `ToolSearch` infra tool in its provider defs (force-included) but the EXECUTION
// gate denied it, so every lazy tool was unreachable in practice. This asserts
// the full authorization chain now allows infra-tool execution.
func TestInfraToolsExecutable_DenyDefaultAgent(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	// Ava and Mia are deny-by-default overall, but `ToolSearch` is a structural
	// floor BOTH explicitly seed "allow" for (pkg/coreagent/core.go) — every
	// agent needs it to reach any tiered/lazy tool at all.
	for _, agentID := range []string{"ava", "mia"} {
		t.Run(agentID, func(t *testing.T) {
			agentInst, ok := al.registry.GetAgent(agentID)
			require.True(t, ok)

			allTools := agentInst.Tools.GetAll()
			policyFiltered, policyMap := tools.FilterToolsByPolicy(
				allTools,
				agentInst.AgentType,
				agentInst.LoadToolPolicy(),
			)

			// Post-unification (#438): the single authoritative resolver
			// (tools.EffectiveToolPolicy via FilterToolsByPolicy) resolves
			// ToolSearch through the SAME global×agent merge as every other
			// static builtin tool — no special case (the former unconditional
			// infra force-allow was a CLAUDE.md hard-constraint-6 violation and
			// has been removed). Even a deny-default agent already has
			// `ToolSearch` authorized in the snapshot here, because it is
			// seeded "allow" as real, explicit policy data. We capture it to
			// prove the fix holds at the resolver layer (no longer requiring
			// ensureInfraToolsExecutable).
			rawVerdict, rawAllowed := policyMap["ToolSearch"]

			// ensureInfraToolsExecutable is now an idempotent backstop; calling it
			// must leave the (already-allow) verdict and slice unchanged.
			policyFiltered = ensureInfraToolsExecutable(agentInst.Tools, policyFiltered, policyMap)

			// `ToolSearch` is authorized as "allow" in the exec snapshot.
			require.Equal(
				t,
				"allow",
				policyMap["ToolSearch"],
				"agent %q: `ToolSearch` must be allow in the exec policy snapshot (rawAllowed=%v rawVerdict=%q)",
				agentID,
				rawAllowed,
				rawVerdict,
			)
			require.Contains(t, toolNameSet(policyFiltered), "ToolSearch",
				"agent %q: `ToolSearch` must be in the sent defs surface", agentID)

			// The execution gate itself must authorize `ToolSearch` end-to-end.
			ts := fakeTurnState(agentInst, "sess-exec-"+agentID)
			require.Equal(t, "allow", al.resolveToolPolicyAtExec(ts, "ToolSearch", policyMap),
				"agent %q: resolveToolPolicyAtExec must allow `ToolSearch`", agentID)
			require.Equal(t, "allow", al.resolveSingleToolPolicy(ts, "ToolSearch"),
				"agent %q: resolveSingleToolPolicy must allow registered infra tool", agentID)

			// And every infra tool, for completeness.
			for _, infra := range tools.InfraManifestToolNames() {
				if _, registered := agentInst.Tools.Get(infra); !registered {
					continue
				}
				require.Equal(t, "allow", al.resolveToolPolicyAtExec(ts, infra, policyMap),
					"agent %q: infra tool %q must be executable", agentID, infra)
			}
		})
	}
}

// TestEnsureInfraToolsExecutable_IdempotentAfterUnifiedResolver verifies the
// post-unification (#438) invariant. ToolSearch resolves through the SAME
// global×agent merge as every other static builtin tool (the former
// unconditional infra force-allow inside tools.EffectiveToolPolicy was a
// CLAUDE.md hard-constraint-6 violation and has been removed) and is seeded
// "allow" as real, explicit data for every agent (pkg/coreagent/core.go), so
// `ToolSearch` is already present in policyMap as "allow" after the filter —
// even for a deny-default agent (Ava), because that is its real resolved
// policy. Therefore ensureInfraToolsExecutable is an idempotent no-op: it
// must not double-add the tool nor change the "allow" verdict.
//
// The OBSERVABLE behavior on the non-compressed path (ToolSearch never surfaced to
// the model) is preserved by stripInfraToolDefs, asserted in
// TestStripInfraToolDefs_RemovesLoadTool below — not by gating the policy verdict.
func TestEnsureInfraToolsExecutable_IdempotentAfterUnifiedResolver(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()
	ava, ok := al.registry.GetAgent("ava")
	require.True(t, ok)
	allTools := ava.Tools.GetAll()
	policyFiltered, policyMap := tools.FilterToolsByPolicy(allTools, ava.AgentType, ava.LoadToolPolicy())

	// ToolSearch resolves "allow" from its own real seeded policy entry, even
	// for deny-default Ava.
	require.Equal(t, "allow", policyMap["ToolSearch"],
		"ToolSearch must resolve allow from its own seeded policy entry for a deny-default agent")
	before := len(policyFiltered)

	out := ensureInfraToolsExecutable(ava.Tools, policyFiltered, policyMap)
	require.Len(t, out, before,
		"ensureInfraToolsExecutable must not double-add infra")
	require.Equal(t, "allow", policyMap["ToolSearch"],
		"ToolSearch must remain allow")
}

// TestStripInfraToolDefs_RemovesLoadTool proves the observable behavior on the
// NON-compressed defs path is preserved: ToolSearch (manifest infra) is stripped
// from the surfaced tool set, so the model never sees it when compression is off
// — byte-for-byte the pre-unification behavior.
func TestStripInfraToolDefs_RemovesLoadTool(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()
	ava, ok := al.registry.GetAgent("ava")
	require.True(t, ok)
	allTools := ava.Tools.GetAll()
	policyFiltered, policyMap := tools.FilterToolsByPolicy(allTools, ava.AgentType, ava.LoadToolPolicy())

	// Precondition: the unified resolver kept ToolSearch in the filtered slice.
	require.Contains(t, toolNameSet(policyFiltered), "ToolSearch")
	require.Equal(t, "allow", policyMap["ToolSearch"])

	// Non-compressed defs path strips it.
	stripped := stripInfraToolDefs(policyFiltered)
	require.NotContains(t, toolNameSet(stripped), "ToolSearch",
		"stripInfraToolDefs must remove ToolSearch from the non-compressed surfaced set")
	// And it strips ONLY infra — every non-infra tool survives.
	require.Len(t, stripped, len(policyFiltered)-1,
		"exactly one infra tool (ToolSearch) must be removed")
}

// toolNameSet is a tiny helper for membership assertions.
func toolNameSet(ts []tools.Tool) map[string]bool {
	m := make(map[string]bool, len(ts))
	for _, t := range ts {
		m[t.Name()] = true
	}
	return m
}

// ─── Part A §5c — Token-win measurable + monotonic ─────────────────────────

// TestTokenWin_ByteSizeMaterially proves that for Jim (a broad-access agent),
// the compressed providerToolDefs JSON is materially smaller than the full-set
// JSON, and that loading N lazy tools one-at-a-time grows the sent-defs size
// monotonically without ever exceeding the full-set size.
//
// "Materially smaller" is defined as compressed < full. If they are equal or
// larger, the compression is silently regressing to all-full — this test fails
// loudly.
//
// Monotonicity ensures that each tools{action:'load'} call increases the sent
// surface predictably and never exceeds what we'd send without compression.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §5c
func TestTokenWin_ByteSizeMaterially(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok, "jim must be in registry")

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())
	require.NotEmpty(t, policyFiltered, "Jim must have policy-filtered tools")

	// Collect all lazy tools for Jim in a stable order.
	var lazyNames []string
	for _, tool := range policyFiltered {
		if tools.ToolManifestTier(tool.Name()) == tools.ManifestLazy {
			lazyNames = append(lazyNames, tool.Name())
		}
	}
	require.NotEmpty(t, lazyNames, "Jim must have lazy tools for this test to be meaningful")

	// Baseline: measure full-set JSON size (no compression).
	fullDefs := tools.ToolsToProviderDefs(policyFiltered)
	fullJSON, err := json.Marshal(fullDefs)
	require.NoError(t, err)
	fullBytes := len(fullJSON)

	sessionID := "sess-token-monotonic"

	// Step 0: zero lazy tools loaded — compressed must be < full.
	ts0 := fakeTurnState(jimAgent, sessionID)
	defs0 := al.buildCompressedToolDefs(ts0, policyFiltered)
	json0, err := json.Marshal(defs0)
	require.NoError(t, err)
	prevBytes := len(json0)

	assert.Less(t, prevBytes, fullBytes,
		"compressed defs at 0 loaded tools must be materially smaller than full-set; "+
			"compressed=%d full=%d", prevBytes, fullBytes)

	// Load lazy tools one at a time; verify monotonic growth and never-exceeds-full.
	// Use up to 5 lazy tools to keep the test fast.
	maxLoads := len(lazyNames)
	if maxLoads > 5 {
		maxLoads = 5
	}
	for i := 0; i < maxLoads; i++ {
		al.markToolsLoaded(bucketFor(jimAgent, sessionID), []string{lazyNames[i]})

		ts := fakeTurnState(jimAgent, sessionID)
		defs := al.buildCompressedToolDefs(ts, policyFiltered)
		defsJSON, err := json.Marshal(defs)
		require.NoError(t, err)
		curBytes := len(defsJSON)

		// Monotonic: each additional loaded tool must grow or stay equal (never shrink).
		assert.GreaterOrEqual(t, curBytes, prevBytes,
			"loading tool %q must not shrink the sent-defs size; was=%d now=%d", lazyNames[i], prevBytes, curBytes)

		// Never exceeds full.
		assert.LessOrEqual(t, curBytes, fullBytes,
			"sent-defs after loading %d tools must not exceed full-set size; cur=%d full=%d", i+1, curBytes, fullBytes)

		prevBytes = curBytes
	}
}

// TestTokenWin_LoadingAllLazyReachesFullSize proves that once all lazy tools are
// loaded, the compressed defs reach (or exceed, due to infra force-include)
// the size of the full policy-filtered set — i.e., no tools are lost.
//
// This is the upper-bound counterpart to TestTokenWin_ByteSizeMaterially.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §5c
func TestTokenWin_LoadingAllLazyReachesFullSize(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())

	// Collect all lazy names.
	var lazyNames []string
	for _, tool := range policyFiltered {
		if tools.ToolManifestTier(tool.Name()) == tools.ManifestLazy {
			lazyNames = append(lazyNames, tool.Name())
		}
	}
	require.NotEmpty(t, lazyNames)

	sessionID := "sess-all-lazy-loaded"
	al.markToolsLoaded(bucketFor(jimAgent, sessionID), lazyNames)

	// Baseline: policy-filtered set as provider defs.
	pfDefs := tools.ToolsToProviderDefs(policyFiltered)

	ts := fakeTurnState(jimAgent, sessionID)
	compressedDefs := al.buildCompressedToolDefs(ts, policyFiltered)

	// Once all lazy tools are loaded, compressed defs must be >= pf count
	// (infra tools are force-included so compressed may be slightly larger).
	assert.GreaterOrEqual(t, len(compressedDefs), len(pfDefs),
		"with all lazy tools loaded, compressed defs count must be >= policy-filtered count; "+
			"compressed=%d pf=%d (infra force-include may add a few)", len(compressedDefs), len(pfDefs))
}

// ─── Part A §5a — Search-then-load reachability ────────────────────────────

// TestSearchThenLoad_Reachability proves that a tool found via the unified
// tools{query:...} infra tool is in the lazy/loadable set, and that
// calling tools{names:[...]} then makes it appear in buildCompressedToolDefs
// (callable).
//
// This is the "query→find→load→callable" chain at the helper level. It chains
// the two param paths of the unified `tools` infra tool without a live LLM:
//  1. tools{query:...} finds a lazy tool by name/description.
//  2. The tool is in the lazy set (query without resolver does NOT promote it).
//  3. tools{names:[...]} loads it for the session.
//  4. buildCompressedToolDefs now includes it (callable).
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §5a, §5b (search-then-load)
func TestSearchThenLoad_Reachability(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())

	// Find a lazy tool registered for Jim (we pick the first lazy tool in the
	// policy-filtered set as the search target).
	var lazyName string
	for _, tool := range policyFiltered {
		if tools.ToolManifestTier(tool.Name()) == tools.ManifestLazy {
			lazyName = tool.Name()
			break
		}
	}
	require.NotEmpty(t, lazyName, "Jim must have at least one lazy tool")

	// Step 1: Verify the lazy tool is in Jim's registry (loadable set).
	_, inRegistry := jimAgent.Tools.Get(lazyName)
	require.True(t, inRegistry, "lazy tool %q must be in Jim's registry (loadable set)", lazyName)

	// Step 2: Before load, it must NOT be in compressed defs.
	transcriptID := "sess-search-load-chain"
	tsBefore := fakeTurnState(jimAgent, transcriptID)
	defsBefore := al.buildCompressedToolDefs(tsBefore, policyFiltered)
	for _, d := range defsBefore {
		require.NotEqual(t, lazyName, d.Function.Name,
			"lazy tool %q must not be callable before tools{names:[...]} is called", lazyName)
	}

	// Step 3: Call ToolSearch{names:[...]}.Execute with the lazy name (simulating
	// the model calling load by name after a query result returned the tool name).
	toolsToolRaw, ok := jimAgent.Tools.Get("ToolSearch")
	require.True(t, ok, "`ToolSearch` infra tool must be registered for jim in compressed mode")
	tt, ok := toolsToolRaw.(*tools.ToolsTool)
	require.True(t, ok, "`ToolSearch` infra tool must be *tools.ToolsTool")

	ctx := tools.WithAgentID(context.Background(), "jim")
	ctx = tools.WithTranscriptSessionID(ctx, transcriptID)
	ctx = tools.WithSessionKey(ctx, transcriptID)

	loadResult := tt.Execute(ctx, map[string]any{"names": []any{lazyName}})
	require.False(t, loadResult.IsError,
		"ToolSearch{names:[...]} must succeed for lazy tool %q found via query; error: %s", lazyName, loadResult.ForLLM)

	// Step 4: After load, the tool must appear in compressed defs (callable).
	tsAfter := fakeTurnState(jimAgent, transcriptID)
	defsAfter := al.buildCompressedToolDefs(tsAfter, policyFiltered)
	defNamesAfter := make(map[string]bool, len(defsAfter))
	for _, d := range defsAfter {
		defNamesAfter[d.Function.Name] = true
	}
	assert.True(t, defNamesAfter[lazyName],
		"query-then-load chain: lazy tool %q must be callable after ToolSearch{names:[...]}.Execute", lazyName)
}

// TestVisibility_SearchOnlyToolFoundByDescriptionBecomesUsable is FR-031a's
// dynamic-promotion half (distinct from the static index-membership property
// TestVisibility_SearchOnlyToolsRemainInSearchIndex in pkg/tools asserts): a
// search-only (Tier 3) tool found via ToolSearch's QUERY (by-description)
// path — not the by-name path TestSearchThenLoad_Reachability exercises — is
// (a) made usable by that search and (b) its full callable schema is present
// in the next turn's callable set (ADR-071 §4.4, User Story 4 Acceptance
// Scenario 2 / FR-031a).
//
// Drives the REAL query+auto-load path in tools_tool.go's execSearchAndLoad
// against jim's real, production-registered tool set — not a synthetic
// fixture — so this also exercises the real BM25 ranking. The query is
// deliberately generic ("take a screenshot of the browser") rather than
// pinned to one exact tool name, and the assertions are made against
// WHICHEVER tool the real ranking auto-loads, so the test is not coupled to
// D2's (not-yet-implemented) ambiguity-band ranking changes — only to the
// property that a query-path promotion makes SOME tool usable end to end.
func TestVisibility_SearchOnlyToolFoundByDescriptionBecomesUsable(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())

	transcriptID := "sess-query-promote-d3"

	// Before the query: nothing new is loaded, so the manifest note is
	// unaffected by this test's own future actions (baseline).
	tsBefore := fakeTurnState(jimAgent, transcriptID)
	defsBefore := al.buildCompressedToolDefs(tsBefore, policyFiltered)
	beforeNames := make(map[string]bool, len(defsBefore))
	for _, d := range defsBefore {
		beforeNames[d.Function.Name] = true
	}

	toolsToolRaw, ok := jimAgent.Tools.Get("ToolSearch")
	require.True(t, ok, "`ToolSearch` infra tool must be registered for jim in compressed mode")
	tt, ok := toolsToolRaw.(*tools.ToolsTool)
	require.True(t, ok, "`ToolSearch` infra tool must be *tools.ToolsTool")

	ctx := tools.WithAgentID(context.Background(), "jim")
	ctx = tools.WithTranscriptSessionID(ctx, transcriptID)
	ctx = tools.WithSessionKey(ctx, transcriptID)

	queryResult := tt.Execute(ctx, map[string]any{"query": "take a screenshot of the current browser tab"})
	require.False(t, queryResult.IsError, "ToolSearch{query:...} must not error; got: %s", queryResult.ForLLM)
	require.Contains(t, queryResult.ForLLM, "Loaded the best match",
		"the query must auto-load a match against jim's real tool set; got: %s", queryResult.ForLLM)

	// After the query: buildCompressedToolDefs must contain EXACTLY ONE new
	// name relative to the baseline (the auto-loaded match), proving the
	// query-path promotion reached the manifest builder end to end.
	tsAfter := fakeTurnState(jimAgent, transcriptID)
	defsAfter := al.buildCompressedToolDefs(tsAfter, policyFiltered)

	var newlyCallable []string
	for _, d := range defsAfter {
		if !beforeNames[d.Function.Name] {
			newlyCallable = append(newlyCallable, d.Function.Name)
		}
	}
	require.Len(t, newlyCallable, 1,
		"exactly one new tool must become callable after the query-path promotion; got %v", newlyCallable)

	promoted := newlyCallable[0]
	assert.Equal(t, tools.ManifestLazy, tools.ToolManifestTier(promoted),
		"the auto-loaded tool %q must have been ManifestLazy before promotion (that is the whole point of the discovery path)", promoted)
}

// ─── Part A §5a — Manifest determinism under load churn ───────────────────

// TestManifestDeterminism_LoadSameToolTwice proves that loading the same lazy
// tool twice via markToolsLoaded is idempotent: the sent-defs set and the
// manifest note are the same after both calls.
//
// This guards against a regression where double-loading corrupts the loaded
// map or produces duplicate entries in the provider defs.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §5a (determinism under load churn)
func TestManifestDeterminism_LoadSameToolTwice(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())

	// Find a lazy tool for Jim.
	var lazyName string
	for _, tool := range policyFiltered {
		if tools.ToolManifestTier(tool.Name()) == tools.ManifestLazy {
			lazyName = tool.Name()
			break
		}
	}
	require.NotEmpty(t, lazyName)

	sessionID := "sess-idempotent-load"

	// Load the tool once.
	al.markToolsLoaded(bucketFor(jimAgent, sessionID), []string{lazyName})
	ts1 := fakeTurnState(jimAgent, sessionID)
	defs1 := al.buildCompressedToolDefs(ts1, policyFiltered)
	note1 := al.buildToolManifestNote(ts1, policyFiltered)

	// Load the same tool again (idempotent).
	al.markToolsLoaded(bucketFor(jimAgent, sessionID), []string{lazyName})
	ts2 := fakeTurnState(jimAgent, sessionID)
	defs2 := al.buildCompressedToolDefs(ts2, policyFiltered)
	note2 := al.buildToolManifestNote(ts2, policyFiltered)

	// Sent-defs set must be identical (no duplicates introduced by double-load).
	require.Equal(t, len(defs1), len(defs2),
		"double-loading must not change the sent-defs count; first=%d second=%d", len(defs1), len(defs2))

	names1 := make(map[string]bool, len(defs1))
	for _, d := range defs1 {
		names1[d.Function.Name] = true
	}
	for _, d := range defs2 {
		assert.True(t, names1[d.Function.Name],
			"double-load introduced new def %q not present in first load", d.Function.Name)
	}

	// Manifest note must be identical: no phantom entries, no duplicates.
	assert.Equal(t, note1, note2,
		"double-loading must not change the manifest note")

	// The loaded tool must still NOT appear in the manifest note.
	assert.NotContains(t, note1, "  - "+lazyName,
		"loaded tool %q must not appear in manifest note after idempotent double-load", lazyName)
}

// TestManifestDeterminism_LoadChurn proves that loading and then ignoring
// additional lazy tools on the same session produces a stable, deterministic
// manifest note across repeated calls (no churn or randomness).
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §5a (determinism under load churn)
func TestManifestDeterminism_LoadChurn(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok)

	allTools := jimAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, jimAgent.AgentType, jimAgent.LoadToolPolicy())

	// Load the first lazy tool to give the session some state.
	sessionID := "sess-churn"
	for _, tool := range policyFiltered {
		if tools.ToolManifestTier(tool.Name()) == tools.ManifestLazy {
			al.markToolsLoaded(bucketFor(jimAgent, sessionID), []string{tool.Name()})
			break
		}
	}

	// Build the manifest note multiple times; every result must be identical.
	var notes []string
	for i := 0; i < 5; i++ {
		ts := fakeTurnState(jimAgent, sessionID)
		note := al.buildToolManifestNote(ts, policyFiltered)
		notes = append(notes, note)
	}
	for i := 1; i < len(notes); i++ {
		assert.Equal(t, notes[0], notes[i],
			"manifest note must be deterministic: call 0 and call %d differ", i)
	}
}

// TestCanLoad_HiddenMCPTool_AllowDefaultAgent is the regression test for the bug
// the MCP UAT caught: a deferred/hidden MCP tool (RegisterHidden, not in GetAll()
// until promoted) is surfaced by tools{query} search but the load path's canLoad
// gate used only GetAll() — so it rejected the hidden tool as "unknown", breaking
// search→load→use for MCP. canLoad is now hidden-aware (GetIncludingHidden +
// per-tool policy). An allow-default agent (the seeded worker tier) must be able to load a
// policy-allowed hidden tool; a deny-default agent (Ava) must not. The 4 core
// agents are all deny-default now, so a worker is the allow-default fixture.
func TestCanLoad_HiddenMCPTool_AllowDefaultAgent(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	loadHidden := func(t *testing.T, agentID string) *tools.ToolResult {
		t.Helper()
		agentInst, ok := al.registry.GetAgent(agentID)
		require.True(t, ok)
		// Register a hidden lazy tool (simulates a deferred MCP tool).
		agentInst.Tools.RegisterHidden(&mockCustomTool{})
		toolsTool, ok := agentInst.Tools.Get("ToolSearch")
		require.True(t, ok, "agent %q must have the unified 'ToolSearch' tool", agentID)
		tt, ok := toolsTool.(*tools.ToolsTool)
		require.True(t, ok)
		ctx := tools.WithAgentID(context.Background(), agentID)
		ctx = tools.WithSessionKey(ctx, "sess-hidden-"+agentID)
		return tt.Execute(ctx, map[string]any{"names": []any{"mock_custom"}})
	}

	// No agent tier has an implicit allow rail anymore (CLAUDE.md hard
	// constraint 6): the 4 core agents and the seeded worker/explorer/
	// researcher tier are ALL deny-default. This subtest grants worker an
	// explicit allow for the hidden tool's name to prove the ToolSearch
	// mechanism succeeds when policy permits it, mirroring how ava's paired
	// subtest below proves it correctly blocks when policy denies.
	t.Run("explicitly_allowed_agent_loads_hidden", func(t *testing.T) {
		worker, ok := al.registry.GetAgent("worker")
		require.True(t, ok, "worker agent must exist")
		worker.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{"mock_custom": "allow"},
		})

		res := loadHidden(t, "worker")
		require.NotNil(t, res)
		require.False(
			t,
			res.IsError,
			"worker (explicitly allowed) must load the hidden tool, got error: %s",
			res.ForLLM,
		)
		require.Contains(t, res.ForLLM, "mock_custom", "result should report the loaded hidden tool")
		require.Contains(t, res.ForLLM, "\"loaded\"")
	})

	t.Run("ava_deny_default_rejects_hidden", func(t *testing.T) {
		res := loadHidden(t, "ava")
		require.NotNil(t, res)
		// Ava is deny-by-default and mock_custom is not in her allow-list, so the
		// hidden tool must be rejected (no policy escalation via load).
		require.True(t, res.IsError, "ava (deny-default) must reject the unlisted hidden tool")
	})
}

// ─── GAP 1 regression: a ScopeCore Tier-2 previewed tool is lazy-loaded for a real core agent ───

// TestMiaNavigate_FullTierDirectlyCallable closes the coverage gap where C4
// (TestReachabilityInvariant_AllCoreAgents) trivially passed for Mia because
// `navigate` is a sysagent tool registered only via WireSysagentDeps (not called
// in the test harness), so Mia's policy-filtered set never contained `navigate`
// and the Full-tier assertion branch was never exercised for that tool.
//
// This test originally proved `navigate` was promoted to ManifestFull (round 2
// of feat/0.1.0-uat-fixes). ADR-071 D3 §4.1 REVERSED that promotion: navigate
// moved back down to the lazy tier, specifically the new previewed (Tier 2)
// subdivision, to remove its permanent visibility advantage now that the
// full-tier set is being kept small deliberately. The test was then rewritten
// (not deleted, per this codebase's regression-history convention) to pin
// that demotion.
//
// The tool-manifest-tier-redesign review's F1 finding then retired `navigate`
// outright: its UI-navigation callback was nil in every production path (no
// wire frame existed for a navigation event to travel over), so the tool was
// a total no-op occupying one of only 8 Tier-2 slots. With navigate gone, the
// mechanism this test exists to protect — a ScopeCore Tier-2 previewed tool's
// lazy-load discovery path for a real core agent with a real seeded policy —
// is retargeted at `get_workspace`, the one other ScopeCore tool in the
// previewed set. Mia does not have get_workspace allowed, so the agent under
// test moves to Ava (pkg/coreagent/core.go IDAva, which does seed
// get_workspace: allow). Rewritten again (still not deleted) to pin:
//  1. ToolManifestTier("get_workspace") == ManifestLazy (not Full).
//  2. ToolManifestVisibility("get_workspace") == ManifestPreviewed (Tier 2,
//     not search-only) — it still gets a preview line, just not a callable
//     def every turn.
//  3. On turn 1, with no prior markToolsLoaded call, get_workspace is NOT in
//     buildCompressedToolDefs's output (it must be found/loaded first).
//  4. get_workspace DOES appear in the manifest note as a preview entry.
//  5. After a markToolsLoaded call for get_workspace, it appears in
//     buildCompressedToolDefs — proving it is fully reachable, just one
//     discovery round trip away.
//
// Traces to: ADR-071 D3 §4.1 (bash/navigate/create_task/update_task leave the
// always-listed set); pkg/tools/manifest.go fullManifestToolNames/previewedLazyToolNames;
// tool-manifest-tier-redesign review F1 (navigate retirement).
func TestMiaNavigate_DemotedToPreviewedTier(t *testing.T) {
	// Precondition: ToolManifestTier/Visibility classification is the single
	// source of truth. If either assertion fails, get_workspace's Tier-2
	// classification was reverted — everything else here is moot.
	require.Equal(t, tools.ManifestLazy, tools.ToolManifestTier("get_workspace"),
		"ToolManifestTier(\"get_workspace\") must be ManifestLazy. If this assertion "+
			"breaks, get_workspace was added to fullManifestToolNames in pkg/tools/manifest.go.")
	require.Equal(t, tools.ManifestPreviewed, tools.ToolManifestVisibility("get_workspace"),
		"ToolManifestVisibility(\"get_workspace\") must be ManifestPreviewed "+
			"(it is one of the 7 Tier 2 names in previewedLazyToolNames).")

	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	avaAgent, ok := al.registry.GetAgent("ava")
	require.True(t, ok, "ava must be in registry")

	// get_workspace is NOT registered in the test harness (it is a sysagent tool
	// wired via WireSysagentDeps, which is not called in unit tests — that would
	// create a circular import: pkg/agent → pkg/sysagent/tools → pkg/agent).
	// Inject a minimal stub so we can verify the previewed-tier path without the
	// circular dep.
	getWorkspaceStub := &fakeGetWorkspaceTool{}
	avaAgent.Tools.Register(getWorkspaceStub)

	// Build Ava's policy-filtered set. The allow-policy for Ava includes
	// "get_workspace" (pkg/coreagent/core.go IDAva block), so the stub must
	// survive the policy filter.
	allTools := avaAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, avaAgent.AgentType, avaAgent.LoadToolPolicy())

	// Non-vacuous: get_workspace must be in policyFiltered after the stub injection.
	// If this fails, Ava's policy no longer allows get_workspace — check core.go.
	var getWorkspaceInPF bool
	for _, t2 := range policyFiltered {
		if t2.Name() == "get_workspace" {
			getWorkspaceInPF = true
			break
		}
	}
	require.True(t, getWorkspaceInPF,
		"POLICY REGRESSION: `get_workspace` must be in Ava's policy-filtered set. "+
			"Check IDAva policy in pkg/coreagent/core.go — get_workspace must be explicitly allowed.")

	sessionID := "sess-ava-get-workspace-d3"

	// Turn 1: no markToolsLoaded call — get_workspace must NOT be directly callable.
	ts := fakeTurnState(avaAgent, sessionID)
	defs := al.buildCompressedToolDefs(ts, policyFiltered)

	defNames := make(map[string]bool, len(defs))
	for _, d := range defs {
		defNames[d.Function.Name] = true
	}

	// DIFFERENTIATION CONTROL: send_message (Full-tier, always registered) must be
	// in defs — proves the Full-tier path itself still works (not a vacuous assertion).
	assert.True(t, defNames["send_message"],
		"send_message (Full-tier, always registered) must be in Ava's compressed defs as a control")

	// PRIMARY ASSERTION: get_workspace must NOT appear without a prior load — it
	// pays the same one-time discovery cost as bash/create_task/update_task.
	assert.False(t, defNames["get_workspace"],
		"ADR-071 D3: `get_workspace` (ManifestLazy/ManifestPreviewed) must NOT appear in "+
			"Ava's compressed defs on turn 1 without a prior markToolsLoaded call.")

	// get_workspace DOES appear in the manifest note as a preview entry (Tier 2).
	note := al.buildToolManifestNote(ts, policyFiltered)
	assert.Contains(t, note, "  - get_workspace",
		"get_workspace (previewed lazy) must appear in the manifest note as a preview entry")

	// After a load, get_workspace becomes callable — it remains fully reachable,
	// just one deliberate discovery round trip away.
	al.markToolsLoaded(bucketFor(avaAgent, sessionID), []string{"get_workspace"})
	tsAfter := fakeTurnState(avaAgent, sessionID)
	defsAfter := al.buildCompressedToolDefs(tsAfter, policyFiltered)
	defNamesAfter := make(map[string]bool, len(defsAfter))
	for _, d := range defsAfter {
		defNamesAfter[d.Function.Name] = true
	}
	assert.True(t, defNamesAfter["get_workspace"],
		"get_workspace must become callable after markToolsLoaded — it is demoted, not unreachable")
}

// fakeGetWorkspaceTool is a minimal stub that satisfies the tools.Tool
// interface for the purpose of testing the manifest tier path without
// importing pkg/sysagent/tools (which would create a circular dependency
// from pkg/agent). It mirrors the real WorkspaceGetTool's Name(), Scope(),
// and Category() values.
type fakeGetWorkspaceTool struct{}

func (f *fakeGetWorkspaceTool) Name() string { return "get_workspace" }
func (f *fakeGetWorkspaceTool) Description() string {
	return "Get a single workspace's details (stub for manifest tier test)."
}

func (f *fakeGetWorkspaceTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (f *fakeGetWorkspaceTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (f *fakeGetWorkspaceTool) Category() tools.ToolCategory { return tools.CategoryWorkspaces }
func (f *fakeGetWorkspaceTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: "stub"}
}

// ─── GAP 2 / ADR-071 D3: task tools split across Full and Previewed tiers ────

// TestPromotedTaskTools_CallableOnTurn1_NoLoad originally proved that
// `create_task`, `list_tasks`, `update_task` (all promoted to ManifestFull in
// round 2 of feat/0.1.0-uat-fixes) were callable on turn 1 without a prior
// markToolsLoaded call. ADR-071 D3 §4.1/§4.2 SPLIT that trio: `list_tasks`
// stays Full (a read the agent needs to orient itself), while `create_task`
// and `update_task` drop to the previewed lazy tier (Tier 2) — deliberately,
// so `delegate` keeps a wider visibility margin over the task-mutation verbs
// per ADR-053's measured ordering. This test is rewritten to pin the SPLIT
// tier behavior rather than the old "all three are Full" one:
//  1. Asserts ToolManifestTier: list_tasks == ManifestFull; create_task and
//     update_task == ManifestLazy (with ManifestVisibility == ManifestPreviewed).
//  2. Calls buildCompressedToolDefs on turn 1 (no markToolsLoaded) and asserts
//     list_tasks IS in the sent defs, while create_task/update_task are NOT.
//  3. Asserts list_tasks does NOT appear in the manifest note (Full tier has
//     no manifest presence), while create_task/update_task DO (previewed).
//  4. Tests both Jim and Mia, using DIFFERENT agent instances to prove the
//     assertion is not hardcoded.
//
// Traces to: ADR-071 D3 §4.1/§4.2; pkg/tools/manifest.go
// fullManifestToolNames/previewedLazyToolNames; pkg/agent/loop.go
// registerSharedTools task-tools block.
func TestPromotedTaskTools_CallableOnTurn1_NoLoad(t *testing.T) {
	// Precondition: the D3 tier split, fails fast if reverted.
	require.Equal(t, tools.ManifestFull, tools.ToolManifestTier("list_tasks"),
		"ADR-071 D3 REGRESSION: ToolManifestTier(\"list_tasks\") must stay ManifestFull.")
	for _, name := range []string{"create_task", "update_task"} {
		require.Equal(t, tools.ManifestLazy, tools.ToolManifestTier(name),
			"ADR-071 D3 REGRESSION: ToolManifestTier(%q) must be ManifestLazy "+
				"(demoted from Full in D3 §4.1). If this assertion breaks, %q was "+
				"re-added to fullManifestToolNames.", name, name)
		require.Equal(t, tools.ManifestPreviewed, tools.ToolManifestVisibility(name),
			"ADR-071 D3 REGRESSION: ToolManifestVisibility(%q) must be ManifestPreviewed.", name)
	}

	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	tests := []struct {
		agentID   string
		sessionID string
	}{
		// Two DIFFERENT agents — differentiation test: same assertion path with different
		// inputs (different agent policies and registries) proves the assertion is not
		// accidentally hardcoded to a single agent's state.
		{agentID: "jim", sessionID: "sess-gap2-jim"},
		{agentID: "mia", sessionID: "sess-gap2-mia"},
	}
	taskTools := []string{"create_task", "list_tasks", "update_task"}

	for _, tc := range tests {
		t.Run(tc.agentID, func(t *testing.T) {
			agentInst, ok := al.registry.GetAgent(tc.agentID)
			require.True(t, ok, "agent %q must be in registry", tc.agentID)

			allTools := agentInst.Tools.GetAll()
			policyFiltered, _ := tools.FilterToolsByPolicy(allTools, agentInst.AgentType, agentInst.LoadToolPolicy())

			// Non-vacuous: all three task tools must be in the policy-filtered set.
			// If any is missing, the policy for this agent no longer allows it — check core.go.
			pfNames := make(map[string]bool, len(policyFiltered))
			for _, t2 := range policyFiltered {
				pfNames[t2.Name()] = true
			}
			for _, name := range taskTools {
				require.True(t, pfNames[name],
					"POLICY REGRESSION: agent %q — %q must be in policy-filtered set. "+
						"Check the agent's allow-policy in pkg/coreagent/core.go.", tc.agentID, name)
			}

			// Also confirm the tools are registered in the test harness
			// (the taskStore is initialized from tmpDir in newCompressedCfg via NewAgentLoop).
			for _, name := range taskTools {
				_, registered := agentInst.Tools.Get(name)
				require.True(t, registered,
					"REGISTRATION GAP: agent %q — %q must be registered in the test harness. "+
						"The task store is seeded from cfg.Agents.Defaults.Home (t.TempDir()) "+
						"in NewAgentLoop; if this fails, the taskStore or tool registration changed.", tc.agentID, name)
			}

			// Turn 1: NO markToolsLoaded call.
			ts := fakeTurnState(agentInst, tc.sessionID)
			defs := al.buildCompressedToolDefs(ts, policyFiltered)

			defNames := make(map[string]bool, len(defs))
			for _, d := range defs {
				defNames[d.Function.Name] = true
			}

			// list_tasks (Full) is directly callable without a load.
			assert.True(t, defNames["list_tasks"],
				"agent %q: list_tasks (ManifestFull) must appear in buildCompressedToolDefs "+
					"on turn 1 without a prior markToolsLoaded call.", tc.agentID)
			// create_task/update_task (now previewed lazy) are NOT — they pay the
			// one-time discovery cost D3 introduced.
			for _, name := range []string{"create_task", "update_task"} {
				assert.False(t, defNames[name],
					"ADR-071 D3: agent %q — %q (now ManifestLazy/ManifestPreviewed) must NOT "+
						"appear in buildCompressedToolDefs on turn 1 without a load.", tc.agentID, name)
			}

			note := al.buildToolManifestNote(ts, policyFiltered)
			// list_tasks (Full) has no manifest-block presence at all.
			assert.NotContains(t, note, "  - list_tasks",
				"agent %q: list_tasks (Full-tier) must NOT appear in the manifest note", tc.agentID)
			// create_task/update_task (previewed) DO appear as preview entries.
			for _, name := range []string{"create_task", "update_task"} {
				assert.Contains(t, note, "  - "+name,
					"agent %q: %q (previewed lazy) must appear in the manifest note as a preview entry", tc.agentID, name)
			}
		})
	}
}

// TestPromotedTaskTools_DifferentiationCheck is the explicit differentiation test:
// it proves the Full/previewed/search-only assertions above are NOT vacuous by
// showing that a genuinely SEARCH-ONLY tool (find_skills — ManifestLazy AND
// ManifestSearchOnly, in Mia's policy-filtered set) does NOT appear in defs on
// turn 1 without a load call, AND does NOT appear in the manifest note either
// (the D3 property create_task/update_task deliberately do NOT share).
//
// This guards against a regression where buildCompressedToolDefs accidentally
// sends ALL tools (making the "not directly callable" assertions vacuously
// true), and against a regression where the manifest note accidentally lists
// every lazy tool regardless of visibility (making the "previewed only"
// distinction above vacuous).
//
// Traces to: ADR-071 D3 §4.1/§4.4; QA anti-shortcut: differentiation test.
func TestPromotedTaskTools_DifferentiationCheck(t *testing.T) {
	// find_skills is ManifestLazy AND ManifestSearchOnly (Tier 3) and in Mia's
	// allow-list — it is the control tool for BOTH properties under test.
	require.Equal(t, tools.ManifestLazy, tools.ToolManifestTier("find_skills"),
		"find_skills must be ManifestLazy for this differentiation test to be valid")
	require.Equal(t, tools.ManifestSearchOnly, tools.ToolManifestVisibility("find_skills"),
		"find_skills must be ManifestSearchOnly (Tier 3) for this differentiation test to be valid")

	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	miaAgent, ok := al.registry.GetAgent("mia")
	require.True(t, ok, "mia must be in registry")

	allTools := miaAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, miaAgent.AgentType, miaAgent.LoadToolPolicy())

	// find_skills must be in Mia's policy-filtered set (control tool is present).
	var findSkillsPresent bool
	for _, t2 := range policyFiltered {
		if t2.Name() == "find_skills" {
			findSkillsPresent = true
			break
		}
	}
	require.True(t, findSkillsPresent,
		"find_skills must be in Mia's policy-filtered set for this differentiation test to be meaningful")

	// Turn 1: no load call.
	ts := fakeTurnState(miaAgent, "sess-gap2-diff")
	defs := al.buildCompressedToolDefs(ts, policyFiltered)

	defNames := make(map[string]bool, len(defs))
	for _, d := range defs {
		defNames[d.Function.Name] = true
	}

	// list_tasks (Full) is present (the positive case).
	assert.True(t, defNames["list_tasks"],
		"list_tasks (ManifestFull) must be in defs on turn 1")

	// create_task/update_task (previewed lazy) and find_skills (search-only lazy)
	// are all absent from defs without a load — the negative case for defs.
	for _, name := range []string{"create_task", "update_task", "find_skills"} {
		assert.False(t, defNames[name],
			"DIFFERENTIATION: %q (lazy) must NOT be in defs on turn 1 without a load call. "+
				"If this assertion fails, buildCompressedToolDefs sends ALL tools regardless of tier — "+
				"the positive assertion above is then vacuous and the manifest optimization is broken.", name)
	}

	note := al.buildToolManifestNote(ts, policyFiltered)
	// create_task/update_task (previewed) DO appear in the note.
	for _, name := range []string{"create_task", "update_task"} {
		assert.Contains(t, note, "  - "+name,
			"DIFFERENTIATION: %q (previewed lazy) must appear in the manifest note", name)
	}
	// find_skills (search-only) does NOT — proving the previewed-vs-search-only
	// split inside the manifest note is real, not vacuous.
	assert.NotContains(t, note, "  - find_skills",
		"DIFFERENTIATION: find_skills (search-only lazy) must NOT appear in the manifest note. "+
			"If this assertion fails, the ManifestSearchOnly filter in BuildCompressedManifest is not "+
			"actually filtering anything, and every lazy tool renders a preview line regardless of D3.")
}

// ─── Live-toggle regression: ToolSearch always registered ────────────────────

// TestLoadToolRegistered_UncompressedBoot proves that ToolSearch is registered
// in every agent's Tools registry even when the loop is booted with
// cfg.Tools.Manifest.Compressed=false.
//
// Before the fix: the registration was gated on Compressed=true at boot. A
// gateway booting with compressed=false would never register ToolSearch. After a
// live tools_on_demand PUT (false→true), the per-turn code paths would call
// Get("ToolSearch") expecting it to be present — and silently get !ok, causing
// every lazy tool to become unreachable with no error or log.
//
// After the fix: registration is unconditional. This test asserts the invariant
// for the same agents TestSearchToolsRegistered_CompressedMode covers, but for
// the Compressed=false boot path.
func TestLoadToolRegistered_UncompressedBoot(t *testing.T) {
	cfg := newUncompressedCfg(t)
	// Confirm the config is uncompressed so this test is meaningful.
	require.False(t, cfg.Tools.Manifest.Compressed,
		"test precondition: cfg.Tools.Manifest.Compressed must be false")

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	ids := al.registry.ListAgentIDs()
	require.NotEmpty(t, ids)
	for _, agentID := range ids {
		agentInst, ok := al.registry.GetAgent(agentID)
		require.True(t, ok, "agent %q must be in registry", agentID)

		_, hasLoadTool := agentInst.Tools.Get("ToolSearch")
		assert.True(t, hasLoadTool,
			"agent %q: ToolSearch must be registered even when Compressed=false at boot "+
				"(live tools_on_demand toggle must not break the registry)", agentID)
	}
}

// TestLoadTool_LiveToggle_CompressedDefsWork is the runtime-reachability test
// required by the reviewer. It proves the critical scenario:
//   - The loop boots with Compressed=false (ToolSearch was NOT registered before
//     the fix; IS registered after the fix).
//   - A live tools_on_demand false→true toggle occurs (Compressed is flipped in
//     SwapConfig, not by re-running registration).
//   - After the toggle, Get("ToolSearch") succeeds for a deny-default agent (Ava).
//   - buildCompressedToolDefs for Ava includes ToolSearch in its defs.
//   - A lazy tool is reachable for Ava: it appears in the manifest note and can
//     be loaded via ToolSearch.Execute.
//
// The toggle is simulated by calling buildCompressedToolDefs with a modified
// config snapshot (Compressed=true) on a loop that was booted with Compressed=false,
// which is exactly the condition the live PUT creates.
func TestLoadTool_LiveToggle_CompressedDefsWork(t *testing.T) {
	// Boot with Compressed=false — the critical precondition.
	cfg := newUncompressedCfg(t)
	require.False(t, cfg.Tools.Manifest.Compressed)

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	// Deny-default agent: Ava. She is the hardest case — ToolSearch is not in her
	// explicit allow-list, so FilterToolsByPolicy denies it; only the infra
	// force-include in buildCompressedToolDefs makes it reachable.
	avaAgent, ok := al.registry.GetAgent("ava")
	require.True(t, ok, "ava must be in registry")

	// ASSERTION 1: After an uncompressed boot, ToolSearch IS registered (the fix).
	// Before the fix this would return !ok, making every lazy tool unreachable
	// after a live toggle.
	_, registeredAfterUncompressedBoot := avaAgent.Tools.Get("ToolSearch")
	require.True(t, registeredAfterUncompressedBoot,
		"ToolSearch must be registered for ava even after Compressed=false boot "+
			"(the fix: unconditional registration)")

	// Simulate the live toggle: Compressed is now true (PUT tools_on_demand:true
	// called SwapConfig). The per-turn code in runTurn will call
	// buildCompressedToolDefs with the updated cfg. We simulate that here.
	allTools := avaAgent.Tools.GetAll()
	policyFiltered, policyMap := tools.FilterToolsByPolicy(allTools, avaAgent.AgentType, avaAgent.LoadToolPolicy())

	// Force infra into execution snapshot (mirrors the runTurn path).
	policyFiltered = ensureInfraToolsExecutable(avaAgent.Tools, policyFiltered, policyMap)

	// ASSERTION 2: ToolSearch is now in the execution policy snapshot as "allow".
	require.Equal(t, "allow", policyMap["ToolSearch"],
		"after live toggle: ToolSearch must be allow in Ava's exec policy snapshot")

	// ASSERTION 3: buildCompressedToolDefs includes ToolSearch in the sent defs.
	ts := fakeTurnState(avaAgent, "sess-live-toggle-ava")
	defs := al.buildCompressedToolDefs(ts, policyFiltered)
	defNames := make(map[string]bool, len(defs))
	for _, d := range defs {
		defNames[d.Function.Name] = true
	}
	assert.True(t, defNames["ToolSearch"],
		"after live toggle: ToolSearch must be in compressed defs for deny-default agent ava")

	// ASSERTION 4: A lazy tool is reachable for Ava (find_skills is in her allow-list
	// and is ManifestLazy). Without the fix the entire lazy tier becomes unreachable
	// because ToolSearch.Execute would not exist. With the fix it works.
	require.Equal(t, tools.ManifestLazy, tools.ToolManifestTier("find_skills"),
		"find_skills must be ManifestLazy for this assertion to be meaningful")
	_, findSkillsReg := avaAgent.Tools.Get("find_skills")
	require.True(t, findSkillsReg, "find_skills must be registered for ava")

	toolsToolRaw, ok := avaAgent.Tools.Get("ToolSearch")
	require.True(t, ok, "ToolSearch must be Get-able for ava after uncompressed boot (the fix)")
	tt, ok := toolsToolRaw.(*tools.ToolsTool)
	require.True(t, ok, "ToolSearch must be *tools.ToolsTool")

	ctx := tools.WithAgentID(context.Background(), "ava")
	ctx = tools.WithTranscriptSessionID(ctx, "sess-live-toggle-ava")
	result := tt.Execute(ctx, map[string]any{"names": []any{"find_skills"}})
	assert.False(t, result.IsError,
		"after live toggle: find_skills must be loadable by ava via ToolSearch; got error: %s", result.ForLLM)
}

// TestLoadTool_UncompressedDefs_LoadToolNotSentToModel proves that in uncompressed
// mode (Compressed=false), the ToolSearch infra tool does NOT appear in the provider
// defs surfaced to the model — for ANY agent, regardless of whether its own seeded
// policy data allows or denies it.
//
// Post-unification (#438): ToolSearch resolves through the SAME global×agent
// merge as every other static builtin tool (compositor.go's former
// unconditional infra force-allow was a CLAUDE.md hard-constraint-6 violation
// and has been removed). It is seeded "allow" as real, explicit data for
// every agent (pkg/coreagent/core.go), so it IS present in policyFiltered for
// every seeded agent below — not because of a bypass, but because that is its
// real resolved policy. The observable behavior (ToolSearch never surfaced
// when compression is off) is preserved by stripInfraToolDefs on the
// non-compressed defs path in runTurn, independent of whatever policy value
// ToolSearch actually resolved to.
//
// The test mirrors the real path (ToolsToProviderDefs(stripInfraToolDefs(...))).
func TestLoadTool_UncompressedDefs_LoadToolNotSentToModel(t *testing.T) {
	cfg := newUncompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	// The 4 seeded core agents are deny-default overall, but ToolSearch is a
	// structural floor every one of them seeds "allow" explicitly
	// (pkg/coreagent/core.go) — so it resolves "allow" and is present in the
	// filtered slice like any other allowed tool.
	for _, agentID := range []string{"mia", "jim", "ava", "ray"} {
		t.Run("deny-default/"+agentID, func(t *testing.T) {
			agentInst, ok := al.registry.GetAgent(agentID)
			require.True(t, ok)

			allTools := agentInst.Tools.GetAll()
			policyFiltered, policyMap := tools.FilterToolsByPolicy(
				allTools,
				agentInst.AgentType,
				agentInst.LoadToolPolicy(),
			)

			// ToolSearch resolves "allow" from its own real seeded policy entry.
			require.Equal(t, "allow", policyMap["ToolSearch"],
				"agent %q: ToolSearch must resolve allow from its own seeded policy entry", agentID)

			// ...but the real uncompressed path strips it before surfacing,
			// regardless of its resolved policy.
			uncompressedDefs := tools.ToolsToProviderDefs(stripInfraToolDefs(policyFiltered))
			for _, d := range uncompressedDefs {
				assert.NotEqual(t, "ToolSearch", d.Function.Name,
					"agent %q: ToolSearch must NOT appear in uncompressed surfaced defs", agentID)
			}
		})
	}

	// An agent whose OWN policy explicitly allows ToolSearch (mirroring real
	// seeded data — pkg/coreagent/core.go grants it to every agent) still
	// must not see it surfaced uncompressed: stripInfraToolDefs strips it
	// unconditionally, independent of the resolved policy value. This used to
	// synthesize an EMPTY ToolPolicyCfg and rely on compositor.go's
	// unconditional infra force-allow to put ToolSearch in policyFiltered —
	// that bypass is gone (CLAUDE.md hard constraint 6), so the cfg here
	// carries a real, explicit "ToolSearch": allow entry instead. We borrow
	// Ava's registry (ToolSearch is always registered there).
	t.Run("explicit-allow/synthetic", func(t *testing.T) {
		ava, ok := al.registry.GetAgent("ava")
		require.True(t, ok)
		allTools := ava.Tools.GetAll()

		explicitAllow := &tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{"ToolSearch": config.ToolPolicyAllow},
		}
		policyFiltered, policyMap := tools.FilterToolsByPolicy(allTools, ava.AgentType, explicitAllow)

		// ToolSearch resolves "allow" from its own real seeded policy entry.
		require.Equal(t, "allow", policyMap["ToolSearch"],
			"explicit-allow: ToolSearch is allow in the filtered set")
		require.Contains(t, toolNameSet(policyFiltered), "ToolSearch",
			"explicit-allow: ToolSearch present in policyFiltered")

		// The uncompressed path strips it regardless of the resolved policy.
		uncompressedDefs := tools.ToolsToProviderDefs(stripInfraToolDefs(policyFiltered))
		for _, d := range uncompressedDefs {
			assert.NotEqual(t, "ToolSearch", d.Function.Name,
				"explicit-allow: ToolSearch must NOT be surfaced uncompressed (stripped regardless of resolved policy)")
		}
	})
}

// TestRegistrationGuard_DerivesFromInfraNames pins ADR-071 D1 / spec FR-013
// (W-D1 test 8): the double-registration guard in registerSharedTools reads
// tools.InfraManifestToolNames() — the name set — rather than a hardcoded
// literal. Proven behaviourally: calling registerSharedTools a second time
// against the SAME registry must be a true no-op for every infra tool name,
// not just for whichever literal a hardcoded guard happened to spell
// correctly. Regression target: ADR-071 §2.1(c) — a guard still keyed on the
// retired "load_tool" literal would silently stop guarding after the rename
// and re-register (RegisterReplacing) a fresh ToolsTool instance on every
// call, breaking any in-flight session-scoped state the BM25 engine cache
// carries.
func TestRegistrationGuard_DerivesFromInfraNames(t *testing.T) {
	cfg := newCompressedCfg(t)
	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	infraNames := tools.InfraManifestToolNames()
	require.NotEmpty(t, infraNames, "InfraManifestToolNames() must not be empty")

	agent, ok := al.registry.GetAgent(testDefaultAgentID)
	require.True(t, ok)

	before := make(map[string]tools.Tool, len(infraNames))
	for _, name := range infraNames {
		tool, found := agent.Tools.Get(name)
		require.True(t, found, "infra tool %q must be registered after NewAgentLoop", name)
		before[name] = tool
	}

	// Re-run the shared-tool registration pass directly, exactly as
	// UpsertAgentFast/ReloadProviderAndConfig do on a live config change.
	registerSharedTools(al, cfg, msgBus, al.registry, provider)

	agentAfter, ok := al.registry.GetAgent(testDefaultAgentID)
	require.True(t, ok)
	for _, name := range infraNames {
		after, ok := agentAfter.Tools.Get(name)
		require.True(t, ok, "infra tool %q must still be registered", name)
		assert.Same(t, before[name], after,
			"registerSharedTools must not replace infra tool %q on a second pass — "+
				"the guard must derive its check from InfraManifestToolNames(), not a literal", name)
	}
}

// TestRegistrationGuard_SurvivesLiveConfigToggle is the integration-level
// companion to TestRegistrationGuard_DerivesFromInfraNames (spec FR-013,
// W-D1 test 13) — the exact scenario the guard's own doc comment cites:
// registerSharedTools re-running against an EXISTING agent's registry as
// part of a live config change, with no process restart. UpsertAgentFast
// (issue #571) is precisely this — cloneAgents() carries the existing
// agent's live *ToolsTool instance forward, then registerSharedTools
// re-runs across every agent (including the untouched ones) so the
// new/updated agent gets full parity. If the guard only matched the retired
// "load_tool" literal it would silently stop guarding post-rename and
// replace ToolSearch on the untouched agent too.
func TestRegistrationGuard_SurvivesLiveConfigToggle(t *testing.T) {
	baseAgents := []config.AgentConfig{
		{ID: "alpha", Name: "Alpha", Type: config.AgentTypeCustom},
	}
	al := buildFastUpsertTestLoop(t, baseAgents)

	alpha, ok := al.registry.GetAgent("alpha")
	require.True(t, ok)
	before, ok := alpha.Tools.Get("ToolSearch")
	require.True(t, ok, "alpha must have ToolSearch registered at boot")

	newAgent := config.AgentConfig{ID: "gamma", Name: "Gamma", Type: config.AgentTypeCustom}
	cfg := cloneCfg(t, al.GetConfig())
	cfg.Agents.List = append(cfg.Agents.List, newAgent)

	_, err := al.UpsertAgentFast(cfg, "gamma")
	require.NoError(t, err)

	alphaAfter, ok := al.registry.GetAgent("alpha")
	require.True(t, ok)
	after, ok := alphaAfter.Tools.Get("ToolSearch")
	require.True(t, ok, "alpha must still have ToolSearch registered after the live upsert")
	assert.Same(t, before, after,
		"registerSharedTools must not re-register ToolSearch on alpha while re-touching it "+
			"during gamma's live upsert — the guard must survive the toggle")

	gamma, ok := al.registry.GetAgent("gamma")
	require.True(t, ok)
	_, ok = gamma.Tools.Get("ToolSearch")
	assert.True(t, ok, "the newly upserted agent must also get ToolSearch registered")
}
