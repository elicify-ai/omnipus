// Omnipus — ADR-068 D5/D10, R-8, FR-028, FR-029: grouping by a relation
// compares by target IDENTITY, and an unresolved value is reported, never
// silently rendered as its own group.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package vaultfind

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// ---------------------------------------------------------------------------
// A THIRD, INDEPENDENT FIXTURE
//
// near_test.go's nearFixture already proves this file's exact pattern is the
// right one — a test-controlled resolver stub, because the production
// wikilink resolver (pkg/vaultprops) is out of this package's scope (doc.go).
// This is its OWN declaration rather than a reuse of nearFixture: both are
// Stage 3 work landing in the same package concurrently, and a shared
// mutable fixture two agents both extend is exactly the shared-state risk
// the coordinator's rules exist to avoid. Same "greenhouse" vocabulary
// (ADR-068 D0), independently declared.
// ---------------------------------------------------------------------------

const groupBedYAML = `
schema_version: 1
type: bed
label: Bed
identity:
  prefix: BED
properties:
  name: { type: text, required: true }
`

const groupPlantYAML = `
schema_version: 1
type: plant
label: Plant
identity:
  prefix: PL
properties:
  species:    { type: text, required: true }
  bed:        { type: relation, to: bed, inverse: plants }
  companions: { type: relation, to: plant, many: true }
`

func groupSchemaSet(t *testing.T) *records.SchemaSet {
	t.Helper()
	root := t.TempDir()
	dir := records.SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bed.yaml"), []byte(groupBedYAML), 0o600); err != nil {
		t.Fatalf("WriteFile(bed.yaml): %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plant.yaml"), []byte(groupPlantYAML), 0o600); err != nil {
		t.Fatalf("WriteFile(plant.yaml): %v", err)
	}
	set, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("the fixture schema was rejected: %v", report.Rejections)
	}
	return set
}

type groupFixture struct {
	t       *testing.T
	store   propindex.Store
	set     *records.SchemaSet
	text    *stubText
	targets map[string]string // wikilink Target text -> record identity
}

func newGroupFixture(t *testing.T) *groupFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "properties.db")
	store, err := propindex.Open(context.Background(), path, propindex.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return &groupFixture{
		t: t, store: store, set: groupSchemaSet(t),
		text:    &stubText{hits: map[string]TextHit{}},
		targets: map[string]string{},
	}
}

func (f *groupFixture) write(path, src string) {
	f.t.Helper()
	b := []byte(src)
	rec := records.ParseRecord(path, b)
	sc, _ := f.set.Get(rec.TypeName())
	rows := propindex.BuildNoteRows(rec, sc, b, propindex.SourceHash(b))
	if err := f.store.UpsertNote(context.Background(), rows); err != nil {
		f.t.Fatalf("UpsertNote(%s): %v", path, err)
	}
	f.text.hits[path] = TextHit{Path: path, SourceHash: rows.SourceHash, Score: 1}
}

// bed writes a bed record and registers EVERY name it may legally be
// addressed by — a real vault can hold several wikilinks to one target
// (a rename ADR-067 D10 has not yet rewritten everywhere, or a deliberate
// [[Target|alias]]), and D5/R-8's whole claim is that grouping must not
// care which spelling a particular source record happened to use.
func (f *groupFixture) bed(id string, names ...string) {
	f.t.Helper()
	primary := names[0]
	f.write(fmt.Sprintf("garden/beds/%s.md", id), fmt.Sprintf(`---
type: bed
id: %s
name: %s
---
`, id, primary))
	for _, n := range names {
		f.targets[n] = id
	}
}

func (f *groupFixture) plant(id, species, bedTarget string) {
	f.t.Helper()
	f.write(fmt.Sprintf("garden/plants/%s.md", id), fmt.Sprintf(`---
type: plant
id: %s
species: %s
bed: "[[%s]]"
---
`, id, species, bedTarget))
}

// plantWithCompanions writes a plant record whose `companions` relation
// (many: true) holds the given target texts, verbatim — deliberately not
// necessarily the plants' own IDs, so a test can address one target through
// two different spellings the way it addresses a bed through two names.
func (f *groupFixture) plantWithCompanions(id, species string, companionTargets ...string) {
	f.t.Helper()
	quoted := make([]string, 0, len(companionTargets))
	for _, c := range companionTargets {
		quoted = append(quoted, fmt.Sprintf("\"[[%s]]\"", c))
	}
	companions := ""
	if len(quoted) > 0 {
		companions = fmt.Sprintf("companions: [%s]\n", strings.Join(quoted, ", "))
	}
	f.write(fmt.Sprintf("garden/plants/%s.md", id), fmt.Sprintf(`---
type: plant
id: %s
species: %s
%s---
`, id, species, companions))
	// A plant is itself a legal companion target, addressed by its own id.
	f.targets[id] = id
}

func (f *groupFixture) resolve(w records.Wikilink) (string, bool) {
	id, ok := f.targets[w.Target]
	return id, ok
}

func (f *groupFixture) deps() Deps {
	return Deps{Schemas: f.set, Store: f.store, Text: f.text, Resolve: f.resolve, Epoch: 1}
}

// ---------------------------------------------------------------------------

// TestGroupByRelation_IdentityNotDisplayText is D5/R-8 applied to FR-029: two
// plants whose `bed` wikilink names the SAME target through TWO DIFFERENT
// spellings ("Greenhouse" and the alias "The Big Greenhouse") land in ONE
// group, because grouping compares the resolved record identity, not the
// wikilink's own text.
func TestGroupByRelation_IdentityNotDisplayText(t *testing.T) {
	f := newGroupFixture(t)
	f.bed("BED-GH", "Greenhouse", "The Big Greenhouse")
	f.plant("PL-0001", "Monstera", "Greenhouse")
	f.plant("PL-0002", "Fern", "The Big Greenhouse")

	groups := []string{"bed"}
	r := req(withType("plant"))
	r.GroupBy = &groups
	resp := mustFind(t, f.deps(), r)

	if resp.Groups == nil {
		t.Fatalf("no groups were returned")
	}
	if got := len(*resp.Groups); got != 1 {
		t.Fatalf("got %d groups for two spellings of ONE target, want 1: %+v", got, *resp.Groups)
	}
	g := (*resp.Groups)[0]
	if g.Count != 2 {
		t.Errorf("group count = %d, want 2 (both plants, one target, one group)", g.Count)
	}
}

// TestGroupByRelation_UnresolvedIsExcludedAndReported is D5's own sentence:
// "reported... never silently rendered as a distinct group of one." A plant
// whose bed wikilink names nothing the resolver knows about must NOT form a
// group by itself, must NOT be folded into the "absent" group (it HAS a
// value — the value simply does not resolve), and MUST be named in the
// problem list.
func TestGroupByRelation_UnresolvedIsExcludedAndReported(t *testing.T) {
	f := newGroupFixture(t)
	f.bed("BED-GH", "Greenhouse")
	f.plant("PL-0001", "Monstera", "Greenhouse")
	f.plant("PL-0002", "Fern", "Nonexistent Bed")

	groups := []string{"bed"}
	r := req(withType("plant"))
	r.GroupBy = &groups
	resp, err := Find(context.Background(), f.deps(), r)
	if err != nil {
		t.Fatalf("Find: unexpected refusal: %v", err)
	}
	assertResponseInvariants(t, resp)

	if resp.Groups == nil {
		t.Fatalf("no groups were returned")
	}
	for _, g := range *resp.Groups {
		if g.Absent != nil && *g.Absent {
			t.Errorf("PL-0002's unresolved bed landed in the ABSENT group — it HAS a value, "+
				"it simply does not resolve; folding it into absence misreports it: %+v", g)
		}
		for _, p := range g.Paths {
			if p == "garden/plants/PL-0002.md" {
				t.Errorf("PL-0002 (unresolved bed) appears in group %q — D5 forbids a "+
					"silent group of one for an unresolved relation", g.Key)
			}
		}
	}
	// Exactly one real group (Greenhouse, PL-0001 only).
	if got := len(*resp.Groups); got != 1 {
		t.Fatalf("got %d groups, want 1 (PL-0002's unresolved value forms none): %+v", got, *resp.Groups)
	}
	if (*resp.Groups)[0].Count != 1 {
		t.Errorf("Greenhouse group count = %d, want 1 (PL-0001 only)", (*resp.Groups)[0].Count)
	}

	found := false
	for _, p := range resp.Problems {
		if p.Code == generated.DanglingRelation {
			for _, rec := range p.Records {
				if rec == "PL-0002" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("PL-0002's unresolved bed was not reported as a dangling_relation problem: %+v", resp.Problems)
	}
	// COMPLETE must say no — a query that silently dropped a record from
	// grouping without saying so is exactly AC-P1's failure.
	if resp.Complete {
		t.Errorf("COMPLETE is true despite an unresolved relation being excluded from grouping")
	}
}

// TestGroupByText_FoldsAcrossRecords proves the sibling fix this change made
// while it was already rewriting the bucket-identity code: FR-011a requires
// text matching to be case-INSENSITIVE, and grouping is a form of equality
// (R-5's whole argument, applied to `text` rather than `enum`) — two records
// spelling the SAME value in different case must land in ONE group, not
// fork on the raw bytes.
func TestGroupByText_FoldsAcrossRecords(t *testing.T) {
	f := newGroupFixture(t)
	f.bed("BED-GH", "Greenhouse")
	f.plant("PL-0001", "Monstera", "Greenhouse")
	f.plant("PL-0002", "monstera", "Greenhouse")
	f.plant("PL-0003", "MONSTERA", "Greenhouse")

	groups := []string{"species"}
	r := req(withType("plant"))
	r.GroupBy = &groups
	resp := mustFind(t, f.deps(), r)

	if resp.Groups == nil {
		t.Fatalf("no groups were returned")
	}
	if got := len(*resp.Groups); got != 1 {
		names := make([]string, 0, got)
		for _, g := range *resp.Groups {
			names = append(names, fmt.Sprintf("%q(%d)", g.Key, g.Count))
		}
		t.Fatalf("got %d groups for three spellings of one text value, want 1: %v", got, names)
	}
	if (*resp.Groups)[0].Count != 3 {
		t.Errorf("group count = %d, want 3", (*resp.Groups)[0].Count)
	}
}

// TestGroupByRelation_NoResolverDegradesRatherThanExcludesEverything proves
// the stated degraded mode: with NO resolver wired at all (Deps.Resolve is
// nil, as it legitimately is on a caller that never set one up), grouping by
// a relation still runs — folded on the raw wikilink text — rather than
// silently excluding every relation value from every group. The degradation
// is visible elsewhere in the same response (a filter on the relation would
// report CompareRelationUnresolved); grouping choosing to return NOTHING
// here would be a second, worse silence layered on top of the first.
func TestGroupByRelation_NoResolverDegradesRatherThanExcludesEverything(t *testing.T) {
	f := newGroupFixture(t)
	f.bed("BED-GH", "Greenhouse")
	f.plant("PL-0001", "Monstera", "Greenhouse")

	d := f.deps()
	d.Resolve = nil

	groups := []string{"bed"}
	r := req(withType("plant"))
	r.GroupBy = &groups
	resp := mustFind(t, d, r)

	if resp.Groups == nil || len(*resp.Groups) != 1 {
		t.Fatalf("grouping with no resolver returned %v groups, want exactly 1 (degraded, not empty)", resp.Groups)
	}
}

// TestGroupByRelation_ManyValuedRecordJoinsEveryGroup is FR-028 for a
// RELATION property specifically — the earlier tests in this file each use a
// record with exactly ONE relation value, which cannot exercise "a record
// appears in EVERY group it belongs to" at all: with one value there is only
// ever one group to appear in, and an assertion built over that fixture would
// pass identically whether or not the fan-out logic is even wired up. This
// fixture gives PL-0001 TWO companions, so the property this test names is
// actually observable.
//
// It also exercises identity-based dedupe WITHIN a multi-value list: PL-0002
// names PL-0003 as a companion through an ALIAS ("Cactus Friend") rather than
// PL-0003's own id, and must still land in PL-0003's group rather than
// forking a second one — R-8 inside a `many` property, not only across
// records.
func TestGroupByRelation_ManyValuedRecordJoinsEveryGroup(t *testing.T) {
	f := newGroupFixture(t)
	f.targets["Cactus Friend"] = "PL-0003"
	// PL-0001 names PL-0003 TWICE, through two different spellings within its
	// OWN list — "PL-0003" and the alias "Cactus Friend" both resolve to the
	// SAME identity. This is the within-record twin of the alias fixture
	// below: it must dedupe to ONE membership of PL-0003's group, not count
	// PL-0001 twice because the two spellings looked different on the page.
	f.plantWithCompanions("PL-0001", "Monstera", "PL-0002", "PL-0003", "Cactus Friend")
	f.plantWithCompanions("PL-0002", "Fern", "Cactus Friend")
	f.plantWithCompanions("PL-0003", "Cactus")

	groups := []string{"companions"}
	r := req(withType("plant"))
	r.GroupBy = &groups
	resp := mustFind(t, f.deps(), r)

	if resp.Groups == nil {
		t.Fatalf("no groups were returned")
	}
	byKey := map[string]generated.VaultFindGroup{}
	absent := 0
	for _, g := range *resp.Groups {
		if g.Absent != nil && *g.Absent {
			absent++
			continue
		}
		byKey[g.Key] = g
	}

	// PL-0001 belongs to BOTH the PL-0002 group and the PL-0003 group — the
	// core FR-028 fact this fixture exists to make observable.
	inGroup := func(g generated.VaultFindGroup, path string) bool {
		for _, p := range g.Paths {
			if p == path {
				return true
			}
		}
		return false
	}
	if g, ok := byKey["[[PL-0002]]"]; !ok || !inGroup(g, "garden/plants/PL-0001.md") {
		t.Fatalf("PL-0001 is missing from its OWN companion PL-0002's group: %+v", byKey["[[PL-0002]]"])
	}
	if g, ok := byKey["[[PL-0003]]"]; !ok || !inGroup(g, "garden/plants/PL-0001.md") {
		t.Fatalf("PL-0001 is missing from its OWN companion PL-0003's group: %+v", byKey["[[PL-0003]]"])
	}

	// PL-0002 names PL-0003 through the alias "Cactus Friend" — it must land
	// in the SAME PL-0003 group as PL-0001's direct reference, not a second
	// group keyed on the alias text.
	if got := len(*resp.Groups) - absent; got != 2 {
		names := make([]string, 0, got)
		for k := range byKey {
			names = append(names, k)
		}
		t.Fatalf("got %d non-absent groups, want 2 (PL-0002 and PL-0003): %v", got, names)
	}
	if g := byKey["[[PL-0003]]"]; g.Count != 2 || !inGroup(g, "garden/plants/PL-0002.md") {
		t.Fatalf("PL-0003's group = %+v, want PL-0001 AND PL-0002 (the latter via its alias \"Cactus Friend\")", g)
	}
	// PL-0001 itself names PL-0003 TWICE in its own list, via two spellings —
	// it must still count ONCE in PL-0003's group, not twice. This is the
	// case a within-record dedupe keyed on DISPLAY TEXT rather than resolved
	// IDENTITY would get wrong while every other assertion in this test still
	// passed (found by mutation, not by inspection): "PL-0003" and "Cactus
	// Friend" render as different strings and would look like two distinct
	// values to a text-keyed dedupe, even though R-8 says they are one.
	pl0001Count := 0
	for _, p := range byKey["[[PL-0003]]"].Paths {
		if p == "garden/plants/PL-0001.md" {
			pl0001Count++
		}
	}
	if pl0001Count != 1 {
		t.Fatalf("PL-0001 appears %d times in PL-0003's group, want 1 — it named PL-0003 "+
			"twice in its own companions list, via two spellings of ONE identity", pl0001Count)
	}
	if g := byKey["[[PL-0002]]"]; g.Count != 1 {
		t.Fatalf("PL-0002's group count = %d, want 1 (PL-0001 only)", g.Count)
	}

	// FR-028, stated as an assertion rather than left implicit: group counts
	// SUM TO MORE than the number of records that matched (2: PL-0001 and
	// PL-0002 each have companions; PL-0003 has none). This is NOT a double
	// count. It is Obsidian's rejected alternative — one combined group per
	// distinct COMBINATION of values — that the spec (ADR-068 D10) names by
	// example as useless for categorisation: a record naming two companions
	// belongs in both companions' views, and a system answering "who lists
	// PL-0003 as a companion" by checking only one group per record would
	// answer it wrong.
	matchedRecords := 2 // PL-0001, PL-0002 (PL-0003 is absent, not matched)
	groupSum := byKey["[[PL-0002]]"].Count + byKey["[[PL-0003]]"].Count
	if groupSum <= matchedRecords {
		t.Fatalf("group counts summed to %d, want MORE than %d matched records — "+
			"FR-028 requires PL-0001 to be counted in BOTH its groups, which is what "+
			"makes the sum exceed the match count in the first place", groupSum, matchedRecords)
	}
	if groupSum != 3 {
		t.Errorf("group counts summed to %d, want exactly 3 (1 + 2)", groupSum)
	}

	// The absent group is real too: PL-0003 has no companions of its own.
	if absent != 1 {
		t.Errorf("expected exactly one absent group (PL-0003 has no companions), got %d", absent)
	}
}
