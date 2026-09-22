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
		ParentDurableKey: parent, WorkspaceID: "ws", AgentID: "agent-1", ParentAgentID: "agent-1",
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
	var got []string
	for _, event := range h.deliverer.snapshot() {
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

func TestBoot_RepairsHalfWrittenCompletion(t *testing.T) {
	for _, tc := range []struct {
		name          string
		inboxFirst    bool
		terminalFirst bool
	}{
		{name: "inbox first", inboxFirst: true},
		{name: "terminal first", terminalFirst: true},
		{name: "both written before wake", inboxFirst: true, terminalFirst: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newBootRecoveryHarness(t)
			parent := h.rootSession(t)
			child := h.newSession(t, session.SessionTypeDelegate, parent)
			state := session.LifecycleRunning
			if tc.terminalFirst {
				state = session.LifecycleCompleted
				if err := h.sessions.AppendTranscriptStrict(child, session.TranscriptEntry{Role: "assistant", Content: "finished"}); err != nil {
					t.Fatalf("append child result: %v", err)
				}
			}
			h.persist(t, h.steeredRecord(child, parent, state))
			finalID := child + ":1:final"
			if tc.inboxFirst {
				if _, err := h.inbox.Append(parent, bootHandback(t, child, parent, finalID)); err != nil {
					t.Fatalf("Append final: %v", err)
				}
			}

			if err := h.recovery().Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			rec, _ := h.lifecycle.Load(child)
			if rec.State != session.LifecycleCompleted {
				t.Fatalf("state = %q, want completed", rec.State)
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
			if count != 1 || len(h.deliverer.snapshot()) != 1 {
				t.Fatalf("final entries = %d, wakes = %d", count, len(h.deliverer.snapshot()))
			}
		})
	}
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
