// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestBootSweep_RestartInterruptedSession_ReportsInterruptedNotFailed pins the
// founder rule "a session does not fail": a session whose turn was cut off by a
// restart is reported by GET /api/v1/sessions (which reads UnifiedMeta.Status)
// as "interrupted" — never "failed". The e2e spec
// tests/e2e/conformance-design-exec-e2e.spec.ts::Conformance_bootsweep_E2E
// asserts the same wire value after a real kill -9 + restart.
//
// The fixture is a real UnifiedStore session (real meta.json on disk) with a
// task-origin running lifecycle record under the SAME session id, swept through
// the real runBootSweep entry point.
func TestBootSweep_RestartInterruptedSession_ReportsInterruptedNotFailed(t *testing.T) {
	al, agentID := newMetaReconcileTestAgentLoop(t)
	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("GetAgentStore returned nil — test harness misconfigured")
	}
	meta, err := store.NewSession(session.SessionTypeTask, "task", agentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	h := newBootSweepHarness(t)
	h.pe.agentLoop = al
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: agentID, OwnerScopeKind: session.OwnerScopeHuman,
		Origin:    &session.Origin{Kind: session.OriginKindTask, TaskID: "task-1"},
		CreatedAt: time.Now().Add(-time.Hour),
	})

	res := h.pe.runBootSweep(context.Background())
	if len(res.SweptToFailed) != 1 || res.SweptToFailed[0] != meta.ID {
		t.Fatalf("SweptToFailed = %v, want exactly [%s]", res.SweptToFailed, meta.ID)
	}

	got, err := store.GetMeta(meta.ID)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got.Status != session.StatusInterrupted {
		t.Errorf("session status after restart sweep = %q, want %q (a session does not fail)",
			got.Status, session.StatusInterrupted)
	}
}
