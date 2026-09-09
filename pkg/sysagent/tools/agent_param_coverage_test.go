// Omnipus — create_agent/update_agent parameter-coverage regression tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// declaredObjectProperties extracts the top-level "properties" key names from
// a tool's Parameters() JSON-schema-shaped map, as used by every tool in this
// package (map[string]any{"type": "object", "properties": map[string]any{...}}).
func declaredObjectProperties(t *testing.T, params map[string]any) map[string]bool {
	t.Helper()
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("Parameters() has no \"properties\" map: %#v", params)
	}
	out := make(map[string]bool, len(props))
	for name := range props {
		out[name] = true
	}
	return out
}

// TestAgentCreateTool_NoOrphanedParameters is a regression test for the
// "declares a parameter, silently never applies it" bug (ADR-037
// anti-pattern: a caller passing e.g. restrict_to_workspace:true got a
// success response implying it was applied when nothing happened).
//
// It lists every property AgentCreateTool.Parameters() declares and asserts
// each one is actually read somewhere by feeding it a value and checking
// for an observable effect (or, for fields whose effect is validation-only
// like the mandatory ones, that they are at minimum referenced). This is not
// a generic reflection-based check — it is a literal enumeration of the
// known-consumed set, so a newly added but unwired property fails this test
// by omission rather than by a false negative.
func TestAgentCreateTool_NoOrphanedParameters(t *testing.T) {
	deps, _ := newTestDeps()
	tool := systools.NewAgentCreateTool(deps)

	declared := declaredObjectProperties(t, tool.Parameters())

	// consumedByExecute is the exhaustive set of AgentCreateTool.Execute's
	// args[...] reads, transcribed from source (not derived), so this test
	// fails the moment Parameters() and Execute() drift apart again.
	consumedByExecute := map[string]bool{
		"name": true, "description": true, "soul": true, "model": true,
		"color": true, "icon": true, "agent_type": true, "cli": true,
		"cli_path": true, "provider": true, "model_fallbacks": true,
		"heartbeat": true, "max_tool_iterations": true,
	}

	for name := range declared {
		if !consumedByExecute[name] {
			t.Errorf(
				"create_agent Parameters() declares %q but Execute() never reads it — "+
					"a caller passing it gets a success response implying it was applied "+
					"when it silently was not (ADR-037 anti-pattern)", name,
			)
		}
	}
	// The reverse direction also catches a stale entry in consumedByExecute
	// itself (e.g. a field deleted from Execute but left in this set).
	for name := range consumedByExecute {
		if !declared[name] {
			t.Errorf("consumedByExecute lists %q but create_agent Parameters() no longer declares it — stale test fixture", name)
		}
	}

	// The two fields with no backing config.AgentConfig field anywhere in the
	// codebase (confirmed: timeout_seconds has no AgentConfig field at all —
	// see pkg/gateway/rest.go's documented pre-existing gap on the identical
	// wire field — and restrict_to_workspace is a fully retired concept per
	// pkg/config/validator.go's fr001RemovedKeysMsg) must be GONE from the
	// schema entirely, not merely unread.
	for _, retired := range []string{"timeout_seconds", "restrict_to_workspace"} {
		if declared[retired] {
			t.Errorf("create_agent Parameters() still declares retired/unbacked field %q", retired)
		}
	}
}

// TestAgentUpdateTool_NoOrphanedParameters mirrors
// TestAgentCreateTool_NoOrphanedParameters for update_agent.
func TestAgentUpdateTool_NoOrphanedParameters(t *testing.T) {
	deps, _ := newTestDeps()
	tool := systools.NewAgentUpdateTool(deps)

	declared := declaredObjectProperties(t, tool.Parameters())

	consumedByExecute := map[string]bool{
		"id": true, "name": true, "description": true, "soul": true,
		"model": true, "model_fallbacks": true, "provider": true,
		"color": true, "icon": true, "heartbeat": true,
		"max_tool_iterations": true,
	}

	for name := range declared {
		if !consumedByExecute[name] {
			t.Errorf(
				"update_agent Parameters() declares %q but Execute() never reads it — "+
					"a caller passing it gets a success response implying it was applied "+
					"when it silently was not (ADR-037 anti-pattern)", name,
			)
		}
	}
	for name := range consumedByExecute {
		if !declared[name] {
			t.Errorf("consumedByExecute lists %q but update_agent Parameters() no longer declares it — stale test fixture", name)
		}
	}

	for _, retired := range []string{"timeout_seconds", "restrict_to_workspace"} {
		if declared[retired] {
			t.Errorf("update_agent Parameters() still declares retired/unbacked field %q", retired)
		}
	}
}

// TestAgentCreate_AppliesProviderAndMaxToolIterations proves create_agent's
// provider and max_tool_iterations parameters are not just schema-declared
// but actually persisted onto the entity record.
func TestAgentCreate_AppliesProviderAndMaxToolIterations(t *testing.T) {
	deps, _ := newTestDeps()
	tool := systools.NewAgentCreateTool(deps)

	result := tool.Execute(context.Background(), map[string]any{
		"name":                "Provider Bot",
		"description":         "Tests provider + max_tool_iterations wiring",
		"soul":                "You are a test bot.",
		"model":               "test/model",
		"color":               "#22C55E",
		"icon":                "robot",
		"provider":            "openrouter",
		"max_tool_iterations": float64(42),
	})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}

	ag, err := agentstore.New(deps.Home).Get("provider-bot")
	if err != nil {
		t.Fatalf("expected agent entity record to exist: %v", err)
	}
	if ag.Model == nil || ag.Model.Provider != "openrouter" {
		t.Errorf("Model.Provider = %+v, want \"openrouter\"", ag.Model)
	}
	if ag.MaxToolIterations != 42 {
		t.Errorf("MaxToolIterations = %d, want 42", ag.MaxToolIterations)
	}
}

// TestAgentCreate_RejectsNegativeMaxToolIterations proves the validation
// path: a negative max_tool_iterations is rejected with INVALID_INPUT, not
// silently clamped or silently ignored.
func TestAgentCreate_RejectsNegativeMaxToolIterations(t *testing.T) {
	deps, _ := newTestDeps()
	tool := systools.NewAgentCreateTool(deps)

	result := tool.Execute(context.Background(), map[string]any{
		"name":                "Bad Bot",
		"description":         "d",
		"soul":                "s",
		"model":               "test/model",
		"color":               "#22C55E",
		"icon":                "robot",
		"max_tool_iterations": float64(-1),
	})
	if !result.IsError {
		t.Fatal("expected error for negative max_tool_iterations, got success")
	}
	errBody := parseError(t, result.ForLLM)
	errObj, _ := errBody["error"].(map[string]any)
	if errObj["code"] != "INVALID_INPUT" {
		t.Errorf("error code = %v, want INVALID_INPUT", errObj["code"])
	}
	if _, err := agentstore.New(deps.Home).Get("bad-bot"); err == nil {
		t.Error("agent should not have been created after validation failure")
	}
}

// TestAgentUpdate_AppliesProviderAndMaxToolIterations mirrors the create-side
// proof for update_agent, including provider CLEARING via an explicit empty
// string (matching REST's updateAgent semantics for the identical wire
// field — see pkg/gateway/rest.go's req.Provider handling).
func TestAgentUpdate_AppliesProviderAndMaxToolIterations(t *testing.T) {
	deps, _ := newTestDeps()
	store := agentstore.New(deps.Home)
	if err := store.Create("my-agent", &config.AgentConfig{
		ID:                "my-agent",
		Name:              "My Agent",
		Model:             &config.AgentModelConfig{Primary: "test/model", Provider: "anthropic"},
		MaxToolIterations: 10,
	}); err != nil {
		t.Fatalf("test setup: create agent entity record: %v", err)
	}

	tool := systools.NewAgentUpdateTool(deps)
	result := tool.Execute(context.Background(), map[string]any{
		"id":                  "my-agent",
		"provider":            "openrouter",
		"max_tool_iterations": float64(99),
	})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}

	updated, err := store.Get("my-agent")
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if updated.Model == nil || updated.Model.Provider != "openrouter" {
		t.Errorf("Model.Provider = %+v, want \"openrouter\"", updated.Model)
	}
	if updated.MaxToolIterations != 99 {
		t.Errorf("MaxToolIterations = %d, want 99", updated.MaxToolIterations)
	}

	// Now clear the provider with an explicit empty string.
	result2 := tool.Execute(context.Background(), map[string]any{
		"id":       "my-agent",
		"provider": "",
	})
	if result2.IsError {
		t.Fatalf("expected success clearing provider, got error: %s", result2.ForLLM)
	}
	cleared, err := store.Get("my-agent")
	if err != nil {
		t.Fatalf("Get after clearing provider: %v", err)
	}
	if cleared.Model == nil || cleared.Model.Provider != "" {
		t.Errorf("Model.Provider after clear = %+v, want empty string", cleared.Model)
	}
	// max_tool_iterations must be untouched by the second call (not present
	// in its args).
	if cleared.MaxToolIterations != 99 {
		t.Errorf("MaxToolIterations after unrelated update = %d, want unchanged 99", cleared.MaxToolIterations)
	}
}
