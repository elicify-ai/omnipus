// Omnipus — the seven absorbed disjunctions, graded on the real vault against
// the hand-derived oracle.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHAT THIS GRADES AND WHAT IT REFUSES TO COUNT
//
// Seven `or:` groups across two bases stopped being losses. Each one enabled a
// view that was previously stored DISABLED, so each one is a chance to return
// rows the Obsidian original does not — the FR-105 failure.
//
// Five of the seven can actually SHOW that failure. Two cannot, and are named
// UNFALSIFIABLE rather than counted: `brand-kit` and `round` are types no note
// in this vault carries, so those two views return zero rows whatever their
// filter says, and an EXACT verdict on them establishes nothing. Three agents
// on this project have reported EXACT on a view over an empty record type; the
// only defence is to work out which of your own views are vacuous BEFORE
// measuring, so the count that is reported is the count that means something.
//
// The strongest single case is "Investor Contacts": 52 `company` notes are in
// scope and the oracle expects 0 rows, so a translation that lost the
// `segment.contains("investor")` clause would show 52 rows instead of none.
// That is a grader with real discriminating power, not one agreeing with a zero.
// ---------------------------------------------------------------------------

// tdAbsorbed is the seven groups, as (base file, view name) with the record
// type each view resolves to. Written from the two `.base` files, not from the
// importer's output.
var tdAbsorbed = []struct {
	base, view, recordType string
}{
	{"Content.base", "Calendar", "content"},
	{"Content.base", "By Platform", "content"},
	{"Content.base", "Backlog", "content"},
	{"Content.base", "Published", "content"},
	{"Content.base", "Brand Kits by Venture", "brand-kit"},
	{"Fundraising.base", "Round Pipeline", "round"},
	{"Fundraising.base", "Investor Contacts", "company"},
}

func TestTypedDisjunction_TheSevenAreEnabledAndNoneBroadens(t *testing.T) {
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
	want := map[string][]string{}
	for _, b := range oracle.Bases {
		for _, v := range b.Views {
			want[b.Base+"|"+v.Name] = fr105Sorted(v.Rows)
		}
	}

	l := w3Load(t, root)

	// Index the produced views by base basename + display name, so a view is
	// located the way the oracle names it rather than by a slug the importer
	// chose.
	type produced struct {
		slug    string
		outPath string
	}
	byKey := map[string]produced{}
	for _, b := range rep.Bases {
		for _, v := range b.Views {
			if v.OutputRelPath == "" {
				continue
			}
			key := filepath.Base(b.BaseRelPath) + "|" + v.DisplayName
			byKey[key] = produced{
				slug:    strings.TrimSuffix(filepath.Base(v.OutputRelPath), ".yaml"),
				outPath: v.OutputRelPath,
			}
		}
	}

	graded, unfalsifiable := 0, 0
	for _, tc := range tdAbsorbed {
		key := tc.base + "|" + tc.view
		t.Run(tc.base+"/"+tc.view, func(t *testing.T) {
			p, ok := byKey[key]
			if !ok {
				t.Fatalf("the import produced no view for %q", key)
			}
			sv, ok := l.views.Get(p.slug)
			if !ok {
				t.Fatalf("the import reports writing %q but no such view loaded", p.slug)
			}

			// CLAIM 1 — the view is no longer disabled. This is the whole
			// deliverable; everything below is the check that enabling it was
			// legitimate.
			if sv.Def.Disabled != nil && *sv.Def.Disabled {
				t.Fatalf("still DISABLED, so the disjunction was not absorbed: %v", w3Strings(sv.Def.Untranslated))
			}

			// CLAIM 2 — the mixed-type disjunction is gone from the loss list.
			// A view can be enabled while still NAMING a loss, and a loss that
			// is silently kept would leave the report saying the group is
			// still unrepresentable.
			for _, u := range w3Strings(sv.Def.Untranslated) {
				if strings.Contains(u, `type == "`) && len(distinctTypeLiterals(u)) > 1 {
					t.Errorf("the mixed-type group is still reported as a loss: %q", u)
				}
			}

			// CLAIM 3 — the type survived. Absorption is only exact because
			// the view carries `type: T`; an untyped view here would be the
			// vault-wide broadening the old refusal correctly refused.
			if sv.Def.Type == nil {
				t.Fatalf("the view lost its record type, so the absorption premise does not hold")
			}
			if *sv.Def.Type != tc.recordType {
				t.Fatalf("record type is %q, the base file says %q", *sv.Def.Type, tc.recordType)
			}

			expected, known := want[key]
			if !known {
				t.Fatalf("the oracle does not cover %q — an ungraded newly-enabled view is exactly where a broadening hides", key)
			}

			candidates := 0
			for _, n := range l.notes {
				if n.Rec.TypeName() == *sv.Def.Type {
					candidates++
				}
			}
			got := w3Rows(t, l, sv.Def, w3Clock())

			// FR-105, THE ONE THAT MUST HOLD: no row the original lacks.
			// Checked for every view including the unfalsifiable ones — it
			// costs nothing and a surprise there would be worth seeing.
			if extra := fr105MissingFrom(expected, got); len(extra) > 0 {
				t.Errorf("FR-105 BROADENING: returns %d row(s) the Obsidian original does not: %v", len(extra), extra)
			}
			if missing := fr105MissingFrom(got, expected); len(missing) > 0 {
				t.Logf("NARROWING (allowed by FR-105, recorded anyway): the original returns %d row(s) the import does not: %v",
					len(missing), missing)
			}

			// INSTRUMENT POWER. Two independent ways this grade can be empty
			// of meaning, and both are reported rather than counted.
			if candidates == 0 {
				unfalsifiable++
				t.Logf("UNFALSIFIABLE: the vault holds NO notes of record type %q, so this view returns 0 rows whatever its filter says. oracle=%d imported=%d — NOT COUNTED",
					*sv.Def.Type, len(expected), len(got))
				return
			}

			// The maximally broadened translation: the same view with every
			// filter clause removed. If that is not visible against the oracle,
			// the comparison above could not have caught a broadening either.
			broad := sv.Def
			broad.Filter = nil
			widened := w3Rows(t, l, broad, w3Clock())
			if len(fr105MissingFrom(expected, widened)) == 0 {
				unfalsifiable++
				t.Logf("UNFALSIFIABLE: stripping EVERY filter clause returns %d row(s) and none is a row the oracle lacks, so the grade above has no power. oracle=%d imported=%d — NOT COUNTED",
					len(widened), len(expected), len(got))
				return
			}

			graded++
			t.Logf("GRADED: oracle=%d imported=%d over %d candidate %q note(s); stripping every filter clause would have shown %d row(s) the oracle lacks",
				len(expected), len(got), candidates, *sv.Def.Type,
				len(fr105MissingFrom(expected, widened)))
		})
	}

	t.Logf("SEVEN ABSORBED GROUPS: %d graded with a falsifiable instrument, %d unfalsifiable and not counted", graded, unfalsifiable)
	if graded == 0 {
		t.Fatal("nothing was gradeable, so this test proves nothing about the seven")
	}
}

// TestTypedDisjunction_TheMixedTypeGapIsGoneFromTheReport is the report-level
// half: the gap category must stop firing entirely, and it must stop because
// the groups were absorbed rather than because the classifier stopped
// recognising them.
func TestTypedDisjunction_TheMixedTypeGapIsGoneFromTheReport(t *testing.T) {
	if os.Getenv(kbFixtureEnv) == "" {
		t.Skipf("%s is unset", kbFixtureEnv)
	}
	root := fixtureVaultCopy(t)
	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	for _, b := range rep.Bases {
		for _, v := range b.Views {
			for _, loss := range v.Losses {
				if isCombinatorExpr(loss) && len(distinctTypeLiterals(loss)) > 1 {
					t.Errorf("%s / %q still loses a mixed-type group: %q", b.BaseRelPath, v.DisplayName, loss)
				}
			}
		}
	}
}
