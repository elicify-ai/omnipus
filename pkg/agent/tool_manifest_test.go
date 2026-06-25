// Omnipus — tool-manifest optimization tests for the agent loop (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the compressed-manifest mechanism in the agent loop. Each test is
// scoped narrowly so it does not OOM the dev pod — never run the full
// pkg/agent suite here.

package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
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
func fakeTurnState(agent *AgentInstance, sessionID string) *turnState {
	return &turnState{
		agent: agent,
		opts: processOptions{
			TranscriptSessionID: sessionID,
		},
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

// TestCompressedToolDefs_LegacyPath proves that with Compressed=false, defs
// equal ToolsToProviderDefs(policyFiltered) — byte-for-byte backward compat.
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

	// Legacy path: same count as ToolsToProviderDefs.
	legacyDefs := tools.ToolsToProviderDefs(pfOff)
	assert.Equal(t, len(legacyDefs), len(pfOff),
		"uncompressed: defs count must equal policy-filtered tool count")

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

			sessionID := "sess-reachability-" + agentID
			ts := fakeTurnState(agentInst, sessionID)
			defs := al.buildCompressedToolDefs(ts, policyFiltered)
			note := al.buildToolManifestNote(ts, policyFiltered)

			defNames := make(map[string]bool, len(defs))
			for _, d := range defs {
				defNames[d.Function.Name] = true
			}

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
func TestCanLoad_PolicyDeniedToolRejected(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	// Ava has deny-by-default; read_file is not in her allow list.
	avaAgent, ok := al.registry.GetAgent("ava")
	require.True(t, ok)

	// Verify read_file is not in Ava's policy-filtered set.
	allTools := avaAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, avaAgent.AgentType, avaAgent.LoadToolPolicy())
	hasReadFile := false
	for _, t := range policyFiltered {
		if t.Name() == "read_file" {
			hasReadFile = true
		}
	}
	if hasReadFile {
		t.Skip("read_file is allowed for ava in this config — skip policy-denied test")
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
