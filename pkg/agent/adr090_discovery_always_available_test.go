// Omnipus — ADR-090 discovery invariant: ToolSearch is non-deniable
// infrastructure; target-tool permissions still apply.
//
// User correction 2026-09-18 (explicit, accepted): "tool discovery was
// actually intentionally not possible to deny that is intended design."
// ToolSearch remains offered and executable on compressed turns even when an
// operator sets Deny on it (global ceiling or per-agent). That does NOT grant
// denied target tools: previews, query matches, load, and execution still
// honour FilterToolsByPolicy for every non-infra name. Goal forcing is
// unchanged — a narrowed first-move request may still withhold ToolSearch
// (ADR-088 D3); that is session applicability, not an operator deny of
// discovery.
//
// These tests drive the parent producer path
// (loop_run_turn.go::prepareToolSurface): FilterToolsByPolicy, then
// ensureInfraToolsExecutable, then buildCompressedToolDefs /
// resolveToolPolicyAtExec / the real ToolSearch Execute (canLoad).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func discoverySetToolSearchVerdict(t *testing.T, cfg *config.Config, agentID string, verdict config.ToolPolicy) {
	t.Helper()
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		if ac.ID != agentID {
			continue
		}
		if ac.Tools == nil {
			ac.Tools = &config.AgentToolsCfg{}
		}
		if ac.Tools.Builtin.Policies == nil {
			ac.Tools.Builtin.Policies = make(map[string]config.ToolPolicy)
		}
		ac.Tools.Builtin.Policies["ToolSearch"] = verdict
		return
	}
	t.Fatalf("agent %q is not in cfg.Agents.List", agentID)
}

func discoveryDenyToolSearchGlobally(cfg *config.Config) {
	gp := make(map[string]string, len(cfg.Sandbox.ToolPolicies)+1)
	for k, v := range cfg.Sandbox.ToolPolicies {
		gp[k] = v
	}
	gp["ToolSearch"] = string(config.ToolPolicyDeny)
	cfg.Sandbox.ToolPolicies = gp
}

// discoveryPrepareSurface is the compressed-turn producer path: policy filter
// then the infra execution floor (ensureInfraToolsExecutable).
func discoveryPrepareSurface(inst *AgentInstance) ([]tools.Tool, map[string]string) {
	pf, pmap := tools.FilterToolsByPolicy(inst.Tools.GetAll(), inst.AgentType, inst.LoadToolPolicy())
	pf = ensureInfraToolsExecutable(inst.Tools, pf, pmap)
	return pf, pmap
}

func discoveryQuery(t *testing.T, al *AgentLoop, agentID, sessionID, query string) *tools.ToolResult {
	t.Helper()
	inst, ok := al.registry.GetAgent(agentID)
	require.True(t, ok)
	raw, ok := inst.Tools.Get("ToolSearch")
	require.True(t, ok)
	tt, ok := raw.(*tools.ToolsTool)
	require.True(t, ok)
	ctx := tools.WithAgentID(context.Background(), agentID)
	ctx = tools.WithTranscriptSessionID(ctx, sessionID)
	ctx = tools.WithSessionKey(ctx, sessionID)
	return tt.Execute(ctx, map[string]any{"query": query})
}

func discoveryAssertDoorForced(t *testing.T, al *AgentLoop, agentID string) {
	t.Helper()
	inst, ok := al.registry.GetAgent(agentID)
	require.Truef(t, ok, "role %q must be registered", agentID)
	_, registered := inst.Tools.Get("ToolSearch")
	require.Truef(t, registered, "ToolSearch must be registered for %q", agentID)

	pf, pmap := discoveryPrepareSurface(inst)
	require.Equal(t, "allow", pmap["ToolSearch"],
		"role %q: the infra floor must authorize ToolSearch even if operator policy said deny", agentID)
	require.Contains(t, toolNameSet(pf), "ToolSearch",
		"role %q: the infra floor must keep ToolSearch on the filtered surface", agentID)

	ts := fakeTurnState(inst, "sess-discovery-door-"+agentID)
	names := adr090DefNames(al.buildCompressedToolDefs(ts, pf))
	assert.Truef(t, names["ToolSearch"],
		"role %q: compressed defs must offer ToolSearch (non-deniable discovery door)", agentID)
	assert.Equalf(t, "allow", al.resolveToolPolicyAtExec(ts, "ToolSearch", pmap),
		"role %q: exec must authorize ToolSearch after the infra floor", agentID)
}

// TestADR090_DiscoveryInfrastructure_NotDeniablePerAgent pins the user
// decision: a per-agent ToolSearch Deny does not remove the discovery door.
func TestADR090_DiscoveryInfrastructure_NotDeniablePerAgent(t *testing.T) {
	cfg := newCompressedCfg(t)
	discoverySetToolSearchVerdict(t, cfg, string(coreagent.IDWorker), config.ToolPolicyDeny)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()
	discoveryAssertDoorForced(t, al, string(coreagent.IDWorker))
}

// TestADR090_DiscoveryInfrastructure_NotDeniableGlobally pins the same floor
// against a global-ceiling Deny (strictest-wins would otherwise bind every
// role whose own entry says allow).
func TestADR090_DiscoveryInfrastructure_NotDeniableGlobally(t *testing.T) {
	cfg := newCompressedCfg(t)
	discoveryDenyToolSearchGlobally(cfg)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()
	for _, agentID := range []string{string(coreagent.IDJim), string(coreagent.IDWorker)} {
		t.Run(agentID, func(t *testing.T) {
			discoveryAssertDoorForced(t, al, agentID)
		})
	}
}

// TestADR090_DiscoveryInfrastructure_DeniedTargetsStayDenied is the other
// half of the distinction: forcing the discovery door must not reveal, load,
// or execute a target tool the agent's policy denies. Ava's knowledge_edit
// is a seeded write-deny (ADR-090 knowledge matrix).
func TestADR090_DiscoveryInfrastructure_DeniedTargetsStayDenied(t *testing.T) {
	const deniedTarget = "knowledge_edit"
	cfg := newCompressedCfg(t)
	cfg.Tools.Manifest.PreviewAllLazy = true
	t.Cleanup(func() { tools.SetPreviewAllLazy(false) })
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	ava, ok := al.registry.GetAgent(string(coreagent.IDAva))
	require.True(t, ok)
	_, registered := ava.Tools.Get(deniedTarget)
	require.True(t, registered, "precondition: %s must be registered for ava", deniedTarget)

	pf, pmap := discoveryPrepareSurface(ava)
	require.Equal(t, "allow", pmap["ToolSearch"],
		"precondition: the discovery door is on the floor")
	_, targetSurvived := pmap[deniedTarget]
	require.False(t, targetSurvived,
		"precondition: ava's filter must drop %s (seeded write deny)", deniedTarget)
	assert.NotContains(t, toolNameSet(pf), deniedTarget,
		"denied target must not ride the infra floor onto the filtered slice")

	ts := fakeTurnState(ava, "sess-discovery-target-deny")
	names := adr090DefNames(al.buildCompressedToolDefs(ts, pf))
	assert.True(t, names["ToolSearch"], "discovery door stays offered")
	assert.False(t, names[deniedTarget],
		"compressed defs must not offer a policy-denied target")
	assert.Equal(t, "deny", al.resolveToolPolicyAtExec(ts, deniedTarget, pmap),
		"exec must deny a target the filter omitted")

	note := al.buildToolManifestNote(ts, pf)
	assert.NotContains(t, note, "  - "+deniedTarget,
		"preview must not list a policy-denied target")

	load := adr090Load(t, al, string(coreagent.IDAva), "sess-discovery-target-deny", deniedTarget)
	require.True(t, load.IsError, "loading a denied target through ToolSearch must fail")
	assert.Contains(t, load.ForLLM, "denied by this agent's policy",
		"the load failure must name the policy denial, got %q", load.ForLLM)

	query := discoveryQuery(t, al, string(coreagent.IDAva), "sess-discovery-target-deny-q", deniedTarget)
	assert.False(t, strings.Contains(query.ForLLM, `"name":"`+deniedTarget+`"`),
		"query matches must not name a policy-denied target; got %q", query.ForLLM)
}

// TestADR090_DiscoveryInfrastructure_AllowedTargetStillLoads proves the floor
// did not over-restrict: an allowed deferred tool still loads through the
// (non-deniable) door. Jim's knowledge_read is seeded allow.
func TestADR090_DiscoveryInfrastructure_AllowedTargetStillLoads(t *testing.T) {
	const allowedTarget = "knowledge_read"
	cfg := newCompressedCfg(t)
	discoverySetToolSearchVerdict(t, cfg, string(coreagent.IDJim), config.ToolPolicyDeny)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	jim, ok := al.registry.GetAgent(string(coreagent.IDJim))
	require.True(t, ok)
	pf, pmap := discoveryPrepareSurface(jim)
	require.Equal(t, "allow", pmap["ToolSearch"])
	require.Contains(t, []string{"allow", "ask"}, pmap[allowedTarget],
		"precondition: jim's %s must survive the filter", allowedTarget)

	ts := fakeTurnState(jim, "sess-discovery-target-allow")
	pre := adr090DefNames(al.buildCompressedToolDefs(ts, pf))
	require.False(t, pre[allowedTarget],
		"precondition: %s is deferred (not callable before discovery)", allowedTarget)

	res := adr090Load(t, al, string(coreagent.IDJim), "sess-discovery-target-allow", allowedTarget)
	require.False(t, res.IsError, "loading jim's allowed %s must succeed: %s", allowedTarget, res.ForLLM)
	post := adr090DefNames(al.buildCompressedToolDefs(ts, pf))
	assert.True(t, post[allowedTarget],
		"%s must be callable in this session after discovery", allowedTarget)
}
