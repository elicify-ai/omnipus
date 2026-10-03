// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// W6 crash/durability RED pack — ADR-20260928 D1.5 (answer reservation and
// delivery-intent split), D1.7 (durable pending-question record), D1.4/MAJ-002
// (a transcript append failure must refuse visibly BEFORE dispatch), T2/T14
// (post-crash explicit RESUME completes delivery exactly once).
//
// Every case drives the PRODUCTION message_parent park and delegate respond
// paths over REAL stores: the lifecycle JSONL, the pending_questions sidecar,
// the session transcript and the inbox. The single double is w6ResumeRecorder's
// Dispatch turn-start edge; child state is never asserted from it — only from
// real lifecycle records. The fault is injected at the process edge by making
// the real lifecycle (or transcript) JSONL file owner-unwritable (chmod 0400,
// non-root), so the sidecar commits while the lifecycle persist fails.
//
// Oracles are derived from the ADR, not from this tree's behaviour:
//   - D1.5: a split reservation must retain exactly answer_pending_delivery —
//     the one recoverable reservation state the ADR specifies ("retain
//     answer_pending_delivery"; T2 "the original exact owner text and
//     intent"; open carries no reserved answer and cannot block a changed
//     answer) — report the incomplete step visibly, never falsely mark the
//     question applied, never accept a different answer over the reservation,
//     and stay recoverable without a fresh answer; a fresh explicit retry of
//     the SAME answer (the respond path is the only explicit resume action in
//     this tree) finishes delivery once.
//   - D1.7: the sidecar keeps the exact original deadline across Stop.
//   - MAJ-002: a failed answer recording must be refused visibly before any
//     dispatch or question consumption side effect beyond the reservation.
//
// Remaining W6 scope stays out on purpose (no seam in this tree): expiry
// sweeper, provenance Who/When/Which/Once, withdrawal/redirect, relay chains,
// and D8.5's no-boot-auto-dispatch leg.

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
	// w6cParkCorrelation is cut 2's park-split question. Cuts 1 and 3 park
	// through w6ParkSelfOKQuestion, whose correlation comes back from the
	// park helper itself.
	w6cParkCorrelation = "w6-crash-park-phantom"
	w6cAnswer          = "yes, deliver the reserved answer"
	w6cPhantomAnswer   = "phantom answer to a never-parked question"
	// w6cDifferentAnswer is cut 1's refused second answer: a different text
	// for the same reserved question must conflict, never overwrite.
	w6cDifferentAnswer = "no, use the production key instead"
	// w6cPermDenied is the EACCES text darwin and linux both produce when
	// the euid opens an owned 0400 file O_RDWR|O_CREATE|O_APPEND. The
	// instrument checks require it so a random failure cannot pass for the
	// injected one.
	w6cPermDenied = "permission denied"
)

// w6cQuestionStore is a fresh sidecar reader over lc's directory.
func w6cQuestionStore(lc *session.LifecycleStore) *session.QuestionStore {
	return session.NewQuestionStore(session.PendingQuestionDir(lc.Dir()))
}

// w6cMustQuestion loads the sidecar record through a fresh real store.
func w6cMustQuestion(t *testing.T, lc *session.LifecycleStore, sessionID string) *session.PendingQuestion {
	t.Helper()
	q, err := w6cQuestionStore(lc).Load(sessionID)
	if err != nil {
		t.Fatalf("QuestionStore.Load(%s): %v", sessionID, err)
	}
	if q == nil {
		t.Fatalf("QuestionStore.Load(%s): no pending question record", sessionID)
	}
	return q
}

func w6cLifecycleFile(lc *session.LifecycleStore, sessionID string) string {
	return filepath.Join(lc.Dir(), sessionID+".jsonl")
}

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
			"lifecycle/transcript append failure this cut injects cannot be produced " +
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

// w6cRestoreWrite repairs the file mid-test so the explicit retry leg runs
// against a healthy store.
func w6cRestoreWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("Chmod(0600, %s): %v", path, err)
	}
}

// w6cAssertSameReservation fails unless got is the very reservation want
// holds. D1.5's "first committed reservation wins" makes the reserved
// identity {status, answer text, correlation, authority, asker, generation,
// original deadline} the thing a refused second answer must leave byte-for-
// byte unchanged; deadline compares by instant, not zone.
func w6cAssertSameReservation(t *testing.T, got, want *session.PendingQuestion) {
	t.Helper()
	if got.Status != want.Status || got.AnswerText != want.AnswerText ||
		got.CorrelationID != want.CorrelationID || got.Authority != want.Authority ||
		got.AskerSessionID != want.AskerSessionID || got.AskerGeneration != want.AskerGeneration ||
		!got.OriginalDeadline.Equal(want.OriginalDeadline) {
		t.Fatalf("reservation identity changed: got status=%q answer=%q corr=%q authority=%q asker=%q gen=%d deadline=%s, want status=%q answer=%q corr=%q authority=%q asker=%q gen=%d deadline=%s",
			got.Status, got.AnswerText, got.CorrelationID, got.Authority, got.AskerSessionID, got.AskerGeneration, got.OriginalDeadline.Format(time.RFC3339Nano),
			want.Status, want.AnswerText, want.CorrelationID, want.Authority, want.AskerSessionID, want.AskerGeneration, want.OriginalDeadline.Format(time.RFC3339Nano))
	}
}

// TestW6CrashLifecyclePersistFails_AnswerStaysPendingDelivery_NotApplied is
// cut 1: the child is a stopped self_ok asker. With the sidecar writable and
// the child's lifecycle JSONL unwritable, respond reserves the answer
// (sidecar) and records it in the transcript, then the lifecycle persist at
// finish fails. ADR D1.5: the tool must report the incomplete step visibly,
// the sidecar must hold exactly answer_pending_delivery with the original
// instruction unconditionally, and after repair a DIFFERENT answer through
// the same respond path must be refused as a conflict — not by a write
// failure — leaving the reservation untouched, while a fresh explicit respond
// with the SAME answer delivers it exactly once and only then marks the
// question applied.
func TestW6CrashLifecyclePersistFails_AnswerStaysPendingDelivery_NotApplied(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-crash-resume")
	parent := u1LaunchChild(t, al, root, "w6-crash-resume-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-crash-resume-child")
	corr, deadline, generation := w6ParkSelfOKQuestion(t, al, child.SessionID)

	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}
	w6StopTurns(t, al, parent.SessionID, by)
	w6StopTurns(t, al, child.SessionID, by)

	if before := w6cMustQuestion(t, al.GetSessionLifecycleStore(), child.SessionID); before.Status != session.QuestionStatusOpen {
		t.Fatalf("sidecar must be open before the respond: status=%q", before.Status)
	}

	lifecycleFile := w6cLifecycleFile(al.GetSessionLifecycleStore(), child.SessionID)
	w6cDenyWrite(t, lifecycleFile)

	freshLC, freshInbox := w6ReopenMessagingStores(t, al)
	delegate, resumes := w6ParentRespondTool(t, al, freshLC, freshInbox)
	parentCtx := tools.WithTranscriptSessionID(context.Background(), parent.SessionID)

	result := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, corr, w6cAnswer))
	instruction := w6SelfOKDelivery(corr, w6cAnswer)

	if !result.IsError {
		t.Fatalf("respond with an unwritable lifecycle file must fail visibly (ADR D1.5 'report the incomplete step visibly'): error=false text=%q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "answer pending delivery") || !strings.Contains(result.ForLLM, w6cPermDenied) {
		t.Fatalf("respond error must name the pending-delivery step and the injected write failure, not a random failure: %q", result.ForLLM)
	}
	if len(resumes.calls) > 1 {
		t.Fatalf("one respond must not dispatch the child more than once: %v", resumes.calls)
	}

	// ADR D1.5/T2 name exactly one recoverable reservation state:
	// answer_pending_delivery ("retain answer_pending_delivery"; T2 "the
	// original exact owner text and intent"). open carries no reserved-answer
	// identity — it cannot block a changed answer — so resetting to open is
	// not recovery, and applied is false completion. Neither is acceptable.
	q := w6cMustQuestion(t, freshLC, child.SessionID)
	if q.Status != session.QuestionStatusAnswerPendingDelivery {
		t.Fatalf("sidecar must hold exactly answer_pending_delivery when the lifecycle persist failed (ADR D1.5 'retain answer_pending_delivery'; open has no reserved answer, applied is false completion): status=%q answer_text=%q", q.Status, q.AnswerText)
	}
	if q.AnswerText != instruction {
		t.Fatalf("the reservation must keep the exact original committed instruction unconditionally (ADR T2 'original exact owner text and intent'): got %q want %q", q.AnswerText, instruction)
	}
	if !q.OriginalDeadline.Equal(deadline) {
		t.Fatalf("failed respond must not rewrite the original deadline: got %s want %s", q.OriginalDeadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
	}
	if delivered := w6CountTranscript(t, al.GetSessionStore(), child.SessionID, instruction); delivered != 1 {
		t.Fatalf("the answer must be recorded in the transcript exactly once across the failed respond (ADR D1.5 once-only): copies=%d", delivered)
	}
	afterChild := w6MustLoad(t, freshLC, child.SessionID)
	if afterChild.State != session.LifecycleStopped || afterChild.Generation != generation {
		t.Fatalf("child must remain stopped at the same generation while the lifecycle persist fails (state from the real record, not the dispatch recorder): state=%s generation=%d want_generation=%d", afterChild.State, afterChild.Generation, generation)
	}
	dispatchesAfterFault := len(resumes.calls)

	// Repair BEFORE the negative control: a later permission error must not
	// masquerade as the conflict refusal this leg demands.
	w6cRestoreWrite(t, lifecycleFile)

	// Negative control (D1.5 "without permitting another question/message use";
	// Once: "a second use or concurrent direct/relay answer is refused";
	// "first committed reservation wins"): a DIFFERENT answer for the same
	// question through the same production respond path must be refused as a
	// conflict — visibly, and not by the injected write failure.
	differentInstruction := w6SelfOKDelivery(corr, w6cDifferentAnswer)
	conflict := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, corr, w6cDifferentAnswer))
	if !conflict.IsError {
		t.Fatalf("a different answer over a reserved question must be refused (ADR D1.5): error=false text=%q", conflict.ForLLM)
	}
	if !strings.Contains(conflict.ForLLM, "already has a different pending answer") {
		t.Fatalf("the refusal must be the conflict refusal for the reserved answer, not a generic failure (ADR D1.5 'first committed reservation wins'): text=%q", conflict.ForLLM)
	}
	if strings.Contains(conflict.ForLLM, w6cPermDenied) {
		t.Fatalf("the lifecycle file is repaired, so a permission error here is not the conflict refusal (ADR D1.5): text=%q", conflict.ForLLM)
	}
	if got := w6CountTranscript(t, al.GetSessionStore(), child.SessionID, instruction); got != 1 {
		t.Fatalf("the refused second answer must not touch the reserved transcript copy: copies=%d", got)
	}
	if got := w6CountTranscript(t, al.GetSessionStore(), child.SessionID, differentInstruction); got != 0 {
		t.Fatalf("the refused second answer must never be recorded in the transcript: copies=%d", got)
	}
	if len(resumes.calls) != dispatchesAfterFault {
		t.Fatalf("the refused second answer must not dispatch the child: before=%d after=%d (%v)", dispatchesAfterFault, len(resumes.calls), resumes.calls)
	}
	w6cAssertSameReservation(t, w6cMustQuestion(t, freshLC, child.SessionID), q)

	// Then the explicit retry of the SAME answer: the same production respond
	// action, same correlation, same text — this tree's explicit resume seam.
	second := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, corr, w6cAnswer))
	if second.IsError {
		t.Fatalf("explicit retry after repair must complete the reserved delivery (ADR D1.5 'previously reserved delivery is recoverable without inventing a fresh answer'): error=%v text=%q sidecar=%q", second.IsError, second.ForLLM, w6cMustQuestion(t, freshLC, child.SessionID).Status)
	}
	if got := w6CountTranscript(t, al.GetSessionStore(), child.SessionID, instruction); got != 1 {
		t.Fatalf("retry must deliver the SAME answer exactly once, never a second copy (ADR D1.5/T2 once): copies=%d", got)
	}
	if got := w6CountTranscript(t, al.GetSessionStore(), child.SessionID, differentInstruction); got != 0 {
		t.Fatalf("the refused answer must still be absent after the retry: copies=%d", got)
	}
	if fq := w6cMustQuestion(t, freshLC, child.SessionID); fq.Status != session.QuestionStatusApplied || fq.AnswerText != instruction {
		t.Fatalf("retry must mark the question applied only after delivery, keeping the delivered answer identity (ADR D1.5 'finish applied only after the asker has received the answer'): status=%q answer_text=%q", fq.Status, fq.AnswerText)
	}
	afterRetry := w6MustLoad(t, freshLC, child.SessionID)
	if afterRetry.State == session.LifecycleStopped || afterRetry.Generation != generation {
		t.Fatalf("retry must move the same generation out of stopped (queued or admitted, per admission): state=%s generation=%d want_generation=%d", afterRetry.State, afterRetry.Generation, generation)
	}
	if afterParent := w6MustLoad(t, freshLC, parent.SessionID); afterParent.State != session.LifecycleStopped {
		t.Fatalf("retry resumes only the child; parent must stay stopped: state=%s", afterParent.State)
	}
}

// TestW6CrashPositiveControl_WritableLifecycle_DeliversOnceAndApplies is
// cut 1's instrument control: the identical flow with a writable lifecycle
// file delivers once and applies. It proves the split test's failure comes
// from the injected lifecycle-write failure, not from the harness.
func TestW6CrashPositiveControl_WritableLifecycle_DeliversOnceAndApplies(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-crash-positive")
	parent := u1LaunchChild(t, al, root, "w6-crash-positive-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-crash-positive-child")
	corr, _, generation := w6ParkSelfOKQuestion(t, al, child.SessionID)

	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}
	w6StopTurns(t, al, parent.SessionID, by)
	w6StopTurns(t, al, child.SessionID, by)

	freshLC, freshInbox := w6ReopenMessagingStores(t, al)
	delegate, resumes := w6ParentRespondTool(t, al, freshLC, freshInbox)
	parentCtx := tools.WithTranscriptSessionID(context.Background(), parent.SessionID)

	result := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, corr, w6cAnswer))
	instruction := w6SelfOKDelivery(corr, w6cAnswer)

	if result.IsError {
		t.Fatalf("healthy-path respond must not fail: error=%v text=%q", result.IsError, result.ForLLM)
	}
	if got := w6CountTranscript(t, al.GetSessionStore(), child.SessionID, instruction); got != 1 {
		t.Fatalf("healthy path delivers the answer exactly once: copies=%d", got)
	}
	if len(resumes.calls) != 1 {
		t.Fatalf("healthy path dispatches the child once: %v", resumes.calls)
	}
	if fq := w6cMustQuestion(t, freshLC, child.SessionID); fq.Status != session.QuestionStatusApplied {
		t.Fatalf("healthy path applies the question: status=%q", fq.Status)
	}
	afterChild := w6MustLoad(t, freshLC, child.SessionID)
	if afterChild.State == session.LifecycleStopped || afterChild.Generation != generation {
		t.Fatalf("healthy path moves the same generation out of stopped: state=%s generation=%d", afterChild.State, afterChild.Generation)
	}

	again := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, corr, w6cAnswer))
	if !again.IsError {
		t.Fatalf("a second respond on an applied question must be refused (once): text=%q", again.ForLLM)
	}
	if got := w6CountTranscript(t, al.GetSessionStore(), child.SessionID, instruction); got != 1 {
		t.Fatalf("refused second respond must not copy the answer again: copies=%d", got)
	}
}

// TestW6CrashParkPersistFails_PhantomOpenQuestionMustNotBeAnswerable is
// cut 2: message_parent's park commits the open sidecar record inside the
// lifecycle Mutate callback BEFORE the lifecycle persist. With the lifecycle
// file unwritable the park fails visibly — but the sidecar record the failed
// park left behind must not stay answerable across a fresh store open: it
// must be reconciled away (or quarantined so a respond is refused visibly),
// never answered into a session that never durably parked (ADR D1.5/D1.7).
func TestW6CrashParkPersistFails_PhantomOpenQuestionMustNotBeAnswerable(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-crash-park")
	parent := u1LaunchChild(t, al, root, "w6-crash-park-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-crash-park-child")

	lifecycleFile := w6cLifecycleFile(al.GetSessionLifecycleStore(), child.SessionID)
	w6cDenyWrite(t, lifecycleFile)

	tool := tools.NewMessageParentTool(al.getUpwardDeliverer(), al.GetSessionLifecycleStore())
	tool.SetSessionMessagingEnabled(func() bool { return true })
	ctx := tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), child.SessionID), child.SessionID)
	result := tool.Execute(ctx, map[string]any{
		"kind": "question", "text": "crash park probe", "wait": true,
		"authority": "self_ok", "correlation_id": w6cParkCorrelation,
	})
	if !result.IsError {
		t.Fatalf("park with an unwritable lifecycle file must fail visibly: text=%q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "failed to park session") || !strings.Contains(result.ForLLM, w6cPermDenied) {
		t.Fatalf("park error must name the park step and the injected write failure: %q", result.ForLLM)
	}

	// The split happened: the sidecar open record committed, the lifecycle
	// append did not. Repair so the production Stop below can write.
	w6cRestoreWrite(t, lifecycleFile)
	if q := w6cMustQuestion(t, al.GetSessionLifecycleStore(), child.SessionID); q.Status != session.QuestionStatusOpen {
		t.Fatalf("the failed park leaves its sidecar record behind; it must be visible as open here: status=%q", q.Status)
	}

	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}
	w6StopTurns(t, al, child.SessionID, by)

	freshLC, freshInbox := w6ReopenMessagingStores(t, al)
	childRec := w6MustLoad(t, freshLC, child.SessionID)
	if childRec.State != session.LifecycleStopped || childRec.NeedsInput != nil {
		t.Fatalf("the failed park must not have landed needs_input on the child: state=%s needs_input=%v", childRec.State, childRec.NeedsInput)
	}

	delegate, resumes := w6ParentRespondTool(t, al, freshLC, freshInbox)
	parentCtx := tools.WithTranscriptSessionID(context.Background(), parent.SessionID)
	answer := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, w6cParkCorrelation, w6cPhantomAnswer))
	instruction := w6SelfOKDelivery(w6cParkCorrelation, w6cPhantomAnswer)

	if deliveries := w6CountTranscript(t, al.GetSessionStore(), child.SessionID, instruction); deliveries != 0 {
		t.Fatalf("an answer to a never-parked phantom question must never be delivered (ADR D1.5/D1.7): copies=%d respond=%q", deliveries, answer.ForLLM)
	}
	if len(resumes.calls) != 0 {
		t.Fatalf("a phantom question must not dispatch the child: %v", resumes.calls)
	}
	if _, err := w6cQuestionStore(freshLC).Load(child.SessionID); err != nil {
		return // reconciled away: nothing left to answer
	}
	if !answer.IsError {
		t.Fatalf("the phantom open question survived the fresh open, so a respond against it must be refused visibly (ADR D1.5 reconcile/quarantine): error=false text=%q", answer.ForLLM)
	}
	if fq := w6cMustQuestion(t, freshLC, child.SessionID); fq.Status == session.QuestionStatusApplied {
		t.Fatalf("the phantom question must never be marked applied: status=%q", fq.Status)
	}
}

// TestW6CrashParkPositiveControl_SidecarMirrorsParkAndKeepsDeadlineOverStop
// is cut 2's instrument control and the D1.7 deadline read: a healthy park
// writes both records with the SAME instant, and a Stop leaves the sidecar
// open with that exact original deadline through a fresh store open.
func TestW6CrashParkPositiveControl_SidecarMirrorsParkAndKeepsDeadlineOverStop(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-crash-park-ok")
	parent := u1LaunchChild(t, al, root, "w6-crash-park-ok-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-crash-park-ok-child")
	corr, deadline, generation := w6ParkSelfOKQuestion(t, al, child.SessionID)

	parked := w6cMustQuestion(t, al.GetSessionLifecycleStore(), child.SessionID)
	if parked.Status != session.QuestionStatusOpen || parked.CorrelationID != corr ||
		parked.Authority != session.QuestionAuthoritySelfOK || parked.AskerSessionID != child.SessionID ||
		parked.AskerGeneration != generation || !parked.OriginalDeadline.Equal(deadline) {
		t.Fatalf("healthy park must write a sidecar record mirroring the park (ADR D1.7): %+v want corr=%s authority=%s generation=%d deadline=%s",
			parked, corr, session.QuestionAuthoritySelfOK, generation, deadline.Format(time.RFC3339Nano))
	}

	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}
	w6StopTurns(t, al, child.SessionID, by)

	freshLC, _ := w6ReopenMessagingStores(t, al)
	freshQ := w6cMustQuestion(t, freshLC, child.SessionID)
	if freshQ.Status != session.QuestionStatusOpen || !freshQ.OriginalDeadline.Equal(deadline) {
		t.Fatalf("Stop must leave the sidecar open with the exact original deadline (ADR D1.7): status=%q deadline=%s want %s",
			freshQ.Status, freshQ.OriginalDeadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
	}
	after := w6MustLoad(t, freshLC, child.SessionID)
	if after.State != session.LifecycleStopped || after.Generation != generation {
		t.Fatalf("child must be stopped at the parked generation: state=%s generation=%d want=%d", after.State, after.Generation, generation)
	}
}

// TestW6CrashTranscriptAppendFails_AnswerStaysPendingDelivery_NoDispatch is
// cut 3: the transcript append fails after the reservation committed. ADR
// MAJ-002/D1.5: the tool refuses visibly, nothing is dispatched, nothing is
// marked applied, the sidecar keeps answer_pending_delivery with the exact
// reserved text; after repair the fresh explicit retry records the answer
// once, delivers once, and applies.
func TestW6CrashTranscriptAppendFails_AnswerStaysPendingDelivery_NoDispatch(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-crash-transcript")
	parent := u1LaunchChild(t, al, root, "w6-crash-transcript-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-crash-transcript-child")
	corr, _, generation := w6ParkSelfOKQuestion(t, al, child.SessionID)

	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}
	w6StopTurns(t, al, parent.SessionID, by)
	w6StopTurns(t, al, child.SessionID, by)

	// One real store write so the transcript file exists; then deny writes
	// to it. Reads still work under 0400, so the respond reaches exactly the
	// reserved-then-append-fails window.
	seed := session.TranscriptEntry{ID: "w6c-seed", Role: "assistant", Content: "seed before fault", Timestamp: time.Now(), AgentID: testDefaultAgentID}
	if err := al.GetSessionStore().AppendTranscript(child.SessionID, seed); err != nil {
		t.Fatalf("AppendTranscript(seed): %v", err)
	}
	transcriptFile := w6cTranscriptFile(al.GetSessionStore(), child.SessionID)
	w6cDenyWrite(t, transcriptFile)

	freshLC, freshInbox := w6ReopenMessagingStores(t, al)
	delegate, resumes := w6ParentRespondTool(t, al, freshLC, freshInbox)
	parentCtx := tools.WithTranscriptSessionID(context.Background(), parent.SessionID)

	result := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, corr, w6cAnswer))
	instruction := w6SelfOKDelivery(corr, w6cAnswer)

	if !result.IsError {
		t.Fatalf("a failed answer recording must refuse visibly (ADR MAJ-002): error=false text=%q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "answer pending delivery") || !strings.Contains(result.ForLLM, w6cPermDenied) {
		t.Fatalf("the refusal must name the pending-delivery step and the injected append failure: %q", result.ForLLM)
	}
	if len(resumes.calls) != 0 {
		t.Fatalf("no dispatch may follow a failed answer recording (ADR MAJ-002 visible refusal before dispatch): %v", resumes.calls)
	}
	q := w6cMustQuestion(t, freshLC, child.SessionID)
	if q.Status != session.QuestionStatusAnswerPendingDelivery || q.AnswerText != instruction {
		t.Fatalf("the reservation must survive with the exact answer, never applied: status=%q answer_text=%q want_text=%q", q.Status, q.AnswerText, instruction)
	}
	if got := w6CountTranscript(t, al.GetSessionStore(), child.SessionID, instruction); got != 0 {
		t.Fatalf("the answer must not appear in the transcript when its append failed: copies=%d", got)
	}
	afterChild := w6MustLoad(t, freshLC, child.SessionID)
	if afterChild.State != session.LifecycleStopped || afterChild.Generation != generation {
		t.Fatalf("child must remain stopped at the same generation: state=%s generation=%d want_generation=%d", afterChild.State, afterChild.Generation, generation)
	}

	w6cRestoreWrite(t, transcriptFile)
	second := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, corr, w6cAnswer))
	if second.IsError {
		t.Fatalf("retry after repair must record and deliver the reserved answer: error=%v text=%q", second.IsError, second.ForLLM)
	}
	if got := w6CountTranscript(t, al.GetSessionStore(), child.SessionID, instruction); got != 1 {
		t.Fatalf("retry records and delivers the answer exactly once: copies=%d", got)
	}
	if fq := w6cMustQuestion(t, freshLC, child.SessionID); fq.Status != session.QuestionStatusApplied {
		t.Fatalf("retry applies the question only after delivery: status=%q", fq.Status)
	}
	if afterRetry := w6MustLoad(t, freshLC, child.SessionID); afterRetry.State == session.LifecycleStopped || afterRetry.Generation != generation {
		t.Fatalf("retry moves the same generation out of stopped: state=%s generation=%d want_generation=%d", afterRetry.State, afterRetry.Generation, generation)
	}
}
