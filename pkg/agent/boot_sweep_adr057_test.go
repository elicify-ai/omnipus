// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 FR-078 / BDD-87's no-orphan-directory requirement remains: a
// refused transcript append to an unminted child must create nothing.
// Frozen ADR-20260928 D8/D8.3 supersedes the old terminal reconciliation
// oracle for that steered child. PlanEngine.bootSweep leaves its complete
// lifecycle and journal untouched across reopening the real store;
// SteerBootRecovery is responsible for the non-terminal restart stop.
package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// u19NewUnminted is the child session id BDD-87 requires never got minted —
// delegate.go's lifecycle Persist ran, but session.CreateSessionWithID never
// did, because the process crashed in between.
const u19UnmintedChildID = "u19-child-crashed-before-mint"

// u19SteeringSessionID is the delegating chat's own durable id — distinct
// from the child id per the spec's own "distinct ids everywhere" corollary
// (FR-074).
const u19SteeringSessionID = "u19-parent-chat-durable-key"

// TestBootSweep_ReconcilesChildAcrossRestart is test #65
// (TestBootSweep_ReconcilesChildAcrossRestart, BDD-87, FR-078).
func TestBootSweep_ReconcilesChildAcrossRestart(t *testing.T) {
	h := newBootSweepHarness(t)

	// (1) Persist a lifecycle record shaped exactly like delegate.go's
	// executeRun mint (pkg/tools/delegate.go:1166-1180) for a ROOT-level
	// delegation: OwnerScopeParentSession/SteeringSessionID set, State=queued
	// (the state a mint-then-crash leaves behind — the spawn goroutine that
	// would advance it to `running` never got to run), no OwnsPlanID/GoalRef
	// (a plain delegate child is not a plan-owner or goal-bearing session,
	// so neither boot-sweep exemption applies).
	// Generation: 1 — the "crashed before mint" this test models is the
	// CHILD's own TRANSCRIPT session (session.CreateSessionWithID, minted
	// later during the actual spawn) never being created, NOT the lifecycle
	// record's Generation: SteerLauncher.Launch writes the durable record —
	// with Generation already stamped 1 — as its FIRST, atomic write, before
	// any turn (and therefore before the transcript mint) ever runs. A
	// lifecycle record with Generation 0 is not a state delegate.go's real
	// mint can produce at any point, crash or not.
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID:      u19UnmintedChildID,
		Generation:     1,
		State:          session.LifecycleQueued,
		OwnerScopeKind: session.OwnerScopeParentSession,
		OwnerScopeID:   u19SteeringSessionID,
		ParentAgentID:  "parent-agent",
		SteeredBy:      &session.SteeredBy{SteeringSessionID: u19SteeringSessionID, RootSessionID: u19SteeringSessionID},
		AgentID:        "child-agent",
		CreatedAt:      time.Now().Add(-1 * time.Minute),
	})

	beforeSweep := snapshotBootSweepRecord(t, h.ls, u19UnmintedChildID)
	// Reopen the real lifecycle store: no in-memory fixture can preserve a
	// record the on-disk sweep has actually rewritten.
	h.ls = session.NewLifecycleStore(h.ls.Dir())
	h.pe.SetLifecycleStore(h.ls)
	result := h.pe.runBootSweep(context.Background())
	assertBootSweepRecordUntouched(t, h.ls, u19UnmintedChildID, beforeSweep)
	if result.Scanned != 1 {
		t.Errorf("Scanned = %d, want 1 (the durable queued child)", result.Scanned)
	}
	if len(result.SweptToFailed) != 0 {
		t.Errorf("SweptToFailed = %v, want none — D8.3 leaves the steered child to SteerBootRecovery", result.SweptToFailed)
	}
	reconciled, err := h.ls.Load(u19UnmintedChildID)
	if err != nil {
		t.Fatalf("load protected child record: %v", err)
	}
	if reconciled.State != session.LifecycleQueued || reconciled.Terminal() || reconciled.FailedReason != "" {
		t.Errorf("child after plan sweep = %q/%q terminal=%v, want queued with no failed reason and non-terminal (D8.3)", reconciled.State, reconciled.FailedReason, reconciled.Terminal())
	}

	// (3) The second FR-078 clause: even AFTER the boot sweep ran, a
	// transcript write against the SAME un-minted child id is refused, and
	// creates no directory anywhere on disk. Uses a REAL *session.UnifiedStore
	// rooted at its own dedicated temp dir — the child's own transcript store
	// is a wholly separate on-disk tree from the lifecycle store above,
	// exactly mirroring production (session_lifecycle/ vs the per-agent
	// session store), so this proves the boot sweep does not, and cannot,
	// retroactively mint the child's transcript session out of the reconciled
	// lifecycle record.
	transcriptBase := t.TempDir()
	us, err := session.NewUnifiedStore(transcriptBase)
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := us.Close(); closeErr != nil {
			t.Errorf("UnifiedStore.Close: %v", closeErr)
		}
	})

	// Snapshot the tree right after store construction, BEFORE the refused
	// write attempt: NewUnifiedStore itself creates a store-internal
	// ".context" bootstrap directory (pkg/session/unified.go:395) — a
	// reserved name validateSessionID itself refuses as a session id
	// (unified.go:361) — which has nothing to do with this session's write.
	// The "wrote nothing" proof below compares against THIS baseline, not
	// against an assumption that a freshly constructed store's directory is
	// empty.
	before, rdErr := os.ReadDir(transcriptBase)
	if rdErr != nil {
		t.Fatalf("read transcript base dir before the write attempt: %v", rdErr)
	}
	beforeNames := make(map[string]bool, len(before))
	for _, e := range before {
		beforeNames[e.Name()] = true
	}

	writeErr := us.AppendTranscriptStrict(u19UnmintedChildID, session.TranscriptEntry{
		Role:      "user",
		Content:   "a message arriving after the crash, addressed to a session that was never minted",
		Timestamp: time.Now(),
	})
	if writeErr == nil {
		t.Fatal("AppendTranscriptStrict against the un-minted child id returned nil error — " +
			"FR-078 requires a non-nil error, asserted positively, not merely the absence of an orphan directory")
	}
	t.Logf("AppendTranscriptStrict correctly refused: %v", writeErr)

	// Positive proof the refusal created nothing — the specific directory the
	// old lenient AppendTranscript would have minted (BDD-01's exact defect
	// AppendTranscriptStrict exists to close) does not exist.
	childDir := filepath.Join(transcriptBase, u19UnmintedChildID)
	if _, statErr := os.Stat(childDir); statErr == nil {
		t.Fatalf("AppendTranscriptStrict created a directory at %s despite returning a non-nil error", childDir)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unexpected stat error on %s: %v", childDir, statErr)
	}

	// And no NEW entry appeared anywhere in the transcript base — the
	// stronger "wrote nothing" proof BDD-87 asks for, not just "this one
	// specific path is absent" — compared against the pre-write baseline so
	// the store's own internal ".context" bootstrap dir is correctly
	// excluded rather than mistaken for a leaked orphan.
	after, rdErr := os.ReadDir(transcriptBase)
	if rdErr != nil {
		t.Fatalf("read transcript base dir after the write attempt: %v", rdErr)
	}
	var newEntries []string
	for _, e := range after {
		if !beforeNames[e.Name()] {
			newEntries = append(newEntries, e.Name())
		}
	}
	if len(newEntries) != 0 {
		t.Fatalf("the refused write left NEW entries in the transcript base dir: %v", newEntries)
	}
}
