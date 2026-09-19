// Omnipus — live execution floor for non-deniable ToolSearch discovery.
//
// Parent verification 2026-09-18 (discovery-design-verification.log): schema
// and the infra snapshot included ToolSearch after ensureInfraToolsExecutable,
// but resolveToolPolicyAtExec returned deny because resolveSingleToolPolicy
// re-ran FilterToolsByPolicy against the operator Deny. User design: discovery
// cannot be denied. This file pins the live recheck, not the schema floor.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func discoveryExecLoop(t *testing.T, mutate func(*config.Config)) *AgentLoop {
	t.Helper()
	cfg := newCompressedCfg(t)
	if mutate != nil {
		mutate(cfg)
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })
	return al
}

func discoveryFilterSnapshot(t *testing.T, inst *AgentInstance) map[string]string {
	t.Helper()
	filtered, policy := tools.FilterToolsByPolicy(inst.Tools.GetAll(), inst.AgentType, inst.LoadToolPolicy())
	ensureInfraToolsExecutable(inst.Tools, filtered, policy)
	return policy
}

// TestADR090_DiscoveryExec_DenyAndAskCannotBlock is the actual failing
// boundary: filter-time snapshot offers ToolSearch, live policy says deny or
// ask, exec must still allow.
func TestADR090_DiscoveryExec_DenyAndAskCannotBlock(t *testing.T) {
	for _, verdict := range []config.ToolPolicy{config.ToolPolicyDeny, config.ToolPolicyAsk} {
		t.Run(string(verdict), func(t *testing.T) {
			al := discoveryExecLoop(t, func(cfg *config.Config) {
				discoverySetToolSearchVerdict(t, cfg, string(coreagent.IDWorker), verdict)
			})
			inst, ok := al.registry.GetAgent(string(coreagent.IDWorker))
			require.True(t, ok)
			pmap := discoveryFilterSnapshot(t, inst)
			require.Equal(t, "allow", pmap["ToolSearch"],
				"producer snapshot must offer the discovery door")
			ts := fakeTurnState(inst, "sess-discovery-exec-"+string(verdict))
			assert.Equal(t, "allow", al.resolveSingleToolPolicy(ts, "ToolSearch"),
				"live recheck must not honour operator %s on ToolSearch", verdict)
			assert.Equal(t, "allow", al.resolveToolPolicyAtExec(ts, "ToolSearch", pmap),
				"exec must authorize ToolSearch despite operator %s", verdict)
		})
	}
}

// TestADR090_DiscoveryExec_GlobalDenyCannotBlock is the same live recheck
// against a global-ceiling Deny.
func TestADR090_DiscoveryExec_GlobalDenyCannotBlock(t *testing.T) {
	al := discoveryExecLoop(t, discoveryDenyToolSearchGlobally)
	inst, ok := al.registry.GetAgent(string(coreagent.IDJim))
	require.True(t, ok)
	pmap := discoveryFilterSnapshot(t, inst)
	require.Equal(t, "allow", pmap["ToolSearch"])
	ts := fakeTurnState(inst, "sess-discovery-exec-global")
	assert.Equal(t, "allow", al.resolveToolPolicyAtExec(ts, "ToolSearch", pmap))
}

// TestADR090_DiscoveryExec_DeletedAgentAndUnregisteredStillDeny keeps the
// instance/registration checks. Non-deniable discovery is not a ghost door.
func TestADR090_DiscoveryExec_DeletedAgentAndUnregisteredStillDeny(t *testing.T) {
	al := discoveryExecLoop(t, nil)
	inst, ok := al.registry.GetAgent(string(coreagent.IDWorker))
	require.True(t, ok)
	pmap := map[string]string{"ToolSearch": "allow"}
	ts := fakeTurnState(inst, "sess-discovery-exec-gone")

	t.Run("deleted agent", func(t *testing.T) {
		require.True(t, al.registry.RemoveAgent(string(coreagent.IDWorker)))
		assert.Equal(t, "deny", al.resolveSingleToolPolicy(ts, "ToolSearch"))
		assert.Equal(t, "deny", al.resolveToolPolicyAtExec(ts, "ToolSearch", pmap),
			"a deleted agent must not keep discovery execution authority")
	})

	t.Run("unregistered ToolSearch", func(t *testing.T) {
		empty := &AgentInstance{ID: "ghost-discovery", AgentType: "core", Tools: tools.NewToolRegistry()}
		al.registry.mu.Lock()
		al.registry.agents["ghost-discovery"] = empty
		al.registry.mu.Unlock()
		ghostTS := fakeTurnState(empty, "sess-discovery-exec-unreg")
		assert.Equal(t, "deny", al.resolveSingleToolPolicy(ghostTS, "ToolSearch"))
		assert.Equal(t, "deny", al.resolveToolPolicyAtExec(ghostTS, "ToolSearch", pmap))
	})

	t.Run("absent from filter-time snapshot", func(t *testing.T) {
		al2 := discoveryExecLoop(t, nil)
		inst2, ok := al2.registry.GetAgent(string(coreagent.IDJim))
		require.True(t, ok)
		ts2 := fakeTurnState(inst2, "sess-discovery-exec-not-offered")
		assert.Equal(t, "deny", al2.resolveToolPolicyAtExec(ts2, "ToolSearch", map[string]string{}),
			"filter-time offered applicability: not in the snapshot → deny")
	})
}

// TestADR090_DiscoveryExec_DeniedTargetAndMidturnRevoke still honours target
// permissions. The discovery floor must not leak onto ordinary tools.
func TestADR090_DiscoveryExec_DeniedTargetAndMidturnRevoke(t *testing.T) {
	const target = "knowledge_edit"
	al := discoveryExecLoop(t, nil)
	ava, ok := al.registry.GetAgent(string(coreagent.IDAva))
	require.True(t, ok)
	_, registered := ava.Tools.Get(target)
	require.True(t, registered)
	pmap := discoveryFilterSnapshot(t, ava)
	ts := fakeTurnState(ava, "sess-discovery-exec-target")

	assert.Equal(t, "allow", al.resolveToolPolicyAtExec(ts, "ToolSearch", pmap))
	assert.Equal(t, "deny", al.resolveToolPolicyAtExec(ts, target, pmap),
		"seeded deny of a target tool must still deny at exec")

	jim, ok := al.registry.GetAgent(string(coreagent.IDJim))
	require.True(t, ok)
	jimMap := discoveryFilterSnapshot(t, jim)
	require.Contains(t, []string{"allow", "ask"}, jimMap["knowledge_read"])
	jimTS := fakeTurnState(jim, "sess-discovery-exec-revoke")
	before := al.resolveToolPolicyAtExec(jimTS, "knowledge_read", jimMap)
	require.NotEqual(t, "deny", before, "precondition: jim knowledge_read is permitted at filter time")

	revoked := jim.LoadToolPolicy()
	require.NotNil(t, revoked)
	copyCfg := *revoked
	copyCfg.Policies = make(map[string]config.ToolPolicy, len(revoked.Policies)+1)
	for k, v := range revoked.Policies {
		copyCfg.Policies[k] = v
	}
	copyCfg.Policies["knowledge_read"] = config.ToolPolicyDeny
	jim.StoreToolPolicy(&copyCfg)
	assert.Equal(t, "deny", al.resolveToolPolicyAtExec(jimTS, "knowledge_read", jimMap),
		"mid-turn revoke of a target tool must still deny at exec")
	assert.Equal(t, "allow", al.resolveToolPolicyAtExec(jimTS, "ToolSearch", jimMap),
		"revoking a target must not take down the discovery door")
}

// Discovery must use the current registry instance when a turn retains an old one.
func TestADR090_DiscoveryExec_ReplacedInstanceWithoutDiscoveryDenies(t *testing.T) {
	al := discoveryExecLoop(t, nil)
	inst, ok := al.registry.GetAgent(string(coreagent.IDWorker))
	require.True(t, ok)
	ts := fakeTurnState(inst, "sess-discovery-replaced")
	replacement := &AgentInstance{ID: inst.ID, AgentType: inst.AgentType, Tools: tools.NewToolRegistry()}
	al.registry.mu.Lock()
	al.registry.agents[inst.ID] = replacement
	al.registry.mu.Unlock()
	t.Cleanup(func() { al.registry.mu.Lock(); al.registry.agents[inst.ID] = inst; al.registry.mu.Unlock() })
	assert.Equal(t, "deny", al.resolveToolPolicyAtExec(ts, "ToolSearch", map[string]string{"ToolSearch": "allow"}), "an old turn must not retain a tool removed from the current instance")
}
