// memory_behavioral_test.go — Spec-5 behavioral tests for room-based MemoryStore.
// GREENFIELD: tests for the new per-memory .md file format and room topology.
// Old MEMORY.md-format tests replaced per FR-7.6 / operator D2 greenfield decision.
//
// Traces to: Spec-5 FR-7.1 (room routing), FR-7.2 (full frontmatter),
// FR-7.3 (3 tools), FR-7.6 (greenfield).

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dapicom-ai/omnipus/pkg/memrooms"
)

// ---------------------------------------------------------------------------
// TestMemoryStore_AppendLongTerm_WritesMemoryFile
// Verifies that AppendLongTerm creates a per-memory .md file with correct type.
// Traces to: FR-7.2
// ---------------------------------------------------------------------------

func TestMemoryStore_AppendLongTerm_WritesMemoryFile(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)

	if err := ms.AppendLongTerm("always use flock for concurrent writes", "lesson_learned"); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	memoriesDir := filepath.Join(dir, memrooms.OmnipusRoomDir, memrooms.MemoriesSubdir)
	entries, err := os.ReadDir(memoriesDir)
	if err != nil {
		t.Fatalf("read memories dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 memory file, got %d", len(entries))
	}

	data, err := os.ReadFile(filepath.Join(memoriesDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read memory file: %v", err)
	}
	s := string(data)

	// Must contain lesson type (maps from lesson_learned).
	if !strings.Contains(s, "type: lesson") {
		t.Errorf("memory file missing type:lesson; got:\n%s", s)
	}
	// Content must be in the body.
	if !strings.Contains(s, "always use flock") {
		t.Errorf("memory file missing content; got:\n%s", s)
	}

	// Second write — creates a second distinct file.
	time.Sleep(2 * time.Millisecond)
	if err := ms.AppendLongTerm("second entry", "key_decision"); err != nil {
		t.Fatalf("AppendLongTerm second: %v", err)
	}
	entries2, _ := os.ReadDir(memoriesDir)
	if len(entries2) != 2 {
		t.Errorf("expected 2 memory files after second write, got %d", len(entries2))
	}
}

// ---------------------------------------------------------------------------
// TestMemoryStore_AppendLongTerm_StripsNull_RejectsCommentInjection
// Traces to: FR-7.2 (content validation carried forward)
// ---------------------------------------------------------------------------

func TestMemoryStore_AppendLongTerm_StripsNull_RejectsCommentInjection(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)

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
}

// ---------------------------------------------------------------------------
// TestMemoryStore_SearchEntries_LiteralSubstring
// Verifies literal substring search (no regex expansion).
// Traces to: FR-7.3 (recall returns matching entries)
// ---------------------------------------------------------------------------

func TestMemoryStore_SearchEntries_LiteralSubstring(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)

	if err := ms.AppendLongTerm("alpha entry one", "reference"); err != nil {
		t.Fatalf("AppendLongTerm 1: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := ms.AppendLongTerm("alpha entry two", "key_decision"); err != nil {
		t.Fatalf("AppendLongTerm 2: %v", err)
	}

	// ".*" is a regex metachar — must match nothing (literal match only).
	results, err := ms.SearchEntries(".*", 10)
	if err != nil {
		t.Fatalf("SearchEntries regex metachar: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("SearchEntries('.*') should return 0 (literal match), got %d", len(results))
	}

	// Plain literal match must work.
	results2, err := ms.SearchEntries("alpha entry", 10)
	if err != nil {
		t.Fatalf("SearchEntries literal: %v", err)
	}
	if len(results2) != 2 {
		t.Fatalf("SearchEntries('alpha entry'): expected 2, got %d", len(results2))
	}
}

// ---------------------------------------------------------------------------
// TestMemoryStore_WriteLastSession_OverwritesAtomically
// Traces to: room topology (last-session.md per room)
// ---------------------------------------------------------------------------

func TestMemoryStore_WriteLastSession_OverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)

	if err := ms.WriteLastSession("initial content"); err != nil {
		t.Fatalf("WriteLastSession initial: %v", err)
	}

	newContent := "## Updated last session\nThis overwrites the previous content."
	if err := ms.WriteLastSession(newContent); err != nil {
		t.Fatalf("WriteLastSession overwrite: %v", err)
	}

	got, err := ms.ReadLastSession()
	if err != nil {
		t.Fatalf("ReadLastSession: %v", err)
	}
	if got != newContent {
		t.Errorf("ReadLastSession got %q, want %q", got, newContent)
	}
	if strings.Contains(got, "initial content") {
		t.Error("last-session.md still contains old content after overwrite")
	}

	differentContent := "## Session 2\nA different session."
	if err := ms.WriteLastSession(differentContent); err != nil {
		t.Fatalf("WriteLastSession differentContent: %v", err)
	}
	got2, _ := ms.ReadLastSession()
	if got2 == got {
		t.Error("WriteLastSession: two different writes produced the same read")
	}
}

// ---------------------------------------------------------------------------
// TestMemoryStore_AppendRetro_ValidatesSessionID
// Traces to: session ID validation (carried forward from original)
// ---------------------------------------------------------------------------

func TestMemoryStore_AppendRetro_ValidatesSessionID(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)

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
		})
	}

	// Valid session ID must succeed.
	if err := ms.AppendRetro("valid-session-123", r); err != nil {
		t.Errorf("AppendRetro with valid session ID failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestMemoryStore_AppendRetro_NoUserConfirmedField
// Traces to: retro format (no user_confirmed field)
// ---------------------------------------------------------------------------

func TestMemoryStore_AppendRetro_NoUserConfirmedField(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)

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

	// Retro lives in <workspace>/.omnipus/retros/<date>/<sessionID>_retro.md
	dateStr := r.Timestamp.UTC().Format("2006-01-02")
	retroPath := filepath.Join(dir, memrooms.OmnipusRoomDir, "retros", dateStr, sessionID+"_retro.md")
	raw, err := os.ReadFile(retroPath)
	if err != nil {
		t.Fatalf("reading retro file %s: %v", retroPath, err)
	}

	if strings.Contains(string(raw), "user_confirmed") {
		t.Errorf("retro file contains 'user_confirmed' — must not be present;\ngot:\n%s", string(raw))
	}
}

// ---------------------------------------------------------------------------
// TestMemoryStore_SweepRetros_Deletes30DayOld
// Traces to: retro retention sweep
// ---------------------------------------------------------------------------

func TestMemoryStore_SweepRetros_Deletes30DayOld(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)

	// Create an "old" retro directory.
	oldDate := time.Now().UTC().AddDate(0, 0, -35)
	oldDateStr := oldDate.Format("2006-01-02")
	oldDir := filepath.Join(dir, memrooms.OmnipusRoomDir, "retros", oldDateStr)
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatalf("create old retro dir: %v", err)
	}
	oldRetroPath := filepath.Join(oldDir, "old-session_retro.md")
	if err := os.WriteFile(oldRetroPath,
		[]byte("<!-- ts=2020-01-01T00:00:00.000Z trigger=joined fallback=false -->\nold recap\n"),
		0o600); err != nil {
		t.Fatalf("write old retro file: %v", err)
	}

	// Create a "new" retro directory (today).
	newDateStr := time.Now().UTC().Format("2006-01-02")
	newDir := filepath.Join(dir, memrooms.OmnipusRoomDir, "retros", newDateStr)
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatalf("create new retro dir: %v", err)
	}
	newRetroPath := filepath.Join(newDir, "new-session_retro.md")
	if err := os.WriteFile(newRetroPath,
		[]byte("<!-- ts=2026-04-24T10:00:00.000Z trigger=joined fallback=false -->\nnew recap\n"),
		0o600); err != nil {
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
// TestMemoryStore_GetMemoryContext_ContainsBothSections
// Traces to: FR-7.3 (GetMemoryContext used by system prompt)
// ---------------------------------------------------------------------------

func TestMemoryStore_GetMemoryContext_ContainsBothSections(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)

	if err := ms.WriteLastSession("## Last session\nWe shipped the rooms."); err != nil {
		t.Fatalf("WriteLastSession: %v", err)
	}
	if err := ms.AppendLongTerm("rooms keystone shipped", "key_decision"); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	ctx := ms.GetMemoryContext()

	if !strings.Contains(ctx, "## Last Session") {
		t.Errorf("GetMemoryContext missing '## Last Session'; got:\n%s", truncateStr2(ctx, 300))
	}
	if !strings.Contains(ctx, "## Long-term memory") {
		t.Errorf("GetMemoryContext missing '## Long-term memory'; got:\n%s", truncateStr2(ctx, 300))
	}
	if !strings.Contains(ctx, "rooms keystone shipped") {
		t.Errorf("GetMemoryContext missing expected content; got:\n%s", ctx)
	}
}

// ---------------------------------------------------------------------------
// TestMemoryStore_AppendLongTerm_MaxRune4096
// Boundary: content at exactly 4096 runes must succeed.
// ---------------------------------------------------------------------------

func TestMemoryStore_AppendLongTerm_MaxRune4096(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)

	content4096 := strings.Repeat("a", 4096)
	if utf8.RuneCountInString(content4096) != 4096 {
		t.Fatal("test setup: content4096 not exactly 4096 runes")
	}
	if err := ms.AppendLongTerm(content4096, "reference"); err != nil {
		t.Errorf("AppendLongTerm at exactly 4096 runes failed: %v", err)
	}

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
