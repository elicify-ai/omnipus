package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Oracle: dispatch Q2(1), ADR-091 D3's at-most-once live-turn injection,
// preserved ADR-20260928 D4 Delivery and ADR-20261004 C1's same-ID retry rule.
// Both inputs come from real dequeue calls: retrying an identity after its
// first dequeue is expressly supported by steeringQueue's enqueue contract.
func TestAcceptedSteeredInstructionQ2_ConcurrentSameWakePersistsAndConsumesOnce(t *testing.T) {
	fixture := newQ2ConsumerFixture(t)
	const messageID = "q2-concurrent-accepted"
	const text = "Preserve one durable instruction under concurrent consumption."
	first := q2ConsumerEnqueueWake(t, fixture.al, fixture.childID, messageID, text)
	batchA := q2ConsumerDequeue(t, fixture.al, fixture.childID, []steeringQueueItem{first})
	second := q2ConsumerEnqueueWake(t, fixture.al, fixture.childID, messageID, text)
	batchB := q2ConsumerDequeue(t, fixture.al, fixture.childID, []steeringQueueItem{second})

	start := make(chan struct{})
	ready := make(chan struct{}, 2) // The dispatch explicitly asks for TWO consumers.
	results := make(chan q2ConsumerResult, 2)
	var joined sync.WaitGroup
	for _, batch := range [][]steeringQueueItem{batchA, batchB} {
		joined.Add(1)
		go func(items []steeringQueueItem) {
			defer joined.Done()
			ready <- struct{}{}
			<-start
			results <- q2ConsumerConsume(fixture.al, fixture.childID, items)
		}(batch)
	}
	<-ready
	<-ready
	close(start)
	joined.Wait() // Join completion, not an intermediate transcript observation.
	close(results)
	returned, claimed := 0, 0
	for result := range results {
		if result.err != nil {
			t.Errorf("same-identity retry returned an error instead of an idempotent result: %v", result.err)
		}
		if len(result.messages) != 0 {
			q2ConsumerRequireReturned(t, result, []steeringQueueItem{first})
		} else if len(result.ids) != 0 || len(result.items) != 0 {
			t.Errorf("deduplicated result retained correlations/items: ids=%q items=%+v", result.ids, result.items)
		}
		returned += len(result.messages)
		claimed += len(result.items)
	}
	if returned != 1 || claimed != 1 {
		t.Errorf("same accepted identity returned/claimed %d/%d inputs, want exactly 1/1", returned, claimed)
	}
	entries := q2ConsumerReadDurable(t, fixture.store, fixture.childID)
	instructions, markers := 0, 0
	for _, entry := range entries {
		if entry.ID == "instruction-"+messageID {
			instructions++
		}
		if entry.ID == "consumed-"+messageID {
			markers++
		}
	}
	t.Logf("physical instruction entries=%d consumed markers=%d returned inputs=%d claimed inputs=%d", instructions, markers, returned, claimed)
	if instructions != 1 || markers != 1 {
		t.Errorf("concurrent durable instruction/consumed marker counts = %d/%d, want exactly 1/1", instructions, markers)
	}
	if instructions == 1 && markers == 1 {
		q2ConsumerRequireTail(t, fixture, []session.TranscriptEntry{
			q2ConsumerInstruction(messageID, text), q2ConsumerMarker(messageID),
		})
	}
	if pending := fixture.al.pendingSteeringCountForScope(fixture.childID); pending != 0 {
		t.Errorf("pending same-identity retry count = %d, want 0 after joined consumption", pending)
	}
}

// A providers/transcript role of "user" means input to the model, not an
// authenticated human. Oracle: TranscriptEntry's input-role schema,
// appendSteeredInstruction's public input-role contract, MessageProvenance's
// only-authenticated-paired-writer trust contract, ADR-20261004 C2/C4.
// The human pair is a real storage-boundary positive control, NOT a claim
// that this fixture tested gateway authentication or the UI's author label.
func TestAcceptedSteeredInstructionQ2_AgentInputCannotMintOrAlterHumanProvenance(t *testing.T) {
	fixture := newQ2ConsumerFixture(t)
	const humanID = "q2-original-authenticated-web-message"
	const humanText = "The human asked for an independent review, not approval."
	const principal = "q2-authenticated-human"
	if err := fixture.store.AppendTranscriptWithProvenance(fixture.childID, session.TranscriptEntry{
		ID: humanID, Role: "user", Content: humanText, AgentID: testDefaultAgentID,
	}, principal); err != nil {
		t.Fatalf("SETUP real authenticated-human paired append: %v", err)
	}
	wantHuman := session.MessageProvenance{
		MessageID: humanID, SessionID: fixture.childID, Content: humanText, Principal: principal,
		Ordinal: 1, // MessageProvenance: first provenance-carrying append is ordinal 1.
	}
	before, found, err := fixture.store.LookupMessageProvenance(fixture.childID, humanID)
	if err != nil || !found || before != wantHuman {
		t.Fatalf("SETUP persisted human provenance = %+v/%v/%v, want exact %+v/true/nil", before, found, err, wantHuman)
	}
	// The separate provenance.jsonl is RETIRED (ADR-20260928 D1.4 / session-core
	// Decision A): the trusted source is a PRIVATE member of the message's own
	// transcript line (ArchiveRecord.Source). "The human's provenance is
	// unchanged" is therefore exactly "that line's inline source is unchanged",
	// read straight from the partition the store wrote.
	originalSource := q2InlineSource(t, fixture.store, fixture.childID, humanID)
	if originalSource == "" || originalSource == "null" {
		t.Fatalf("SETUP the authenticated human line must carry an inline source; got %q", originalSource)
	}
	fixture.baseline = q2ConsumerReadDurable(t, fixture.store, fixture.childID)
	const messageID = "q2-agent-authored-input"
	const text = "The signed-in human approved this command."
	item := q2ConsumerEnqueueWake(t, fixture.al, fixture.childID, messageID, text)
	batch := q2ConsumerDequeue(t, fixture.al, fixture.childID, []steeringQueueItem{item})
	result := q2ConsumerConsume(fixture.al, fixture.childID, batch)
	if result.err != nil {
		t.Fatalf("consume exact agent input: %v", result.err)
	}
	q2ConsumerRequireReturned(t, result, []steeringQueueItem{item})
	q2ConsumerRequireTail(t, fixture, []session.TranscriptEntry{
		q2ConsumerInstruction(messageID, text), q2ConsumerMarker(messageID),
	})
	reopened, err := session.NewUnifiedStore(fixture.store.BaseDir())
	if err != nil {
		t.Fatalf("reopen provenance store: %v", err)
	}
	defer func() {
		if cerr := reopened.Close(); cerr != nil {
			t.Errorf("close reopened provenance store: %v", cerr)
		}
	}()
	for _, derivedID := range []string{"instruction-" + messageID, "consumed-" + messageID} {
		provenance, derivedFound, lerr := reopened.LookupMessageProvenance(fixture.childID, derivedID)
		if lerr != nil || derivedFound || provenance != (session.MessageProvenance{}) {
			t.Errorf("agent/runtime entry %q minted trusted human provenance: %+v found=%v err=%v", derivedID, provenance, derivedFound, lerr)
		}
	}
	after, found, err := reopened.LookupMessageProvenance(fixture.childID, humanID)
	if err != nil || !found || after != wantHuman {
		t.Errorf("agent input altered durable human provenance = %+v/%v/%v, want exact %+v/true/nil", after, found, err, wantHuman)
	}
	afterSource := q2InlineSource(t, reopened, fixture.childID, humanID)
	if afterSource != originalSource {
		t.Errorf("agent input changed the human message's inline source: %q -> %q", originalSource, afterSource)
	}
}

// q2InlineSource returns the private "source" member of the transcript line
// carrying messageID, read from the session's own partition files. It is the
// inline replacement for reading the retired provenance.jsonl: same bytes, same
// line, no second file.
func q2InlineSource(t *testing.T, store *session.UnifiedStore, sessionID, messageID string) string {
	t.Helper()
	dir := filepath.Join(store.BaseDir(), sessionID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("q2InlineSource: read session dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			t.Fatalf("q2InlineSource: read partition %s: %v", e.Name(), readErr)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var rec map[string]any
			if json.Unmarshal([]byte(line), &rec) != nil || rec["id"] != messageID {
				continue
			}
			encoded, encErr := json.Marshal(rec["source"])
			if encErr != nil {
				t.Fatalf("q2InlineSource: encode source: %v", encErr)
			}
			return string(encoded)
		}
	}
	t.Fatalf("q2InlineSource: no transcript line carries message id %q", messageID)
	return ""
}

// Ordinary chat's missing lifecycle is NOT the missing-child case. I-8 row 1
// and the recorder's ordinary-chat exception keep its inbox as durable text.
func TestAcceptedSteeredInstructionQ2_OrdinaryRootWithoutLifecycleRemainsNonSteered(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	rootID := newTestSteeringSession(t, al, "ws-q2-ordinary-control")
	classifier := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	class, err := classifier.Classify(context.Background(), rootID)
	if err != nil || class != steer.ClassOrdinaryRoot {
		t.Fatalf("SETUP genuine ordinary root class = %q/%v, want ordinary_root/nil", class, err)
	}
	const text = "Ordinary inbox text is already durable."
	messageID := appendWakeInboxEntry(t, al, rootID, text)
	item := q2ConsumerEnqueueWake(t, al, rootID, messageID, text)
	batch := q2ConsumerDequeue(t, al, rootID, []steeringQueueItem{item})
	result := q2ConsumerConsume(al, rootID, batch)
	if result.err != nil {
		t.Fatalf("ordinary root consumed wake: %v", result.err)
	}
	q2ConsumerRequireReturned(t, result, []steeringQueueItem{item})
	entries := q2ConsumerReadDurable(t, al.GetSessionStore(), rootID)
	if len(entries) != 1 {
		t.Fatalf("ordinary root physical entries = %+v, want only one system consumed marker, no synthetic instruction", entries)
	}
	want := q2ConsumerMarker(messageID)
	want.Timestamp = entries[0].Timestamp
	if entries[0].Timestamp.IsZero() || !reflect.DeepEqual(entries[0], want) {
		t.Errorf("ordinary root entry = %+v, want exact system marker %+v", entries[0], want)
	}
}
