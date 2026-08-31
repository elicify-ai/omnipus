// Omnipus — the suite for A TEMPLATE NOTE IS A PROPERTY DECLARATION.
//
// The rule under test: a note the operator wrote as `type: template` with
// `template_type: <T>` is their own statement of what a `T` note carries, so
// every key it declares is a property of T — even the ones no real T note has
// ever been given.
//
// Both directions are tested, because a rule that only ever ADDS is a rule
// nobody has checked the limits of:
//
//   - It adds a property name no note of the type carries (the whole point).
//   - It NEVER touches a property real notes do carry, never contributes a
//     VALUE, never invents a record type, and never makes anything required.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// templateEvidenceFixture builds a vault of `bodies` (frontmatter fragments,
// each becoming one note of recordType) plus one `type: template` note whose
// frontmatter is templateBody, and returns the inferred declarations for
// recordType keyed by property name.
//
// It returns the whole group as well, so a test can assert on the OBSERVATION
// (DeclaredCount, PresentNonEmptyCount) and not only on the declaration the
// observation produced.
func templateEvidenceFixture(t *testing.T, recordType, templateBody string, bodies ...string) (map[string]InferredProperty, *TypeGroup, map[string]*TypeGroup) {
	t.Helper()
	dir := t.TempDir()
	notes := make([]NoteRecord, 0, len(bodies)+1)
	for i, body := range bodies {
		notes = append(notes, noteOnDisk(t, dir, noteName(i),
			"---\ntype: "+recordType+"\n"+body+"---\n\nbody\n"))
	}
	if templateBody != "" {
		notes = append(notes, noteOnDisk(t, dir, "Template.md",
			"---\ntype: template\n"+templateBody+"---\n\nbody\n"))
	}
	groups := CollectTypeGroups(notes)
	g := groups[recordType]
	out := map[string]InferredProperty{}
	if g != nil {
		for _, p := range InferSchema(g, BuildNameIndex(notes)) {
			out[p.Name] = p
		}
	}
	return out, g, groups
}

func noteName(i int) string {
	return string(rune('a'+i)) + ".md"
}

// TestTemplateNote_DeclaresAPropertyNoRealNoteCarries is the founder's own
// `Template — legal-entity.md`, reduced to the two properties that cost him
// two views: `last_refreshed` and `registration_renewal_date` are written in
// the template and left blank, and no legal-entity note carries either.
func TestTemplateNote_DeclaresAPropertyNoRealNoteCarries(t *testing.T) {
	got, g, _ := templateEvidenceFixture(t, "legal-entity",
		"company: Acme\njurisdiction: SG\nlast_refreshed:\nregistration_renewal_date:\ntemplate_type: legal-entity\ntemplate_kind: infra\n",
		"company: \"[[Acme]]\"\njurisdiction: SG\n",
		"company: \"[[Beta]]\"\njurisdiction: UK\n",
	)

	for _, name := range []string{"last_refreshed", "registration_renewal_date"} {
		p, ok := got[name]
		if !ok {
			t.Fatalf("%q is not declared on legal-entity — the template note names it, so the operator has already said it exists (declared: %v)", name, sortedInferredNames(got))
		}
		if p.Required {
			t.Errorf("%q was declared REQUIRED — no legal-entity note carries a value for it, so requiring it would make every existing note invalid", name)
		}
		if p.Many {
			t.Errorf("%q was declared many=true — the template carries no list evidence, only the key", name)
		}
		if p.ObservedCount != 0 {
			t.Errorf("%q has ObservedCount=%d, want 0 — a template is not a note of the type and must not be counted as one", name, p.ObservedCount)
		}
	}

	// The TYPE of each follows the existing zero-values rule and nothing new:
	// a `_date` suffix is name-evidence for a date, `last_refreshed` is not
	// (dateNameExact rejects it by name, deliberately).
	if got["registration_renewal_date"].Type != records.TypeDate {
		t.Errorf("registration_renewal_date declared %q, want date — with no values at all classifyWithNoValues reads the name, and `_date` is on its closed list",
			got["registration_renewal_date"].Type)
	}
	if got["registration_renewal_date"].NameEvidenced == nil {
		t.Error("registration_renewal_date was typed from its name with no NameEvidenced payload — an unreported guess is exactly what this package refuses")
	}
	if got["last_refreshed"].Type != records.TypeText {
		t.Errorf("last_refreshed declared %q, want text — its name is on dateNameExact's REJECTED list, so nothing here types it", got["last_refreshed"].Type)
	}

	// The template must not have been counted as a legal-entity note.
	if g.NoteCount != 2 {
		t.Errorf("legal-entity NoteCount=%d, want 2 — the template note is a `template`, not a legal-entity, and counting it would deflate every `required` on the type", g.NoteCount)
	}
}

// TestTemplateNote_NeverOverridesAnObservedProperty is the guard rail. Real
// notes always decide a property the template also names — the template
// contributes a NAME and never a value, an arity or a count.
func TestTemplateNote_NeverOverridesAnObservedProperty(t *testing.T) {
	got, g, _ := templateEvidenceFixture(t, "connected-account",
		// The founder's real connected-account template writes `status:
		// active` — a DEFAULT, not an observation. If that value were taken
		// as evidence it would land in the enum.
		"platform:\nstatus: active\ntemplate_type: connected-account\n",
		"platform: github\nstatus: live\n",
		"platform: slack\nstatus: live\n",
		"platform: x\nstatus: revoked\n",
	)

	status := got["status"]
	if status.Type != records.TypeEnum {
		t.Fatalf("status declared %q, want enum — three notes carry two repeated values", status.Type)
	}
	for _, v := range status.EnumValues {
		if v == "active" {
			t.Errorf("the enum for status contains %q, which appears ONLY in the template's default — a template default is not an observation, and admitting it widens the closed set on no evidence (values: %v)", v, status.EnumValues)
		}
	}
	if status.ObservedCount != 3 {
		t.Errorf("status ObservedCount=%d, want 3 — the template must not inflate the count `required` and FR-104b's tie-break are computed from", status.ObservedCount)
	}
	if po := g.Props["status"]; po != nil && po.DeclaredCount != 3 {
		t.Errorf("status DeclaredCount=%d, want 3 — the template declares the key too, but it is not a note of this type", po.DeclaredCount)
	}
}

// TestTemplateNote_DoesNotInventARecordType keeps this rule strictly to
// AUGMENTING a type real notes carry. Declaring a whole type from a file that
// is not a note of it is FR-018d provisioning's job, it is decided from the
// `.base` files, and two rules racing to create the same type would be two
// answers to one question.
func TestTemplateNote_DoesNotInventARecordType(t *testing.T) {
	_, _, groups := templateEvidenceFixture(t, "project",
		"target:\nstage:\ntemplate_type: round\n",
		"stage: live\n",
	)
	if g, ok := groups["round"]; ok {
		t.Errorf("a type group was created for %q from a template alone (%d notes, props %v) — no note carries `type: round`, and creating the type here would collide with FR-018d provisioning", "round", g.NoteCount, g.PropOrder)
	}
}

// TestTemplateNote_ScaffoldingKeysAreNotProperties: `template_type` and
// `template_kind` describe the TEMPLATE, not the record it templates. Copying
// them onto the target type would declare two properties no note of that type
// will ever carry, and they would then be offered as filterable columns.
func TestTemplateNote_ScaffoldingKeysAreNotProperties(t *testing.T) {
	got, _, _ := templateEvidenceFixture(t, "invoice",
		"amount:\ntemplate_type: invoice\ntemplate_kind: infra\n",
		"client: \"[[Acme]]\"\n",
	)
	if _, ok := got["amount"]; !ok {
		t.Fatalf("the fixture did not exercise the rule at all — `amount` should have been carried across (declared: %v)", sortedInferredNames(got))
	}
	for _, k := range []string{"template_type", "template_kind"} {
		if _, ok := got[k]; ok {
			t.Errorf("%q was declared as a property of invoice — it describes the template file, not an invoice", k)
		}
	}
}

// TestTemplateNote_OnlyATypeTemplateNoteSpeaks: `template_type` on a note
// that is NOT a template says nothing. The rule reads a deliberate pair of
// keys the operator wrote together, not one key wherever it turns up.
func TestTemplateNote_OnlyATypeTemplateNoteSpeaks(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "a.md", "---\ntype: invoice\nclient: \"[[Acme]]\"\n---\n\nbody\n"),
		// A `project` note that happens to carry `template_type: invoice`.
		noteOnDisk(t, dir, "b.md", "---\ntype: project\ntemplate_type: invoice\nsmuggled:\n---\n\nbody\n"),
	}
	groups := CollectTypeGroups(notes)
	if _, ok := groups["invoice"].Props["smuggled"]; ok {
		t.Error("a non-template note carrying `template_type:` donated a property to another record type — the rule must require `type: template` as well, or any note can rewrite any schema")
	}
}

// TestTemplateNote_BlankTemplateWithNoTypeIsInert: a template that names no
// `template_type` has nothing to attach its keys to and must be left alone.
func TestTemplateNote_BlankTemplateWithNoTypeIsInert(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "a.md", "---\ntype: invoice\nclient: \"[[Acme]]\"\n---\n\nbody\n"),
		noteOnDisk(t, dir, "t.md", "---\ntype: template\nfloating:\ntemplate_type:\n---\n\nbody\n"),
	}
	groups := CollectTypeGroups(notes)
	if _, ok := groups["invoice"].Props["floating"]; ok {
		t.Error("a template with a BLANK `template_type` donated a property to invoice — it names no target, so it must reach no type at all")
	}
}

func sortedInferredNames(m map[string]InferredProperty) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ---------------------------------------------------------------------------
// THE PROVENANCE OF A NAME-EVIDENCED GUESS
//
// Before templates were read, a property typed from its name had exactly one
// possible history: notes of the type declared the key and every one left it
// blank. report.go's renderNameEvidenced states that as the section's premise
// and CHECKS every entry against it, printing a CONTRADICTION when an entry
// claims zero declaring notes.
//
// Reading templates created a second, legitimate history — no note declares
// the key, the TEMPLATE does — and the first fixture run after that change
// printed exactly the contradiction the peer's guard exists to catch:
//
//	legal-entity.registration_renewal_date -> date (declared by 0 note(s), every one blank)
//	CONTRADICTION — no note declares `registration_renewal_date` at all ...
//
// The guard was right and the payload was wrong. These tests pin the payload
// carrying WHICH evidence it rests on, so the sentence the operator reads is
// true of the entry it is printed under.
// ---------------------------------------------------------------------------

// nameEvidencedFor runs the same fixture as above and returns the one
// name-evidenced inference for a property, or fails.
func nameEvidencedFor(t *testing.T, got map[string]InferredProperty, prop string) NameEvidencedInference {
	t.Helper()
	p, ok := got[prop]
	if !ok {
		t.Fatalf("%q was not declared at all", prop)
	}
	if p.NameEvidenced == nil {
		t.Fatalf("%q was declared %q with no NameEvidenced payload", prop, p.Type)
	}
	return *p.NameEvidenced
}

func TestNameEvidenced_TemplateProvenanceIsNamedAndIsNotAContradiction(t *testing.T) {
	got, _, _ := templateEvidenceFixture(t, "legal-entity",
		"registration_renewal_date:\ntemplate_type: legal-entity\n",
		"company: \"[[Acme]]\"\n",
		"company: \"[[Beta]]\"\n",
	)
	ne := nameEvidencedFor(t, got, "registration_renewal_date")

	if ne.DeclaringNotes != 0 {
		t.Errorf("DeclaringNotes=%d, want 0 — no legal-entity note writes this key; only the template does, and inflating this count would launder the template into a note", ne.DeclaringNotes)
	}
	if len(ne.DeclaringTemplates) != 1 || ne.DeclaringTemplates[0] != "Template.md" {
		t.Fatalf("DeclaringTemplates=%v, want exactly [Template.md] — the guess rests on that file and the operator has to be told which one", ne.DeclaringTemplates)
	}

	lines := ne.ReportLines()
	if len(lines) == 0 {
		t.Fatal("ReportLines() is empty")
	}
	if !strings.Contains(lines[0], "Template.md") {
		t.Errorf("the entry line does not name the template it rests on:\n  %s", lines[0])
	}
	if strings.Contains(lines[0], "declared by 0 note") {
		t.Errorf("the entry line still claims notes declared the key:\n  %s", lines[0])
	}
	for _, l := range lines {
		if strings.Contains(l, "CONTRADICTION") {
			t.Errorf("a template-evidenced entry reports a CONTRADICTION, but its premise holds — the template really does declare the key:\n  %s", l)
		}
	}
}

func TestNameEvidenced_NoteProvenanceStillReadsAsItAlwaysDid(t *testing.T) {
	// No template at all: the original history, unchanged.
	got, _, _ := templateEvidenceFixture(t, "project", "",
		"deadline:\nstage: live\n",
		"deadline:\nstage: done\n",
	)
	ne := nameEvidencedFor(t, got, "deadline")

	if ne.DeclaringNotes != 2 {
		t.Errorf("DeclaringNotes=%d, want 2 — both project notes write the key and leave it blank", ne.DeclaringNotes)
	}
	if len(ne.DeclaringTemplates) != 0 {
		t.Errorf("DeclaringTemplates=%v, want empty — no template was involved", ne.DeclaringTemplates)
	}
	lines := ne.ReportLines()
	if !strings.Contains(lines[0], "2 note(s)") {
		t.Errorf("the entry line lost the note count it always carried:\n  %s", lines[0])
	}
	for _, l := range lines {
		if strings.Contains(l, "CONTRADICTION") {
			t.Errorf("an ordinary note-evidenced entry reports a CONTRADICTION:\n  %s", l)
		}
	}
}

// TestNameEvidenced_NoEvidenceAtAllIsStillACONTRADICTION keeps the peer's
// guard alive. Moving the premise check into this payload must not quietly
// retire it: an entry resting on NOTHING — no note, no template — is the
// impossible case, and it must still say so.
func TestNameEvidenced_NoEvidenceAtAllIsStillACONTRADICTION(t *testing.T) {
	ne := NameEvidencedInference{
		RecordType: "legal-entity",
		Property:   "phantom_date",
		Type:       records.TypeDate,
		// Nothing declared it. This cannot arise from CollectTypeGroups; it
		// is constructed here precisely because the check must survive a
		// future path that manages to produce it.
	}
	lines := ne.ReportLines()
	found := false
	for _, l := range lines {
		if strings.Contains(l, "CONTRADICTION") {
			found = true
		}
	}
	if !found {
		t.Errorf("an inference with NO declaring note and NO declaring template printed no CONTRADICTION — the premise of the whole section fails for it and the report must say so, not narrate it as fine:\n  %v", lines)
	}
}
