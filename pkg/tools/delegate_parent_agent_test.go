// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// FR-013/FR-015/FR-034 — the delegate lifecycle mint stamps ParentAgentID
// from ToolAgentID(ctx), fails CLOSED when it cannot resolve one, and
// carries it forward onto every generation mint.
//
// Every assertion here is on an OUTCOME (what exists on disk afterwards),
// never on whether a guard ran: the negative test proves NO record file was
// created, and the positive tests read the persisted record back.

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// lifecycleFilesOnDisk lists the .jsonl record files physically present in
// the store's directory. Used instead of Load/List so a "no record was
// written" assertion cannot be satisfied by a record that exists but happens
// to be unreadable or filtered out.
func lifecycleFilesOnDisk(t *testing.T, lc *session.LifecycleStore) []string {
	t.Helper()
	entries, err := os.ReadDir(lc.Dir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read lifecycle dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			out = append(out, e.Name())
		}
	}
	return out
}

// soleLifecycleRecord returns the single persisted lifecycle record, failing
// if there is not exactly one.
func soleLifecycleRecord(t *testing.T, lc *session.LifecycleStore) session.LifecycleRecord {
	t.Helper()
	recs, err := lc.List(session.LifecycleFilter{})
	if err != nil {
		t.Fatalf("list lifecycle records: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected exactly 1 lifecycle record, got %d", len(recs))
	}
	return recs[0]
}

// TestDelegateMint_FailsClosedOnEmptyParentAgentID — FR-015. When
// ToolAgentID(ctx) cannot be resolved (missing key, or a value that trims to
// empty), the mint MUST fail closed: an error is returned to the caller and
// NO lifecycle record is written. ToolAgentID returns "" for both a missing
// context key and a wrong-typed value (comma-ok, error discarded), so the
// unset case and the garbage case are indistinguishable to the mint site and
// must be treated identically.
func TestDelegateMint_FailsClosedOnEmptyParentAgentID(t *testing.T) {
	cases := []struct {
		name string
		ctx  func() context.Context
	}{
		{
			name: "agent id never injected",
			ctx: func() context.Context {
				return WithTranscriptSessionID(context.Background(), "parent-transcript")
			},
		},
		{
			name: "agent id present but empty",
			ctx: func() context.Context {
				return WithAgentID(WithTranscriptSessionID(context.Background(), "parent-transcript"), "")
			},
		},
		{
			name: "agent id whitespace only",
			ctx: func() context.Context {
				return WithAgentID(WithTranscriptSessionID(context.Background(), "parent-transcript"), " \t\n ")
			},
		},
		{
			name: "agent id present but wrong-typed",
			ctx: func() context.Context {
				// A caller that stashed a non-string under the agent-id key.
				// ToolAgentID's comma-ok assertion discards the type failure
				// and silently returns "" — indistinguishable from "unset",
				// so it must fail closed too rather than mint an orphan.
				return context.WithValue(
					WithTranscriptSessionID(context.Background(), "parent-transcript"),
					ctxKeyAgentID, 12345)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, lc, _, _ := newADR053TestTool(t)

			result := tool.Execute(tc.ctx(), map[string]any{"task": "do something", "agent_id": "ray"})

			if result == nil || !result.IsError {
				t.Fatalf("expected the delegation to be REFUSED with an unresolvable parent agent id, got: %+v", result)
			}
			// The refusal must name the cause so an operator can act on it.
			if !strings.Contains(result.ForLLM, "delegating agent") {
				t.Errorf("expected the error to name the unresolvable delegating agent, got: %s", result.ForLLM)
			}
			// The outcome that actually matters: nothing was persisted.
			if files := lifecycleFilesOnDisk(t, lc); len(files) != 0 {
				t.Fatalf("a lifecycle record was written despite the refusal: %v — "+
					"an unattributable record can never be returned to its parent by list_jobs", files)
			}
		})
	}
}

// TestDelegateMint_StampsParentAgentID — FR-013/FR-034, parameterised over
// EVERY mint site so a future mint path cannot silently orphan a session
// from its parent's roster while the suite stays green.
func TestDelegateMint_StampsParentAgentID(t *testing.T) {
	const (
		parentAgent = "mia"
		childAgent  = "ray"
		transcript  = "shared-transcript-1"
	)

	// Mint site 1 + 2: `delegate.run`, async and sync. The two share one
	// mint site today, but they are exercised separately so a future split
	// of the dispatch paths is caught.
	for _, async := range []bool{true, false} {
		name := "run async"
		if !async {
			name = "run sync"
		}
		t.Run(name, func(t *testing.T) {
			tool, lc, _, _ := newADR053TestTool(t)
			ctx := WithAgentID(WithTranscriptSessionID(context.Background(), transcript), parentAgent)

			result := tool.Execute(ctx, map[string]any{
				"task": "do something", "agent_id": childAgent, "async": async,
			})
			if result.IsError {
				t.Fatalf("delegation failed: %s", result.ForLLM)
			}

			rec := soleLifecycleRecord(t, lc)
			if rec.ParentAgentID != parentAgent {
				t.Errorf("ParentAgentID = %q, want %q (the DELEGATING agent)", rec.ParentAgentID, parentAgent)
			}
			// The child's own id is a different field entirely.
			if rec.AgentID != childAgent {
				t.Errorf("AgentID = %q, want %q (the delegate TARGET)", rec.AgentID, childAgent)
			}
			// The linkage must be genuinely distinct from the shared
			// transcript key — this is the whole reason the field exists.
			if rec.ParentDurableKey != transcript {
				t.Fatalf("ParentDurableKey = %q, want %q (fixture broken)", rec.ParentDurableKey, transcript)
			}
			if rec.ParentAgentID == rec.ParentDurableKey {
				t.Errorf("ParentAgentID must differ from ParentDurableKey (which parent and child SHARE); both = %q", rec.ParentAgentID)
			}

			// And it must be physically present on disk under the key
			// list_jobs will read.
			assertRawParentAgentID(t, lc, rec.SessionID, parentAgent)
		})
	}

	// Mint site 3: the follow_up/Play generation mint. ParentAgentID is
	// CARRIED FORWARD from the prior generation, never re-sourced from the
	// follow_up caller's own context — proven here by having a DIFFERENT
	// agent issue the follow_up and asserting the parent is unchanged.
	t.Run("follow_up generation mint carries the parent forward", func(t *testing.T) {
		tool, lc, _, _ := newADR053TestTool(t)
		ctx := WithAgentID(WithTranscriptSessionID(context.Background(), transcript), parentAgent)

		sessionID := runAndExtractSessionID(t, tool, ctx, "background task")
		waitForTerminalLifecycle(t, lc, sessionID)

		// A different agent, same transcript session (see the note in the
		// report: verifyCallerOwnsSession keys on the SHARED
		// ParentDurableKey, so this is currently permitted).
		otherCtx := WithAgentID(WithTranscriptSessionID(context.Background(), transcript), "jim")
		result := tool.Execute(otherCtx, map[string]any{
			"action": "follow_up", "session_id": sessionID, "task": "one more thing",
		})
		if result.IsError {
			t.Fatalf("follow_up failed: %s", result.ForLLM)
		}

		rec, err := lc.Load(sessionID)
		if err != nil {
			t.Fatalf("load after follow_up: %v", err)
		}
		if rec.Generation < 1 {
			t.Fatalf("Generation = %d, want a new generation (>=1)", rec.Generation)
		}
		if rec.ParentAgentID != parentAgent {
			t.Errorf("ParentAgentID = %q after a follow_up issued by %q, want the ORIGINAL parent %q "+
				"— a generation mint must carry the parent forward, never re-source it from the caller",
				rec.ParentAgentID, "jim", parentAgent)
		}
		assertRawParentAgentID(t, lc, sessionID, parentAgent)
	})
}

// assertRawParentAgentID reads the raw tail JSONL line and asserts the
// parent_agent_id key is present with the expected value. Reading the raw
// bytes is deliberate: unmarshalling into the struct cannot distinguish an
// absent key from a present-but-empty one.
func assertRawParentAgentID(t *testing.T, lc *session.LifecycleStore, sessionID, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(lc.Dir(), sessionID+".jsonl"))
	if err != nil {
		t.Fatalf("read raw lifecycle file: %v", err)
	}
	var last string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			last = line
		}
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(last), &raw); err != nil {
		t.Fatalf("decode raw JSONL: %v\nline: %s", err, last)
	}
	val, ok := raw["parent_agent_id"]
	if !ok {
		t.Fatal("raw JSONL is missing the parent_agent_id key — the field must not carry `omitempty` (FR-015)")
	}
	var got string
	if err := json.Unmarshal(val, &got); err != nil {
		t.Fatalf("decode parent_agent_id: %v", err)
	}
	if got != want {
		t.Errorf("raw parent_agent_id = %q, want %q", got, want)
	}
}

// waitForTerminalLifecycle blocks until sessionID's record reaches a terminal
// state (the async delegation goroutine drives queued→running→completed).
func waitForTerminalLifecycle(t *testing.T, lc *session.LifecycleStore, sessionID string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		rec, err := lc.Load(sessionID)
		if err == nil && rec.Terminal() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("session %s never reached a terminal lifecycle state", sessionID)
}
