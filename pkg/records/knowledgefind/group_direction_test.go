// Omnipus — knowledge_find: the direction a request groups in.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// A GROUP DIRECTION, AND WHAT IT MEANS PER DECLARED TYPE
//
// `group_by` used to be a bare list of property names. A saved view's own
// `grouping` keys have carried a direction since ADR-068 D24.1, so a view that
// recorded DESC was written to disk faithfully and then REFUSED at serve time
// (records.ServeRefusalGroupDirection) — correctly, because the alternative
// was reordering the operator's groups ascending with nothing anywhere to say
// so. These tests are the other half of closing that: the request carries the
// direction now, and what it MEANS is asserted here rather than assumed.
// ---------------------------------------------------------------------------

// groupKeysOf reads the outer groups' display keys in the order the response
// returned them. The ORDER is the whole subject, so nothing here sorts.
func groupKeysOf(t *testing.T, resp generated.VaultFindResponse) []string {
	t.Helper()
	if resp.Groups == nil {
		t.Fatal("the response carries no groups at all; the request asked for group_by")
	}
	out := make([]string, 0, len(*resp.Groups))
	for _, g := range *resp.Groups {
		if g.Absent != nil && *g.Absent {
			out = append(out, "<absent>")
			continue
		}
		out = append(out, g.Key)
	}
	return out
}

func groupBy(keys ...generated.VaultFindGroupBy) *[]generated.VaultFindGroupBy {
	out := append([]generated.VaultFindGroupBy(nil), keys...)
	return &out
}

func descKey(property string) generated.VaultFindGroupBy {
	d := generated.VaultFindGroupByDirectionDesc
	return generated.VaultFindGroupBy{Property: property, Direction: &d}
}

func ascKey(property string) generated.VaultFindGroupBy {
	d := generated.VaultFindGroupByDirectionAsc
	return generated.VaultFindGroupBy{Property: property, Direction: &d}
}

// TestGroupDirection_EnumOrdersLexicallyInBothDirections.
//
// R-5/R-E, and it is the ruling this test exists to pin rather than the
// mechanism. `condition` is declared `[seedling, growing, dormant]`. A reader
// who expects `desc` to walk that list backwards would expect
// dormant/growing/seedling; what they get is seedling/growing/dormant, because
// an enum has NO declared-position ordinal — records.comparisonDomain maps
// TypeEnum onto TypeText, so the order is LEXICAL over the value:
// dormant < growing < seedling ascending, and the exact reverse descending.
//
// The two directions are asserted against each other as well as against their
// literal expectations: an implementation that ignored the direction entirely
// would satisfy the ascending case and fail here.
func TestGroupDirection_EnumOrdersLexicallyInBothDirections(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "seedling", "10.0")
	f.plant(2, "growing", "20.0")
	f.plant(3, "dormant", "30.0")
	d := f.deps()

	plant := "plant"
	asc := groupKeysOf(t, mustFind(t, d, generated.VaultFindRequest{
		Type: &plant, GroupBy: groupBy(ascKey("condition")),
	}))
	desc := groupKeysOf(t, mustFind(t, d, generated.VaultFindRequest{
		Type: &plant, GroupBy: groupBy(descKey("condition")),
	}))

	wantAsc := []string{"dormant", "growing", "seedling"}
	if strings.Join(asc, ",") != strings.Join(wantAsc, ",") {
		t.Fatalf("ascending groups = %v, want %v (LEXICAL over the value, not the order the enum declares them in)", asc, wantAsc)
	}
	wantDesc := []string{"seedling", "growing", "dormant"}
	if strings.Join(desc, ",") != strings.Join(wantDesc, ",") {
		t.Fatalf("descending groups = %v, want %v — the exact reverse of ascending.\n"+
			"If this returned the ascending order the direction is being dropped, which is the silence "+
			"records.ServeRefusalGroupDirection used to refuse rather than perform.", desc, wantDesc)
	}
}

// TestGroupDirection_NumberOrdersNaturallyNotLexically is the assertion that
// makes the whole feature worth having, and it is deliberately built so that
// the WRONG implementation is the plausible one.
//
// The counts are 2, 9 and 12. Ordered as TEXT — which is how every group used
// to be ordered, on the folded DISPLAY key — descending gives 9, 2, 12,
// because `9` > `2` > `1`. That answer is not merely unsorted: it is
// confidently wrong, and it is what "Most Connected" (the founder's own base,
// grouping `formula.backlink_count` DESC) would have rendered as its headline.
//
// Ordered as NUMBERS it is 12, 9, 2.
func TestGroupDirection_NumberOrdersNaturallyNotLexically(t *testing.T) {
	f := newFixture(t)
	for i, cuttings := range []int{2, 9, 12} {
		f.write(fmt.Sprintf("garden/n-%d.md", i), fmt.Sprintf(`---
type: plant
id: PL-90%02d
species: Ficus
cuttings: %d
---
`, i, cuttings))
	}
	d := f.deps()

	plant := "plant"
	desc := groupKeysOf(t, mustFind(t, d, generated.VaultFindRequest{
		Type: &plant, GroupBy: groupBy(descKey("cuttings")),
	}))
	if want := []string{"12", "9", "2"}; strings.Join(desc, ",") != strings.Join(want, ",") {
		t.Fatalf("descending groups on an integer = %v, want %v.\n"+
			"[9 2 12] means the groups are ordered as TEXT: `9` sorts before `2` sorts before `1`. "+
			"A count is a number and must order as one.", desc, want)
	}

	asc := groupKeysOf(t, mustFind(t, d, generated.VaultFindRequest{
		Type: &plant, GroupBy: groupBy(ascKey("cuttings")),
	}))
	if want := []string{"2", "9", "12"}; strings.Join(asc, ",") != strings.Join(want, ",") {
		t.Fatalf("ascending groups on an integer = %v, want %v — the same natural order, forwards", asc, want)
	}
}

// TestGroupDirection_DateOrdersChronologically. Same rule, the other natural
// domain. The three dates are chosen so text order and chronological order
// AGREE on ISO-8601 — they always do — which is precisely why the ascending
// case proves little on its own and the descending case is the assertion.
func TestGroupDirection_DateOrdersChronologically(t *testing.T) {
	f := newFixture(t)
	for i, planted := range []string{"2026-01-09", "2026-02-28", "2026-11-02"} {
		f.write(fmt.Sprintf("garden/d-%d.md", i), fmt.Sprintf(`---
type: plant
id: PL-91%02d
species: Fern
planted: %s
---
`, i, planted))
	}
	d := f.deps()

	plant := "plant"
	desc := groupKeysOf(t, mustFind(t, d, generated.VaultFindRequest{
		Type: &plant, GroupBy: groupBy(descKey("planted")),
	}))
	if want := []string{"2026-11-02", "2026-02-28", "2026-01-09"}; strings.Join(desc, ",") != strings.Join(want, ",") {
		t.Fatalf("descending groups on a date = %v, want %v (most recent first)", desc, want)
	}
}

// TestGroupDirection_AbsentGroupIsLastInBothDirections is the DECISION, stated
// in the contract (VaultFindGroupBy) and pinned here.
//
// A record with no value has not got a small value and has not got a large one
// either: absence is OUTSIDE the order, not at one end of it. Letting `desc`
// reverse it would put "nobody recorded this" first — exactly where a reader
// asking for the biggest group is looking. It is the same rule row sorting
// already applies (assemble.go's compareByProperty), and the two must not
// disagree inside one response.
func TestGroupDirection_AbsentGroupIsLastInBothDirections(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "10.0")
	f.plant(2, "dormant", "20.0")
	f.write("garden/nocondition.md", `---
type: plant
id: PL-9200
species: Aloe
---
`)
	d := f.deps()

	plant := "plant"
	for _, tc := range []struct {
		name string
		key  generated.VaultFindGroupBy
	}{
		{"ascending", ascKey("condition")},
		{"descending", descKey("condition")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := groupKeysOf(t, mustFind(t, d, generated.VaultFindRequest{
				Type: &plant, GroupBy: groupBy(tc.key),
			}))
			if len(got) != 3 {
				t.Fatalf("groups = %v, want three (dormant, growing and the absent one)", got)
			}
			if got[len(got)-1] != "<absent>" {
				t.Fatalf("groups = %v; the absent group must be LAST in both directions — "+
					"absence is outside the order, not the largest value in it", got)
			}
		})
	}
}

// TestGroupDirection_EachLevelCarriesItsOwn. Two levels, opposite directions.
// A single shared direction would pass whichever half it happened to match, so
// the test asks for one of each and checks both.
func TestGroupDirection_EachLevelCarriesItsOwn(t *testing.T) {
	f := newFixture(t)
	// Two species, each with three conditions, so both levels have something
	// to order.
	for i, sp := range []string{"Aloe", "Zamia"} {
		for j, cond := range []string{"seedling", "growing", "dormant"} {
			f.write(fmt.Sprintf("garden/l-%d-%d.md", i, j), fmt.Sprintf(`---
type: plant
id: PL-93%d%d
species: %s
condition: %s
---
`, i, j, sp, cond))
		}
	}
	d := f.deps()

	plant := "plant"
	resp := mustFind(t, d, generated.VaultFindRequest{
		Type:    &plant,
		GroupBy: groupBy(ascKey("species"), descKey("condition")),
	})
	outer := groupKeysOf(t, resp)
	if want := []string{"Aloe", "Zamia"}; strings.Join(outer, ",") != strings.Join(want, ",") {
		t.Fatalf("outer groups = %v, want %v ascending", outer, want)
	}
	for _, g := range *resp.Groups {
		if g.Subgroups == nil {
			t.Fatalf("group %q has no subgroups; the request named two levels", g.Key)
		}
		var sub []string
		for _, s := range *g.Subgroups {
			sub = append(sub, s.Key)
		}
		want := []string{"seedling", "growing", "dormant"}
		if strings.Join(sub, ",") != strings.Join(want, ",") {
			t.Fatalf("subgroups of %q = %v, want %v.\n"+
				"The inner level declares `desc` and the outer declares `asc`; applying the outer "+
				"key's direction to both answers a question nobody put.", g.Key, sub, want)
		}
	}
}

// TestGroupDirection_OmittedMeansAscending pins the documented default. Stated
// in prose in the contract rather than as a JSON Schema `default:` (the two
// generators disagree about a defaulted property), so nothing but a test holds
// the two ends together.
func TestGroupDirection_OmittedMeansAscending(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "seedling", "10.0")
	f.plant(2, "dormant", "20.0")
	d := f.deps()

	plant := "plant"
	omitted := groupKeysOf(t, mustFind(t, d, generated.VaultFindRequest{
		Type: &plant, GroupBy: groupBy(generated.VaultFindGroupBy{Property: "condition"}),
	}))
	explicit := groupKeysOf(t, mustFind(t, d, generated.VaultFindRequest{
		Type: &plant, GroupBy: groupBy(ascKey("condition")),
	}))
	if strings.Join(omitted, ",") != strings.Join(explicit, ",") {
		t.Fatalf("omitted direction gave %v and an explicit asc gave %v; omitted MEANS asc", omitted, explicit)
	}
	if want := []string{"dormant", "seedling"}; strings.Join(omitted, ",") != strings.Join(want, ",") {
		t.Fatalf("groups = %v, want %v", omitted, want)
	}
}

// TestGroupDirection_UnknownSpellingIsRefusedByName.
//
// `desc := *g.Direction == "desc"` alone would make `descending`, `DESC` and
// `down` all group ASCENDING with nothing said — a view asking for "most
// connected first" answering with the least connected first and reporting
// itself complete. The generated enum already knows which two spellings exist;
// this asserts something asks it. It is the same rule `sort` applies, and the
// asymmetry between the two was itself once the defect.
func TestGroupDirection_UnknownSpellingIsRefusedByName(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "10.0")
	d := f.deps()

	plant := "plant"
	bad := generated.VaultFindGroupByDirection("descending")
	resp := mustRefuse(t, d, generated.VaultFindRequest{
		Type:    &plant,
		GroupBy: groupBy(generated.VaultFindGroupBy{Property: "condition", Direction: &bad}),
	})
	reason := resp.Problems[0].Reason
	if !strings.Contains(reason, "descending") {
		t.Errorf("the refusal does not quote the spelling it rejected: %q", reason)
	}
	if !strings.Contains(reason, "condition") {
		t.Errorf("the refusal does not name the group key it rejected: %q", reason)
	}
	if resp.Problems[0].Permitted == nil {
		t.Fatalf("the refusal does not list the permitted directions; %q leaves a caller with an error and nothing to do about it", reason)
	}
	if got := strings.Join(*resp.Problems[0].Permitted, ","); got != "asc,desc" {
		t.Errorf("permitted = %q, want \"asc,desc\"", got)
	}
}

// TestGroupDirection_ExecutedEchoNamesIt. FR-122 makes `query_echo` a claim
// about what RAN. A descending grouping echoed as a bare property name is the
// original silence moved one surface along: the answer would be ordered one
// way and described another.
func TestGroupDirection_ExecutedEchoNamesIt(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "10.0")
	d := f.deps()

	plant := "plant"
	resp := mustFind(t, d, generated.VaultFindRequest{
		Type: &plant, GroupBy: groupBy(descKey("condition")),
	})
	if !strings.Contains(resp.QueryEcho, "group_by=condition desc") {
		t.Errorf("query_echo = %q; it must name the direction the grouping actually ran in", resp.QueryEcho)
	}
}
