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
