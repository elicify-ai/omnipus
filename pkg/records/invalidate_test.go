// Omnipus — tests for FR-015: a schema change invalidates affected records.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// TestSchemaChange_InvalidatesAffectedRecords covers FR-015.
//
// The failure it prevents is quiet: an operator tightens an enum, the RECORDS
// do not change, and nothing in the note-scanning path notices the schema did —
// so every now-non-conforming record goes on being reported as valid.
func TestSchemaChange_InvalidatesAffectedRecords(t *testing.T) {
	const before = `
schema_version: 1
type: widget
properties:
  status: { type: enum, values: [todo, doing, done, shipped] }
`
	const after = `
schema_version: 1
type: widget
properties:
  status: { type: enum, values: [todo, doing, done] }
`
	root := writeVaultSchema(t, "", "widget.yaml", before)
	snapBefore, setBefore, report, err := SnapshotSchemas(root)
	if err != nil || !report.OK() {
		t.Fatalf("initial load: err=%v rejections=%v", err, report.Rejections)
	}

	// A record that conforms to the OLD schema and not the new one.
	rec := ParseRecord("notes/a.md", []byte("---\ntype: widget\nstatus: shipped\n---\n"))
	if !ValidateRecord(setBefore, rec, ValidateOptions{}).Valid() {
		t.Fatalf("the fixture record must be valid under the original schema")
	}

	// Tighten the schema. The record's BYTES are untouched; its meaning is not.
	writeVaultSchema(t, root, "widget.yaml", after)
	snapAfter, setAfter, report, err := SnapshotSchemas(root)
	if err != nil || !report.OK() {
		t.Fatalf("reload: err=%v rejections=%v", err, report.Rejections)
	}

	t.Run("FR-015 the schema change is detected", func(t *testing.T) {
		changes := DiffSchemaSnapshots(snapBefore, snapAfter)
		if len(changes) != 1 {
			t.Fatalf("expected exactly one change, got %v", changes)
		}
		if changes[0].Kind != SchemaModified || changes[0].Type != "widget" {
			t.Fatalf("expected widget/modified, got %+v", changes[0])
		}
	})

	t.Run("FR-015 detection is by CONTENT, not modification time", func(t *testing.T) {
		// A git checkout rewrites every timestamp; a same-second edit changes
		// none. Content hashing is what makes the check honest in both cases.
		reRoot := writeVaultSchema(t, "", "widget.yaml", before)
		s1, _, _, err := SnapshotSchemas(reRoot)
		if err != nil {
			t.Fatalf("%v", err)
		}
		// Rewrite the SAME bytes with a new mtime.
		p := filepath.Join(SchemaDir(reRoot), "widget.yaml")
		if werr := os.WriteFile(p, []byte(before), 0o644); werr != nil {
			t.Fatalf("%v", werr)
		}
		future := time.Now().Add(2 * time.Hour)
		if cerr := os.Chtimes(p, future, future); cerr != nil {
			t.Fatalf("%v", cerr)
		}
		s2, _, _, err := SnapshotSchemas(reRoot)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if changes := DiffSchemaSnapshots(s1, s2); len(changes) != 0 {
			t.Fatalf("FR-015: identical content with a newer mtime is NOT a change; got %v", changes)
		}
	})

	t.Run("FR-015 the affected record is revalidated and now reported", func(t *testing.T) {
		rep, affected := Revalidate(snapBefore, snapAfter, setAfter, []Record{rec}, ValidateOptions{})
		if !reflect.DeepEqual(affected, []string{"widget"}) {
			t.Fatalf("expected [widget] affected, got %v", affected)
		}
		if len(rep.Records) != 1 {
			t.Fatalf("the affected record must be selected for revalidation; got %d", len(rep.Records))
		}
		if rep.Valid() {
			t.Fatalf("FR-015: after the schema tightened, `shipped` is no longer permitted and the record MUST be reported")
		}
		f := rep.Findings()[0]
		if f.Code != FindingEnumNotPermitted || f.RecordPath != "notes/a.md" {
			t.Fatalf("expected an enum finding naming the record; got %+v", f)
		}
		if !reflect.DeepEqual(f.Permitted, []string{"todo", "doing", "done"}) {
			t.Fatalf("the finding must list the NEW permitted set; got %v", f.Permitted)
		}
	})

	t.Run("FR-015 an unaffected record type is not revalidated", func(t *testing.T) {
		other := ParseRecord("notes/b.md", []byte("---\ntype: gadget\nstatus: whatever\n---\n"))
		rep, _ := Revalidate(snapBefore, snapAfter, setAfter, []Record{rec, other}, ValidateOptions{})
		if len(rep.Records) != 1 || rep.Records[0].Path != "notes/a.md" {
			t.Fatalf("only records of the affected type may be selected; got %v", rep.Records)
		}
	})

	t.Run("FR-015 a schema fixed from a REJECTED state is a change", func(t *testing.T) {
		// A rejected file has no entry in the schema set, so nothing in the
		// set could notice it was repaired. The snapshot tracks FILES, which
		// is why this works.
		fixRoot := writeVaultSchema(t, "", "widget.yaml", "type: widget\nproperties:\n  s: { type: text }\n")
		s1, set1, rep1, err := SnapshotSchemas(fixRoot)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if rep1.OK() || set1.Len() != 0 {
			t.Fatalf("the fixture must start rejected (FR-002)")
		}
		writeVaultSchema(t, fixRoot, "widget.yaml", "schema_version: 1\ntype: widget\nproperties:\n  s: { type: text }\n")
		s2, set2, rep2, err := SnapshotSchemas(fixRoot)
		if err != nil || !rep2.OK() {
			t.Fatalf("the repaired schema must load: %v / %v", err, rep2.Rejections)
		}
		changes := DiffSchemaSnapshots(s1, s2)
		if len(changes) != 1 || changes[0].Type != "widget" {
			t.Fatalf("repairing a rejected schema must register as a change; got %v", changes)
		}
		if got := AffectedRecordTypes(set2, changes); !reflect.DeepEqual(got, []string{"widget"}) {
			t.Fatalf("expected [widget], got %v", got)
		}
	})

	t.Run("FR-015 a deleted schema invalidates its records so they revert to ordinary notes", func(t *testing.T) {
		delRoot := writeVaultSchema(t, "", "widget.yaml", after)
		s1, _, _, err := SnapshotSchemas(delRoot)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if rerr := os.Remove(filepath.Join(SchemaDir(delRoot), "widget.yaml")); rerr != nil {
			t.Fatalf("%v", rerr)
		}
		s2, set2, _, err := SnapshotSchemas(delRoot)
		if err != nil {
			t.Fatalf("%v", err)
		}
		changes := DiffSchemaSnapshots(s1, s2)
		if len(changes) != 1 || changes[0].Kind != SchemaRemoved || changes[0].Type != "widget" {
			t.Fatalf("expected widget/removed, got %v", changes)
		}
		rep, _ := Revalidate(s1, s2, set2, []Record{rec}, ValidateOptions{})
		if len(rep.Records) != 1 {
			t.Fatalf("the record must still be selected, so its stale findings are dropped; got %d", len(rep.Records))
		}
		if rep.Records[0].Recognised {
			t.Fatalf("FR-005: with its schema gone the note is an ordinary note again")
		}
		if !rep.Valid() {
			t.Fatalf("an ordinary note is not invalid; findings: %v", rep.Findings())
		}
	})

	t.Run("FR-015 a relation's target type change affects the referring type too", func(t *testing.T) {
		relRoot := writeVaultSchema(t, "", "company.yaml", "schema_version: 1\ntype: company\nproperties:\n  name: { type: text }\n")
		writeVaultSchema(t, relRoot, "deal.yaml", "schema_version: 1\ntype: deal\nproperties:\n  buyer: { type: relation, to: company, inverse: deals }\n")
		s1, _, rep1, err := SnapshotSchemas(relRoot)
		if err != nil || !rep1.OK() {
			t.Fatalf("%v / %v", err, rep1.Rejections)
		}
		writeVaultSchema(t, relRoot, "company.yaml", "schema_version: 1\ntype: company\nproperties:\n  name: { type: text }\n  size: { type: number }\n")
		s2, set2, rep2, err := SnapshotSchemas(relRoot)
		if err != nil || !rep2.OK() {
			t.Fatalf("%v / %v", err, rep2.Rejections)
		}
		got := AffectedRecordTypes(set2, DiffSchemaSnapshots(s1, s2))
		want := []string{"company", "deal"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("changing `company` changes what a deal's relation may point at, so both must be affected; want %v, got %v", want, got)
		}
	})

	t.Run("no change means nothing is revalidated", func(t *testing.T) {
		rep, affected := Revalidate(snapAfter, snapAfter, setAfter, []Record{rec}, ValidateOptions{})
		if len(affected) != 0 || len(rep.Records) != 0 {
			t.Fatalf("an unchanged schema set must invalidate nothing; got %v / %d records", affected, len(rep.Records))
		}
	})
}
