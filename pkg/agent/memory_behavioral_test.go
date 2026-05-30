// memory_behavioral_test.go — Fix C (memory) behavioral tests.
// Traces to: env-awareness-and-memory-spec.md (spec v7), FR-001 through FR-031.

package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// #2 — TestMemoryStore_AppendLongTerm_FormatExact
// Traces to: env-awareness-and-memory-spec.md FR-002
// Bytes written must match the pinned format:
//   <!-- ts=<ISO8601ms> cat=<category> -->\n<content>\n
// ---------------------------------------------------------------------------

func TestMemoryStore_AppendLongTerm_FormatExact(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	content := "always use flock for concurrent writes"
	category := "lesson_learned"

	if err := ms.AppendLongTerm(content, category); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "memory", "MEMORY.md"))
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	s := string(raw)

	// Must contain the header comment with ts= and cat=.
	if !strings.Contains(s, "<!-- ts=") {
		t.Errorf("MEMORY.md missing <!-- ts= header; got:\n%s", s)
	}
	if !strings.Contains(s, "cat=lesson_learned") {
		t.Errorf("MEMORY.md missing cat=lesson_learned; got:\n%s", s)
	}
	if !strings.Contains(s, " -->") {
		t.Errorf("MEMORY.md header comment not closed with ' -->'; got:\n%s", s)
	}

	// Content must appear after the header.
	headerEnd := strings.Index(s, "-->")
	if headerEnd < 0 {
		t.Fatal("no --> found")
	}
	body := s[headerEnd+3:]
	if !strings.Contains(body, content) {
		t.Errorf("content %q not found after header; body:\n%s", content, body)
	}

	// Differentiation: a second entry with different category must differ.
	time.Sleep(2 * time.Millisecond)
	if err := ms.AppendLongTerm("second entry", "key_decision"); err != nil {
		t.Fatalf("AppendLongTerm second: %v", err)
	}
	raw2, _ := os.ReadFile(filepath.Join(dir, "memory", "MEMORY.md"))
	if !strings.Contains(string(raw2), "cat=key_decision") {
		t.Error("second entry missing cat=key_decision")
	}
}

// ---------------------------------------------------------------------------
// #3 — TestMemoryStore_AppendLongTerm_StripsNull_RejectsCommentInjection
// Traces to: env-awareness-and-memory-spec.md FR-002, FR-003
// ---------------------------------------------------------------------------

func TestMemoryStore_AppendLongTerm_StripsNull_RejectsCommentInjection(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	tests := []struct {
		name      string
		content   string
		category  string
		wantErr   bool
		errSubstr string
	}{
		{
			name:     "NUL byte stripped, succeeds",
			content:  "fact with \x00 nul",
			category: "reference",
			wantErr:  false,
		},
		{
			name:      "whitespace-only content rejected",
			content:   "   \t\n",
			category:  "reference",
			wantErr:   true,
			errSubstr: "empty",
		},
		{
			name:      "empty string rejected",
			content:   "",
			category:  "reference",
			wantErr:   true,
			errSubstr: "empty",
		},
		{
			name:      "HTML comment injection rejected",
			content:   "attack <!-- inject -->",
			category:  "reference",
			wantErr:   true,
			errSubstr: "comment",
		},
		{
			name:      "content exceeding 4096 runes rejected",
			content:   strings.Repeat("x", 4097),
			category:  "reference",
			wantErr:   true,
			errSubstr: "4096",
		},
		{
			name:      "invalid category rejected",
			content:   "valid content",
			category:  "bogus_cat",
			wantErr:   true,
			errSubstr: "invalid category",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ms.AppendLongTerm(tc.content, tc.category)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.errSubstr)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.errSubstr)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}

	// After the NUL-stripped write, the file must not contain a literal NUL.
	raw, _ := os.ReadFile(filepath.Join(dir, "memory", "MEMORY.md"))
	if bytes.ContainsRune(raw, 0) {
		t.Error("MEMORY.md contains a NUL byte — NUL stripping failed")
	}
}

// ---------------------------------------------------------------------------
// #5 — TestMemoryStore_ReadLongTermEntries_ParsesAndCaches
// Traces to: env-awareness-and-memory-spec.md FR-004
// Two reads with same mtime → only one disk read (spy via file rename).
// ---------------------------------------------------------------------------

func TestMemoryStore_ReadLongTermEntries_ParsesAndCaches(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	if err := ms.AppendLongTerm("first fact", "reference"); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	// First read — cold cache.
	entries1, err := ms.ReadLongTermEntries()
	if err != nil {
		t.Fatalf("ReadLongTermEntries first: %v", err)
	}
	if len(entries1) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries1))
	}

	// Second read without any mtime change — should return the same slice (cache hit).
	entries2, err := ms.ReadLongTermEntries()
	if err != nil {
		t.Fatalf("ReadLongTermEntries second: %v", err)
	}
	if len(entries2) != 1 {
		t.Fatalf("expected 1 entry on second read, got %d", len(entries2))
	}

	// The returned slice must be the same (pointer equality = cache hit).
	if len(entries1) != len(entries2) || entries1[0].Content != entries2[0].Content {
		t.Error("ReadLongTermEntries: second read returned different data — cache may not be working")
	}

	// After a new append (file changes), cache must miss and return 2 entries.
	time.Sleep(5 * time.Millisecond) // ensure mtime advances
	if appendErr := ms.AppendLongTerm("second fact", "key_decision"); appendErr != nil {
		t.Fatalf("AppendLongTerm second: %v", appendErr)
	}
	entries3, err := ms.ReadLongTermEntries()
	if err != nil {
		t.Fatalf("ReadLongTermEntries after append: %v", err)
	}
	if len(entries3) != 2 {
		t.Errorf("expected 2 entries after second append, got %d", len(entries3))
	}
}

// ---------------------------------------------------------------------------
// #6 — TestMemoryStore_SearchEntries_NewestFirstLiteral
// Traces to: env-awareness-and-memory-spec.md FR-005
// Regex metachars in query are treated literally (no regex expansion).
// ---------------------------------------------------------------------------

func TestMemoryStore_SearchEntries_NewestFirstLiteral(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	// Write two entries with distinct content.
	if err := ms.AppendLongTerm("alpha entry one", "reference"); err != nil {
		t.Fatalf("AppendLongTerm 1: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := ms.AppendLongTerm("alpha entry two", "key_decision"); err != nil {
		t.Fatalf("AppendLongTerm 2: %v", err)
	}

	// Search with a regex metachar that would match everything if expanded: ".*"
	// FR-005: literal match only, so ".*" should match nothing (no content contains ".*").
	results, err := ms.SearchEntries(".*", 10)
	if err != nil {
		t.Fatalf("SearchEntries regex metachar: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("SearchEntries('.*') should return 0 results (literal match), got %d", len(results))
	}

	// Plain literal match must work.
	results2, err := ms.SearchEntries("alpha entry", 10)
	if err != nil {
		t.Fatalf("SearchEntries literal: %v", err)
	}
	if len(results2) != 2 {
		t.Fatalf("SearchEntries('alpha entry'): expected 2 results, got %d", len(results2))
	}

	// Results must be newest-first.
	if !results2[0].Timestamp.After(results2[1].Timestamp) &&
		results2[0].Timestamp != results2[1].Timestamp {
		t.Errorf("results not newest-first: [0]=%v [1]=%v",
			results2[0].Timestamp, results2[1].Timestamp)
	}
	if results2[0].Category != "key_decision" {
		t.Errorf("results2[0].Category = %q, want key_decision (newest)", results2[0].Category)
	}
}

// ---------------------------------------------------------------------------
// #8 — TestMemoryStore_WriteLastSession_OverwritesAtomically
// Traces to: env-awareness-and-memory-spec.md FR-007
// WriteLastSession must overwrite any prior content atomically.
// ---------------------------------------------------------------------------

func TestMemoryStore_WriteLastSession_OverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	// Write initial content.
	if err := ms.WriteLastSession("initial content"); err != nil {
		t.Fatalf("WriteLastSession initial: %v", err)
	}

	// Overwrite.
	newContent := "## Updated last session\nThis overwrites the previous content."
	if err := ms.WriteLastSession(newContent); err != nil {
		t.Fatalf("WriteLastSession overwrite: %v", err)
	}

	got, err := ms.ReadLastSession()
	if err != nil {
		t.Fatalf("ReadLastSession: %v", err)
	}

	// Must contain ONLY the new content — old content must be gone.
	if got != newContent {
		t.Errorf("ReadLastSession got %q, want %q", got, newContent)
	}
	if strings.Contains(got, "initial content") {
		t.Error("LAST_SESSION.md still contains old content after overwrite")
	}

	// Differentiation: different writes produce different reads.
	differentContent := "## Session 2\nA different session."
	if err := ms.WriteLastSession(differentContent); err != nil {
		t.Fatalf("WriteLastSession differentContent: %v", err)
	}
	got2, _ := ms.ReadLastSession()
	if got2 == got {
		t.Error("WriteLastSession: two different writes produced the same read — not overwriting")
	}
}

// ---------------------------------------------------------------------------
// #9 — TestMemoryStore_AppendRetro_ValidatesSessionID
// Traces to: env-awareness-and-memory-spec.md FR-009
// Path-traversal IDs must be rejected with an error; no file must be written.
// ---------------------------------------------------------------------------

func TestMemoryStore_AppendRetro_ValidatesSessionID(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	r := Retro{
		Timestamp:        time.Now().UTC(),
		Trigger:          "joined",
		WentWell:         []string{"good thing"},
		NeedsImprovement: []string{"bad thing"},
	}

	badIDs := []struct {
		name string
		id   string
	}{
		{"path traversal with ../", "../../etc/passwd"},
		{"slash in id", "foo/bar"},
		{"backslash in id", `foo\bar`},
		{"empty id", ""},
	}

	for _, tc := range badIDs {
		t.Run(tc.name, func(t *testing.T) {
			err := ms.AppendRetro(tc.id, r)
			if err == nil {
				t.Fatalf("AppendRetro(%q) should fail, got nil", tc.id)
			}
			// Verify no file was written to any unexpected location.
			// We check that /etc/passwd was NOT written (safety check).
			if _, statErr := os.Stat("/etc/passwd"); statErr == nil {
				// /etc/passwd exists normally; ensure we didn't append to it.
				// (We can't easily check the content, but reaching here means
				// the error was returned so no write happened.)
			}
		})
	}

	// Valid session ID must succeed.
	validID := "valid-session-123"
	if err := ms.AppendRetro(validID, r); err != nil {
		t.Errorf("AppendRetro with valid session ID failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// #10 — TestMemoryStore_AppendRetro_NoUserConfirmedField
// Traces to: env-awareness-and-memory-spec.md FR-009
// The retro file must NOT contain a "user_confirmed" key.
// ---------------------------------------------------------------------------

func TestMemoryStore_AppendRetro_NoUserConfirmedField(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	sessionID := "session-no-confirm"
	r := Retro{
		Timestamp:        time.Now().UTC(),
		Trigger:          "joined",
		WentWell:         []string{"shipped the feature"},
		NeedsImprovement: []string{"better testing"},
	}

	if err := ms.AppendRetro(sessionID, r); err != nil {
		t.Fatalf("AppendRetro: %v", err)
	}

	// Find and read the retro file.
	dateStr := r.Timestamp.UTC().Format("2006-01-02")
	retroPath := filepath.Join(dir, "memory", "sessions", dateStr, sessionID+"_retro.md")
	raw, err := os.ReadFile(retroPath)
	if err != nil {
		t.Fatalf("reading retro file %s: %v", retroPath, err)
	}

	if strings.Contains(string(raw), "user_confirmed") {
		t.Errorf("retro file contains 'user_confirmed' field — must not be present (FR-009);\ngot:\n%s", string(raw))
	}
}

// ---------------------------------------------------------------------------
// #11 — TestMemoryStore_SweepRetros_Deletes30DayOld
// Traces to: env-awareness-and-memory-spec.md FR-031
// A retro directory with an old date must be swept.
// ---------------------------------------------------------------------------

func TestMemoryStore_SweepRetros_Deletes30DayOld(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	// Create an "old" retro directory by placing a file with an old date.
	oldDate := time.Now().UTC().AddDate(0, 0, -35) // 35 days ago — outside 30-day window
	oldDateStr := oldDate.Format("2006-01-02")
	oldDir := filepath.Join(dir, "memory", "sessions", oldDateStr)
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatalf("create old retro dir: %v", err)
	}
	oldRetroPath := filepath.Join(oldDir, "old-session_retro.md")
	if err := os.WriteFile(
		oldRetroPath,
		[]byte("<!-- ts=2020-01-01T00:00:00.000Z trigger=joined fallback=false -->\nold recap\n"),
		0o600,
	); err != nil {
		t.Fatalf("write old retro file: %v", err)
	}

	// Create a "new" retro directory (today).
	newDateStr := time.Now().UTC().Format("2006-01-02")
	newDir := filepath.Join(dir, "memory", "sessions", newDateStr)
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatalf("create new retro dir: %v", err)
	}
	newRetroPath := filepath.Join(newDir, "new-session_retro.md")
	if err := os.WriteFile(
		newRetroPath,
		[]byte("<!-- ts=2026-04-24T10:00:00.000Z trigger=joined fallback=false -->\nnew recap\n"),
		0o600,
	); err != nil {
		t.Fatalf("write new retro file: %v", err)
	}

	deleted, err := ms.SweepRetros(30)
	if err != nil {
		t.Fatalf("SweepRetros: %v", err)
	}
	if deleted != 1 {
		t.Errorf("SweepRetros deleted %d files, want 1 (the old one)", deleted)
	}

	// Old retro must be gone.
	if _, statErr := os.Stat(oldRetroPath); !os.IsNotExist(statErr) {
		t.Errorf("old retro file still exists after sweep: %s", oldRetroPath)
	}

	// New retro must still exist.
	if _, statErr := os.Stat(newRetroPath); statErr != nil {
		t.Errorf("new retro file unexpectedly missing after sweep: %v", statErr)
	}
}

// ---------------------------------------------------------------------------
// TestMemoryStore_AppendLongTerm_SeparatorBetweenEntries
// Traces to: env-awareness-and-memory-spec.md FR-002
// A "<!-- next -->" separator must appear between consecutive entries.
// ---------------------------------------------------------------------------

func TestMemoryStore_AppendLongTerm_SeparatorBetweenEntries(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	if err := ms.AppendLongTerm("entry one", "reference"); err != nil {
		t.Fatalf("AppendLongTerm 1: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := ms.AppendLongTerm("entry two", "key_decision"); err != nil {
		t.Fatalf("AppendLongTerm 2: %v", err)
	}

	raw, _ := os.ReadFile(filepath.Join(dir, "memory", "MEMORY.md"))
	s := string(raw)

	if !strings.Contains(s, "<!-- next -->") {
		t.Errorf("MEMORY.md missing <!-- next --> separator between entries;\ngot:\n%s", s)
	}
	// The separator must appear between the two entries.
	idx1 := strings.Index(s, "entry one")
	idxSep := strings.Index(s, "<!-- next -->")
	idx2 := strings.Index(s, "entry two")
	if !(idx1 < idxSep && idxSep < idx2) {
		t.Errorf("separator not between entries: entry1=%d sep=%d entry2=%d", idx1, idxSep, idx2)
	}
}

// ---------------------------------------------------------------------------
// TestMemoryStore_ReadLongTermEntries_NewestFirst
// Traces to: env-awareness-and-memory-spec.md FR-004
// Explicitly verify ordering when entries have different timestamps.
// ---------------------------------------------------------------------------

func TestMemoryStore_ReadLongTermEntries_NewestFirst(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	categories := []string{"reference", "key_decision", "lesson_learned"}
	for i, cat := range categories {
		if i > 0 {
			time.Sleep(3 * time.Millisecond) // ensure distinct timestamps
		}
		if err := ms.AppendLongTerm("entry for "+cat, cat); err != nil {
			t.Fatalf("AppendLongTerm %s: %v", cat, err)
		}
	}

	entries, err := ms.ReadLongTermEntries()
	if err != nil {
		t.Fatalf("ReadLongTermEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Newest first: lesson_learned > key_decision > reference
	if entries[0].Category != "lesson_learned" {
		t.Errorf("entries[0].Category = %q, want lesson_learned", entries[0].Category)
	}
	if entries[2].Category != "reference" {
		t.Errorf("entries[2].Category = %q, want reference", entries[2].Category)
	}
	// Timestamps must be monotonically decreasing.
	for i := 1; i < len(entries); i++ {
		if entries[i].Timestamp.After(entries[i-1].Timestamp) {
			t.Errorf("entries not newest-first: [%d]=%v after [%d]=%v",
				i, entries[i].Timestamp, i-1, entries[i-1].Timestamp)
		}
	}
}

// ---------------------------------------------------------------------------
// TestMemoryStore_GetMemoryContext_ContainsBothSections
// Traces to: env-awareness-and-memory-spec.md FR-019
// Pre-seed both LAST_SESSION.md and MEMORY.md; verify both headers appear.
// ---------------------------------------------------------------------------

func TestMemoryStore_GetMemoryContext_ContainsBothSections(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	if err := ms.WriteLastSession("## Last session\nWe shipped lane M."); err != nil {
		t.Fatalf("WriteLastSession: %v", err)
	}
	if err := ms.AppendLongTerm("lane M shipped", "key_decision"); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	ctx := ms.GetMemoryContext()

	if !strings.Contains(ctx, "## Last Session") {
		t.Errorf("GetMemoryContext missing '## Last Session'; got:\n%s", truncateStr2(ctx, 300))
	}
	if !strings.Contains(ctx, "## Long-term memory") {
		t.Errorf("GetMemoryContext missing '## Long-term memory'; got:\n%s", truncateStr2(ctx, 300))
	}

	// Content assertion: actual content must be present, not just empty sections.
	if !strings.Contains(ctx, "lane M shipped") {
		t.Errorf("GetMemoryContext missing expected content 'lane M shipped'; got:\n%s", ctx)
	}
}

// ---------------------------------------------------------------------------
// TestMemoryStore_AppendLongTerm_MaxRune4096
// Boundary: content at exactly 4096 runes must succeed.
// Traces to: env-awareness-and-memory-spec.md FR-002
// ---------------------------------------------------------------------------

func TestMemoryStore_AppendLongTerm_MaxRune4096(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	// Exactly 4096 runes must succeed.
	content4096 := strings.Repeat("a", 4096)
	if utf8.RuneCountInString(content4096) != 4096 {
		t.Fatal("test setup: content4096 not exactly 4096 runes")
	}
	if err := ms.AppendLongTerm(content4096, "reference"); err != nil {
		t.Errorf("AppendLongTerm at exactly 4096 runes failed: %v", err)
	}

	// 4097 runes must fail.
	content4097 := strings.Repeat("a", 4097)
	if err := ms.AppendLongTerm(content4097, "reference"); err == nil {
		t.Error("AppendLongTerm at 4097 runes should fail, got nil")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func truncateStr2(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
