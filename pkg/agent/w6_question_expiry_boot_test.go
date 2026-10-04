// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// W6 D1.8 pack — SUPERSEDED by the steering-commands amendment (ADR-20260928
// amendment "Steering commands: no person question", 2026-10-04, Correction
// C2): the 24-hour question-park expiry is REMOVED — the boot arms
// (expireQuestion, the relay closer, the expiry notice, the pending-question
// boot reader) are deleted from boot_sweep.go with NO replacement expiry,
// and the root clarification cards and message_parent are untouched. The
// ruling this pack now pins, from that amendment: a helper question does not
// expire, and it does not park (fail) its helper — however old it is, and
// through any number of boots.
//
// The pack was RED for the original D1.8 expiry ("the 24-hour question-park
// limit", frozen asset a-u1-runtime-contracts-20261002). Its expiry
// expectations are retired, not deleted; each test now pins the surviving
// rule over the SAME scenario, at the same strength:
//
//	ExpiredOwnerRequired_FailsAskerOwnerUnreachableOnce ->
//		AgedOwnerRequired_SurvivesBootUnchanged (the aged park and its
//		sidecar survive two boots untouched; zero fatal notices; the
//		retired owner-unreachable outcome asserted negatively; the late
//		answer is not refused)
//	NotExpiredOwnerRequired_SurvivesBootUnchanged -> unchanged (fresh-park
//		control: boot changes no parked question)
//	StoppedAsker/expired_stopped_asker_fails_owner_unreachable ->
//		aged_stopped_asker_stays_stopped_question_open
//	StoppedAsker/not_expired_stopped_asker_control_stays_stopped ->
//		unchanged
//	ExpiredSelfOK_FailsAnswerTimeout -> AgedSelfOK_SurvivesBootUnchanged
//
// Test plan (elicify-test-writing step 1)
//
//   behaviour under test: after a process restart, SteerBootRecovery.Run —
//     the consumer gateway_boot.go::wireSteerDeps wires into its BootHook —
//     changes NOTHING about a parked question, whatever its age: the asker
//     keeps its needs_input record (same correlation, same recorded
//     deadline, same generation), the sidecar stays open and answerable at
//     the unchanged original deadline, no fatal error reaches the direct
//     parent, the asker's goal stays active, no run is dispatched, and a
//     second boot changes nothing. A stopped asker stays stopped with its
//     question open. A late answer — the question never expired — is not
//     refused.
//
//   specification source: the amendment's Correction C2 (expiry removed, no
//     replacement) plus the delegate_respond.go contract the amendment
//     locked (a respond is an ordinary steering message: no question
//     reservation, no authority gate, no stale refusal; a stopped record
//     revives, a live record takes the steering queue — a needs_input
//     record is neither, so nothing is dispatched and nothing is consumed).
//     session.PendingQuestion.Answerable reads the STATUS, never the clock
//     — the recorded deadline is data, and nothing acts on its age. Every
//     expected value below derives from those rules, never from observed
//     code.
//
//   unit boundary: everything real — LifecycleStore, UnifiedStore,
//     MessageInboxStore, the QuestionStore sidecar, MessageParentTool's
//     production park, SteerCanceller.StopTurns, SteerBootRecovery.Run with
//     the real upward deliverer, the production delegate respond. The ONLY
//     injected dependency is MessageParentTool's existing SetClock at park
//     time (a caller-side dependency that already exists — no production
//     hook is added); boot itself runs on the real clock. No model turn is
//     ever started: the resume recorder proves Dispatch is (or is not)
//     asked, and a generation change would expose any automatic resume.
//
//   case table:
//     aged owner_required (48h past its original deadline) + boot x2 ->
//       needs_input unchanged (correlation, deadline, generation), sidecar
//       open + answerable at the ORIGINAL deadline, zero fatals (and
//       specifically none with the retired owner-unreachable text), goal
//       active, late respond not refused and consuming nothing, second boot
//       changes nothing.
//     fresh owner_required + boot x2 (positive control) -> identical
//       survival — boot changes no parked question, whatever its age.
//     aged owner_required + Stop + boot -> the asker stays STOPPED (boot
//       adds no failure), question open + answerable at the original
//       deadline, zero fatals, goal active.
//     fresh owner_required + Stop + boot (positive control) -> stays
//       stopped with the question open, boot adds nothing.
//     aged self_ok + boot x2 -> needs_input unchanged, zero fatals (and
//       specifically none with the retired answer-timeout text), sidecar
//       open + answerable, goal active, late respond not refused.
//
//   known gaps (deliberately not covered here):
//     - the relay half lives in w6_question_expiry_relay_test.go (boot must
//       not close any relay record either).
//     - the D1.4 trusted-provenance writer and the D1.7 withdrawal audit
//       seams are unrelated to the expiry and stay with their own packs.
//
//   deferred to CHECK (skill step 4 items 2-3): green-after-implementation
//     and the mutation probes — re-adding any expiry consumer (an aged-park
//     leg fails on state or fatal count); closing or re-baselining the
//     sidecar on age (killed by the open/answerable/original-deadline
//     oracle); refusing a late answer (killed by the respond legs);
//     boot-resuming a parked asker (killed by the generation and
//     resume-recorder oracles).

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

// Retired D1.8 literals, kept ONLY as negative oracles: no boot, no stop and
// no respond may ever produce them again (Correction C2 removed the expiry
// that owned them).
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
	return steer.LaunchResult{}, fmt.Errorf("boot must never launch a new session")
}

func (r *w6qeResumeRecorder) Dispatch(_ context.Context, sessionID string, generation int) (steer.DispatchResult, error) {
	r.calls = append(r.calls, w6qeResumeCall{sessionID: sessionID, generation: generation})
	return steer.DispatchResult{State: steer.DispatchRunning, Generation: generation}, nil
}

// w6qeQuestionStore is the same store resolution the production park uses
// (pkg/tools/message_parent.go's question store resolution).
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
// instant. The deadline stays RECORDED DATA under the superseding rule;
// nothing may act on its age. Returns the deadline and the asker's
// generation.
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
// respond path frames (delegate_respond.go::respondAnswerInstruction's
// format) appears in the asker's transcript.
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

// w6qeAssertGoalActive pins F0929-6: nothing about a question — its age
// included — ever clears a goal.
func w6qeAssertGoalActive(t *testing.T, goalID, when string) {
	t.Helper()
	g, err := resolveGoalRecordStore().Get(goalID)
	if err != nil {
		t.Fatalf("Get(goal after %s): %v", when, err)
	}
	if g.State != generated.GoalStateActive {
		t.Fatalf("goal state after %s = %q, want active — F0929-6: nothing about a question clears an active goal", when, g.State)
	}
}

// TestW6QuestionExpiryBoot_AgedOwnerRequired_SurvivesBootUnchanged (was
// TestW6QuestionExpiryBoot_ExpiredOwnerRequired_FailsAskerOwnerUnreachableOnce)
// is the primary behavioral oracle of the superseding rule: an owner_required
// question parked through the production tool 48h ago (shipped 24h TTL → the
// original deadline 24h in the past) survives two boots UNCHANGED — the
// asker stays needs_input with the same park at the same generation, the
// sidecar stays open and answerable at the UNCHANGED original deadline, no
// fatal reaches the direct parent (and specifically none with the retired
// owner-unreachable text), the goal stays active, a late answer is not
// refused and consumes nothing, and the second boot changes nothing.
func TestW6QuestionExpiryBoot_AgedOwnerRequired_SurvivesBootUnchanged(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-qexp-boot")
	parent := u1LaunchChild(t, al, root, "w6-qexp-boot-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-qexp-boot-child")
	goalID := activateTestGoalRecord(t, child.SessionID, "W6 aged question boot: goal must stay active")

	correlationID := "w6-qexp-boot-owner-required"
	parkClock := time.Now().Add(-48 * time.Hour)
	deadline, generation := w6qeParkQuestion(t, al, child.SessionID, correlationID,
		session.QuestionAuthorityOwnerRequired, parkClock)

	lc, inbox := w6qeReopen(t, al)

	// Pre-boot integrity on the FRESH stores: the restart must find the park
	// and the sidecar at the exact past deadline — and the sidecar is
	// genuinely AGED past its original deadline before boot runs, so the
	// survival asserted below is the no-expiry rule, not a fresh park.
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
	if !before.Expired(time.Now()) {
		t.Fatalf("setup: the sidecar is not aged past its original deadline %s — the scenario must be the aged shape the retired expiry owned", deadline.Format(time.RFC3339Nano))
	}

	// The production boot consumer, twice — the second run must change
	// nothing, exactly as the retired expiry's idempotence guarantee did.
	var notices []string
	recovery := w6qeBootRecovery(t, al, lc, inbox, &notices)
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("SteerBootRecovery.Run #1: %v", err)
	}
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("SteerBootRecovery.Run #2: %v", err)
	}

	// ORACLE (Correction C2 — no replacement expiry): the asker is NOT
	// failed by its question's age — still needs_input with the SAME park at
	// the SAME generation, however old the question is.
	after := w6qeMustLoad(t, lc, child.SessionID)
	if after.State != session.LifecycleNeedsInput || after.NeedsInput == nil {
		t.Fatalf("aged asker state after boot = %s needs_input=%v, want needs_input — C2: a helper question does not expire and does not park (fail) its helper",
			after.State, after.NeedsInput)
	}
	if after.NeedsInput.CorrelationID != correlationID || !after.NeedsInput.TTLDeadline.Equal(deadline) {
		t.Fatalf("aged needs_input changed: (corr %q, deadline %s), want (%q, %s) — boot never rewrites or consumes the park",
			after.NeedsInput.CorrelationID, after.NeedsInput.TTLDeadline.Format(time.RFC3339Nano),
			correlationID, deadline.Format(time.RFC3339Nano))
	}
	if after.Generation != generation {
		t.Fatalf("asker generation after boot = %d, want %d — an aged question must not dispatch a run or mint a generation",
			after.Generation, generation)
	}
	// ORACLE: no expiry notice exists anymore — zero fatal errors of ANY
	// text, and specifically none carrying the retired owner-unreachable
	// outcome.
	if fatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, ""); fatals != 0 {
		t.Fatalf("boot produced %d fatal error notice(s) for the aged question, want 0 — the expiry notice is retired with the expiry (C2); nothing fails the helper", fatals)
	}
	if ownerFatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, w6qeOwnerUnreachableText); ownerFatals != 0 {
		t.Fatalf("boot produced %d %q fatal(s), want 0 — that outcome is retired (C2)", ownerFatals, w6qeOwnerUnreachableText)
	}
	// ORACLE: the question is still OPEN and ANSWERABLE at its ORIGINAL
	// deadline — age alone never closes or re-baselines it (Answerable reads
	// the status, never the clock).
	afterQ := w6qeMustQuestion(t, lc, child.SessionID)
	if afterQ.Status != session.QuestionStatusOpen || !afterQ.Answerable() {
		t.Fatalf("aged question after boot = (status %s, answerable %t), want open and answerable — C2: a helper question does not expire", afterQ.Status, afterQ.Answerable())
	}
	if !afterQ.OriginalDeadline.Equal(deadline) {
		t.Fatalf("question deadline after boot = %s, want the ORIGINAL %s unchanged — nothing re-baselines the recorded deadline",
			afterQ.OriginalDeadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
	}
	w6qeAssertGoalActive(t, goalID, "aged question at boot")

	// A late answer long after the original deadline is NOT refused: the
	// question never expired, and a respond is an ordinary steering message.
	// A needs_input asker is neither stopped nor terminal, so respond
	// dispatches nothing and consumes nothing — the parked child takes the
	// queued answer only when something actually resumes it.
	const lateAnswer = "late owner answer long after the original deadline"
	late, resumes := w6qeRespond(t, al, lc, inbox, parent.SessionID, child.SessionID, correlationID, lateAnswer)
	if late.IsError {
		t.Fatalf("late owner answer after the original deadline was refused (%q) — C2: the question does not expire; there is no stale refusal", late.ForLLM)
	}
	if len(resumes.calls) != 0 {
		t.Fatalf("respond to a needs_input asker dispatched %d resume(s) (%v) — a parked asker is not a stopped one; respond dispatches nothing", len(resumes.calls), resumes.calls)
	}
	if delivered := w6qeCountDelivered(t, al.GetSessionStore(), child.SessionID, correlationID, lateAnswer); delivered != 0 {
		t.Fatalf("answer text delivered into the asker transcript %d time(s) — a parked asker consumes nothing until it is resumed", delivered)
	}
	finalQ := w6qeMustQuestion(t, lc, child.SessionID)
	if finalQ.Status != session.QuestionStatusOpen || !finalQ.Answerable() {
		t.Fatalf("question after the late answer = (status %s, answerable %t), want still open and answerable — a respond is an ordinary steering message; it consumes no question record", finalQ.Status, finalQ.Answerable())
	}
}

// TestW6QuestionExpiryBoot_NotExpiredOwnerRequired_SurvivesBootUnchanged is
// the fresh-park control: the identical park 1h ago (deadline 23h out) must
// survive boot unchanged — boot changes no parked question, whatever its
// age, and a boot that touched live questions would be a generic failure,
// not the superseding rule.
func TestW6QuestionExpiryBoot_NotExpiredOwnerRequired_SurvivesBootUnchanged(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-qexp-boot-control")
	parent := u1LaunchChild(t, al, root, "w6-qexp-boot-control-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-qexp-boot-control-child")
	goalID := activateTestGoalRecord(t, child.SessionID, "W6 fresh question boot control: goal stays active")

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
		t.Fatalf("POSITIVE CONTROL FAILED — a fresh parked question was changed at boot: state=%s needs_input=%v; "+
			"a boot that touches live questions is a generic failure, not the superseding rule", after.State, after.NeedsInput)
	}
	if after.NeedsInput.CorrelationID != correlationID || !after.NeedsInput.TTLDeadline.Equal(deadline) {
		t.Fatalf("control needs_input changed: (corr %q, deadline %s), want (%q, %s) — boot never rewrites the park",
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

// TestW6QuestionExpiryBoot_StoppedAsker pins the superseding rule for a
// stopped helper: Stop preserves the question sidecar at the ORIGINAL
// deadline (NeedsInput is cleared outside needs_input), and boot leaves the
// stopped asker EXACTLY stopped — however old the question is, boot adds no
// failure and touches no question record — while the fresh control stays
// stopped with its question open too.
func TestW6QuestionExpiryBoot_StoppedAsker(t *testing.T) {
	t.Run("aged_stopped_asker_stays_stopped_question_open", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		wireSteerCompletionDeps(t, al)

		root := newTestSteeringSession(t, al, "ws-w6-qexp-stopped")
		parent := u1LaunchChild(t, al, root, "w6-qexp-stopped-parent")
		child := u1LaunchChild(t, al, parent.SessionID, "w6-qexp-stopped-child")
		goalID := activateTestGoalRecord(t, child.SessionID, "W6 stopped aged question: goal must stay active")

		correlationID := "w6-qexp-stopped-aged"
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
		if stoppedQ.Status != session.QuestionStatusOpen || !stoppedQ.OriginalDeadline.Equal(deadline) || !stoppedQ.Expired(time.Now()) {
			t.Fatalf("setup: Stop did not preserve the sidecar aged at the original deadline: status=%s deadline=%s want open at %s",
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

		// ORACLE (Correction C2): the aged question neither expires nor
		// fails its stopped helper — the asker is EXACTLY where Stop left
		// it, at the same generation, with zero fatals of any text (and
		// specifically none with the retired owner-unreachable outcome).
		after := w6qeMustLoad(t, lc, child.SessionID)
		if after.State != session.LifecycleStopped {
			t.Fatalf("aged stopped asker after boot = %s (reason %q), want stopped — C2: a helper question does not expire and does not fail its helper; boot adds no stop and no failure",
				after.State, after.FailedReason)
		}
		if after.Generation != generation {
			t.Fatalf("stopped asker generation after boot = %d, want %d — boot must not revive or re-generate a stopped asker over an aged question",
				after.Generation, generation)
		}
		if fatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, ""); fatals != 0 {
			t.Fatalf("boot produced %d fatal error notice(s) for the stopped asker, want 0 — the expiry notice is retired with the expiry (C2)", fatals)
		}
		if ownerFatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, w6qeOwnerUnreachableText); ownerFatals != 0 {
			t.Fatalf("boot produced %d %q fatal(s), want 0 — that outcome is retired (C2)", ownerFatals, w6qeOwnerUnreachableText)
		}
		afterQ := w6qeMustQuestion(t, lc, child.SessionID)
		if afterQ.Status != session.QuestionStatusOpen || !afterQ.Answerable() || !afterQ.OriginalDeadline.Equal(deadline) {
			t.Fatalf("aged stopped asker sidecar after boot = (status %s, answerable %t, deadline %s), want open, answerable, %s unchanged — boot touches no question record",
				afterQ.Status, afterQ.Answerable(), afterQ.OriginalDeadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
		}
		w6qeAssertGoalActive(t, goalID, "stopped-asker aged question at boot")
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

		// The oracle absorbs the verified production facts instead of
		// denying them: a STOPPED landing publishes NOTHING upward as a
		// fatal — the pre-ADR "interrupted: the session was cancelled" error
		// event is deleted (steer_cancel.go::reportSteeredSessionTerminalUpward,
		// which routes a never-ran stop through landSteeredStopReport). The
		// direct parent learns through the D6 stopped-child notice: a
		// NON-fatal error-kind inbox entry whose text is the production
		// builder's over the LANDED transition
		// (stopped_notice.go::stoppedChildNoticeText, composed from
		// LifecycleStore.ListStoppedTransitions) — never a sentence invented
		// here. The notice is delivered while StopTurns runs
		// (steer_cancel.go::landSteeredStopReport publishes it only after
		// the landing and its ledger history are durable), so it is readable
		// BEFORE any boot.
		transitions, err := lc.ListStoppedTransitions(child.SessionID)
		if err != nil {
			t.Fatalf("ListStoppedTransitions(%s): %v", child.SessionID, err)
		}
		if len(transitions) != 1 {
			t.Fatalf("setup: fresh store holds %d landed stop transitions for the stopped child, want exactly 1 — "+
				"the D6 notice text cannot be derived without the single landed transition", len(transitions))
		}
		wantNoticeText := stoppedChildNoticeText(transitions[0])
		stoppedChildNotices := func() int {
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
				if aerr != nil || e.Fatal || e.SessionId != child.SessionID || e.Text != wantNoticeText {
					continue
				}
				count++
			}
			return count
		}

		// Gate BEFORE boot: the child is already stopped with the question
		// still open and answerable at the original deadline, the inbox
		// carries the stop's own D6 notice exactly once, and ZERO fatal
		// errors — no expiry may ever fire, before boot or at it.
		before := w6qeMustLoad(t, lc, child.SessionID)
		if before.State != session.LifecycleStopped {
			t.Fatalf("setup: Stop did not land stopped: state=%s", before.State)
		}
		beforeQ := w6qeMustQuestion(t, lc, child.SessionID)
		if beforeQ.Status != session.QuestionStatusOpen || !beforeQ.Answerable() || !beforeQ.OriginalDeadline.Equal(deadline) {
			t.Fatalf("setup: Stop did not preserve the sidecar: status=%s answerable=%t deadline=%s want open, answerable, %s",
				beforeQ.Status, beforeQ.Answerable(), beforeQ.OriginalDeadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
		}
		if beforeNotices := stoppedChildNotices(); beforeNotices != 1 {
			t.Fatalf("before-boot inbox holds %d stopped-child notice(s) with the production text, want exactly 1 — "+
				"the Stop's own D6 notice must be present and deduped before boot", beforeNotices)
		}
		if fatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, w6qeOwnerUnreachableText); fatals != 0 {
			t.Fatalf("before-boot inbox holds %d %q fatal(s), want 0 — that outcome is retired (C2)", fatals, w6qeOwnerUnreachableText)
		}
		if totalFatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, ""); totalFatals != 0 {
			t.Fatalf("before-boot inbox holds %d fatal(s) in total, want 0 — a STOPPED landing publishes nothing upward "+
				"as a fatal (steer_cancel.go::reportSteeredSessionTerminalUpward)", totalFatals)
		}

		var notices []string
		recovery := w6qeBootRecovery(t, al, lc, inbox, &notices)
		if err := recovery.Run(context.Background()); err != nil {
			t.Fatalf("SteerBootRecovery.Run: %v", err)
		}

		after := w6qeMustLoad(t, lc, child.SessionID)
		if after.State != session.LifecycleStopped {
			t.Fatalf("POSITIVE CONTROL FAILED — a stopped asker with a LIVE question did not stay stopped: state=%s reason=%q; "+
				"a boot that touches live stopped questions is a generic failure, not the superseding rule", after.State, after.FailedReason)
		}
		if after.Generation != generation {
			t.Fatalf("control generation after boot = %d, want %d", after.Generation, generation)
		}
		// Boot must add NOTHING: the D6 notice stays exactly one (deduped —
		// the boot replay never re-appends a stored notice), zero fatals of
		// any text appear, and the retired expiry text never appears — no
		// question is ever expired at boot.
		if afterNotices := stoppedChildNotices(); afterNotices != 1 {
			t.Fatalf("after boot %d stopped-child notice(s) carry the production text, want exactly 1 — "+
				"boot must neither duplicate nor remove the stop's own notice", afterNotices)
		}
		if fatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, ""); fatals != 0 {
			t.Fatalf("control holds %d fatal error notice(s) after boot, want 0 — boot must add no fatal to a live stopped question", fatals)
		}
		if ownerFatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, w6qeOwnerUnreachableText); ownerFatals != 0 {
			t.Fatalf("control produced %d %q fatal notices, want 0 — no question is ever expired at boot (C2)",
				ownerFatals, w6qeOwnerUnreachableText)
		}
		afterQ := w6qeMustQuestion(t, lc, child.SessionID)
		if afterQ.Status != session.QuestionStatusOpen || !afterQ.Answerable() || !afterQ.OriginalDeadline.Equal(deadline) {
			t.Fatalf("control sidecar changed: status=%s answerable=%t deadline=%s, want open, answerable, %s unchanged",
				afterQ.Status, afterQ.Answerable(), afterQ.OriginalDeadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
		}
	})
}

// TestW6QuestionExpiryBoot_AgedSelfOK_SurvivesBootUnchanged (was
// TestW6QuestionExpiryBoot_ExpiredSelfOK_FailsAnswerTimeout) pins the
// superseding rule for the self_ok shape: an aged self_ok question keeps its
// asker in needs_input — no answer-timeout failure exists anymore, zero
// fatals reach the direct parent (the retired answer_timeout outcome
// asserted negatively), the sidecar stays open and answerable at the
// original deadline, the goal stays active, and a late answer is not
// refused.
func TestW6QuestionExpiryBoot_AgedSelfOK_SurvivesBootUnchanged(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-qexp-selfok")
	parent := u1LaunchChild(t, al, root, "w6-qexp-selfok-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-qexp-selfok-child")
	goalID := activateTestGoalRecord(t, child.SessionID, "W6 self_ok aged question: goal must stay active")

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
	if after.State != session.LifecycleNeedsInput || after.NeedsInput == nil {
		t.Fatalf("aged self_ok asker after boot = (%s, needs_input %v), want needs_input — C2: a helper question does not expire and does not fail its helper",
			after.State, after.NeedsInput)
	}
	if after.Generation != generation {
		t.Fatalf("asker generation after boot = %d, want %d — an aged question must not dispatch a run", after.Generation, generation)
	}
	if fatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, ""); fatals != 0 {
		t.Fatalf("fatal notices to the DIRECT parent = %d, want 0 — the expiry fatal is retired with the expiry (C2)", fatals)
	}
	if timeoutFatals := w6qeCountFatalErrors(t, inbox, parent.SessionID, child.SessionID, w6qeReasonAnswerTimeout); timeoutFatals != 0 {
		t.Fatalf("aged self_ok question produced %d %q fatal(s), want 0 — that outcome is retired (C2)", timeoutFatals, w6qeReasonAnswerTimeout)
	}
	afterQ := w6qeMustQuestion(t, lc, child.SessionID)
	if afterQ.Status != session.QuestionStatusOpen || !afterQ.Answerable() || !afterQ.OriginalDeadline.Equal(deadline) {
		t.Fatalf("aged self_ok sidecar = (status %s, answerable %t, deadline %s), want open, answerable, %s unchanged — C2: a helper question does not expire",
			afterQ.Status, afterQ.Answerable(), afterQ.OriginalDeadline.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano))
	}
	w6qeAssertGoalActive(t, goalID, "self_ok aged question at boot")

	// A late answer cannot be refused — there is no expiry to refuse it.
	const lateAnswer = "late self_ok answer long after the original deadline"
	late, resumes := w6qeRespond(t, al, lc, inbox, parent.SessionID, child.SessionID, correlationID, lateAnswer)
	if late.IsError {
		t.Fatalf("late self_ok answer after the original deadline was refused (%q) — C2: the question does not expire; there is no stale refusal", late.ForLLM)
	}
	if len(resumes.calls) != 0 {
		t.Fatalf("respond to a needs_input asker dispatched %d resume(s) (%v)", len(resumes.calls), resumes.calls)
	}
	if delivered := w6qeCountDelivered(t, al.GetSessionStore(), child.SessionID, correlationID, lateAnswer); delivered != 0 {
		t.Fatalf("late answer delivered into the asker transcript %d time(s) — a parked asker consumes nothing until it is resumed", delivered)
	}
	finalQ := w6qeMustQuestion(t, lc, child.SessionID)
	if finalQ.Status != session.QuestionStatusOpen || !finalQ.Answerable() {
		t.Fatalf("question after the late answer = (status %s, answerable %t), want still open and answerable — a respond is an ordinary steering message; it consumes no question record", finalQ.Status, finalQ.Answerable())
	}
}
