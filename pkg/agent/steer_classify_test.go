// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 landing order I-8 — one table test per row of the classification
// table (TDD plan test 12,
// TestClassifyRecord_AllClasses_RecordMetaConsistency), each built against
// REAL *session.LifecycleStore / *session.UnifiedStore instances, never a
// spy standing in for either store.

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func newTestClassifierStores(t *testing.T) (*session.LifecycleStore, *session.UnifiedStore) {
	t.Helper()
	lifecycle := session.NewLifecycleStore(t.TempDir())
	unified, err := session.NewUnifiedStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	return lifecycle, unified
}

// newMetaSession creates a session of sessionType with a store-assigned id
// (no real parent required) and, when parentID != "", stamps
// ParentSessionID on it directly. Returns the assigned id.
func newMetaSession(t *testing.T, us *session.UnifiedStore, sessionType session.UnifiedSessionType, parentID string) string {
	t.Helper()
	meta, err := us.NewSession(sessionType, "webchat", "agent-1")
	if err != nil {
		t.Fatalf("NewSession(%s): %v", sessionType, err)
	}
	if parentID != "" {
		if err := us.SetMeta(meta.ID, session.MetaPatch{ParentSessionID: &parentID}); err != nil {
			t.Fatalf("SetMeta(%q).ParentSessionID: %v", meta.ID, err)
		}
	}
	return meta.ID
}

// TestSteerRecordClassifier_AllEightRows exercises every row of landing
// order I-8's classification table.
func TestSteerRecordClassifier_AllEightRows(t *testing.T) {
	t.Run("row1_no_record_meta_has_no_parent_and_not_delegate_type_is_ordinary_root", func(t *testing.T) {
		lifecycle, unified := newTestClassifierStores(t)
		id := newMetaSession(t, unified, session.SessionTypeChat, "")
		c := NewSteerRecordClassifier(lifecycle, unified)

		class, err := c.Classify(context.Background(), id)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		if class != steer.ClassOrdinaryRoot {
			t.Fatalf("Classify(no record, meta=chat/no-parent) = %q, want ordinary_root", class)
		}
	})

	t.Run("row2_record_present_steeredby_nil_origin_present_meta_agrees_is_ordinary_root", func(t *testing.T) {
		lifecycle, unified := newTestClassifierStores(t)
		id := newMetaSession(t, unified, session.SessionTypeTask, "")
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: id, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-1", AgentID: "worker",
			Origin: &session.Origin{Kind: session.OriginKindTask},
		}); err != nil {
			t.Fatalf("Persist: %v", err)
		}
		c := NewSteerRecordClassifier(lifecycle, unified)

		class, err := c.Classify(context.Background(), id)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		if class != steer.ClassOrdinaryRoot {
			t.Fatalf("Classify(record, SteeredBy=nil, Origin=task, meta agrees) = %q, want ordinary_root", class)
		}
	})

	t.Run("row3_record_present_valid_steeredby_meta_agrees_is_steered", func(t *testing.T) {
		lifecycle, unified := newTestClassifierStores(t)
		steerer := newMetaSession(t, unified, session.SessionTypeChat, "")
		child := newMetaSession(t, unified, session.SessionTypeDelegate, steerer)
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: child, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: steerer,
			WorkspaceID: "ws-1", AgentID: "worker",
			Origin: &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-1"},
			SteeredBy: &session.SteeredBy{
				SteeringSessionID: steerer,
				RootSessionID:     steerer,
			},
		}); err != nil {
			t.Fatalf("Persist: %v", err)
		}
		c := NewSteerRecordClassifier(lifecycle, unified)

		class, err := c.Classify(context.Background(), child)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		if class != steer.ClassSteered {
			t.Fatalf("Classify(valid SteeredBy, meta agrees) = %q, want steered", class)
		}
	})

	t.Run("row4_no_record_meta_has_parent_is_damaged_child", func(t *testing.T) {
		lifecycle, unified := newTestClassifierStores(t)
		steerer := newMetaSession(t, unified, session.SessionTypeChat, "")
		child := newMetaSession(t, unified, session.SessionTypeDelegate, steerer)
		// Deliberately NO lifecycle record for child.
		c := NewSteerRecordClassifier(lifecycle, unified)

		class, err := c.Classify(context.Background(), child)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		if class != steer.ClassDamagedChild {
			t.Fatalf("Classify(no record, meta has parent) = %q, want damaged_child", class)
		}
	})

	t.Run("row5_record_present_steeredby_nil_origin_present_meta_has_parent_is_damaged_child_edge_lost", func(t *testing.T) {
		lifecycle, unified := newTestClassifierStores(t)
		steerer := newMetaSession(t, unified, session.SessionTypeChat, "")
		child := newMetaSession(t, unified, session.SessionTypeDelegate, steerer)
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: child, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-1", AgentID: "worker",
			// Origin present (written by ADR-091 code) but SteeredBy was
			// lost — the edge-lost case, distinct from row 6's legacy case.
			Origin: &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-1"},
		}); err != nil {
			t.Fatalf("Persist: %v", err)
		}
		c := NewSteerRecordClassifier(lifecycle, unified)

		class, err := c.Classify(context.Background(), child)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		if class != steer.ClassDamagedChild {
			t.Fatalf("Classify(SteeredBy=nil, Origin present, meta has parent) = %q, want damaged_child", class)
		}
	})

	t.Run("row6_record_present_steeredby_nil_origin_absent_delegate_type_is_legacy_delegate", func(t *testing.T) {
		lifecycle, unified := newTestClassifierStores(t)
		steerer := newMetaSession(t, unified, session.SessionTypeChat, "")
		child := newMetaSession(t, unified, session.SessionTypeDelegate, steerer)
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: child, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-1", AgentID: "worker",
			// No Origin at all — a record written before ADR-091.
		}); err != nil {
			t.Fatalf("Persist: %v", err)
		}
		c := NewSteerRecordClassifier(lifecycle, unified)

		class, err := c.Classify(context.Background(), child)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		if class != steer.ClassLegacyDelegate {
			t.Fatalf("Classify(SteeredBy=nil, Origin=nil, type=delegate) = %q, want legacy_delegate", class)
		}
	})

	t.Run("row7_record_unreadable_is_unreadable", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX-only: relies on file permission bits to make a record file genuinely unreadable")
		}
		lifecycle, unified := newTestClassifierStores(t)
		id := newMetaSession(t, unified, session.SessionTypeDelegate, "")
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: id, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-1", AgentID: "worker",
		}); err != nil {
			t.Fatalf("Persist: %v", err)
		}
		path := filepath.Join(lifecycle.Dir(), id+".jsonl")
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
		if _, err := os.ReadFile(path); err == nil {
			t.Skip("record file still readable by this process (likely running as root)")
		} else if !errors.Is(err, os.ErrPermission) {
			t.Fatalf("expected a permission error, got: %v", err)
		}
		c := NewSteerRecordClassifier(lifecycle, unified)

		class, err := c.Classify(context.Background(), id)
		if err == nil {
			t.Fatal("Classify(unreadable record) returned nil error, want the load failure")
		}
		if class != steer.ClassUnreadable {
			t.Fatalf("Classify(unreadable record) = %q, want unreadable", class)
		}
	})

	t.Run("row8_steeredby_present_but_meta_disagrees_is_invalid_edge", func(t *testing.T) {
		lifecycle, unified := newTestClassifierStores(t)
		steerer := newMetaSession(t, unified, session.SessionTypeChat, "")
		imposter := newMetaSession(t, unified, session.SessionTypeChat, "")
		child := newMetaSession(t, unified, session.SessionTypeDelegate, imposter)
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: child, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: steerer,
			WorkspaceID: "ws-1", AgentID: "worker",
			Origin: &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-1"},
			SteeredBy: &session.SteeredBy{
				// The record's edge names `steerer`, but the session's own
				// metadata ParentSessionID names `imposter` — disagreement
				// (R02: record and metadata must agree).
				SteeringSessionID: steerer,
				RootSessionID:     steerer,
			},
		}); err != nil {
			t.Fatalf("Persist: %v", err)
		}
		c := NewSteerRecordClassifier(lifecycle, unified)

		class, err := c.Classify(context.Background(), child)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		if class != steer.ClassInvalidEdge {
			t.Fatalf("Classify(SteeredBy present, meta disagrees) = %q, want invalid_edge", class)
		}
	})
}

// TestSteerRecordClassifier_ImplementsInterface is a compile-time-adjacent
// proof that *SteerRecordClassifier satisfies steer.RecordClassifier.
func TestSteerRecordClassifier_ImplementsInterface(t *testing.T) {
	var _ steer.RecordClassifier = (*SteerRecordClassifier)(nil)
}
