// Omnipus — the founder's own descending-grouping views, served end to end.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// THE VIEW THAT IMPORTED AND THEN COULD NOT BE USED
//
// `Entities.base` groups its "Most Connected" view by
// `formula.backlink_count` DESCENDING. The importer carried that faithfully —
// `ViewGroupBy.direction` has existed on the view types since ADR-068 D24.1 —
// and the view was written ENABLED. Serving it was then REFUSED
// (records.ServeRefusalGroupDirection), because a knowledge_find request's
// `group_by` was a bare list of property names with nowhere to put the answer.
// The refusal was right: the alternative was returning the groups ascending
// and letting a reordering nobody asked for pass as the answer.
//
// `group_by` carries a direction per key now. This is the end-to-end check
// that the founder's own view is REACHABLE — not that a unit test of the
// ordering passes, which is asserted next door in
// pkg/records/knowledgefind/group_direction_test.go.
// ---------------------------------------------------------------------------

// TestFixtureVault_DescendingGroupViewsAreServable walks every view in the
// real vault whose grouping declares a direction, and requires that NONE of
// them is refused for its direction.
//
// It asserts on the whole class rather than on one named view on purpose: a
// second base acquiring a descending grouping tomorrow must be covered without
// anyone remembering to add it here.
func TestFixtureVault_DescendingGroupViewsAreServable(t *testing.T) {
	root := fixtureVaultCopy(t)
	if _, err := Run(root, true); err != nil {
		t.Fatalf("import failed: %v", err)
	}
	schemas, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("loading schemas: %v", err)
	}
	views, viewRep, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("loading views: %v", err)
	}
	if !viewRep.OK() {
		t.Fatalf("the importer wrote views the real loader rejects: %v", viewRep.Rejections)
	}
	loader := records.NewViewFindLoader(views)

	directional := 0
	for _, sv := range views.Views() {
		if sv.Def.Grouping == nil {
			continue
		}
		var declared []string
		for _, g := range *sv.Def.Grouping {
			if g.Direction != nil {
				declared = append(declared, g.Property+"/"+string(*g.Direction))
			}
		}
		if len(declared) == 0 {
			continue
		}
		directional++

		// A DISABLED view is refused for FR-105 reasons that have nothing to
		// do with the direction, so the direction is judged with `disabled`
		// cleared — otherwise this test would report a pass it did not earn on
		// any view that happens to be disabled for another cause.
		wasDisabled := sv.Def.Disabled != nil && *sv.Def.Disabled
		sv.Def.Disabled = nil

		name := sv.Name()
		if refusal, refused := loader.ServeRefusal(name); refused {
			t.Errorf("view %q (grouping %s) is refused as %s: %s\n"+
				"A direction the request can carry is not a reason to refuse the view.",
				name, strings.Join(declared, ", "), refusal.Code, refusal.Reason)
			continue
		}
		req, ok := loader.View(name)
		if !ok {
			t.Errorf("view %q (grouping %s) is unservable and reports no reason", name, strings.Join(declared, ", "))
			continue
		}

		// AND THE DIRECTION SURVIVED THE CROSSING. Serving the view while
		// silently dropping the direction is the exact failure the refusal
		// prevented; a test asserting only "servable" would call that a pass.
		if req.GroupBy == nil || len(*req.GroupBy) != len(*sv.Def.Grouping) {
			t.Errorf("view %q declares %d grouping key(s) and the request carries %v",
				name, len(*sv.Def.Grouping), req.GroupBy)
			continue
		}
		for i, g := range *sv.Def.Grouping {
			got := (*req.GroupBy)[i]
			if got.Property != g.Property {
				t.Errorf("view %q group key %d = %q, want %q", name, i, got.Property, g.Property)
			}
			switch {
			case g.Direction == nil && got.Direction != nil:
				t.Errorf("view %q key %q declares no direction and the request invented %q",
					name, g.Property, string(*got.Direction))
			case g.Direction != nil && got.Direction == nil:
				t.Errorf("view %q groups %q %s and the request carries no direction — the groups would come back ascending with nothing to say so",
					name, g.Property, string(*g.Direction))
			case g.Direction != nil && string(*got.Direction) != string(*g.Direction):
				t.Errorf("view %q key %q: direction %q crossed as %q",
					name, g.Property, string(*g.Direction), string(*got.Direction))
			}
		}
		t.Logf("SERVABLE %-28q grouping %-38s (view was disabled for other reasons: %v)",
			name, strings.Join(declared, ", "), wasDisabled)
	}
	if directional == 0 {
		t.Fatal("no view in the fixture vault declares a group direction, so this test asserted nothing")
	}
	t.Logf("%d view(s) declare a group direction; all servable with the direction carried", directional)
}

// TestFixtureVault_MostConnectedMatchesTheOracle grades the one view this work
// unblocked against the hand-derived expectation, on the row set FR-105
// governs.
//
// IT KEYS NOTES BY VAULT-RELATIVE PATH, NOT BY FILENAME STEM, and that is not
// tidiness. The shared fr105 harness keys its note map by stem, and this vault
// holds TWO notes called `Singtel` — `05-Maps/Entities/Singtel.md` and
// `Personal/Subscriptions/Singtel.md`. One overwrites the other in a
// stem-keyed map, so grading this view through that harness reported a
// one-row NARROWING that does not exist: an artefact of the grader, presented
// as a finding about the product. Paths are unique; stems are not.
//
// READ THE FALSIFIABILITY VERDICT THIS TEST PRINTS BEFORE CREDITING IT. If
// stripping every clause but the folder leaves the row set unchanged, the
// grade above discriminates nothing and must not be counted as evidence.
func TestFixtureVault_MostConnectedMatchesTheOracle(t *testing.T) {
	oraclePath := os.Getenv(fr105OracleEnv)
	if oraclePath == "" {
		t.Skipf("%s is unset — set it to the hand-derived expected-row-set JSON for the real vault", fr105OracleEnv)
	}
	root := fixtureVaultCopy(t)
	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	data, err := os.ReadFile(oraclePath) //nolint:gosec // operator-supplied acceptance oracle
	if err != nil {
		t.Fatalf("reading the oracle: %v", err)
	}
	var oracle fr105JSONOracle
	if err = json.Unmarshal(data, &oracle); err != nil {
		t.Fatalf("parsing the oracle: %v", err)
	}
	const base, view = "Entities.base", "Most Connected"
	var want []string
	for _, b := range oracle.Bases {
		if b.Base != base {
			continue
		}
		for _, v := range b.Views {
			if v.Name == view {
				want = fr105Sorted(append([]string(nil), v.Rows...))
			}
		}
	}
	if len(want) == 0 {
		t.Fatalf("the oracle does not cover %s / %q — an uncovered view is exactly where a broadening hides", base, view)
	}

	slug := ""
	for _, b := range rep.Bases {
		if filepath.Base(b.BaseRelPath) != base {
			continue
		}
		for _, v := range b.Views {
			if v.DisplayName == view && v.OutputRelPath != "" {
				slug = strings.TrimSuffix(filepath.Base(v.OutputRelPath), ".yaml")
			}
		}
	}
	if slug == "" {
		t.Fatalf("the import wrote no view file for %s / %q", base, view)
	}

	schemas, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("loading schemas: %v", err)
	}
	views, _, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("loading views: %v", err)
	}
	loader := records.NewViewFindLoader(views)
	req, ok := loader.View(slug)
	if !ok {
		refusal, _ := loader.ServeRefusal(slug)
		t.Fatalf("%q is STILL not servable: %s\nThis is the one loss this work exists to close.", view, refusal.String())
	}

	notes := notesByPath(t, root)
	got := untypedRowsFor(t, req, schemas, notes)
	if extra := fr105MissingFrom(want, got); len(extra) > 0 {
		t.Errorf("FR-105 BROADENING in %q: %d row(s) the Obsidian original does not return: %v", view, len(extra), extra)
	}
	if missing := fr105MissingFrom(got, want); len(missing) > 0 {
		t.Errorf("NARROWING in %q: %d row(s) the original returns and this does not: %v", view, len(missing), missing)
	}

	// THE FALSIFIABILITY VERDICT, measured rather than assumed.
	stripped := req
	stripped.Filter = folderClausesOnly(req.Filter)
	switch {
	case stripped.Filter == nil:
		t.Logf("FALSIFIABILITY: %q has no folder clause; not assessed", view)
	case reflect.DeepEqual(stripped.Filter, req.Filter):
		t.Logf("FALSIFIABILITY: UNFALSIFIABLE — %q's filter is a folder clause and NOTHING ELSE, so there is nothing "+
			"to strip and the %d-row match above cannot tell a correct translation from a broadening. "+
			"What this test does settle is that the view is REACHABLE at all, which it was not. The falsifiable "+
			"content of this work is the group ORDER, asserted against a live engine in "+
			"pkg/records/knowledgefind/group_direction_test.go.", view, len(got))
	default:
		broad := untypedRowsFor(t, stripped, schemas, notes)
		if len(broad) == len(got) {
			t.Logf("FALSIFIABILITY: UNFALSIFIABLE — %q returns the same %d rows with every non-folder clause stripped, "+
				"so this grade discriminates nothing", view, len(got))
		} else {
			t.Logf("FALSIFIABILITY: falsifiable — stripped to its folder clauses alone the view returns %d rows against %d",
				len(broad), len(got))
		}
	}
}

// notesByPath reads every note in the imported vault, keyed by VAULT-RELATIVE
// PATH. See this test's own note on why a stem-keyed map is not usable here.
func notesByPath(t *testing.T, root string) map[string]fr105Note {
	t.Helper()
	inv, err := ScanVault(root)
	if err != nil {
		t.Fatalf("re-scanning the imported vault: %v", err)
	}
	out := map[string]fr105Note{}
	for _, abs := range inv.Notes {
		data, readErr := os.ReadFile(abs) //nolint:gosec // path from this run's own scan
		if readErr != nil {
			t.Fatalf("reading %s: %v", abs, readErr)
		}
		rel, relErr := filepath.Rel(inv.Root, abs)
		if relErr != nil {
			t.Fatalf("relativising %s: %v", abs, relErr)
		}
		slash := filepath.ToSlash(rel)
		if _, clash := out[slash]; clash {
			t.Fatalf("two notes share the vault-relative path %q; the key this grading relies on is not unique", slash)
		}
		out[slash] = fr105Note{Rec: records.ParseRecord(abs, data), RelPath: slash}
	}
	return out
}

// untypedRowsFor is fr105RowsFor for a view that declares NO record type.
//
// It exists because "Most Connected" is exactly that: `Entities.base` filters
// on a FOLDER, not on a type, so the imported view is untyped (FR-018b permits
// it) and fr105RowsFor — which starts by looking the request's record type up
// in the schema set — cannot evaluate one. Every leaf still goes through the
// product's own comparator via fr105EvalNode; the only thing that differs is
// which notes are offered to it.
//
// A non-`file.*` leaf in an untyped view is FATAL here rather than evaluated
// against a guessed schema: there is no single schema to decide it against,
// and picking one would make this harness answer a question the product does
// not.
func untypedRowsFor(t *testing.T, req generated.VaultFindRequest, schemas *records.SchemaSet, notes map[string]fr105Note) []string {
	t.Helper()
	if req.Type != nil {
		return fr105RowsFor(t, req, schemas, notes)
	}
	assertOnlyFileLeaves(t, req.Filter)
	var out []string
	for stem, note := range notes {
		if req.Filter == nil || fr105EvalNode(t, *req.Filter, nil, note) {
			out = append(out, stem)
		}
	}
	sort.Strings(out)
	return out
}

func assertOnlyFileLeaves(t *testing.T, n *generated.VaultFilterNode) {
	t.Helper()
	if n == nil {
		return
	}
	switch {
	case n.All != nil:
		for _, c := range *n.All {
			assertOnlyFileLeaves(t, &c)
		}
	case n.Any != nil:
		for _, c := range *n.Any {
			assertOnlyFileLeaves(t, &c)
		}
	case n.Not != nil:
		assertOnlyFileLeaves(t, n.Not)
	case n.Property != nil:
		if !records.IsFileNamespace(*n.Property) {
			t.Fatalf("this untyped view names the declared property %q; there is no single schema to decide it against, "+
				"so grading it here would answer a different question from the product's", *n.Property)
		}
	}
}

// folderClausesOnly rebuilds a filter tree keeping ONLY its `file.folder`
// leaves, in their original combinator shape.
//
// The shape matters, and getting it wrong is how a strip test lies. An earlier
// version of this helper collected the leaves into a flat `all:`; "Most
// Connected" filters on an `any:` of two folder leaves (`= 05-Maps/Entities`
// OR `LIKE 05-Maps/Entities/%`, which is how FR-134 translates
// `file.inFolder`), so ANDing them returned zero rows and the test cheerfully
// reported the strip as "falsifiable" on the strength of a broken query.
//
// A `not:` subtree is dropped rather than descended into: stripping a negation
// to its inner leaf INVERTS the view, which is a different question from
// broadening it.
func folderClausesOnly(n *generated.VaultFilterNode) *generated.VaultFilterNode {
	if n == nil {
		return nil
	}
	switch {
	case n.All != nil:
		kept := keptChildren(*n.All)
		if len(kept) == 0 {
			return nil
		}
		if len(kept) == 1 {
			one := kept[0]
			return &one
		}
		return &generated.VaultFilterNode{All: &kept}
	case n.Any != nil:
		kept := keptChildren(*n.Any)
		if len(kept) == 0 {
			return nil
		}
		if len(kept) == 1 {
			one := kept[0]
			return &one
		}
		return &generated.VaultFilterNode{Any: &kept}
	case n.Not != nil:
		return nil
	case n.Property != nil && strings.HasPrefix(*n.Property, "file.folder"):
		one := *n
		return &one
	}
	return nil
}

func keptChildren(children []generated.VaultFilterNode) []generated.VaultFilterNode {
	var kept []generated.VaultFilterNode
	for i := range children {
		if k := folderClausesOnly(&children[i]); k != nil {
			kept = append(kept, *k)
		}
	}
	return kept
}
