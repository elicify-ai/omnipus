// Omnipus — regression coverage for the 2026-09-13 UAT finding D-09:
// `join` was accepted, reported complete, and borrowed nothing.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

const bedSchemaYAML = `
schema_version: 1
type: bed
label: Bed
properties:
  location: { type: text }
  sunlight: { type: enum, values: [full, partial, shade] }
  gardener: { type: person }
`

// plantAndBedSet loads plant + bed schemas through the real loader.
func plantAndBedSet(t *testing.T) *records.SchemaSet {
	t.Helper()
	root := t.TempDir()
	dir := records.SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"plant.yaml": plantSchemaYAML, "bed.yaml": bedSchemaYAML} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	set, report, err := records.LoadSchemas(root)
	if err != nil || !report.OK() {
		t.Fatalf("LoadSchemas: %v %v", err, report.Rejections)
	}
	return set
}

// TestUAT_D09_JoinBorrowsTheTargetsDeclaredColumns — Q-04: `join:["owner"]`
// rendered `owner [[Name]]:` and nothing after the colon.
func TestUAT_D09_JoinBorrowsTheTargetsDeclaredColumns(t *testing.T) {
	f := newFixture(t)
	f.set = plantAndBedSet(t)
	f.write("garden/Bed 1.md", "---\ntype: bed\nid: BED-0001\nlocation: south wall\nsunlight: full\ngardener: \"[[Rosa]]\"\n---\n")
	f.plant(1, "growing", "40.0") // bed: [[Bed 1]]
	d := f.deps()
	d.ResolveNear = func(near string) (string, bool) {
		if strings.Contains(near, "Bed 1") {
			return "garden/Bed 1.md", true
		}
		return "", false
	}

	join := []string{"bed"}
	r := req(withType("plant"))
	r.Join = &join
	resp := mustFind(t, d, r)

	if len(resp.Rows) != 1 || len(resp.Rows[0].Joins) != 1 {
		t.Fatalf("expected one row with one join marker, got %s", Render(resp))
	}
	j := resp.Rows[0].Joins[0]
	got := map[string]string{}
	for _, c := range j.Cells {
		got[c.Property] = c.Value
	}
	if got["location"] != "south wall" || got["sunlight"] != "full" {
		t.Fatalf("D-09: the bed's declared columns must be borrowed, got %+v\n%s", got, Render(resp))
	}
	if _, nested := got["gardener"]; nested {
		t.Errorf("a person/relation of the target must not be borrowed a second level down: %+v", got)
	}
	rendered := Render(resp)
	if !strings.Contains(rendered, "bed [[Bed 1]]: location south wall sunlight full") {
		t.Errorf("borrowed columns must render after the marker:\n%s", rendered)
	}
	if !resp.Complete {
		t.Errorf("a fully borrowed join must stay complete: %v", resp.CompleteReason)
	}
}

// TestUAT_D09_UnresolvableJoinTargetIsReportedNotSilent — the marker stays,
// and the reason it carries no columns is a named problem.
func TestUAT_D09_UnresolvableJoinTargetIsReportedNotSilent(t *testing.T) {
	f := newFixture(t)
	f.set = plantAndBedSet(t)
	f.plant(1, "growing", "40.0")
	d := f.deps()
	d.ResolveNear = func(string) (string, bool) { return "", false }

	join := []string{"bed"}
	r := req(withType("plant"))
	r.Join = &join
	resp := mustFind(t, d, r)
	if resp.Complete {
		t.Fatalf("a join that borrowed nothing must not read as complete")
	}
	var found bool
	for _, p := range resp.Problems {
		if p.Code == generated.RecordProblemCodeDanglingRelation && strings.Contains(p.Reason, "no columns could be borrowed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the bare marker must be explained by a problem, got %+v", resp.Problems)
	}
}
