//go:build goolm && stdjson

package agent

import (
	"context"
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
	entries := crumbEntryLines(crumb)
	order := make([]int, 0, len(entries))
	for _, line := range entries {
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
	crumb := crumbOf(t, archive, len(archive))
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
	crumb := crumbOf(t, archive, 6) // staged Skip, independent of any snapshot Skip
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
	// whatever its snippet. Ids are fixed width and every snippet is the same
	// 80-rune literal; archive_line renders two digits for lines 10..39 and
	// one digit for lines 0..9, so the two-digit line 39 used below is the
	// widest entry, and the retained newest block (39 downward — still inside
	// the two-digit range at this fixture size under the frozen cap) costs
	// exactly perEntry runes per entry, making the retained-count derivation
	// below exact for the retained set.
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
	crumb := crumbOf(t, archive, total)
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

// F1 (CHECK of 23180217d): the cap-pressure oracle above derives its allowed
// bound from breadcrumbTokenCap itself, so a widened production cap kept it
// green — the bound moved with the defect (mutant M09, 1000 → 1050, survived).
// MAJ-CW-003 requires the breadcrumb to fit "the existing breadcrumb cap": the
// cap as frozen in the pre-#1081 baseline 0a750f3c2 — breadcrumbTokenCap=1000
// and breadcrumbCharsPerToken=4, both unchanged since 3c3604301 — i.e. 4000
// runes. This guard pins that bound as a literal, independent of the
// production constants and helpers, so a cap-contract change fails here
// instead of silently re-deriving a larger allowance.
func TestArchiveBreadcrumbCapPressureStaysWithinFrozenBaselineCap(t *testing.T) {
	// Same fixture family as the cap-pressure test above: 40 same-cost tool
	// records, so the renderer is genuinely under pressure at 4000 runes.
	const entries = 40
	snippet := "m08 literal payload | " + strings.Repeat("p", 58) // 80 runes: at truncateSnippet's limit, no truncation
	archive := make([]memory.ArchivedMessage, 0, entries)
	for i := 0; i < entries; i++ {
		archive = append(archive, archiveGapToolMsg(fmt.Sprintf("bc-cap-call-%02d", i), snippet))
	}
	crumb := crumbOf(t, archive, entries)
	// The frozen bound itself: 1000 tokens x 4 chars/token at baseline
	// 0a750f3c2 (ADR-066 MAJ-CW-003, "within the existing breadcrumb cap").
	// Deliberately not breadcrumbTokenCap*breadcrumbCharsPerToken — the point
	// of this guard is that the bound does not move with those constants.
	const frozenBoundRunes = 4000
	if n := utf8.RuneCountInString(crumb); n > frozenBoundRunes {
		t.Fatalf("breadcrumb exceeds the frozen existing-cap contract (baseline 0a750f3c2: 1000 tokens x 4 chars/token = %d runes): got %d runes", frozenBoundRunes, n)
	}
	// The bound must be exercised, not vacuous: at 4000 runes the fixture
	// cannot fit whole, so the omitted marker must be present.
	if !strings.Contains(crumb, "earlier ranges") {
		t.Fatalf("fixture must be under real cap pressure at the frozen %d-rune bound; no omitted marker in %q", frozenBoundRunes, crumb)
	}
	// And the pressure must retain the newest record, not satisfy the bound
	// by dropping the useful entries (MAJ-CW-003 retention semantics).
	if !strings.Contains(crumb, "archive_line=39") {
		t.Fatalf("newest record (archive_line=39) must be retained under cap pressure: %q", crumb)
	}
}

// Boundary: skip=0 evicts nothing, so no breadcrumb prefix may be fabricated
// (the restored renderer's zero-skip contract; the range recall address must
// be a real evicted prefix).
func TestArchiveBreadcrumbZeroSkipRendersNothing(t *testing.T) {
	archive := []memory.ArchivedMessage{archiveGapToolMsg("bc-zero-call-0", "unused literal payload")}
	if crumb := crumbOf(t, archive, 0); crumb != "" {
		t.Fatalf("skip=0 evicts nothing, so no breadcrumb prefix may be fabricated: %q", crumb)
	}
	if crumb := crumbOf(t, archive, 0); crumb != "" {
		t.Fatalf("staged skip=0 evicts nothing, so no breadcrumb prefix may be fabricated: %q", crumb)
	}
}

// crumbOf renders the breadcrumb for a dense archive prefix through the indexed
// slot reader (the renderer no longer takes a lifetime slice).
func crumbOf(t *testing.T, archive []memory.ArchivedMessage, skip int) string {
	t.Helper()
	crumb, err := buildArchiveBreadcrumb(context.Background(), denseArchive(archive), skip)
	if err != nil {
		t.Fatalf("buildArchiveBreadcrumb: %v", err)
	}
	return crumb
}

// cwAssertBreadcrumbPointer asserts the breadcrumb renders an exact addressed
// pointer (tool_call_id AND archive_line together) for callID at line. It moved
// here when recall_archive_range_breadcrumb_test.go's raw-JSONL fixture was
// deleted (session-core DEL-10).
func cwAssertBreadcrumbPointer(t *testing.T, text, callID string, line int) {
	t.Helper()
	normalized := strings.ReplaceAll(text, "\"", "")
	id := `tool_call_id\s*[:=]\s*` + regexp.QuoteMeta(callID) + `\b`
	address := `archive_line\s*[:=]\s*` + strconv.Itoa(line) + `\b`
	forward := regexp.MustCompile(id + `[^\n]{0,100}` + address)
	reverse := regexp.MustCompile(address + `[^\n]{0,100}` + id)
	if !forward.MatchString(normalized) && !reverse.MatchString(normalized) {
		t.Fatalf("breadcrumb must include exact addressed pointer (tool_call_id=%s, archive_line=%d): %q", callID, line, text)
	}
}
