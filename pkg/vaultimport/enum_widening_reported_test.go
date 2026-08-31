// Omnipus — the end-to-end guard on the enum widening's ACCOUNT.
//
// WHY THIS FILE IS SEPARATE FROM infer_enum_widening_test.go, AND WHY IT IS
// THE ONLY ONE OF THE TWO THAT WOULD HAVE CAUGHT THE REAL DEFECT.
//
// Every test in that file calls WidenEnumsFromBases directly and asserts on
// the value it returns and on the declaration it mutated. All 21 of them
// passed while the feature was, in the way that matters to the operator,
// broken: `Run` widened the schema and the REPORT said nothing, because
// CollectEnumWidenings was defined, named once in a comment, and never
// called. The schema grew and nobody was told.
//
// That is precisely the failure the rule was approved on the promise of not
// committing. Widening rather than refusing is only defensible because the
// operator is TOLD — a silent widening makes a mistyped filter match nothing
// forever and look perfectly healthy, which is worse than the disabled view
// it replaced. So the account is not a nicety attached to the feature; it is
// the condition the feature is allowed to exist under, and it needs a test at
// the level where it can go missing: the whole of `Run`, over a real vault on
// disk, asserting the Report.
//
// The shape of the escape is worth naming, because it is general: a unit test
// that calls the function under test directly can never fail for a missing
// CALL to that function. Anything whose value lies in being wired needs one
// test that does not do the wiring itself.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vaultWithAnUnobservedEnumFilter builds the smallest vault that reproduces
// the founder's Tasks.base situation: notes carrying three states, and a base
// filtering on a fourth that none of them carries.
func vaultWithAnUnobservedEnumFilter(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	notes := []struct{ name, status string }{
		{"a.md", "todo"}, {"b.md", "todo"}, {"c.md", "done"},
		{"d.md", "done"}, {"e.md", "blocked"},
	}
	for _, n := range notes {
		body := "---\ntype: task\nstatus: " + n.status + "\n---\n\nbody\n"
		if err := os.WriteFile(filepath.Join(root, n.name), []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", n.name, err)
		}
	}

	base := `filters:
  and:
    - type == "task"
views:
  - type: table
    name: Doing now
    filters:
      and:
        - status == "doing"
    order:
      - file.name
      - status
`
	if err := os.WriteFile(filepath.Join(root, "Tasks.base"), []byte(base), 0o644); err != nil {
		t.Fatalf("writing Tasks.base: %v", err)
	}
	return root
}

// TestRun_ReportsTheEnumWideningItPerformed is the guard. It asserts the
// account survives the WIRING, not merely that WidenEnumsFromBases can
// produce one.
func TestRun_ReportsTheEnumWideningItPerformed(t *testing.T) {
	root := vaultWithAnUnobservedEnumFilter(t)

	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	// First establish the widening actually happened, so a failure below is
	// unambiguously about the ACCOUNT and not about the rule not firing.
	schemaPath := filepath.Join(root, ".omnipus-vault", "records", "task.yaml")
	written, readErr := os.ReadFile(schemaPath)
	if readErr != nil {
		t.Fatalf("reading the schema this run wrote: %v", readErr)
	}
	if !strings.Contains(string(written), "doing") {
		t.Fatalf("the written schema does not contain `doing`, so this fixture is not exercising the rule at all:\n%s", written)
	}

	if len(rep.EnumWidenings) == 0 {
		t.Fatalf("THE SCHEMA GREW AND THE REPORT SAYS NOTHING. `task.status` gained `doing` on the strength of a `.base` file and the operator is not told. Widening is only defensible because it is reported — silently, a mistyped filter matches nothing forever and looks healthy, which is worse than the disabled view it replaced.")
	}

	var found *EnumWidening
	for i := range rep.EnumWidenings {
		if rep.EnumWidenings[i].RecordType == "task" && rep.EnumWidenings[i].Property == "status" {
			found = &rep.EnumWidenings[i]
		}
	}
	if found == nil {
		t.Fatalf("the report carries %d widening(s), none of them task.status: %+v", len(rep.EnumWidenings), rep.EnumWidenings)
	}
	if len(found.Added) != 1 || found.Added[0] != "doing" {
		t.Errorf("the account says Added=%v, want exactly [doing]", found.Added)
	}
	if !containsString(found.Bases, "Tasks.base") {
		t.Errorf("the account names bases %v — the operator has to be told which file to open", found.Bases)
	}

	// And the account has to be legible, not merely present.
	joined := strings.Join(found.ReportLines(), "\n")
	for _, want := range []string{"doing", "Tasks.base", "no note", "knowledge_configure"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the account never mentions %q — an operator cannot check or correct what it does not say:\n%s", want, joined)
		}
	}
}

// TestRun_TheRenderedReportShowsTheWidening closes the last gap between "the
// Report struct holds it" and "a human running the importer sees it". A field
// nothing prints is the same silence one field further along.
func TestRun_TheRenderedReportShowsTheWidening(t *testing.T) {
	root := vaultWithAnUnobservedEnumFilter(t)

	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if len(rep.EnumWidenings) == 0 {
		t.Skip("no widening was recorded — TestRun_ReportsTheEnumWideningItPerformed is the test for that, and it fails loudly")
	}

	var buf bytes.Buffer
	rep.Render(&buf)
	out := buf.String()

	if !strings.Contains(out, "doing") {
		t.Errorf("the RENDERED report never prints the admitted value `doing`. The Report carries the account and nothing puts it on the page, which is the same silence one field further along — the operator still cannot see that his `task.status` grew:\n%s", out)
	}
}

// TestRun_ReportsNoWideningWhenEveryLiteralWasObserved is the negative half.
// Without it the two tests above could be satisfied by a report that always
// claims a widening, which would train the operator to ignore the notice.
func TestRun_ReportsNoWideningWhenEveryLiteralWasObserved(t *testing.T) {
	root := t.TempDir()
	for i, status := range []string{"todo", "todo", "done", "done", "blocked"} {
		body := "---\ntype: task\nstatus: " + status + "\n---\n\nbody\n"
		name := string(rune('a'+i)) + ".md"
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	base := `filters:
  and:
    - type == "task"
views:
  - type: table
    name: Done
    filters:
      and:
        - status == "done"
`
	if err := os.WriteFile(filepath.Join(root, "Tasks.base"), []byte(base), 0o644); err != nil {
		t.Fatalf("writing Tasks.base: %v", err)
	}

	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if len(rep.EnumWidenings) != 0 {
		t.Errorf("the report claims %d widening(s) over a base whose every literal the notes already carry: %+v — a notice that fires when nothing happened is a notice the operator learns to skip", len(rep.EnumWidenings), rep.EnumWidenings)
	}
}
