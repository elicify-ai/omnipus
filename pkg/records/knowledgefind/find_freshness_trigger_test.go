// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Finding 2 (false-incompleteness on benign divergence): the A2(d) non-zero
// freshness downgrade fired on `!Fresh || PendingFiles>0 || IndexedFiles<Scanned`.
// Two of those disjuncts flag states that do NOT under-report:
//
//   (a) a mtime-only touch (git checkout / rsync / backup restore) reads as
//       Changed>0 → PendingFiles>0 → !Fresh, so an otherwise-complete answer was
//       downgraded to complete:false with the alarming "has not finished
//       indexing this vault" — on a vault that is fully indexed.
//   (c) an unreadable/permanently-skipped file is on disk but never in the
//       index, so IndexedFiles<ScannedFiles is permanently true → every search
//       is incomplete forever.
//
// The signal must fire only on GENUINE under-reporting: NewFiles>0 (matching
// files on disk that are not in the index at all). Changed and Removed do not
// under-report.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 \
//   -run '^TestFind_Freshness_' ./pkg/records/knowledgefind/

package knowledgefind

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// hasIndexUnavailable reports whether any recorded problem is the A2(d)
// coverage/freshness warning.
func hasIndexUnavailable(problems []generated.RecordProblem) bool {
	for _, p := range problems {
		if p.Code == generated.IndexUnavailable {
			return true
		}
	}
	return false
}

// (a) A benign mtime touch — the file is still indexed, just its stat moved —
// must NOT downgrade an otherwise-complete answer.
func TestFind_Freshness_BenignMtimeTouch_StaysComplete(t *testing.T) {
	f := gardenCorpus(t)
	f.text.only = []string{"garden/plants/PL-0002.md"}
	// The whole vault IS indexed (Indexed == Scanned, New == 0). One file's
	// mtime moved with no content change, so it reads as Changed and Pending>0,
	// Fresh=false — the exact shape a git checkout leaves behind.
	f.text.fresh = &TextIndexFreshness{
		Built: true, Fresh: false,
		ScannedFiles: 4, IndexedFiles: 4, PendingFiles: 1,
		NewFiles: 0, ChangedFiles: 1, RemovedFiles: 0,
	}

	resp := mustFind(t, f.deps(), req(withType("plant"), withWords("Fern")))

	if len(resp.Rows) != 1 {
		t.Fatalf("expected the indexed hit, got %d rows", len(resp.Rows))
	}
	if hasIndexUnavailable(resp.Problems) {
		t.Fatalf("a mtime-only touch (Changed=1, New=0) must NOT emit the freshness/coverage warning: %+v", resp.Problems)
	}
	if !resp.Complete {
		t.Fatalf("a fully-indexed vault with only a benign mtime touch must stay complete:true, got reason %q", str2(resp.CompleteReason))
	}
}

// (b) A genuinely partial/lagging index — matching files on disk not indexed at
// all — MUST still downgrade with the message. This is the case A2(d) exists for.
func TestFind_Freshness_GenuinePartialIndex_IsIncomplete(t *testing.T) {
	f := gardenCorpus(t)
	f.text.only = []string{"garden/plants/PL-0002.md"}
	f.text.fresh = &TextIndexFreshness{
		Built: true, Fresh: false,
		ScannedFiles: 4, IndexedFiles: 1, PendingFiles: 3,
		NewFiles: 3, ChangedFiles: 0, RemovedFiles: 0,
	}

	resp := mustFind(t, f.deps(), req(withType("plant"), withWords("Fern")))

	if resp.Complete {
		t.Fatalf("a genuinely partial index (3 matching-eligible files unindexed) must be complete:false")
	}
	var found bool
	for _, p := range resp.Problems {
		if p.Code == generated.IndexUnavailable &&
			strings.Contains(p.Reason, "1 of") && strings.Contains(p.Reason, "4") &&
			strings.Contains(p.Reason, "3") && strings.Contains(p.Reason, "under-report") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no freshness/coverage problem quoting the counts (1 of 4, 3 not yet indexed, may under-report): %+v", resp.Problems)
	}
}

// (c) An unreadable/permanently-skipped file alone leaves IndexedFiles<ScannedFiles
// but NewFiles==0 (it is not "not yet indexed" — it cannot be indexed). The
// search must NOT be reported incomplete forever.
func TestFind_Freshness_UnreadableFileAlone_NotPermanentlyIncomplete(t *testing.T) {
	f := gardenCorpus(t)
	f.text.only = []string{"garden/plants/PL-0002.md"}
	// 4 files on disk, 3 indexed, 1 unreadable and excluded from New by the
	// index's own accounting. Nothing is genuinely pending.
	f.text.fresh = &TextIndexFreshness{
		Built: true, Fresh: true,
		ScannedFiles: 4, IndexedFiles: 3, PendingFiles: 0,
		NewFiles: 0, ChangedFiles: 0, RemovedFiles: 0,
	}

	resp := mustFind(t, f.deps(), req(withType("plant"), withWords("Fern")))

	if hasIndexUnavailable(resp.Problems) {
		t.Fatalf("an unreadable file alone (Indexed<Scanned but New=0) must NOT make the search incomplete: %+v", resp.Problems)
	}
	if !resp.Complete {
		t.Fatalf("an unreadable file alone must not permanently downgrade to complete:false, got reason %q", str2(resp.CompleteReason))
	}
}
