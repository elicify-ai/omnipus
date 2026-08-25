// Omnipus — tests for FR-001..FR-004, FR-009.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeVaultSchema writes one schema file into a temp vault and returns the
// vault root. Each call writes into the SAME root when given one.
func writeVaultSchema(t *testing.T, root, filename, body string) string {
	t.Helper()
	if root == "" {
		root = t.TempDir()
	}
	dir := SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
	return root
}

// TestSchema_LoadPathMatchesFR001 pins the literal path FR-001 names, so a
// refactor cannot quietly relocate schemas out from under an operator's vault.
func TestSchema_LoadPathMatchesFR001(t *testing.T) {
	got := SchemaDir(filepath.Join("some", "vault"))
	want := filepath.Join("some", "vault", ".omnipus-vault", "records")
	if got != want {
		t.Fatalf("FR-001 requires <vault>/.omnipus-vault/records/<type>.yaml; SchemaDir gave %q, want %q", got, want)
	}
}

// TestSchema_LoadAndReject covers FR-001, FR-002 and FR-003 — spec §7 test 1,
// tracing US-1 scenario 1.5.
func TestSchema_LoadAndReject(t *testing.T) {
	t.Run("FR-001 a well-formed schema loads from the vault", func(t *testing.T) {
		root := writeVaultSchema(t, "", "widget.yaml", `
schema_version: 1
type: widget
label: Widget
identity:
  prefix: WI
properties:
  name:   { type: text, required: true }
  status: { type: enum, values: [draft, shipped] }
`)
		set, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if !report.OK() {
			t.Fatalf("expected a clean load, got rejections: %v", report.Rejections)
		}
		sc, ok := set.Get("widget")
		if !ok {
			t.Fatalf("FR-001: schema for %q did not load; loaded types = %v", "widget", set.Types())
		}
		if sc.SchemaVersion != 1 || sc.Type != "widget" || sc.Identity.Prefix != "WI" {
			t.Fatalf("schema fields not read: %+v", sc)
		}
		if len(sc.PropertyOrder) != 2 || sc.PropertyOrder[0] != "name" || sc.PropertyOrder[1] != "status" {
			t.Fatalf("property declaration order not preserved: %v", sc.PropertyOrder)
		}
	})

	t.Run("FR-002 a schema with no schema_version is rejected", func(t *testing.T) {
		root := writeVaultSchema(t, "", "widget.yaml", `
type: widget
properties:
  name: { type: text }
`)
		set, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if _, ok := set.Get("widget"); ok {
			t.Fatalf("FR-002 / US-1.5: a schema with no schema_version must be rejected and no records validated against it, but type %q loaded", "widget")
		}
		if len(report.Rejections) != 1 {
			t.Fatalf("expected exactly one rejection, got %d: %v", len(report.Rejections), report.Rejections)
		}
		rej := report.Rejections[0]
		if rej.Code != RejectMissingVersion {
			t.Fatalf("expected code %q, got %q", RejectMissingVersion, rej.Code)
		}
		if !strings.Contains(rej.Reason, "schema_version") {
			t.Fatalf("the rejection must name the missing field; reason was %q", rej.Reason)
		}
		if len(rej.Paths) != 1 || !strings.HasSuffix(rej.Paths[0], "widget.yaml") {
			t.Fatalf("the rejection must name the offending file; paths were %v", rej.Paths)
		}
	})

	t.Run("FR-002 schema_version 0 is a version, not an absence", func(t *testing.T) {
		// Guards the *int: decoding into a plain int would make an explicit 0
		// indistinguishable from a missing field, and the two need different
		// messages.
		root := writeVaultSchema(t, "", "widget.yaml", `
schema_version: 0
type: widget
properties:
  name: { type: text }
`)
		_, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if len(report.Rejections) != 1 {
			t.Fatalf("expected one rejection, got %v", report.Rejections)
		}
		if report.Rejections[0].Code != RejectUnsupportedVersion {
			t.Fatalf("schema_version: 0 must be reported as an UNSUPPORTED version, not a missing one; got %q", report.Rejections[0].Code)
		}
	})

	t.Run("FR-003 two files declaring one record type are BOTH rejected, both paths named", func(t *testing.T) {
		root := writeVaultSchema(t, "", "widget.yaml", `
schema_version: 1
type: widget
properties:
  name: { type: text }
`)
		writeVaultSchema(t, root, "widget-copy.yaml", `
schema_version: 1
type: widget
properties:
  name: { type: text }
`)
		set, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if _, ok := set.Get("widget"); ok {
			t.Fatalf("FR-003 / edge table: a type declared in two files must have BOTH rejected; %q loaded anyway", "widget")
		}
		if set.Len() != 0 {
			t.Fatalf("expected zero loaded types, got %v", set.Types())
		}
		if len(report.Rejections) != 1 {
			t.Fatalf("expected one conflict rejection, got %d: %v", len(report.Rejections), report.Rejections)
		}
		rej := report.Rejections[0]
		if rej.Code != RejectDuplicateType {
			t.Fatalf("expected code %q, got %q", RejectDuplicateType, rej.Code)
		}
		if len(rej.Paths) != 2 {
			t.Fatalf("FR-003 requires BOTH paths named; got %v", rej.Paths)
		}
		joined := strings.Join(rej.Paths, " ")
		for _, want := range []string{"widget.yaml", "widget-copy.yaml"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("FR-003 requires both paths named; %q missing from %v", want, rej.Paths)
			}
		}
		if !strings.Contains(rej.Reason, "widget.yaml") || !strings.Contains(rej.Reason, "widget-copy.yaml") {
			t.Fatalf("the human-readable reason must name both paths too; got %q", rej.Reason)
		}
	})

	t.Run("FR-003 one bad schema does not blind the vault", func(t *testing.T) {
		root := writeVaultSchema(t, "", "good.yaml", `
schema_version: 1
type: good
properties:
  name: { type: text }
`)
		writeVaultSchema(t, root, "bad.yaml", `
type: bad
properties:
  name: { type: text }
`)
		set, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if _, ok := set.Get("good"); !ok {
			t.Fatalf("a valid schema must still load alongside a rejected one; loaded = %v", set.Types())
		}
		if _, ok := set.Get("bad"); ok {
			t.Fatalf("the rejected schema must not load")
		}
		if got := report.RejectedTypes(); len(got) != 1 || got[0] != "bad" {
			t.Fatalf("expected RejectedTypes == [bad], got %v", got)
		}
	})

	t.Run("FR-005 a vault with no schema directory is not an error", func(t *testing.T) {
		set, report, err := LoadSchemas(t.TempDir())
		if err != nil {
			t.Fatalf("a vault with no schemas must load cleanly (ADR-068 §9.1); got error %v", err)
		}
		if set.Len() != 0 || !report.OK() {
			t.Fatalf("expected an empty set and clean report, got %v / %v", set.Types(), report.Rejections)
		}
	})

	t.Run("the .seq allocator file is not mistaken for a schema", func(t *testing.T) {
		root := writeVaultSchema(t, "", "widget.yaml", `
schema_version: 1
type: widget
properties:
  name: { type: text }
`)
		// ADR-068 D7.1 puts .seq in this same directory.
		if err := os.WriteFile(filepath.Join(SchemaDir(root), ".seq"), []byte(`{"widget":7}`), 0o644); err != nil {
			t.Fatalf("write .seq: %v", err)
		}
		_, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if !report.OK() {
			t.Fatalf("D7.1's .seq must be skipped, not rejected as a schema; rejections: %v", report.Rejections)
		}
	})
}

// TestSchema_TypesAreScopedToRecordType covers FR-004 and FR-009 — spec §7's
// traceability row for US-1 scenario 1.2.
func TestSchema_TypesAreScopedToRecordType(t *testing.T) {
	t.Run("FR-004 exactly seven property types exist", func(t *testing.T) {
		// The oracle is ADR-068 D3's table, transcribed here from the ADR and
		// not from the implementation.
		want := []string{"text", "enum", "relation", "date", "number", "money", "person"}
		if len(PropertyTypes) != len(want) {
			t.Fatalf("FR-004 declares exactly %d property types, package declares %d: %v", len(want), len(PropertyTypes), PropertyTypes)
		}
		for i, w := range want {
			if string(PropertyTypes[i]) != w {
				t.Fatalf("property type %d: want %q, got %q", i, w, PropertyTypes[i])
			}
		}
	})

	t.Run("FR-004 an undeclared property type is rejected listing the seven", func(t *testing.T) {
		root := writeVaultSchema(t, "", "widget.yaml", `
schema_version: 1
type: widget
properties:
  price: { type: currency }
`)
		_, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if len(report.Rejections) != 1 {
			t.Fatalf("expected one rejection, got %v", report.Rejections)
		}
		reason := report.Rejections[0].Reason
		for _, pt := range []string{"text", "enum", "relation", "date", "number", "money", "person"} {
			if !strings.Contains(reason, pt) {
				t.Fatalf("a type rejection must list the supported types; %q missing from %q", pt, reason)
			}
		}
	})

	t.Run("FR-009 the same property name in two record types is unrelated", func(t *testing.T) {
		// ADR-068 D3.3: Obsidian binds a property type to a NAME, vault-wide,
		// which is why real vaults carry prm-tier and health_sleep_total_minutes.
		root := writeVaultSchema(t, "", "alpha.yaml", `
schema_version: 1
type: alpha
properties:
  status: { type: enum, values: [open, closed] }
`)
		writeVaultSchema(t, root, "beta.yaml", `
schema_version: 1
type: beta
properties:
  status: { type: number }
`)
		set, report, err := LoadSchemas(root)
		if err != nil || !report.OK() {
			t.Fatalf("both schemas must load: err=%v rejections=%v", err, report.Rejections)
		}
		alpha, _ := set.Get("alpha")
		beta, _ := set.Get("beta")
		ap, _ := alpha.Property("status")
		bp, _ := beta.Property("status")
		if ap.Type != TypeEnum {
			t.Fatalf("alpha.status must be enum, got %q", ap.Type)
		}
		if bp.Type != TypeNumber {
			t.Fatalf("FR-009: beta.status must be number and unrelated to alpha.status, got %q", bp.Type)
		}
		if ap.RecordType != "alpha" || bp.RecordType != "beta" {
			t.Fatalf("a property must know its owning record type; got %q and %q", ap.RecordType, bp.RecordType)
		}

		// The behavioural consequence: a value valid for one is a fault for the
		// other. `open` is a permitted alpha status and is not a number.
		rec := ParseRecord("beta/one.md", []byte("---\ntype: beta\nstatus: open\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if rep.Valid() {
			t.Fatalf("FR-009: `status: open` must be a fault on record type beta (declared number), but the record validated clean")
		}
		if got := rep.Errors()[0].Code; got != FindingNotANumber {
			t.Fatalf("expected %q, got %q", FindingNotANumber, got)
		}
	})

	t.Run("ADR-068 D0 the package ships no record types of its own", func(t *testing.T) {
		// D0 is non-negotiable: no built-in company, contact, deal or
		// interaction, "not even as overridable defaults".
		set := NewSchemaSet()
		if set.Len() != 0 {
			t.Fatalf("ADR-068 D0: a fresh schema set must declare NO record types; it declared %v", set.Types())
		}
		for _, banned := range []string{"company", "contact", "deal", "interaction", "person", "note", "task"} {
			if _, ok := set.Get(banned); ok {
				t.Fatalf("ADR-068 D0: %q must not exist as a built-in record type", banned)
			}
		}
		empty, report, err := LoadSchemas(t.TempDir())
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if empty.Len() != 0 || !report.OK() {
			t.Fatalf("ADR-068 D0: loading a vault with no schema files must yield zero record types, got %v", empty.Types())
		}
	})
}
