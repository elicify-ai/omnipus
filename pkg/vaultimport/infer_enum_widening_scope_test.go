// Omnipus — the two properties enum widening has to have that its own unit
// tests cannot see, because both are about what happens OUTSIDE the function.
//
//  1. FR-009 SCOPING. `status` is not one property; it is a different property
//     on every record type that declares it, with a different vocabulary each
//     time. The founder's vault has at least four (`task`, `content`,
//     `contract`, `brief`), and they disagree. A widening derived from a view
//     that resolved to `task` must reach `task.status` and nothing else. An
//     admission that leaked across types would quietly rewrite the vocabulary
//     of every type sharing a property NAME — and it would look correct in
//     every single-type test, which is exactly why it is tested here.
//
//  2. THE ACCEPTANCE ROUND TRIP. The value has to survive being written to a
//     schema file and read back by the REAL loader. If it does not, the clause
//     translates at import time against an in-memory index and is then refused
//     by records at QUERY time — a view that imports clean and errors when the
//     operator opens it, which is strictly worse than the named loss it
//     replaced. And the same round trip has to leave every note the importer
//     read still valid: this package's standing bar is that a note it typed is
//     never reported invalid by the same run.
//
// Both go through the REAL records loader (schemaSetFromRendered renders with
// RenderSchemaYAML and parses back with records.ParseSchema), never an
// in-memory shortcut, because an in-memory shortcut is precisely the failure
// mode (2) exists to catch.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// twoTypesSharingStatus builds a vault where `task` and `content` both declare
// `status` and their vocabularies do not overlap at all, so a leak in either
// direction is unmistakable.
func twoTypesSharingStatus(t *testing.T) ([]NoteRecord, map[string][]InferredProperty) {
	t.Helper()
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "t1.md", "---\ntype: task\nstatus: todo\n---\n\nbody\n"),
		noteOnDisk(t, dir, "t2.md", "---\ntype: task\nstatus: done\n---\n\nbody\n"),
		noteOnDisk(t, dir, "c1.md", "---\ntype: content\nstatus: scheduled\n---\n\nbody\n"),
		noteOnDisk(t, dir, "c2.md", "---\ntype: content\nstatus: draft\n---\n\nbody\n"),
	}
	groups := CollectTypeGroups(notes)
	names := BuildNameIndex(notes)
	inferred := map[string][]InferredProperty{}
	for typeName, g := range groups {
		inferred[typeName] = InferSchema(g, names)
	}
	for _, typeName := range []string{"task", "content"} {
		p, ok := findInferredProperty(inferred[typeName], "status")
		if !ok || p.Type != records.TypeEnum {
			t.Fatalf("fixture is not exercising the rule: %s.status inferred as %q, want enum", typeName, p.Type)
		}
	}
	return notes, inferred
}

// baseTaskOnlyDoing resolves to `task` and to no other type, and filters on a
// value neither type carries.
const baseTaskOnlyDoing = `
filters:
  and:
    - file.inFolder("Notes")
views:
  - type: table
    name: Doing now
    filters:
      and:
        - type == "task"
        - status == "doing"
`

func TestEnumWidening_IsScopedToTheViewsOwnRecordType(t *testing.T) {
	_, inferred := twoTypesSharingStatus(t)
	pb, err := ParseBaseFile([]byte(baseTaskOnlyDoing))
	if err != nil {
		t.Fatalf("fixture base does not parse: %v", err)
	}

	widenings := WidenEnumsFromBases(inferred, []string{"Tasks.base"}, map[string]*ParsedBase{"Tasks.base": pb})

	taskStatus, _ := findInferredProperty(inferred["task"], "status")
	if !containsString(taskStatus.EnumValues, "doing") {
		t.Fatalf("task.status = %v — the view resolved to `task` and filters on \"doing\", so that is where the value belongs", taskStatus.EnumValues)
	}

	contentStatus, _ := findInferredProperty(inferred["content"], "status")
	if containsString(contentStatus.EnumValues, "doing") {
		t.Errorf("content.status = %v — \"doing\" leaked across record types. FR-009 scopes a property to its type: `status` on `content` is a different property with a different vocabulary, and admitting a task's literal into it rewrites a vocabulary the operator never touched", contentStatus.EnumValues)
	}
	if len(contentStatus.EnumValues) != 2 || !containsString(contentStatus.EnumValues, "draft") || !containsString(contentStatus.EnumValues, "scheduled") {
		t.Errorf("content.status = %v, want exactly its two observed values (draft, scheduled) — untouched", contentStatus.EnumValues)
	}

	if len(widenings) != 1 {
		t.Fatalf("got %d widening record(s), want 1", len(widenings))
	}
	if widenings[0].RecordType != "task" {
		t.Errorf("the account attributes the widening to %q, want task — a report that names the wrong type sends the operator to the wrong schema file", widenings[0].RecordType)
	}
}

// TestEnumWidening_SurvivesTheWrittenSchemaAndInvalidatesNoNote is the
// acceptance round trip.
//
// The assertion that matters is made against a schema set produced by the REAL
// loader from the REAL rendered YAML, and it is made in BOTH directions:
// before widening the value is refused, after widening it is accepted. Without
// the "before" half the test would pass against a schema that accepts
// everything, which is the shape a broken enum declaration actually takes.
func TestEnumWidening_SurvivesTheWrittenSchemaAndInvalidatesNoNote(t *testing.T) {
	notes, inferred := twoTypesSharingStatus(t)

	dir := t.TempDir()
	doingNote := noteOnDisk(t, dir, "doing.md", "---\ntype: task\nstatus: doing\n---\n\nbody\n")

	// BEFORE: the written schema refuses the value. This is the control, and
	// it is what stops this test passing vacuously.
	beforeSet, _, err := schemaSetFromRendered(inferred, nil)
	if err != nil {
		t.Fatalf("rendering and reloading the pre-widening schemas: %v", err)
	}
	if rr := records.ValidateRecord(beforeSet, doingNote.Rec, records.ValidateOptions{}); rr.Valid() {
		t.Fatal("a note holding `status: doing` is already VALID before any widening — the fixture does not exercise the rule, so nothing below would mean anything")
	}

	pb, err := ParseBaseFile([]byte(baseTaskOnlyDoing))
	if err != nil {
		t.Fatalf("fixture base does not parse: %v", err)
	}
	if got := WidenEnumsFromBases(inferred, []string{"Tasks.base"}, map[string]*ParsedBase{"Tasks.base": pb}); len(got) != 1 {
		t.Fatalf("got %d widening record(s), want 1", len(got))
	}

	// AFTER: rendered through RenderSchemaYAML and parsed back by
	// records.ParseSchema, the value is accepted.
	afterSet, loadReport, err := schemaSetFromRendered(inferred, nil)
	if err != nil {
		t.Fatalf("rendering and reloading the widened schemas: %v", err)
	}
	if !loadReport.OK() {
		t.Fatalf("the widened schema was REJECTED by the real loader: %+v", loadReport.Rejections)
	}
	if rr := records.ValidateRecord(afterSet, doingNote.Rec, records.ValidateOptions{}); !rr.Valid() {
		t.Errorf("`status: doing` is still refused by the WRITTEN schema after widening: %v.\nThe clause would translate at import time against the in-memory index and then be refused at query time — a view that imports clean and errors when opened, which is worse than the loss it replaced", rr.Findings)
	}

	// And the bar this package does not cross: no note that was valid before
	// is invalid after. Widening only ever ADDS a permitted value, so this is
	// arithmetic rather than luck — which is exactly why a failure here would
	// mean the widening did something other than what it claims.
	recs := make([]records.Record, 0, len(notes))
	for _, n := range notes {
		recs = append(recs, n.Rec)
	}
	after := records.Validate(afterSet, recs, records.ValidateOptions{ReportUndeclaredProperties: true})
	var invalid []string
	for _, rr := range after.Records {
		if rr.Recognised && !rr.Valid() {
			invalid = append(invalid, rr.Path+": "+findingsText(rr))
		}
	}
	if len(invalid) != 0 {
		t.Errorf("%d note(s) the importer read are invalid against the WIDENED schema; the bar is zero:\n  %s",
			len(invalid), strings.Join(invalid, "\n  "))
	}
}

func findingsText(rr records.RecordReport) string {
	parts := make([]string, 0, len(rr.Findings))
	for _, f := range rr.Findings {
		parts = append(parts, f.String())
	}
	return strings.Join(parts, "; ")
}
