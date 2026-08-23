// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// Regression tests for the memory-tool reload-wiring gap.
//
// Before the fix, NewAgentLoop wired the audit logger and a shared
// MemoryRateLimiter onto every agent's remember/run_retrospective tools at
// boot, but ReloadProviderAndConfig (called by ~10 REST endpoints, including
// onboarding completion) rebuilds the registry via NewAgentRegistry — which
// constructs brand-new RememberTool/RetrospectiveTool instances that never
// learned about either dependency. The rate limiter was additionally a bare
// local variable (memRL), never stored on *AgentLoop, so there was no way to
// even re-attach "the same limiter" later.
//
// These tests exercise the real ReloadProviderAndConfig path (not a unit
// stub) and assert the POST-RELOAD tool instances functionally behave as if
// wired — reading unexported AgentLoop/tool-registry state is not enough,
// since the bug was precisely that the registry-level field could be
// populated while the individual tool instances were not.
// ---------------------------------------------------------------------------

// memReloadTestLoop builds a real AgentLoop with audit logging enabled and the
// core agent roster seeded (ava, mia, jim, ray), rooted at a fresh OMNIPUS_HOME
// so agent workspaces and the audit log resolve under a hermetic temp dir.
// Returns the loop and the path audit.jsonl will be written to.
func memReloadTestLoop(t *testing.T) (*AgentLoop, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Sandbox.AuditLog = true

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })

	auditPath := filepath.Join(home, "system", "audit.jsonl")
	return al, auditPath
}

// reloadWithSameAgents drives ReloadProviderAndConfig with a fresh Config
// carrying the same Agents.Defaults/List/Sandbox.AuditLog as the live config —
// the same shape a real REST handler builds via safeUpdateConfigJSON before
// calling TriggerReload. A fresh *config.Config is built (not a struct copy of
// al.cfg, which holds a sync.RWMutex) per the established test pattern in
// global_policy_hot_update_test.go.
func reloadWithSameAgents(t *testing.T, al *AgentLoop) {
	t.Helper()
	liveCfg := al.GetConfig()
	newCfg := &config.Config{}
	newCfg.Agents.Defaults = liveCfg.Agents.Defaults
	newCfg.Agents.List = make([]config.AgentConfig, len(liveCfg.Agents.List))
	copy(newCfg.Agents.List, liveCfg.Agents.List)
	newCfg.Sandbox.AuditLog = liveCfg.Sandbox.AuditLog
	require.NoError(t, al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, newCfg),
		"ReloadProviderAndConfig must succeed")
}

// readAuditEvents reads and parses audit.jsonl, tolerating a not-yet-created
// file (no writes yet).
func readAuditEvents(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read audit log %s: %v", path, err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &parsed), "audit line must be valid JSON: %s", line)
		out = append(out, parsed)
	}
	return out
}

// TestReloadWiring_MemoryAuditLogger_PersistsAcrossReload proves the audit-
// logger half of the fix: after ReloadProviderAndConfig rebuilds the registry,
// the fresh RememberTool instance for a live agent must still emit its
// structured "memory.remember" audit event (SEC-15) — i.e. it was not left
// with a nil audit logger.
func TestReloadWiring_MemoryAuditLogger_PersistsAcrossReload(t *testing.T) {
	al, auditPath := memReloadTestLoop(t)
	require.NotNil(t, al.auditLogger,
		"audit logging was enabled via cfg.Sandbox.AuditLog; al.auditLogger must be set at boot")

	reloadWithSameAgents(t, al)

	// al.auditLogger itself is never reassigned by ReloadProviderAndConfig
	// (only the registry is rebuilt) — that is exactly the trap the original
	// bug exploited: the AgentLoop-level field stays populated while the
	// freshly-constructed RememberTool instance never received it. Prove the
	// wiring functionally rather than just checking the field.
	const agentID = "mia"
	inst, ok := al.GetRegistry().GetAgent(agentID)
	require.True(t, ok, "agent %q must exist in the post-reload registry", agentID)

	ctx := tools.WithAgentID(context.Background(), agentID)
	result := inst.Tools.ExecuteWithContext(ctx, "remember", map[string]any{
		"content":  "reload wiring regression check",
		"category": "reference",
	}, "", "", nil)
	require.False(t, result.IsError, "remember call must succeed: %+v", result)

	events := readAuditEvents(t, auditPath)
	found := false
	for _, e := range events {
		if e["event"] == "memory.remember" && e["agent_id"] == agentID {
			found = true
			break
		}
	}
	require.True(t, found,
		"expected a memory.remember audit entry for agent %q after reload; its absence means the "+
			"post-reload RememberTool instance has a nil audit logger (the reload-wiring gap)", agentID)
}

// TestReloadWiring_MemoryRateLimiter_SameInstancePersistsAcrossReload proves
// the rate-limiter half of the fix: the shared MemoryRateLimiter built once in
// NewAgentLoop must be the SAME instance re-wired onto the fresh registry on
// every reload — not reconstructed (which would reset every agent's
// sliding-window budget on any unrelated config change) and not dropped
// entirely (which would silently disable the SEC #155 item 6 rate limiter,
// as it did before this fix, since memRL was a bare local never stored on
// *AgentLoop).
func TestReloadWiring_MemoryRateLimiter_SameInstancePersistsAcrossReload(t *testing.T) {
	al, _ := memReloadTestLoop(t)

	limiterBefore := al.memoryRateLimiter
	require.NotNil(t, limiterBefore, "NewAgentLoop must construct a shared memory rate limiter")

	const agentID = "ava"
	// Exhaust the per-agent budget directly against the limiter object that
	// is (or should be) wired onto ava's remember tool. "<local>" mirrors the
	// caller identity callerIdentity(ctx) derives when no channel/chatID is
	// set (empty channel + empty chatID -> "<local>"), matching the
	// ExecuteWithContext(ctx, ..., "", "", nil) call below.
	for i := 0; i < limiterBefore.PerAgentLimit(); i++ {
		decision := limiterBefore.Allow(agentID, "<local>")
		require.True(t, decision.Allowed, "pre-exhaustion call %d should be allowed", i)
	}
	sanity := limiterBefore.Allow(agentID, "<local>")
	require.False(t, sanity.Allowed, "sanity check: the per-agent budget must be exhausted before reload")

	reloadWithSameAgents(t, al)

	limiterAfter := al.memoryRateLimiter
	require.Same(t, limiterBefore, limiterAfter,
		"the SAME MemoryRateLimiter instance must persist across reload, not be reconstructed "+
			"(a fresh limiter would silently reset every agent's rate-limit budget on any unrelated "+
			"config change)")

	inst, ok := al.GetRegistry().GetAgent(agentID)
	require.True(t, ok, "agent %q must exist in the post-reload registry", agentID)

	ctx := tools.WithAgentID(context.Background(), agentID)
	result := inst.Tools.ExecuteWithContext(ctx, "remember", map[string]any{
		"content":  "should be rate limited",
		"category": "reference",
	}, "", "", nil)

	require.True(t, result.IsError,
		"post-reload remember call for the exhausted agent must be rate-limited; if it succeeds, the "+
			"post-reload RememberTool instance is either missing the rate limiter entirely (nil) or was "+
			"wired to a freshly-constructed one whose budget was never exhausted")
	require.Contains(t, result.ForLLM, "rate limit", "error message must indicate a rate-limit rejection")
}
