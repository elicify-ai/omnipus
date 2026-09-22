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
		// I-1 round 9: every session that steers a child has its own record —
		// the launcher writes an ordinary_root record for it at first
		// delegation. The chain walk (lead review item 1) requires this to
		// resolve `steerer` as the terminal root.
		mustPersistOrdinaryRoot(t, lifecycle, steerer, session.OriginKindChat)
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

	t.Run("row3b_three_level_chain_walks_to_the_real_root_is_steered", func(t *testing.T) {
		lifecycle, unified := newTestClassifierStores(t)
		root := newMetaSession(t, unified, session.SessionTypeChat, "")
		mustPersistOrdinaryRoot(t, lifecycle, root, session.OriginKindChat)
		mid := newMetaSession(t, unified, session.SessionTypeDelegate, root)
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: mid, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: root,
			WorkspaceID: "ws-1", AgentID: "mid-agent",
			Origin:    &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-mid"},
			SteeredBy: &session.SteeredBy{SteeringSessionID: root, RootSessionID: root},
		}); err != nil {
			t.Fatalf("Persist(mid): %v", err)
		}
		leaf := newMetaSession(t, unified, session.SessionTypeDelegate, mid)
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: leaf, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: mid,
			WorkspaceID: "ws-1", AgentID: "leaf-agent",
			Origin:    &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-leaf"},
			SteeredBy: &session.SteeredBy{SteeringSessionID: mid, RootSessionID: root},
		}); err != nil {
			t.Fatalf("Persist(leaf): %v", err)
		}
		c := NewSteerRecordClassifier(lifecycle, unified)

		class, err := c.Classify(context.Background(), leaf)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		if class != steer.ClassSteered {
			t.Fatalf("Classify(three-level chain, real root) = %q, want steered", class)
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

	// Lead review item 1 (CP-0 follow-up): I-8's invalid_edge row also names
	// a cycle, an unknown ancestor, and a wrong root — the chain walk below
	// exercises each, not just the local field/meta-agreement checks above.

	t.Run("row8b_cycle_in_the_ancestor_chain_is_invalid_edge", func(t *testing.T) {
		lifecycle, unified := newTestClassifierStores(t)
		a := newMetaSession(t, unified, session.SessionTypeDelegate, "")
		b := newMetaSession(t, unified, session.SessionTypeDelegate, "")
		mustSetParent(t, unified, a, b)
		mustSetParent(t, unified, b, a)
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: a, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: b,
			WorkspaceID: "ws-1", AgentID: "agent-a",
			Origin:    &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-a"},
			SteeredBy: &session.SteeredBy{SteeringSessionID: b, RootSessionID: "root-that-is-never-reached"},
		}); err != nil {
			t.Fatalf("Persist(a): %v", err)
		}
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: b, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: a,
			WorkspaceID: "ws-1", AgentID: "agent-b",
			Origin:    &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-b"},
			SteeredBy: &session.SteeredBy{SteeringSessionID: a, RootSessionID: "root-that-is-never-reached"},
		}); err != nil {
			t.Fatalf("Persist(b): %v", err)
		}
		c := NewSteerRecordClassifier(lifecycle, unified)

		class, err := c.Classify(context.Background(), a)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		if class != steer.ClassInvalidEdge {
			t.Fatalf("Classify(a in an a<->b cycle) = %q, want invalid_edge", class)
		}
	})

	t.Run("row8c_unknown_ancestor_is_invalid_edge", func(t *testing.T) {
		lifecycle, unified := newTestClassifierStores(t)
		const ghost = "ghost-session-that-was-never-created"
		child := newMetaSession(t, unified, session.SessionTypeDelegate, ghost)
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: child, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: ghost,
			WorkspaceID: "ws-1", AgentID: "worker",
			Origin:    &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-1"},
			SteeredBy: &session.SteeredBy{SteeringSessionID: ghost, RootSessionID: ghost},
		}); err != nil {
			t.Fatalf("Persist: %v", err)
		}
		c := NewSteerRecordClassifier(lifecycle, unified)

		class, err := c.Classify(context.Background(), child)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		if class != steer.ClassInvalidEdge {
			t.Fatalf("Classify(SteeringSessionID names a session with no record at all) = %q, want invalid_edge", class)
		}
	})

	t.Run("row8d_wrong_root_is_invalid_edge", func(t *testing.T) {
		lifecycle, unified := newTestClassifierStores(t)
		root := newMetaSession(t, unified, session.SessionTypeChat, "")
		mustPersistOrdinaryRoot(t, lifecycle, root, session.OriginKindChat)
		mid := newMetaSession(t, unified, session.SessionTypeDelegate, root)
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: mid, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: root,
			WorkspaceID: "ws-1", AgentID: "mid-agent",
			Origin:    &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-mid"},
			SteeredBy: &session.SteeredBy{SteeringSessionID: root, RootSessionID: root},
		}); err != nil {
			t.Fatalf("Persist(mid): %v", err)
		}
		leaf := newMetaSession(t, unified, session.SessionTypeDelegate, mid)
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: leaf, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: mid,
			WorkspaceID: "ws-1", AgentID: "leaf-agent",
			Origin: &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-leaf"},
			SteeredBy: &session.SteeredBy{
				SteeringSessionID: mid,
				// Wrong on purpose: the walked root is `root`, not this.
				RootSessionID: "not-the-real-root",
			},
		}); err != nil {
			t.Fatalf("Persist(leaf): %v", err)
		}
		c := NewSteerRecordClassifier(lifecycle, unified)

		class, err := c.Classify(context.Background(), leaf)
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		if class != steer.ClassInvalidEdge {
			t.Fatalf("Classify(RootSessionID does not match the walked root) = %q, want invalid_edge", class)
		}
	})
}

// Lead review item 2 (CP-0 follow-up): I-8 has no explicit row for a record
// written before ADR-091 (no Origin), a NON-delegate session type, whose
// metadata happens to carry a ParentSessionID for reasons unrelated to
// steering (e.g. an old agent-created task session — ADR-057-era provenance
// this ADR does not interpret as a steering edge). Decision, stated in the
// phase-2 report: treat it as ordinary_root, not damaged_child — refusing
// it at boot would break every existing install with such a session, and
// I-8's own text scopes "written before ADR-091" to Type == delegate only
// (row 6); it never says a non-delegate legacy record with a parent-shaped
// metadata field is unrunnable.
func TestSteerRecordClassifier_LegacyNonDelegateRecordWithParent_IsOrdinaryRoot(t *testing.T) {
	lifecycle, unified := newTestClassifierStores(t)
	oldParent := newMetaSession(t, unified, session.SessionTypeChat, "")
	// A pre-ADR-091 agent-created task session: Type=task (not delegate),
	// ParentSessionID set (ADR-057-era provenance), and a lifecycle record
	// with neither Origin nor SteeredBy — exactly what existed before this
	// ADR landed.
	oldTask := newMetaSession(t, unified, session.SessionTypeTask, oldParent)
	if err := lifecycle.Persist(&session.LifecycleRecord{
		SessionID: oldTask, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	c := NewSteerRecordClassifier(lifecycle, unified)

	class, err := c.Classify(context.Background(), oldTask)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if class != steer.ClassOrdinaryRoot {
		t.Fatalf("Classify(pre-ADR-091 task record, meta has parent) = %q, want ordinary_root (not damaged_child)", class)
	}
}

// mustPersistOrdinaryRoot persists an ordinary_root lifecycle record for id
// (SteeredBy nil, Origin present with the given kind) — the record I-1
// round 9 says every session that has launched a child carries.
func mustPersistOrdinaryRoot(t *testing.T, lifecycle *session.LifecycleStore, id string, kind session.OriginKind) {
	t.Helper()
	if err := lifecycle.Persist(&session.LifecycleRecord{
		SessionID: id, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-1", AgentID: "root-agent",
		Origin: &session.Origin{Kind: kind},
	}); err != nil {
		t.Fatalf("mustPersistOrdinaryRoot(%q): %v", id, err)
	}
}

// mustSetParent stamps id's own UnifiedMeta.ParentSessionID to parentID.
func mustSetParent(t *testing.T, us *session.UnifiedStore, id, parentID string) {
	t.Helper()
	if err := us.SetMeta(id, session.MetaPatch{ParentSessionID: &parentID}); err != nil {
		t.Fatalf("SetMeta(%q).ParentSessionID: %v", id, err)
	}
}

// TestSteerRecordClassifier_ImplementsInterface is a compile-time-adjacent
// proof that *SteerRecordClassifier satisfies steer.RecordClassifier.
func TestSteerRecordClassifier_ImplementsInterface(t *testing.T) {
	var _ steer.RecordClassifier = (*SteerRecordClassifier)(nil)
}
