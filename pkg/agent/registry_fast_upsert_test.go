// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/routing"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// Regression tests for issue #571: "Agent create/update triggers a full
// config reload — finish ADR-054's split on the read path."
//
// A test that only asserts "the agent is in the map" passes against every
// broken variant of this fix (a bare UpsertAgent call, no resolver refresh,
// no wiring). These tests instead prove an agent published via
// AgentLoop.UpsertAgentFast is INDISTINGUISHABLE from one produced by a full
// AgentLoop.ReloadProviderAndConfig rebuild: same tool set, same resolved
// tool policy, resolvable via GetAgent/ResolveRoute/GetDefaultAgent, and safe
// under concurrent calls for DIFFERENT agents (the exact "Ava building a
// team" scenario ADR-054 fixed on the write side).
// ---------------------------------------------------------------------------

// buildFastUpsertTestLoop builds a real *AgentLoop (not a bare *AgentRegistry
// stub) rooted at a fresh, hermetic OMNIPUS_HOME, seeded with agents. Mirrors
// the established pattern in memory_reload_wiring_test.go.
func buildFastUpsertTestLoop(t *testing.T, agents []config.AgentConfig) *AgentLoop {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10
	cfg.Agents.List = agents

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })
	return al
}

// cloneCfg returns a fresh *config.Config carrying the same
// Agents.Defaults/List/Sandbox as live, WITHOUT aliasing live's slices or its
// sync.RWMutex (config.Config is not itself copy-safe as a struct value —
// see reloadWithSameAgents's doc comment in memory_reload_wiring_test.go for
// the established rationale). Callers append/mutate the returned config's
// own Agents.List freely.
func cloneCfg(t *testing.T, live *config.Config) *config.Config {
	t.Helper()
	nc := &config.Config{}
	nc.Agents.Defaults = live.Agents.Defaults
	nc.Agents.List = make([]config.AgentConfig, len(live.Agents.List), len(live.Agents.List)+1)
	copy(nc.Agents.List, live.Agents.List)
	nc.Sandbox = live.Sandbox
	nc.SkippedAgentIDs = append([]string(nil), live.SkippedAgentIDs...)
	return nc
}

// toolNames returns the sorted tool names registered on inst.
func toolNames(inst *AgentInstance) []string {
	all := inst.Tools.GetAll()
	names := make([]string, 0, len(all))
	for _, tl := range all {
		names = append(names, tl.Name())
	}
	sort.Strings(names)
	return names
}

// resolveRouteSafely calls ResolveRoute and converts a panic (e.g. a nil
// *routing.RouteResolver — the exact failure mode of TRAP 1 left unclosed)
// into a clean, readable test failure instead of a raw panic/stack trace.
func resolveRouteSafely(t *testing.T, reg *AgentRegistry, input routing.RouteInput) (out routing.ResolvedRoute) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ResolveRoute panicked (TRAP 1 not closed — the registry's cached RouteResolver "+
				"was not rebuilt fresh from cfg): %v", r)
		}
	}()
	return reg.ResolveRoute(input)
}

// TestUpsertAgentFast_ParityWithFullReload is the strongest form of the
// acceptance test: the SAME new agent is built two independent ways — once
// via AgentLoop.UpsertAgentFast (the fix under test) and once via a full
// AgentLoop.ReloadProviderAndConfig rebuild (the pre-existing, trusted
// baseline) — starting from identical config. The two resulting
// *AgentInstance values must be indistinguishable in tool set and resolved
// tool policy.
//
// MUTATION-SENSITIVE (TRAP 2 / wiring parity): if UpsertAgentFast's re-wiring
// pass (registerSharedTools and the passes after it) is skipped or partial,
// the fast-upserted instance is missing tools registerSharedTools alone adds
// (delegate, web_search, web_fetch, message, handoff, send_file, find_skills,
// task_*, list_jobs, browser tools, …) while NewAgentInstance's own
// unconditional registrations (read_file, write_file, bash, …) still show up
// — producing a strict SUBSET, not an empty set, so the failure is a clear
// asymmetric diff rather than "everything is missing".
func TestUpsertAgentFast_ParityWithFullReload(t *testing.T) {
	baseAgents := []config.AgentConfig{
		{ID: "alpha", Name: "Alpha", Type: config.AgentTypeCustom},
	}
	newAgent := config.AgentConfig{ID: "gamma", Name: "Gamma", Type: config.AgentTypeCustom}

	alFast := buildFastUpsertTestLoop(t, baseAgents)
	alFull := buildFastUpsertTestLoop(t, baseAgents)

	cfgFast := cloneCfg(t, alFast.GetConfig())
	cfgFast.Agents.List = append(cfgFast.Agents.List, newAgent)
	cfgFull := cloneCfg(t, alFull.GetConfig())
	cfgFull.Agents.List = append(cfgFull.Agents.List, newAgent)

	instFast, err := alFast.UpsertAgentFast(cfgFast, "gamma")
	require.NoError(t, err, "UpsertAgentFast must succeed")
	require.NotNil(t, instFast)

	require.NoError(t, alFull.ReloadProviderAndConfig(context.Background(), &mockProvider{}, cfgFull),
		"full reload must succeed to build the trusted comparison baseline")
	instFull, ok := alFull.GetRegistry().GetAgent("gamma")
	require.True(t, ok, "full-reload registry must contain gamma")
	require.NotNil(t, instFull)

	// --- Same tool set. ---
	namesFast := toolNames(instFast)
	namesFull := toolNames(instFull)
	require.Equal(t, namesFull, namesFast,
		"a fast-upserted agent must have the IDENTICAL tool set to a fully-rebuilt one")

	// --- Same resolved tool policy, for every tool the full-reload instance has,
	// using EACH tool's own declared Scope() (not a single assumed scope). ---
	polFast := instFast.LoadToolPolicy()
	polFull := instFull.LoadToolPolicy()
	require.NotNil(t, polFast)
	require.NotNil(t, polFull)
	for _, tl := range instFull.Tools.GetAll() {
		name := tl.Name()
		scope := tl.Scope()
		want := tools.EffectiveToolPolicy(polFull, scope, instFull.AgentType, name)
		got := tools.EffectiveToolPolicy(polFast, scope, instFast.AgentType, name)
		require.Equal(t, want, got, "resolved tool policy for %q must match between fast-upsert and full reload", name)
	}

	require.Equal(t, instFull.AgentType, instFast.AgentType, "AgentType must match")
	require.Equal(t, instFull.Model, instFast.Model, "Model must match")
}

// TestUpsertAgentFast_TrapsClosed_RoutingAndDefaultAgent proves TRAP 1 is
// closed: a fast-upserted agent must be resolvable not just via GetAgent (the
// map lookup every broken variant also satisfies) but through ResolveRoute
// and GetDefaultAgent too — the registry's cached RouteResolver and
// defaultAgentOverride, which have no setter of their own and are otherwise
// only refreshed by a full registry rebuild.
//
// MUTATION-SENSITIVE (TRAP 1): commenting out
// `newRegistry.resolver = routing.NewRouteResolver(cfg)` inside
// UpsertAgentFast makes the ResolveRoute assertion below fail (via
// resolveRouteSafely's panic-to-fatal conversion, since cloneAgents()
// deliberately leaves resolver at its nil zero value). Commenting out the
// `newRegistry.SetDefaultAgentOverride(...)` call makes the GetDefaultAgent
// assertion fail (falls back to the lexicographically-first non-worker agent
// instead of resolving the newly-configured default — there is no "main"
// sentinel to fall back to anymore).
func TestUpsertAgentFast_TrapsClosed_RoutingAndDefaultAgent(t *testing.T) {
	al := buildFastUpsertTestLoop(t, []config.AgentConfig{
		{ID: "alpha", Name: "Alpha", Type: config.AgentTypeCustom},
	})

	if _, ok := al.GetRegistry().GetAgent("gamma"); ok {
		t.Fatal("gamma must not exist before the upsert")
	}

	newAgent := config.AgentConfig{ID: "gamma", Name: "Gamma", Type: config.AgentTypeCustom}
	cfg := cloneCfg(t, al.GetConfig())
	cfg.Agents.List = append(cfg.Agents.List, newAgent)

	inst, err := al.UpsertAgentFast(cfg, "gamma")
	require.NoError(t, err)
	require.NotNil(t, inst)

	// --- GetAgent: the exact primitive validateTaskAgentID (POST /tasks) uses. ---
	got, ok := al.GetRegistry().GetAgent("gamma")
	require.True(t, ok, "GetAgent must resolve the fast-upserted agent (POST /tasks validation path)")
	require.Same(t, inst, got, "GetAgent must return the SAME instance UpsertAgentFast just built")

	// --- ResolveRoute (TRAP 1). ---
	route := resolveRouteSafely(t, al.GetRegistry(), routing.RouteInput{
		Channel:  "webchat",
		Identity: &config.ChannelIdentity{Kind: "agent", ID: "gamma"},
	})
	require.False(t, route.Drop, "route must not drop")
	require.Equal(t, "gamma", route.AgentID,
		"ResolveRoute must resolve to the fast-upserted agent — this fails if the registry's cached "+
			"RouteResolver is not rebuilt fresh from cfg (TRAP 1)")

	// --- Workspace core_team validation's underlying data condition.
	// pkg/gateway/rest_workspaces.go's validateCoreTeamMembers reads
	// cfg.Agents.List directly (not the registry) — asserted here against the
	// SAME cfg object UpsertAgentFast published as al.cfg. ---
	liveCfg := al.GetConfig()
	found := false
	for i := range liveCfg.Agents.List {
		if liveCfg.Agents.List[i].ID == "gamma" {
			found = true
			require.False(t, liveCfg.Agents.List[i].IsSystem(), "gamma must not be classified a System Agent")
		}
	}
	require.True(t, found, "al.cfg.Agents.List (published by UpsertAgentFast) must contain gamma")

	// --- GetDefaultAgent (TRAP 1), once cfg names gamma as the default. ---
	cfg2 := cloneCfg(t, al.GetConfig()) // already contains gamma from the upsert above
	cfg2.Agents.Defaults.DefaultAgentID = "gamma"
	inst2, err := al.UpsertAgentFast(cfg2, "gamma")
	require.NoError(t, err)
	require.NotNil(t, inst2)

	def := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, def)
	require.Equal(t, "gamma", def.ID,
		"GetDefaultAgent must resolve the fast-upserted agent once cfg.Agents.Defaults.DefaultAgentID "+
			"names it — this fails if defaultAgentOverride is not refreshed fresh from cfg (TRAP 1)")
}

// TestUpsertAgentFast_ConcurrentDifferentAgents_NoLostUpdate proves TRAP 2's
// atomicity requirement holds under real concurrency, not just in a single
// goroutine: N different agents upserted in parallel (the "Ava building a
// team" scenario ADR-054 fixed on the write side) must ALL survive — a bare
// "read old registry, clone, wire, write" would let a later publisher
// silently overwrite an earlier one's agent with a clone that never saw it.
// Every caller is handed the SAME, already-complete config snapshot (mirrors
// how a real REST request's cfg is always a full re-read of the entity store
// via refreshConfigAndRewireServices, never a partial delta) so this isolates
// the assertion to the registry-publish race itself, not to cfg-merging
// (which UpsertAgentFast does not claim to solve).
//
// Run with -race to additionally confirm no data race in the publish path.
func TestUpsertAgentFast_ConcurrentDifferentAgents_NoLostUpdate(t *testing.T) {
	al := buildFastUpsertTestLoop(t, []config.AgentConfig{
		{ID: "alpha", Name: "Alpha", Type: config.AgentTypeCustom},
	})

	const n = 12
	ids := make([]string, n)
	fullList := append([]config.AgentConfig(nil), al.GetConfig().Agents.List...)
	for i := 0; i < n; i++ {
		ids[i] = fmt.Sprintf("team-%02d", i)
		fullList = append(fullList, config.AgentConfig{ID: ids[i], Name: ids[i], Type: config.AgentTypeCustom})
	}
	baseCfg := cloneCfg(t, al.GetConfig())
	baseCfg.Agents.List = fullList

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := al.UpsertAgentFast(baseCfg, ids[i])
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "concurrent UpsertAgentFast #%d must not fail", i)
	}

	reg := al.GetRegistry()
	for _, id := range ids {
		_, ok := reg.GetAgent(id)
		require.True(t, ok,
			"agent %q must be present in the registry after concurrent parallel creation — losing it here "+
				"reintroduces the exact split-brain race ADR-054 fixed on the write side (e.g. Ava building a "+
				"team), just relocated to UpsertAgentFast's own publish step (TRAP 2)", id)
	}
	if _, ok := reg.GetAgent("alpha"); !ok {
		t.Fatal("pre-existing agent 'alpha' must survive concurrent upserts of OTHER agents")
	}
}

// TestUpsertAgentFast_UnknownAgent verifies the error path: cfg not
// containing agentID must fail loudly rather than silently upserting nothing
// or an empty instance.
func TestUpsertAgentFast_UnknownAgent(t *testing.T) {
	al := buildFastUpsertTestLoop(t, []config.AgentConfig{{ID: "alpha", Type: config.AgentTypeCustom}})
	cfg := al.GetConfig()
	_, err := al.UpsertAgentFast(cfg, "does-not-exist")
	require.Error(t, err, "UpsertAgentFast must reject an agent ID absent from cfg.Agents.List")
}

// TestUpsertAgentFast_MainIsNoLongerReserved pins the removal of the "main"
// sentinel: the retired sentinel used to occupy a reserved-ID slot that
// NewAgentRegistry (and this UpsertAgentFast path) rejected any real agent
// from claiming. That reserved-ID map, and UpsertAgentFast's rejection of
// it, are BOTH gone now — "main" has no schema of its own, so an operator is
// free to create a perfectly ordinary agent literally named "main" and it
// must succeed exactly like any other id, with no special-cased failure.
func TestUpsertAgentFast_MainIsNoLongerReserved(t *testing.T) {
	al := buildFastUpsertTestLoop(t, []config.AgentConfig{{ID: "alpha", Type: config.AgentTypeCustom}})
	cfg := cloneCfg(t, al.GetConfig())
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: "main", Name: "Just A Regular Agent"})
	inst, err := al.UpsertAgentFast(cfg, "main")
	require.NoError(t, err, "UpsertAgentFast must accept 'main' like any other agent id — it is not reserved anymore")
	require.NotNil(t, inst)
	require.Equal(t, "main", inst.ID)

	got, ok := al.GetRegistry().GetAgent("main")
	require.True(t, ok, "the newly-created 'main' agent must be resolvable via GetAgent like any other agent")
	require.Same(t, inst, got)
}
