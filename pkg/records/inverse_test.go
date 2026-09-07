// Omnipus — tests for FindInverses (D5, FR-032).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import "testing"

// twoTypeSchema declares "bed" and "plant", with plant.bed as the ONE
// declared relation — its inverse "plants" is never written to bed's schema
// at all, which is the whole point of D5.
func twoTypeSchema(t *testing.T, inverseName string) *SchemaSet {
	t.Helper()
	root := writeVaultSchema(t, "", "bed.yaml", `
schema_version: 1
type: bed
label: Bed
identity:
  prefix: BED
properties:
  name: { type: text }
`)
	inverseLine := ""
	if inverseName != "" {
		inverseLine = ", inverse: " + inverseName
	}
	root = writeVaultSchema(t, root, "plant.yaml", `
schema_version: 1
type: plant
label: Plant
identity:
  prefix: PL
properties:
  bed: { type: relation, to: bed`+inverseLine+` }
`)
	set, report, err := LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("expected a clean load, got rejections: %v", report.Rejections)
	}
	return set
}

// TestFindInverses_DeclaredOnce is D5's own example: the inverse is declared
// on the relation's side and is resolvable from the TARGET type without bed's
// schema mentioning "plants" anywhere.
func TestFindInverses_DeclaredOnce(t *testing.T) {
	set := twoTypeSchema(t, "plants")

	bed, ok := set.Get("bed")
	if !ok {
		t.Fatalf("bed did not load")
	}
	if _, ok := bed.Property("plants"); ok {
		t.Fatalf("FR-032: bed's schema must never declare \"plants\" — the inverse is derived, not stored")
	}

	got := set.FindInverses("bed", "plants")
	if len(got) != 1 {
		t.Fatalf("FindInverses(bed, plants) = %d edges, want 1: %+v", len(got), got)
	}
	edge := got[0]
	if edge.SourceType != "plant" {
		t.Errorf("SourceType = %q, want %q", edge.SourceType, "plant")
	}
	if edge.Source == nil || edge.Source.Name != "bed" {
		t.Fatalf("Source = %+v, want the plant.bed property", edge.Source)
	}
	if got, want := edge.TargetType(), "bed"; got != want {
		t.Errorf("TargetType() = %q, want %q", got, want)
	}
	if got, want := edge.Name(), "plants"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

// TestFindInverses_UndeclaredNameIsEmpty is the FR-024 half: a name nobody
// declared as an inverse must not be silently resolved to something close.
func TestFindInverses_UndeclaredNameIsEmpty(t *testing.T) {
	set := twoTypeSchema(t, "plants")
	if got := set.FindInverses("bed", "flowers"); len(got) != 0 {
		t.Fatalf("FindInverses(bed, flowers) = %+v, want none — nothing declares this inverse", got)
	}
	// A relation with NO declared inverse contributes no derived name at all.
	bare := twoTypeSchema(t, "")
	if got := bare.FindInverses("bed", "plants"); len(got) != 0 {
		t.Fatalf("FindInverses on a relation with no `inverse:` = %+v, want none", got)
	}
}

// TestFindInverses_WrongTargetTypeIsEmpty proves the match is scoped to the
// TARGET type the inverse was declared onto — the same name asked of an
// unrelated type must not match (FR-009's "scoped to a record type" applied
// to inverses).
func TestFindInverses_WrongTargetTypeIsEmpty(t *testing.T) {
	set := twoTypeSchema(t, "plants")
	if got := set.FindInverses("plant", "plants"); len(got) != 0 {
		t.Fatalf("FindInverses(plant, plants) = %+v, want none — the inverse is declared onto bed, not plant", got)
	}
}

// TestFindInverses_CollisionReturnsBoth is the case D5 does not rule out: two
// DIFFERENT record types each declaring `inverse: plants` onto "bed" must
// both come back, so a caller can report the collision rather than silently
// preferring one (ADR-068's whole "never silently pick one" posture, FR-009
// applied to a derived name).
func TestFindInverses_CollisionReturnsBoth(t *testing.T) {
	root := writeVaultSchema(t, "", "bed.yaml", `
schema_version: 1
type: bed
label: Bed
identity:
  prefix: BED
properties:
  name: { type: text }
`)
	root = writeVaultSchema(t, root, "plant.yaml", `
schema_version: 1
type: plant
label: Plant
identity:
  prefix: PL
properties:
  bed: { type: relation, to: bed, inverse: plants }
`)
	root = writeVaultSchema(t, root, "cutting.yaml", `
schema_version: 1
type: cutting
label: Cutting
identity:
  prefix: CT
properties:
  bed: { type: relation, to: bed, inverse: plants }
`)
	set, report, err := LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("expected a clean load, got rejections: %v", report.Rejections)
	}

	got := set.FindInverses("bed", "plants")
	if len(got) != 2 {
		t.Fatalf("FindInverses(bed, plants) = %d edges, want 2 (the collision), got %+v", len(got), got)
	}
	sources := map[string]bool{}
	for _, e := range got {
		sources[e.SourceType] = true
	}
	if !sources["plant"] || !sources["cutting"] {
		t.Fatalf("FindInverses did not name both colliding source types: %+v", got)
	}
}

// TestFindInverses_NilSetIsEmpty — a nil receiver answers like an empty one,
// matching SchemaSet.Get and .Types' own nil-safety (schema.go), so a caller
// that has not yet loaded a schema set does not have to special-case it.
func TestFindInverses_NilSetIsEmpty(t *testing.T) {
	var set *SchemaSet
	if got := set.FindInverses("bed", "plants"); got != nil {
		t.Fatalf("FindInverses on a nil SchemaSet = %+v, want nil", got)
	}
}
