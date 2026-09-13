// Omnipus — regression coverage for the 2026-09-14 Codex review, finding 7:
// a joined record's invalid value (an enum outside its closed set) was
// dropped from the borrowed cells with no problem reported, so the answer
// could still say COMPLETE: yes while a cell was silently missing.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestJoin_InvalidJoinedEnumIsReportedNotSilent — the bed's `sunlight` holds
// "blazing", which is not one of [full, partial, shade]. The borrowed cell
// must show what the file says, the problem list must name the bed, the
// property and the reason, and the verdict must not read complete.
func TestJoin_InvalidJoinedEnumIsReportedNotSilent(t *testing.T) {
	f := newFixture(t)
	f.set = plantAndBedSet(t)
	f.write("garden/Bed 1.md", "---\ntype: bed\nid: BED-0001\nlocation: south wall\nsunlight: blazing\n---\n")
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
	got := map[string]string{}
	for _, c := range resp.Rows[0].Joins[0].Cells {
		got[c.Property] = c.Value
	}
	if got["location"] != "south wall" {
		t.Fatalf("the valid borrowed column must still be present, got %+v", got)
	}
	if v, ok := got["sunlight"]; !ok || v == "" {
		t.Fatalf("the non-conforming borrowed value must render what the file says, not vanish: %+v\n%s", got, Render(resp))
	}

	var named bool
	for _, p := range resp.Problems {
		if p.Code == generated.TypeMismatch &&
			strings.Contains(p.Reason, "garden/Bed 1.md") &&
			strings.Contains(p.Reason, "sunlight") &&
			strings.Contains(p.Reason, "join bed") {
			named = true
		}
	}
	if !named {
		t.Fatalf("the invalid joined value must be a named problem (path, property, relation), got %+v\n%s", resp.Problems, Render(resp))
	}
	if resp.Complete {
		t.Fatalf("an answer with a silently-invalid joined cell must not read complete:\n%s", Render(resp))
	}
}

// TestJoin_AbsentJoinedValueStaysSilent — genuine absence is not a problem:
// a bed with no `sunlight` at all borrows only what it has, and the verdict
// stays complete.
func TestJoin_AbsentJoinedValueStaysSilent(t *testing.T) {
	f := newFixture(t)
	f.set = plantAndBedSet(t)
	f.write("garden/Bed 1.md", "---\ntype: bed\nid: BED-0001\nlocation: south wall\n---\n")
	f.plant(1, "growing", "40.0")
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
	if !resp.Complete {
		t.Fatalf("an absent joined value is legitimate absence, not a problem: %v\n%s", resp.CompleteReason, Render(resp))
	}
	for _, p := range resp.Problems {
		if strings.Contains(p.Reason, "sunlight") {
			t.Fatalf("absence must not be reported as a problem: %+v", p)
		}
	}
}
