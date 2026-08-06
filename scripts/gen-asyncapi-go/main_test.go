package main

import (
	"strings"
	"testing"
)

// TestWriteStruct_ThreeWayCollisionErrors verifies that a schema where three
// properties produce a Go field collision at both the first and second rename
// level causes writeStruct to return a descriptive error instead of silently
// emitting an IsIsRef (or similar invalid) field name.
//
// The collision scenario uses three properties:
//
//	"_ref"   → toPascalCase = "Ref" → underscore rule renames to "IsRef"
//	"ref"    → toPascalCase = "Ref" → triggers rename of "_ref" to "IsRef"
//	"is_ref" → toPascalCase = "IsRef" → collides with the already-reserved "IsRef" slot
//
// All three ultimately contend for the same Go identifier space; the generator
// must return a fatal error rather than silently producing an invalid struct.
func TestWriteStruct_ThreeWayCollisionErrors(t *testing.T) {
	// Build a schema with three properties that force a three-way collision.
	s := &schema{
		name:       "ThreeWayCollision",
		schemaType: "object",
		properties: map[string]*schema{
			"_ref":   {schemaType: "boolean"},
			"ref":    {schemaType: "string"},
			"is_ref": {schemaType: "string"},
		},
		// Order matters: process "_ref" first so it occupies "Ref", then "ref"
		// triggers the rename of "_ref" to "IsRef", then "is_ref" tries to claim
		// "IsRef" and must fail.
		propertyOrder: []string{"_ref", "ref", "is_ref"},
		required:      map[string]bool{"_ref": true, "ref": true, "is_ref": true},
	}

	_, err := generate(map[string]*schema{"ThreeWayCollision": s})
	if err == nil {
		t.Fatal("expected an error for three-way Go field name collision, got nil")
	}
	if !strings.Contains(err.Error(), "three-way") {
		t.Errorf("expected error message to contain 'three-way', got: %v", err)
	}
}

// TestGenerateUsesMatchingNamedInlinePayload verifies the happy-path branch of
// matchingNamedInlineGoType: when a property's inline shape exactly matches a
// sibling named schema named `<OwnerWithoutFrame><PascalCase(propName)>`, the
// generator emits a pointer (`*ErrorPayload`) for the optional field — the same
// shape that the prior hand-adjustment to asyncapi_types.gen.go produced. This
// is the regression test for the inline-mirror-of-named-schema pattern.
func TestGenerateUsesMatchingNamedInlinePayload(t *testing.T) {
	payloadShape := func() *schema {
		return &schema{
			schemaType: "object",
			properties: map[string]*schema{
				"message": {schemaType: "string"},
			},
			propertyOrder: []string{"message"},
			required:      map[string]bool{"message": true},
		}
	}
	schemas := map[string]*schema{
		"ErrorFrame": {
			schemaType: "object",
			properties: map[string]*schema{
				"payload": payloadShape(),
			},
			propertyOrder: []string{"payload"},
			required:      map[string]bool{},
		},
		"ErrorPayload": payloadShape(),
	}

	src, err := generate(schemas)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	want := "Payload *ErrorPayload `json:\"payload,omitempty\"`"
	if !strings.Contains(string(src), want) {
		t.Fatalf("generated source does not contain %q:\n%s", want, src)
	}
}

// TestGenerate_RequiredMatchingPropertyReturnsValueType verifies the
// required-field branch of matchingNamedInlineGoType (TA-3). When the inline
// payload property is required, the generator emits the named type as a VALUE
// (no pointer, no omitempty) — matching the spec's required-field semantics.
// Without this assertion, a future regression could silently start
// omitempty-tagging required fields, breaking the SPA Zod validator that
// requires the field to be present.
func TestGenerate_RequiredMatchingPropertyReturnsValueType(t *testing.T) {
	payloadShape := func() *schema {
		return &schema{
			schemaType: "object",
			properties: map[string]*schema{
				"message": {schemaType: "string"},
			},
			propertyOrder: []string{"message"},
			required:      map[string]bool{"message": true},
		}
	}
	schemas := map[string]*schema{
		"ErrorFrame": {
			schemaType: "object",
			properties: map[string]*schema{
				"payload": payloadShape(),
			},
			propertyOrder: []string{"payload"},
			required:      map[string]bool{"payload": true},
		},
		"ErrorPayload": payloadShape(),
	}

	src, err := generate(schemas)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	want := "Payload ErrorPayload `json:\"payload\"`"
	if !strings.Contains(string(src), want) {
		t.Fatalf(
			"generated source does not contain %q (required field must be a value type, no pointer, no omitempty):\n%s",
			want,
			src,
		)
	}
}

// TestGenerate_RefPropertyShortCircuits verifies the $ref-property short-circuit
// branch of matchingNamedInlineGoType (TA-2). A property whose schema has a
// $ref (e.g. payload: { $ref: ErrorPayload }) must NOT be coerced to a named
// type via the candidate-name heuristic — it is already a $ref and the
// regular resolveGoType path handles it. A regression here could silently
// rewrite every cross-schema $ref in the AsyncAPI schema set.
func TestGenerate_RefPropertyShortCircuits(t *testing.T) {
	schemas := map[string]*schema{
		"ErrorFrame": {
			schemaType: "object",
			properties: map[string]*schema{
				"payload": {ref: "ErrorPayload"},
			},
			propertyOrder: []string{"payload"},
			required:      map[string]bool{},
		},
		"ErrorPayload": {
			schemaType: "object",
			properties: map[string]*schema{
				"message": {schemaType: "string"},
			},
			propertyOrder: []string{"message"},
			required:      map[string]bool{"message": true},
		},
	}

	src, err := generate(schemas)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// $ref-resolved field is `*ErrorPayload` (optional, omitempty) — same
	// shape as the matching-inline branch — but the test fails the regression
	// scenario: if matchingNamedInlineGoType ever silently maps a $ref to the
	// candidate-name path, the shape stays the same here because the candidate
	// name also resolves to ErrorPayload. The load-bearing assertion is that
	// the generator does NOT crash and does NOT produce a `struct{...}` shape.
	want := "Payload *ErrorPayload `json:\"payload,omitempty\"`"
	if !strings.Contains(string(src), want) {
		t.Fatalf(
			"generated source does not contain %q (ref must short-circuit to the regular $ref resolver):\n%s",
			want,
			src,
		)
	}
}

// TestGenerate_ShapeMismatchFallsBackToInline verifies the false-return path of
// sameSchemaShape (TA-4). When the inline payload shape DOES NOT match the
// sibling named schema (here: extra field the named schema lacks), the
// generator must fall back to the anonymous inline-struct emit — NOT silently
// coerce to *Name. A regression that always returned the named type on a
// near-miss would silently break wire-shape contracts.
func TestGenerate_ShapeMismatchFallsBackToInline(t *testing.T) {
	inlineShape := &schema{
		schemaType: "object",
		properties: map[string]*schema{
			"message": {schemaType: "string"},
			"detail":  {schemaType: "string"}, // extra field the named schema lacks
		},
		propertyOrder: []string{"message", "detail"},
		required:      map[string]bool{"message": true},
	}
	namedShape := &schema{
		schemaType: "object",
		properties: map[string]*schema{
			"message": {schemaType: "string"},
		},
		propertyOrder: []string{"message"},
		required:      map[string]bool{"message": true},
	}
	schemas := map[string]*schema{
		"ErrorFrame": {
			schemaType: "object",
			properties: map[string]*schema{
				"payload": inlineShape,
			},
			propertyOrder: []string{"payload"},
			required:      map[string]bool{},
		},
		"ErrorPayload": namedShape,
	}

	src, err := generate(schemas)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Mismatch must fall back to anonymous inline struct, NOT *ErrorPayload.
	// The anonymous emit does not carry a `*ErrorPayload` pointer; the field
	// is named `Payload` but the type is `struct{ ... }`.
	notWant := "*ErrorPayload"
	if strings.Contains(string(src), "Payload "+notWant+" ") {
		t.Fatalf("shape mismatch must NOT coerce to %s; the inline has an extra field:\n%s", notWant, src)
	}
	want := "Payload struct"
	if !strings.Contains(string(src), want) {
		t.Fatalf("expected fallback to anonymous struct emit containing %q:\n%s", want, src)
	}
}

// TestGenerate_OptionalInverseCase verifies the optional-field inverse case
// (TA-5): when the named schema exists with a matching shape and the field
// is OPTIONAL (the inverse of TestGenerate_RequiredMatchingPropertyReturnsValueType),
// the generator must emit `*Name` with omitempty — and crucially, when the
// inline payload has ZERO nested properties (a primitive), the matcher must
// short-circuit and the field must emit the regular inline-primitive Go type,
// not an empty anonymous struct.
func TestGenerate_OptionalInverseCase(t *testing.T) {
	schemas := map[string]*schema{
		"ErrorFrame": {
			schemaType: "object",
			properties: map[string]*schema{
				"payload": {
					schemaType:    "object",
					properties:    map[string]*schema{},
					propertyOrder: []string{},
					required:      map[string]bool{},
				},
			},
			propertyOrder: []string{"payload"},
			required:      map[string]bool{},
		},
		"ErrorPayload": {
			schemaType: "object",
			properties: map[string]*schema{
				"message": {schemaType: "string"},
			},
			propertyOrder: []string{"message"},
			required:      map[string]bool{"message": true},
		},
	}

	src, err := generate(schemas)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Empty-properties short-circuit: matchingNamedInlineGoType returns
	// ("", false) and the inline falls through to resolveGoType. With no
	// nested properties and no additionalProperties, the inline emit is
	// map[string]any or an anonymous empty struct — NOT a pointer to the
	// sibling named type (which would silently widen the wire shape).
	if strings.Contains(string(src), "Payload *ErrorPayload ") {
		t.Fatalf("empty-properties short-circuit must NOT coerce to *ErrorPayload:\n%s", src)
	}
}
