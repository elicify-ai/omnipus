// Omnipus — RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan test 58
// (contract-shape).
//
// Oracle: FR-VA-009b ("ViewDef.kind's inline enum MUST be extracted to
// contracts/components/schemas/ViewKind.yaml, referenced by $ref from both
// ViewDef.kind and LibraryEntryView.kind, preserving x-enum-varnames") and
// FR-VA-009c ("A new contracts/components/schemas/ViewRejectionCode.yaml
// enum MUST enumerate every existing RejectView* code plus view_too_large,
// referenced from LibraryEntryView.rejection — never a bare type: string").
//
// This test reads contracts/openapi.yaml and contracts/components/schemas/
// directly rather than the generated Go types, per Hard Constraint #8: the
// spec file IS the source of truth this test's oracle is checked against,
// and generated types would not exist yet to check anyway
// (make gen-contracts has not been run against a spec that doesn't have
// these schemas). Verified by direct read at RED time: ViewDef.kind is
// still an inline `type: string` / `enum:` block (contracts/openapi.yaml,
// ~line 710), contracts/components/schemas has no ViewKind.yaml,
// ViewRejectionCode.yaml or LibraryEntryView.yaml, and LibraryEntry.yaml has
// no `is_view`/`view` field at all.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestContractShape_ViewKindAndViewRejectionCodeAreExtractedSchemas$' ./pkg/records/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package records

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// repoRootFromPkgRecords walks up from this file's own directory to the repo
// root (identified by go.mod), so the test works regardless of the working
// directory `go test` is invoked from.
func repoRootFromPkgRecords(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find repo root (go.mod) walking up from %s", dir)
		}
		dir = parent
	}
}

// TestContractShape_ViewKindAndViewRejectionCodeAreExtractedSchemas is TDD
// Plan test 58 (R2-MAJ-005, FR-VA-009b/009c): ViewDef.kind and
// LibraryEntryView.kind must both $ref a new ViewKind.yaml schema (never an
// inline or duplicated enum), and LibraryEntryView.rejection must $ref a new
// ViewRejectionCode.yaml enum (never a bare `type: string`).
func TestContractShape_ViewKindAndViewRejectionCodeAreExtractedSchemas(t *testing.T) {
	root := repoRootFromPkgRecords(t)
	schemasDir := filepath.Join(root, "contracts", "components", "schemas")

	viewKindPath := filepath.Join(schemasDir, "ViewKind.yaml")
	if _, err := os.Stat(viewKindPath); err != nil {
		t.Errorf("FR-VA-009b: %s must exist (ViewDef.kind's enum extracted to its own schema); stat error: %v",
			viewKindPath, err)
	}

	viewRejectionPath := filepath.Join(schemasDir, "ViewRejectionCode.yaml")
	if _, err := os.Stat(viewRejectionPath); err != nil {
		t.Errorf("FR-VA-009c: %s must exist (a new enum for every RejectView* code plus view_too_large); "+
			"stat error: %v", viewRejectionPath, err)
	}

	libraryEntryViewPath := filepath.Join(schemasDir, "LibraryEntryView.yaml")
	if _, err := os.Stat(libraryEntryViewPath); err != nil {
		t.Errorf("D-CONTRACT: %s must exist (the nested view object LibraryEntry.view $refs); "+
			"stat error: %v", libraryEntryViewPath, err)
	}

	openapiBytes, err := os.ReadFile(filepath.Join(root, "contracts", "openapi.yaml"))
	if err != nil {
		t.Fatalf("reading contracts/openapi.yaml: %v", err)
	}
	openapi := string(openapiBytes)

	// ViewDef.kind must be a $ref, not an inline enum. A regex over the raw
	// YAML is deliberately conservative here — it looks for the SPECIFIC
	// inline-enum shape this file has today (kind: / type: string / enum:)
	// immediately following one another, which is exactly what FR-VA-009b
	// requires to be gone.
	inlineKindEnum := regexp.MustCompile(`kind:\s*\n\s+type:\s*string\s*\n\s+enum:`)
	if inlineKindEnum.MatchString(openapi) {
		t.Errorf("FR-VA-009b: contracts/openapi.yaml still declares ViewDef.kind as an inline " +
			"`type: string` / `enum:` — it must be `$ref: \"./components/schemas/ViewKind.yaml\"` instead")
	}

	libraryEntryBytes, err := os.ReadFile(filepath.Join(schemasDir, "LibraryEntry.yaml"))
	if err != nil {
		t.Fatalf("reading contracts/components/schemas/LibraryEntry.yaml: %v", err)
	}
	libraryEntry := string(libraryEntryBytes)
	if !regexp.MustCompile(`is_view`).MatchString(libraryEntry) {
		t.Errorf("FR-VA-009: contracts/components/schemas/LibraryEntry.yaml must declare `is_view` " +
			"— it does not exist on this schema today")
	}
	if !regexp.MustCompile(`\bview:`).MatchString(libraryEntry) {
		t.Errorf("FR-VA-009/D-CONTRACT: contracts/components/schemas/LibraryEntry.yaml must declare a " +
			"nested `view:` object ($ref LibraryEntryView.yaml) — it does not exist on this schema today")
	}
}
