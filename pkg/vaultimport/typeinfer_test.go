// Omnipus — FR-104b's unit suite: the founder's "use common sense" ruling on
// untyped notes, held to the three things that make an inference honest —
// it is only written when exactly one type fits, it is never written on a
// coin toss, and every note leaves with a stated reason.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// Helpers. Notes are written as REAL files and parsed through
// records.ParseRecord — never hand-built Record structs — so a test that
// passes here is a test against the parser the product actually uses.
// ---------------------------------------------------------------------------

// noteOnDisk writes one note into dir and returns it parsed, with the same
// provenance ScanVault/LoadNotes would have given it.
func noteOnDisk(t *testing.T, dir, rel, content string) NoteRecord {
	t.Helper()
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", rel, err)
	}
	rec := records.ParseRecord(abs, []byte(content))
	if rec.ParseError != "" {
		t.Fatalf("fixture note %s does not parse: %s", rel, rec.ParseError)
	}
	return NoteRecord{AbsPath: abs, RelPath: rel, Rec: rec}
}

// props builds one inferred type's declaration list. A name suffixed with
// "!" is required — a compact spelling that keeps the shape of each fixture
// schema readable on one line.
func props(names ...string) []InferredProperty {
	out := make([]InferredProperty, 0, len(names))
	for _, n := range names {
		p := InferredProperty{Type: records.TypeText}
		if strings.HasSuffix(n, "!") {
			p.Name, p.Required = strings.TrimSuffix(n, "!"), true
		} else {
			p.Name = n
		}
		out = append(out, p)
	}
	return out
}

// outcomeFor returns the single recorded outcome for one note path.
func outcomeFor(t *testing.T, rep TypeInferenceReport, relPath string) TypeInferenceOutcome {
	t.Helper()
	for _, n := range rep.Notes {
		if n.RelPath == relPath {
			return n
		}
	}
	t.Fatalf("no recorded outcome for %q — it was silently skipped; outcomes present: %d", relPath, len(rep.Notes))
	return TypeInferenceOutcome{}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// The match rule: exactly one type fits -> write it.
// ---------------------------------------------------------------------------

func TestInferTypes_ExactlyOneCandidate_WritesTypeAndSaysWhy(t *testing.T) {
	dir := t.TempDir()
	n := noteOnDisk(t, dir, "untyped.md", "---\ntitle: Snow Crash\nauthor: Stephenson\n---\n\nbody text\n")

	rep := InferTypesForUntypedNotes([]NoteRecord{n}, map[string][]InferredProperty{
		"book":  props("title!", "author!"),
		"movie": props("title!", "director!"),
	}, true)

	if rep.Written != 1 || rep.Ambiguous != 0 || rep.NoMatch != 0 {
		t.Fatalf("expected exactly one written type, got written=%d ambiguous=%d no-match=%d",
			rep.Written, rep.Ambiguous, rep.NoMatch)
	}
	out := outcomeFor(t, rep, "untyped.md")
	if out.Inferred != "book" {
		t.Errorf("inferred type = %q, want \"book\" (it carries author, which movie does not declare)", out.Inferred)
	}
	if !out.Written {
		t.Error("outcome does not report the file as written, but write=true and a type was inferred")
	}
	if got := out.Candidates; len(got) != 1 || got[0] != "book" {
		t.Errorf("candidates = %v, want exactly [book]", got)
	}
	// The reason is the founder-facing half of the ruling: it must name the
	// type. An outcome nobody can explain in one line is a bad inference.
	if !strings.Contains(out.Reason, "book") {
		t.Errorf("reason does not name the inferred type: %q", out.Reason)
	}

	// The write itself: `type: book` present, and the note's own content
	// untouched around it.
	got := readFile(t, n.AbsPath)
	want := "---\ntype: book\ntitle: Snow Crash\nauthor: Stephenson\n---\n\nbody text\n"
	if got != want {
		t.Errorf("the edit was not the minimal one-line insertion.\n got: %q\nwant: %q", got, want)
	}
}

func TestInferTypes_WrittenTypeIsAdoptedInMemory(t *testing.T) {
	dir := t.TempDir()
	n := noteOnDisk(t, dir, "untyped.md", "---\ntitle: Snow Crash\nauthor: Stephenson\n---\n")
	notes := []NoteRecord{n}

	InferTypesForUntypedNotes(notes, map[string][]InferredProperty{
		"book": props("title!", "author!"),
	}, true)

	// Without this, the SAME run that typed the note would go on to report
	// it as "not a record at all" — a report contradicting the file it just
	// wrote. The in-memory record must agree with the disk.
	if got := notes[0].Rec.TypeName(); got != "book" {
		t.Errorf("in-memory record type = %q after the write, want \"book\" — downstream validation would still see this note as untyped", got)
	}
}

// ---------------------------------------------------------------------------
// Ambiguity: two candidates is a coin toss, and a coin toss is not written.
// ---------------------------------------------------------------------------

func TestInferTypes_TwoCandidates_IsAmbiguousAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	const src = "---\nname: Barbara\nemail: b@example.com\n---\n"
	n := noteOnDisk(t, dir, "untyped.md", src)

	rep := InferTypesForUntypedNotes([]NoteRecord{n}, map[string][]InferredProperty{
		// Two types with the SAME shape — nothing in the note can separate
		// them, which is exactly when guessing must stop.
		"person":  props("name!", "email!"),
		"contact": props("name!", "email!"),
	}, true)

	if rep.Ambiguous != 1 || rep.Written != 0 {
		t.Fatalf("expected one ambiguous and zero written, got written=%d ambiguous=%d no-match=%d",
			rep.Written, rep.Ambiguous, rep.NoMatch)
	}
	out := outcomeFor(t, rep, "untyped.md")
	if out.Inferred != "" {
		t.Errorf("an ambiguous note was given type %q — the rule is to leave it untyped", out.Inferred)
	}
	if len(out.Candidates) != 2 {
		t.Errorf("candidates = %v, want both contenders recorded", out.Candidates)
	}
	// The founder must be able to act on this line: it has to name WHICH
	// types tied, not merely that there was a tie.
	for _, want := range []string{"contact", "person"} {
		if !strings.Contains(out.Reason, want) {
			t.Errorf("reason does not name candidate %q, so the operator cannot choose: %q", want, out.Reason)
		}
	}
	if got := readFile(t, n.AbsPath); got != src {
		t.Errorf("an ambiguous note's file was modified.\n got: %q\nwant: %q", got, src)
	}
	if notes := notes1(n); notes[0].Rec.TypeName() != "" {
		t.Error("an ambiguous note was typed in memory")
	}
}

func notes1(n NoteRecord) []NoteRecord { return []NoteRecord{n} }

// ---------------------------------------------------------------------------
// No match: nothing fit. Record it, invent nothing.
// ---------------------------------------------------------------------------

func TestInferTypes_UndeclaredKey_IsNoMatch(t *testing.T) {
	dir := t.TempDir()
	const src = "---\nsprocket: 5\nwidget: blue\n---\n"
	n := noteOnDisk(t, dir, "untyped.md", src)

	rep := InferTypesForUntypedNotes([]NoteRecord{n}, map[string][]InferredProperty{
		"book": props("title!", "author!"),
	}, true)

	if rep.NoMatch != 1 || rep.Written != 0 || rep.Ambiguous != 0 {
		t.Fatalf("expected one no-match, got written=%d ambiguous=%d no-match=%d",
			rep.Written, rep.Ambiguous, rep.NoMatch)
	}
	out := outcomeFor(t, rep, "untyped.md")
	if out.Inferred != "" || len(out.Candidates) != 0 {
		t.Errorf("a no-match note got type=%q candidates=%v", out.Inferred, out.Candidates)
	}
	if got := readFile(t, n.AbsPath); got != src {
		t.Error("a no-match note's file was modified")
	}
}

// TestInferTypes_NoPropertiesAtAll_IsNoMatch guards match condition (1),
// which is the one a reader is most likely to think redundant. A note with
// no frontmatter properties satisfies "every key I carry is declared"
// VACUOUSLY — for every type at once. Without condition (1) such a note
// matches every schema in the vault, and an empty note in a single-type
// vault would be silently written as that type.
func TestInferTypes_NoPropertiesAtAll_IsNoMatch(t *testing.T) {
	dir := t.TempDir()
	// The type declares NO REQUIRED properties, and that detail is the whole
	// test. An earlier version of this fixture required title and author,
	// and it passed against code with condition (1) deleted — the required
	// check was disqualifying the empty note on its own, so the guard under
	// test was never reached and the test proved nothing. (Found by
	// mutation: replacing `case len(keys) == 0:` with `case false:` left it
	// green.)
	//
	// With nothing required, an empty note satisfies "every key I carry is
	// declared" and "every requirement is met" VACUOUSLY, so without
	// condition (1) it matches this single type outright and gets written.
	inferred := map[string][]InferredProperty{"book": props("title", "author")}

	for _, tc := range []struct{ name, src string }{
		{"no frontmatter block", "just a body, no fences\n"},
		{"empty frontmatter block", "---\n---\n\nbody\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := noteOnDisk(t, dir, tc.name+".md", tc.src)
			rep := InferTypesForUntypedNotes([]NoteRecord{n}, inferred, true)

			if rep.NoMatch != 1 || rep.Written != 0 {
				t.Fatalf("expected no-match, got written=%d ambiguous=%d no-match=%d",
					rep.Written, rep.Ambiguous, rep.NoMatch)
			}
			out := outcomeFor(t, rep, tc.name+".md")
			if out.Inferred != "" {
				t.Errorf("a note with no properties was typed %q by vacuous match", out.Inferred)
			}
			if got := readFile(t, n.AbsPath); got != tc.src {
				t.Errorf("file was modified.\n got: %q\nwant: %q", got, tc.src)
			}
		})
	}
}

// TestInferTypes_MissingRequiredProperty_DisqualifiesTheType guards match
// condition (3). Without it the loosest schema in the vault swallows
// everything: a note carrying only `title` would be adopted by a type whose
// every real note also carries `author`.
func TestInferTypes_MissingRequiredProperty_DisqualifiesTheType(t *testing.T) {
	dir := t.TempDir()
	n := noteOnDisk(t, dir, "untyped.md", "---\ntitle: Lonely\n---\n")

	rep := InferTypesForUntypedNotes([]NoteRecord{n}, map[string][]InferredProperty{
		"book": props("title!", "author!"), // author is required and absent
	}, true)

	if rep.NoMatch != 1 || rep.Written != 0 {
		t.Fatalf("a note missing a required property was adopted anyway: written=%d ambiguous=%d no-match=%d",
			rep.Written, rep.Ambiguous, rep.NoMatch)
	}
}

// TestInferTypes_EmptyValueIsAbsence pins the presence rule to FR-007's own
// definition. An explicit null and an empty scalar are ABSENCE — the same
// reading that decided `required` in the first place. A looser reading here
// would let a note satisfy a requirement it then fails validation on.
func TestInferTypes_EmptyValueIsAbsence(t *testing.T) {
	dir := t.TempDir()
	inferred := map[string][]InferredProperty{"book": props("title!", "author!")}

	for _, tc := range []struct{ name, src string }{
		{"explicit null", "---\ntitle: A Book\nauthor:\n---\n"},
		{"empty string", "---\ntitle: A Book\nauthor: \"\"\n---\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := noteOnDisk(t, dir, tc.name+".md", tc.src)
			rep := InferTypesForUntypedNotes([]NoteRecord{n}, inferred, true)
			if rep.Written != 0 || rep.NoMatch != 1 {
				t.Errorf("%s counted as a present value: written=%d no-match=%d", tc.name, rep.Written, rep.NoMatch)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Scope and honesty of the report.
// ---------------------------------------------------------------------------

// TestInferTypes_AlreadyTypedNotesAreNotConsidered: FR-104b is about notes
// that carry NO type. A note that already declares one is not the
// importer's business, and must not appear in the report at all — the
// harness counts outcomes against the untyped total.
func TestInferTypes_AlreadyTypedNotesAreNotConsidered(t *testing.T) {
	dir := t.TempDir()
	const src = "---\ntype: movie\ntitle: Alien\nauthor: Nobody\n---\n"
	typed := noteOnDisk(t, dir, "typed.md", src)
	untyped := noteOnDisk(t, dir, "untyped.md", "---\ntitle: Snow Crash\nauthor: Stephenson\n---\n")

	rep := InferTypesForUntypedNotes([]NoteRecord{typed, untyped}, map[string][]InferredProperty{
		"book": props("title!", "author!"),
	}, true)

	if len(rep.Notes) != 1 {
		t.Fatalf("recorded %d outcomes, want 1 — only untyped notes are in scope", len(rep.Notes))
	}
	if rep.Notes[0].RelPath != "untyped.md" {
		t.Errorf("recorded an outcome for %q, want the untyped note", rep.Notes[0].RelPath)
	}
	if got := readFile(t, typed.AbsPath); got != src {
		t.Error("an already-typed note's file was rewritten")
	}
}

// TestInferTypes_EveryOutcomeHasAReason is the invariant the whole ruling
// rests on: "left as is" is a decision, and a decision has to be written
// down. A recorded outcome with no reason is a silent skip wearing a report
// entry's clothes.
func TestInferTypes_EveryOutcomeHasAReason(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "a-written.md", "---\ntitle: T\nauthor: A\n---\n"),
		noteOnDisk(t, dir, "b-ambiguous.md", "---\nname: N\nemail: e\n---\n"),
		noteOnDisk(t, dir, "c-nomatch.md", "---\nsprocket: 5\n---\n"),
		noteOnDisk(t, dir, "d-empty.md", "no frontmatter\n"),
	}
	rep := InferTypesForUntypedNotes(notes, map[string][]InferredProperty{
		"book":    props("title!", "author!"),
		"person":  props("name!", "email!"),
		"contact": props("name!", "email!"),
	}, true)

	if len(rep.Notes) != len(notes) {
		t.Fatalf("%d notes in, %d outcomes out — %d silently skipped", len(notes), len(rep.Notes), len(notes)-len(rep.Notes))
	}
	for _, n := range rep.Notes {
		if strings.TrimSpace(n.Reason) == "" {
			t.Errorf("%s has an outcome with no stated reason", n.RelPath)
		}
	}
	// The four buckets must account for every note; a note counted in none
	// of them is invisible in the founder-facing summary line.
	if sum := rep.Written + rep.Ambiguous + rep.NoMatch + rep.WriteErrors; sum != len(rep.Notes) {
		t.Errorf("counts sum to %d but %d outcomes were recorded — the summary line does not add up", sum, len(rep.Notes))
	}
	if rep.Written != 1 || rep.Ambiguous != 1 || rep.NoMatch != 2 {
		t.Errorf("written=%d ambiguous=%d no-match=%d, want 1/1/2", rep.Written, rep.Ambiguous, rep.NoMatch)
	}
}

// TestInferTypes_OutcomesAreSortedByPath keeps the report stable run to run;
// Go's map iteration would otherwise reorder it and make two reports of the
// same vault diff against each other.
func TestInferTypes_OutcomesAreSortedByPath(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "zulu.md", "---\nsprocket: 1\n---\n"),
		noteOnDisk(t, dir, "alpha.md", "---\nsprocket: 2\n---\n"),
		noteOnDisk(t, dir, "mike.md", "---\nsprocket: 3\n---\n"),
	}
	rep := InferTypesForUntypedNotes(notes, map[string][]InferredProperty{}, false)
	got := make([]string, 0, len(rep.Notes))
	for _, n := range rep.Notes {
		got = append(got, n.RelPath)
	}
	want := []string{"alpha.md", "mike.md", "zulu.md"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("outcomes are not path-sorted: %v", got)
		}
	}
}

// ---------------------------------------------------------------------------
// Dry run: decide everything, write nothing.
// ---------------------------------------------------------------------------

func TestInferTypes_DryRunDecidesWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	const src = "---\ntitle: Snow Crash\nauthor: Stephenson\n---\n"
	n := noteOnDisk(t, dir, "untyped.md", src)

	rep := InferTypesForUntypedNotes([]NoteRecord{n}, map[string][]InferredProperty{
		"book": props("title!", "author!"),
	}, false)

	if rep.Written != 1 {
		t.Fatalf("a dry run must still DECIDE: written=%d", rep.Written)
	}
	out := outcomeFor(t, rep, "untyped.md")
	if out.Inferred != "book" {
		t.Errorf("dry run inferred %q, want book", out.Inferred)
	}
	// The per-note Written flag reports what happened to the FILE, and on a
	// dry run nothing did. A dry run that claims it wrote is the exact
	// dishonesty this flag exists to prevent.
	if out.Written {
		t.Error("dry-run outcome claims the file was written")
	}
	if !strings.Contains(out.Reason, "would write") {
		t.Errorf("dry-run reason does not say it is hypothetical: %q", out.Reason)
	}
	if got := readFile(t, n.AbsPath); got != src {
		t.Errorf("a dry run modified the file.\n got: %q\nwant: %q", got, src)
	}
}

// ---------------------------------------------------------------------------
// The write is the minimum possible edit.
// ---------------------------------------------------------------------------

func TestWriteTypeKey_IsTheMinimalEdit(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{
			name: "preserves key order, blank lines and body verbatim",
			src:  "---\nzebra: last\nalpha: first\n\nspaced: yes\n---\n\n# Heading\n\ntext with --- dashes\n",
			want: "---\ntype: book\nzebra: last\nalpha: first\n\nspaced: yes\n---\n\n# Heading\n\ntext with --- dashes\n",
		},
		{
			name: "preserves CRLF line endings",
			src:  "---\r\ntitle: T\r\n---\r\nbody\r\n",
			want: "---\r\ntype: book\r\ntitle: T\r\n---\r\nbody\r\n",
		},
		{
			name: "preserves a UTF-8 BOM ahead of the fence",
			src:  "\xef\xbb\xbf---\ntitle: T\n---\nbody\n",
			want: "\xef\xbb\xbf---\ntype: book\ntitle: T\n---\nbody\n",
		},
		{
			name: "preserves quoting and comments it did not write",
			src:  "---\ntitle: \"Quoted: Value\"  # trailing comment\n---\n",
			want: "---\ntype: book\ntitle: \"Quoted: Value\"  # trailing comment\n---\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "note.md")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("seeding: %v", err)
			}
			if err := writeTypeKey(path, "book"); err != nil {
				t.Fatalf("writeTypeKey: %v", err)
			}
			if got := readFile(t, path); got != tc.want {
				t.Errorf("edit changed more than the one inserted line.\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// TestWriteTypeKey_PreservesFileMode: a note is the operator's own document.
// The importer must not impose the control plane's own 0600 posture on it.
func TestWriteTypeKey_PreservesFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(path, []byte("---\ntitle: T\n---\n"), 0o644); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := writeTypeKey(path, "book"); err != nil {
		t.Fatalf("writeTypeKey: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o640 {
		t.Errorf("file mode = %o after the edit, want 0640 preserved", got)
	}
}

// TestWriteTypeKey_RefusesRatherThanRepairs: each refusal returns a named
// error the report prints, rather than guessing at a repair.
func TestWriteTypeKey_RefusesRatherThanRepairs(t *testing.T) {
	cases := []struct {
		name, src, typeName, wantErrSubstr string
	}{
		{"no frontmatter fence", "body only\n", "book", "no frontmatter block"},
		{"single line, no newline", "no newline at all", "book", "no frontmatter block"},
		{"first line is not the fence", "# Title\n---\ntitle: T\n---\n", "book", "no frontmatter block"},
		{"type name would need quoting", "---\ntitle: T\n---\n", "book: bad", "not a plain scalar"},
		{"empty type name", "---\ntitle: T\n---\n", "", "not a plain scalar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "note.md")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("seeding: %v", err)
			}
			err := writeTypeKey(path, tc.typeName)
			if err == nil {
				t.Fatalf("writeTypeKey accepted %q — it must refuse", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Errorf("error %q does not name the reason (want substring %q)", err, tc.wantErrSubstr)
			}
			if got := readFile(t, path); got != tc.src {
				t.Error("a refused write still modified the file")
			}
		})
	}
}

// TestInferTypes_WriteFailureIsItsOwnOutcome: a filesystem problem must not
// be downgraded into "no match", which would misattribute it to the
// inference and hide a real, fixable error from the operator.
func TestInferTypes_WriteFailureIsItsOwnOutcome(t *testing.T) {
	dir := t.TempDir()
	// A note whose frontmatter parses but which writeTypeKey refuses to
	// edit: the fence is not on the first line, so there is no safe
	// insertion point.
	n := noteOnDisk(t, dir, "untyped.md", "---\ntitle: T\nauthor: A\n---\n")
	// Remove the file out from under the write to force a real I/O failure.
	if err := os.Remove(n.AbsPath); err != nil {
		t.Fatalf("removing: %v", err)
	}

	rep := InferTypesForUntypedNotes([]NoteRecord{n}, map[string][]InferredProperty{
		"book": props("title!", "author!"),
	}, true)

	if rep.WriteErrors != 1 {
		t.Fatalf("write errors = %d, want 1 (written=%d no-match=%d)", rep.WriteErrors, rep.Written, rep.NoMatch)
	}
	if rep.Written != 0 || rep.NoMatch != 0 {
		t.Errorf("a write failure was miscounted: written=%d no-match=%d", rep.Written, rep.NoMatch)
	}
	out := outcomeFor(t, rep, "untyped.md")
	if out.Inferred != "" {
		t.Errorf("a failed write still reports type %q as inferred", out.Inferred)
	}
	if !strings.Contains(out.Reason, "could NOT be edited") {
		t.Errorf("reason does not say the write failed: %q", out.Reason)
	}
}

// TestIsPlainYAMLScalar_RejectsAnythingNeedingQuotes keeps the guard honest
// about what it accepts: the value has to round-trip unquoted, or the line
// this writes would parse back as something other than what was inferred.
func TestIsPlainYAMLScalar_RejectsAnythingNeedingQuotes(t *testing.T) {
	ok := []string{"book", "meeting-note", "daily_log", "v1.2", "Type9"}
	bad := []string{"", " book", "book ", "book: x", "a b", "#book", "[book]", "yes\nno", "café"}
	for _, s := range ok {
		if !isPlainYAMLScalar(s) {
			t.Errorf("isPlainYAMLScalar(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if isPlainYAMLScalar(s) {
			t.Errorf("isPlainYAMLScalar(%q) = true — this would be written unquoted", s)
		}
	}
}

// TestIsReservedKey pins the three keys a schema never declares. If this
// list drifts from CollectTypeGroups' own exclusions, the key sets compared
// during matching and the property sets inferred for the schemas stop being
// drawn on the same terms, and notes stop matching their own type.
func TestIsReservedKey(t *testing.T) {
	for _, k := range []string{records.RecordTypeKey, records.RecordIDKey, records.RecordIDKeyNamespaced} {
		if !isReservedKey(k) {
			t.Errorf("isReservedKey(%q) = false — it would be compared against schema properties", k)
		}
	}
	for _, k := range []string{"title", "author", "status", "typeface"} {
		if isReservedKey(k) {
			t.Errorf("isReservedKey(%q) = true — a real property would be ignored during matching", k)
		}
	}
}

// TestInferTypes_ReservedKeysDoNotBlockAMatch: a note carrying `id:` is
// still matchable. `id` is never a declared property, so treating it as an
// undeclared key would disqualify every type for every identified note.
func TestInferTypes_ReservedKeysDoNotBlockAMatch(t *testing.T) {
	dir := t.TempDir()
	n := noteOnDisk(t, dir, "untyped.md", "---\nid: abc-123\ntitle: T\nauthor: A\n---\n")

	rep := InferTypesForUntypedNotes([]NoteRecord{n}, map[string][]InferredProperty{
		"book": props("title!", "author!"),
	}, true)

	if rep.Written != 1 {
		t.Fatalf("a note carrying the reserved `id:` key failed to match: written=%d no-match=%d", rep.Written, rep.NoMatch)
	}
}
