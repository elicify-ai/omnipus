// Omnipus — get_usage tool tests.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// BDD: get_usage tool exercises.
// Traces to: diag.go UsageQueryTool, Fix-Wave reviewer findings G1.

package systools_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/session"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
)

// makeSession is a helper that constructs a *session.UnifiedMeta with the
// specified agent ID, token counts, and UpdatedAt timestamp.
func makeSession(id, agentID string, tokensIn, tokensOut int, updatedAt time.Time) *session.UnifiedMeta {
	sm := &session.UnifiedMeta{
		SessionMeta: session.SessionMeta{
			ID:            id,
			AgentID:       agentID,
			ActiveAgentID: agentID,
			AgentIDs:      []string{agentID},
			Status:        "active",
			Channel:       "webchat",
			CreatedAt:     updatedAt,
			UpdatedAt:     updatedAt,
			Stats: session.SessionStats{
				TokensIn:    tokensIn,
				TokensOut:   tokensOut,
				TokensTotal: tokensIn + tokensOut,
			},
		},
	}
	return sm
}

// newTestDepsWithSessions constructs a *Deps whose ListSessions returns the
// provided sessions slice.  GetCfg returns the provided config.
func newTestDepsWithSessions(cfg *config.Config, sessions []*session.UnifiedMeta) *systools.Deps {
	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }
	return &systools.Deps{
		Home:             "/tmp/omnipus-test",
		ConfigPath:       "/tmp/omnipus-test/config.json",
		GetCfg:           getCfg,
		MutateConfig:     testMutateConfig(&mu, getCfg),
		SaveConfigLocked: func(cfg *config.Config) error { return nil },
		ListSessions: func() ([]*session.UnifiedMeta, []error) {
			return sessions, nil
		},
	}
}

// TestGetUsage_NilListSessions verifies that a nil ListSessions dependency
// returns a USAGE_UNAVAILABLE error rather than an empty "no usage" report.
// BDD: Given Deps.ListSessions == nil,
// When get_usage is called,
// Then an error result with code USAGE_UNAVAILABLE is returned.
func TestGetUsage_NilListSessions(t *testing.T) {
	deps, _ := newTestDeps() // newTestDeps does not wire ListSessions
	tool := systools.NewUsageQueryTool(deps)

	res := tool.Execute(context.Background(), map[string]any{})
	if !res.IsError {
		t.Fatalf("expected error when ListSessions is nil, got success: %s", res.ForLLM)
	}
	m := parseError(t, res.ForLLM)
	errObj, _ := m["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("expected error object in response, got: %s", res.ForLLM)
	}
	code, _ := errObj["code"].(string)
	if code != "USAGE_UNAVAILABLE" {
		t.Errorf("error code = %q, want USAGE_UNAVAILABLE; full response: %s", code, res.ForLLM)
	}
}

// TestGetUsage_InvalidPeriod verifies that an unrecognized period returns an error.
// BDD: Given period="garbage",
// When get_usage is called,
// Then an error result with code INVALID_PARAM is returned.
func TestGetUsage_InvalidPeriod(t *testing.T) {
	cfg := config.DefaultConfig()
	deps := newTestDepsWithSessions(cfg, nil)
	tool := systools.NewUsageQueryTool(deps)

	res := tool.Execute(context.Background(), map[string]any{"period": "garbage"})
	if !res.IsError {
		t.Fatalf("expected error for period=garbage, got success: %s", res.ForLLM)
	}
	m := parseError(t, res.ForLLM)
	errObj, _ := m["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "INVALID_PARAM" {
		t.Errorf("error code = %q, want INVALID_PARAM; full response: %s", code, res.ForLLM)
	}
}

// TestGetUsage_InvalidBy verifies that an unrecognized by dimension returns an error.
// BDD: Given by="badvalue",
// When get_usage is called,
// Then an error result with code INVALID_PARAM is returned.
func TestGetUsage_InvalidBy(t *testing.T) {
	cfg := config.DefaultConfig()
	deps := newTestDepsWithSessions(cfg, nil)
	tool := systools.NewUsageQueryTool(deps)

	res := tool.Execute(context.Background(), map[string]any{"by": "badvalue"})
	if !res.IsError {
		t.Fatalf("expected error for by=badvalue, got success: %s", res.ForLLM)
	}
	m := parseError(t, res.ForLLM)
	errObj, _ := m["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "INVALID_PARAM" {
		t.Errorf("error code = %q, want INVALID_PARAM; full response: %s", code, res.ForLLM)
	}
}

// TestGetUsage_AgentIDFilter verifies that the agent_id filter restricts results
// to only that agent's tokens.
// BDD: Given two sessions — one for "agent-x" (in=100, out=50) and one for
// "agent-y" (in=200, out=80) — both within the current month,
// When get_usage is called with agent_id="agent-x",
// Then the returned total reflects only agent-x's tokens (in=100, out=50, total=150).
func TestGetUsage_AgentIDFilter(t *testing.T) {
	now := time.Now().UTC()
	sessions := []*session.UnifiedMeta{
		makeSession("sess-x", "agent-x", 100, 50, now),
		makeSession("sess-y", "agent-y", 200, 80, now),
	}
	cfg := config.DefaultConfig()
	deps := newTestDepsWithSessions(cfg, sessions)
	tool := systools.NewUsageQueryTool(deps)

	res := tool.Execute(context.Background(), map[string]any{
		"agent_id": "agent-x",
		"period":   "month",
	})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.ForLLM)
	}

	m := parseSuccess(t, res.ForLLM)
	totalRaw, _ := m["total"].(map[string]any)
	if totalRaw == nil {
		t.Fatalf("expected total field in response: %s", res.ForLLM)
	}
	gotIn := int(totalRaw["in"].(float64))
	gotOut := int(totalRaw["out"].(float64))
	gotTotal := int(totalRaw["total"].(float64))
	if gotIn != 100 || gotOut != 50 || gotTotal != 150 {
		t.Errorf("agent_id filter: got in=%d out=%d total=%d, want in=100 out=50 total=150; resp=%s",
			gotIn, gotOut, gotTotal, res.ForLLM)
	}

	// agent-y must not appear in the breakdown.
	breakdown, _ := m["breakdown"].([]any)
	for _, raw := range breakdown {
		entry, _ := raw.(map[string]any)
		if key, _ := entry["key"].(string); key == "agent-y" {
			t.Errorf("agent-y must not appear in breakdown when agent_id=agent-x; resp=%s", res.ForLLM)
		}
	}
}

// TestGetUsage_SessionIDFilter verifies that the session_id filter restricts
// results to only that session's tokens.
// BDD: Given two sessions — "sess-alpha" (agent-a, in=10, out=5) and
// "sess-beta" (agent-b, in=300, out=100) — both current month,
// When get_usage is called with session_id="sess-alpha",
// Then total.in=10, total.out=5.
func TestGetUsage_SessionIDFilter(t *testing.T) {
	now := time.Now().UTC()
	sessions := []*session.UnifiedMeta{
		makeSession("sess-alpha", "agent-a", 10, 5, now),
		makeSession("sess-beta", "agent-b", 300, 100, now),
	}
	cfg := config.DefaultConfig()
	deps := newTestDepsWithSessions(cfg, sessions)
	tool := systools.NewUsageQueryTool(deps)

	res := tool.Execute(context.Background(), map[string]any{
		"session_id": "sess-alpha",
		"period":     "all",
	})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.ForLLM)
	}

	m := parseSuccess(t, res.ForLLM)
	totalRaw, _ := m["total"].(map[string]any)
	gotIn := int(totalRaw["in"].(float64))
	gotOut := int(totalRaw["out"].(float64))
	if gotIn != 10 || gotOut != 5 {
		t.Errorf("session_id filter: got in=%d out=%d, want in=10 out=5; resp=%s", gotIn, gotOut, res.ForLLM)
	}
}

// TestGetUsage_ExcludesSubagent3p verifies that external CLI workers (subagent_3p)
// are excluded from usage reports.
//
// BDD: Given a config with two agents:
//   - "native-agent" (Type=worker, Executor.Kind=native)
//   - "ext-agent"    (Type=worker, Executor.Kind=external-cli)
//
// And sessions for both agents (both within the current month),
// When get_usage is called without filters,
// Then the report includes native-agent's tokens but NOT ext-agent's tokens.
// This exercises the config.IsExternalCLIWorkerID→Exclude path end-to-end.
func TestGetUsage_ExcludesSubagent3p(t *testing.T) {
	now := time.Now().UTC()

	// Build a config with one native worker and one external-cli worker.
	cfg := config.DefaultConfig()
	cfg.Agents.List = append(cfg.Agents.List,
		config.AgentConfig{
			ID:   "native-agent",
			Name: "Native Worker",
			Type: config.AgentTypeWorker,
			Subagents: &config.SubagentsConfig{
				Executor: &config.ExecutorConfig{Kind: config.ExecutorKindNative},
			},
		},
		config.AgentConfig{
			ID:   "ext-agent",
			Name: "External CLI Worker",
			Type: config.AgentTypeWorker,
			Subagents: &config.SubagentsConfig{
				Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI},
			},
		},
	)

	sessions := []*session.UnifiedMeta{
		makeSession("sess-native", "native-agent", 100, 50, now),
		makeSession("sess-ext", "ext-agent", 999, 888, now),
	}
	deps := newTestDepsWithSessions(cfg, sessions)
	tool := systools.NewUsageQueryTool(deps)

	res := tool.Execute(context.Background(), map[string]any{"period": "all"})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.ForLLM)
	}

	m := parseSuccess(t, res.ForLLM)

	// Total must reflect only the native agent (in=100, out=50, total=150).
	totalRaw, _ := m["total"].(map[string]any)
	if totalRaw == nil {
		t.Fatalf("expected total field: %s", res.ForLLM)
	}
	gotTotal := int(totalRaw["total"].(float64))
	if gotTotal != 150 {
		t.Errorf("total.total = %d, want 150 (ext-agent tokens must be excluded); resp=%s", gotTotal, res.ForLLM)
	}

	// ext-agent must not appear in the breakdown.
	breakdown, _ := m["breakdown"].([]any)
	for _, raw := range breakdown {
		entry, _ := raw.(map[string]any)
		if key, _ := entry["key"].(string); key == "ext-agent" {
			t.Errorf("ext-agent must not appear in breakdown (subagent_3p excluded); resp=%s", res.ForLLM)
		}
	}
}

// TestGetUsage_PartialErrors verifies that when ListSessions returns errors,
// the response includes "partial":true and "partial_error_count" matching the
// number of errors, while still returning whatever data was collected.
// BDD: Given ListSessions returns 1 valid session and 2 errors,
// When get_usage is called,
// Then the response is a success with partial=true and partial_error_count=2.
func TestGetUsage_PartialErrors(t *testing.T) {
	now := time.Now().UTC()
	goodSession := makeSession("sess-good", "agent-ok", 50, 25, now)

	var mu sync.Mutex
	cfg := config.DefaultConfig()
	getCfg := func() *config.Config { return cfg }
	deps := &systools.Deps{
		Home:             "/tmp/omnipus-test",
		ConfigPath:       "/tmp/omnipus-test/config.json",
		GetCfg:           getCfg,
		MutateConfig:     testMutateConfig(&mu, getCfg),
		SaveConfigLocked: func(cfg *config.Config) error { return nil },
		ListSessions: func() ([]*session.UnifiedMeta, []error) {
			return []*session.UnifiedMeta{goodSession}, []error{
				errors.New("store-1: read error"),
				errors.New("store-2: read error"),
			}
		},
	}
	tool := systools.NewUsageQueryTool(deps)

	res := tool.Execute(context.Background(), map[string]any{"period": "all"})
	if res.IsError {
		t.Fatalf("expected non-error response (partial success), got error: %s", res.ForLLM)
	}

	m := parseSuccess(t, res.ForLLM)

	partial, hasPartial := m["partial"]
	if !hasPartial {
		t.Errorf("expected partial field in response when errors occurred; resp=%s", res.ForLLM)
	}
	if b, ok := partial.(bool); !ok || !b {
		t.Errorf("partial must be true, got %v; resp=%s", partial, res.ForLLM)
	}

	errCountRaw, hasCount := m["partial_error_count"]
	if !hasCount {
		t.Errorf("expected partial_error_count field; resp=%s", res.ForLLM)
	}
	if errCount, ok := errCountRaw.(float64); !ok || int(errCount) != 2 {
		t.Errorf("partial_error_count = %v, want 2; resp=%s", errCountRaw, res.ForLLM)
	}

	// The good session's data must still appear.
	totalRaw, _ := m["total"].(map[string]any)
	if totalRaw == nil {
		t.Fatalf("expected total field even with partial data: %s", res.ForLLM)
	}
	gotIn := int(totalRaw["in"].(float64))
	if gotIn != 50 {
		t.Errorf("total.in = %d, want 50; resp=%s", gotIn, res.ForLLM)
	}
}
