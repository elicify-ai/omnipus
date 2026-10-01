//go:build goolm && stdjson

package agent

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Surviving-mutant coverage gaps from the CHECK of 939989605 (#1081): the
// suite asserted the breadcrumb's range address, pointer association and cap
// size, but never the rendered ENTRY ORDER (M06 oldest-first), never the
// staged-Skip-vs-snapshot-Skip choice in breadcrumbForWindow (M07), and never
// that entries retained under cap pressure actually survive (M08). Expected
// values below derive from the spec (docs/internal/specs/
// adr-066-context-overflow-spec.md, amendment 2026-09-30: B-58 real inclusive
// evicted range, FR-030 staged Skip, FR-032 rebuilt breadcrumbs) and the
// renderer's own doc comments — not from observed output. Entry framing is
// deliberately not asserted as prose (the repo convention for this renderer is
// address/pointer/cap assertions); order, identity, count and accounting are.

func archiveGapToolMsg(id, content string) memory.ArchivedMessage {
	return memory.ArchivedMessage{Message: providers.Message{Role: "tool", ToolCallID: id, Content: content}}
}

func archiveGapAssistantMsg(content string) memory.ArchivedMessage {
	return memory.ArchivedMessage{Message: providers.Message{Role: "assistant", Content: content}}
}

// crumbEntryLines returns the rendered entry lines in output order. Entry
// lines are the only ones beginning "- "; header and omitted-marker lines are
// not entries.
func crumbEntryLines(crumb string) []string {
	var lines []string
	for _, line := range strings.Split(crumb, "\n") {
		if strings.HasPrefix(line, "- ") {
			lines = append(lines, line)
		}
	}
	return lines
}

var crumbArchiveLineRe = regexp.MustCompile(`archive_line=(\d+)`)

// crumbLineOrder returns the archive_line addresses of the entry lines in
// rendered order. Every evicted record with a nonempty snippet is addressed by
// exactly one entry (MAJ-CW-003: recall marks are addressed by archive line),
// so this sequence is the render order of the evicted records.
func crumbLineOrder(t *testing.T, crumb string) []int {
	t.Helper()
	var order []int
	for _, line := range crumbEntryLines(crumb) {
		m := crumbArchiveLineRe.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("entry line without an archive_line address: %q", line)
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("entry archive_line is not a number: %q", line)
		}
		order = append(order, n)
	}
	return order
}

// M06: the restored renderer must list the evicted entries NEWEST-FIRST, so a
// model reading the breadcrumb meets the nearest evicted context first. An
// oldest-first rendering addresses the same records and passes every
// range/pointer/cap assertion — only the order catches it.
func TestArchiveBreadcrumbEntriesRenderNewestFirst(t *testing.T) {
	archive := []memory.ArchivedMessage{
		archiveGapToolMsg("bc-ord-call-0", "order literal payload zero"),
		archiveGapAssistantMsg("assistant literal reply one"),
		archiveGapToolMsg("bc-ord-call-2", "order literal payload two"),
		archiveGapAssistantMsg("assistant literal reply three"),
		archiveGapToolMsg("bc-ord-call-4", "order literal payload four"),
		archiveGapAssistantMsg("assistant literal reply five"),
	}
	crumb := buildArchiveBreadcrumb(archive, len(archive))
	if want := "archive_range={from:0,to:5}"; !strings.Contains(crumb, want) {
		t.Fatalf("header must address the whole evicted prefix 0..5: want %q in %q", want, crumb)
	}
	want := []int{5, 4, 3, 2, 1, 0}
	if order := crumbLineOrder(t, crumb); !slices.Equal(order, want) {
		t.Fatalf("entries must render newest-first: want %v got %v", want, order)
	}
	if strings.Contains(crumb, "earlier ranges") {
		t.Fatalf("every entry fits the cap, so no omitted-range marker may appear: %q", crumb)
	}
}

// M07: relief stages a newer Skip than the snapshot's persisted one (FR-030);
// breadcrumbForWindow's contract is to render the STAGED prefix from the same
// snapshot, without rereading persisted metadata. Rendering snap.State.Skip
// instead addresses a prefix that was never evicted and omits the records the
// staged Skip did evict.
func TestBreadcrumbForWindowRendersStagedSkipNotSnapshotSkip(t *testing.T) {
	var archive []memory.ArchivedMessage
	for i := 0; i < 6; i++ {
		archive = append(archive,
			archiveGapToolMsg(fmt.Sprintf("bc-m07-call-%d", i), fmt.Sprintf("m07 literal payload %d", i)))
	}
	// The checkpoint staged Skip=6 while its snapshot still carries Skip=2.
	snap := memory.WindowSnapshot{State: memory.WindowState{Skip: 2}, Archive: archive}
	crumb := breadcrumbForWindow(snap, 6)
	if want := "archive_range={from:0,to:5}"; !strings.Contains(crumb, want) {
		t.Fatalf("staged Skip=6 must address evicted lines 0..5, not the snapshot Skip: want %q in %q", want, crumb)
	}
	want := []int{5, 4, 3, 2, 1, 0}
	if order := crumbLineOrder(t, crumb); !slices.Equal(order, want) {
		t.Fatalf("staged Skip=6 must render entries for all of lines 0..5: want %v got %v", want, order)
	}
	// archive_line=2 sits inside the snapshot's own live window (Skip=2);
	// only the staged Skip makes it evicted, so its pointer is the
	// discriminator between the staged and snapshot prefixes.
	cwAssertBreadcrumbPointer(t, crumb, "bc-m07-call-2", 2)
	cwAssertBreadcrumbPointer(t, crumb, "bc-m07-call-5", 5)
}

// M08: when cap pressure omits entries, the entries that still fit must
// survive — exactly the newest contiguous block, newest-first, with the
// omitted marker counting exactly the dropped remainder. Dropping the
// retained entries (or shortening the retained set) must fail here even
// though the range address and the omitted marker stay correct.
func TestArchiveBreadcrumbCapPressureRetainsNewestEntriesAndCountsOmitted(t *testing.T) {
	const entries = 40
	total := entries
	// Every tool record with a ToolCallID renders an addressed pointer entry,
	// whatever its snippet, so all 40 records here produce entries costing
	// exactly the same runes: ids are fixed width, every archive_line has two
	// digits and every snippet is the same 80-rune literal, making the
	// retained-count derivation below exact.
	snippet := "m08 literal payload | " + strings.Repeat("p", 58) // 80 runes: at truncateSnippet's limit, no truncation
	archive := make([]memory.ArchivedMessage, 0, total)
	for i := 0; i < total; i++ {
		archive = append(archive, archiveGapToolMsg(fmt.Sprintf("bc-m08-call-%02d", i), snippet))
	}
	// Derived budget, per the renderer's cap contract: the whole crumb fits
	// breadcrumbTokenCap*breadcrumbCharsPerToken runes, reserving the
	// worst-case header and omitted-marker the renderer reserves. This
	// arithmetic derives the expected retained count from the documented cap;
	// it is not read off rendered output.
	reserve := utf8.RuneCountInString(archiveBreadcrumbHeader(maxIntValue())) +
		utf8.RuneCountInString(fmt.Sprintf("\n+%d earlier ranges", maxIntValue()))
	budget := breadcrumbTokenCap*breadcrumbCharsPerToken - reserve
	perEntry := utf8.RuneCountInString(fmt.Sprintf("- tool_call_id=%q, archive_line=%d · %q", "bc-m08-call-39", 39, snippet)) + 1
	kept := 0
	for used := 0; used+perEntry <= budget; used += perEntry {
		kept++
	}
	if kept < 2 || kept >= entries {
		t.Fatalf("fixture must exercise cap pressure (some entries retained, some omitted): kept=%d perEntry=%d budget=%d", kept, perEntry, budget)
	}
	crumb := buildArchiveBreadcrumb(archive, total)
	if want := fmt.Sprintf("archive_range={from:0,to:%d}", total-1); !strings.Contains(crumb, want) {
		t.Fatalf("cap pressure must not hide the real evicted range: want %q in %q", want, crumb)
	}
	wantOrder := make([]int, 0, kept)
	for line := total - 1; len(wantOrder) < kept; line-- {
		wantOrder = append(wantOrder, line)
	}
	if order := crumbLineOrder(t, crumb); !slices.Equal(order, wantOrder) {
		t.Fatalf("cap pressure must retain exactly the newest %d entries, contiguous and newest-first: want %v got %v", kept, wantOrder, order)
	}
	if want := fmt.Sprintf("+%d earlier ranges", entries-kept); !strings.Contains(crumb, want) {
		t.Fatalf("omitted accounting must count exactly the %d dropped entries: want %q in %q", entries-kept, want, crumb)
	}
	if n := utf8.RuneCountInString(crumb); n > breadcrumbTokenCap*breadcrumbCharsPerToken {
		t.Fatalf("breadcrumb exceeds the cap contract: %d > %d", n, breadcrumbTokenCap*breadcrumbCharsPerToken)
	}
}

// Boundary: skip=0 evicts nothing, so no breadcrumb prefix may be fabricated
// (the restored renderer's zero-skip contract; the range recall address must
// be a real evicted prefix).
func TestArchiveBreadcrumbZeroSkipRendersNothing(t *testing.T) {
	archive := []memory.ArchivedMessage{archiveGapToolMsg("bc-zero-call-0", "unused literal payload")}
	if crumb := buildArchiveBreadcrumb(archive, 0); crumb != "" {
		t.Fatalf("skip=0 evicts nothing, so no breadcrumb prefix may be fabricated: %q", crumb)
	}
	snap := memory.WindowSnapshot{State: memory.WindowState{Skip: 0}, Archive: archive}
	if crumb := breadcrumbForWindow(snap, 0); crumb != "" {
		t.Fatalf("staged skip=0 evicts nothing, so no breadcrumb prefix may be fabricated: %q", crumb)
	}
}
