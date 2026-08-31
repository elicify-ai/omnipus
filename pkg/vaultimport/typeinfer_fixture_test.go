// Omnipus — FR-104b's EXIT PROOF: a small vault built here, imported for
// real, with the three counts and every per-note reason printed.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS FIXTURE IS BUILT IN CODE RATHER THAN CHECKED IN AS testdata
//
// Every note below exists to exercise ONE branch of the match rule, and the
// branch it exercises is stated on the note. A testdata directory hides that
// mapping in file names; here the intent and the bytes sit together, and a
// reviewer can change a single key and watch the expected outcome move.
//
// It is also collision-proof: this package is being edited by several people
// at once and a shared testdata/ directory is a merge conflict waiting to
// happen.
// ---------------------------------------------------------------------------

// note is one file in the fixture vault.
type note struct {
	// rel is the vault-relative path.
	rel string
	// body is the complete file content, frontmatter included.
	body string
	// want is the outcome this note is here to produce. Empty means the note
	// carries a `type:` and is scaffolding, not a subject.
	want wantOutcome
	// why states, in one line, what the note is testing. It is printed in
	// the failure message so a broken expectation names its own intent.
	why string
}

type wantOutcome string

const (
	wantWritten   wantOutcome = "WRITTEN"
	wantAmbiguous wantOutcome = "AMBIGUOUS"
	wantNoMatch   wantOutcome = "NO-MATCH"
)

// fixtureNotes is the vault. The first block establishes four record types
// by example — the importer infers their schemas from these — and the second
// block is the untyped notes those schemas are then matched against.
func fixtureNotes() []note {
	return []note{
		// --- scaffolding: four types, inferred from observed frontmatter ---
		//
		// person declares {email, team}, both required. `email` is text and
		// `team` is a 2-value enum.
		//
		// THERE ARE SEVEN PEOPLE HERE AND THE COUNT IS LOAD-BEARING. The
		// enum heuristic treats any property with 6 or fewer distinct values
		// as a closed vocabulary, so with two people `email` would infer as
		// enum(alice@…, bob@…) — and condition (4) would then refuse to type
		// ANY new person, because their address is not one of the two
		// declared "values". Seven distinct addresses push `email` past that
		// threshold and it infers as text, which is what it is.
		//
		// This is a real property of the importer, not a fixture quirk: a
		// record type evidenced by very few notes gets a tight enum on
		// genuinely free-text fields, and tight enums refuse newcomers. It
		// is the correct conservative behaviour — the alternative is writing
		// a type onto a note that then fails validation — but it is worth
		// knowing that a 2-note type will adopt almost nothing.
		{rel: "people/Alice.md", body: "---\ntype: person\nemail: alice@example.com\nteam: eng\n---\n\nAlice.\n"},
		{rel: "people/Bob.md", body: "---\ntype: person\nemail: bob@example.com\nteam: ops\n---\n\nBob.\n"},
		{rel: "people/Dan.md", body: "---\ntype: person\nemail: dan@example.com\nteam: eng\n---\n"},
		{rel: "people/Erin.md", body: "---\ntype: person\nemail: erin@example.com\nteam: ops\n---\n"},
		{rel: "people/Frank.md", body: "---\ntype: person\nemail: frank@example.com\nteam: eng\n---\n"},
		{rel: "people/Gita.md", body: "---\ntype: person\nemail: gita@example.com\nteam: ops\n---\n"},
		{rel: "people/Hugo.md", body: "---\ntype: person\nemail: hugo@example.com\nteam: eng\n---\n"},

		// project declares {status, owner, due}, all three required.
		// status is a closed vocabulary {active, done}; owner is a relation
		// to person; due is a date.
		{rel: "projects/Apollo.md", body: "---\ntype: project\nstatus: active\nowner: \"[[Alice]]\"\ndue: 2026-01-01\n---\n"},
		{rel: "projects/Borealis.md", body: "---\ntype: project\nstatus: done\nowner: \"[[Bob]]\"\ndue: 2026-02-01\n---\n"},
		{rel: "projects/Cirrus.md", body: "---\ntype: project\nstatus: active\nowner: \"[[Alice]]\"\ndue: 2026-03-01\n---\n"},

		// task and milestone declare the SAME shape {status, due}. They are
		// both here on purpose: two types of one shape is the ordinary way a
		// real vault produces an honest ambiguity.
		{rel: "tasks/T1.md", body: "---\ntype: task\nstatus: open\ndue: 2026-01-05\n---\n"},
		{rel: "tasks/T2.md", body: "---\ntype: task\nstatus: open\ndue: 2026-01-06\n---\n"},
		{rel: "tasks/T3.md", body: "---\ntype: task\nstatus: done\ndue: 2026-01-07\n---\n"},

		{rel: "milestones/M1.md", body: "---\ntype: milestone\nstatus: open\ndue: 2026-05-01\n---\n"},
		{rel: "milestones/M2.md", body: "---\ntype: milestone\nstatus: done\ndue: 2026-05-02\n---\n"},
		{rel: "milestones/M3.md", body: "---\ntype: milestone\nstatus: open\ndue: 2026-05-03\n---\n"},

		// --- the subjects: nine untyped notes, nine different shapes ------
		{
			rel:  "inbox/Quarterly plan.md",
			body: "---\nstatus: active\nowner: \"[[Alice]]\"\ndue: 2026-04-01\n---\n\nThe plan.\n",
			want: wantWritten,
			why:  "every key is one project declares, every project requirement is present, and no other type declares `owner` — one candidate.",
		},
		{
			rel:  "inbox/New colleague.md",
			body: "---\nemail: carol@example.com\nteam: eng\n---\n\nCarol starts Monday.\n",
			want: wantWritten,
			why:  "{email, team} is person's whole shape and nothing else declares either key — one candidate.",
		},
		{
			rel:  "inbox/Loose item.md",
			body: "---\nstatus: open\ndue: 2026-04-02\n---\n",
			want: wantAmbiguous,
			why:  "task and milestone declare the identical shape and both accept these values; picking one would be a coin toss.",
		},
		{
			rel:  "inbox/Random thought.md",
			body: "---\nmood: pensive\nweather: rain\n---\n\nHm.\n",
			want: wantNoMatch,
			why:  "no inferred type declares `mood` or `weather` at all.",
		},
		{
			rel:  "inbox/Plain note.md",
			body: "No frontmatter here at all, just prose.\n",
			want: wantNoMatch,
			why:  "no frontmatter properties, so there is no shape to match — the vacuous-match trap condition (1) closes.",
		},
		{
			rel:  "inbox/Orphan owner.md",
			body: "---\nowner: \"[[Alice]]\"\n---\n",
			want: wantNoMatch,
			why:  "only project declares `owner`, but project also requires status and due and neither is here.",
		},
		{
			rel:  "inbox/Odd status.md",
			body: "---\nstatus: blocked\nowner: \"[[Alice]]\"\ndue: 2026-04-10\n---\n",
			want: wantNoMatch,
			why:  "shape fits project exactly, but `blocked` is not one of project's observed status values — typing it project would write a note that fails the very schema this run produced.",
		},
		{
			rel:  "inbox/Vague date.md",
			body: "---\nstatus: active\nowner: \"[[Alice]]\"\ndue: sometime next spring\n---\n",
			want: wantNoMatch,
			why:  "shape fits project exactly, but `due` is a date on project and this value is not a date.",
		},
		{
			rel:  "inbox/Listed owner.md",
			body: "---\nstatus: active\nowner:\n  - \"[[Alice]]\"\n  - \"[[Bob]]\"\ndue: 2026-04-11\n---\n",
			want: wantNoMatch,
			why:  "project's `owner` was observed as a single link on every project note, so it is declared single-valued; a two-element list is the wrong arity for it.",
		},
	}
}

// writeFixtureVault materialises the fixture and returns the vault root.
func writeFixtureVault(t *testing.T, notes []note) string {
	t.Helper()
	root := t.TempDir()
	for _, n := range notes {
		abs := filepath.Join(root, filepath.FromSlash(n.rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", n.rel, err)
		}
		if err := os.WriteFile(abs, []byte(n.body), 0o644); err != nil {
			t.Fatalf("write %s: %v", n.rel, err)
		}
	}
	return root
}

// outcomeOf classifies one recorded decision into the same three buckets the
// counts partition, derived from the OUTCOME's own fields rather than from
// the counters — so a test comparing the two can actually disagree with them.
func outcomeOf(o TypeInferenceOutcome) wantOutcome {
	switch {
	case o.Inferred != "":
		return wantWritten
	case len(o.Candidates) > 1:
		return wantAmbiguous
	default:
		return wantNoMatch
	}
}

// TestImporter_UntypedNotes_ExitProof is the exit criterion: import a real
// vault off a real disk, print the three counts and every per-note reason,
// and hold each note to the outcome it was written to produce.
func TestImporter_UntypedNotes_ExitProof(t *testing.T) {
	notes := fixtureNotes()
	root := writeFixtureVault(t, notes)

	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	ti := rep.TypeInference
	byPath := map[string]TypeInferenceOutcome{}
	for _, o := range ti.Notes {
		byPath[filepath.ToSlash(o.RelPath)] = o
	}

	// ---- the printed proof -------------------------------------------
	t.Logf("FR-104b over %d notes: %d untyped, %d decided — WRITTEN=%d AMBIGUOUS=%d NO-MATCH=%d (write errors=%d)",
		rep.Discriminator.TotalNotes, rep.Discriminator.WithoutType, len(ti.Notes),
		ti.Written, ti.Ambiguous, ti.NoMatch, ti.WriteErrors)
	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		o := byPath[p]
		t.Logf("  [%s] %s\n      %s", outcomeOf(o), p, o.Reason)
	}

	// ---- every untyped note has an outcome, and only untyped ones ----
	if len(ti.Notes) != rep.Discriminator.WithoutType {
		t.Errorf("%d notes carried no `type:` but %d have a recorded outcome — the difference was silently skipped",
			rep.Discriminator.WithoutType, len(ti.Notes))
	}

	// ---- per-note expectations ---------------------------------------
	for _, n := range notes {
		if n.want == "" {
			if _, ok := byPath[n.rel]; ok {
				t.Errorf("%s carries a `type:` already but got an inference outcome", n.rel)
			}
			continue
		}
		o, ok := byPath[n.rel]
		if !ok {
			t.Errorf("%s: no recorded outcome at all (expected %s) — %s", n.rel, n.want, n.why)
			continue
		}
		if got := outcomeOf(o); got != n.want {
			t.Errorf("%s: got %s (inferred=%q candidates=%v), want %s\n      intent: %s\n      reason given: %s",
				n.rel, got, o.Inferred, o.Candidates, n.want, n.why, o.Reason)
		}
		if strings.TrimSpace(o.Reason) == "" {
			t.Errorf("%s: outcome with no stated reason — an inference that cannot be explained is not an inference", n.rel)
		}
	}

	// ---- the counters must agree with the outcomes they summarise -----
	var written, ambiguous, nomatch int
	for _, o := range ti.Notes {
		switch outcomeOf(o) {
		case wantWritten:
			written++
		case wantAmbiguous:
			ambiguous++
		case wantNoMatch:
			nomatch++
		}
	}
	if written != ti.Written || ambiguous != ti.Ambiguous || nomatch != ti.NoMatch {
		t.Errorf("counters disagree with the outcomes they summarise: counted WRITTEN=%d AMBIGUOUS=%d NO-MATCH=%d, reported %d/%d/%d",
			written, ambiguous, nomatch, ti.Written, ti.Ambiguous, ti.NoMatch)
	}
	if got := ti.Written + ti.Ambiguous + ti.NoMatch + ti.WriteErrors; got != len(ti.Notes) {
		t.Errorf("the four counts sum to %d but there are %d recorded outcomes — they must partition the notes", got, len(ti.Notes))
	}

	// ---- the write actually happened, in the file, minimally ---------
	for _, n := range notes {
		if n.want != wantWritten {
			continue
		}
		o := byPath[n.rel]
		raw, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(n.rel)))
		if readErr != nil {
			t.Fatalf("re-reading %s: %v", n.rel, readErr)
		}
		want := "---\ntype: " + o.Inferred + "\n"
		if !bytes.HasPrefix(raw, []byte(want)) {
			t.Errorf("%s: expected the file to open with %q, got %q", n.rel, want, first120(raw))
		}
		// The minimal-edit promise: the file is its original bytes plus one
		// line, nothing else moved.
		if got, wantLen := len(raw), len(n.body)+len("type: "+o.Inferred+"\n"); got != wantLen {
			t.Errorf("%s: file is %d bytes, expected %d (original %d + one `type:` line) — the edit was not minimal",
				n.rel, got, wantLen, len(n.body))
		}
	}

	// ---- a note that was typed is validated AS that type -------------
	// The whole reason inference runs before validation is that a note the
	// run just typed must not be counted as "not a record at all" by the
	// same run. If this drifts, the report contradicts the files on disk.
	if rep.Validation.RecognisedRecords < ti.Written {
		t.Errorf("%d notes were typed by this run but only %d records were recognised in validation — the newly typed notes were not carried into validation",
			ti.Written, rep.Validation.RecognisedRecords)
	}
}

// TestImporter_TypedNotesAreNeverSelfInvalidated is FR-104b's ACCEPTANCE
// BAR, stated by the founder-facing rule rather than by an internal
// invariant: after an import, the number of notes the importer itself typed
// AND then reported invalid must be ZERO.
//
// An importer that manufactures its own validation errors is worse than one
// that leaves the note alone — the operator opens a report full of findings
// whose only cause is the importer's guess, and has no way to tell those
// from faults that were already there.
//
// This does NOT re-derive the answer. It loads the schemas the run wrote
// through records.LoadSchemas and asks records.ValidateRecord — the real
// product path — about each note the run typed.
func TestImporter_TypedNotesAreNeverSelfInvalidated(t *testing.T) {
	root := writeFixtureVault(t, fixtureNotes())

	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if rep.TypeInference.Written == 0 {
		t.Fatal("the run typed nothing, so this test would pass vacuously — the fixture must contain at least one note that matches exactly one type")
	}

	schemaSet, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("loading the schemas this run wrote: %v", err)
	}

	selfInvalidated := 0
	for _, o := range rep.TypeInference.Notes {
		if !o.Written {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(o.RelPath))
		raw, readErr := os.ReadFile(abs)
		if readErr != nil {
			t.Fatalf("re-reading %s: %v", o.RelPath, readErr)
		}
		rec := records.ParseRecord(abs, raw)
		if rec.ParseError != "" {
			t.Errorf("%s: the importer's own edit made the note unparseable: %s", o.RelPath, rec.ParseError)
			selfInvalidated++
			continue
		}
		if got := rec.TypeName(); got != o.Inferred {
			t.Errorf("%s: the file on disk says type %q but the report claims %q", o.RelPath, got, o.Inferred)
		}
		rr := records.ValidateRecord(schemaSet, rec, records.ValidateOptions{ReportUndeclaredProperties: true})
		if !rr.Recognised {
			t.Errorf("%s: typed %q, but no schema recognises that type", o.RelPath, o.Inferred)
			selfInvalidated++
			continue
		}
		if !rr.Valid() {
			selfInvalidated++
			t.Errorf("%s: the importer wrote `type: %s` and the SAME run then reports it invalid. Findings:\n      %v",
				o.RelPath, o.Inferred, rr.Findings)
		}
	}
	if selfInvalidated != 0 {
		t.Errorf("ACCEPTANCE BAR FAILED: %d of %d notes typed by this import are invalid against the schemas the same import wrote; the bar is zero",
			selfInvalidated, rep.TypeInference.Written)
	}
	t.Logf("acceptance bar: %d notes typed, %d of them self-invalidated (bar is 0)", rep.TypeInference.Written, selfInvalidated)
}

func first120(b []byte) string {
	if len(b) > 120 {
		return string(b[:120]) + "..."
	}
	return string(b)
}

// TestImporter_UntypedNotes_DryRunWritesNothing holds the other half of the
// promise: a dry run reports what it WOULD write and does not touch a byte.
func TestImporter_UntypedNotes_DryRunWritesNothing(t *testing.T) {
	notes := fixtureNotes()
	root := writeFixtureVault(t, notes)

	before := map[string][]byte{}
	for _, n := range notes {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(n.rel)))
		if err != nil {
			t.Fatalf("reading %s: %v", n.rel, err)
		}
		before[n.rel] = raw
	}

	rep, err := Run(root, false)
	if err != nil {
		t.Fatalf("dry-run import failed: %v", err)
	}
	if rep.TypeInference.Written == 0 {
		t.Fatal("the dry run reported nothing it would write — this fixture has notes that match exactly one type, so the test can no longer tell a dry run from a no-op")
	}

	for _, n := range notes {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(n.rel)))
		if err != nil {
			t.Fatalf("re-reading %s: %v", n.rel, err)
		}
		if !bytes.Equal(raw, before[n.rel]) {
			t.Errorf("%s was MODIFIED by a dry run:\n  before: %q\n  after:  %q", n.rel, before[n.rel], raw)
		}
	}
	for _, o := range rep.TypeInference.Notes {
		if o.Written {
			t.Errorf("%s: outcome claims Written on a dry run", o.RelPath)
		}
	}
}

// renderFixtureReport is a convenience for eyeballing the whole report while
// working on this file; it is exercised by the exit proof so it cannot rot.
func renderFixtureReport(rep *Report) string {
	var buf bytes.Buffer
	rep.Render(&buf)
	return buf.String()
}

// TestImporter_ReportNamesEveryUntypedNote proves the RENDERED report — the
// thing the founder actually reads — names every undecided note and its
// reason, rather than only carrying them in a struct field.
func TestImporter_ReportNamesEveryUntypedNote(t *testing.T) {
	root := writeFixtureVault(t, fixtureNotes())
	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	out := renderFixtureReport(rep)
	if len(rep.TypeInference.Notes) == 0 {
		t.Fatal("no untyped notes in the fixture — this test would pass vacuously")
	}
	for _, o := range rep.TypeInference.Notes {
		if !strings.Contains(out, o.RelPath) {
			t.Errorf("the rendered report never names %s", o.RelPath)
		}
		if !strings.Contains(out, o.Reason) {
			t.Errorf("the rendered report never gives %s's reason (%q)", o.RelPath, o.Reason)
		}
	}
	if !strings.Contains(out, fmt.Sprintf("%d typed by this run", rep.TypeInference.Written)) {
		t.Errorf("the rendered report does not state the written count; got:\n%s", out)
	}
}
