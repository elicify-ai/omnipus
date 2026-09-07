// Omnipus — regression tests for the D3 integrity cursor paging (Issue 8).
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Two confirmed code-review findings live here:
//
//   Finding 3 — stateless paging assumed a stable finding order across re-runs.
//     Each cursor request re-runs the whole sweep and slices Findings[offset:].
//     Categories whose order came from the store's implicit row order (orphan
//     rows, relations) could re-order between the page-1 request and a later
//     cursor request, so the offset landed on a DIFFERENT finding — silently
//     skipping or duplicating findings while the count line claimed a clean
//     range. The fix is a deterministic total order over every category's
//     findings before paging.
//
//   Finding 4 — an offset at or beyond a category's finding count printed a
//     reversed "26-25 of 25 — end" range instead of a clean past-end message.

package knowledge

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// pageThroughIntegrityCategory walks one category the way a caller actually
// does: render page 1, follow the "next page: cursor=" it hands back, render
// that, and so on until a page offers no further cursor. It collects the
// finding-detail lines in the order they are paged, so the returned slice is
// the exact sequence a reader would see across pages — the thing Finding 3 is
// about. It goes through renderIntegrity (parse + page), not a private slice.
func pageThroughIntegrityCategory(t *testing.T, r *IntegrityReport, cat string) []string {
	t.Helper()
	var details []string
	cursor := cat + integrityCursorSep + "0"
	visited := map[string]bool{}
	for {
		if visited[cursor] {
			t.Fatalf("paging looped on cursor %q — the cursor is not advancing", cursor)
		}
		visited[cursor] = true

		var b strings.Builder
		renderIntegrity(&b, r, cursor)
		out := b.String()

		for _, line := range strings.Split(out, "\n") {
			if !strings.HasPrefix(line, "  ") {
				continue
			}
			body := line[2:]
			if !strings.HasPrefix(body, cat) {
				continue
			}
			rest := body[len(cat):]
			if strings.HasPrefix(rest, ":") {
				// A "<cat>: ..." status or overflow line, not a finding.
				continue
			}
			if d := strings.TrimSpace(rest); d != "" {
				details = append(details, d)
			}
		}

		next := ""
		if i := strings.Index(out, "next page: cursor="); i >= 0 {
			frag := out[i+len("next page: cursor="):]
			if nl := strings.IndexByte(frag, '\n'); nl >= 0 {
				frag = frag[:nl]
			}
			next = strings.TrimSpace(frag)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return details
}

// orphanRowReport builds a report whose only findings are orphan rows, one per
// record in the given order. Orphan rows are added in the store's scan order
// (integrity.go's ScanRecords loop), so the record slice order IS the raw
// finding order — which is exactly the SQLite-implicit-order dependency Finding
// 3 is about, reproduced with a fake index instead of a database.
func orphanRowReport(t *testing.T, records []IndexedRecord) *IntegrityReport {
	t.Helper()
	root := t.TempDir()
	schemas := integrityFixtureSchemas(t, root)
	report, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS: OSLinkFS(), Root: mustCollectionRoot(t, root),
		CollectionName: "notes", Schemas: schemas,
		Store: &fakePropertyIndex{records: records},
	})
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	return report
}

// TestIntegrityCursor_PagingIsStableAcrossReorderedStream is Finding 3's
// reproduction: the SAME set of findings, presented to the sweep in two
// different stream orders, must page into the IDENTICAL sequence — no skip, no
// duplicate — because a later cursor request re-runs the sweep and must not
// depend on the store handing rows back in the same order it did on page 1.
func TestIntegrityCursor_PagingIsStableAcrossReorderedStream(t *testing.T) {
	// More than two pages so the cursor genuinely crosses page boundaries.
	const n = 45
	if n <= 2*integrityFindingsPageSize {
		t.Fatalf("fixture must span more than two pages (%d), got %d", 2*integrityFindingsPageSize, n)
	}

	forward := make([]IndexedRecord, 0, n)
	for i := 0; i < n; i++ {
		forward = append(forward, IndexedRecord{
			// A path no note exists at → an orphan row. Zero-padded so the
			// deterministic Path order is unambiguous to a human reader too.
			Path:       fmt.Sprintf("Widgets/orphan-%02d.md", i),
			RecordType: "widget",
			RecordID:   fmt.Sprintf("WI-%04d", i),
		})
	}
	reversed := make([]IndexedRecord, n)
	for i, r := range forward {
		reversed[n-1-i] = r
	}

	cat := string(CategoryOrphanRow)
	seqA := pageThroughIntegrityCategory(t, orphanRowReport(t, forward), cat)
	seqB := pageThroughIntegrityCategory(t, orphanRowReport(t, reversed), cat)

	if len(seqA) != n {
		t.Fatalf("run A paged %d orphan-row findings, want %d (skip or dup across pages)", len(seqA), n)
	}
	if len(seqB) != n {
		t.Fatalf("run B paged %d orphan-row findings, want %d (skip or dup across pages)", len(seqB), n)
	}

	// No duplicates within a run.
	seen := map[string]bool{}
	for _, d := range seqA {
		if seen[d] {
			t.Fatalf("run A paged the same finding twice: %q", d)
		}
		seen[d] = true
	}

	// The two runs must be byte-for-byte the same sequence. Without a
	// deterministic order this fails: reversed input pages in reversed order.
	for i := range seqA {
		if seqA[i] != seqB[i] {
			t.Fatalf("paged sequence differs at position %d across two runs of the same findings:\n  A=%q\n  B=%q\n"+
				"paging must not depend on the store's row order", i, seqA[i], seqB[i])
		}
	}
}

// reportWithFindingCount builds a report whose single category holds exactly
// count retained findings (Total == count, nothing dropped), for exercising the
// paging renderer directly at chosen offsets.
func reportWithFindingCount(cat IntegrityCategory, count int) *IntegrityReport {
	c := &CategoryResult{Category: cat, Total: count}
	for i := 0; i < count; i++ {
		c.Findings = append(c.Findings, IntegrityFinding{
			Category: cat,
			Path:     fmt.Sprintf("n%02d.md", i),
			Detail:   fmt.Sprintf("finding %02d detail", i),
		})
	}
	return &IntegrityReport{ScopeLabel: "test", NotesSwept: count, Categories: []*CategoryResult{c}}
}

// TestIntegrityCursor_OffsetPastEndIsCleanNotReversed is Finding 4's
// reproduction. parseIntegrityCursor accepts any non-negative offset and the
// renderer clamped it to len(Findings); at offset == count the status line then
// formatted offset+1..end as "26-25 of 25 — end" — a reversed, nonsensical
// range. An offset at or beyond the finding count names no page and must say so
// cleanly.
func TestIntegrityCursor_OffsetPastEndIsCleanNotReversed(t *testing.T) {
	const count = 25 // not a multiple of the page size, so offset==count is a genuine over-run
	cat := string(CategoryBrokenLink)

	for _, offset := range []int{count, count + 1, 100} {
		t.Run(fmt.Sprintf("offset=%d", offset), func(t *testing.T) {
			r := reportWithFindingCount(CategoryBrokenLink, count)
			var b strings.Builder
			renderIntegrity(&b, r, fmt.Sprintf("%s%s%d", cat, integrityCursorSep, offset))
			out := b.String()

			// The reversed range is the exact defect: "showing 26-25 of 25".
			reversed := fmt.Sprintf("showing %d-%d", offset+1, count)
			if strings.Contains(out, reversed) {
				t.Fatalf("offset %d past the end printed a reversed range %q:\n%s", offset, reversed, out)
			}
			// A past-end offset must never claim to be SHOWING findings at all.
			if strings.Contains(out, "showing ") {
				t.Fatalf("offset %d past the end must not print a showing-range at all:\n%s", offset, out)
			}
			// It must say, plainly, that this offset is past the end.
			if !strings.Contains(out, "past the end") {
				t.Fatalf("offset %d must print a clean past-end message; got:\n%s", offset, out)
			}
			// No finding lines are emitted at a past-end offset.
			for i := 0; i < count; i++ {
				if strings.Contains(out, fmt.Sprintf("finding %02d detail", i)) {
					t.Fatalf("offset %d past the end must emit no finding lines; leaked finding %d:\n%s", offset, i, out)
				}
			}
		})
	}
}

// TestIntegrityCursor_LastValidPageStillRenders guards against the past-end fix
// swallowing the genuine last page: the final page of a category (offset on the
// last page boundary) must still render its findings and an "end" line.
func TestIntegrityCursor_LastValidPageStillRenders(t *testing.T) {
	const count = 25
	cat := string(CategoryBrokenLink)
	// Last page boundary: for 25 findings and page size 20, offset 20 shows 21-25.
	lastOffset := (count / integrityFindingsPageSize) * integrityFindingsPageSize
	r := reportWithFindingCount(CategoryBrokenLink, count)
	var b strings.Builder
	renderIntegrity(&b, r, fmt.Sprintf("%s%s%d", cat, integrityCursorSep, lastOffset))
	out := b.String()
	want := fmt.Sprintf("showing %d-%d of %d — end", lastOffset+1, count, count)
	if !strings.Contains(out, want) {
		t.Fatalf("the genuine last page must still render %q; got:\n%s", want, out)
	}
	if !strings.Contains(out, fmt.Sprintf("finding %02d detail", count-1)) {
		t.Fatalf("the genuine last page must render its findings; got:\n%s", out)
	}
}
