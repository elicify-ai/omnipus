package agent

// Q2 consumption-boundary pack. Oracles: dispatch requirements 1–4;
// ADR-091 D2/D3 and landing-order I-8; ADR-20260928 D4 Delivery, preserved
// by ADR-20261004 C1/C2/C4. GREEN and mutations are deferred to a fresh CHECK
// instance: the author must not audit its own RED suite.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

const q2ConsumerTask = "Q2 preserve this original launch instruction"

// not-wire-format: private test fixture, never persisted or transported.
type q2ConsumerFixture struct {
	al       *AgentLoop
	store    *session.UnifiedStore
	childID  string
	parentID string
	baseline []session.TranscriptEntry
}

func newQ2ConsumerFixture(t *testing.T) q2ConsumerFixture {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	al.SetSteeringMode(SteeringAll)
	parentID := newTestSteeringSession(t, al, "ws-q2-consumer")
	child, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID, TargetAgentID: testDefaultAgentID,
		Task:   q2ConsumerTask,
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "q2-consumer-launch"},
	})
	if err != nil {
		t.Fatalf("SETUP real child Launch: %v", err)
	}
	store := al.ResolveSessionStore(child.SessionID)
	if store == nil {
		t.Fatal("SETUP launcher-created child has no owning transcript store")
	}
	classifier := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), store)
	class, err := classifier.Classify(context.Background(), child.SessionID)
	if err != nil || class != steer.ClassSteered {
		t.Fatalf("SETUP genuine child class = %q/%v, want steered/nil", class, err)
	}
	baseline := q2ConsumerReadDurable(t, store, child.SessionID)
	if len(baseline) != 1 || baseline[0].Role != "user" || baseline[0].Content != q2ConsumerTask || baseline[0].AgentID != testDefaultAgentID {
		t.Fatalf("SETUP real launch history = %+v, want exactly its original user instruction", baseline)
	}
	return q2ConsumerFixture{al: al, store: store, childID: child.SessionID, parentID: parentID, baseline: baseline}
}

func q2ConsumerEnqueueWake(t *testing.T, al *AgentLoop, sessionID, messageID, text string) steeringQueueItem {
	t.Helper()
	message := providers.Message{Role: "user", Content: text}
	if err := al.EnqueueSteeringWake(sessionID, testDefaultAgentID, sessionID, messageID, message); err != nil {
		t.Fatalf("SETUP real EnqueueSteeringWake(%q): %v", messageID, err)
	}
	// Expected shape derives from caller input, not the dequeued output.
	return steeringQueueItem{message: message, wake: &steeringWake{
		messageID: messageID, transcriptSessionID: sessionID, agentID: testDefaultAgentID,
	}}
}

func q2ConsumerDequeue(t *testing.T, al *AgentLoop, scope string, want []steeringQueueItem) []steeringQueueItem {
	t.Helper()
	actualScope, items := al.steering.dequeueItemsScope(scope)
	if actualScope != scope || !reflect.DeepEqual(items, want) {
		t.Fatalf("real dequeued scope/items = %q/%+v, want %q/%+v", actualScope, items, scope, want)
	}
	return items
}

// not-wire-format: private observation of the real consumer's return values.
type q2ConsumerResult struct {
	messages []providers.Message
	ids      []string
	items    []steeringQueueItem
	err      error
}

func q2ConsumerConsume(al *AgentLoop, scope string, items []steeringQueueItem) q2ConsumerResult {
	messages, ids, consumed, err := al.consumeDequeuedSteeringResult(scope, items)
	return q2ConsumerResult{messages: messages, ids: ids, items: consumed, err: err}
}

func q2ConsumerRequireReturned(t *testing.T, result q2ConsumerResult, want []steeringQueueItem) {
	t.Helper()
	messages := make([]providers.Message, 0, len(want))
	ids := make([]string, 0, len(want))
	for _, item := range want {
		messages = append(messages, item.message)
		ids = append(ids, item.correlationID)
	}
	if !reflect.DeepEqual(result.messages, messages) || !reflect.DeepEqual(result.ids, ids) {
		t.Errorf("returned messages/correlations = %+v/%q, want exact consumed prefix %+v/%q", result.messages, result.ids, messages, ids)
	}
	if len(result.items) != len(want) || (len(want) != 0 && !reflect.DeepEqual(result.items, want)) {
		t.Errorf("returned consumed items = %+v, want exact prefix %+v (wake identity retained)", result.items, want)
	}
}

// Read both physical JSONL and a reopened real store. A malformed line must
// fail, not disappear behind ReadTranscript's malformed-line filtering.
func q2ConsumerReadDurable(t *testing.T, store *session.UnifiedStore, sessionID string) []session.TranscriptEntry {
	t.Helper()
	path := filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read physical transcript: %v", err)
	}
	var physical []session.TranscriptEntry
	for index, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry session.TranscriptEntry
		if uerr := json.Unmarshal(line, &entry); uerr != nil {
			t.Fatalf("physical transcript line %d is malformed; cannot trust filtered counts: %v", index+1, uerr)
		}
		physical = append(physical, entry)
	}
	reopened, err := session.NewUnifiedStore(store.BaseDir())
	if err != nil {
		t.Fatalf("reopen actual transcript store: %v", err)
	}
	defer func() {
		if cerr := reopened.Close(); cerr != nil {
			t.Errorf("close reopened transcript store: %v", cerr)
		}
	}()
	visible, err := reopened.ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("reopened ReadTranscript: %v", err)
	}
	if len(visible) != len(physical) || (len(physical) != 0 && !reflect.DeepEqual(visible, physical)) {
		t.Fatalf("reopened transcript differs from physical durable entries: visible=%+v physical=%+v", visible, physical)
	}
	return physical
}

func q2ConsumerRequireTail(t *testing.T, fixture q2ConsumerFixture, want []session.TranscriptEntry) {
	t.Helper()
	entries := q2ConsumerReadDurable(t, fixture.store, fixture.childID)
	if len(entries) != len(fixture.baseline)+len(want) {
		t.Errorf("durable transcript entry count = %d, want baseline %d + exact tail %d: %+v", len(entries), len(fixture.baseline), len(want), entries)
		return
	}
	if !reflect.DeepEqual(entries[:len(fixture.baseline)], fixture.baseline) {
		t.Error("consumer changed previously persisted launch/human history")
	}
	for index, expected := range want {
		actual := entries[len(fixture.baseline)+index]
		if actual.Timestamp.IsZero() {
			t.Errorf("tail %d has no durable timestamp", index)
		}
		// Timestamp is store-assigned. All other fields must equal the exact
		// specified input/marker shape, including no trusted-write marker.
		expected.Timestamp = actual.Timestamp
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("durable tail %d = %+v, want exact %+v", index, actual, expected)
		}
	}
}

func q2ConsumerInstruction(messageID, text string) session.TranscriptEntry {
	return session.TranscriptEntry{ID: "instruction-" + messageID, Role: "user", Content: text, AgentID: testDefaultAgentID}
}

func q2ConsumerMarker(messageID string) session.TranscriptEntry {
	return session.TranscriptEntry{ID: "consumed-" + messageID, Type: session.EntryTypeSystem, Role: "system", Content: "consumed " + messageID, AgentID: testDefaultAgentID}
}

func q2ConsumerRequireQueued(t *testing.T, al *AgentLoop, scope string, want []steeringQueueItem) {
	t.Helper()
	al.steering.mu.Lock()
	actual := append([]steeringQueueItem(nil), al.steering.queues[scope]...)
	al.steering.mu.Unlock()
	if !reflect.DeepEqual(actual, want) {
		t.Errorf("restored queue = %+v, want exact suffix-before-arrival %+v", actual, want)
	}
}
