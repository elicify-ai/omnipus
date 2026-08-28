// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/routing"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// ---------------------------------------------------------------------------
// Issue #571 (sysagent half): system.agent.create / system.agent.update — an
// agent creating or updating another agent — must publish through
// AgentLoop.UpsertAgentFast, the SAME fast path pkg/gateway/rest.go's
// fastAgentUpsert already uses for the REST create/update handlers, instead
// of triggering a full config reload (which restarts channels, cron,
// schedulers, and the plan engine on every call — up to ~60s under load, the
// exact mechanism that severed POST /api/v1/agents requests under CI load
// per pkg/gateway/agent_fast_upsert_no_restart_test.go's own "property not
// mechanism" rationale).
//
// These tests hold the sysagent tools to the same bar: a test asserting only
// "the agent is in the map" passes against a bare full reload too (a full
// reload eventually publishes a working registry). Instead:
//  1. The full-reload trigger itself must be invoked ZERO times for a plain
//     create/update (no concurrent reload in flight).
//  2. The agent must be genuinely resolvable afterward — not merely present
//     in a map — via AgentRegistry.GetAgent AND ResolveRoute.
//  3. delete_agent is asserted to KEEP using the full reload path exactly
//     once per call — the deliberate decision documented in
//     AgentDeleteTool.Execute, proven here as a regression guard rather than
//     left as an unverified claim.
// ---------------------------------------------------------------------------

// buildSysagentFastUpsertTestLoop builds a real *agent.AgentLoop rooted at a
// fresh, hermetic home dir. Mirrors buildFastUpsertTestLoop in
// pkg/agent/registry_fast_upsert_test.go and the harness
// TestAgentDelete_ImmediatelyUnroutableAndUnlisted_NoRestart already uses in
// this package, so create_agent/update_agent/delete_agent's real Deps wiring
// (ReloadFunc + UpsertAgentFastFunc) exercises the real registry-publish
// path, not a bare stub.
func buildSysagentFastUpsertTestLoop(
	t *testing.T, home string, provider *reloadTestProvider, bindings []config.AgentBinding,
) *agent.AgentLoop {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              filepath.Join(home, "workspace"),
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         8192,
				MaxToolIterations: 10,
			},
		},
		Bindings: bindings,
	}
	al, err := agent.NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	t.Cleanup(func() { al.Close() })
	return al
}

// newSysagentFastUpsertDeps wires Deps the way pkg/gateway/gateway.go wires
// production sysAgentDeps: ReloadFunc is the full config reload (re-derived
// from the entity store, mirroring reloadTrigger /
// populateAgentsListFromEntityStoreStrict) and counted via reloadCalls;
// UpsertAgentFastFunc is the fast path (AgentLoop.UpsertAgentFast), falling
// back to the counted ReloadFunc on failure or when a reload is already
// pending.
//
// Like gateway.go's real closure, UpsertAgentFastFunc first re-derives
// al.cfg.Agents.List/SkippedAgentIDs from the entity store via
// al.MutateConfig before calling UpsertAgentFast — AgentCreateTool/
// AgentUpdateTool persist straight to the entity store (agentstore.Store)
// and never touch al.cfg themselves, so without this refresh al.cfg would
// still be missing the very agent UpsertAgentFast is asked to publish. This
// deliberately calls the SAME exported primitives
// (AgentLoop.MutateConfig + agentstore.Store.List) gateway.go's closure
// composes, rather than importing pkg/gateway's unexported
// populateAgentsListFromEntityStoreStrict helper directly — pulling in
// pkg/gateway here would drag its (expensive to link) test dependency graph
// into this package's test binary for no behavioral gain.
func newSysagentFastUpsertDeps(
	al *agent.AgentLoop, provider *reloadTestProvider, home string, reloadCalls *atomic.Int32,
) *systools.Deps {
	reloadFunc := func() error {
		reloadCalls.Add(1)
		agents, skipped, listErr := agentstore.New(home).List()
		if listErr != nil {
			return fmt.Errorf("list agent entity records: %w", listErr)
		}
		newCfg := *al.GetConfig()
		newCfg.Agents.List = agents
		newCfg.SkippedAgentIDs = skipped
		return al.ReloadProviderAndConfig(context.Background(), provider, &newCfg)
	}
	return &systools.Deps{
		Home:   home,
		GetCfg: al.GetConfig,
		MutateConfig: func(fn func(*config.Config) error) error {
			return fn(al.GetConfig())
		},
		SaveConfigLocked: func(*config.Config) error { return nil },
		ReloadFunc:       reloadFunc,
		UpsertAgentFastFunc: func(agentID string) error {
			if al.IsReloadPending() {
				return reloadFunc()
			}
			refreshErr := al.MutateConfig(func(cfg *config.Config) error {
				agents, skipped, listErr := agentstore.New(home).List()
				if listErr != nil {
					return fmt.Errorf("list agent entity records: %w", listErr)
				}
				cfg.Agents.List = agents
				cfg.SkippedAgentIDs = skipped
				return nil
			})
			if refreshErr != nil {
				return reloadFunc()
			}
			if _, err := al.UpsertAgentFast(al.GetConfig(), agentID); err != nil {
				return reloadFunc()
			}
			return nil
		},
	}
}

// TestAgentCreate_DoesNotTriggerFullReload proves create_agent (no concurrent
// reload in flight) never invokes the full-reload trigger, and that the new
// agent is genuinely usable afterward: resolvable via GetAgent AND via
// ResolveRoute against a channel binding configured BEFORE the agent existed
// (an operator can bind a channel to an agent ID that gets created later).
func TestAgentCreate_DoesNotTriggerFullReload(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	const agentID = "fast-path-create-agent"
	al := buildSysagentFastUpsertTestLoop(t, home, provider, []config.AgentBinding{
		{AgentID: agentID, Match: config.BindingMatch{Channel: "telegram", AccountID: "*"}},
	})

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Fast Path Create Agent",
		"description": "proves create_agent publishes via the fast path, not a full reload",
		"soul":        "You are a test agent.",
		"model":       "test-model",
		"color":       "#22C55E",
		"icon":        "robot",
	})
	if result.IsError {
		t.Fatalf("create_agent failed: %s", result.ForLLM)
	}
	created := parseSuccess(t, result.ForLLM)
	if got, _ := created["id"].(string); got != agentID {
		t.Fatalf("create_agent id = %q, want %q (slug mismatch — fix the test's expected ID)", got, agentID)
	}

	if got := reloadCalls.Load(); got != 0 {
		t.Fatalf("create_agent must NEVER invoke the full-reload trigger when no reload is already in "+
			"flight (got %d calls) — invoking it reaches the restartServices cascade "+
			"(channels/cron/plan-engine/schedulers all restart), the exact mechanism issue #571 exists "+
			"to remove from the agent create/update path", got)
	}

	// Usable, not merely present: GetAgent.
	inst, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || inst == nil {
		t.Fatalf("the new agent %q must be resolvable via GetAgent immediately after create_agent, with "+
			"zero reloads", agentID)
	}

	// Usable, not merely present: ResolveRoute against a pre-existing binding.
	route := al.GetRegistry().ResolveRoute(routing.RouteInput{Channel: "telegram", AccountID: "acct-1"})
	if route.Drop {
		t.Fatalf("route must not drop for %q", agentID)
	}
	if route.AgentID != agentID {
		t.Fatalf("ResolveRoute must resolve the telegram binding to %q, got %q (matched_by=%s) — this "+
			"fails if the fast path left the registry's cached RouteResolver stale", agentID, route.AgentID,
			route.MatchedBy)
	}
}

// TestAgentUpdate_DoesNotTriggerFullReload is the update-path counterpart: a
// soul change (the update_agent field this fix targets) must publish via the
// fast path with zero reload-trigger invocations, and the update must be
// genuinely visible afterward (a NEW instance carrying the updated model),
// not merely a config-file change nobody re-read.
func TestAgentUpdate_DoesNotTriggerFullReload(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	const agentID = "fast-path-update-agent"
	al := buildSysagentFastUpsertTestLoop(t, home, provider, nil)

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)

	createResult := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Fast Path Update Agent",
		"description": "created so update_agent has a target",
		"soul":        "original soul",
		"model":       "test-model",
		"color":       "#22C55E",
		"icon":        "robot",
	})
	if createResult.IsError {
		t.Fatalf("create_agent (setup) failed: %s", createResult.ForLLM)
	}
	created := parseSuccess(t, createResult.ForLLM)
	if got, _ := created["id"].(string); got != agentID {
		t.Fatalf("create_agent id = %q, want %q (slug mismatch — fix the test's expected ID)", got, agentID)
	}
	instBeforeUpdate, ok := al.GetRegistry().GetAgent(agentID)
	if !ok {
		t.Fatalf("test setup: %q must be registered after create", agentID)
	}

	// Reset the counter: this test is about update_agent's own behavior, not
	// create_agent's (already covered by TestAgentCreate_DoesNotTriggerFullReload).
	reloadCalls.Store(0)

	updateResult := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":    agentID,
		"soul":  "an updated soul, no cascade expected",
		"model": "test-model-v2",
	})
	if updateResult.IsError {
		t.Fatalf("update_agent failed: %s", updateResult.ForLLM)
	}

	if got := reloadCalls.Load(); got != 0 {
		t.Fatalf("update_agent must NEVER invoke the full-reload trigger when no reload is already in "+
			"flight (got %d calls)", got)
	}

	instAfterUpdate, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || instAfterUpdate == nil {
		t.Fatalf("the updated agent %q must remain resolvable via GetAgent, with zero reloads", agentID)
	}
	if instAfterUpdate == instBeforeUpdate {
		t.Fatalf("update_agent's fast path must publish a NEW *AgentInstance reflecting the update, not " +
			"leave the pre-update instance pointer live")
	}
	if instAfterUpdate.Model != "test-model-v2" {
		t.Fatalf("updated agent's Model = %q, want %q — the fast-upserted instance must reflect the "+
			"just-updated config, not a stale snapshot", instAfterUpdate.Model, "test-model-v2")
	}
}

// TestAgentDelete_StillUsesFullReload_Deliberately regression-guards the
// deliberate decision documented in AgentDeleteTool.Execute: unlike
// create/update above, delete_agent has no fast-path counterpart to call —
// AgentRegistry.RemoveAgent lacks the resolver-rebuild + atomic-publish
// parity AgentLoop.UpsertAgentFast has, and building that parity belongs in
// pkg/agent, not reinvented in this tool. This asserts the full-reload
// trigger fires exactly once per delete (never zero — proving delete did NOT
// silently start using a fast path with no safety net — and never more than
// once), and that deletion is still genuinely effective (agent gone from
// GetAgent and unroutable), matching
// TestAgentDelete_ImmediatelyUnroutableAndUnlisted_NoRestart's existing
// coverage from the reload-mechanism side.
func TestAgentDelete_StillUsesFullReload_Deliberately(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	const agentID = "fast-path-delete-agent"
	al := buildSysagentFastUpsertTestLoop(t, home, provider, []config.AgentBinding{
		{AgentID: agentID, Match: config.BindingMatch{Channel: "telegram", AccountID: "*"}},
	})

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)

	createResult := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Fast Path Delete Agent",
		"description": "created so delete_agent has a target",
		"soul":        "You are a test agent.",
		"model":       "test-model",
		"color":       "#22C55E",
		"icon":        "robot",
	})
	if createResult.IsError {
		t.Fatalf("create_agent (setup) failed: %s", createResult.ForLLM)
	}

	// A second, surviving agent so the roster is non-empty post-delete —
	// mirrors TestAgentDelete_ImmediatelyUnroutableAndUnlisted_NoRestart's own
	// "keeper" rationale (an empty roster changes pickAgentID's fallback
	// behavior and would mask the assertions below).
	keeperResult := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Keeper Agent",
		"description": "stays in the roster after the other agent is deleted",
		"soul":        "You persist.",
		"model":       "test-model",
		"color":       "#3366FF",
		"icon":        "robot",
	})
	if keeperResult.IsError {
		t.Fatalf("create_agent (keeper) failed: %s", keeperResult.ForLLM)
	}

	if _, ok := al.GetRegistry().GetAgent(agentID); !ok {
		t.Fatalf("test setup: expected %q to be registered immediately after create", agentID)
	}
	reloadCalls.Store(0)

	deleteResult := systools.NewAgentDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":      agentID,
		"confirm": true,
	})
	if deleteResult.IsError {
		t.Fatalf("delete_agent failed: %s", deleteResult.ForLLM)
	}

	if got := reloadCalls.Load(); got != 1 {
		t.Fatalf("delete_agent must invoke the full-reload trigger EXACTLY ONCE (deliberately not "+
			"fast-pathed — see AgentDeleteTool.Execute's doc comment), got %d", got)
	}

	if _, ok := al.GetRegistry().GetAgent(agentID); ok {
		t.Fatalf("deleted agent %q must no longer be resolvable via GetAgent after the reload", agentID)
	}
	route := al.GetRegistry().ResolveRoute(routing.RouteInput{Channel: "telegram", AccountID: "acct-1"})
	if route.AgentID == agentID && !route.Drop {
		t.Fatalf("ResolveRoute must no longer resolve the telegram binding to the deleted agent %q, got "+
			"agent_id=%q drop=%v", agentID, route.AgentID, route.Drop)
	}
}
