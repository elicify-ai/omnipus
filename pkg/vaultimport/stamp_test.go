// Omnipus — tests for ADR-068 D7 identifier stamping.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// The fixture: a vault that is MESSY ON PURPOSE
//
// Every note below is modelled on something the founder's real 766-note vault
// actually contains, because a fixture built from clean synthetic notes proves
// only that the happy path works and this change's whole risk is the unhappy
// ones. In particular the vault holds notes with NO frontmatter at all, notes
// whose type cannot be determined, and values outside their declared enum —
// none of which may be corrupted or wrongly stamped.
// ---------------------------------------------------------------------------

// messyCompanyNote is the byte-identity witness. It carries, deliberately:
// a YAML comment; keys in an order no serialiser would choose; a blank line
// INSIDE the frontmatter block; a single-quoted value beside a bare one; a
// block sequence; a trailing-space line; and body prose containing a `---`
// that is not a fence.
const messyCompanyNote = `---
# Acme — imported from the old CRM, do not re-sort these keys
type: company
status: active
name: 'Acme Corp'

# the next two were added by hand in 2019
segment: vendor
tags:
  - enterprise
  - eu
owner: "Dana O'Neill"
---

# Acme Corp

Notes on Acme --- including the dash-dash-dash above, which is prose.

- renewal is in March
`

const companySchema = `schema_version: 1
type: company
label: Company
identity:
  prefix: CO
properties:
  name:    { type: text, required: true }
  status:  { type: enum, values: [prospect, active] }
  segment: { type: enum, values: [vendor, customer, partner] }
  tags:    { type: enum, values: [enterprise, eu, smb], many: true }
  owner:   { type: text }
`

// widgetSchema declares NO `identity:` block. It is the fixture for the
// documented prefix-less fallback.
const widgetSchema = `schema_version: 1
type: widget
label: Widget
properties:
  name: { type: text }
`

// messyVault writes the fixture and returns its root.
func messyVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	schemaDir := records.SchemaDir(root)
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatalf("creating the schema directory: %v", err)
	}
	writeFixture(t, filepath.Join(schemaDir, "company.yaml"), companySchema)
	writeFixture(t, filepath.Join(schemaDir, "widget.yaml"), widgetSchema)

	// A record with messy frontmatter — the byte-identity witness.
	writeFixture(t, filepath.Join(root, "acme.md"), messyCompanyNote)

	// A record that ALREADY carries an identifier, and carries the FIRST one
	// the allocator would otherwise hand out. It must be left alone, and it
	// must push the counter past CO-0001.
	writeFixture(t, filepath.Join(root, "globex.md"), "---\ntype: company\nid: CO-0001\nname: Globex\n---\n\nAlready identified.\n")

	// No frontmatter at all. The founder's vault is full of these.
	writeFixture(t, filepath.Join(root, "scratch.md"), "# Just a note\n\nNo frontmatter anywhere in this file.\n")

	// Frontmatter, but no `type:` — an ordinary note (FR-005).
	writeFixture(t, filepath.Join(root, "meeting.md"), "---\ndate: 2026-03-01\nattendees: [dana, sam]\n---\n\nMeeting notes.\n")

	// A `type:` this vault declares no schema for. Its author plainly means it
	// as a record; Omnipus does not know what it is. It must be REPORTED and
	// never stamped.
	writeFixture(t, filepath.Join(root, "voyager.md"), "---\ntype: spaceship\nname: Voyager\n---\n\nNot a declared type.\n")

	// A record whose `status` is OUTSIDE its declared enum. It is still a
	// record — validity is a separate question from identity — so it must be
	// stamped, and its invalid value must survive untouched.
	writeFixture(t, filepath.Join(root, "invalid-enum.md"), "---\ntype: company\nname: Initech\nstatus: exploded\n---\n\nInvalid enum value on purpose.\n")

	// An empty `type:` — TypeName() returns "", so it is an ordinary note.
	writeFixture(t, filepath.Join(root, "empty-type.md"), "---\ntype:\nname: Nothing\n---\n\nEmpty type value.\n")

	// A UTF-8 BOM. records.ParseRecord skips it and sees a record;
	// knowledge.fmParse does NOT skip it and would otherwise prepend a SECOND
	// frontmatter block.
	writeFixture(t, filepath.Join(root, "bom.md"), "\xef\xbb\xbf---\ntype: company\nname: Umbrella\n---\n\nBOM note.\n")

	// CRLF line endings throughout.
	writeFixture(t, filepath.Join(root, "crlf.md"), "---\r\ntype: company\r\nname: Cyberdyne\r\n---\r\n\r\nCRLF note.\r\n")

	// A record of the PREFIX-LESS type.
	writeFixture(t, filepath.Join(root, "widget.md"), "---\ntype: widget\nname: Sprocket\n---\n\nNo identity prefix on this type.\n")

	return root
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture %q: %v", path, err)
	}
}

// snapshotVault reads every .md file under root into a path -> bytes map.
func snapshotVault(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, path)
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("snapshotting the vault: %v", err)
	}
	return out
}

// stampFixture runs the stamping pass over the fixture vault.
func stampFixture(t *testing.T, root string, write bool) IdentityStampReport {
	t.Helper()
	rep, err := StampVault(root, Options{Write: write})
	if err != nil {
		t.Fatalf("StampVault: %v", err)
	}
	return rep
}

// ---------------------------------------------------------------------------
// THE HEADLINE PROOF: the write is a splice, not a re-serialisation
// ---------------------------------------------------------------------------

// TestStamp_WriteIsASpliceAndIsByteIdenticalOutsideTheInsertedLine is the
// obligation RecordWriteRequest's own schema states: "the vault is
// simultaneously a human's working notes, and a writer that re-serialises YAML
// degrades it a little on every touch".
//
// The oracle is derived from that rule, not from what the code produces: take
// the file AFTER stamping, delete the ONE line that was added, and the result
// must equal the original file BYTE FOR BYTE. A YAML round-trip fails this
// immediately — it drops the comment, sorts the keys, collapses the blank
// line, and normalises 'Acme Corp' to Acme Corp.
func TestStamp_WriteIsASpliceAndIsByteIdenticalOutsideTheInsertedLine(t *testing.T) {
	root := messyVault(t)
	notePath := filepath.Join(root, "acme.md")

	before, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("reading the note before: %v", err)
	}

	rep := stampFixture(t, root, true)

	after, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("reading the note after: %v", err)
	}
	if bytes.Equal(before, after) {
		t.Fatal("the note was not changed at all — nothing was stamped, so this test proves nothing")
	}

	id := stampedIDFor(t, rep, "acme.md")
	added, rest := removeOneLine(t, string(after), records.RecordIDKey+": "+id)
	if added != 1 {
		t.Fatalf("expected exactly ONE line carrying the identifier, found %d", added)
	}
	if rest != string(before) {
		t.Fatalf("the file is NOT byte-identical outside the inserted line.\n--- want (original) ---\n%q\n--- got (after, id line removed) ---\n%q", string(before), rest)
	}

	// The specific degradations a re-serialiser causes, named individually, so
	// a failure says WHICH one happened rather than only that bytes differ.
	for _, survivor := range []string{
		"# Acme — imported from the old CRM, do not re-sort these keys",
		"name: 'Acme Corp'",
		"# the next two were added by hand in 2019",
		"  - enterprise",
		`owner: "Dana O'Neill"`,
		"Notes on Acme --- including the dash-dash-dash above, which is prose.",
	} {
		if !strings.Contains(string(after), survivor) {
			t.Errorf("the splice destroyed %q — this is the YAML-re-serialisation degradation the rule forbids", survivor)
		}
	}

	// Key ORDER, not merely key presence: a sort is the degradation that is
	// easiest to miss because every key is still there.
	if got := frontmatterKeyOrder(t, after); !startsWith(got, []string{"type", "status", "name", "segment", "tags", "owner"}) {
		t.Errorf("frontmatter key order changed: got %v; the operator's own order must survive", got)
	}
}

// TestStamp_PreservesCRLFAndBOM covers the two file-level encodings that a
// naive splice corrupts. The BOM case is the dangerous one: records.ParseRecord
// skips a BOM and sees a record, while knowledge.fmParse does not skip it and
// reports "no frontmatter" — so an unguarded SetProperty PREPENDS a second
// frontmatter block and the note stops parsing altogether.
func TestStamp_PreservesCRLFAndBOM(t *testing.T) {
	root := messyVault(t)
	stampFixture(t, root, true)

	bom, err := os.ReadFile(filepath.Join(root, "bom.md"))
	if err != nil {
		t.Fatalf("reading the BOM note: %v", err)
	}
	if !bytes.HasPrefix(bom, []byte{0xef, 0xbb, 0xbf}) {
		t.Error("the UTF-8 BOM was lost")
	}
	if n := bytes.Count(bom, []byte("---")); n != 2 {
		t.Errorf("the BOM note has %d `---` fences, want 2 — a second frontmatter block was prepended", n)
	}
	if rec := records.ParseRecord("bom.md", bom); rec.ID() == "" {
		t.Errorf("the BOM note was not stamped; it parses as %q with id %q", rec.TypeName(), rec.ID())
	}

	crlf, err := os.ReadFile(filepath.Join(root, "crlf.md"))
	if err != nil {
		t.Fatalf("reading the CRLF note: %v", err)
	}
	if bytes.Contains(bytes.ReplaceAll(crlf, []byte("\r\n"), nil), []byte("\n")) {
		t.Error("a bare LF was introduced into a CRLF file")
	}
	if rec := records.ParseRecord("crlf.md", crlf); rec.ID() == "" {
		t.Error("the CRLF note was not stamped")
	}
}

// ---------------------------------------------------------------------------
// What must NEVER be stamped
// ---------------------------------------------------------------------------

// TestStamp_NeverStampsWhatItCannotClassify is the importer's existing
// discipline applied to identifiers: report what you cannot classify, never
// guess. A note with no frontmatter, no `type:`, an empty `type:`, or a
// `type:` this vault declares no schema for must come through untouched.
func TestStamp_NeverStampsWhatItCannotClassify(t *testing.T) {
	root := messyVault(t)
	before := snapshotVault(t, root)

	rep := stampFixture(t, root, true)
	after := snapshotVault(t, root)

	for _, rel := range []string{"scratch.md", "meeting.md", "voyager.md", "empty-type.md"} {
		if !bytes.Equal(before[rel], after[rel]) {
			t.Errorf("%s was modified; it is not a classifiable record and must be left exactly as it was.\nbefore: %q\nafter:  %q",
				rel, before[rel], after[rel])
		}
	}

	// The undeclared type is REPORTED, which is the half that makes the
	// refusal usable — an operator cannot fix a schema nobody told them was
	// missing.
	if got := rep.UndeclaredType["spaceship"]; got != 1 {
		t.Errorf("UndeclaredType[spaceship] = %d, want 1 — the note that declares a type this vault has no schema for must be named", got)
	}
	if rep.NotRecords != 3 {
		t.Errorf("NotRecords = %d, want 3 (scratch.md, meeting.md, empty-type.md)", rep.NotRecords)
	}
}

// TestStamp_StampsARecordWhoseValueIsOutsideItsEnum separates identity from
// validity. `status: exploded` is not a legal value, and the note is still a
// company — refusing it an identifier would make an invalid record
// permanently uneditable, which is the exact opposite of the fix.
func TestStamp_StampsARecordWhoseValueIsOutsideItsEnum(t *testing.T) {
	root := messyVault(t)
	rep := stampFixture(t, root, true)

	id := stampedIDFor(t, rep, "invalid-enum.md")
	if !strings.HasPrefix(id, "CO-") {
		t.Errorf("id %q does not carry the company prefix", id)
	}
	data, err := os.ReadFile(filepath.Join(root, "invalid-enum.md"))
	if err != nil {
		t.Fatalf("reading the note: %v", err)
	}
	if !strings.Contains(string(data), "status: exploded") {
		t.Error("the out-of-enum value was altered; stamping must not repair or normalise a value")
	}
}

// TestStamp_NeverTouchesANoteThatAlreadyHasAnID is the idempotence guarantee's
// load-bearing half, and also the collision case: globex.md holds CO-0001,
// which is the first identifier the allocator would otherwise hand out.
func TestStamp_NeverTouchesANoteThatAlreadyHasAnID(t *testing.T) {
	root := messyVault(t)
	before := snapshotVault(t, root)

	rep := stampFixture(t, root, true)
	after := snapshotVault(t, root)

	if !bytes.Equal(before["globex.md"], after["globex.md"]) {
		t.Errorf("globex.md was rewritten; a note that already has an identifier must never be touched.\nbefore: %q\nafter:  %q",
			before["globex.md"], after["globex.md"])
	}
	if rep.AlreadyIdentified != 1 {
		t.Errorf("AlreadyIdentified = %d, want 1", rep.AlreadyIdentified)
	}

	// No newly minted identifier may equal the one already on disk.
	for _, st := range rep.Stamped {
		if st.ID == "CO-0001" {
			t.Fatalf("%s was minted CO-0001, which globex.md already holds — the D7 uniqueness invariant is broken", st.RelPath)
		}
	}
	if len(rep.Collisions) == 0 {
		t.Error("the allocator advanced past CO-0001 without reporting the collision; an unexplained counter jump is what that report exists to explain")
	}
}

// ---------------------------------------------------------------------------
// Idempotence and dry run
// ---------------------------------------------------------------------------

// TestStamp_IsIdempotent runs the pass twice and requires the second run to
// change nothing at all — the founder's explicit requirement for the repair
// path.
func TestStamp_IsIdempotent(t *testing.T) {
	root := messyVault(t)

	first := stampFixture(t, root, true)
	if len(first.Stamped) == 0 {
		t.Fatal("the first run stamped nothing, so this test cannot prove idempotence")
	}
	afterFirst := snapshotVault(t, root)

	second := stampFixture(t, root, true)
	afterSecond := snapshotVault(t, root)

	if len(second.Stamped) != 0 {
		t.Errorf("the second run stamped %d note(s); it must find nothing to do", len(second.Stamped))
	}
	for rel, want := range afterFirst {
		if !bytes.Equal(want, afterSecond[rel]) {
			t.Errorf("%s changed on the second run.\nafter run 1: %q\nafter run 2: %q", rel, want, afterSecond[rel])
		}
	}
	if second.AlreadyIdentified != first.AlreadyIdentified+len(first.Stamped) {
		t.Errorf("AlreadyIdentified = %d on the second run, want %d — every note the first run stamped must now count as already identified",
			second.AlreadyIdentified, first.AlreadyIdentified+len(first.Stamped))
	}
}

// TestStamp_DryRunWritesNothingButNamesTheIdentifiers holds both halves of the
// --dry-run promise. Reporting "N notes would be stamped" without saying with
// WHAT is the weaker answer, so the dry run runs the real allocator and names
// the identifiers a real run produces — while leaving every byte alone.
func TestStamp_DryRunWritesNothingButNamesTheIdentifiers(t *testing.T) {
	root := messyVault(t)
	before := snapshotVault(t, root)

	dry := stampFixture(t, root, false)
	after := snapshotVault(t, root)

	if !dry.DryRun {
		t.Error("the report does not declare itself a dry run")
	}
	if len(dry.Stamped) == 0 {
		t.Fatal("the dry run reported nothing to stamp")
	}
	for rel, want := range before {
		if !bytes.Equal(want, after[rel]) {
			t.Errorf("--dry-run modified %s.\nbefore: %q\nafter:  %q", rel, want, after[rel])
		}
	}
	for _, st := range dry.Stamped {
		if st.Written {
			t.Errorf("%s is reported as Written on a dry run", st.RelPath)
		}
		if st.ID == "" {
			t.Errorf("%s would be stamped with no identifier named", st.RelPath)
		}
	}

	// The counter must not have advanced either, or a dry run silently burns
	// identifiers. The real run that follows must produce the SAME ones.
	wet := stampFixture(t, root, true)
	if len(wet.Stamped) != len(dry.Stamped) {
		t.Fatalf("the real run stamped %d notes, the dry run predicted %d", len(wet.Stamped), len(dry.Stamped))
	}
	for i := range wet.Stamped {
		if wet.Stamped[i].RelPath != dry.Stamped[i].RelPath || wet.Stamped[i].ID != dry.Stamped[i].ID {
			t.Errorf("dry run predicted %s -> %s but the real run wrote %s -> %s",
				dry.Stamped[i].RelPath, dry.Stamped[i].ID, wet.Stamped[i].RelPath, wet.Stamped[i].ID)
		}
	}
}

// ---------------------------------------------------------------------------
// The prefix-less schema
// ---------------------------------------------------------------------------

// TestStamp_SchemaWithNoIdentityPrefixMintsABareZeroPaddedNumber pins the
// documented decision for a schema that declares no `identity:` block. The
// fallback is not an accident of formatting: identity_prefix is optional, and
// "no prefix" must not become a second way to end up with no identifier.
func TestStamp_SchemaWithNoIdentityPrefixMintsABareZeroPaddedNumber(t *testing.T) {
	root := messyVault(t)
	rep := stampFixture(t, root, true)

	id := stampedIDFor(t, rep, "widget.md")
	if id != "0001" {
		t.Errorf("widget.md was minted %q, want %q — a schema with no identity prefix mints a bare zero-padded number", id, "0001")
	}
	if strings.HasPrefix(id, "-") {
		t.Errorf("id %q starts with a bare separator; the prefix-less form must not leave a dangling dash", id)
	}

	data, err := os.ReadFile(filepath.Join(root, "widget.md"))
	if err != nil {
		t.Fatalf("reading widget.md: %v", err)
	}
	// It must read back as the STRING "0001", not the integer 1 — an
	// identifier that YAML re-reads as a number stops matching the id the SPA
	// holds.
	rec := records.ParseRecord("widget.md", data)
	if got := rec.ID(); got != "0001" {
		t.Errorf("widget.md reads back id %q, want %q — the value was not quoted and YAML re-read it", got, "0001")
	}
}

// ---------------------------------------------------------------------------
// The importer path
// ---------------------------------------------------------------------------

// TestImport_StampsIdentifiersDuringImport is the founder's first ask: a vault
// imported by `records import-obsidian` comes out with identifiers on it. This
// is the regression that matters most — it is the exact state the founder's
// own 766-note vault was left in, where every row had id: null and the whole
// inline-editing feature was invisible.
func TestImport_StampsIdentifiersDuringImport(t *testing.T) {
	root := messyVault(t)
	// Remove the pre-written schemas: a real import INFERS them, and the
	// identifiers must be minted against the schemas the import itself wrote.
	if err := os.RemoveAll(records.SchemaDir(root)); err != nil {
		t.Fatalf("clearing the schema directory: %v", err)
	}

	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if len(rep.IdentityStamps.Stamped) == 0 {
		t.Fatal("the import stamped NO identifiers — this is the defect the change exists to fix")
	}

	// Assert against the FILES, not the report: the report claiming a stamp
	// and the note not carrying one is precisely the false-green this must
	// rule out.
	for _, rel := range []string{"acme.md", "invalid-enum.md", "bom.md", "crlf.md"} {
		data, rerr := os.ReadFile(filepath.Join(root, rel))
		if rerr != nil {
			t.Fatalf("reading %s: %v", rel, rerr)
		}
		if id := records.ParseRecord(rel, data).ID(); id == "" {
			t.Errorf("%s has no id on disk after the import", rel)
		}
	}

	// And the import's own validation pass must see the identifiers it wrote,
	// or the report contradicts the files on disk.
	if rep.Validation.RecognisedRecords == 0 {
		t.Error("the import recognised no records at all")
	}

	// THE IMPORT AND REPAIR PATHS DIFFER HERE, AND THE DIFFERENCE IS CORRECT.
	//
	// voyager.md declares `type: spaceship`, which no schema declared BEFORE
	// this run. The importer's job is to INFER a schema for every `type:` it
	// finds, so by the time stamping happens `spaceship` is a declared record
	// type and the note is stamped. That is not "stamping a note whose type
	// could not be determined" — the type was determined, by inference, and
	// the inferred schema is written to disk beside it.
	//
	// The repair path (StampVault) has no inference step, so the SAME note in
	// a vault whose schemas do not mention `spaceship` is reported and left
	// alone — see TestStamp_NeverStampsWhatItCannotClassify.
	//
	// Pinned because the two look contradictory from the outside, and
	// "correcting" either one to match the other would break a real
	// requirement.
	voyager, err := os.ReadFile(filepath.Join(root, "voyager.md"))
	if err != nil {
		t.Fatalf("reading voyager.md: %v", err)
	}
	if id := records.ParseRecord("voyager.md", voyager).ID(); id == "" {
		t.Error("voyager.md was not stamped; the importer inferred a schema for its type, so it IS a record of a declared type")
	}
	if _, err := os.Stat(filepath.Join(records.SchemaDir(root), "spaceship.yaml")); err != nil {
		t.Errorf("the import did not write a schema for the inferred type, so stamping its notes would have been a guess: %v", err)
	}
}

// TestImport_DryRunStampsNothing holds the --dry-run promise through the
// importer, not merely through StampVault.
func TestImport_DryRunStampsNothing(t *testing.T) {
	root := messyVault(t)
	if err := os.RemoveAll(records.SchemaDir(root)); err != nil {
		t.Fatalf("clearing the schema directory: %v", err)
	}
	before := snapshotVault(t, root)

	rep, err := Run(root, false)
	if err != nil {
		t.Fatalf("dry-run import failed: %v", err)
	}
	after := snapshotVault(t, root)

	for rel, want := range before {
		if !bytes.Equal(want, after[rel]) {
			t.Errorf("a dry-run import modified %s.\nbefore: %q\nafter:  %q", rel, want, after[rel])
		}
	}
	if len(rep.IdentityStamps.Stamped) == 0 {
		t.Error("the dry-run import reported no identifiers it would stamp")
	}
}

// TestStampVault_RefusesAnUnimportedVault covers the state that looks
// identical to "nothing to do" in a count and needs a completely different
// action from the operator.
func TestStampVault_RefusesAnUnimportedVault(t *testing.T) {
	root := messyVault(t)
	if err := os.RemoveAll(records.SchemaDir(root)); err != nil {
		t.Fatalf("clearing the schema directory: %v", err)
	}

	_, err := StampVault(root, Options{Write: true})
	if err == nil {
		t.Fatal("StampVault accepted a vault with no record schemas; it must refuse rather than report a cheerful zero")
	}
	if !strings.Contains(err.Error(), "import-obsidian") {
		t.Errorf("the refusal does not name the action that would fix it: %v", err)
	}
}

// TestStampReport_RendersTheZeroCase proves the section is printed even when
// nothing was stamped. A vault where no note has an identifier looks exactly
// like a healthy one from outside; a silent section is how that stays a
// mystery.
func TestStampReport_RendersTheZeroCase(t *testing.T) {
	var buf bytes.Buffer
	IdentityStampReport{UndeclaredType: map[string]int{}, TypeFailures: map[string]string{}}.Render(&buf, 0)
	out := buf.String()
	if !strings.Contains(out, "record identifier") {
		t.Errorf("the identifier section was not rendered for an empty report:\n%s", out)
	}
	if !strings.Contains(out, "0 note(s)") {
		t.Errorf("the empty report does not state the zero explicitly:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// stampedIDFor returns the identifier the report says was written to rel.
func stampedIDFor(t *testing.T, rep IdentityStampReport, rel string) string {
	t.Helper()
	for _, st := range rep.Stamped {
		if st.RelPath == rel {
			return st.ID
		}
	}
	t.Fatalf("%s was not stamped; report stamped %d note(s): %+v", rel, len(rep.Stamped), rep.Stamped)
	return ""
}

// removeOneLine deletes every line whose trimmed form equals want, returning
// how many it removed and the remaining text.
func removeOneLine(t *testing.T, src, want string) (int, string) {
	t.Helper()
	// Split on \n but keep \r, so a CRLF file round-trips through this helper
	// unchanged and the byte comparison stays honest.
	lines := strings.SplitAfter(src, "\n")
	var kept []string
	removed := 0
	for _, l := range lines {
		if strings.TrimRight(l, "\r\n") == want {
			removed++
			continue
		}
		kept = append(kept, l)
	}
	return removed, strings.Join(kept, "")
}

// stripLinesWithKey removes every frontmatter line whose key is key,
// returning how many were removed and the remaining text. Used by the
// minimal-edit assertions to take the identifier line back out before
// comparing bytes.
func stripLinesWithKey(src, key string) (int, string) {
	lines := strings.SplitAfter(src, "\n")
	var kept []string
	removed := 0
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimRight(l, "\r\n"), key+":") {
			removed++
			continue
		}
		kept = append(kept, l)
	}
	return removed, strings.Join(kept, "")
}

// frontmatterKeyOrder returns the top-level frontmatter keys in file order.
func frontmatterKeyOrder(t *testing.T, src []byte) []string {
	t.Helper()
	var out []string
	inBlock := false
	for _, raw := range strings.Split(string(src), "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "---" {
			if inBlock {
				break
			}
			inBlock = true
			continue
		}
		if !inBlock || line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "-") {
			continue
		}
		if i := strings.Index(line, ":"); i > 0 {
			out = append(out, line[:i])
		}
	}
	return out
}

// startsWith reports whether got begins with the whole of want.
func startsWith(got, want []string) bool {
	if len(got) < len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
