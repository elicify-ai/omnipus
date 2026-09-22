// Omnipus — ADR-090 TEST007: actual-runtime manifest acceptance across the
// nine-role roster. Complements tool_manifest_test.go (tier mechanics on the
// pre-ADR-090 core roster) and pkg/gateway's knowledge posture tests (verdict
// resolution) by checking what a REAL loop registry actually sends: the
// upfront tool set in both tool contexts, deferred tools requiring discovery,
// denied tools staying unreachable, and the roster the loop itself registers.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// adr090RosterIDs is the current nine-role roster (ADR-090 spec table): four
// core chat roles, three worker roles, two hidden System Agents. Ray, explorer
// and max are retired and must not appear anywhere this file checks. Stated
// from the spec, not read back from coreagent, so a roster regression fails
// here rather than agreeing with itself.
var adr090RosterIDs = []string{
	string(coreagent.IDMia), string(coreagent.IDJim), string(coreagent.IDAva),
	string(coreagent.IDAdmin), string(coreagent.IDPlanner), string(coreagent.IDResearcher),
	string(coreagent.IDWorker), string(coreagent.IDJudge), string(coreagent.IDPlanSupervisor),
}

// adr090DefNames flattens provider tool definitions to a name set.
func adr090DefNames(defs []providers.ToolDefinition) map[string]bool {
	m := make(map[string]bool, len(defs))
	for _, d := range defs {
		m[d.Function.Name] = true
	}
	return m
}

// adr090Load runs the REAL ToolSearch load path (loop_wire.go::registerToolSearch
// closures — canLoad policy gate, markLoaded bucketing) for one tool name, with
// a context carrying the same (agent, transcript session, session key) triple a
// live turn would. Returns the tool's own ToolResult for the caller to judge.
func adr090Load(t *testing.T, al *AgentLoop, agentID, sessionID, toolName string) *tools.ToolResult {
	t.Helper()
	inst, ok := al.registry.GetAgent(agentID)
	require.Truef(t, ok, "role %q must be registered to run a ToolSearch load", agentID)
	raw, ok := inst.Tools.Get("ToolSearch")
	require.Truef(t, ok, "ToolSearch must be registered for %q", agentID)
	tt, ok := raw.(*tools.ToolsTool)
	require.Truef(t, ok, "ToolSearch must be a *tools.ToolsTool, got %T", raw)
	ctx := tools.WithAgentID(context.Background(), agentID)
	ctx = tools.WithTranscriptSessionID(ctx, sessionID)
	ctx = tools.WithSessionKey(ctx, sessionID)
	return tt.Execute(ctx, map[string]any{"names": []any{toolName}})
}

// TestADR090_Acceptance_RosterRegistryMatchesNineRoles pins the roster the
// loop itself registers on a fresh-install composition: every one of the nine
// ADR-090 roles present, and NOTHING outside them. The config-level roster is
// already pinned in pkg/coreagent (adr090_roster_test.go); this adds the
// runtime half — SeedConfig's list actually becoming loop instances — so a
// registry filter (e.g. a chat-visibility overreach) cannot silently drop or
// add roles between config and runtime.
func TestADR090_Acceptance_RosterRegistryMatchesNineRoles(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	want := make(map[string]bool, len(adr090RosterIDs))
	for _, id := range adr090RosterIDs {
		want[id] = true
	}
	for _, id := range al.registry.ListAgentIDs() {
		assert.Truef(t, want[id],
			"the loop registry carries %q outside the ADR-090 nine-role roster — a retired or unseeded identity leaked through", id)
	}
	for _, id := range adr090RosterIDs {
		_, ok := al.registry.GetAgent(id)
		assert.Truef(t, ok, "ADR-090 role %q must be registered as a loop instance", id)
	}
}

// adr090AssertContextsForRole asserts the TEST007 contract for one role:
// every policy-allowed FULL-tier tool (the globally upfront tool set) is
// callable in BOTH the normal and the compressed tool context; the compressed
// context additionally carries the ToolSearch discovery door while the normal
// context does not; neither context ever surfaces a name policy did not allow;
// and a registered-but-denied tool is reachable through none of the surfaces.
//
// The two surfaces are built with the EXACT producer expressions a live turn
// uses (loop_run_turn.go): the normal path is
// tools.ToolsToProviderDefs(stripInfraToolDefs(policyFilteredTools)) and the
// compressed path is buildCompressedToolDefs(turnState, policyFilteredTools),
// both fed the same tools.FilterToolsByPolicy output a turn would compute.
func adr090AssertContextsForRole(t *testing.T, al *AgentLoop, agentID string) {
	t.Helper()
	inst, ok := al.registry.GetAgent(agentID)
	require.Truef(t, ok, "ADR-090 role %q must be registered", agentID)

	pf, _ := tools.FilterToolsByPolicy(inst.Tools.GetAll(), inst.AgentType, inst.LoadToolPolicy())
	require.NotEmptyf(t, pf, "role %q resolved an empty policy-filtered tool set", agentID)

	allowed := make(map[string]bool, len(pf))
	var upfront []string
	for _, tool := range pf {
		allowed[tool.Name()] = true
		if tools.ToolManifestTier(tool.Name()) == tools.ManifestFull {
			upfront = append(upfront, tool.Name())
		}
	}
	// Non-vacuity: if a role allows no full-tier tool at all, the upfront-set
	// assertions below would pass without testing anything.
	require.NotEmptyf(t, upfront,
		"role %q allows no full-tier (upfront) tool — the upfront-set leg would be vacuous", agentID)

	normalNames := adr090DefNames(tools.ToolsToProviderDefs(stripInfraToolDefs(pf)))
	ts := fakeTurnState(inst, "sess-adr090-ctx-"+agentID)
	compressedNames := adr090DefNames(al.buildCompressedToolDefs(ts, pf))

	for _, name := range upfront {
		assert.Truef(t, normalNames[name],
			"upfront tool %q must be callable in the NORMAL context for %s", name, agentID)
		assert.Truef(t, compressedNames[name],
			"upfront tool %q must be callable in the COMPRESSED context for %s", name, agentID)
	}

	// The discovery door exists only in the compressed context. Driving the
	// normal-path producer over the COMPRESSED fixture's filtered set (where
	// ToolSearch is registered and seeded allow) is what makes the strip
	// assertion meaningful — on an uncompressed fixture the infra tool may not
	// be registered at all, and stripping nothing proves nothing.
	assert.Truef(t, compressedNames["ToolSearch"],
		"the compressed context must carry the ToolSearch discovery door for %s", agentID)
	assert.Falsef(t, normalNames["ToolSearch"],
		"the normal context must not carry ToolSearch (infra tier is stripped) for %s", agentID)

	// Normal context = exactly the policy-allowed set minus infra names.
	for name := range allowed {
		if tools.ToolManifestTier(name) == tools.ManifestInfra {
			continue
		}
		assert.Truef(t, normalNames[name],
			"policy-allowed tool %q must be in the NORMAL context for %s", name, agentID)
	}
	for name := range normalNames {
		assert.Truef(t, allowed[name],
			"the NORMAL context surfaced %q for %s, which policy did not allow", name, agentID)
	}

	// Compressed context never surfaces a name outside the allowed set (the
	// only legal addition is the infra discovery door itself).
	for name := range compressedNames {
		assert.Truef(t, allowed[name] || name == "ToolSearch",
			"the COMPRESSED context surfaced %q for %s, which policy did not allow", name, agentID)
	}

	// Denied tools remain unavailable: pick the first registered-but-denied
	// name from the real registry (robust to catalog churn) and assert it is
	// reachable through neither defs surface nor the manifest note preview.
	var deniedExample string
	for _, tool := range inst.Tools.GetAll() {
		if !allowed[tool.Name()] {
			deniedExample = tool.Name()
			break
		}
	}
	require.NotEmptyf(t, deniedExample,
		"role %q allows every tool it registers — the denied leg would be vacuous", agentID)
	assert.Falsef(t, normalNames[deniedExample],
		"denied tool %q must not surface in the NORMAL context for %s", deniedExample, agentID)
	assert.Falsef(t, compressedNames[deniedExample],
		"denied tool %q must not surface in the COMPRESSED context for %s", deniedExample, agentID)
	assert.NotContainsf(t, al.buildToolManifestNote(ts, pf), "  - "+deniedExample,
		"denied tool %q must not preview in the compressed manifest note for %s", deniedExample, agentID)
}

// TestADR090_Acceptance_UpfrontSetExposedInBothContexts runs the TEST007
// upfront-set contract across the full nine-role roster through real loop
// instances. tool_manifest_test.go already covers the tier mechanics for the
// pre-ADR-090 four-core roster; this adds the current roster, the normal-path
// producer expression, the deny-by-default spot check per role, and an
// uncompressed-fixture cross-check for one role.
func TestADR090_Acceptance_UpfrontSetExposedInBothContexts(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	for _, id := range adr090RosterIDs {
		t.Run(id, func(t *testing.T) {
			adr090AssertContextsForRole(t, al, id)
		})
	}

	// Cross-check on the UNCOMPRESSED fixture (the actual normal-mode boot
	// shape): the normal-path producer over its filtered set equals that set
	// minus infra names — set equality in both directions.
	cfgOff := newUncompressedCfg(t)
	alOff := mustNewAgentLoop(t, cfgOff, bus.NewMessageBus(), &mockProvider{})
	defer alOff.Close()
	mia, ok := alOff.registry.GetAgent("mia")
	require.True(t, ok, "mia must be registered on the uncompressed fixture")
	pfOff, _ := tools.FilterToolsByPolicy(mia.Tools.GetAll(), mia.AgentType, mia.LoadToolPolicy())
	require.NotEmpty(t, pfOff)
	namesOff := adr090DefNames(tools.ToolsToProviderDefs(stripInfraToolDefs(pfOff)))
	allowedOff := make(map[string]bool, len(pfOff))
	for _, tool := range pfOff {
		allowedOff[tool.Name()] = true
	}
	for name := range allowedOff {
		if tools.ToolManifestTier(name) == tools.ManifestInfra {
			continue
		}
		assert.Truef(t, namesOff[name],
			"uncompressed fixture: policy-allowed tool %q must be in the normal context", name)
	}
	for name := range namesOff {
		assert.Truef(t, allowedOff[name],
			"uncompressed fixture: the normal context surfaced %q outside the policy-allowed set", name)
	}
}

// TestADR090_Acceptance_KnowledgeDeferredRequiresDiscovery proves the
// permitted-deferred half of TEST007 through the real discovery path for the
// founder's knowledge deltas (ADR-090 review, 2026-09-17): Jim knowledge reads
// allow, knowledge writes ask. The verdict matrix itself is pinned in
// pkg/gateway (knowledge_effective_posture_test.go); the requires below only
// establish the precondition the legs depend on.
//
// Both verdicts must behave identically at DISCOVERY time (the tool loads and
// becomes callable); `ask` governs the call, not the load. And the load must
// be REQUIRED: nothing previewed, nothing callable before ToolSearch runs,
// including in the manifest note — the knowledge family is search-only lazy
// (pkg/tools::ToolManifestTier), so it costs zero tokens until discovered.
func TestADR090_Acceptance_KnowledgeDeferredRequiresDiscovery(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jim, ok := al.registry.GetAgent("jim")
	require.True(t, ok)
	pf, verdicts := tools.FilterToolsByPolicy(jim.Tools.GetAll(), jim.AgentType, jim.LoadToolPolicy())
	require.Equal(t, "allow", verdicts["knowledge_read"],
		"precondition: Jim knowledge reads must resolve allow (founder delta)")
	require.Equal(t, "ask", verdicts["knowledge_edit"],
		"precondition: Jim knowledge writes must resolve ask (founder delta)")

	require.Equal(t, tools.ManifestLazy, tools.ToolManifestTier("knowledge_read"))
	require.Equal(t, tools.ManifestSearchOnly, tools.ToolManifestVisibility("knowledge_read"))
	require.Equal(t, tools.ManifestLazy, tools.ToolManifestTier("knowledge_edit"))
	require.Equal(t, tools.ManifestSearchOnly, tools.ToolManifestVisibility("knowledge_edit"))

	sess := "sess-adr090-knowledge-deferred"
	ts := fakeTurnState(jim, sess)
	preNames := adr090DefNames(al.buildCompressedToolDefs(ts, pf))
	note := al.buildToolManifestNote(ts, pf)
	for _, name := range []string{"knowledge_read", "knowledge_edit"} {
		assert.Falsef(t, preNames[name],
			"deferred tool %q must NOT be callable in the compressed context before discovery", name)
		assert.NotContainsf(t, note, "  - "+name,
			"search-only tool %q must not preview in the manifest note", name)
	}

	// Discovery through the real load path: allow AND ask both load.
	for _, name := range []string{"knowledge_read", "knowledge_edit"} {
		res := adr090Load(t, al, "jim", sess, name)
		require.Falsef(t, res.IsError,
			"loading %q through ToolSearch must succeed under verdict %q: %s", name, verdicts[name], res.ForLLM)
	}

	// Post-discovery, both appear in the SAME session's compressed defs —
	// and a different session still sees neither (ADR-071 D3 §4.6: loaded
	// tools are bucketed per (agent, session)).
	postNames := adr090DefNames(al.buildCompressedToolDefs(ts, pf))
	assert.True(t, postNames["knowledge_read"],
		"knowledge_read must be callable in the compressed context after discovery")
	assert.True(t, postNames["knowledge_edit"],
		"knowledge_edit must be callable in the compressed context after discovery")
	otherNames := adr090DefNames(al.buildCompressedToolDefs(fakeTurnState(jim, sess+"-other"), pf))
	assert.False(t, otherNames["knowledge_read"],
		"a different session must not inherit the discovery")
	assert.False(t, otherNames["knowledge_edit"],
		"a different session must not inherit the discovery")
}

// TestADR090_Acceptance_DeniedToolsUnreachableThroughDiscovery proves the
// denied half of TEST007: policy denials hold at the only remaining door.
// FilterToolsByPolicy already drops denied names from every defs surface
// (asserted per-role in the both-contexts test); ToolSearch is the one path
// that could still hand a denied name over, so each leg here loads a denied
// tool through the real canLoad gate and requires the denial to fire.
func TestADR090_Acceptance_DeniedToolsUnreachableThroughDiscovery(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	deniedThroughDiscovery := func(t *testing.T, agentID, toolName string) *tools.ToolResult {
		t.Helper()
		inst, ok := al.registry.GetAgent(agentID)
		require.Truef(t, ok, "role %q must be registered", agentID)
		_, registered := inst.Tools.Get(toolName)
		require.Truef(t, registered,
			"%q must be registered for %s — the leg is vacuous otherwise (an unregistered name is denied by absence, not by policy)", toolName, agentID)
		_, verdicts := tools.FilterToolsByPolicy(inst.Tools.GetAll(), inst.AgentType, inst.LoadToolPolicy())
		_, survived := verdicts[toolName]
		assert.Falsef(t, survived,
			"precondition: the filter must DROP %q for %s (deny does not survive)", toolName, agentID)
		res := adr090Load(t, al, agentID, "sess-adr090-deny-"+agentID, toolName)
		require.Truef(t, res.IsError, "loading denied tool %q for %s must fail", toolName, agentID)
		assert.Contains(t, res.ForLLM, "denied by this agent's policy",
			"the failure must name the policy denial")
		return res
	}

	t.Run("judge denies knowledge reads", func(t *testing.T) {
		deniedThroughDiscovery(t, "judge", "knowledge_read")
	})
	t.Run("judge denies a full-tier tool (bash)", func(t *testing.T) {
		deniedThroughDiscovery(t, "judge", "bash")
	})
	t.Run("ava denies knowledge writes", func(t *testing.T) {
		deniedThroughDiscovery(t, "ava", "knowledge_edit")
	})
	t.Run("plansupervisor denies knowledge reads", func(t *testing.T) {
		deniedThroughDiscovery(t, "plansupervisor", "knowledge_read")
	})

	// Founder delta (ADR-090 review, 2026-09-17): Admin knowledge reads Allow.
	// The same door that rejects the denials above must accept this one.
	t.Run("admin knowledge reads load cleanly", func(t *testing.T) {
		admin, ok := al.registry.GetAgent("admin")
		require.True(t, ok, "admin must be registered")
		_, verdicts := tools.FilterToolsByPolicy(admin.Tools.GetAll(), admin.AgentType, admin.LoadToolPolicy())
		require.Equal(t, "allow", verdicts["knowledge_read"],
			"precondition: admin knowledge reads must resolve allow (founder delta)")
		res := adr090Load(t, al, "admin", "sess-adr090-admin-read", "knowledge_read")
		assert.Falsef(t, res.IsError,
			"loading admin's allowed knowledge_read must succeed: %s", res.ForLLM)
	})
}
