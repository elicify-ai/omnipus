package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Frozen ADR-20260928 D6/D6b: a stopped child cuts the completion frontier;
// "On this completed path open_questions is empty." ADR-20261004 decision 6
// / C2 deletes the special question pause: "A helper question to its parent
// is an ordinary message and does not park the helper." This fixture therefore
// uses a real admitted parent/child and owner-landed Stop, never a park/relay
// ledger or expiry. Every original behavioural assertion is retained: the
// persisted final has a non-nil empty open_questions array, the exact genuine
// answer and no stale answer; the ordinary question remains in the parent's
// inbox unacknowledged with its exact id, sender, source session and text.
// The separate verified D6 stop notice is explicitly taken during setup.
func TestCompletion_CompletedHandbackWithStoppedChildQuestion_CarriesNoOpenQuestions(t *testing.T) {
	al, cleanup := newSteerALWithProvider(t, &depthEchoProvider{})
	t.Cleanup(cleanup)
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-d6b-completed")
	parent := g1LaunchQueuedChild(t, al, rootID, "call-d6b-parent")
	stoppedChild := g1AdmitCompletionChild(t, al, parent.SessionID, "call-d6b-worker")
	parent = g1AdmitWaitingParent(t, al, parent)

	// ADR-20261004 locked decision 6 / C2: "A helper question to its parent
	// is an ordinary message and does not park the helper." Seed that exact
	// ordinary message after the parent's initial real turn has retired;
	// there is no pending owner question, relay record, pause or expiry.
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

	// Land the child's real stop through the execution owner. The frontier
	// still cuts on the resulting stopped state, not a fabricated snapshot.
	stoppedChild = g1StopThroughOwner(t, al, stoppedChild)
	// D6's notice is now implemented: prove the owner's real landing produced
	// it, and model the parent taking ONLY that notice. The ordinary question
	// remains untouched, so every original unacked-question assertion below
	// remains exactly as strong. No fabricated stop or silent notice loss.
	noticeID, _ := assertU1StoppedChildNotice(t, al, parent.SessionID, stoppedChild, string(session.StopCauseStop), "operator")
	if ackErr := al.GetMessageInboxStore().Ack(parent.SessionID, []string{noticeID}); ackErr != nil {
		t.Fatalf("SETUP parent takes the verified stop notice only: %v", ackErr)
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
	// Emptiness alone does not certify the wire shape: the contract makes
	// open_questions a REQUIRED, NON-NULLABLE array
	// (contracts/components/schemas/SessionMessageHandback.yaml — listed in
	// `required`, `type: array`, no `nullable`, unlike parent_session_id),
	// and the generated field carries json:"open_questions" without
	// omitempty (pkg/api/generated). nil marshals `null` and would fail the
	// SPA's required z.array(z.string()) parse. This decode came through the
	// real persisted round-trip — FromSessionMessageHandback and the inbox
	// store both marshal to JSON bytes, and Drain reads entries unmarshaled
	// back from those bytes — where nil and empty stay distinguishable, so
	// asserting non-nil here pins the exact `[]` wire shape.
	if hb.OpenQuestions == nil {
		t.Fatalf("completed hand-back open_questions = nil, want non-nil empty [] — the contract requires a " +
			"non-null array (SessionMessageHandback.yaml: required, type array, no nullable); nil marshals " +
			"null and would fail the generated Zod parse, and emptiness alone does not certify the wire shape")
	}

	// Genuine final result, no stale content.
	if hb.ResultSoFar != parentAnswer {
		t.Fatalf("handback result_so_far = %q, want %q — the parent's genuine final answer", hb.ResultSoFar, parentAnswer)
	}
	if strings.Contains(hb.ResultSoFar, staleText) {
		t.Fatalf("handback result_so_far contains stale seeded text %q — stale content must never travel", staleText)
	}

	// Retention of an ordinary upward question: the completed path must
	// leave that message exactly where it was. Drain returns only UNACKED
	// entries and writes no acks (pkg/session contract), so a hit here is
	// itself the unacked proof. Re-drain AFTER completeSteeredTurn and the
	// decoded hand-back, and assert the EXACT seeded question — same
	// message id, same sender, same source session, same text — not merely
	// the same count: a regression that acks or drains-and-discards the
	// parent's inbox on the completed path would silently drop a user's
	// outstanding question, and the child is already stopped, so nobody
	// would ever answer it.
	postMsgs, _, _, postDrainErr := al.GetMessageInboxStore().Drain(parent.SessionID, stoppedChild.SessionID, "", 10)
	if postDrainErr != nil {
		t.Fatalf("Drain(parent) after completion: %v", postDrainErr)
	}
	if len(postMsgs) != 1 {
		t.Fatalf("parent inbox messages after completion = %d, want 1 — the relayed question must survive "+
			"the completed path parked and unacked at its D1 home (the parent's inbox)", len(postMsgs))
	}
	postKind, postKindErr := postMsgs[0].Discriminator()
	if postKindErr != nil || postKind != "question" {
		t.Fatalf("parent inbox message kind after completion = %q (err %v), want \"question\"", postKind, postKindErr)
	}
	postQ, postQErr := postMsgs[0].AsSessionMessageQuestion()
	if postQErr != nil {
		t.Fatalf("AsSessionMessageQuestion(post-completion): %v", postQErr)
	}
	if postQ.MessageId != "q-d6b-completed-1" {
		t.Fatalf("post-completion question message_id = %q, want %q — the seeded question's id must survive unchanged", postQ.MessageId, "q-d6b-completed-1")
	}
	if postQ.SenderIdentity != stoppedChild.AgentID {
		t.Fatalf("post-completion question sender_identity = %q, want %q — the sender must survive unchanged", postQ.SenderIdentity, stoppedChild.AgentID)
	}
	if postQ.SessionId != stoppedChild.SessionID {
		t.Fatalf("post-completion question session_id = %q, want %q — the source session must survive unchanged", postQ.SessionId, stoppedChild.SessionID)
	}
	if postQ.Text != questionText {
		t.Fatalf("post-completion question text = %q, want %q — the payload must survive unchanged", postQ.Text, questionText)
	}
}
