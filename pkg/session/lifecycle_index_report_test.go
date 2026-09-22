// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 landing order I-9 — coverage for LifecycleIndex.Report():
// ensureWarm must record every record it could not load during its
// backfill scan, rather than only logging and silently skipping it, so an
// operator-facing consumer (WP-D's boot sweep) can see what the index
// dropped. POSIX-only: uses chmod 0000 to make a real record file
// genuinely unreadable, mirroring this package's own existing precedent
// (TestParentIndex_SteadyStateQueryNeverTouchesUnrelatedSessionFiles).

package session

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLifecycleIndex_Report_EmptyBeforeAnyWarm(t *testing.T) {
	s := newTestLifecycleStore(t)
	rep := s.IndexReport()
	if len(rep.Unreadable) != 0 {
		t.Fatalf("IndexReport().Unreadable = %v, want empty before ensureWarm has ever run", rep.Unreadable)
	}
}

func TestLifecycleIndex_Report_AllMalformedJSONL(t *testing.T) {
	s := newTestLifecycleStore(t)
	const id = "child-report-all-malformed"
	if err := os.WriteFile(filepath.Join(s.Dir(), id+".jsonl"), []byte("{not-json}\n[]\n"), 0o600); err != nil {
		t.Fatalf("write malformed lifecycle file: %v", err)
	}
	if _, err := s.List(LifecycleFilter{SteeringSessionID: "trigger-warm"}); err != nil {
		t.Fatalf("List must continue past one malformed record: %v", err)
	}
	report := s.IndexReport()
	if len(report.Unreadable) != 1 || report.Unreadable[0].ID != id || report.Unreadable[0].Err == nil {
		t.Fatalf("IndexReport().Unreadable = %+v, want malformed record %q", report.Unreadable, id)
	}
}

// TestLifecycleIndex_Report_ListsUnreadableRecordFromCorruptFile is the I-9
// scenario from the TDD plan (test 25 / FR-A-014): one lifecycle file is
// corrupt (here: unreadable), the index warms, and Report() lists that
// session id and the error instead of silently dropping it.
func TestLifecycleIndex_Report_ListsUnreadableRecordFromCorruptFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only: relies on file permission bits to make a record file genuinely unreadable")
	}
	s := newTestLifecycleStore(t)
	const parent = "chat-report-i9"
	const good = "child-report-i9-good"
	const corrupt = "child-report-i9-corrupt"

	if err := s.Persist(&LifecycleRecord{
		SessionID: good, State: LifecycleRunning,
		OwnerScopeKind: OwnerScopeHuman, SteeredBy: &SteeredBy{SteeringSessionID: parent},
		WorkspaceID: "ws-1", AgentID: "ray",
	}); err != nil {
		t.Fatalf("seed %q: %v", good, err)
	}
	// Deliberately a DIFFERENT SteeringSessionID (or none) — ensureWarm's
	// backfill scan (triggered by the List query below) still walks every
	// persisted session_id regardless of parent, but this way `corrupt`
	// is never one of `parent`'s indexed children, so the later
	// List(SteeringSessionID: parent) query never tries to re-Load it
	// directly (that path's own stale-entry self-heal only understands
	// ErrLifecycleNotFound, not an arbitrary permission error — a
	// pre-existing, out-of-scope-for-I-9 behavior this test does not
	// exercise). This isolates the fact under test: ensureWarm's scan
	// itself must record the unreadable record, not silently drop it.
	if err := s.Persist(&LifecycleRecord{
		SessionID: corrupt, State: LifecycleRunning,
		OwnerScopeKind: OwnerScopeHuman,
		WorkspaceID:    "ws-1", AgentID: "ray",
	}); err != nil {
		t.Fatalf("seed %q: %v", corrupt, err)
	}

	corruptPath := filepath.Join(s.Dir(), corrupt+".jsonl")
	if err := os.Chmod(corruptPath, 0o000); err != nil {
		t.Fatalf("chmod corrupt file: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(corruptPath, 0o600) })

	// Sanity: prove the file is actually unreadable by this process — if
	// not (e.g. running as root), skip rather than pass vacuously.
	if _, err := os.ReadFile(corruptPath); err == nil {
		t.Skip("corrupt fixture is still readable by this process (likely running as root)")
	} else if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected a permission error reading a chmod 0000 file, got: %v", err)
	}

	// Any SteeringSessionID query triggers ensureWarm's one-time backfill
	// scan, which is what walks every persisted session_id including the
	// corrupt one.
	list, err := s.List(LifecycleFilter{SteeringSessionID: parent})
	if err != nil {
		t.Fatalf("List must not fail outright because an UNRELATED record is unreadable: %v", err)
	}
	if len(list) != 1 || list[0].SessionID != good {
		t.Fatalf("List(SteeringSessionID=%q) = %v, want exactly [%q]", parent, list, good)
	}

	rep := s.IndexReport()
	if len(rep.Unreadable) != 1 {
		t.Fatalf("IndexReport().Unreadable = %v, want exactly one entry for %q", rep.Unreadable, corrupt)
	}
	got := rep.Unreadable[0]
	if got.ID != corrupt {
		t.Errorf("Unreadable[0].ID = %q, want %q", got.ID, corrupt)
	}
	if got.Err == nil {
		t.Error("Unreadable[0].Err = nil, want the load error that made the record unreadable")
	}

	// The good sibling must NOT appear in the unreadable report.
	for _, u := range rep.Unreadable {
		if u.ID == good {
			t.Errorf("IndexReport().Unreadable unexpectedly includes the readable sibling %q", good)
		}
	}
}
