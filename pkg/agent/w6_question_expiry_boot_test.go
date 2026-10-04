// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// W6 D1.8 RED — the original-24h question-park expiry must be enforced by the
// production BOOT consumer, from the durable pending-question record, using
// the ORIGINAL TTLDeadline, even for a stopped asker (ADR-20260928 D1.8,
// T8/T14/T19; F1011-Q2; F0929-6; MAJ-006).
//
// Test plan (elicify-test-writing step 1)
//
//   behaviour under test: after a process restart, SteerBootRecovery.Run —
//     the consumer gateway_boot.go::wireSteerDeps wires into its BootHook —
//     expires a parked question whose original deadline has passed: the asker
//     fails visibly (failed(owner_unreachable) with the exact text "owner
//     could not be reached" for owner_required; failed(answer_timeout)
//     otherwise), exactly one fatal error reaches the DIRECT parent, the
//     question closes unanswerable (a late answer is refused stale and
//     consumes nothing), the asker's goal stays active, no run is dispatched
//     (generation unchanged), and a second boot does not double-fail. A
//     not-yet-expired question — parked or stopped — survives boot unchanged.
//
//   specification source: ADR-20260928 section "D1 ... 8. The 24-hour
//     question-park limit" (frozen copy
//     a-u1-runtime-contracts-20261002/assets/ADR-20260928-sub-agent-control-plane@cd20cf8b.md),
//     plus D1.5 ("An expired/withdrawn question cannot be reserved"), D5
//     ("Any refusal is a visible error to the sender"; fatal upward to the
//     direct parent), F0929-6/D7 ("question expiry ... never clear[s] an
//     active goal"), T8 (boot AND periodic, no double-failure, late message
//     not consumed), T14/T19 (stopped asker keeps the original deadline and
//     still expires on it). Every expected string and reason below is copied
//     from the ADR, not from observed code — no expiry consumer exists in
//     this tree (boot_sweep.go::recoverSteered reads no QuestionStore), which
//     is exactly what this pack proves RED.
//
//   unit boundary: everything real — LifecycleStore, UnifiedStore,
//     MessageInboxStore, the QuestionStore sidecar, MessageParentTool's
//     production park, SteerCanceller.StopTurns, SteerBootRecovery.Run with
//     the real upward deliverer. The ONLY injected dependency is
//     MessageParentTool's existing SetClock at park time (a caller-side
//     dependency that already exists — no production hook is added); boot
//     itself runs on the real clock. No model turn is ever started: the
//     resume recorder proves Dispatch is (or is not) asked, and a generation
//     change would expose any automatic resume.
//
//   case table:
//     expired owner_required + boot  -> failed(owner_unreachable), 1 fatal
//       "owner could not be reached" to the direct parent, sidecar closed
//       unanswerable at the unchanged original deadline, goal active,
//       generation unchanged, late respond refused with nothing delivered
//       and nothing applied, second boot changes nothing (T8).
//     not-expired owner_required + boot (positive control) -> still
//       needs_input at the SAME deadline, sidecar still open, 0 fatals, goal
//       active — so a generic "boot fails everything" cannot pass.
//     stopped expired asker + boot -> failed(owner_unreachable) as above;
//     stopped not-expired asker + boot (positive control) -> stays stopped
//       with the question open. MIXED-RISK: if the control leg ever fails
//       too, the failure is W3b stopped-handling contamination, not isolated
//       D1.8 expiry RED — the diagnostics name which leg broke.
//     expired self_ok + boot -> failed(answer_timeout), 1 fatal notice, the
//       notice must NOT carry the owner-unreachable text.
//
//   known gaps (reported BLOCKED, never faked here):
//     - periodic expiry: this tree has NO periodic consumer — no scheduler
//       tick or injected sweeper reads the QuestionStore anywhere in
//       pkg/agent or pkg/gateway — so the periodic leg of D1.8/T8 has no
//       production caller to drive and is reported BLOCKED, not tested with
//       an invented fake.
//     - relay closure on expiry ("closes open relays superseded"): the c8 W6
//       partial has no relay writer, so no relay chain can be built without
//       hand-seeding fake records — BLOCKED, not covered here.
//     - "without consuming its source" (D1.5) is asserted to the depth this
//       tree allows: the late respond is a visible error, dispatches nothing,
//       delivers no answer text into the asker transcript and never writes
//       applied. The full trusted-provenance Who/When/Which/Once writer
//       (D1.4) is a separate, still-missing surface.
//
//   deferred to CHECK (skill step 4 items 2-3): green-after-implementation
//     and the mutation probes (skip-deadline-check generic failure — killed
//     by the positive controls; deadline read from NeedsInput instead of the
//     sidecar — killed by the stopped legs whose NeedsInput Stop cleared;
//     fatal to a non-direct ancestor — killed by the parent-inbox count;
//     goal cleared on expiry — killed by the goal assertions; late reserve
//     allowed — killed by the respond leg).

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Spec literals (ADR D1.8): the failure reasons and the exact owner text.
const (
	w6qeReasonOwnerUnreachable = "owner_unreachable"
	w6qeReasonAnswerTimeout    = "answer_timeout"
	w6qeOwnerUnreachableText   = "owner could not be reached"
)

// w6qeResumeRecorder is the turn-start edge only: it records that Dispatch
// was ASKED and never starts a model turn.
type w6qeResumeRecorder struct {
	calls []w6qeResumeCall
}

type w6qeResumeCall struct {
	sessionID  string
	generation int
}

func (r *w6qeResumeRecorder) Launch(context.Context, steer.LaunchRequest) (steer.LaunchResult, error) {
	return steer.LaunchResult{}, fmt.Errorf("expiry must never launch a new session")
}

func (r *w6qeResumeRecorder) Dispatch(_ context.Context, sessionID string, generation int) (steer.DispatchResult, error) {
	r.calls = append(r.calls, w6qeResumeCall{sessionID: sessionID, generation: generation})
	return steer.DispatchResult{State: steer.DispatchRunning, Generation: generation}, nil
}

// w6qeQuestionStore is the same store resolution the production park uses
// (pkg/tools/delegate_question.go::questionStoreFromLifecycle).
func w6qeQuestionStore(lc *session.LifecycleStore) *session.QuestionStore {
	return session.NewQuestionStore(session.PendingQuestionDir(lc.Dir()))
}

// w6qeMustLoad loads a lifecycle record from the given store.
func w6qeMustLoad(t *testing.T, lc *session.LifecycleStore, sessionID string) *session.LifecycleRecord {
	t.Helper()
	rec, err := lc.Load(sessionID)
	if err != nil {
		t.Fatalf("LifecycleStore.Load(%s): %v", sessionID, err)
	}
	if rec == nil {
		t.Fatalf("LifecycleStore.Load(%s): nil record", sessionID)
	}
	return rec
}

// w6qeMustQuestion loads the current pending-question record.
func w6qeMustQuestion(t *testing.T, lc *session.LifecycleStore, sessionID string) *session.PendingQuestion {
	t.Helper()
	q, err := w6qeQuestionStore(lc).Load(sessionID)
	if err != nil {
		t.Fatalf("QuestionStore.Load(%s): %v", sessionID, err)
	}
	return q
}

// w6qeReopen reopens the lifecycle and inbox stores over the SAME
// directories as fresh objects — the process-restart shape.
func w6qeReopen(t *testing.T, al *AgentLoop) (*session.LifecycleStore, *session.MessageInboxStore) {
	t.Helper()
	home := al.GetConfig().Agents.Defaults.Home
	return session.NewLifecycleStore(al.GetSessionLifecycleStore().Dir()),
		session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
}

// w6qeBootRecovery builds SteerBootRecovery exactly as the production boot
// hook does (gateway_boot.go::wireSteerDeps BootHook) — real classifier, real
// upward deliverer, operator-notice sink — but over the FRESH store objects
// a restart would hold.
func w6qeBootRecovery(t *testing.T, al *AgentLoop, lc *session.LifecycleStore, inbox *session.MessageInboxStore, notices *[]string) *SteerBootRecovery {
	t.Helper()
	return &SteerBootRecovery{
		Lifecycle:  lc,
		Sessions:   al.GetSessionStore(),
		Inbox:      inbox,
		Classifier: NewSteerRecordClassifier(lc, al.GetSessionStore()),
		Deliverer:  al.getUpwardDeliverer(),
		OperatorNotice: func(message string) {
			*notices = append(*notices, message)
		},
	}
}

// w6qeParkQuestion parks a real wait=true question through the production
// message_parent tool with the tool's own SetClock dependency pinned to
// parkClock. The TTL stays the shipped session.DefaultNeedsInputTTL — never
// overridden — so the original deadline is parkClock+24h, and the park MUST
// land both the needs_input record and the open sidecar at that exact
// instant. Returns the deadline and the asker's generation.
func w6qeParkQuestion(t *testing.T, al *AgentLoop, childID, correlationID, authority string, parkClock time.Time) (time.Time, int) {
	t.Helper()
	tool := tools.NewMessageParentTool(al.getUpwardDeliverer(), al.GetSessionLifecycleStore())
	tool.SetSessionMessagingEnabled(func() bool { return true })
	tool.SetClock(func() time.Time { return parkClock })
	ctx := tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), childID), childID)
	result := tool.Execute(ctx, map[string]any{
		"kind": "question", "text": "w6 expiry boot: may I use the staging key?", "wait": true,
		"authority": authority, "correlation_id": correlationID,
	})
	if result.IsError {
		t.Fatalf("message_parent park (%s) failed: %s", authority, result.ForLLM)
	}
	deadline := parkClock.Add(session.DefaultNeedsInputTTL)
	rec := w6qeMustLoad(t, al.GetSessionLifecycleStore(), childID)
	if rec.State != session.LifecycleNeedsInput || rec.NeedsInput == nil {
		t.Fatalf("park did not land needs_input: state=%s needs_input=%v", rec.State, rec.NeedsInput)
	}
	if rec.NeedsInput.CorrelationID != correlationID || !rec.NeedsInput.TTLDeadline.Equal(deadline) {
		t.Fatalf("parked needs_input = (corr %q, deadline %s), want (%q, %s)",
			rec.NeedsInput.CorrelationID, rec.NeedsInput.TTLDeadline.Format(time.RFC3339Nano),
			correlationID, deadline.Format(time.RFC3339Nano))
	}
	q := w6qeMustQuestion(t, al.GetSessionLifecycleStore(), childID)
	if q.Status != session.QuestionStatusOpen || !q.OriginalDeadline.Equal(deadline) ||
		q.Authority != authority || q.AskerSessionID != childID ||
		q.CorrelationID != correlationID || q.AskerGeneration != rec.Generation {
		t.Fatalf("park sidecar mismatch: status=%s authority=%s corr=%q asker=%s gen=%d deadline=%s, "+
			"want open %s corr=%q asker=%s gen=%d at %s",
			q.Status, q.Authority, q.CorrelationID, q.AskerSessionID, q.AskerGeneration,
			q.OriginalDeadline.Format(time.RFC3339Nano), authority, correlationID, childID,
			rec.Generation, deadline.Format(time.RFC3339Nano))
	}
	return deadline, rec.Generation
}

// w6qeStopChild stops exactly one session the way a human Stop does.
func w6qeStopChild(t *testing.T, al *AgentLoop, sessionID string) {
	t.Helper()
	canceller := NewSteerCanceller(al.GetSessionLifecycleStore())
	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}
	if _, err := canceller.StopTurns(context.Background(), sessionID, by, false, al.SteerGenerationCancel); err != nil {
		t.Fatalf("StopTurns(%s): %v", sessionID, err)
	}
}

// w6qeCountFatalErrors counts inbox entries of the parent that are fatal
// kind=error messages FROM childID; when phrase is non-empty the text must
// contain it.
func w6qeCountFatalErrors(t *testing.T, inbox *session.MessageInboxStore, parentID, childID, phrase string) int {
	t.Helper()
	entries, err := inbox.Entries(parentID)
	if err != nil {
		t.Fatalf("Inbox.Entries(%s): %v", parentID, err)
	}
	count := 0
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		kind, kerr := entry.Message.Discriminator()
		if kerr != nil || kind != "error" {
			continue
		}
		e, aerr := entry.Message.AsSessionMessageError()
		if aerr != nil || !e.Fatal || e.SessionId != childID {
			continue
		}
		if phrase != "" && !strings.Contains(e.Text, phrase) {
			continue
		}
		count++
	}
	return count
}

// w6qeRespond drives the parent's real delegate respond path against the
// fresh stores and records every Dispatch it asks for.
func w6qeRespond(t *testing.T, al *AgentLoop, lc *session.LifecycleStore, inbox *session.MessageInboxStore, parentID, childID, correlationID, answer string) (*tools.ToolResult, *w6qeResumeRecorder) {
	t.Helper()
	tool := tools.NewDelegateTool("", 0, 0)
	tool.SetLifecycleStore(lc)
	tool.SetMessageInbox(inbox)
	tool.SetSessionStore(al.GetSessionStore())
	tool.SetSessionMessagingEnabled(func() bool { return true })
	resumes := &w6qeResumeRecorder{}
	tool.SetSessionLauncher(resumes)
	ctx := tools.WithTranscriptSessionID(context.Background(), parentID)
	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": childID, "correlation_id": correlationID, "text": answer,
	})
	return result, resumes
}

// w6qeCountDelivered counts how many times the exact answer instruction the
// respond path writes (delegate_park.go::pendingAnswerInstruction's format)
// appears in the asker's transcript.
func w6qeCountDelivered(t *testing.T, store *session.UnifiedStore, sessionID, correlationID, answer string) int {
	t.Helper()
	want := fmt.Sprintf("Answer to your question (correlation_id=%s): %s", correlationID, answer)
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(%s): %v", sessionID, err)
	}
	count := 0
	for _, entry := range entries {
		if entry.Role == "user" && entry.Content == want {
			count++
		}
	}
	return count
}

// w6qeAssertGoalActive pins F0929-6: question expiry never clears a goal.
func w6qeAssertGoalActive(t *testing.T, goalID, when string) {
	t.Helper()
	g, err := resolveGoalRecordStore().Get(goalID)
	if err != nil {
		t.Fatalf("Get(goal after %s): %v", when, err)
	}
	if g.State != generated.GoalStateActive {
		t.Fatalf("goal state after %s = %q, want active — F0929-6/D1.8: expiry never clears an active goal", when, g.State)
	}
}

// TestW6QuestionExpiryBoot_ExpiredOwnerRequired_FailsAskerOwnerUnreachableOnce
// is the primary behavioral RED: an owner_required question parked through
// the production tool 48h ago (shipped 24h TTL → original deadline 24h in
// the past) must be expired by the production boot consumer — the asker
// fails(owner_unreachable), exactly one fatal "owner could not be reached"
// reaches the DIRECT parent, the sidecar closes unanswerable at the
// UNCHANGED original deadline, the goal stays active, no run is dispatched,
// a late owner answer is refused consuming nothing, and a second boot does
// not double-fail (T8).
func TestW6QuestionExpiryBoot_ExpiredOwnerRequired_FailsAskerOwnerUnreachableOnce(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-qexp-boot")
	parent := u1LaunchChild(t, al, root, "w6-qexp-boot-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-qexp-boot-child")
	goalID := activateTestGoalRecord(t, child.SessionID, "W6 expiry boot: goal must stay active")

	correlationID := "w6-qexp-boot-owner-required"
	parkClock := time.Now().Add(-48 * time.Hour)
	deadline, generation := w6qeParkQuestion(t, al, child.SessionID, correlationID,
		session.QuestionAuthorityOwnerRequired, parkClock)

	lc, inbox := w6qeReopen(t, al)

	// Pre-boot integrity on the FRESH stores: the restart must find the park
	// and the sidecar at the exact past deadline.
	rec := w6qeMustLoad(t, lc, child.SessionID)
	if rec.State != session.LifecycleNeedsInput || rec.NeedsInput == nil || !rec.NeedsInput.TTLDeadline.Equal(deadline) {
		t.Fatalf("fresh store lost the parked needs_input: state=%s needs_input=%v want deadline %s",
			rec.State, rec.NeedsInput, deadline.Format(time.RFC3339Nano))
	}
	before := w6qeMustQuestion(t, lc, child.SessionID)
	if before.Status != session.QuestionStatusOpen || !before.OriginalDeadline.Equal(deadline) {
		t.Fatalf("fresh store lost the open sidecar: status=%s deadline=%s want open at %s",
			before.Status, before.OriginalDeadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
	}

	// The production boot consumer, twice — T8 forbids a double-failure.
	var notices []string
	recovery := w6qeBootRecovery(t, al, lc, inbox, &notices)
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("SteerBootRecovery.Run #1: %v", err)
	}
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("SteerBootRecovery.Run #2: %v", err)
	}

	after := w6qeMustLoad(t, lc, child.SessionID)
	if after.State != session.LifecycleFailed {
		t.Fatalf("expired asker state after boot = %s, want failed — D1.8: the boot expiry check must fail the asker visibly (reason so far %q)",
			after.State, after.FailedReason)
	}
	if after.FailedReason != w6qeReasonOwnerUnreachable {
		t.Fatalf("expired asker failed reason = %q, want %q — D1.8 literal: failed(owner_unreachable)", after.FailedReason, w6qeReasonOwnerUnreachable)
	}
	if after.Generation != generation {
		t.Fatalf("asker generation after boot = %d, want %d — expiry must not dispatch a run or mint a generation",
			after.Generation, generation)
	}
	if fatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, w6qeOwnerUnreachableText); fatals != 1 {
		t.Fatalf("fatal %q notices to the DIRECT parent after two boots = %d, want exactly 1 — D1.8/D5: one fatal upward error, no double-failure",
			w6qeOwnerUnreachableText, fatals)
	}
	afterQ := w6qeMustQuestion(t, lc, child.SessionID)
	if afterQ.Status != session.QuestionStatusSuperseded {
		t.Fatalf("expired question status = %s, want superseded — D1.8: the expiry must close the question visibly, not leave it open", afterQ.Status)
	}
	if afterQ.Answerable() {
		t.Fatalf("expired question still answerable — D1.5: an expired question cannot be reserved")
	}
	if !afterQ.OriginalDeadline.Equal(deadline) {
		t.Fatalf("question deadline after boot = %s, want the ORIGINAL %s unchanged — D1.8: expiry never extends or re-baselines the TTL",
			afterQ.OriginalDeadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
	}
	w6qeAssertGoalActive(t, goalID, "question expiry at boot")

	// A late owner answer after expiry: refused as stale, consuming nothing.
	const lateAnswer = "late owner answer after expiry"
	late, resumes := w6qeRespond(t, al, lc, inbox, parent.SessionID, child.SessionID, correlationID, lateAnswer)
	if !late.IsError {
		t.Fatalf("late owner answer after expiry was accepted (%q) — D1.5/D1.8: an expired question cannot be reserved; refusal must be a visible error", late.ForLLM)
	}
	if len(resumes.calls) != 0 {
		t.Fatalf("late answer dispatched %d resumes (%v) — an expired question must resume nothing", len(resumes.calls), resumes.calls)
	}
	if delivered := w6qeCountDelivered(t, al.GetSessionStore(), child.SessionID, correlationID, lateAnswer); delivered != 0 {
		t.Fatalf("late answer text delivered into the asker transcript %d times — expiry refusal must consume nothing", delivered)
	}
	finalQ := w6qeMustQuestion(t, lc, child.SessionID)
	if finalQ.Status == session.QuestionStatusApplied || finalQ.Status == session.QuestionStatusAnswerPendingDelivery {
		t.Fatalf("question status after late answer = %s — the refused answer must never be reserved or applied", finalQ.Status)
	}
}

// TestW6QuestionExpiryBoot_NotExpiredOwnerRequired_SurvivesBootUnchanged is
// the positive control: the identical park 1h ago (deadline 23h out) must
// survive boot unchanged — a generic "boot fails everything" implementation
// cannot pass this.
func TestW6QuestionExpiryBoot_NotExpiredOwnerRequired_SurvivesBootUnchanged(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-qexp-boot-control")
	parent := u1LaunchChild(t, al, root, "w6-qexp-boot-control-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-qexp-boot-control-child")
	goalID := activateTestGoalRecord(t, child.SessionID, "W6 expiry boot control: goal stays active")

	correlationID := "w6-qexp-boot-control"
	parkClock := time.Now().Add(-time.Hour)
	deadline, generation := w6qeParkQuestion(t, al, child.SessionID, correlationID,
		session.QuestionAuthorityOwnerRequired, parkClock)

	lc, inbox := w6qeReopen(t, al)
	var notices []string
	recovery := w6qeBootRecovery(t, al, lc, inbox, &notices)
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("SteerBootRecovery.Run #1: %v", err)
	}
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("SteerBootRecovery.Run #2: %v", err)
	}

	after := w6qeMustLoad(t, lc, child.SessionID)
	if after.State != session.LifecycleNeedsInput || after.NeedsInput == nil {
		t.Fatalf("POSITIVE CONTROL FAILED — a not-yet-expired parked question was changed at boot: state=%s needs_input=%v; "+
			"a boot that fails live questions is a generic failure, not D1.8 expiry", after.State, after.NeedsInput)
	}
	if after.NeedsInput.CorrelationID != correlationID || !after.NeedsInput.TTLDeadline.Equal(deadline) {
		t.Fatalf("control needs_input changed: (corr %q, deadline %s), want (%q, %s) — D1.8: only AT OR AFTER the original deadline expires",
			after.NeedsInput.CorrelationID, after.NeedsInput.TTLDeadline.Format(time.RFC3339Nano),
			correlationID, deadline.Format(time.RFC3339Nano))
	}
	if after.Generation != generation {
		t.Fatalf("control generation after boot = %d, want %d — a live parked question must not be resumed by boot", after.Generation, generation)
	}
	if fatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, ""); fatals != 0 {
		t.Fatalf("control produced %d fatal error notices — a live question must produce none", fatals)
	}
	afterQ := w6qeMustQuestion(t, lc, child.SessionID)
	if afterQ.Status != session.QuestionStatusOpen || !afterQ.Answerable() || !afterQ.OriginalDeadline.Equal(deadline) {
		t.Fatalf("control sidecar changed: status=%s answerable=%t deadline=%s, want open, answerable, %s unchanged",
			afterQ.Status, afterQ.Answerable(), afterQ.OriginalDeadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
	}
	w6qeAssertGoalActive(t, goalID, "control boot")
}

// TestW6QuestionExpiryBoot_StoppedAsker drives D1.8's "even if the helper
// was later stopped": Stop preserves the ORIGINAL deadline in the sidecar
// (NeedsInput is cleared outside needs_input), and the boot consumer must
// still expire the stopped asker on it — while the not-expired control stays
// stopped with its question open. MIXED-RISK: if the control subtest fails
// too, the observed failure is W3b stopped-handling contamination, not
// isolated D1.8 expiry RED.
func TestW6QuestionExpiryBoot_StoppedAsker(t *testing.T) {
	t.Run("expired_stopped_asker_fails_owner_unreachable", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		wireSteerCompletionDeps(t, al)

		root := newTestSteeringSession(t, al, "ws-w6-qexp-stopped")
		parent := u1LaunchChild(t, al, root, "w6-qexp-stopped-parent")
		child := u1LaunchChild(t, al, parent.SessionID, "w6-qexp-stopped-child")
		goalID := activateTestGoalRecord(t, child.SessionID, "W6 stopped expiry: goal must stay active")

		correlationID := "w6-qexp-stopped-expired"
		parkClock := time.Now().Add(-48 * time.Hour)
		deadline, generation := w6qeParkQuestion(t, al, child.SessionID, correlationID,
			session.QuestionAuthorityOwnerRequired, parkClock)
		w6qeStopChild(t, al, child.SessionID)

		lc, inbox := w6qeReopen(t, al)
		stopped := w6qeMustLoad(t, lc, child.SessionID)
		if stopped.State != session.LifecycleStopped {
			t.Fatalf("setup: Stop did not land stopped: state=%s", stopped.State)
		}
		stoppedQ := w6qeMustQuestion(t, lc, child.SessionID)
		if stoppedQ.Status != session.QuestionStatusOpen || !stoppedQ.OriginalDeadline.Equal(deadline) {
			t.Fatalf("setup: Stop did not preserve the sidecar at the original deadline: status=%s deadline=%s want open at %s",
				stoppedQ.Status, stoppedQ.OriginalDeadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
		}

		var notices []string
		recovery := w6qeBootRecovery(t, al, lc, inbox, &notices)
		if err := recovery.Run(context.Background()); err != nil {
			t.Fatalf("SteerBootRecovery.Run #1: %v", err)
		}
		if err := recovery.Run(context.Background()); err != nil {
			t.Fatalf("SteerBootRecovery.Run #2: %v", err)
		}

		after := w6qeMustLoad(t, lc, child.SessionID)
		fatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, w6qeOwnerUnreachableText)
		afterQ := w6qeMustQuestion(t, lc, child.SessionID)
		// MIXED-RISK diagnostic: name exactly which D1.8 expectation broke and
		// that a wrong-reason failure (e.g. interrupted) is W3b contamination.
		if after.State != session.LifecycleFailed || after.FailedReason != w6qeReasonOwnerUnreachable ||
			after.Generation != generation || fatals != 1 || afterQ.Answerable() {
			t.Fatalf("MIXED-RISK (W3b stopped handling vs D1.8 expiry) — stopped expired asker after boot: "+
				"state=%s reason=%q want failed/%q generation=%d want=%d fatal_notices=%d want=1 sidecar_status=%s answerable=%t "+
				"(a failed reason other than %q — e.g. interrupted — is the W3b legacy stop path, not the D1.8 expiry consumer)",
				after.State, after.FailedReason, w6qeReasonOwnerUnreachable, after.Generation, generation,
				fatals, afterQ.Status, afterQ.Answerable(), w6qeReasonOwnerUnreachable)
		}
		if !afterQ.OriginalDeadline.Equal(deadline) {
			t.Fatalf("stopped asker deadline after boot = %s, want the ORIGINAL %s — expiry must use the preserved deadline",
				afterQ.OriginalDeadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
		}
		w6qeAssertGoalActive(t, goalID, "stopped-asker expiry at boot")
	})

	t.Run("not_expired_stopped_asker_control_stays_stopped", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		wireSteerCompletionDeps(t, al)

		root := newTestSteeringSession(t, al, "ws-w6-qexp-stopped-control")
		parent := u1LaunchChild(t, al, root, "w6-qexp-stopped-control-parent")
		child := u1LaunchChild(t, al, parent.SessionID, "w6-qexp-stopped-control-child")

		correlationID := "w6-qexp-stopped-control"
		parkClock := time.Now().Add(-time.Hour)
		deadline, generation := w6qeParkQuestion(t, al, child.SessionID, correlationID,
			session.QuestionAuthorityOwnerRequired, parkClock)
		w6qeStopChild(t, al, child.SessionID)

		lc, inbox := w6qeReopen(t, al)

		// The oracle absorbs a verified production fact instead of denying
		// it: stopping a never-ran steered child runs
		// steer_cancel.go::terminaliseNeverRanStop, whose interrupted report
		// goes through completionMessage — fatal for every outcome except a
		// lifecycle notice. The stop therefore writes EXACTLY ONE fatal
		// parent-inbox entry, its own interrupted notice, at Stop time,
		// BEFORE any boot. Zero fatals is unsatisfiable; the control pins
		// that one notice as the baseline and demands boot adds nothing.
		// (Exact text: the shared helper matches a Contains phrase; the stop
		// notice is asserted with equality.)
		const stopInterruptedText = "interrupted: the session was cancelled"
		stopNoticeFatals := func() int {
			t.Helper()
			entries, err := inbox.Entries(parent.SessionID)
			if err != nil {
				t.Fatalf("Inbox.Entries(%s): %v", parent.SessionID, err)
			}
			count := 0
			for _, entry := range entries {
				if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
					continue
				}
				kind, kerr := entry.Message.Discriminator()
				if kerr != nil || kind != "error" {
					continue
				}
				e, aerr := entry.Message.AsSessionMessageError()
				if aerr != nil || !e.Fatal || e.SessionId != child.SessionID || e.Text != stopInterruptedText {
					continue
				}
				count++
			}
			return count
		}

		// Gate BEFORE boot: exactly one fatal — the stop's own notice. Any
		// other count means the Stop path or this setup changed under the
		// oracle; stop and report the actual counts rather than re-baseline.
		beforeStopNotice := stopNoticeFatals()
		beforeTotal := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, "")
		if beforeStopNotice != 1 || beforeTotal != 1 {
			t.Fatalf("before-boot inbox holds %d fatal(s) whose text is exactly %q and %d fatal(s) in total, want 1 and 1 — "+
				"the stop's own interrupted notice must be the ONLY fatal the Stop wrote; refusing to loosen the oracle",
				beforeStopNotice, stopInterruptedText, beforeTotal)
		}

		var notices []string
		recovery := w6qeBootRecovery(t, al, lc, inbox, &notices)
		if err := recovery.Run(context.Background()); err != nil {
			t.Fatalf("SteerBootRecovery.Run: %v", err)
		}

		after := w6qeMustLoad(t, lc, child.SessionID)
		if after.State != session.LifecycleStopped {
			t.Fatalf("POSITIVE CONTROL FAILED — a stopped asker with a LIVE question did not stay stopped: state=%s reason=%q; "+
				"a boot that fails live stopped questions is a generic failure, not D1.8 expiry", after.State, after.FailedReason)
		}
		if after.Generation != generation {
			t.Fatalf("control generation after boot = %d, want %d", after.Generation, generation)
		}
		// Boot must add NOTHING to the fatal set: the stop's own notice
		// stays exactly one, total fatals stay exactly one, and the D1.8
		// owner-unreachable expiry text never appears — a LIVE stopped
		// question is never expired at boot.
		if afterStopNotice := stopNoticeFatals(); afterStopNotice != 1 {
			t.Fatalf("after boot %d fatal(s) carry exactly %q, want exactly 1 — boot must not touch the stop's own notice",
				afterStopNotice, stopInterruptedText)
		}
		if fatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, ""); fatals != 1 {
			t.Fatalf("control holds %d fatal error notices after boot, want exactly 1 (the stop's own interrupted notice) — "+
				"a boot that adds fatal notices to a live stopped question is a generic failure, not D1.8 expiry", fatals)
		}
		if ownerFatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, w6qeOwnerUnreachableText); ownerFatals != 0 {
			t.Fatalf("control produced %d %q fatal notices — a LIVE stopped question must never be expired at boot (D1.8/T14/T19)",
				ownerFatals, w6qeOwnerUnreachableText)
		}
		afterQ := w6qeMustQuestion(t, lc, child.SessionID)
		if afterQ.Status != session.QuestionStatusOpen || !afterQ.Answerable() || !afterQ.OriginalDeadline.Equal(deadline) {
			t.Fatalf("control sidecar changed: status=%s answerable=%t deadline=%s, want open, answerable, %s unchanged",
				afterQ.Status, afterQ.Answerable(), afterQ.OriginalDeadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
		}
	})
}

// TestW6QuestionExpiryBoot_ExpiredSelfOK_FailsAnswerTimeout drives D1.8's
// other half: an expired self_ok question fails the asker
// failed(answer_timeout) — never the owner-unreachable text — with exactly
// one fatal notice to the direct parent, and a late parent answer cannot
// reserve after expiry.
func TestW6QuestionExpiryBoot_ExpiredSelfOK_FailsAnswerTimeout(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-qexp-selfok")
	parent := u1LaunchChild(t, al, root, "w6-qexp-selfok-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-qexp-selfok-child")
	goalID := activateTestGoalRecord(t, child.SessionID, "W6 self_ok expiry: goal must stay active")

	correlationID := "w6-qexp-boot-self-ok"
	parkClock := time.Now().Add(-48 * time.Hour)
	deadline, generation := w6qeParkQuestion(t, al, child.SessionID, correlationID,
		session.QuestionAuthoritySelfOK, parkClock)

	lc, inbox := w6qeReopen(t, al)
	var notices []string
	recovery := w6qeBootRecovery(t, al, lc, inbox, &notices)
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("SteerBootRecovery.Run #1: %v", err)
	}
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("SteerBootRecovery.Run #2: %v", err)
	}

	after := w6qeMustLoad(t, lc, child.SessionID)
	if after.State != session.LifecycleFailed || after.FailedReason != w6qeReasonAnswerTimeout {
		t.Fatalf("expired self_ok asker after boot = (%s, %q), want (failed, %q) — D1.8: failed(answer_timeout) for a non-owner_required question",
			after.State, after.FailedReason, w6qeReasonAnswerTimeout)
	}
	if after.Generation != generation {
		t.Fatalf("asker generation after boot = %d, want %d — expiry must not dispatch a run", after.Generation, generation)
	}
	if fatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, ""); fatals != 1 {
		t.Fatalf("fatal notices to the DIRECT parent = %d, want exactly 1 — D1.8/D5: expiry sends a fatal error upward, once", fatals)
	}
	if ownerTextFatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, w6qeOwnerUnreachableText); ownerTextFatals != 0 {
		t.Fatalf("self_ok expiry produced %d %q notices — that text is owner_required-only (D1.8/F1011-Q2)", ownerTextFatals, w6qeOwnerUnreachableText)
	}
	afterQ := w6qeMustQuestion(t, lc, child.SessionID)
	if afterQ.Status != session.QuestionStatusSuperseded || afterQ.Answerable() || !afterQ.OriginalDeadline.Equal(deadline) {
		t.Fatalf("expired self_ok sidecar = (status %s, answerable %t, deadline %s), want superseded, unanswerable, %s unchanged",
			afterQ.Status, afterQ.Answerable(), afterQ.OriginalDeadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
	}
	w6qeAssertGoalActive(t, goalID, "self_ok expiry at boot")

	// A late parent answer cannot reserve after expiry.
	const lateAnswer = "late self_ok answer after expiry"
	late, resumes := w6qeRespond(t, al, lc, inbox, parent.SessionID, child.SessionID, correlationID, lateAnswer)
	if !late.IsError {
		t.Fatalf("late self_ok answer after expiry was accepted (%q) — D1.5: an expired question cannot be reserved", late.ForLLM)
	}
	if len(resumes.calls) != 0 {
		t.Fatalf("late answer dispatched %d resumes (%v)", len(resumes.calls), resumes.calls)
	}
	if delivered := w6qeCountDelivered(t, al.GetSessionStore(), child.SessionID, correlationID, lateAnswer); delivered != 0 {
		t.Fatalf("late answer delivered into the asker transcript %d times", delivered)
	}
	finalQ := w6qeMustQuestion(t, lc, child.SessionID)
	if finalQ.Status == session.QuestionStatusApplied || finalQ.Status == session.QuestionStatusAnswerPendingDelivery {
		t.Fatalf("question status after late answer = %s — a refused answer must never be reserved or applied", finalQ.Status)
	}
}
