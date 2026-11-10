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
	"strings"
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
func q2ConsumerReadDurable(t *testing.T, store *session.UnifiedStore, sessionID string, wantRunningSuccess ...session.ToolCallID) []session.TranscriptEntry {
	t.Helper()
	path := filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read physical transcript: %v", err)
	}
	day, dayErr := os.ReadFile(filepath.Join(store.BaseDir(), sessionID, "transcript.day"))
	if len(data) != 0 && dayErr != nil {
		t.Fatalf("read physical transcript partition identity: %v", dayErr)
	}
	var physical []session.TranscriptEntry
	sources := make(map[session.ArchiveAddress]int)
	settled := make(map[q2ConsumerSettleKey]bool)
	var offset int64
	for index, line := range bytes.Split(data, []byte{'\n'}) {
		addr := session.ArchiveAddress{PartitionKey: strings.TrimSpace(string(day)), ByteOffset: offset}
		offset += int64(len(line) + 1)
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record session.ArchiveRecord
		if uerr := json.Unmarshal(line, &record); uerr != nil {
			t.Fatalf("physical transcript line %d is malformed; cannot trust filtered counts: %v", index+1, uerr)
		}
		addr.EntryID = record.ID
		// C2 permits a later settlement to change an earlier public row. Derive
		// that view independently from the physical records, not ReadTranscript.
		// Unknown effects fail loudly; they are never filtered into a false pass.
		if record.Type == session.EntryTypeToolCallEffect {
			q2ConsumerApplySettle(t, physical, sources, settled, record)
			continue
		}
		if record.ViewMembership != session.ViewMembershipChat {
			continue
		}
		sources[addr] = len(physical)
		physical = append(physical, record.TranscriptEntry)
	}
	for _, id := range wantRunningSuccess {
		matches := 0
		for addr, index := range sources {
			for _, call := range physical[index].ToolCalls {
				if call.ID != id {
					continue
				}
				matches++
				transitions := 0
				for key := range settled {
					if key.target == addr && key.callID == id {
						transitions++
					}
				}
				if transitions != 1 || !settled[q2ConsumerSettleKey{addr, id, "running"}] || call.Status != "success" {
					t.Fatalf("call %q must have exactly one physical running-to-success settlement after its source; got %d transitions, projected call=%+v", id, transitions, call)
				}
			}
		}
		if matches != 1 {
			t.Fatalf("call %q has %d physical source occurrences, want exactly 1", id, matches)
		}
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

type q2ConsumerSettleKey struct {
	target session.ArchiveAddress
	callID session.ToolCallID
	from   string
}

// 8e17995a0 / paging spec C2: retain the running source's exact position and
// settle it once, only after that source, without changing the call's identity.
// These instruction/receipt fixtures require settles only, not projections.
func q2ConsumerApplySettle(t *testing.T, entries []session.TranscriptEntry, sources map[session.ArchiveAddress]int, seen map[q2ConsumerSettleKey]bool, record session.ArchiveRecord) {
	t.Helper()
	if err := record.Validate(); err != nil {
		t.Fatalf("invalid physical effect %q: %v", record.ID, err)
	}
	effect := record.ToolCallEffect
	if effect == nil || effect.Settle == nil || len(effect.Projections) != 0 || len(effect.Retracts) != 0 {
		t.Fatalf("unexpected physical effect %q: these fixtures require an exact settlement", record.ID)
	}
	settle := effect.Settle
	index, exists := sources[settle.Target]
	if !exists {
		t.Fatalf("settlement %q must follow its exact physical source %+v, not precede it or target another occurrence", record.ID, settle.Target)
	}
	key := q2ConsumerSettleKey{settle.Target, settle.ToolCall.ID, settle.IfStatus}
	if seen[key] {
		t.Fatalf("duplicate physical settlement of call %q from %q at %+v", key.callID, key.from, key.target)
	}
	seen[key] = true
	matches := 0
	for i := range entries[index].ToolCalls {
		call := &entries[index].ToolCalls[i]
		if call.ID != settle.ToolCall.ID {
			continue
		}
		matches++
		if settle.IfStatus == "" || call.Status != settle.IfStatus {
			t.Fatalf("settlement %q guard=%q, preceding physical status=%q; require the exact guarded sequence", record.ID, settle.IfStatus, call.Status)
		}
		if call.Tool != settle.ToolCall.Tool || !reflect.DeepEqual(call.Parameters, settle.ToolCall.Parameters) {
			t.Fatalf("settlement %q changed the tool or parameters of call %q", record.ID, call.ID)
		}
		if call.Status != "pending" && call.Status != "running" {
			t.Fatalf("settlement %q tried to settle call %q again after terminal status %q", record.ID, call.ID, call.Status)
		}
		if call.Status == "running" {
			switch settle.ToolCall.Status {
			case "success", "error", "cancelled", "interrupted":
			default:
				t.Fatalf("running call %q settled to nonterminal %q", call.ID, settle.ToolCall.Status)
			}
		}
		*call = settle.ToolCall
	}
	if matches != 1 {
		t.Fatalf("settlement %q matched %d source calls, want exactly 1", record.ID, matches)
	}
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
	// ViewMembership is stamped by the WRITER, not by this fixture, and the
	// writer here is the TRANSCRIPT path: AppendTranscriptOnce ->
	// appendTranscriptLocked stamps ViewMembershipChat on every transcript-only
	// write (pkg/session/transcript_indexed.go: "a transcript append is a CHAT
	// record, fixed by the writer API"). The earlier expectation of
	// ViewMembershipBoth came from the model-append path, which this fixture
	// never takes — it made the whole-struct comparison against the durable tail
	// a guaranteed mismatch.
	return session.TranscriptEntry{ID: "instruction-" + messageID, Role: "user", Content: text, AgentID: testDefaultAgentID, ViewMembership: session.ViewMembershipChat}
}

func q2ConsumerMarker(messageID string) session.TranscriptEntry {
	return session.TranscriptEntry{ID: "consumed-" + messageID, Type: session.EntryTypeSystem, Role: "system", Content: "consumed " + messageID, AgentID: testDefaultAgentID, ViewMembership: session.ViewMembershipChat}
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
