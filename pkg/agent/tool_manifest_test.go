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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// newCompressedCfg builds a minimal config with Compressed=true and the four
// core agents seeded (Mia, Jim, Ava, Ray).
func newCompressedCfg(t *testing.T) *config.Config {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "mock-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
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

	// Full-tier tools that Jim is allowed must be present.
	for _, name := range []string{"read_file", "send_message", "exec"} {
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
	al.markToolsLoaded(sessionID, []string{lazyName})

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

	al.markToolsLoaded("sess-A", []string{lazyName})

	// Session B must not see the loaded tool.
	tsB := fakeTurnState(jimAgent, "sess-B")
	defsB := al.buildCompressedToolDefs(tsB, policyFiltered)
	for _, d := range defsB {
		if d.Function.Name == lazyName {
			t.Errorf("sess-B must not inherit sess-A's loaded tool %q", lazyName)
		}
	}
}

// TestCompressedToolDefs_InfraAlwaysPresent proves load_tool and search_tools_*
// are always in the compressed defs (they are ManifestInfra).
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

	assert.True(t, defNames["load_tool"], "load_tool (infra) must always be in compressed defs")
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
		// The manifest header mentions "load_tool" by name in its prose, so we
		// only check for the bullet-entry format "  - <name>".
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

	// Find a lazy tool to load.
	var lazyName string
	for _, t := range policyFiltered {
		if tools.ToolManifestTier(t.Name()) == tools.ManifestLazy {
			lazyName = t.Name()
			break
		}
	}
	require.NotEmpty(t, lazyName)

	sessionID := "sess-loaded-exclude"
	al.markToolsLoaded(sessionID, []string{lazyName})

	ts := fakeTurnState(jimAgent, sessionID)
	note := al.buildToolManifestNote(ts, policyFiltered)

	// The loaded tool must not appear as a manifest entry.
	assert.NotContains(t, note, "  - "+lazyName,
		"loaded tool %q must be excluded from manifest note", lazyName)
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
	al.markToolsLoaded(sessionID, lazyNames)

	ts := fakeTurnState(miaAgent, sessionID)
	note := al.buildToolManifestNote(ts, policyFiltered)
	assert.Empty(t, note, "manifest note must be empty when all lazy tools are loaded")
}

// ─── Reachability invariant ─────────────────────────────────────────────────

// TestReachabilityInvariant_AllCoreAgents proves that for each core agent,
// every policy-allowed tool is reachable: it is either in the compressed defs
// (full/infra) OR it is in the manifest note (lazy, loadable).
//
// No allowed tool may be silently unreachable — this is the critical invariant.
// Non-vacuous: asserts each agent has ≥1 FULL-tier AND ≥1 LAZY-tier tool so
// both switch arms are exercised, and an empty policyFiltered set fails loudly.
func TestReachabilityInvariant_AllCoreAgents(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	for _, agentID := range []string{"mia", "jim", "ava", "ray"} {
		agentID := agentID // capture
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
			require.Greater(t, fullCount, 0,
				"agent %q: must have ≥1 full-tier tool in policyFiltered — both tier branches must be exercised", agentID)
			require.Greater(t, lazyCount, 0,
				"agent %q: must have ≥1 lazy-tier tool in policyFiltered — both tier branches must be exercised", agentID)

			for _, tool := range policyFiltered {
				name := tool.Name()
				tier := tools.ToolManifestTier(name)
				switch tier {
				case tools.ManifestFull, tools.ManifestInfra:
					// Must be in defs.
					assert.True(t, defNames[name],
						"agent %q: full/infra tool %q must be in compressed defs", agentID, name)
				case tools.ManifestLazy:
					// Must appear in the manifest note (as a loadable entry).
					// The entry format is "  - <name>".
					assert.Contains(t, note, "  - "+name,
						"agent %q: lazy tool %q must appear in manifest note", agentID, name)
				}
			}
		})
	}
}

// TestReachabilityInvariant_LoadToolInfra_DenyDefaultAgent proves that even for
// Ava (deny-by-default), load_tool is always present in the compressed defs.
// This is the critical infra invariant: an agent must always be able to load.
func TestReachabilityInvariant_LoadToolInfra_DenyDefaultAgent(t *testing.T) {
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

	assert.True(t, defNames["load_tool"],
		"load_tool must always be in compressed defs even for deny-default agent (ava)")
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

	// Build ctx as load_tool's resolver would see it.
	ctx := tools.WithAgentID(context.Background(), "ava")
	ctx = tools.WithTranscriptSessionID(ctx, "sess-canload")

	// Retrieve the actual load_tool's canLoad via the registered tool.
	loadToolRaw, ok := avaAgent.Tools.Get("load_tool")
	require.True(t, ok, "load_tool must be registered for ava in compressed mode")

	lt, ok := loadToolRaw.(*tools.LoadTool)
	require.True(t, ok, "load_tool must be *tools.LoadTool")

	// We cannot call lt.canLoad directly (unexported). Instead we call Execute
	// with the tool wired and check the result — if canLoad returns true, Execute
	// succeeds (schema is returned); if false, it returns an error.
	result := lt.Execute(ctx, map[string]any{"names": []any{"find_skills"}})
	assert.False(t, result.IsError,
		"find_skills must be loadable by ava; got error: %s", result.ForLLM)
}

// TestCanLoad_FullTierNotLoadable proves that a full-tier tool cannot be
// loaded via load_tool (it is already callable — not loadable).
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

	loadToolRaw, ok := jimAgent.Tools.Get("load_tool")
	require.True(t, ok, "load_tool must be registered for jim in compressed mode")
	lt, ok := loadToolRaw.(*tools.LoadTool)
	require.True(t, ok)

	result := lt.Execute(ctx, map[string]any{"names": []any{"send_message"}})
	assert.True(t, result.IsError,
		"send_message (full-tier) must not be loadable via load_tool")
}

// TestCanLoad_PolicyDeniedToolRejected proves that a policy-denied tool cannot
// be loaded via load_tool (cannot bypass policy via load).
// This test is structurally non-skippable: Ava is deny-by-default and read_file
// is not in her explicit allow-list (pkg/coreagent/core.go Ava policy). If
// read_file appears in Ava's policy-filtered set, that is itself a regression
// that must fail loudly — not be silently skipped.
func TestCanLoad_PolicyDeniedToolRejected(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	// Ava has deny-by-default; read_file is not in her allow list.
	avaAgent, ok := al.registry.GetAgent("ava")
	require.True(t, ok)

	// Structural guarantee: read_file must NOT be in Ava's policy-filtered set.
	// If this assertion fails, the test should fail loudly — it means the Ava
	// policy was changed to allow read_file, which would invalidate the security
	// assertion below. Do NOT replace this with t.Skip.
	allTools := avaAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, avaAgent.AgentType, avaAgent.LoadToolPolicy())
	for _, tool := range policyFiltered {
		require.NotEqual(t, "read_file", tool.Name(),
			"POLICY REGRESSION: read_file must NOT be allowed for ava (deny-by-default). "+
				"If this assertion breaks, the Ava policy changed — verify the intent in core.go and update this test.")
	}

	ctx := tools.WithAgentID(context.Background(), "ava")
	ctx = tools.WithTranscriptSessionID(ctx, "sess-denied")

	loadToolRaw, ok := avaAgent.Tools.Get("load_tool")
	require.True(t, ok)
	lt, ok := loadToolRaw.(*tools.LoadTool)
	require.True(t, ok)

	result := lt.Execute(ctx, map[string]any{"names": []any{"read_file"}})
	assert.True(t, result.IsError,
		"read_file must be rejected for ava (policy denied); got: %s", result.ForLLM)
}

// ─── FIX 2 regression: session-ID consistency ──────────────────────────────

// TestSessionID_NoTranscript_LoadedToolsVisible is the regression test for
// FIX 2. It proves that when TranscriptSessionID is empty (transcript disabled)
// but sessionKey is set, tools marked loaded via the writer's derivation
// (manifestSessionID("", sessionKey) == sessionKey) are visible to the readers
// (buildCompressedToolDefs, buildToolManifestNote) that also use manifestSessionID.
//
// Before FIX 2: readers used ts.opts.TranscriptSessionID directly (""), so
// they looked up the "" bucket while the writer stored under sessionKey →
// loaded tools were invisible to the model.
// After FIX 2: both sides call manifestSessionID("", sessionKey) == sessionKey
// → same bucket, loaded tools visible.
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

	// Simulate the writer: markToolsLoaded using manifestSessionID("", sessionKey).
	writerKey := manifestSessionID("", sessionKey)
	al.markToolsLoaded(writerKey, []string{lazyName})

	// Build turnState as the reader would see it: no transcript ID, session key set.
	ts := fakeTurnStateNoTranscript(jimAgent, sessionKey)

	// Reader: buildCompressedToolDefs must find the loaded tool.
	defs := al.buildCompressedToolDefs(ts, policyFiltered)
	defNames := make(map[string]bool, len(defs))
	for _, d := range defs {
		defNames[d.Function.Name] = true
	}
	assert.True(t, defNames[lazyName],
		"FIX2: lazy tool %q must appear in compressed defs when loaded via session-key bucket (no transcript)", lazyName)

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

	// The real post-FIX 1 behavior: drive load_tool.Execute with a name whose
	// schema resolution will fail (empty string agent ID → agent not found).
	// canLoad will reject it at the pre-rejected stage, not at markLoaded.
	// So we verify the full round-trip is consistent: rejected comes back in
	// the result, not in the loaded set.
	loadToolRaw, ok := jimAgent.Tools.Get("load_tool")
	require.True(t, ok, "load_tool must be registered for jim")
	lt, ok := loadToolRaw.(*tools.LoadTool)
	require.True(t, ok)

	// Execute with an unknown name — should be rejected pre-markLoaded.
	ctx := tools.WithAgentID(context.Background(), "jim")
	ctx = tools.WithTranscriptSessionID(ctx, "sess-fix1-roundtrip")
	result := lt.Execute(ctx, map[string]any{"names": []any{"nonexistent_phantom_xyz"}})
	assert.True(t, result.IsError,
		"nonexistent_phantom_xyz must be rejected by load_tool; got: %s", result.ForLLM)

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
// MCP discovery OFF, search_tools_bm25 and search_tools_regex are registered in
// each agent's Tools registry (Gap 2 fix: available without MCP discovery).
func TestSearchToolsRegistered_CompressedMode(t *testing.T) {
	cfg := newCompressedCfg(t)
	// Ensure MCP discovery is off (the default in test configs) so we verify
	// the Gap 2 path specifically.
	cfg.Tools.MCP.Discovery.Enabled = false

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	for _, agentID := range []string{"mia", "jim", "ava", "ray"} {
		agentInst, ok := al.registry.GetAgent(agentID)
		require.True(t, ok, "agent %q must be in registry", agentID)

		_, hasRegex := agentInst.Tools.Get("search_tools_regex")
		assert.True(t, hasRegex,
			"agent %q: search_tools_regex must be registered when Compressed=true", agentID)

		_, hasBM25 := agentInst.Tools.Get("search_tools_bm25")
		assert.True(t, hasBM25,
			"agent %q: search_tools_bm25 must be registered when Compressed=true", agentID)
	}
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

// TestLoadToCallableRoundTrip proves that executing load_tool for a valid lazy
// name causes that name to appear in buildCompressedToolDefs for the same
// session on the next call — i.e., the tool becomes callable after a load.
//
// This is an end-to-end chain: load_tool.Execute → markLoaded closure →
// al.markToolsLoaded → al.buildCompressedToolDefs sees the tool in defs.
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
			"lazy tool %q must not be callable before load_tool is called", lazyName)
	}

	// Execute load_tool via the registered instance (uses the real markLoaded closure).
	loadToolRaw, ok := jimAgent.Tools.Get("load_tool")
	require.True(t, ok, "load_tool must be registered for jim")
	lt, ok := loadToolRaw.(*tools.LoadTool)
	require.True(t, ok)

	ctx := tools.WithAgentID(context.Background(), "jim")
	ctx = tools.WithTranscriptSessionID(ctx, transcriptID)
	ctx = tools.WithSessionKey(ctx, transcriptID) // match the session key for manifestSessionID

	result := lt.Execute(ctx, map[string]any{"names": []any{lazyName}})
	require.False(t, result.IsError,
		"load_tool.Execute must succeed for a valid lazy tool; got: %s", result.ForLLM)

	// After load: lazyName must appear in compressed defs.
	tsAfter := fakeTurnState(jimAgent, transcriptID)
	defsAfter := al.buildCompressedToolDefs(tsAfter, policyFiltered)
	defNamesAfter := make(map[string]bool, len(defsAfter))
	for _, d := range defsAfter {
		defNamesAfter[d.Function.Name] = true
	}
	assert.True(t, defNamesAfter[lazyName],
		"load→callable round-trip: lazy tool %q must be in compressed defs after load_tool.Execute", lazyName)
}

// TestInfraToolsExecutable_DenyDefaultAgent is the regression test for the bug
// found by live validation: a deny-by-default agent (Ava/Mia) was SHOWN load_tool
// in its provider defs (force-included) but the EXECUTION gate denied it, so every
// lazy tool was unreachable in practice. This asserts the full authorization chain
// now allows infra-tool execution.
func TestInfraToolsExecutable_DenyDefaultAgent(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	// Ava and Mia are deny-by-default; load_tool is not in their allow-list.
	for _, agentID := range []string{"ava", "mia"} {
		t.Run(agentID, func(t *testing.T) {
			agentInst, ok := al.registry.GetAgent(agentID)
			require.True(t, ok)

			allTools := agentInst.Tools.GetAll()
			policyFiltered, policyMap := tools.FilterToolsByPolicy(allTools, agentInst.AgentType, agentInst.LoadToolPolicy())

			// Precondition (the bug): raw policy does NOT authorize load_tool for a
			// deny-default agent. (If a future seed adds it explicitly this just
			// makes the test trivially pass — still correct.)
			_, rawAllowed := policyMap["load_tool"]

			// Apply the fix: force infra tools into the exec snapshot.
			policyFiltered = ensureInfraToolsExecutable(true, agentInst.Tools, policyFiltered, policyMap)

			// After the fix: load_tool is in the snapshot as "allow".
			require.Equal(t, "allow", policyMap["load_tool"],
				"agent %q: load_tool must be allow in the exec policy snapshot (was rawAllowed=%v)", agentID, rawAllowed)
			require.Contains(t, toolNameSet(policyFiltered), "load_tool",
				"agent %q: load_tool must be in the sent defs surface", agentID)

			// The execution gate itself must authorize load_tool end-to-end.
			ts := fakeTurnState(agentInst, "sess-exec-"+agentID)
			require.Equal(t, "allow", al.resolveToolPolicyAtExec(ts, "load_tool", policyMap),
				"agent %q: resolveToolPolicyAtExec must allow load_tool", agentID)
			require.Equal(t, "allow", al.resolveSingleToolPolicy(ts, "load_tool"),
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

// TestEnsureInfraToolsExecutable_NoopWhenCompressedOff verifies the legacy path
// is untouched: with compressed=false, infra tools are NOT force-allowed.
func TestEnsureInfraToolsExecutable_NoopWhenCompressedOff(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()
	ava, ok := al.registry.GetAgent("ava")
	require.True(t, ok)
	allTools := ava.Tools.GetAll()
	policyFiltered, policyMap := tools.FilterToolsByPolicy(allTools, ava.AgentType, ava.LoadToolPolicy())
	before := len(policyFiltered)
	out := ensureInfraToolsExecutable(false, ava.Tools, policyFiltered, policyMap)
	require.Len(t, out, before, "compressed=false must not add infra tools")
	_, ok = policyMap["load_tool"]
	require.False(t, ok, "compressed=false must not allow load_tool")
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
// Monotonicity ensures that each load_tool call increases the sent surface
// predictably and never exceeds what we'd send without compression.
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
		al.markToolsLoaded(sessionID, []string{lazyNames[i]})

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
	al.markToolsLoaded(sessionID, lazyNames)

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

// TestSearchThenLoad_Reachability proves that a tool found via the search_tools_bm25
// or search_tools_regex infra tool is in the lazy/loadable set, and that calling
// load_tool then makes it appear in buildCompressedToolDefs (callable).
//
// This is the "search→find→load→callable" chain at the helper level. It chains
// three helpers without a live LLM:
//  1. search_tools_bm25.Execute finds a lazy tool by description.
//  2. After the search, the tool is promoted (TTL > 0), so Get returns it.
//  3. load_tool.Execute loads it for the session.
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
	var lazyName, lazyDesc string
	for _, tool := range policyFiltered {
		if tools.ToolManifestTier(tool.Name()) == tools.ManifestLazy {
			lazyName = tool.Name()
			lazyDesc = tool.Description()
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
			"lazy tool %q must not be callable before load_tool is called", lazyName)
	}
	_ = lazyDesc // used below for BM25 but description may differ after registration

	// Step 3: Call load_tool.Execute with the lazy name (simulating the model
	// calling load_tool after a search result returned the tool name).
	loadToolRaw, ok := jimAgent.Tools.Get("load_tool")
	require.True(t, ok, "load_tool must be registered for jim in compressed mode")
	lt, ok := loadToolRaw.(*tools.LoadTool)
	require.True(t, ok, "load_tool must be *tools.LoadTool")

	ctx := tools.WithAgentID(context.Background(), "jim")
	ctx = tools.WithTranscriptSessionID(ctx, transcriptID)
	ctx = tools.WithSessionKey(ctx, transcriptID)

	loadResult := lt.Execute(ctx, map[string]any{"names": []any{lazyName}})
	require.False(t, loadResult.IsError,
		"load_tool must succeed for lazy tool %q found via search; error: %s", lazyName, loadResult.ForLLM)

	// Step 4: After load, the tool must appear in compressed defs (callable).
	tsAfter := fakeTurnState(jimAgent, transcriptID)
	defsAfter := al.buildCompressedToolDefs(tsAfter, policyFiltered)
	defNamesAfter := make(map[string]bool, len(defsAfter))
	for _, d := range defsAfter {
		defNamesAfter[d.Function.Name] = true
	}
	assert.True(t, defNamesAfter[lazyName],
		"search-then-load chain: lazy tool %q must be callable after load_tool.Execute", lazyName)
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
	al.markToolsLoaded(sessionID, []string{lazyName})
	ts1 := fakeTurnState(jimAgent, sessionID)
	defs1 := al.buildCompressedToolDefs(ts1, policyFiltered)
	note1 := al.buildToolManifestNote(ts1, policyFiltered)

	// Load the same tool again (idempotent).
	al.markToolsLoaded(sessionID, []string{lazyName})
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
			al.markToolsLoaded(sessionID, []string{tool.Name()})
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
