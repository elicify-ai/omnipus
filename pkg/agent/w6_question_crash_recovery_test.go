// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// W6 crash/durability pack, rewritten for ADR-20261004 ("Steering commands:
// no person question"). The D1.5 answer-reservation machinery this file used
// to pin (the pending_questions sidecar, its answer_pending_delivery /
// applied / conflict states) is deleted: there is no owner-answer acceptor
// and no question record to reserve.
//
// What survives is the RX-DELIVERY integrity rule underneath it, now carried
// by AgentLoop.ReviveStoppedSession (steering.go) — the path a parent's
// respond takes to a stopped helper:
//
//   - the new instruction lands in the TRANSCRIPT BEFORE anything is
//     dispatched, and a failed append refuses the revive VISIBLY — the
//     caller must never read a failed resume as a delivered answer, and the
//     stopped record stays stopped and retryable;
//   - after the fault is repaired, the same respond resumes the SAME
//     conversation (new generation) and the answer lands exactly once.
//
// The fault is injected at the process edge by making the real transcript
// JSONL owner-unwritable (chmod 0400, non-root), exactly as this file always
// injected its write failures. Oracles derive from the locked decisions and
// the revive path's own doc comments, not from observed behaviour.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	w6cAnswer = "yes, deliver the answer now"
	// w6cPermDenied is the EACCES text darwin and linux both produce when
	// the euid opens an owned 0400 file O_RDWR|O_CREATE|O_APPEND. The
	// instrument checks require it so a random failure cannot pass for the
	// injected one.
	w6cPermDenied = "permission denied"
)

func w6cTranscriptFile(store *session.UnifiedStore, sessionID string) string {
	return filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl")
}

// w6cDenyWrite makes path owner-unwritable (0400) and registers restore.
// It proves the injection is live before returning: if the chmod cannot
// block an O_RDWR open, the cut cannot be produced and the test says so
// instead of failing for a bogus reason.
func w6cDenyWrite(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skipf("running as root: chmod 0400 cannot make an owned file unwritable, so the " +
			"transcript append failure this cut injects cannot be produced " +
			"(no production test hook exists and none may be added). Permission-shaped " +
			"leg skipped with this named explanation; not a behavioral RED under root.")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatalf("Chmod(0400, %s): %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, info.Mode().Perm()) })
	if f, err := os.OpenFile(path, os.O_RDWR, 0o600); err == nil {
		f.Close()
		t.Fatalf("chmod 0400 did not make %s unwritable; the write-failure injection is not live", path)
	}
}

// w6cRestoreWrite repairs the file mid-test so the retry leg runs
// against a healthy store.
func w6cRestoreWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("Chmod(0600, %s): %v", path, err)
	}
}

// TestW6RespondToStoppedChild_AppendFailureRefusesVisiblyThenRecovers is the
// surviving cut: with the child's transcript unwritable, the parent's answer
// to the stopped helper must fail VISIBLY (the revive refuses before any
// dispatch — steering.go's append-before-generation rule), leave the record
// stopped and retryable with no transcript copy, and after repair the same
// respond resumes the SAME conversation with the answer landing exactly once.
func TestW6RespondToStoppedChild_AppendFailureRefusesVisiblyThenRecovers(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-crash-resume")
	parent := u1LaunchChild(t, al, root, "w6-crash-resume-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-crash-resume-child")

	// An ordinary question first (ADR-20261004: no park — the helper keeps
	// working; the answer below simply references it by correlation id).
	corr, askedGen := w6AskOrdinaryQuestion(t, al, child.SessionID)

	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}
	w6StopTurns(t, al, parent.SessionID, by)
	w6StopTurns(t, al, child.SessionID, by)
	stopped := w6MustLoad(t, al.GetSessionLifecycleStore(), child.SessionID)
	if stopped.State != session.LifecycleStopped || stopped.Generation != askedGen {
		t.Fatalf("precondition: child must be stopped at the asked generation: state=%s generation=%d want=%d",
			stopped.State, stopped.Generation, askedGen)
	}

	// The injected fault: the transcript write the revive's instruction append
	// performs cannot land.
	transcriptFile := w6cTranscriptFile(al.GetSessionStore(), child.SessionID)
	w6cDenyWrite(t, transcriptFile)

	freshLC, _ := w6ReopenMessagingStores(t, al)
	delegate, launches := w6ParentRespondTool(t, al, freshLC, nil)
	parentCtx := tools.WithTranscriptSessionID(context.Background(), parent.SessionID)

	result := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, corr, w6cAnswer))
	instruction := w6SelfOKDelivery(corr, w6cAnswer)

	if !result.IsError {
		t.Fatalf("respond with an unwritable transcript must fail visibly (the revive refuses before any dispatch): error=false text=%q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, w6cPermDenied) {
		t.Fatalf("respond error must name the injected write failure, not a random failure: %q", result.ForLLM)
	}
	if len(launches.calls) != 0 {
		t.Fatalf("a refused revive must dispatch nothing: %+v", launches.calls)
	}
	afterRefusal := w6MustLoad(t, freshLC, child.SessionID)
	if afterRefusal.State != session.LifecycleStopped || afterRefusal.Generation != askedGen {
		t.Fatalf("the refused respond must leave the record stopped and retryable: state=%s generation=%d want=(%s, %d)",
			afterRefusal.State, afterRefusal.Generation, session.LifecycleStopped, askedGen)
	}
	if got := w6CountTranscript(t, al.GetSessionStore(), child.SessionID, instruction); got != 0 {
		t.Fatalf("the refused answer must not sit in the transcript as if delivered: copies=%d", got)
	}

	// Repair, then the same respond again: the revive now lands — the answer
	// is written where the revived turn reads it, exactly once, and the SAME
	// conversation continues at the next generation.
	w6cRestoreWrite(t, transcriptFile)

	second := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, corr, w6cAnswer))
	if second.IsError {
		t.Fatalf("the same respond after repair must complete the resume, got: %s", second.ForLLM)
	}
	if got := w6CountTranscript(t, al.GetSessionStore(), child.SessionID, instruction); got != 1 {
		t.Fatalf("the answer must land in the transcript exactly once across both attempts: copies=%d", got)
	}
	waitFor(t, 10*time.Second, func() bool {
		rec := w6MustLoad(t, freshLC, child.SessionID)
		return rec.State != session.LifecycleStopped
	})
	resumed := w6MustLoad(t, freshLC, child.SessionID)
	if resumed.Generation != askedGen+1 {
		t.Fatalf("revived child generation = %d, want %d — the resume continues the SAME conversation as a new generation",
			resumed.Generation, askedGen+1)
	}
	if afterParent := w6MustLoad(t, freshLC, parent.SessionID); afterParent.State != session.LifecycleStopped {
		t.Fatalf("the resume addresses only the child; parent must stay stopped: state=%s", afterParent.State)
	}
}
