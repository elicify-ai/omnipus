//go:build goolm && stdjson

package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// F1 archive-addressing RED pack, citation side (C context-window review
// finding F1). The breadcrumb the model receives cites decoded archive
// records — breadcrumb_archive.go::buildArchiveBreadcrumb iterates the
// decoded snapshot slice and cites its indices — and recall_conversation's
// Description tells the model to pass archive_range "only when you already
// have specific archive line numbers to address directly", i.e. exactly the
// numbers breadcrumbs and marks cite. This test drives the REAL snapshot
// breadcrumb and the REAL tool against a real store whose archive carries one
// truncated crash-write line, and requires the cited call to return the cited
// record — never a neighbouring physical line.
//
// Expected on pin 698b682357f817a482a234a630ecbe3300469996: the breadcrumb
// assertions pass (citations are already decoded-space; they pin the citation
// side of the contract), while the cited [3,3] call succeeds and returns the
// WRONG record (physical line 3, "question two" = decoded record 2) — the
// silent shift is the defect under repair.
//
// CI-ready; the narrow local RED run is deliberately taken in ./pkg/memory
// only (one-package rule); this package's proof runs in draft PR CI.

func TestRecallConversation_ArchiveRangeCorruptCitation(t *testing.T) {
	const key = "cw-corrupt-citation"
	lines := []string{
		`{"role":"user","content":"question one","ts":100}` + "\n",
		`{"role":"assistant","content":"answer one","ts":101}` + "\n",
		`{"role":"assistant","content":"answ` + "\n", // truncated crash write
		`{"role":"user","content":"question two","ts":200}` + "\n",
		`{"role":"assistant","content":"answer two","ts":201}` + "\n",
	}
	ctx := makeCtx(key)
	store, path := cwRangeStore(t, key, lines...)
	// Whole conversation evicted: Skip=Count=4 decoded records. This is the
	// state a model actually sees after a slide, when the breadcrumb cites
	// from:0,to:3 and per-entry archive_line numbers.
	if err := store.TruncateHistory(ctx, key, 0); err != nil {
		t.Fatalf("persist actual Skip: %v", err)
	}
	cwAssertPersistedSkip(t, path, 4, 4)

	// The citation side: render the breadcrumb exactly as the window
	// checkpoint does (window_relief.go::breadcrumbForWindow over the real
	// snapshot) and require decoded-space addresses.
	snap, err := store.SnapshotWindow(ctx, key)
	if err != nil {
		t.Fatalf("SnapshotWindow: %v", err)
	}
	if snap.State.Skip != 4 || snap.State.Count != 4 {
		t.Fatalf("snapshot window metadata = Skip:%d Count:%d, want 4/4 decoded records (the corrupt line consumes no index)", snap.State.Skip, snap.State.Count)
	}
	crumb := breadcrumbForWindow(snap, snap.State.Skip)
	cwAssertBreadcrumbRange(t, crumb, 0, 3)
	if !strings.Contains(crumb, "archive_line=3") {
		t.Fatalf("breadcrumb must cite decoded record 3 for the final assistant record, got: %q", crumb)
	}
	if !strings.Contains(crumb, "answer two") {
		t.Fatalf("breadcrumb must show the cited record's literal snippet, got: %q", crumb)
	}

	// The model-visible contract: the cited call returns the cited record.
	tool := NewRecallConversationTool(store, cwNewRangeSink(0))
	res := cwRangeExecute(t, tool, ctx, cwRangeArgs(3, 3))
	page := cwReadRangePage(t, res)
	cwAssertRangePage(t, page, lines[4], 0, utf8.RuneCountInString(lines[4]))

	var cited struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(page.payload), &cited); err != nil {
		t.Fatalf("cited page payload must be the archive's literal JSONL record: %v", err)
	}
	if cited.Role != "assistant" || cited.Content != "answer two" {
		t.Fatalf("the cited record (archive_line=3) must be {assistant, answer two}; got {%s, %q} — a neighbouring physical line was returned for the model's citation (F1 silent shift)", cited.Role, cited.Content)
	}
}
