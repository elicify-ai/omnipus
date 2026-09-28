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
