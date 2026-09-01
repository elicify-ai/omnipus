// Omnipus — AdoptObservedDomains: a declaration nothing was observed for
// yields to one the data made.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// adoptionFixture builds a vault in which ONE record type observes a property
// and another carries the same property name with no value for it anywhere.
//
// The unobserved side is given a real note that omits the property rather than
// no notes at all, because those are different states and only this one
// exercises the rule: a type with no notes is FR-018d provisioned, and a type
// whose notes are silent about a property is the case the founder's
// `bank-account` is actually in.
func adoptionFixture(t *testing.T, observedValues []string) (map[string][]InferredProperty, []NoteRecord) {
	t.Helper()
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "b1.md", "---\ntype: beta\nname: B1\n---\n\nbody\n"),
		noteOnDisk(t, dir, "b2.md", "---\ntype: beta\nname: B2\n---\n\nbody\n"),
	}
	for i, v := range observedValues {
		notes = append(notes, noteOnDisk(t, dir, fmt.Sprintf("a%d.md", i),
			fmt.Sprintf("---\ntype: alpha\nstage: %s\n---\n\nbody\n", v)))
	}
	groups := CollectTypeGroups(notes)
	names := BuildNameIndex(notes)
	inferred := map[string][]InferredProperty{}
	for typeName, g := range groups {
		inferred[typeName] = InferSchema(g, names)
	}
	// `beta` does not carry `stage` in any note; a template donated the name,
	// which is what leaves it standing on the text fallback.
	inferred["beta"] = append(inferred["beta"], InferredProperty{
		Name: "stage", Type: records.TypeText,
	})
	return inferred, notes
}

// TestAdoptObservedDomains_UnobservedYieldsToObserved is the rule working.
func TestAdoptObservedDomains_UnobservedYieldsToObserved(t *testing.T) {
	inferred, notes := adoptionFixture(t, []string{"draft", "review", "final"})

	// The precondition, asserted rather than assumed: without it an adoption
	// that never happened and an adoption that was unnecessary look identical.
	if p, _ := findInferredProperty(inferred["alpha"], "stage"); p.Type != records.TypeEnum {
		t.Fatalf("fixture not exercising the rule: alpha.stage is %q, want enum", p.Type)
	}
	if p, _ := findInferredProperty(inferred["beta"], "stage"); p.Type != records.TypeText {
		t.Fatalf("fixture not exercising the rule: beta.stage is %q, want the text fallback", p.Type)
	}

	adopted, declined := AdoptObservedDomains(inferred, notes)

	got, _ := findInferredProperty(inferred["beta"], "stage")
	if got.Type != records.TypeEnum {
		t.Fatalf("beta.stage stayed %q — a fallback nothing was observed for outranked three real observations, which is the split that costs the founder's untyped views their columns", got.Type)
	}
	if len(adopted) != 1 || adopted[0].RecordType != "beta" || adopted[0].Property != "stage" {
		t.Errorf("the adoption was not accounted for: adopted=%+v", adopted)
	}
	if len(declined) != 0 {
		t.Errorf("nothing should have been declined here: %+v", declined)
	}
	// The vocabulary has to come across too. An enum whose value set did not
	// follow is a domain match on paper that rejects every real value.
	if len(got.EnumValues) != 3 {
		t.Errorf("the adopted enum carries %d values, want the 3 observed: %v", len(got.EnumValues), got.EnumValues)
	}
}

// TestAdoptObservedDomains_DeclinesAVocabularyTooLargeToBeClosed is the half
// that keeps this rule honest, and it is the half the founder's own vault
// exercises: `status` there holds twenty-five distinct values across eighteen
// record types, which is not one closed vocabulary and must not be adopted as
// one.
func TestAdoptObservedDomains_DeclinesAVocabularyTooLargeToBeClosed(t *testing.T) {
	// THE UNION IS BUILT FROM SEVERAL SMALL VOCABULARIES, not one large one,
	// and that is the whole construction rather than a detail. A single type
	// with seventeen distinct values is not an enum at all — it fails the
	// enum rule on its own and the observed domain becomes `text`, so nothing
	// is ever offered for adoption and the decline branch is never reached.
	// An earlier draft of this test did exactly that and reported the decline
	// as missing when the rule had simply never been asked.
	//
	// This is also the founder's real shape: eighteen record types each with a
	// short, sensible `status` vocabulary of its own, whose UNION is
	// twenty-five values and is not a vocabulary at all.
	dir := t.TempDir()
	var notes []NoteRecord
	notes = append(notes,
		noteOnDisk(t, dir, "b1.md", "---\ntype: beta\nname: B1\n---\n\nbody\n"),
		noteOnDisk(t, dir, "b2.md", "---\ntype: beta\nname: B2\n---\n\nbody\n"),
	)
	// Four observing types, five values each, no overlap: each is comfortably
	// an enum on its own; together they are twenty, past the bound.
	perType := 5
	types := []string{"alpha", "gamma", "delta", "epsilon"}
	for ti, rt := range types {
		for i := 0; i < perType; i++ {
			notes = append(notes, noteOnDisk(t, dir,
				fmt.Sprintf("%s%d.md", rt, i),
				fmt.Sprintf("---\ntype: %s\nstage: %s_v%d\n---\n\nbody\n", rt, rt, i)))
			_ = ti
		}
	}
	groups := CollectTypeGroups(notes)
	names := BuildNameIndex(notes)
	inferred := map[string][]InferredProperty{}
	for typeName, g := range groups {
		inferred[typeName] = InferSchema(g, names)
	}
	inferred["beta"] = append(inferred["beta"], InferredProperty{Name: "stage", Type: records.TypeText})

	// The preconditions, asserted: every observing type must genuinely be an
	// enum, and their union must genuinely exceed the bound. Either one false
	// and this test is measuring something else.
	union := map[string]bool{}
	for _, rt := range types {
		p, ok := findInferredProperty(inferred[rt], "stage")
		if !ok || p.Type != records.TypeEnum {
			t.Fatalf("fixture not exercising the rule: %s.stage is %q, want enum", rt, p.Type)
		}
		for _, v := range p.EnumValues {
			union[records.FoldKey(v)] = true
		}
	}
	if len(union) <= enumMaxDistinct {
		t.Fatalf("fixture not exercising the rule: the union is %d values, within the %d bound, so nothing would decline", len(union), enumMaxDistinct)
	}

	adopted, declined := AdoptObservedDomains(inferred, notes)

	got, _ := findInferredProperty(inferred["beta"], "stage")
	if got.Type != records.TypeText {
		t.Fatalf("beta.stage was adopted as %q although the observed union is %d values, past the %d bound past which this package stops calling a set closed",
			got.Type, len(union), enumMaxDistinct)
	}
	for _, a := range adopted {
		if a.RecordType == "beta" && a.Property == "stage" {
			t.Fatalf("beta.stage is listed as adopted: %+v", a)
		}
	}
	// A refusal nobody is told about is indistinguishable from the rule never
	// having looked, which is why this package accounts for its declines as
	// well as its decisions.
	var found bool
	for _, d := range declined {
		if d.RecordType == "beta" && d.Property == "stage" {
			found = true
			if d.UnionSize <= d.Bound {
				t.Errorf("the decline is recorded with UnionSize=%d and Bound=%d, which would not have declined anything", d.UnionSize, d.Bound)
			}
		}
	}
	if !found {
		t.Errorf("the decline was never accounted for: declined=%+v", declined)
	}
}

// TestAdoptObservedDomains_NeverOverridesTheTypesOwnData is the containment,
// and it is the same sentence the two base-file rules turn on: data beats a
// base file, as data beats another type's data.
func TestAdoptObservedDomains_NeverOverridesTheTypesOwnData(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "a1.md", "---\ntype: alpha\nstage: draft\n---\n\nbody\n"),
		noteOnDisk(t, dir, "a2.md", "---\ntype: alpha\nstage: review\n---\n\nbody\n"),
	}
	// SEVEN DISTINCT PROSE VALUES, and the count is load-bearing exactly as it
	// is in the placeholder test next door. Two prose values are still a small
	// enough set to be read as an ENUM, and an earlier draft of this test used
	// two: adoption then replaced beta's vocabulary with alpha's while leaving
	// the declared TYPE at `enum` either way, so an assertion comparing types
	// passed with the containment clause deleted. Seven lands on text, where
	// an adoption is visible as a type change, and the values are compared as
	// well so a vocabulary swap cannot hide inside a matching type.
	prose := []string{
		"waiting on the bank to confirm the wire",
		"blocked until the lease is countersigned",
		"paused while the auditor is on leave",
		"with the founder for a final read",
		"held pending the currency correction",
		"queued behind the Q3 close",
		"open — no owner assigned yet",
	}
	for i, v := range prose {
		notes = append(notes, noteOnDisk(t, dir, fmt.Sprintf("b%d.md", i),
			fmt.Sprintf("---\ntype: beta\nstage: %s\n---\n\nbody\n", v)))
	}
	groups := CollectTypeGroups(notes)
	names := BuildNameIndex(notes)
	inferred := map[string][]InferredProperty{}
	for typeName, g := range groups {
		inferred[typeName] = InferSchema(g, names)
	}

	before, _ := findInferredProperty(inferred["beta"], "stage")
	if before.Type != records.TypeText {
		t.Fatalf("fixture not exercising the rule: beta.stage is %q, not text, so an adoption would not be visible as a type change", before.Type)
	}
	if p, _ := findInferredProperty(inferred["alpha"], "stage"); p.Type != records.TypeEnum {
		t.Fatalf("fixture not exercising the rule: alpha.stage is %q, so there is no domain on offer to refuse", p.Type)
	}

	AdoptObservedDomains(inferred, notes)

	after, _ := findInferredProperty(inferred["beta"], "stage")
	if after.Type != before.Type {
		t.Fatalf("beta.stage changed from %q to %q although beta's own notes carry values for it — those notes are now invalid under a schema derived from the vault that contains them",
			before.Type, after.Type)
	}
	if len(after.EnumValues) != len(before.EnumValues) {
		t.Fatalf("beta.stage kept its type but its vocabulary was replaced (%v -> %v); beta's own notes are the evidence for this property and they are the ones that must stand",
			before.EnumValues, after.EnumValues)
	}
}

// TestAdoptObservedDomains_TheAccountReachesTheFileAnOperatorWouldOpen is the
// honesty half. Every guess this package makes is acceptable because it is
// reported and correctable in one edit, and the report for this one is the
// schema file the property is declared in — not a run summary scrolled past
// once.
func TestAdoptObservedDomains_TheAccountReachesTheFileAnOperatorWouldOpen(t *testing.T) {
	inferred, notes := adoptionFixture(t, []string{"draft", "review", "final"})
	AdoptObservedDomains(inferred, notes)

	raw, err := RenderSchemaYAML("beta", inferred["beta"])
	if err != nil {
		t.Fatalf("RenderSchemaYAML: %v", err)
	}
	yaml := string(raw)
	if !strings.Contains(yaml, "alpha") {
		t.Errorf("beta.yaml does not name the record type whose data decided this, so an operator who thinks it wrong cannot find what to correct:\n%s", yaml)
	}
	if !strings.Contains(yaml, "stage") {
		t.Errorf("beta.yaml does not mention the adopted property at all:\n%s", yaml)
	}
}
