// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for R2-MAJ-015 — tools.delegate.require_parent_agent_id, the operator
// kill switch for delegate's FR-015 fail-closed parent-agent-id guard.
//
// The guard refuses a delegation outright when ToolAgentID(ctx) is empty,
// because the lifecycle record it would mint carries no parent linkage and can
// never be returned to its parent by list_jobs. Its blast radius is the whole
// install: a wiring regression anywhere upstream of ToolAgentID turns EVERY
// delegate call into that error. This file proves the escape hatch is real —
// that the key is actually READ, that it defaults strict, that flipping it to
// false really lets the delegation through, and that it is read LIVE rather
// than captured once at wiring time.
//
// These assert OUTCOMES (was the delegation refused? was a record minted? with
// what ParentAgentID? was the downgrade announced?), never that a setter
// exists.

package tools

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// newRequireParentTestTool builds a DelegateTool with a real
// t.TempDir()-backed lifecycle store, a permissive delegation gate and a
// synchronous mock spawner. The lifecycle store is what makes these tests
// meaningful: the FR-015 guard only runs when t.lifecycle != nil (with no
// store there is no record to orphan), so a tool without one would pass every
// assertion here vacuously.
func newRequireParentTestTool(t *testing.T) (*DelegateTool, *session.LifecycleStore) {
	t.Helper()
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSpawner(&mockDelegateSpawner{})
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetDelegationDenyCheckerAwait(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetSessionMessagingEnabled(func() bool { return true })

	lc := session.NewLifecycleStore(t.TempDir())
	tool.SetLifecycleStore(lc)

	// Drain in-flight async delegation goroutines before the t.TempDir()
	// cleanup runs, so a background write never races RemoveAll. Registered
	// after the TempDir call so LIFO cleanup ordering puts this first.
	t.Cleanup(tool.WaitForAsyncTasks)
	return tool, lc
}

// captureLogs redirects the default slog logger into a buffer for the duration
// of the test and returns an accessor for what was written. Used to prove the
// downgraded path ANNOUNCES itself rather than degrading silently.
func captureLogs(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedWriter{mu: &mu, buf: buf}, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

// lockedWriter serializes writes from the delegation goroutines into the
// capture buffer (bytes.Buffer is not concurrency-safe and these tests run
// under -race).
type lockedWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// mintedRecords returns every lifecycle record in the store.
func mintedRecords(t *testing.T, lc *session.LifecycleStore) []session.LifecycleRecord {
	t.Helper()
	recs, err := lc.List(session.LifecycleFilter{})
	if err != nil {
		t.Fatalf("list lifecycle records: %v", err)
	}
	return recs
}

// TestDelegate_RequireParentAgentID_DefaultRefusesEmptyParent proves the
// SHIPPED default is still fail-closed: an UNWIRED tool (nobody ever called
// SetRequireParentAgentID) refuses a delegation whose context carries no
// resolvable calling-agent id, and mints nothing.
//
// Unwired must resolve strict, not permissive: the whole point of the key is
// that an operator opts OUT of the guard deliberately. A tool that fell open
// merely because a wiring pass forgot it would silently reinstate the orphan
// records FR-015 exists to prevent.
func TestDelegate_RequireParentAgentID_DefaultRefusesEmptyParent(t *testing.T) {
	tool, lc := newRequireParentTestTool(t)

	// No WithAgentID — this is exactly the unresolvable-principal case.
	result := tool.Execute(context.Background(), map[string]any{
		"task":     "investigate the flaky test",
		"agent_id": "ray",
		"async":    false,
	})

	if result == nil || !result.IsError {
		t.Fatalf("default posture must REFUSE a delegation with no parent agent id; got: %+v", result)
	}
	if !strings.Contains(result.ForLLM, "cannot resolve the delegating agent's identity") {
		t.Errorf("refusal must name the reason, got: %s", result.ForLLM)
	}
	if recs := mintedRecords(t, lc); len(recs) != 0 {
		t.Fatalf("refusal must mint NO lifecycle record, found %d: %+v", len(recs), recs)
	}
}

// TestDelegate_RequireParentAgentID_ExplicitFalseProceedsAndLogs proves the
// escape hatch actually works: with the key resolved to false the SAME call
// that was refused above now succeeds, a record IS minted (with an empty
// ParentAgentID — the knowingly-degraded attribution the operator chose), and
// the downgrade is announced at Error level.
func TestDelegate_RequireParentAgentID_ExplicitFalseProceedsAndLogs(t *testing.T) {
	logs := captureLogs(t)
	tool, lc := newRequireParentTestTool(t)
	tool.SetRequireParentAgentID(func() bool { return false })

	result := tool.Execute(context.Background(), map[string]any{
		"task":     "investigate the flaky test",
		"agent_id": "ray",
		"async":    false,
	})

	if result == nil || result.IsError {
		t.Fatalf("with require_parent_agent_id=false the delegation must PROCEED; got: %+v", result)
	}

	recs := mintedRecords(t, lc)
	if len(recs) != 1 {
		t.Fatalf("expected exactly 1 minted lifecycle record, got %d: %+v", len(recs), recs)
	}
	if recs[0].ParentAgentID != "" {
		t.Errorf("downgraded mint must carry the empty parent id it was given, got %q", recs[0].ParentAgentID)
	}
	if recs[0].AgentID != "ray" {
		t.Errorf("child agent id must still be recorded, got %q", recs[0].AgentID)
	}

	// The degradation must be LOUD. A silent fall-through would leave an
	// operator with unattributable sessions and no signal explaining why.
	out := logs()
	if !strings.Contains(out, "level=ERROR") {
		t.Errorf("downgraded mint must log at ERROR level, got:\n%s", out)
	}
	if !strings.Contains(out, "require_parent_agent_id") {
		t.Errorf("the log line must name the key that disabled the guard, got:\n%s", out)
	}
}

// TestDelegate_RequireParentAgentID_ResolvedLiveNotCapturedEagerly is the
// anti-regression for the wiring hazard this whole design exists to dodge:
// gateway boot assigns some of this tool's dependencies AFTER the pass that
// wires it, so anything read eagerly at wiring time freezes at whatever that
// pass happened to see while registration still looks perfectly correct.
//
// The tool is wired with a closure over a config that says "strict" at wiring
// time. The config is then flipped to false — as a real operator edit or a
// late boot assignment would — and the delegation must now be allowed through.
// An implementation that stored a bool instead of the closure fails here: it
// would still be holding the true it read during SetRequireParentAgentID.
//
// It also pins the *bool resolution: the config is driven through
// config.DelegateToolConfig.EffectiveRequireParentAgentID, so a reader that
// compared the raw pointer itself (nil-vs-non-nil) rather than resolving it
// would read an explicit `false` as "set, therefore on" and refuse.
func TestDelegate_RequireParentAgentID_ResolvedLiveNotCapturedEagerly(t *testing.T) {
	tool, lc := newRequireParentTestTool(t)

	// The config as it stands during the wiring pass: key unset => strict.
	cfg := &config.Config{}
	tool.SetRequireParentAgentID(func() bool {
		return cfg.Tools.Delegate.EffectiveRequireParentAgentID()
	})

	// Sanity: at this point the closure resolves strict, so the guard bites.
	first := tool.Execute(context.Background(), map[string]any{
		"task": "before the flip", "agent_id": "ray", "async": false,
	})
	if first == nil || !first.IsError {
		t.Fatalf("unset key must resolve strict at first read; got: %+v", first)
	}

	// The operator flips the switch AFTER wiring. No restart, no re-wire.
	off := false
	cfg.Tools.Delegate.RequireParentAgentID = &off

	second := tool.Execute(context.Background(), map[string]any{
		"task": "after the flip", "agent_id": "ray", "async": false,
	})
	if second == nil || second.IsError {
		t.Fatalf("the switch must be read LIVE — a post-wiring flip to false must take effect "+
			"without re-wiring (an eagerly captured bool fails here); got: %+v", second)
	}

	recs := mintedRecords(t, lc)
	if len(recs) != 1 {
		t.Fatalf("only the post-flip delegation should have minted a record, got %d: %+v", len(recs), recs)
	}
}

// TestDelegate_RequireParentAgentID_TrueStillRefusesWhenExplicitlyWired proves
// the switch is a real two-way control and not a one-way "any wiring disables
// the guard": explicitly wiring it to true keeps the refusal.
func TestDelegate_RequireParentAgentID_TrueStillRefusesWhenExplicitlyWired(t *testing.T) {
	tool, lc := newRequireParentTestTool(t)
	tool.SetRequireParentAgentID(func() bool { return true })

	result := tool.Execute(context.Background(), map[string]any{
		"task": "x", "agent_id": "ray", "async": false,
	})
	if result == nil || !result.IsError {
		t.Fatalf("explicitly-true switch must still refuse; got: %+v", result)
	}
	if recs := mintedRecords(t, lc); len(recs) != 0 {
		t.Fatalf("refusal must mint no record, found %d", len(recs))
	}
}

// TestDelegate_RequireParentAgentID_IrrelevantWhenParentResolves proves the
// switch only ever governs the EMPTY-parent case. With a real calling agent in
// context, both switch positions behave identically and the real parent id is
// recorded — the kill switch must not become a general attribution opt-out.
func TestDelegate_RequireParentAgentID_IrrelevantWhenParentResolves(t *testing.T) {
	for _, tc := range []struct {
		name   string
		strict bool
	}{
		{"guard on", true},
		{"guard off", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool, lc := newRequireParentTestTool(t)
			strict := tc.strict
			tool.SetRequireParentAgentID(func() bool { return strict })

			ctx := WithAgentID(context.Background(), "mia")
			result := tool.Execute(ctx, map[string]any{
				"task": "x", "agent_id": "ray", "async": false,
			})
			if result == nil || result.IsError {
				t.Fatalf("a resolvable parent must always be allowed; got: %+v", result)
			}
			recs := mintedRecords(t, lc)
			if len(recs) != 1 {
				t.Fatalf("expected 1 record, got %d", len(recs))
			}
			if recs[0].ParentAgentID != "mia" {
				t.Errorf("the real parent id must be recorded regardless of the switch, got %q",
					recs[0].ParentAgentID)
			}
		})
	}
}
