package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Regression pack for ADR-20260928 D6b's completed-path sentence (authority:
// git object cd20cf8b365e7bc8a010c731a8f6830e14397c23,
// docs/internal/architecture/ADR-20260928-sub-agent-control-plane.md, D6b):
//
//	"On this completed path open_questions is empty; parkedQuestions stays
//	only for a non-completion hand-back (e.g. a stopped-child report),
//	excluding open-relay questions (MIN-006)."
//
// The completion frontier cuts at a stopped node, so a parent whose only
// outstanding question came from a STOPPED child still completes — and per
// D6b its final hand-back must carry no open questions. The question's
// legitimate home on every non-completion path is the parent's inbox (the D1
// relay: message_parent(kind="question") → steer.OutcomeParkedQuestion →
// SteerUpwardDeliverer → parent inbox); the completed hand-back must not
// re-carry it.
//
// Absent seams, reported rather than faked as behavioral RED (dispatch
// ruling): the D6 stopped-child notice delivery is unimplemented — this test
// deliberately depends on it NOT at all, because the frontier must cut on
// record state alone; and the non-completion hand-back producer that would
// call parkedQuestions with the MIN-006 relay exclusion does not exist yet —
// completionMessage's final-answer branch is parkedQuestions' only caller
// today. The needs_input-blocks contrast is pinned by
// TestCompletion_LastChildWakesParent_NeedsInputSiblingHoldsBackCompletion
// and is not duplicated here.

// TestCompletion_CompletedHandbackWithStoppedChildQuestion_CarriesNoOpenQuestions
// drives the REAL completion path (completeSteeredTurn → completionMessage →
// SteerUpwardDeliverer) for a parent whose only descendant is a stopped child
// whose question is still relayed in the parent's inbox, and asserts from ADR
// D6b — never from observed behavior — that the hand-back persisted in the
// root's inbox carries zero open questions, exactly the parent's genuine
// final answer, and none of the stale content seeded into the parent's
// transcript.
func TestCompletion_CompletedHandbackWithStoppedChildQuestion_CarriesNoOpenQuestions(t *testing.T) {
	al, cleanup := newSteerALWithProvider(t, &depthEchoProvider{})
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-d6b-completed")
	parent := launchRunningChild(t, al, rootID, "call-d6b-parent")
	stoppedChild := launchRunningChild(t, al, parent.SessionID, "call-d6b-worker")

	// The stopped child's outstanding question, relayed into the parent's
	// inbox through the persisted store with the generated wire encoding —
	// the same seeding shape the audited D6b pack uses for its parked
	// sibling. In production this message arrives via
	// message_parent(kind="question") → I-5 Deliver; the question outlives
	// the child's stop (D1.8: the park TTL is independent and continues
	// during a stop).
	const questionText = "D6B-COMPLETED-PATH: which region should the report cover?"
	var question generated.SessionMessage
	if err := question.FromSessionMessageQuestion(generated.SessionMessageQuestion{
		Kind: generated.SessionMessageQuestionKindQuestion, MessageId: "q-d6b-completed-1",
		SessionId: stoppedChild.SessionID, SenderIdentity: stoppedChild.AgentID,
		CreatedAt: time.Now().UTC(), Depth: 1, Text: questionText,
	}); err != nil {
		t.Fatalf("encode question: %v", err)
	}
	if _, err := al.GetMessageInboxStore().Append(parent.SessionID, question); err != nil {
		t.Fatalf("Append(question): %v", err)
	}

	// Instrument check, BEFORE completion: the question is genuinely parked
	// at the parent's inbox as a question message — the non-completion
	// surface D6b permits to hold questions. Drain returns unacked entries
	// without acking (pkg/session contract), so this does not disturb the
	// fixture; it proves the seed is visible to the same Drain API
	// parkedQuestions reads, so a green below could not be an invisible seed.
	inboxMsgs, _, _, err := al.GetMessageInboxStore().Drain(parent.SessionID, stoppedChild.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(parent) instrument check: %v", err)
	}
	if len(inboxMsgs) != 1 {
		t.Fatalf("parent inbox messages before completion = %d, want 1 (the seeded question) — instrument check", len(inboxMsgs))
	}
	seedKind, seedKindErr := inboxMsgs[0].Discriminator()
	if seedKindErr != nil || seedKind != "question" {
		t.Fatalf("parent inbox message kind = %q (err %v), want \"question\" — instrument check", seedKind, seedKindErr)
	}

	// The child's genuine stop: state + stop note, the landed stopped-record
	// shape. The D6 stopped-child NOTICE is a separate, still-unimplemented
	// deliverable; this path must cut on record state alone, so the test
	// never depends on the notice.
	stoppedChild.State = session.LifecycleStopped
	stoppedChild.StopNote = &session.StopNote{
		At: time.Now().UTC(), By: "human:dan",
		Seq: uint64(stoppedChild.Generation), Cause: session.StopCauseStop,
	}
	if persistErr := al.GetSessionLifecycleStore().Persist(stoppedChild); persistErr != nil {
		t.Fatalf("Persist(stopped): %v", persistErr)
	}

	// Stale content seeded into the parent's own transcript only; the
	// hand-back must carry the parent's real result, never this.
	const staleText = "STALE PARENT TEXT MUST NOT REACH ROOT"
	if transcriptErr := al.GetSessionStore().AppendTranscriptStrict(parent.SessionID, session.TranscriptEntry{
		ID: "d6b-parent-stale", Role: "assistant", Content: staleText, Timestamp: time.Now().UTC(),
	}); transcriptErr != nil {
		t.Fatalf("AppendTranscriptStrict(stale): %v", transcriptErr)
	}

	const parentAnswer = "parent final answer for the completed-handback d6b proof"
	if completeErr := al.completeSteeredTurn(context.Background(), parent, turnResult{finalContent: parentAnswer}, nil); completeErr != nil {
		t.Fatalf("completeSteeredTurn(parent): %v", completeErr)
	}

	// The frontier cut at the stopped child, so the completed path was
	// genuinely reached — the OpenQuestions assertion below would otherwise
	// be vacuous (nothing was ever handed back).
	gotParent, err := al.GetSessionLifecycleStore().Load(parent.SessionID)
	if err != nil {
		t.Fatalf("Load(parent): %v", err)
	}
	if gotParent.State != session.LifecycleCompleted {
		t.Fatalf("parent state = %q, want completed — D6b/D6: a stopped descendant does not block and cuts traversal, so the parent's done lands", gotParent.State)
	}

	// Read the hand-back back from the PERSISTED root inbox — never an
	// intercepted in-memory message.
	msgs, _, _, err := al.GetMessageInboxStore().Drain(rootID, parent.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(root): %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("root messages = %d, want 1 (the parent's final hand-back)", len(msgs))
	}
	hbKind, hbKindErr := msgs[0].Discriminator()
	if hbKindErr != nil || hbKind != "handback" {
		t.Fatalf("root message kind = %q (err %v), want \"handback\"", hbKind, hbKindErr)
	}
	hb, hbErr := msgs[0].AsSessionMessageHandback()
	if hbErr != nil {
		t.Fatalf("AsSessionMessageHandback: %v", hbErr)
	}
	if hb.Mode != generated.SessionMessageHandbackModeFinal {
		t.Fatalf("handback mode = %q, want final — the parent's completion hand-back", hb.Mode)
	}

	// D6b (ADR object cd20cf8b): "On this completed path open_questions is
	// empty". The stopped child's relayed question stays parked at the
	// parent's inbox — its non-completion home; it must not ride the
	// completed hand-back.
	if len(hb.OpenQuestions) != 0 {
		t.Fatalf("completed hand-back open_questions = %q, want empty — D6b: on the completed path "+
			"open_questions is empty; parkedQuestions stays only for a non-completion hand-back "+
			"(stopped-child report), excluding relayed questions (MIN-006)", hb.OpenQuestions)
	}

	// Genuine final result, no stale content.
	if hb.ResultSoFar != parentAnswer {
		t.Fatalf("handback result_so_far = %q, want %q — the parent's genuine final answer", hb.ResultSoFar, parentAnswer)
	}
	if strings.Contains(hb.ResultSoFar, staleText) {
		t.Fatalf("handback result_so_far contains stale seeded text %q — stale content must never travel", staleText)
	}
}
