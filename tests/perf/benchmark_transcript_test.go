package perf

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/session"
)

const (
	transcriptGoroutines  = 50
	transcriptMsgsPerGRtn = 20
	transcriptTotal       = transcriptGoroutines * transcriptMsgsPerGRtn // 1000
)

// BenchmarkTranscriptAppendContention measures throughput of concurrent JSONL
// transcript appends from 50 goroutines writing to a single session.
// After all writes complete it reads back the transcript and asserts perfect fidelity.
func BenchmarkTranscriptAppendContention(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		dir := b.TempDir()
		store, err := session.NewUnifiedStore(dir)
		if err != nil {
			b.Fatalf("BenchmarkTranscriptAppendContention: NewUnifiedStore: %v", err)
		}

		meta, err := store.NewSession(session.SessionTypeChat, "perf", "main")
		if err != nil {
			b.Fatalf("BenchmarkTranscriptAppendContention: NewSession: %v", err)
		}
		sid := meta.ID

		var wg sync.WaitGroup
		wg.Add(transcriptGoroutines)

		b.ResetTimer()
		for g := 0; g < transcriptGoroutines; g++ {
			gNum := g
			go func() {
				defer wg.Done()
				for m := 0; m < transcriptMsgsPerGRtn; m++ {
					entry := session.TranscriptEntry{
						ID:        fmt.Sprintf("g%d-m%d-%s", gNum, m, uuid.New().String()),
						Role:      "user",
						Content:   fmt.Sprintf("goroutine %d message %d", gNum, m),
						Timestamp: time.Now(),
						AgentID:   "main",
					}
					if appendErr := store.AppendTranscript(sid, entry); appendErr != nil {
						// Cannot call b.Fatal from goroutine; log and let assertion catch it.
						_ = appendErr
					}
				}
			}()
		}
		wg.Wait()
		b.StopTimer()

		// Fidelity assertion: read back and verify.
		entries, err := store.ReadTranscript(sid)
		if err != nil {
			b.Fatalf("BenchmarkTranscriptAppendContention: ReadTranscript: %v", err)
		}
		if len(entries) != transcriptTotal {
			b.Fatalf("BenchmarkTranscriptAppendContention: expected %d entries, got %d (data loss detected)",
				transcriptTotal, len(entries))
		}
		if err := assertTranscriptFidelity(entries); err != nil {
			b.Fatalf("BenchmarkTranscriptAppendContention: fidelity check: %v", err)
		}

		b.ReportMetric(float64(transcriptTotal), "msgs_written")
		b.ReportMetric(float64(len(entries)), "msgs_read")
	}
}

// TestNoTranscriptDataLossUnderContention is the SLO gate for transcript fidelity.
// It writes 1000 messages from 50 concurrent goroutines to a single session and
// asserts: exactly 1000 entries persisted, all parse cleanly, no duplicate IDs.
func TestNoTranscriptDataLossUnderContention(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	dir := t.TempDir()
	store, err := session.NewUnifiedStore(dir)
	if err != nil {
		t.Fatalf("TestNoTranscriptDataLossUnderContention: NewUnifiedStore: %v", err)
	}

	meta, err := store.NewSession(session.SessionTypeChat, "perf", "main")
	if err != nil {
		t.Fatalf("TestNoTranscriptDataLossUnderContention: NewSession: %v", err)
	}
	sid := meta.ID

	var wg sync.WaitGroup
	var appendErrs []error
	var mu sync.Mutex

	wg.Add(transcriptGoroutines)
	for g := 0; g < transcriptGoroutines; g++ {
		gNum := g
		go func() {
			defer wg.Done()
			for m := 0; m < transcriptMsgsPerGRtn; m++ {
				entry := session.TranscriptEntry{
					ID:        fmt.Sprintf("g%d-m%d-%s", gNum, m, uuid.New().String()),
					Role:      "user",
					Content:   fmt.Sprintf("goroutine %d message %d", gNum, m),
					Timestamp: time.Now(),
					AgentID:   "main",
				}
				if appendErr := store.AppendTranscript(sid, entry); appendErr != nil {
					mu.Lock()
					appendErrs = append(appendErrs, fmt.Errorf("goroutine %d message %d: %w", gNum, m, appendErr))
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	if len(appendErrs) > 0 {
		for _, e := range appendErrs {
			t.Errorf("TestNoTranscriptDataLossUnderContention: append error: %v", e)
		}
		t.FailNow()
	}

	entries, err := store.ReadTranscript(sid)
	if err != nil {
		t.Fatalf("TestNoTranscriptDataLossUnderContention: ReadTranscript: %v", err)
	}

	if len(entries) != transcriptTotal {
		t.Errorf(
			"TestNoTranscriptDataLossUnderContention FAILED: expected %d entries, got %d — data loss detected. "+
				"The JSONL transcript writer must be atomic and serialized. Missing: %d entries.",
			transcriptTotal, len(entries), transcriptTotal-len(entries),
		)
	}

	if err := assertTranscriptFidelity(entries); err != nil {
		t.Errorf("TestNoTranscriptDataLossUnderContention FAILED: fidelity violation: %v", err)
	}

	t.Logf("TestNoTranscriptDataLossUnderContention: %d/%d entries verified "+
		"(no data loss, no duplicates, all parse clean)",
		len(entries), transcriptTotal)
}

// assertTranscriptFidelity verifies that every entry JSON-parses cleanly (non-empty ID)
// and that no duplicate IDs exist.
func assertTranscriptFidelity(entries []session.TranscriptEntry) error {
	seen := make(map[string]int, len(entries))
	for i, e := range entries {
		if e.ID == "" {
			return fmt.Errorf("entry[%d] has empty ID — JSON corruption or missing field", i)
		}
		if prev, dup := seen[e.ID]; dup {
			return fmt.Errorf("duplicate ID %q at entries[%d] and entries[%d]", e.ID, prev, i)
		}
		seen[e.ID] = i
	}
	return nil
}
