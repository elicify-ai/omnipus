//go:build goolm && stdjson

package memory

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// countFileLines counts the total non-empty lines in a file.
// This is a test-local helper distinct from the production countLines
// so that tests remain independent of internal implementation.
func countFileLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("countFileLines open %q: %v", path, err)
	}
	defer f.Close()
	n := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	for scanner.Scan() {
		if len(scanner.Bytes()) > 0 {
			n++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("countFileLines scan: %v", err)
	}
	return n
}

// TestArchive_SkipEvictionDeletesZeroBytes verifies that TruncateHistory
// (which advances meta.Skip) leaves the on-disk JSONL file completely
// untouched — zero bytes deleted, line count unchanged (FR-005, T5).
//
// BDD: Given a session with 10 messages,
//
//	When TruncateHistory is called to keep the last 3 (advancing Skip=7),
//	Then the on-disk .jsonl line count is still 10 (nothing deleted).
func TestArchive_SkipEvictionDeletesZeroBytes(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const key = "evict-zero"
	const total = 10
	const keepLast = 3

	for i := 0; i < total; i++ {
		if err := store.AddMessage(ctx, key, "user", string(rune('a'+i))); err != nil {
			t.Fatalf("AddMessage %d: %v", i, err)
		}
	}

	// Confirm initial on-disk line count.
	path := store.jsonlPath(key)
	beforeLines := countFileLines(t, path)
	if beforeLines != total {
		t.Fatalf("before eviction: expected %d lines on disk, got %d", total, beforeLines)
	}

	// Advance Skip (simulate eviction).
	if err := store.TruncateHistory(ctx, key, keepLast); err != nil {
		t.Fatalf("TruncateHistory: %v", err)
	}

	// After Skip-advance: on-disk line count MUST be unchanged.
	afterLines := countFileLines(t, path)
	if afterLines != total {
		t.Fatalf("after eviction: expected %d lines on disk (zero deleted), got %d",
			total, afterLines)
	}

	// GetHistory must return only keepLast messages (the live window).
	history, err := store.GetHistory(ctx, key)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(history) != keepLast {
		t.Fatalf("GetHistory: expected %d messages, got %d", keepLast, len(history))
	}
}

// TestAddMsg_StampsTimestamp_LegacyLineZero verifies FR-017:
//  1. A freshly-added message round-trips with TS > 0 via ReadArchive.
//  2. A hand-written legacy line (bare providers.Message JSON, no "ts" field)
//     unmarshals to TS==0 without error when read via ReadArchive.
//
// BDD: Given a store,
//
//	When AddMessage is called and then a legacy bare-JSON line is appended manually,
//	Then ReadArchive returns two ArchivedMessages: first with TS>0, second with TS==0.
func TestAddMsg_StampsTimestamp_LegacyLineZero(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const key = "ts-stamp"

	before := time.Now().Unix()
	if err := store.AddMessage(ctx, key, "user", "hello"); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	after := time.Now().Unix()

	// Hand-write a legacy line with no "ts" field (bare providers.Message JSON).
	legacyMsg := providers.Message{Role: "assistant", Content: "legacy-no-ts"}
	legacyLine, err := json.Marshal(legacyMsg)
	if err != nil {
		t.Fatalf("marshal legacy: %v", err)
	}
	legacyLine = append(legacyLine, '\n')

	path := store.jsonlPath(key)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, wErr := f.Write(legacyLine); wErr != nil {
		f.Close()
		t.Fatalf("write legacy line: %v", wErr)
	}
	f.Close()

	// ReadArchive must return both messages: the stamped one and the legacy one.
	archived, err := store.ReadArchive(ctx, key)
	if err != nil {
		t.Fatalf("ReadArchive: %v", err)
	}
	if len(archived) != 2 {
		t.Fatalf("ReadArchive: expected 2 messages, got %d", len(archived))
	}

	// First message: role/content correct, TS in [before, after].
	if archived[0].Role != "user" || archived[0].Content != "hello" {
		t.Errorf("archived[0] = %+v", archived[0])
	}
	if archived[0].TS < before || archived[0].TS > after+1 {
		t.Errorf("archived[0].TS = %d, want in [%d, %d]", archived[0].TS, before, after+1)
	}

	// Second (legacy) message: role/content correct, TS==0 — must not error.
	if archived[1].Role != "assistant" || archived[1].Content != "legacy-no-ts" {
		t.Errorf("archived[1] = %+v", archived[1])
	}
	if archived[1].TS != 0 {
		t.Errorf("archived[1].TS = %d, want 0 for legacy line", archived[1].TS)
	}

	// GetHistory must still return plain providers.Message (TS stripped).
	history, err := store.GetHistory(ctx, key)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("GetHistory: expected 2 messages, got %d", len(history))
	}
	if history[0].Role != "user" || history[0].Content != "hello" {
		t.Errorf("history[0] = %+v", history[0])
	}
	if history[1].Role != "assistant" || history[1].Content != "legacy-no-ts" {
		t.Errorf("history[1] = %+v", history[1])
	}
}

// TestReadArchive_ReachesEvictedTurn verifies FR-016:
// After TruncateHistory advances Skip, ReadArchive still returns the evicted
// messages (index < meta.Skip) while GetHistory does not (T25).
//
// BDD: Given a session with 5 messages,
//
//	When TruncateHistory keeps only the last 2 (Skip=3),
//	Then GetHistory returns 2 messages (the live window),
//	And ReadArchive returns all 5 messages (the full archive).
func TestReadArchive_ReachesEvictedTurn(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const key = "reach-evicted"

	contents := []string{"msg0", "msg1", "msg2", "msg3", "msg4"}
	for _, c := range contents {
		if err := store.AddMessage(ctx, key, "user", c); err != nil {
			t.Fatalf("AddMessage %q: %v", c, err)
		}
	}

	// Advance Skip: keep last 2 → Skip=3.
	if err := store.TruncateHistory(ctx, key, 2); err != nil {
		t.Fatalf("TruncateHistory: %v", err)
	}

	// GetHistory must return only the live window (last 2).
	history, err := store.GetHistory(ctx, key)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("GetHistory: expected 2, got %d", len(history))
	}
	if history[0].Content != "msg3" || history[1].Content != "msg4" {
		t.Errorf("GetHistory returned wrong messages: %+v", history)
	}

	// ReadArchive must return ALL 5 messages — including the evicted ones.
	archived, err := store.ReadArchive(ctx, key)
	if err != nil {
		t.Fatalf("ReadArchive: %v", err)
	}
	if len(archived) != 5 {
		t.Fatalf("ReadArchive: expected 5 (full archive), got %d", len(archived))
	}

	// First archived message is an evicted one (index 0 < Skip=3).
	if archived[0].Content != "msg0" {
		t.Errorf("archived[0].Content = %q, want %q", archived[0].Content, "msg0")
	}
	// Last archived message is a live one.
	if archived[4].Content != "msg4" {
		t.Errorf("archived[4].Content = %q, want %q", archived[4].Content, "msg4")
	}
}

// TestSave_DoesNotCompactSkipped verifies FR-005 at the JSONLStore level:
// After TruncateHistory (which advances Skip), the on-disk log still contains
// the skipped lines, and ReadArchive still returns them (T6).
//
// BDD: Given 8 messages in a session,
//
//	When TruncateHistory keeps the last 3,
//	Then the on-disk .jsonl has 8 lines (unchanged),
//	And ReadArchive returns 8 ArchivedMessages.
//
// Note: JSONLStore has no Save method of its own — this behaviour is guaranteed
// because the only callers of Compact (which would drop lines) are tests.
// The Save path in JSONLBackend and UnifiedStore is a no-op (FR-005).
func TestSave_DoesNotCompactSkipped(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const key = "no-compact"
	const total = 8
	const keepLast = 3

	for i := 0; i < total; i++ {
		if err := store.AddMessage(ctx, key, "user", string(rune('a'+i))); err != nil {
			t.Fatalf("AddMessage %d: %v", i, err)
		}
	}

	// Simulate eviction.
	if err := store.TruncateHistory(ctx, key, keepLast); err != nil {
		t.Fatalf("TruncateHistory: %v", err)
	}

	// On-disk line count must be unchanged — no Compact was called.
	path := store.jsonlPath(key)
	diskLines := countFileLines(t, path)
	if diskLines != total {
		t.Fatalf("on disk: expected %d lines (none deleted), got %d", total, diskLines)
	}

	// ReadArchive reaches every line including the skipped ones.
	archived, err := store.ReadArchive(ctx, key)
	if err != nil {
		t.Fatalf("ReadArchive: %v", err)
	}
	if len(archived) != total {
		t.Fatalf("ReadArchive: expected %d messages, got %d", total, len(archived))
	}

	// The evicted messages (index < Skip) are present in the archive.
	skipped := total - keepLast
	if archived[0].Content != "a" {
		t.Errorf("archived[0].Content = %q, want %q (first evicted)", archived[0].Content, "a")
	}
	evictedLast := string(rune('a' + skipped - 1))
	if archived[skipped-1].Content != evictedLast {
		t.Errorf("archived[%d].Content = %q, want %q (last evicted)",
			skipped-1, archived[skipped-1].Content, evictedLast)
	}

	// The live messages are also present.
	firstLive := string(rune('a' + skipped))
	if archived[skipped].Content != firstLive {
		t.Errorf("archived[%d].Content = %q, want %q (first live)",
			skipped, archived[skipped].Content, firstLive)
	}
}
