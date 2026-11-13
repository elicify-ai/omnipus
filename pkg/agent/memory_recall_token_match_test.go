package agent

import (
	"strings"
	"testing"
)

// #1799 pin: the room index matches on shared words, not on a substring of the
// stored text. The recall_memory tool text describes exactly this behaviour, so
// if the behaviour ever changes to a true substring match this test fails and
// the tool text (pkg/tools/memory_recall_query_text_test.go) must change with it.
func TestMemoryStore_SearchEntries_MatchesSharedWordNotSubstring(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	defer ms.Close()

	if err := ms.AppendLongTerm("MEMSCRIPT-mwd1bfrg is the marker for the scripted test", "reference"); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	// Not a substring of the stored text, but shares the word MEMSCRIPT.
	query := "MEMSCRIPT-nevermwd1bfrg"
	if strings.Contains(strings.ToLower("MEMSCRIPT-mwd1bfrg is the marker for the scripted test"), strings.ToLower(query)) {
		t.Fatalf("test setup wrong: %q is a substring of the stored text", query)
	}
	got, err := ms.SearchEntries(query, 10)
	if err != nil {
		t.Fatalf("SearchEntries(%q): %v", query, err)
	}
	if len(got) != 1 {
		t.Fatalf("SearchEntries(%q) = %d entries, want 1 (shared word match)", query, len(got))
	}

	// Control: a query sharing no word with the stored text finds nothing.
	none, err := ms.SearchEntries("UNRELATEDZZZ-qqq9", 10)
	if err != nil {
		t.Fatalf("SearchEntries(control): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("SearchEntries(control) = %d entries, want 0", len(none))
	}
}
