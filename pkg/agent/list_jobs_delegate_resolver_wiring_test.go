// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// list_jobs_delegate_resolver_wiring_test.go is the #583 regression test for
// the PRODUCTION wiring gap: list_jobs always reported actionable:false for
// running subagents because wireJobRosterForAgent (pkg/agent/loop.go) never
// called listJobs.SetSessionResolver(...) — DelegateTool had no
// ResolvableSessionIDs method to wire in the first place. pkg/tools'
// TestDelegateTool_ResolvableSessionIDs proves the new method itself is
// correct in isolation; this test proves the GLUE — that a real running
// agent's own *tools.DelegateTool is actually reachable from list_jobs via
// the resolver closure wireJobRosterForAgent installs.
package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// blockingTestSpawner implements tools.SubTurnSpawner and blocks until
// release is closed, so a delegated session started in this test stays in
// LifecycleRunning (non-terminal) for the whole assertion window — actionable
// tracks BOTH non-terminal status AND resolvability (list_jobs_sources.go),
// so a session that raced to "completed" before the check would make the
// test meaningless.
type blockingTestSpawner struct {
	release chan struct{}
}

func (b *blockingTestSpawner) SpawnSubTurn(ctx context.Context, _ tools.SubTurnConfig) (*tools.ToolResult, error) {
	select {
	case <-b.release:
		return &tools.ToolResult{ForLLM: "done"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestWireJobRoster_SubagentActionable_ReflectsDelegateSessionIndex proves
// list_jobs(kind="subagent") reports actionable:true for a genuinely running
// delegated session dispatched through the SAME agent's real, production-
// wired *tools.DelegateTool — exercising wireJobRosterForAgent's
// SetSessionResolver closure end to end, not just DelegateTool's own method.
func TestWireJobRoster_SubagentActionable_ReflectsDelegateSessionIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(home, "agents"),
				DefaultModel: config.DefaultModel{Model: "test-model"},
			},
			List: []config.AgentConfig{{ID: "mia", Home: filepath.Join(home, "agents")}},
		},
	}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })

	// Wire the REAL durable lifecycle store — SetSessionMessagingStores
	// re-wires it onto every currently-registered agent's delegate tool
	// (session_messaging_wire.go's wireSessionMessagingForAgent) AND is what
	// agentLoopJobLifecycleLister (list_jobs' subagent-kind reader) reads
	// from — the SAME instance, so a session minted via one is visible to
	// the other, exactly like production boot wiring.
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "inbox"))
	al.SetSessionMessagingStores(inbox, lifecycle)

	inst, ok := al.GetRegistry().GetAgent("mia")
	if !ok {
		t.Fatal("test setup: default agent 'mia' not registered")
	}
	rawDelegate, ok := inst.Tools.Get("delegate")
	if !ok {
		t.Fatal("test setup: delegate tool was not registered by registerSharedTools")
	}
	delegateTool, ok := rawDelegate.(*tools.DelegateTool)
	if !ok {
		t.Fatalf("test setup: delegate tool has unexpected type %T", rawDelegate)
	}
	// Bypass the real workspace delegation-trust gate — this test is about
	// the list_jobs<->delegate WIRING, not trust-graph semantics (covered
	// elsewhere).
	delegateTool.SetDelegationDenyCheckerBackground(func(context.Context, string) *tools.DelegationDenial { return nil })

	spawner := &blockingTestSpawner{release: make(chan struct{})}
	delegateTool.SetSpawner(spawner)
	t.Cleanup(func() {
		close(spawner.release)
		delegateTool.WaitForAsyncTasks()
	})

	runCtx := tools.WithAgentID(context.Background(), "mia")
	runResult := delegateTool.Execute(runCtx, map[string]any{"task": "a long running review", "async": true})
	if runResult.IsError {
		t.Fatalf("delegate run failed: %s", runResult.ForLLM)
	}

	// Poll list_jobs until the row appears — the async dispatch goroutine's
	// lifecycle transition to "running" is not synchronous with Execute
	// returning.
	deadline := time.Now().Add(5 * time.Second)
	var lastBody string
	for time.Now().Before(deadline) {
		res := inst.Tools.Execute(runCtx, "list_jobs", map[string]any{"kind": "subagent"})
		if res == nil || res.IsError {
			t.Fatalf("list_jobs failed: %+v", res)
		}
		lastBody = res.ForLLM

		var payload struct {
			Rows []struct {
				ID         string `json:"id"`
				Status     string `json:"status"`
				Actionable bool   `json:"actionable"`
			} `json:"rows"`
		}
		if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
			t.Fatalf("could not parse list_jobs result %q: %v", res.ForLLM, err)
		}
		if len(payload.Rows) == 1 && payload.Rows[0].Status == "running" {
			if !payload.Rows[0].Actionable {
				t.Fatalf("#583: expected actionable=true for a running subagent whose session_id is live "+
					"in the delegate tool's own in-memory index (SetSessionResolver must be wired by "+
					"wireJobRosterForAgent) — got actionable=false, row=%+v", payload.Rows[0])
			}
			return // success
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("BLOCKED: the delegated session never showed up as a single running subagent row within the "+
		"deadline; last list_jobs body: %s", lastBody)
}

// TestWireJobRoster_SubagentActionable_FalseForUnknownSession is a narrow
// sanity check that the resolver wiring does not report actionable:true
// unconditionally — reusing the SAME setup, a lifecycle record NOT dispatched
// through the delegate tool's own sessionIndex (as if the process restarted
// and the durable record survived but the in-memory index did not) must
// report actionable:false.
func TestWireJobRoster_SubagentActionable_FalseForUnknownSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(home, "agents"),
				DefaultModel: config.DefaultModel{Model: "test-model"},
			},
			List: []config.AgentConfig{{ID: "mia", Home: filepath.Join(home, "agents")}},
		},
	}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })

	lifecycle := session.NewLifecycleStore(filepath.Join(home, "lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "inbox"))
	al.SetSessionMessagingStores(inbox, lifecycle)

	// Seed a running record directly into the durable store, bypassing the
	// delegate tool entirely — this simulates a record that survived a
	// process restart while the in-memory sessionIndex did not.
	if err := lifecycle.Persist(&session.LifecycleRecord{
		SessionID: "orphaned-after-restart", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentAgentID: "mia",
		ParentDurableKey: "some-parent-transcript", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	inst, ok := al.GetRegistry().GetAgent("mia")
	if !ok {
		t.Fatal("test setup: default agent 'mia' not registered")
	}
	runCtx := tools.WithAgentID(context.Background(), "mia")
	res := inst.Tools.Execute(runCtx, "list_jobs", map[string]any{"kind": "subagent"})
	if res == nil || res.IsError {
		t.Fatalf("list_jobs failed: %+v", res)
	}
	var payload struct {
		Rows []struct {
			ID         string `json:"id"`
			Actionable bool   `json:"actionable"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
		t.Fatalf("could not parse list_jobs result: %v (body=%s)", err, res.ForLLM)
	}
	found := false
	for _, row := range payload.Rows {
		if row.ID != "orphaned-after-restart" {
			continue
		}
		found = true
		if row.Actionable {
			t.Errorf("expected actionable=false for a durable record with no live in-memory delegate index "+
				"entry, got true: %+v", row)
		}
	}
	if !found {
		t.Fatalf("expected the seeded session to appear as a row, got: %s", res.ForLLM)
	}
}
