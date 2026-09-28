// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// Tests for the agent-picker-freshness fix (#1009)'s sysagent-side half:
// create_agent (an agent creating another agent mid-conversation) persists
// straight to the entity store and never reaches the gateway's REST
// createAgent handler, so it needs its OWN notification hook
// (systools.Deps.NotifyAgentCreated) to reach the same agent_created WS
// broadcast the REST path fires — otherwise the tab watching the
// conversation that ran create_agent would never see the new agent appear
// in its Agent Picker until the query's 30s staleTime elapsed or a reload
// happened.
//
// These tests reuse buildSysagentFastUpsertTestLoop /
// newSysagentFastUpsertDeps from agent_fast_upsert_test.go (same package,
// same file set) so the harness composes with the fast-upsert path rather
// than duplicating a second AgentLoop/Deps builder.

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// TestAgentCreate_NotifiesAgentCreated proves create_agent invokes
// Deps.NotifyAgentCreated exactly once, with the newly created agent's ID,
// after the agent is durably persisted.
func TestAgentCreate_NotifiesAgentCreated(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	al := buildSysagentFastUpsertTestLoop(t, home, provider, nil)

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)

	var notifiedIDs []string
	deps.NotifyAgentCreated = func(agentID string) {
		notifiedIDs = append(notifiedIDs, agentID)
	}

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Notify Create Agent",
		"description": "proves create_agent invokes NotifyAgentCreated",
		"soul":        "You are a test agent.",
		"model":       "test-model",
		"color":       "#22C55E",
		"icon":        "robot",
	})
	if result.IsError {
		t.Fatalf("create_agent failed: %s", result.ForLLM)
	}
	created := parseSuccess(t, result.ForLLM)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create_agent response missing id: %+v", created)
	}

	if len(notifiedIDs) != 1 {
		t.Fatalf("NotifyAgentCreated must be called exactly once per landed create; got %v", notifiedIDs)
	}
	if notifiedIDs[0] != id {
		t.Fatalf("NotifyAgentCreated called with agent id %q, want %q (the just-created agent)", notifiedIDs[0], id)
	}
}

// TestAgentCreate_ValidationFailure_DoesNotNotifyAgentCreated proves a
// request that never persists an agent (missing required field) does not
// call NotifyAgentCreated — mirroring the REST path's "never on a 4xx"
// rule: the hook means "a new agent now exists", and a refused create
// created nothing.
func TestAgentCreate_ValidationFailure_DoesNotNotifyAgentCreated(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	al := buildSysagentFastUpsertTestLoop(t, home, provider, nil)

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)

	var notifyCalls atomic.Int32
	deps.NotifyAgentCreated = func(string) { notifyCalls.Add(1) }

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		// name deliberately omitted — validate() rejects this before persistAndJoin runs.
		"description": "missing name must error, never persist",
		"soul":        "You are a test agent.",
		"model":       "test-model",
		"color":       "#22C55E",
		"icon":        "robot",
	})
	if !result.IsError {
		t.Fatalf("create_agent with no name must fail validation, got success: %s", result.ForLLM)
	}
	if got := notifyCalls.Load(); got != 0 {
		t.Fatalf("a request that never persisted an agent must not call NotifyAgentCreated; got %d calls", got)
	}
}

// TestAgentCreate_NotifyFiresOnlyAfterAgentIsListable is the ordering-fix
// regression proof for #1009's reappearance: it asserts NotifyAgentCreated
// fires only AFTER publishAgentActivation has actually landed the new agent
// in the live, in-memory config/registry that pkg/gateway/rest_agents.go::
// listAgents (GET /api/v1/agents) reads — not merely after persistAndJoin's
// durable disk write. It checks this synchronously, from INSIDE the notify
// callback itself: al.GetConfig().Agents.List must already contain the new
// agent's ID at the exact moment the callback runs. Against the pre-fix call
// order (notify right after persistAndJoin, before publishAndRespond /
// publishAgentActivation), this would fail — see this task's report for the
// red run captured by temporarily reverting the ordering change.
func TestAgentCreate_NotifyFiresOnlyAfterAgentIsListable(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	al := buildSysagentFastUpsertTestLoop(t, home, provider, nil)

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)

	var (
		notifyCalls     int
		listedAtNotify  bool
		notifiedAgentID string
	)
	deps.NotifyAgentCreated = func(agentID string) {
		notifyCalls++
		notifiedAgentID = agentID
		for _, a := range al.GetConfig().Agents.List {
			if a.ID == agentID {
				listedAtNotify = true
				break
			}
		}
	}

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Ordering Proof Agent",
		"description": "proves NotifyAgentCreated fires only once the agent is live-listable",
		"soul":        "You are a test agent.",
		"model":       "test-model",
		"color":       "#22C55E",
		"icon":        "robot",
	})
	if result.IsError {
		t.Fatalf("create_agent failed: %s", result.ForLLM)
	}
	created := parseSuccess(t, result.ForLLM)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create_agent response missing id: %+v", created)
	}

	if notifyCalls != 1 {
		t.Fatalf("NotifyAgentCreated must be called exactly once per landed create; got %d calls", notifyCalls)
	}
	if notifiedAgentID != id {
		t.Fatalf("NotifyAgentCreated called with agent id %q, want %q", notifiedAgentID, id)
	}
	if !listedAtNotify {
		t.Fatalf("NotifyAgentCreated fired BEFORE the agent was live-listable: " +
			"al.GetConfig().Agents.List did not yet contain the new agent's ID at notify time — " +
			"this is the #1009 ordering race (notify racing publishAgentActivation)")
	}
}

// TestAgentCreate_InitAgentHomeFailure_DoesNotNotifyAgentCreated proves
// NotifyAgentCreated does not fire when persistAndJoin's InitAgentHome step
// fails AFTER agentstore.CreateState has already durably persisted the
// entity — a LATER failure gate than
// TestAgentCreate_ValidationFailure_DoesNotNotifyAgentCreated's earliest
// field-validation rejection. This closes the gap that test leaves open: a
// mutation that hoisted the notify call to fire right after persistAndJoin's
// CreateState succeeds (the #1009 bug's exact original position, before this
// fix moved the call into publishAndRespond gated on publishAgentActivation)
// would still pass the validation-failure test, because that test's request
// never reaches persistAndJoin at all. This test does reach it, and fails at
// a step strictly after CreateState.
func TestAgentCreate_InitAgentHomeFailure_DoesNotNotifyAgentCreated(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	al := buildSysagentFastUpsertTestLoop(t, home, provider, nil)

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)

	var notifyCalls atomic.Int32
	deps.NotifyAgentCreated = func(string) { notifyCalls.Add(1) }

	// Pre-create a REGULAR FILE at the exact path datamodel.InitAgentHome
	// will try to os.MkdirAll a subdirectory under
	// (home/agents/<toSlug(name)>) — MkdirAll on a path whose parent already
	// exists as a non-directory file fails deterministically and portably
	// (no reliance on permission bits, which behave inconsistently as root
	// or across platforms). toSlug("Init Home Fail Agent") deterministically
	// yields "init-home-fail-agent" (lowercase, spaces to hyphens).
	const agentID = "init-home-fail-agent"
	if err := os.MkdirAll(filepath.Join(home, "agents"), 0o755); err != nil {
		t.Fatalf("setup: mkdir agents dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "agents", agentID), []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: write colliding file: %v", err)
	}

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Init Home Fail Agent",
		"description": "proves an InitAgentHome failure after entity save never notifies",
		"soul":        "You are a test agent.",
		"model":       "test-model",
		"color":       "#22C55E",
		"icon":        "robot",
	})
	if !result.IsError {
		t.Fatalf("create_agent must fail when InitAgentHome cannot create the agent's workspace dir "+
			"(collision at %s), got success: %s", filepath.Join(home, "agents", agentID), result.ForLLM)
	}
	if got := notifyCalls.Load(); got != 0 {
		t.Fatalf("a persistAndJoin failure AFTER the entity is already durable must not call "+
			"NotifyAgentCreated (the agent is not yet listable); got %d calls", got)
	}
}

// TestAgentCreate_NilNotifyAgentCreatedIsSafe proves create_agent survives a
// nil Deps.NotifyAgentCreated (the field's documented "nil in tests or when
// not wired" contract) rather than nil-dereferencing.
func TestAgentCreate_NilNotifyAgentCreatedIsSafe(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	al := buildSysagentFastUpsertTestLoop(t, home, provider, nil)

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)
	// deps.NotifyAgentCreated left nil deliberately.

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Nil Notify Agent",
		"description": "proves a nil NotifyAgentCreated never panics",
		"soul":        "You are a test agent.",
		"model":       "test-model",
		"color":       "#22C55E",
		"icon":        "robot",
	})
	if result.IsError {
		t.Fatalf("create_agent failed: %s", result.ForLLM)
	}
}
