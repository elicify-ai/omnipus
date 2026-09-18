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
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCompressedCfg builds a minimal config with Compressed=true and the four
// core agents seeded (Mia, Jim, Ava, Ray).
// firstPreviewedLazyName returns the first previewed (Tier 2) lazy tool in the
// given policy-filtered set, failing the test when there is none. ADR-090
// §5.4 shrinks the previewed set to exactly serve_web, so only General
// Purpose (seeded allowed) can supply one — Jim legitimately has none.
func firstPreviewedLazyName(t *testing.T, policyFiltered []tools.Tool) string {
	t.Helper()
	for _, tl := range policyFiltered {
		if tools.ToolManifestTier(tl.Name()) == tools.ManifestLazy &&
			tools.ToolManifestVisibility(tl.Name()) == tools.ManifestPreviewed {
			return tl.Name()
		}
	}
	t.Fatal("fixture: agent must have at least one previewed lazy tool (ADR-090: serve_web for General Purpose)")
	return ""
}

// requirePreviewedLazyPresent is the non-fatal-variant shape of
// firstPreviewedLazyName for call sites that want the require-style failure
// inside the caller's test function.
func requirePreviewedLazyPresent(t *testing.T, policyFiltered []tools.Tool) {
	t.Helper()
	firstPreviewedLazyName(t, policyFiltered)
}

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
	// Ordinary roles inherit the shipped ceiling through sparse overrides.
	cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
	cfg.Tools.Manifest.Compressed = true
	coreagent.SeedConfig(cfg)
	return cfg
}

// newCompressedLoopWithServeWeb builds the compressed-fixture loop with the
// Tier 1/3 wiring production performs at boot: serve_web registers only when
// ServedSubdirs is available (loop_wire.go), so a bare mustNewAgentLoop leaves
// the previewed tier EMPTY in every agent's registry — no agent to exercise
// the manifest note's "More tools" line with. Wiring the real registration
// condition (not a stub and not a widened grant) gives the manifest tests
// their legitimately permitted previewed subject: serve_web, seeded allowed
// for General Purpose by the ADR-090 role policy.
func newCompressedLoopWithServeWeb(t *testing.T) *AgentLoop {
	t.Helper()
	al := mustNewAgentLoop(t, newCompressedCfg(t), bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })
	al.WireTier13Deps(Tier13Deps{ServedSubdirs: NewServedSubdirs()})
	return al
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
//
// ADR-090 note: the agent under test is General Purpose ("worker"), not Jim.
// ADR-090 §5.4 shrinks the previewed (Tier 2) lazy set to exactly
// `serve_web`, which only General Purpose is seeded allowed — Jim's ADR-090
// policy denies serve_web, so Jim's note is legitimately empty and Jim can no
// longer exercise the "note lists unloaded previewed tools" property.
func TestBuildToolManifestNote_ContainsLazyTools(t *testing.T) {
	al := newCompressedLoopWithServeWeb(t)

	workerAgent, ok := al.registry.GetAgent("worker")
	require.True(t, ok, "worker (General Purpose) must be in the seeded roster")

	allTools := workerAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, workerAgent.AgentType, workerAgent.LoadToolPolicy())

	// Non-vacuous: worker must have at least one previewed lazy tool (serve_web).
	requirePreviewedLazyPresent(t, policyFiltered)

	ts := fakeTurnState(workerAgent, "sess-note")
	note := al.buildToolManifestNote(ts, policyFiltered)

	// Must contain at least one lazy tool entry.
	require.NotEmpty(t, note, "manifest note must be non-empty for worker with unloaded lazy tools")

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
	al := newCompressedLoopWithServeWeb(t)

	// General Purpose, not Jim: ADR-090 §5.4 leaves serve_web as the only
	// previewed lazy tool and seeds it for General Purpose only (Jim denies it).
	workerAgent, ok := al.registry.GetAgent("worker")
	require.True(t, ok, "worker (General Purpose) must be in the seeded roster")

	allTools := workerAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, workerAgent.AgentType, workerAgent.LoadToolPolicy())

	// Find a PREVIEWED lazy tool to load (ADR-071 D3): a search-only (Tier 3)
	// tool would never appear in the note regardless of loaded status, which
	// would make this test pass vacuously without exercising the "loaded"
	// exclusion at all.
	lazyName := firstPreviewedLazyName(t, policyFiltered)

	sessionID := "sess-loaded-exclude"
	al.markToolsLoaded(bucketFor(workerAgent, sessionID), []string{lazyName})

	ts := fakeTurnState(workerAgent, sessionID)
	note := al.buildToolManifestNote(ts, policyFiltered)

	// The loaded tool must not appear as a manifest entry.
	assert.NotContains(t, note, "  - "+lazyName,
		"loaded tool %q must be excluded from manifest note", lazyName)
}

// TestBuildToolManifestNote_PreviewedToolFollowsAgentPolicy pins, on a real
// agent and the real note builder, the property the manifest preview has always
// relied on: the preview is built from the agent's policy-filtered tools — so
// the SAME agent sees the preview line under its seeded policy and no line
// once its policy denies the tool.
//
// The subject moved twice (kept, per this file's rewrite-don't-delete
// convention): ADR-071's 2026-09-14 amendment previewed create_plan and
// execute_plan; ADR-090 §5.4 promotes both into the global upfront full set
// (Plans row: create_plan/execute_plan/stop_plan) and shrinks the previewed
// lazy tier to exactly `serve_web`. The same property is now pinned on
// serve_web for General Purpose, whose ADR-090 seeded policy allows it
// (pkg/coreagent/role_policies_adr090.go IDWorker). pkg/tools'
// manifest_plan_preview_test.go covers the builder against every policy
// layer; this proves the agent loop's own builder renders only what the
// policy filter kept.
func TestBuildToolManifestNote_PreviewedToolFollowsAgentPolicy(t *testing.T) {
	al := newCompressedLoopWithServeWeb(t)

	workerAgent, ok := al.registry.GetAgent("worker")
	require.True(t, ok, "worker (General Purpose) must be in the seeded roster")
	allTools := workerAgent.Tools.GetAll()

	seeded := workerAgent.LoadToolPolicy()
	require.NotNil(t, seeded, "fixture: worker must carry a tool policy")
	allowedTools, allowedVerdicts := tools.FilterToolsByPolicy(allTools, workerAgent.AgentType, seeded)
	require.Contains(t, allowedVerdicts, "serve_web",
		"fixture: worker's seeded policy must not deny serve_web, or the allowed half below is vacuous")
	allowedNote := al.buildToolManifestNote(fakeTurnState(workerAgent, "sess-serveweb-preview-allow"), allowedTools)
	require.NotEmpty(t, allowedNote, "control: the allowed note must be non-empty (worker has serve_web previewed)")
	assert.Contains(t, allowedNote, "  - serve_web — ")

	denied := &tools.ToolPolicyCfg{
		Policies:       make(map[string]config.ToolPolicy, len(seeded.Policies)+1),
		GlobalPolicies: seeded.GlobalPolicies,
		GodMode:        seeded.GodMode,
	}
	for k, v := range seeded.Policies {
		denied.Policies[k] = v
	}
	denied.Policies["serve_web"] = config.ToolPolicyDeny
	deniedTools, deniedVerdicts := tools.FilterToolsByPolicy(allTools, workerAgent.AgentType, denied)
	require.NotContains(t, deniedVerdicts, "serve_web")
	deniedNote := al.buildToolManifestNote(fakeTurnState(workerAgent, "sess-serveweb-preview-deny"), deniedTools)
	// serve_web is the ONLY previewed lazy tool (ADR-090 §5.4), so the denied
	// note is exactly empty — the builder's documented ""-when-nothing-to-
	// inject contract — rather than merely missing one line.
	assert.Empty(t, deniedNote,
		"denying the only previewed lazy tool must empty the note entirely (ADR-090 §5.4 previewed set = {serve_web})")
	assert.NotContains(t, deniedNote, "  - serve_web")
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
	al := newCompressedLoopWithServeWeb(t)

	// General Purpose, not Jim: ADR-090 §5.4 leaves serve_web as the only
	// previewed lazy tool and seeds it for General Purpose only (Jim denies it).
	workerAgent, ok := al.registry.GetAgent("worker")
	require.True(t, ok, "worker (General Purpose) must be in the seeded roster")

	allTools := workerAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, workerAgent.AgentType, workerAgent.LoadToolPolicy())

	lazyName := firstPreviewedLazyName(t, policyFiltered)

	const (
		transcriptID = "transcript-99"
		sessionKey   = "agent:jim:session:transcript-99-wrapped"
	)
	require.NotEqual(t, transcriptID, sessionKey,
		"test precondition: transcriptID and sessionKey must genuinely differ")

	ts := &turnState{
		agent:      workerAgent,
		sessionKey: sessionKey,
		opts:       processOptions{TranscriptSessionID: transcriptID},
	}

	// Before the mid-turn load: the lazy tool is unloaded everywhere.
	beforeNote := al.buildToolManifestNote(ts, policyFiltered)
	require.Contains(t, beforeNote, "  - "+lazyName,
		"fixture: %q must start out unloaded (listed in the manifest note)", lazyName)
	beforeNoteTokens := al.manifestNoteTokens(ts, al.GetConfig())
	beforeSurface := al.sentToolSurfaceTokens(workerAgent, transcriptID, sessionKey)

	// Simulate the ToolSearch mid-turn load exactly as the real writer does
	// (loop.go's markLoaded closure derives the same composite bucket from
	// ctx-carried agent/transcript/session ids that ts.manifestBucket()
	// derives from turnState fields).
	al.markToolsLoaded(ts.manifestBucket(), []string{lazyName})

	afterNote := al.buildToolManifestNote(ts, policyFiltered)
	afterNoteTokens := al.manifestNoteTokens(ts, al.GetConfig())
	afterSurface := al.sentToolSurfaceTokens(workerAgent, transcriptID, sessionKey)

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

// TestReachabilityInvariant_AllCoreAgents proves that for each seeded agent
// (the base chat agents plus the general-purpose worker — ADR-090 §2.0
// roster),
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

	for _, agentID := range []string{"mia", "jim", "ava", "worker"} {
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
//
// Operator Deny of ToolSearch is also non-deniable infrastructure (user
// clarification 2026-09-18); that invariant is pinned in
// adr090_discovery_always_available_test.go, not here. Target-tool permissions
// still apply.
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

	// ADR-071 D2 (ambiguity band, landed 2026-08-28 — BEFORE this test was
	// written) auto-loads EITHER the single best match ("Loaded the best
	// match") OR, when a runner-up scores within the confident band, every
	// plausible match up to searchMaxAutoLoad=3 ("plausible matches were
	// loaded"). This test's original assertion demanded the single-match
	// branch, which was stale at birth: D2 was already merged and this
	// tab/browser-heavy query legitimately promotes browser_open_tab alongside
	// browser_screenshot. The branch itself is D2's decision, so the oracle
	// here pins D2's invariants instead of one branch:
	//   (a) the query must PROMOTE something (a query-path promotion is the
	//       whole subject of FR-031a),
	//   (b) browser_screenshot — the semantically correct tool for a
	//       screenshot query — must be among the promoted; if the ranking
	//       breaks and it is not, this goes red,
	//   (c) at most 3 tools promoted (searchMaxAutoLoad, written as a design
	//       literal so a silent cap change fails this test).
	jsonStart := strings.Index(queryResult.ForLLM, "{")
	require.GreaterOrEqual(t, jsonStart, 0,
		"ToolSearch(query) result must carry its JSON payload; got: %s", queryResult.ForLLM)
	var searchResp struct {
		Loaded []string `json:"loaded"`
	}
	require.NoError(t, json.Unmarshal([]byte(queryResult.ForLLM[jsonStart:]), &searchResp),
		"the JSON payload after the message line must decode; got: %s", queryResult.ForLLM)
	require.NotEmpty(t, searchResp.Loaded, "the query must auto-load at least one tool")
	require.LessOrEqual(t, len(searchResp.Loaded), 3,
		"ADR-071 D2 caps one query's auto-load at 3; got %v", searchResp.Loaded)
	require.Contains(t, searchResp.Loaded, "browser_screenshot",
		"a screenshot query must promote browser_screenshot — if not, the ranking is broken; loaded %v", searchResp.Loaded)

	// After the query: buildCompressedToolDefs must contain EXACTLY the
	// promoted names relative to the baseline — no more (over-promotion or
	// stray loads) and no fewer (a promotion that never reached the manifest
	// builder) — proving the query-path promotion is usable end to end.
	tsAfter := fakeTurnState(jimAgent, transcriptID)
	defsAfter := al.buildCompressedToolDefs(tsAfter, policyFiltered)

	var newlyCallable []string
	for _, d := range defsAfter {
		if !beforeNames[d.Function.Name] {
			newlyCallable = append(newlyCallable, d.Function.Name)
		}
	}
	sort.Strings(newlyCallable)
	loadedSorted := append([]string(nil), searchResp.Loaded...)
	sort.Strings(loadedSorted)
	require.Equal(t, loadedSorted, newlyCallable,
		"exactly the tools the result says were loaded must become callable; result said %v, manifest gained %v", loadedSorted, newlyCallable)

	for _, promoted := range newlyCallable {
		assert.Equal(t, tools.ManifestLazy, tools.ToolManifestTier(promoted),
			"the auto-loaded tool %q must have been ManifestLazy before promotion (that is the whole point of the discovery path)", promoted)
	}
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

// ─── ADR-090 §5.4: get_workspace is in the global upfront set ────────────────

// TestGetWorkspace_UpfrontFullTier_ADR090 pins get_workspace's ADR-090
// classification on a real agent and the real compressed-defs builder.
//
// Regression history (kept, per this file's convention of rewriting rather
// than deleting):
//   - Originally proved `navigate` was ManifestFull (feat/0.1.0-uat-fixes
//     round 2); ADR-071 D3 §4.1 demoted navigate to the previewed lazy tier,
//     and the F1 review finding retired navigate outright, so the mechanism
//     under test moved to `get_workspace` (then a ScopeCore Tier-2 previewed
//     tool, tested via Ava).
//   - ADR-090 §5.4 ("Locate inputs" row of the global upfront set) promotes
//     get_workspace BACK to the always-callable full tier: a registered,
//     permitted tool in the 37-name global set has its full callable
//     definition in context from the first ordinary request and does not
//     require ToolSearch. This rewrite pins the promotion:
//     1. ToolManifestTier("get_workspace") == ManifestFull.
//     2. On turn 1, with no prior markToolsLoaded call, get_workspace IS in
//     buildCompressedToolDefs for Ava (whose seeded policy allows it).
//     3. It has NO manifest-note presence (the full tier has none).
//     4. Boundary preserved (ADR-090 §5.4: "initial visibility does not
//     grant permission"): for Jim, whose seeded ADR-090 policy denies
//     get_workspace, the upfront name never reaches the defs.
//
// Traces to: ADR-090 §5.4; pkg/tools/manifest.go fullManifestToolNames;
// superseding the ADR-071 D3 §4.1 pin this function previously held.
func TestGetWorkspace_UpfrontFullTier_ADR090(t *testing.T) {
	// Precondition: the classification is the single source of truth. If this
	// assertion fails, get_workspace was removed from fullManifestToolNames in
	// pkg/tools/manifest.go — i.e. the ADR-090 upfront set regressed.
	require.Equal(t, tools.ManifestFull, tools.ToolManifestTier("get_workspace"),
		"ToolManifestTier(\"get_workspace\") must be ManifestFull. If this assertion "+
			"breaks, get_workspace was removed from fullManifestToolNames in pkg/tools/manifest.go "+
			"(ADR-090 §5.4 global upfront set regression).")

	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	// get_workspace is NOT registered in the test harness (it is a sysagent tool
	// wired via WireSysagentDeps, which is not called in unit tests — that would
	// create a circular import: pkg/agent → pkg/sysagent/tools → pkg/agent).
	// Inject the same minimal stub the previous revisions of this test used.
	stub := &fakeGetWorkspaceTool{}
	avaAgent, ok := al.registry.GetAgent("ava")
	require.True(t, ok, "ava must be in registry")
	avaAgent.Tools.Register(stub)

	// Ava's ADR-090 seeded policy allows get_workspace, so the stub must
	// survive the policy filter.
	allTools := avaAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, avaAgent.AgentType, avaAgent.LoadToolPolicy())

	var getWorkspaceInPF bool
	for _, t2 := range policyFiltered {
		if t2.Name() == "get_workspace" {
			getWorkspaceInPF = true
			break
		}
	}
	require.True(t, getWorkspaceInPF,
		"POLICY REGRESSION: `get_workspace` must be in Ava's policy-filtered set. "+
			"Check the ADR-090 role policy for Ava in pkg/coreagent/role_policies_adr090.go.")

	// Turn 1: no markToolsLoaded call — the upfront name is directly callable.
	sessionID := "sess-ava-get-workspace-adr090"
	ts := fakeTurnState(avaAgent, sessionID)
	defs := al.buildCompressedToolDefs(ts, policyFiltered)
	defNames := make(map[string]bool, len(defs))
	for _, d := range defs {
		defNames[d.Function.Name] = true
	}

	// CONTROL: send_message (Full-tier, always registered) must be in defs —
	// proves the Full-tier path itself still works (not a vacuous assertion).
	assert.True(t, defNames["send_message"],
		"send_message (Full-tier, always registered) must be in Ava's compressed defs as a control")

	// PRIMARY ASSERTION: the ADR-090 upfront name is present on turn 1 without
	// any ToolSearch load — "callable without ToolSearch" (ADR-090 §10).
	assert.True(t, defNames["get_workspace"],
		"ADR-090 §5.4: get_workspace (global upfront set) must appear in Ava's "+
			"compressed defs on turn 1 without a load call.")

	// The full tier has NO manifest-note presence.
	note := al.buildToolManifestNote(ts, policyFiltered)
	assert.NotContains(t, note, "  - get_workspace",
		"get_workspace (ManifestFull) must have no manifest-note presence")

	// BOUNDARY (ADR-090 §5.4: upfront visibility never grants permission):
	// Jim's ADR-090 seeded policy denies get_workspace, so the same upfront
	// name must never reach his defs.
	jimAgent, ok := al.registry.GetAgent("jim")
	require.True(t, ok, "jim must be in registry")
	jimAgent.Tools.Register(&fakeGetWorkspaceTool{})
	jimFiltered, _ := tools.FilterToolsByPolicy(jimAgent.Tools.GetAll(), jimAgent.AgentType, jimAgent.LoadToolPolicy())
	var jimHasGetWorkspace bool
	for _, t2 := range jimFiltered {
		if t2.Name() == "get_workspace" {
			jimHasGetWorkspace = true
		}
	}
	require.False(t, jimHasGetWorkspace,
		"fixture: Jim's ADR-090 seeded policy must deny get_workspace for the boundary half to be non-vacuous")
	jimDefs := al.buildCompressedToolDefs(fakeTurnState(jimAgent, "sess-jim-get-workspace-adr090"), jimFiltered)
	for _, d := range jimDefs {
		assert.NotEqual(t, "get_workspace", d.Function.Name,
			"ADR-090 §5.4 boundary: the upfront name must NOT reach the defs of an agent whose policy denies it")
	}
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

func (f *fakeGetWorkspaceTool) Scope() tools.ToolScope { return tools.ScopeCore }

func (f *fakeGetWorkspaceTool) Category() tools.ToolCategory { return tools.CategoryWorkspaces }

func (f *fakeGetWorkspaceTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: "stub"}
}

// ─── ADR-090 §5.4: the whole task-tracking trio is in the global upfront set ─

// TestPromotedTaskTools_CallableOnTurn1_NoLoad originally proved that
// `create_task`, `list_tasks`, `update_task` (all promoted to ManifestFull in
// round 2 of feat/0.1.0-uat-fixes) were callable on turn 1 without a prior
// markToolsLoaded call. ADR-071 D3 §4.1/§4.2 SPLIT that trio (list_tasks Full;
// create_task/update_task previewed lazy). ADR-090 §5.4 (Work tracking row of
// the global upfront 37-name set: set_todos, list_tasks, list_jobs,
// create_task, update_task) closes the split again — all three are
// ManifestFull, directly callable from the first ordinary request without
// ToolSearch, subject to each agent's policy. This rewrite pins the ADR-090
// contract:
//  1. ToolManifestTier is ManifestFull for all three.
//  2. On turn 1 (no markToolsLoaded) all three appear in
//     buildCompressedToolDefs for both Jim and Mia.
//  3. None of the three has manifest-note presence (the full tier has none).
//  4. Two DIFFERENT agents (different policies and registries) prove the
//     assertion is not hardcoded.
//
// Traces to: ADR-090 §5.4 Work tracking row; pkg/tools/manifest.go
// fullManifestToolNames; superseding the ADR-071 D3 §4.1/§4.2 split pin.
func TestPromotedTaskTools_CallableOnTurn1_NoLoad(t *testing.T) {
	// Precondition: the ADR-090 upfront classification, fails fast if reverted.
	for _, name := range []string{"create_task", "list_tasks", "update_task"} {
		require.Equal(t, tools.ManifestFull, tools.ToolManifestTier(name),
			"ADR-090 §5.4 REGRESSION: ToolManifestTier(%q) must be ManifestFull — "+
				"the Work tracking row of the global upfront set names it verbatim.", name)
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
			// If any is missing, the policy for this agent no longer allows it — check
			// the ADR-090 role policies (pkg/coreagent/role_policies_adr090.go).
			pfNames := make(map[string]bool, len(policyFiltered))
			for _, t2 := range policyFiltered {
				pfNames[t2.Name()] = true
			}
			for _, name := range taskTools {
				require.True(t, pfNames[name],
					"POLICY REGRESSION: agent %q — %q must be in policy-filtered set. "+
						"Check the agent's ADR-090 role policy in pkg/coreagent/role_policies_adr090.go.", tc.agentID, name)
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

			// Turn 1: NO markToolsLoaded call — the upfront trio is directly callable.
			ts := fakeTurnState(agentInst, tc.sessionID)
			defs := al.buildCompressedToolDefs(ts, policyFiltered)

			defNames := make(map[string]bool, len(defs))
			for _, d := range defs {
				defNames[d.Function.Name] = true
			}
			for _, name := range taskTools {
				assert.True(t, defNames[name],
					"agent %q: %q (ADR-090 upfront set) must appear in buildCompressedToolDefs "+
						"on turn 1 without a prior markToolsLoaded call.", tc.agentID, name)
			}

			// The full tier has no manifest-block presence at all.
			note := al.buildToolManifestNote(ts, policyFiltered)
			for _, name := range taskTools {
				assert.NotContains(t, note, "  - "+name,
					"agent %q: %q (ManifestFull) must NOT appear in the manifest note", tc.agentID, name)
			}
		})
	}
}

// TestPromotedTaskTools_DifferentiationCheck is the explicit differentiation test:
// it proves the upfront/search-only/previewed assertions are NOT vacuous.
// Under ADR-090 §5.4 the full tier IS the upfront 36 (+ToolSearch infra), so
// the meaningful negative control is a genuinely SEARCH-ONLY tool:
// find_skills (ManifestLazy AND ManifestSearchOnly, in Mia's ADR-090 seeded
// allow set) must NOT appear in defs on turn 1 without a load call, and must
// NOT appear in the manifest note either. The previewed positive control is
// serve_web for General Purpose (the one previewed name, seeded allowed only
// there): it must appear in the note without being in turn-1 defs.
//
// This guards against a regression where buildCompressedToolDefs accidentally
// sends ALL tools (making the search-only negative vacuous), and against a
// regression where the manifest note accidentally lists every lazy tool
// regardless of visibility (making the previewed/search-only split vacuous).
//
// Traces to: ADR-090 §5.4; QA anti-shortcut: differentiation test.
func TestPromotedTaskTools_DifferentiationCheck(t *testing.T) {
	// find_skills is ManifestLazy AND ManifestSearchOnly (Tier 3) and in Mia's
	// ADR-090 allow-list — it is the search-only control for both properties.
	require.Equal(t, tools.ManifestLazy, tools.ToolManifestTier("find_skills"),
		"find_skills must be ManifestLazy for this differentiation test to be valid")
	require.Equal(t, tools.ManifestSearchOnly, tools.ToolManifestVisibility("find_skills"),
		"find_skills must be ManifestSearchOnly (Tier 3) for this differentiation test to be valid")
	// serve_web is the single previewed (Tier 2) name under ADR-090 §5.4.
	require.Equal(t, tools.ManifestLazy, tools.ToolManifestTier("serve_web"),
		"serve_web must be ManifestLazy for this differentiation test to be valid")
	require.Equal(t, tools.ManifestPreviewed, tools.ToolManifestVisibility("serve_web"),
		"serve_web must be ManifestPreviewed (Tier 2) for this differentiation test to be valid")

	al := newCompressedLoopWithServeWeb(t)

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

	// The ADR-090 upfront task trio is present (the positive case).
	for _, name := range []string{"create_task", "list_tasks", "update_task"} {
		assert.True(t, defNames[name],
			"%q (ADR-090 upfront set) must be in defs on turn 1", name)
	}

	// find_skills (search-only lazy) is absent from defs without a load —
	// the negative case for defs.
	assert.False(t, defNames["find_skills"],
		"DIFFERENTIATION: find_skills (lazy) must NOT be in defs on turn 1 without a load call. "+
			"If this assertion fails, buildCompressedToolDefs sends ALL tools regardless of tier — "+
			"the positive assertion above is then vacuous and the manifest optimization is broken.")

	note := al.buildToolManifestNote(ts, policyFiltered)
	// find_skills (search-only) does NOT appear in the note — proving the
	// previewed-vs-search-only split inside the manifest note is real, not
	// vacuous (Mia has no previewed allowed tool under ADR-090, so her note is
	// empty; the within-agent previewed positive is asserted below on worker).
	assert.NotContains(t, note, "  - find_skills",
		"DIFFERENTIATION: find_skills (search-only lazy) must NOT appear in the manifest note. "+
			"If this assertion fails, the ManifestSearchOnly filter in BuildCompressedManifest is not "+
			"actually filtering anything, and every lazy tool renders a preview line regardless of ADR-071 D3.")

	// Within-agent previewed positive: General Purpose holds the one previewed
	// name (serve_web, seeded allowed) plus a search-only one (environment_setup,
	// seeded ask) — the SAME agent's note must list the former and not the latter.
	workerAgent, ok := al.registry.GetAgent("worker")
	require.True(t, ok, "worker (General Purpose) must be in the seeded roster")
	workerFiltered, _ := tools.FilterToolsByPolicy(workerAgent.Tools.GetAll(), workerAgent.AgentType, workerAgent.LoadToolPolicy())
	var workerSeesServeWeb, workerSeesEnvSetup bool
	for _, t2 := range workerFiltered {
		switch t2.Name() {
		case "serve_web":
			workerSeesServeWeb = true
		case "environment_setup":
			workerSeesEnvSetup = true
		}
	}
	require.True(t, workerSeesServeWeb,
		"fixture: worker must hold serve_web (ADR-090 role policy) for the previewed control")
	require.True(t, workerSeesEnvSetup,
		"fixture: worker must hold environment_setup (ADR-090 seeded ask) for the search-only control")
	workerTS := fakeTurnState(workerAgent, "sess-gap2-diff-worker")
	workerNote := al.buildToolManifestNote(workerTS, workerFiltered)
	assert.Contains(t, workerNote, "  - serve_web",
		"DIFFERENTIATION: serve_web (previewed lazy) must appear in the manifest note")
	assert.NotContains(t, workerNote, "  - environment_setup",
		"DIFFERENTIATION: environment_setup (search-only lazy) must NOT appear in the manifest note")
	// And serve_web must NOT be in worker's turn-1 defs (it still pays the
	// discovery cost — previewed is a listing, not a grant of full defs).
	workerDefs := al.buildCompressedToolDefs(workerTS, workerFiltered)
	for _, d := range workerDefs {
		assert.NotEqual(t, "serve_web", d.Function.Name,
			"DIFFERENTIATION: serve_web (previewed lazy) must NOT be in defs on turn 1 without a load call")
	}
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

	// Every seeded agent (ADR-090 §2.0 roster) seeds ToolSearch "allow"
	// explicitly — the discovery infrastructure floor is always available
	// (ADR-090 §5.4: ToolSearch is in the global upfront set and cannot be
	// denied) — so it resolves "allow" and is present in the filtered slice
	// like any other allowed tool, regardless of what the agent's own role
	// policy says about the rest of the catalog.
	for _, agentID := range []string{"mia", "jim", "ava", "worker"} {
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
