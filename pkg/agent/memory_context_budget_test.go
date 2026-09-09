package agent

import (
	"strings"
	"testing"
	"time"
)

// TestGetMemoryContext_CharBudget_EvictsOldestFirst covers finding 9
// (context-audit 2026-08): GetMemoryContext used to inject up to 20 memory
// entries verbatim with no total size cap — with each entry independently
// allowed up to 4096 runes, that's 80K+ characters on every single turn.
// This proves the char budget actually bites: with enough entries to exceed
// maxMemoryContextChars, older entries are dropped first (SearchEntries
// returns newest-first, so the newest content must survive) and a
// truncation footer names how many were cut.
func TestGetMemoryContext_CharBudget_EvictsOldestFirst(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)
	t.Cleanup(ms.Close)

	// 6 entries of 2000 chars each = 12000 chars of raw content alone,
	// comfortably over the 8000-char budget — forces eviction.
	const perEntry = 2000
	const count = 6
	markers := make([]string, count)
	for i := 0; i < count; i++ {
		marker := "MARKER_" + string(rune('A'+i))
		markers[i] = marker
		content := marker + "_" + strings.Repeat("x", perEntry-len(marker)-1)
		if err := ms.AppendLongTerm(content, "reference"); err != nil {
			t.Fatalf("AppendLongTerm %d: %v", i, err)
		}
		// Distinct mtimes so newest-first ordering is deterministic (mirrors
		// the sleep pattern in memory_smoke_test.go's TestGetMemoryContextIncludes).
		time.Sleep(3 * time.Millisecond)
	}

	ctx := ms.GetMemoryContext()

	if ctx == "" {
		t.Fatal("GetMemoryContext returned empty string")
	}

	// Total size must stay bounded — not the ~12000+ chars of raw content
	// plus headers/separators that an uncapped version would produce.
	if len(ctx) > maxMemoryContextChars+2000 {
		t.Errorf("GetMemoryContext output too large (%d chars) — budget cap not enforced; got:\n%s",
			len(ctx), ctx)
	}

	// The newest entry (MARKER_F, appended last) must survive — the budget
	// evicts from the oldest end, never the newest.
	if !strings.Contains(ctx, markers[count-1]) {
		t.Errorf("newest entry %q was evicted; oldest-first eviction is broken.\ngot:\n%s",
			markers[count-1], ctx)
	}

	// The oldest entry (MARKER_A, appended first) must have been evicted —
	// otherwise the budget isn't actually bounding anything.
	if strings.Contains(ctx, markers[0]) {
		t.Errorf("oldest entry %q was NOT evicted despite exceeding the char budget.\ngot:\n%s",
			markers[0], ctx)
	}

	// Truncation footer names the cut count and points at recall_memory.
	if !strings.Contains(ctx, "not shown") || !strings.Contains(ctx, "recall_memory") {
		t.Errorf("expected a truncation footer naming recall_memory; got:\n%s", ctx)
	}

	// Framing sentence (finding 9): tells the agent these are past notes
	// that may be stale, from itself or a teammate.
	if !strings.Contains(ctx, "may be") || !strings.Contains(ctx, "recall_memory to search") {
		t.Errorf("expected a framing sentence about stale/teammate notes; got:\n%s", ctx)
	}
}

// TestGetMemoryContext_UnderBudget_NoTruncationFooter proves the footer is
// absent when total content fits comfortably within the budget — no false
// "not shown" claim when nothing was actually cut.
func TestGetMemoryContext_UnderBudget_NoTruncationFooter(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)
	t.Cleanup(ms.Close)

	if err := ms.AppendLongTerm("a small note well under budget", "reference"); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	ctx := ms.GetMemoryContext()
	if !strings.Contains(ctx, "a small note well under budget") {
		t.Fatalf("expected the entry content in output; got:\n%s", ctx)
	}
	if strings.Contains(ctx, "not shown") {
		t.Errorf("did not expect a truncation footer when nothing was cut; got:\n%s", ctx)
	}
}
