// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// boot_sweep_adr057_test.go — ADR-057 unit U19's test for W6b/FR-078 (test
// #65, BDD-87): "A restart mid-delegation leaves no orphan directory".
//
// Per binding Rule 5 this is a NEW file (U19 does not add to the existing
// boot_sweep_test.go, which several other units' tests already share) and
// per binding Rule 6 its unexported helpers are prefixed u19.
//
// FR-078 has two clauses, both asserted here against REAL, on-disk stores
// (binding Rule 1 — no spy, no fake):
//
//  1. The boot sweep (pkg/agent/boot_sweep.go) reconciles an in-flight
//     DELEGATE CHILD's lifecycle record to a terminal state across a
//     process restart. This is the SAME generic non-terminal sweep
//     boot_sweep_test.go's TestBootSweep_NonTerminalToFailedInterrupted
//     already covers for a plan-owner session — this test's contribution is
//     proving the identical mechanism reconciles a record SHAPED like a
//     real delegate.go `run` mint (OwnerScopeParentSession, a
//     ParentDurableKey, no OwnsPlanID/GoalRef — see delegate.go:1166-1180),
//     which no existing boot_sweep_test.go case constructs.
//  2. A transcript write attempted against the un-minted child id — i.e. a
//     crash so early that delegate.go's lifecycle Persist (run's FIRST
//     durable write, :1181) landed but the child's OWN transcript session
//     (session.CreateSessionWithID, minted later during the actual spawn)
//     never did — returns a non-nil error from AppendTranscriptStrict and
//     creates NO directory, even after the boot sweep has run. This is
//     asserted POSITIVELY (the write attempt happened and failed), not as
//     "no orphan directory was found lying around" (which a run that wrote
//     nothing anywhere would satisfy vacuously — grill C-3, spec §4 item 8).
package agent

import (
	"context"
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

// u19ParentDurableKey is the delegating chat's own durable id — distinct
// from the child id per the spec's own "distinct ids everywhere" corollary
// (FR-074).
const u19ParentDurableKey = "u19-parent-chat-durable-key"

// TestBootSweep_ReconcilesChildAcrossRestart is test #65
// (TestBootSweep_ReconcilesChildAcrossRestart, BDD-87, FR-078).
func TestBootSweep_ReconcilesChildAcrossRestart(t *testing.T) {
	h := newBootSweepHarness(t)

	// (1) Persist a lifecycle record shaped exactly like delegate.go's
	// executeRun mint (pkg/tools/delegate.go:1166-1180) for a ROOT-level
	// delegation: OwnerScopeParentSession/ParentDurableKey set, State=queued
	// (the state a mint-then-crash leaves behind — the spawn goroutine that
	// would advance it to `running` never got to run), no OwnsPlanID/GoalRef
	// (a plain delegate child is not a plan-owner or goal-bearing session,
	// so neither boot-sweep exemption applies).
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID:        u19UnmintedChildID,
		Generation:       0,
		State:            session.LifecycleQueued,
		OwnerScopeKind:   session.OwnerScopeParentSession,
		OwnerScopeID:     u19ParentDurableKey,
		ParentAgentID:    "parent-agent",
		ParentDurableKey: u19ParentDurableKey,
		AgentID:          "child-agent",
		CreatedAt:        time.Now().Add(-1 * time.Minute),
	})

	// (2) Simulate the restart: run the boot sweep exactly as Start would at
	// boot, right after bootReconcile.
	result := h.pe.runBootSweep(context.Background())

	if len(result.SweptToFailed) != 1 || result.SweptToFailed[0] != u19UnmintedChildID {
		t.Fatalf("SweptToFailed = %v, want exactly [%q]", result.SweptToFailed, u19UnmintedChildID)
	}

	reconciled, err := h.ls.Load(u19UnmintedChildID)
	if err != nil {
		t.Fatalf("load reconciled child record: %v", err)
	}
	if reconciled.State != session.LifecycleFailed {
		t.Fatalf("reconciled child state = %q, want %q (terminal)", reconciled.State, session.LifecycleFailed)
	}
	if !session.IsTerminalLifecycleState(reconciled.State) {
		t.Fatalf("reconciled child state %q is not terminal per IsTerminalLifecycleState", reconciled.State)
	}
	if reconciled.FailedReason == "" {
		t.Fatal("reconciled child record has no failed_reason — a terminal failed record MUST name why")
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
	} else if !os.IsNotExist(statErr) {
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
