// Omnipus — spec tests 92/93 and FR-021e/FR-131/FR-133/FR-134: the storage half
// of file metadata.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// taggedNote is a note that exercises every new projection at once: frontmatter
// tags, inline body tags, a plain link, an embed, a link inside a code fence,
// and a frontmatter wikilink that must NOT become a body link.
const taggedNoteSrc = `---
type: plant
id: PL-9001
species: Monstera deliciosa
condition: growing
bed: "[[Bed 3]]"
tags: [greenhouse, care/watering]
status: open
notes_count: 4
---

# Monstera

Tagged #indoor and #care/misting here. Not a tag: #2026, and https://x.example/a#anchor.

Linked to [[Bed 3]] and to [[Rosa|the keeper]].

Embedded: ![[photo.png]]

` + "```" + `
#not-a-tag-in-code and [[Not A Link]]
` + "```" + `

Inline ` + "`#also-not-a-tag`" + ` too.
`

func taggedNote(t *testing.T) NoteRows {
	t.Helper()
	return note(t, "garden/monstera.md", plantSchema(t), taggedNoteSrc)
}

// ---------------------------------------------------------------------------
// FR-131 — the three stat columns
// ---------------------------------------------------------------------------

// TestFileMeta_StatColumnsRoundTripThroughTheStore is the plainest statement of
// "the columns exist and are populated": what the indexer stat'ed comes back out
// of the candidate stream.
func TestFileMeta_StatColumnsRoundTripThroughTheStore(t *testing.T) {
	store, _ := openIndex(t, Options{})

	rows := taggedNote(t)
	when := time.Date(2026, 8, 30, 14, 3, 27, 123456789, time.UTC)
	born := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	rows.SetFileMeta(FileMeta{
		Known:        true,
		ModTime:      when,
		Size:         4096,
		BirthTime:    born,
		HasBirthTime: true,
	})
	mustUpsert(t, store, rows)

	got := collect(t, store, Selector{})
	if len(got) != 1 {
		t.Fatalf("expected one candidate, got %d", len(got))
	}
	meta := got[0].File

	if !meta.Known {
		t.Fatal("the stat metadata came back unknown for a note that was stat'ed; the three columns are not being written or not being read")
	}
	if !meta.ModTime.Equal(when) {
		t.Errorf("file.mtime round-tripped as %s, want %s", meta.ModTime, when)
	}
	if meta.Size != 4096 {
		t.Errorf("file.size round-tripped as %d, want 4096", meta.Size)
	}
	if !meta.HasBirthTime || !meta.BirthTime.Equal(born) {
		t.Errorf("file.ctime round-tripped as %s (has=%v), want %s", meta.BirthTime, meta.HasBirthTime, born)
	}
}

// TestFileMeta_AnUnstattedNoteIsAbsentNotEpoch is the failure this design
// refuses to have.
//
// A note the walk carried no stat for must read back ABSENT. Storing zero
// instead plants 1970-01-01 on it, and `sort: file.mtime desc` then returns a
// plausible, stable, WRONG ordering with every unstat'ed note at one end and no
// error anywhere.
func TestFileMeta_AnUnstattedNoteIsAbsentNotEpoch(t *testing.T) {
	store, _ := openIndex(t, Options{})
	mustUpsert(t, store, taggedNote(t)) // no SetFileMeta call at all

	got := collect(t, store, Selector{})
	if len(got) != 1 {
		t.Fatalf("expected one candidate, got %d", len(got))
	}
	meta := got[0].File
	if meta.Known {
		t.Errorf("an unstat'ed note came back with KNOWN metadata %+v; absence was written as a value", meta)
	}
	if !meta.ModTime.IsZero() || !meta.BirthTime.IsZero() || meta.Size != 0 {
		t.Errorf("an unstat'ed note carries values: %+v", meta)
	}
	if meta.HasBirthTime {
		t.Error("an unstat'ed note claims a birth time")
	}
}

// ---------------------------------------------------------------------------
// FR-133 — ctime is the BIRTH time, or nothing
// ---------------------------------------------------------------------------

// TestFileMeta_CtimeIsBirthTimeNeverTheInodeChangeTime is spec test 93.
//
// The two are trivially confused — they share a letter and a colloquial name —
// and the wrong one is always available in the same stat structure. The
// discriminator here is TIME: the file is created, a pause is taken, and then
// its MODE is changed. A chmod moves st_ctime to now and leaves the birth time
// where it was, so an implementation that reached for st_ctime reports an
// instant inside the pause window and one that reads the birth time reports an
// instant before it.
//
// On a platform (or filesystem) with no birth time the requirement is the other
// half of FR-133 and is asserted just as hard: the value is ABSENT, and nothing
// is substituted for it.
func TestFileMeta_CtimeIsBirthTimeNeverTheInodeChangeTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	mustWriteFile(t, path, "# hello\n")

	createdBy := time.Now()
	const pause = 80 * time.Millisecond
	time.Sleep(pause)

	// chmod: moves the POSIX inode-change time to NOW, leaves birth time alone.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	chmodAt := time.Now()

	meta, err := StatFile(path)
	if err != nil {
		t.Fatalf("StatFile: %v", err)
	}
	if !meta.Known {
		t.Fatal("StatFile returned unknown metadata for a file it just read")
	}

	if !meta.HasBirthTime {
		// FR-133's honest absence. It must be a CLEAN absence — no half-filled
		// instant sitting in the field for someone to read past the flag.
		if !meta.BirthTime.IsZero() {
			t.Errorf("HasBirthTime is false but BirthTime holds %s; an absent value must be absent, "+
				"not a number guarded by a flag someone can forget to check", meta.BirthTime)
		}
		t.Logf("this platform records no birth time; FR-133's absence branch asserted (GOOS-dependent, not a skip)")
		return
	}

	// The birth time must predate the chmod by about the pause. A margin is
	// allowed for coarse filesystem timestamp granularity, but not enough to
	// swallow the whole pause — otherwise this assertion would pass over
	// st_ctime, which is the one thing it exists to reject.
	margin := pause / 2
	if !meta.BirthTime.Before(chmodAt.Add(-margin)) {
		t.Errorf(
			"file.ctime is %s, which is at or after the chmod at %s.\n"+
				"The file was created at about %s and chmod'ed %s later. A birth time must sit before "+
				"the pause; a value at the chmod moment is the POSIX INODE-CHANGE time, which FR-133 "+
				"forbids substituting — an honest absence beats a plausible wrong answer.",
			meta.BirthTime, chmodAt, createdBy, pause)
	}
	if meta.BirthTime.After(meta.ModTime.Add(time.Second)) {
		t.Errorf("file.ctime %s is after file.mtime %s; a file cannot be created after it was last written",
			meta.BirthTime, meta.ModTime)
	}
}

// TestFileMeta_AbsentBirthTimeStoresNullNotAnInstant closes the storage half of
// FR-133: the flag survives the round trip, so a platform that gave nothing
// cannot come back looking as though it gave the epoch.
func TestFileMeta_AbsentBirthTimeStoresNullNotAnInstant(t *testing.T) {
	store, _ := openIndex(t, Options{})
	rows := taggedNote(t)
	rows.SetFileMeta(FileMeta{
		Known:   true,
		ModTime: time.Date(2026, 8, 30, 14, 0, 0, 0, time.UTC),
		Size:    12,
		// No birth time — the platform did not record one.
	})
	mustUpsert(t, store, rows)

	got := collect(t, store, Selector{})
	if len(got) != 1 {
		t.Fatalf("expected one candidate, got %d", len(got))
	}
	meta := got[0].File
	if meta.HasBirthTime || !meta.BirthTime.IsZero() {
		t.Errorf("a note stored with no birth time came back with one: %+v", meta)
	}
	if !meta.Known || meta.Size != 12 {
		t.Errorf("the OTHER two columns must survive an absent ctime; got %+v", meta)
	}
}

// ---------------------------------------------------------------------------
// FR-131 — note_tags and note_links
// ---------------------------------------------------------------------------

func tagsOf(t *testing.T, store Store, sel Selector) []string {
	t.Helper()
	var out []string
	if err := store.Tags(context.Background(), sel, func(h TagHit) error {
		out = append(out, h.Tag.Tag)
		return nil
	}); err != nil {
		t.Fatalf("Tags: %v", err)
	}
	sort.Strings(out)
	return out
}

func linksOf(t *testing.T, store Store, sel Selector) (links, embeds []string) {
	t.Helper()
	if err := store.Links(context.Background(), sel, func(h LinkHit) error {
		if h.Link.Embed {
			embeds = append(embeds, h.Link.Target)
		} else {
			links = append(links, h.Link.Target)
		}
		return nil
	}); err != nil {
		t.Fatalf("Links: %v", err)
	}
	sort.Strings(links)
	sort.Strings(embeds)
	return links, embeds
}

// TestTags_FrontmatterAndBodyAreBothIndexedAndCodeIsNot.
func TestTags_FrontmatterAndBodyAreBothIndexedAndCodeIsNot(t *testing.T) {
	store, _ := openIndex(t, Options{})
	mustUpsert(t, store, taggedNote(t))

	got := tagsOf(t, store, Selector{})
	want := []string{"care/misting", "care/watering", "greenhouse", "indoor"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("file.tags stored %v, want %v", got, want)
	}

	for _, forbidden := range []string{"2026", "anchor", "not-a-tag-in-code", "also-not-a-tag"} {
		for _, g := range got {
			if g == forbidden {
				t.Errorf("%q was indexed as a tag; it is a bare number, a URL fragment, or text inside code. "+
					"A phantom tag is a record appearing in an answer it does not belong in.", forbidden)
			}
		}
	}
}

// TestLinks_BodyOnlyWithTheEmbedFlag.
//
// The frontmatter `bed: "[[Bed 3]]"` is a DECLARED relation and is already
// stored once in note_relations. Reading it here as well would put one edge in
// two tables with two meanings, and a caller assembling both would count it
// twice — so the body link to the same target is the only note_links row for
// it.
func TestLinks_BodyOnlyWithTheEmbedFlag(t *testing.T) {
	store, _ := openIndex(t, Options{})
	mustUpsert(t, store, taggedNote(t))

	links, embeds := linksOf(t, store, Selector{})
	if strings.Join(links, ",") != "Bed 3,Rosa" {
		t.Errorf("file.links stored %v, want [Bed 3 Rosa]", links)
	}
	if strings.Join(embeds, ",") != "photo.png" {
		t.Errorf("file.embeds stored %v, want [photo.png]", embeds)
	}
	for _, l := range links {
		if l == "Not A Link" {
			t.Error("a wikilink inside a fenced code block was indexed as a link")
		}
	}

	// The display text survives. FR-131's literal column list is four columns
	// wide; records.FileLinkRow reads Heading and Display, and a four-column
	// table drops the display text of every link with nothing looking wrong.
	var sawDisplay bool
	if err := store.Links(context.Background(), Selector{}, func(h LinkHit) error {
		if h.Link.Target == "Rosa" && h.Link.Display == "the keeper" {
			sawDisplay = true
		}
		return nil
	}); err != nil {
		t.Fatalf("Links: %v", err)
	}
	if !sawDisplay {
		t.Error("[[Rosa|the keeper]] came back with no display text; records.FileLinkRow reads it")
	}
}

// TestChildStreams_AreStreamedSeparatelyAndDoNotFanOut is FR-131's assembly
// rule measured rather than inspected.
//
// The corpus is chosen so a Cartesian product is UNMISTAKABLE: the three child
// counts are pairwise different and all greater than one, so a fan-out cannot
// coincide with a correct count. This is the numeric companion to
// TestSQLGate_NoStatementJoinsTwoChildTables, which forbids the join that would
// cause it — one asserts the shape of the SQL, the other the shape of the
// answer.
func TestChildStreams_AreStreamedSeparatelyAndDoNotFanOut(t *testing.T) {
	store, _ := openIndex(t, Options{})
	rows := taggedNote(t)
	mustUpsert(t, store, rows)

	wantTags, wantLinks := len(rows.Tags), len(rows.Links)
	if wantTags < 2 || wantLinks < 2 || wantTags == wantLinks {
		t.Fatalf("the fixture cannot distinguish a product from a sum: %d tags, %d links", wantTags, wantLinks)
	}

	var tags, links int
	if err := store.Tags(context.Background(), Selector{}, func(TagHit) error { tags++; return nil }); err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if err := store.Links(context.Background(), Selector{}, func(LinkHit) error { links++; return nil }); err != nil {
		t.Fatalf("Links: %v", err)
	}
	if tags != wantTags {
		t.Errorf("the tag stream yielded %d rows for a note with %d tags (links=%d, product=%d)",
			tags, wantTags, wantLinks, wantTags*wantLinks)
	}
	if links != wantLinks {
		t.Errorf("the link stream yielded %d rows for a note with %d links (tags=%d, product=%d)",
			links, wantLinks, wantTags, wantTags*wantLinks)
	}

	cands := collect(t, store, Selector{})
	if len(cands) != 1 {
		t.Fatalf("the candidate stream yielded %d records for one note; the property join is fanning out", len(cands))
	}
}

// TestChildRows_AreReplacedOnReindexAndRemovedOnDelete.
//
// A derived index that accumulates is an index holding values the vault does
// not contain. The two new child tables must follow the same
// delete-then-insert rule the other three already do.
func TestChildRows_AreReplacedOnReindexAndRemovedOnDelete(t *testing.T) {
	store, _ := openIndex(t, Options{})
	mustUpsert(t, store, taggedNote(t))

	shrunk := note(t, "garden/monstera.md", plantSchema(t), `---
type: plant
id: PL-9001
species: Monstera deliciosa
tags: [greenhouse]
---

Only [[Bed 3]] now.
`)
	mustUpsert(t, store, shrunk)

	if got := tagsOf(t, store, Selector{}); strings.Join(got, ",") != "greenhouse" {
		t.Errorf("after re-indexing a note whose tags shrank, the store holds %v; stale rows were left behind", got)
	}
	links, embeds := linksOf(t, store, Selector{})
	if strings.Join(links, ",") != "Bed 3" || len(embeds) != 0 {
		t.Errorf("after re-indexing, links=%v embeds=%v; stale rows were left behind", links, embeds)
	}

	if err := store.DeleteNote(context.Background(), "garden/monstera.md"); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	if got := tagsOf(t, store, Selector{}); len(got) != 0 {
		t.Errorf("deleting the note left %v in note_tags", got)
	}
	if l, e := linksOf(t, store, Selector{}); len(l) != 0 || len(e) != 0 {
		t.Errorf("deleting the note left links=%v embeds=%v in note_links", l, e)
	}
}

// ---------------------------------------------------------------------------
// FR-021e — every note's frontmatter, typed or not
// ---------------------------------------------------------------------------

// TestRawRows_AnUntypedNoteStillHoldsItsFrontmatter is founder ruling 1 at the
// storage layer.
func TestRawRows_AnUntypedNoteStillHoldsItsFrontmatter(t *testing.T) {
	store, _ := openIndex(t, Options{})
	mustUpsert(t, store, note(t, "garden/ordinary.md", nil, `---
status: open
owner: Rosa
labels: [urgent, indoor]
empty:
---

# Just a note
`))

	got := collect(t, store, Selector{})
	if len(got) != 1 {
		t.Fatalf("expected one candidate, got %d", len(got))
	}
	c := got[0]
	if c.RecordType != "" {
		t.Errorf("an untyped note acquired a record type %q", c.RecordType)
	}

	status, ok := c.Prop("status")
	if !ok {
		t.Fatal("FR-021e: a note with no matching schema holds NO row for `status`, so `status IS NULL` " +
			"would answer TRUE for a file that plainly says `status: open`. That early return is the defect " +
			"the founder ruled on.")
	}
	if status.State != records.StatePresent {
		t.Errorf("`status: open` is stored as state %v; it must be PRESENT", status.State)
	}
	if len(status.Elems) != 1 || status.Elems[0].Text != "open" {
		t.Errorf("`status` stored %+v; the raw scalar text must survive", status.Elems)
	}
	if status.Type != RawPropertyType {
		t.Errorf("`status` was stored with declared type %q; nothing declared it, and guessing a type "+
			"is the failure D3 names", status.Type)
	}

	labels, _ := c.Prop("labels")
	if len(labels.Elems) != 2 || labels.Elems[0].Text != "urgent" || labels.Elems[1].Text != "indoor" {
		t.Errorf("a list-valued raw key stored %+v; every element must survive in source order", labels.Elems)
	}

	// R-3 is NOT softened by the ruling. A key with no value is still not a
	// value: the KEY exists (so file.properties can name it) and the STATE is
	// absent.
	empty, ok := c.Prop("empty")
	if !ok {
		t.Fatal("`empty:` must still contribute a row, or file.properties cannot name the key")
	}
	if empty.State != records.StateAbsent {
		t.Errorf("`empty:` is stored as state %v; R-3/FR-007 say a key with no value is ABSENT, and the "+
			"founder ruling does not change that — it is about a key that HAS a value", empty.State)
	}
	if len(empty.Elems) != 0 {
		t.Errorf("`empty:` acquired values %+v", empty.Elems)
	}
}

// TestRawRows_ATypedNotesUndeclaredKeysAreStoredToo.
//
// The ruling is about a key on disk, not about a note with no schema. A typed
// note carrying a key its own type does not declare is in exactly the same
// position, and an untyped view naming that key must find it.
func TestRawRows_ATypedNotesUndeclaredKeysAreStoredToo(t *testing.T) {
	store, _ := openIndex(t, Options{})
	mustUpsert(t, store, taggedNote(t))

	got := collect(t, store, Selector{})
	if len(got) != 1 {
		t.Fatalf("expected one candidate, got %d", len(got))
	}
	c := got[0]

	status, ok := c.Prop("status")
	if !ok {
		t.Fatal("a typed note's undeclared `status` key was not stored; an untyped view naming `status` " +
			"would report this note as having none")
	}
	if status.State != records.StatePresent || len(status.Elems) != 1 || status.Elems[0].Text != "open" {
		t.Errorf("undeclared `status` stored as %+v", status)
	}

	// A key the schema DOES declare keeps its typed row and gains no second,
	// raw one — two rows for one (note_id, prop, elem) is a primary-key
	// collision that would fail the whole note's write.
	species, ok := c.Prop("species")
	if !ok {
		t.Fatal("the declared `species` property lost its row")
	}
	if species.Type != records.TypeText {
		t.Errorf("`species` came back with type %q; a declared property must keep its DECLARED type, "+
			"not be overwritten by a raw row", species.Type)
	}
	if len(species.Elems) != 1 {
		t.Errorf("`species` has %d elements; a declared key must not be stored twice", len(species.Elems))
	}
}

// TestRawRows_ANoteWithNoFrontmatterContributesNone keeps the ruling from
// growing into "every note has properties".
func TestRawRows_ANoteWithNoFrontmatterContributesNone(t *testing.T) {
	store, _ := openIndex(t, Options{})
	mustUpsert(t, store, note(t, "garden/plain.md", nil, "# Just a note\n\n- [ ] water the ferns\n"))

	got := collect(t, store, Selector{})
	if len(got) != 1 {
		t.Fatalf("expected one candidate, got %d", len(got))
	}
	if len(got[0].Props) != 0 {
		t.Errorf("a note with no frontmatter acquired properties: %#v", got[0].Props)
	}
}
