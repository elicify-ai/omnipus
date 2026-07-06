// memory_citation_test.go — tests for cited_in (op:cited) event emission
// (FR-7.5 / NFR-1). Verifies that when a recalled memory's ID or title appears
// in the agent's response text, a CounterOpCited record is appended to the
// memory's room counters.jsonl.

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/memrooms"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// readCounterRecords parses a counters.jsonl file into CounterRecords.
func readCounterRecords(t *testing.T, path string) []memrooms.CounterRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read counters: %v", err)
	}
	var recs []memrooms.CounterRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r memrooms.CounterRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("unmarshal counter line %q: %v", line, err)
		}
		recs = append(recs, r)
	}
	return recs
}

// countOp returns how many counter records have the given op.
func countOp(recs []memrooms.CounterRecord, op memrooms.CounterOp) int {
	n := 0
	for _, r := range recs {
		if r.Op == op {
			n++
		}
	}
	return n
}

// TestCitationTracker_RecallThenCiteWritesOpCited verifies the full chain:
// recall_memory (via the real adapter + tool) records the surfaced memory into
// the turn's citation tracker, and a subsequent EmitCitations call whose text
// contains the recalled memory's ID appends exactly one op:cited record to
// counters.jsonl.
func TestCitationTracker_RecallThenCiteWritesOpCited(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	defer ms.Close()

	if err := ms.AppendLongTerm("Distributed tracing with Jaeger helps debug latency.", "reference"); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	adapter := NewMemoryStoreAdapter(ms)
	recall := tools.NewRecallMemoryTool(adapter)
	tracker := newCitationTracker(ms)
	ctx := tools.WithCitationTracker(context.Background(), tracker)

	// Recall surfaces the memory and records it on the tracker.
	res := recall.Execute(ctx, map[string]any{"query": "jaeger"})
	if res == nil || res.IsError {
		t.Fatalf("recall failed: %+v", res)
	}
	results, err := ms.SearchEntries("jaeger", 10)
	if err != nil || len(results) == 0 {
		t.Fatalf("no recall results to derive a memory ID: %v", err)
	}
	memID := results[0].ID
	if memID == "" {
		t.Fatal("recalled memory has empty ID; cannot test citation")
	}

	// Before citation: counters.jsonl should have only access events (written
	// by the recall path), no cited events.
	countersPath := ms.PrivateRoom().CountersPath
	before := readCounterRecords(t, countersPath)
	if got := countOp(before, memrooms.CounterOpCited); got != 0 {
		t.Fatalf("expected 0 cited events before citation, got %d", got)
	}
	if got := countOp(before, memrooms.CounterOpAccess); got == 0 {
		t.Fatal("expected at least one access event from recall, got 0")
	}

	// Agent response that references the memory ID by id: citation should fire.
	tracker.EmitCitations("Based on memory id:" + memID + " we use Jaeger.")

	after := readCounterRecords(t, countersPath)
	cited := countOp(after, memrooms.CounterOpCited)
	if cited != 1 {
		t.Fatalf("expected 1 op:cited event after citation, got %d (records: %+v)", cited, after)
	}

	// The cited record must reference the right memory.
	var citedRec *memrooms.CounterRecord
	for i := range after {
		if after[i].Op == memrooms.CounterOpCited {
			citedRec = &after[i]
			break
		}
	}
	if citedRec == nil {
		t.Fatal("cited record not found")
	}
	if citedRec.MemoryID != memID {
		t.Errorf("cited memory_id = %q, want %q", citedRec.MemoryID, memID)
	}
	if citedRec.By == "" {
		t.Error("cited record has empty By (agent id)")
	}

	// Dedup within a turn: a second EmitCitations referencing the same ID must
	// NOT append another op:cited.
	tracker.EmitCitations("again referencing " + memID + " in a second iteration")
	after2 := readCounterRecords(t, countersPath)
	if got := countOp(after2, memrooms.CounterOpCited); got != 1 {
		t.Errorf("expected dedup to 1 cited event, got %d", got)
	}
}

// TestCitationTracker_NoReferenceNoCite verifies that an agent response which
// does NOT reference a recalled memory's ID or title produces no op:cited.
func TestCitationTracker_NoReferenceNoCite(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	defer ms.Close()

	if err := ms.AppendLongTerm("We deploy with Helm on AWS EKS.", "reference"); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	adapter := NewMemoryStoreAdapter(ms)
	recall := tools.NewRecallMemoryTool(adapter)
	tracker := newCitationTracker(ms)
	ctx := tools.WithCitationTracker(context.Background(), tracker)

	if res := recall.Execute(ctx, map[string]any{"query": "helm"}); res == nil || res.IsError {
		t.Fatalf("recall failed: %+v", res)
	}

	// Unrelated response text — no citation.
	tracker.EmitCitations("I have no idea what we use for deployments.")

	countersPath := ms.PrivateRoom().CountersPath
	recs := readCounterRecords(t, countersPath)
	if got := countOp(recs, memrooms.CounterOpCited); got != 0 {
		t.Errorf("expected 0 cited events for unrelated text, got %d", got)
	}
}

// TestCitationTracker_TitleCite verifies that referencing the memory's TITLE
// (not the ID) also produces an op:cited event.
func TestCitationTracker_TitleCite(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	defer ms.Close()

	if err := ms.AppendLongTerm("Postgres replication lag runbook steps", "reference"); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	adapter := NewMemoryStoreAdapter(ms)
	recall := tools.NewRecallMemoryTool(adapter)
	tracker := newCitationTracker(ms)
	ctx := tools.WithCitationTracker(context.Background(), tracker)

	if res := recall.Execute(ctx, map[string]any{"query": "postgres"}); res == nil || res.IsError {
		t.Fatalf("recall failed: %+v", res)
	}

	results, err := ms.SearchEntries("postgres", 10)
	if err != nil || len(results) == 0 {
		t.Fatalf("no recall results: %v", err)
	}
	title := results[0].Title
	if len([]rune(title)) < 4 {
		t.Skipf("title too short for citation matching: %q", title)
	}

	// Reference the title verbatim (case-insensitive).
	lower := strings.ToLower(title)
	tracker.EmitCitations("As noted in " + lower + ", replication lag is monitored.")

	countersPath := ms.PrivateRoom().CountersPath
	recs := readCounterRecords(t, countersPath)
	if got := countOp(recs, memrooms.CounterOpCited); got != 1 {
		t.Errorf("expected 1 cited event for title reference, got %d (title=%q)", got, title)
	}
}

// TestCitationTracker_NilTrackerIsNoOp verifies that a nil tracker (main
// gateway agent) does not panic and recall still works.
func TestCitationTracker_NilTrackerIsNoOp(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	defer ms.Close()

	if err := ms.AppendLongTerm("nil tracker smoke test content", "reference"); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	adapter := NewMemoryStoreAdapter(ms)
	recall := tools.NewRecallMemoryTool(adapter)
	// No tracker installed on ctx (mirrors the main gateway agent path).
	ctx := context.Background()

	res := recall.Execute(ctx, map[string]any{"query": "smoke"})
	if res == nil || res.IsError {
		t.Fatalf("recall without tracker failed: %+v", res)
	}

	// newCitationTracker(nil) returns nil; EmitCitations must be nil-safe.
	var nilTracker *citationTracker
	nilTracker.EmitCitations("anything " + filepath.Base(ms.PrivateRoom().Root))
}
