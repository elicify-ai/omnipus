// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"path/filepath"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestComputeSessionLifecycle_RestartInterruptedIsInterruptedNotFailed pins the
// founder ruling (2026-10-06): the Session.lifecycle_state a client sees for a
// session a restart cut off (lifecycle failed with failed_reason "interrupted",
// the boot sweep's record) is "interrupted", while a genuinely failed record
// (any other failed_reason) is still "failed".
func TestComputeSessionLifecycle_RestartInterruptedIsInterruptedNotFailed(t *testing.T) {
	ls := session.NewLifecycleStore(filepath.Join(t.TempDir(), "session_lifecycle"))
	cases := []struct {
		id     string
		reason string
		want   gen.SessionLifecycleState
	}{
		{"sess-restart", "interrupted", gen.SessionLifecycleStateInterrupted},
		{"sess-real-failure", "judge_rounds_exhausted", gen.SessionLifecycleStateFailed},
	}
	for _, c := range cases {
		if err := ls.Persist(&session.LifecycleRecord{
			SessionID: c.id, Generation: 1, State: session.LifecycleFailed, FailedReason: c.reason,
			WorkspaceID: "ws", AgentID: "agent-1", OwnerScopeKind: session.OwnerScopeHuman,
			CreatedAt: time.Now().Add(-time.Hour),
		}); err != nil {
			t.Fatalf("persist %s: %v", c.id, err)
		}
	}
	for _, c := range cases {
		state, _ := computeSessionLifecycle(ls, c.id)
		if state == nil {
			t.Fatalf("%s: lifecycle_state absent, want %q", c.id, c.want)
		}
		if *state != c.want {
			t.Errorf("%s (failed_reason=%q): lifecycle_state = %q, want %q", c.id, c.reason, *state, c.want)
		}
	}
}
