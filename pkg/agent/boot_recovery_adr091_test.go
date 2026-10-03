package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type bootRecordingDeliverer struct {
	mu        sync.Mutex
	inbox     *session.MessageInboxStore
	lifecycle *session.LifecycleStore
	events    []steer.UpwardEvent
}

func (d *bootRecordingDeliverer) Deliver(_ context.Context, event steer.UpwardEvent) (steer.Delivery, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, err := d.lifecycle.Load(event.ChildSessionID)
	if err != nil {
		return steer.Delivery{}, err
	}
	result, err := d.inbox.Append(rec.SteeredBy.SteeringSessionID, event.Message)
	if err != nil {
		return steer.Delivery{}, err
	}
	d.events = append(d.events, event)
	return steer.Delivery{MessageID: result.MessageID, Outcome: steer.DeliveryWoke}, nil
}

func (d *bootRecordingDeliverer) snapshot() []steer.UpwardEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]steer.UpwardEvent(nil), d.events...)
}

type bootRecoveryHarness struct {
	lifecycle *session.LifecycleStore
	sessions  *session.UnifiedStore
	inbox     *session.MessageInboxStore
	deliverer *bootRecordingDeliverer
	notices   []string
}

func newBootRecoveryHarness(t *testing.T) *bootRecoveryHarness {
	t.Helper()
	root := t.TempDir()
	sessions, err := session.NewUnifiedStore(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sessions.Close(); closeErr != nil {
			t.Errorf("UnifiedStore.Close: %v", closeErr)
		}
	})
	h := &bootRecoveryHarness{
		lifecycle: session.NewLifecycleStore(filepath.Join(root, "lifecycle")),
		sessions:  sessions,
		inbox:     session.NewMessageInboxStore(filepath.Join(root, "inbox")),
	}
	h.deliverer = &bootRecordingDeliverer{inbox: h.inbox, lifecycle: h.lifecycle}
	return h
}

func (h *bootRecoveryHarness) newSession(t *testing.T, typ session.UnifiedSessionType, parentID string) string {
	t.Helper()
	meta, err := h.sessions.NewSession(typ, "web", "agent-1")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if parentID != "" {
		if err := h.sessions.SetMeta(meta.ID, session.MetaPatch{ParentSessionID: &parentID}); err != nil {
			t.Fatalf("SetMeta parent: %v", err)
		}
	}
	return meta.ID
}

// rootSession creates a chat session together with its ordinary_root record,
// as the launcher does for every session that steers a child (landing order
// I-1, founder decision round 9). A child whose steering session has no record
// is an invalid edge, so every fixture tree starts from a recorded root.
func (h *bootRecoveryHarness) rootSession(t *testing.T) string {
	t.Helper()
	id := h.newSession(t, session.SessionTypeChat, "")
	h.persist(t, &session.LifecycleRecord{
		SessionID: id, Generation: 1, State: session.LifecycleRunning,
		Origin: &session.Origin{Kind: session.OriginKindChat}, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: "ws", AgentID: "agent-1",
	})
	return id
}

func (h *bootRecoveryHarness) persist(t *testing.T, rec *session.LifecycleRecord) {
	t.Helper()
	if err := h.lifecycle.Persist(rec); err != nil {
		t.Fatalf("Persist(%s): %v", rec.SessionID, err)
	}
}

func (h *bootRecoveryHarness) steeredRecord(id, parent string, state session.LifecycleState) *session.LifecycleRecord {
	return &session.LifecycleRecord{
		SessionID: id, Generation: 1, State: state,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate},
		SteeredBy:      &session.SteeredBy{SteeringSessionID: parent, RootSessionID: parent},
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: parent,
		WorkspaceID: "ws", AgentID: "agent-1", ParentAgentID: "agent-1",
	}
}

func (h *bootRecoveryHarness) recovery() *SteerBootRecovery {
	return &SteerBootRecovery{
		Lifecycle:      h.lifecycle,
		Sessions:       h.sessions,
		Inbox:          h.inbox,
		Classifier:     NewSteerRecordClassifier(h.lifecycle, h.sessions),
		Deliverer:      h.deliverer,
		OperatorNotice: func(message string) { h.notices = append(h.notices, message) },
	}
}

type bootMessageEnvelope struct {
	MessageID string `json:"message_id"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Fatal     bool   `json:"fatal"`
	Mode      string `json:"mode"`
}

func bootEnvelope(t *testing.T, message generated.SessionMessage) bootMessageEnvelope {
	t.Helper()
	raw, err := message.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var envelope bootMessageEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("Unmarshal envelope: %v", err)
	}
	return envelope
}

func bootHandback(t *testing.T, child, parent, id string) generated.SessionMessage {
	t.Helper()
	gen := 1
	var message generated.SessionMessage
	err := message.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId: id, SessionId: child, ParentSessionId: &parent, CreatedAt: time.Now(),
		Depth: 1, Direction: "child_to_parent", Generation: &gen, Kind: "handback",
		Mode: "final", ResultSoFar: "finished", SenderIdentity: "agent-1",
		Artifacts: []string{}, OpenQuestions: []string{}, UntrustedOrigin: true,
	})
	if err != nil {
		t.Fatalf("encode handback: %v", err)
	}
	return message
}

func bootProgress(t *testing.T, child, parent, id string) generated.SessionMessage {
	t.Helper()
	gen := 1
	var message generated.SessionMessage
	err := message.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId: id, SessionId: child, ParentSessionId: &parent, CreatedAt: time.Now(),
		Depth: 1, Direction: "child_to_parent", Generation: &gen, Kind: "progress",
		Text: "working", SenderIdentity: "agent-1", UntrustedOrigin: true,
	})
	if err != nil {
		t.Fatalf("encode progress: %v", err)
	}
	return message
}

func bootQuestion(t *testing.T, child, parent, id string) generated.SessionMessage {
	t.Helper()
	gen := 1
	var message generated.SessionMessage
	err := message.FromSessionMessageQuestion(generated.SessionMessageQuestion{
		MessageId: id, SessionId: child, ParentSessionId: &parent, CreatedAt: time.Now(),
		Depth: 1, Direction: "child_to_parent", Generation: &gen, Kind: "question",
		CorrelationId: "corr", Text: "choose", Wait: true, SenderIdentity: "agent-1", UntrustedOrigin: true,
	})
	if err != nil {
		t.Fatalf("encode question: %v", err)
	}
	return message
}

func TestBoot_FailureDeliveredUpward(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	h.persist(t, h.steeredRecord(child, parent, session.LifecycleRunning))

	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	rec, err := h.lifecycle.Load(child)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.State != session.LifecycleFailed || rec.FailedReason != failedReasonInterrupted {
		t.Fatalf("record = state %q reason %q", rec.State, rec.FailedReason)
	}
	events := h.deliverer.snapshot()
	if len(events) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(events))
	}
	envelope := bootEnvelope(t, events[0].Message)
	if envelope.Kind != "error" || !envelope.Fatal || !strings.HasPrefix(envelope.Text, "interrupted:") {
		t.Fatalf("upward message = %+v", envelope)
	}
}

func TestBoot_StoppedStaysStoppedUnlessNewerInstruction(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleRunning)
	rec.Stop = &session.Stop{Generation: 1, At: time.Now(), By: session.Principal{Kind: session.PrincipalKindHuman, ID: "owner"}}
	h.persist(t, rec)

	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	after, _ := h.lifecycle.Load(child)
	if after.State != session.LifecycleRunning || after.Stop == nil || after.Stop.Generation != 1 {
		t.Fatalf("stopped record changed: %+v", after)
	}
	if len(h.deliverer.snapshot()) != 0 {
		t.Fatal("stamped session was woken or reported as interrupted")
	}
}

func TestBoot_RenudgesUnconsumedEntriesOnce_EligibleOnly(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleNeedsInput)
	rec.NeedsInput = &session.NeedsInput{CorrelationID: "corr", TTLDeadline: time.Now().Add(time.Hour)}
	h.persist(t, rec)

	for _, message := range []generated.SessionMessage{
		bootHandback(t, child, parent, "handback-open"),
		bootProgress(t, child, parent, "progress-open"),
		bootQuestion(t, child, parent, "question-open"),
		bootHandback(t, child, parent, "handback-consumed"),
	} {
		if _, err := h.inbox.Append(parent, message); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := h.sessions.AppendTranscriptStrict(parent, session.TranscriptEntry{
		ID: "consumed-handback", Type: session.EntryTypeSystem, Role: "system", Content: "consumed handback-consumed",
	}); err != nil {
		t.Fatalf("AppendTranscriptStrict: %v", err)
	}

	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	snapshot := h.deliverer.snapshot()
	got := make([]string, 0, len(snapshot))
	for _, event := range snapshot {
		got = append(got, bootEnvelope(t, event.Message).MessageID)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"handback-open", "question-open"}) {
		t.Fatalf("re-woken = %v", got)
	}
	remaining, _, _, err := h.inbox.Drain(parent, child, "", 20)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	for _, message := range remaining {
		if bootEnvelope(t, message).MessageID == "handback-consumed" {
			t.Fatal("consumed entry was not acknowledged")
		}
	}
}

// TestBoot_RepairsHalfWrittenCompletion — W3 RED reconcile (fresh-context
// qa-lead), ADR-20260928 sub-agent control plane (frozen asset cd20cf8b).
// The pre-migration oracle demanded that boot promote a steered record to
// completed from an inbox final alone in every crash cut — the inbox-first
// repair D2 retired: "finishFromFinal must not turn a non-terminal record
// into done/failed merely because an inbox final exists: require the
// matching lifecycle/outbox commit (otherwise report the inconsistent record
// visibly and do not consume the id). Parent consumption/ack never acts as
// the lifecycle winner." The three crash cuts are migrated INDIVIDUALLY:
//
//  1. inbox final WITHOUT a committed outcome — reported visibly, never
//     promoted, the id never consumed (no re-wake as a final answer, no
//     acknowledgement), and the entry left recoverable exactly once for a
//     correct boot repair (D2/D8.5).
//  2. a COMMITTED terminal/outbox cut off before the inbox append — the
//     legitimate positive: the exact committed payload publishes once
//     through the deliverer with its durable delivery facts (D2 "Publish
//     only a committed outbox", D8.1). Built through the same ONE real
//     LifecycleStore.Mutate the production commit boundary performs
//     (commitAllGenDoneFinal) — never a hand-seeded terminal record, which
//     in production cannot exist without its protected commit tuple.
//  3. a committed outbox whose parent-inbox append already landed — the
//     existing id retries the downstream wake without a second final (D2).
//
// Unit boundary: the same real stores and SteerBootRecovery.Run the boot
// suite always uses; the recording deliverer appends to the REAL parent
// inbox. Specification source: the frozen ADR text quoted above — no
// expected value below is derived from observed behaviour of the code under
// test. Known gaps (deliberate): no FramesPersisted fact is asserted (the
// pinned tree has no frames writer — audited and reported separately, no
// fake boolean fact); the D8.3 stopped(restart) landing of the uncommitted
// run is W3b, so case 1 pins non-promotion, not the landing state;
// CHECK mutations (promote-on-inbox-presence, dropped phantom notice,
// byte-divergent publication, duplicate append on the already-appended id,
// skipped UpdateFinalDelivery facts) are deferred to CHECK.
func TestBoot_RepairsHalfWrittenCompletion(t *testing.T) {
	t.Run("inbox final without commit is reported, never promoted or consumed", func(t *testing.T) {
		h := newBootRecoveryHarness(t)
		parent := h.rootSession(t)
		child := h.newSession(t, session.SessionTypeDelegate, parent)
		h.persist(t, h.steeredRecord(child, parent, session.LifecycleRunning))
		finalID := child + ":1:final"
		if _, err := h.inbox.Append(parent, bootHandback(t, child, parent, finalID)); err != nil {
			t.Fatalf("Append final: %v", err)
		}

		if err := h.recovery().Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}

		rec, err := h.lifecycle.Load(child)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if rec.State == session.LifecycleCompleted {
			t.Fatal("an inbox final with NO committed lifecycle/outbox outcome promoted the record to completed — D2: the promotion requires the matching commit; an inbox final alone is never a lifecycle winner")
		}
		if rec.FinalDelivery != nil {
			t.Fatalf("a protected commit was minted from inbox presence alone (commit %q) — D2: recovery never mints an outbox commit from a phantom final", rec.FinalDelivery.CommitID)
		}
		if rec.Generation != 1 {
			t.Fatalf("tail generation = %d, want 1 — reconciliation from a phantom final must not mint a newer generation (D8.5)", rec.Generation)
		}
		joined := strings.Join(h.notices, "\n")
		if !strings.Contains(joined, child) || !strings.Contains(joined, finalID) {
			t.Fatalf("the inconsistent record was not reported visibly (notices %q) — D2: report the inconsistent record visibly, naming the session and the unconsumed final %s", joined, finalID)
		}
		for _, event := range h.deliverer.snapshot() {
			envelope := bootEnvelope(t, event.Message)
			if envelope.MessageID == finalID && event.Outcome == steer.OutcomeFinalAnswer {
				t.Fatalf("the phantom final %s was re-woken as a final answer — D2: do not consume the id; the inbox final is never re-published as the completion", finalID)
			}
		}
		entries, err := h.inbox.Entries(parent)
		if err != nil {
			t.Fatalf("Entries(parent): %v", err)
		}
		for _, entry := range entries {
			if entry.Kind == session.InboxEntryAck && slices.Contains(entry.AckedIDs, finalID) {
				t.Fatalf("the phantom final %s was acknowledged — D2: parent consumption/ack never acts as the lifecycle winner, and a consumed id could never be repaired", finalID)
			}
		}
		messages, _, _, err := h.inbox.Drain(parent, child, "", 20)
		if err != nil {
			t.Fatalf("Drain: %v", err)
		}
		count := 0
		for _, message := range messages {
			if bootEnvelope(t, message).MessageID == finalID {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("phantom final entries in the parent inbox = %d, want exactly 1 — the id is not consumed and the record stays recoverable for a correct boot repair (D2)", count)
		}
	})

	t.Run("committed outbox cut before append publishes exactly once at the terminal tail", func(t *testing.T) {
		h := newBootRecoveryHarness(t)
		parent := h.rootSession(t)
		child := h.newSession(t, session.SessionTypeDelegate, parent)
		commitID := "commit-g1-repairs-terminal-first"
		finalID, payload, payloadHash := commitAllGenDoneFinal(t, h, child, parent, commitID,
			"committed, never appended: the crash cut before the parent inbox append")

		if err := h.recovery().Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}

		rec, err := h.lifecycle.Load(child)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if rec.State != session.LifecycleCompleted {
			t.Fatalf("state = %q, want completed — the committed terminal outcome stands; a delivery retry never blanket-fails a committed generation (D2)", rec.State)
		}
		if rec.FinalDelivery == nil || rec.FinalDelivery.CommitID != commitID ||
			rec.FinalDelivery.MessageID != finalID || rec.FinalDelivery.PayloadHash != payloadHash {
			t.Fatalf("the protected commit identity changed: %+v — D2: outcome, final id and payload identity are immutable once committed", rec.FinalDelivery)
		}
		messages := realDelivererInboxMessages(t, h.inbox, parent)
		got, ok := messages[finalID]
		if !ok {
			t.Fatalf("the committed final %s never reached the parent inbox (ids %v) — D2/D8.1: the exact committed message is published to the direct parent", finalID, realDelivererMessageIDs(messages))
		}
		if got != string(payload) {
			t.Fatalf("published final %s bytes diverge from the committed payload — D2: the exact committed upward message is published", finalID)
		}
		if len(messages) != 1 {
			t.Fatalf("parent inbox holds %d message entries (%v), want exactly the one committed final — publish once, the replay id is the duplicate guard (D2)", len(messages), realDelivererMessageIDs(messages))
		}
		wakes := 0
		for _, event := range h.deliverer.snapshot() {
			if bootEnvelope(t, event.Message).MessageID == finalID && event.Outcome == steer.OutcomeFinalAnswer {
				wakes++
			}
		}
		if wakes != 1 {
			t.Fatalf("committed final %s woke the parent %d times, want exactly 1 (D2)", finalID, wakes)
		}
		progress, revision, retired, err := h.lifecycle.FinalDeliveryState(child, 1, commitID)
		if err != nil {
			t.Fatalf("FinalDeliveryState(G): %v", err)
		}
		if retired || revision < 1 || !progress.InboxAppended || !progress.WakeRecorded {
			t.Fatalf("delivery facts after boot = %+v revision %d retired %v — the pass must record the durable inbox append and wake through UpdateFinalDelivery (D2)", progress, revision, retired)
		}
	})

	t.Run("committed outbox with the inbox append already durable does not duplicate", func(t *testing.T) {
		h := newBootRecoveryHarness(t)
		parent := h.rootSession(t)
		child := h.newSession(t, session.SessionTypeDelegate, parent)
		commitID := "commit-g1-repairs-both-written"
		finalID, payload, payloadHash := commitAllGenDoneFinal(t, h, child, parent, commitID,
			"committed and already appended: the crash cut before the wake")
		var committed generated.SessionMessage
		if err := committed.UnmarshalJSON(payload); err != nil {
			t.Fatalf("decode committed payload: %v", err)
		}
		if _, err := h.inbox.Append(parent, committed); err != nil {
			t.Fatalf("Append committed final: %v", err)
		}
		if seeded := realDelivererInboxMessages(t, h.inbox, parent); len(seeded) != 1 {
			t.Fatalf("fixture control: parent inbox holds %d entries before boot, want exactly the appended committed final", len(seeded))
		}

		if err := h.recovery().Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}

		rec, err := h.lifecycle.Load(child)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if rec.State != session.LifecycleCompleted {
			t.Fatalf("state = %q, want completed — the committed terminal outcome stands (D2)", rec.State)
		}
		if rec.FinalDelivery == nil || rec.FinalDelivery.CommitID != commitID ||
			rec.FinalDelivery.MessageID != finalID || rec.FinalDelivery.PayloadHash != payloadHash {
			t.Fatalf("the protected commit identity changed: %+v — D2: the commit identity is immutable once committed", rec.FinalDelivery)
		}
		messages := realDelivererInboxMessages(t, h.inbox, parent)
		got, ok := messages[finalID]
		if !ok {
			t.Fatalf("the committed final %s vanished from the parent inbox (ids %v) — the already-appended id stays", finalID, realDelivererMessageIDs(messages))
		}
		if got != string(payload) {
			t.Fatalf("stored final %s bytes diverge from the committed payload — D2: an existing id with the same committed identity is retried, never replaced", finalID)
		}
		if len(messages) != 1 {
			t.Fatalf("parent inbox holds %d message entries (%v), want exactly 1 — an existing id with the same committed payload is never appended a second time (D2)", len(messages), realDelivererMessageIDs(messages))
		}
		wakes := 0
		for _, event := range h.deliverer.snapshot() {
			if bootEnvelope(t, event.Message).MessageID == finalID && event.Outcome == steer.OutcomeFinalAnswer {
				wakes++
			}
		}
		if wakes != 1 {
			t.Fatalf("committed final %s woke the parent %d times, want exactly 1 — the downstream wake retries once for the already-appended id (D2)", finalID, wakes)
		}
		progress, revision, retired, err := h.lifecycle.FinalDeliveryState(child, 1, commitID)
		if err != nil {
			t.Fatalf("FinalDeliveryState(G): %v", err)
		}
		if retired || revision < 1 || !progress.InboxAppended || !progress.WakeRecorded {
			t.Fatalf("delivery facts after boot = %+v revision %d retired %v — the durable receipts must be recorded through UpdateFinalDelivery (D2)", progress, revision, retired)
		}
	})
}

func TestBoot_ClassifiesAllClasses(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.newSession(t, session.SessionTypeChat, "")
	h.persist(t, &session.LifecycleRecord{
		SessionID: parent, Generation: 1, State: session.LifecycleRunning,
		Origin: &session.Origin{Kind: session.OriginKindChat}, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: "ws", AgentID: "agent-1", ParentAgentID: "agent-1",
	})

	steered := h.newSession(t, session.SessionTypeDelegate, parent)
	steeredRec := h.steeredRecord(steered, parent, session.LifecycleNeedsInput)
	steeredRec.NeedsInput = &session.NeedsInput{CorrelationID: "corr", TTLDeadline: time.Now().Add(time.Hour)}
	h.persist(t, steeredRec)

	legacy := h.newSession(t, session.SessionTypeDelegate, "")
	h.persist(t, &session.LifecycleRecord{
		SessionID: legacy, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws", AgentID: "agent-1", ParentAgentID: "agent-1",
	})

	damaged := h.newSession(t, session.SessionTypeDelegate, parent)
	invalid := h.newSession(t, session.SessionTypeDelegate, parent)
	invalidRec := h.steeredRecord(invalid, "different-parent", session.LifecycleRunning)
	h.persist(t, invalidRec)

	if err := os.MkdirAll(h.lifecycle.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.lifecycle.Dir(), "unreadable.jsonl"), []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ordinaryAfter, _ := h.lifecycle.Load(parent)
	steeredAfter, _ := h.lifecycle.Load(steered)
	legacyAfter, _ := h.lifecycle.Load(legacy)
	invalidAfter, _ := h.lifecycle.Load(invalid)
	if ordinaryAfter.State != session.LifecycleRunning {
		t.Fatalf("ordinary root state = %q", ordinaryAfter.State)
	}
	if steeredAfter.State != session.LifecycleNeedsInput {
		t.Fatalf("steered parked state = %q", steeredAfter.State)
	}
	if legacyAfter.State != session.LifecycleFailed || legacyAfter.FailedReason != failedReasonPreADR091NotResumable {
		t.Fatalf("legacy consequence = state %q reason %q", legacyAfter.State, legacyAfter.FailedReason)
	}
	if invalidAfter.State != session.LifecycleRunning {
		t.Fatalf("invalid edge was resumed or rewritten: %q", invalidAfter.State)
	}
	if h.lifecycle.Exists(damaged) {
		t.Fatal("damaged metadata-only child unexpectedly gained a lifecycle record")
	}
	joined := strings.Join(h.notices, "\n")
	for _, expected := range []string{legacy, damaged, invalid, "unreadable"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("operator notices %q do not name %q", joined, expected)
		}
	}
}

// TestBoot_TimeoutStoppedRecoversAsTimedOut pins the U1 stopped-state
// consolidation regression (PR #1139 gate finding): a steered session whose
// lifetime budget expired — stop_note.cause=timeout, the shape
// steer_completion.go::completeSteeredTurn lands live — and that never
// stored its final before the gateway restart must reach its parent as
// steer.OutcomeTimedOut with a "timeout:" text, exactly the pre-consolidation
// LifecycleTimedOut case of terminalErrorBootMessage did. The merged
// "stopped" state deliberately erased the cancelled/timed-out state
// distinction (founder ruling: timed out = stopped); the RETAINED stop_note
// cause is what keeps the two apart for the parent.
func TestBoot_TimeoutStoppedRecoversAsTimedOut(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleStopped)
	rec.StopNote = &session.StopNote{
		At: time.Now().UTC(), By: session.StopActorSystem,
		Seq: uint64(rec.Generation), Cause: session.StopCauseTimeout,
	}
	h.persist(t, rec)

	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := h.deliverer.snapshot()
	if len(events) != 1 {
		t.Fatalf("deliveries = %d, want 1 (the terminal notice)", len(events))
	}
	if events[0].Outcome != steer.OutcomeTimedOut {
		t.Fatalf("boot-recovered timeout stop outcome = %q, want %q", events[0].Outcome, steer.OutcomeTimedOut)
	}
	envelope := bootEnvelope(t, events[0].Message)
	if envelope.Kind != "error" || !envelope.Fatal || !strings.HasPrefix(envelope.Text, "timeout:") {
		t.Fatalf("upward message = %+v, want a fatal error with a timeout: prefix", envelope)
	}
	after, err := h.lifecycle.Load(child)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The failed reason follows the boot vocabulary the stored text itself
	// derives on a later restart (failedReasonFromBootText): "timeout".
	if after.State != session.LifecycleFailed || after.FailedReason != "timeout" {
		t.Fatalf("record = state %q reason %q, want failed/timeout", after.State, after.FailedReason)
	}
}

// TestBoot_StoppedNonTimeoutStillInterrupted keeps the timeout reroute
// narrow: a stop-caused stop (a human Stop, current generation) and a STALE
// timeout note (Seq from an older generation a Revive kept) both recover as
// a plain interruption, exactly as before the fix.
func TestBoot_StoppedNonTimeoutStillInterrupted(t *testing.T) {
	for name, note := range map[string]*session.StopNote{
		"stop_cause_current_gen": {
			At: time.Now().UTC(), By: session.StopActorSystem,
			Seq: 1, Cause: session.StopCauseStop,
		},
		"stale_timeout_note": {
			At: time.Now().UTC(), By: session.StopActorSystem,
			Seq: 0, Cause: session.StopCauseTimeout, // Seq 0 predates generation 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newBootRecoveryHarness(t)
			parent := h.rootSession(t)
			child := h.newSession(t, session.SessionTypeDelegate, parent)
			rec := h.steeredRecord(child, parent, session.LifecycleStopped)
			rec.StopNote = note
			h.persist(t, rec)

			if err := h.recovery().Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			events := h.deliverer.snapshot()
			if len(events) != 1 {
				t.Fatalf("deliveries = %d, want 1", len(events))
			}
			if events[0].Outcome != steer.OutcomeInterrupted {
				t.Fatalf("outcome = %q, want %q", events[0].Outcome, steer.OutcomeInterrupted)
			}
			envelope := bootEnvelope(t, events[0].Message)
			if envelope.Kind != "error" || !envelope.Fatal || !strings.HasPrefix(envelope.Text, "interrupted:") {
				t.Fatalf("upward message = %+v, want a fatal error with an interrupted: prefix", envelope)
			}
			after, err := h.lifecycle.Load(child)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if after.State != session.LifecycleFailed || after.FailedReason != failedReasonInterrupted {
				t.Fatalf("record = state %q reason %q, want failed/%q", after.State, after.FailedReason, failedReasonInterrupted)
			}
		})
	}
}

func TestIndexReport_SurfacedToOperator(t *testing.T) {
	h := newBootRecoveryHarness(t)
	if err := os.MkdirAll(h.lifecycle.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.lifecycle.Dir(), "broken.jsonl"), []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	joined := strings.Join(h.notices, "\n")
	if !strings.Contains(joined, "broken") || !strings.Contains(joined, "unreadable") {
		t.Fatalf("operator notices = %q", joined)
	}
}
