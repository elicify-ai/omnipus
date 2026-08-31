// Omnipus — the tool DESCRIPTION is the only thing the model reads, so a
// description that disagrees with the code is a live defect, in both
// directions:
//
//	the description OMITS a capability   → the capability does not exist in
//	                                       practice, however well implemented
//	the description NAMES something the  → every call that follows the
//	code rejects                           description fails
//
// Neither half is hypothetical here. `checkbox` — the eighth property type,
// implemented, validated and accepted by knowledge_configure's own write path
// — was unreachable through the ONLY agent-facing schema-authoring tool
// because the `definition` description asserted "the seven property types are
// … there is no eighth". A model reads a positive negative claim like that and
// acts on it; nothing in the product could create a checkbox property. The
// same shape has been found twice more on this branch in a week (an importer
// advertising 4 of 15 aggregate ops; a description asking for three keys the
// code had just retired).
//
// So these tests do not check prose against a transcription of the rules. They
// check it against THE SAME VARIABLES THE VALIDATION READS, so that growing a
// closed set fails the build rather than quietly stranding the new member.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// paramDescription returns one declared parameter's description text.
func paramDescription(t *testing.T, tool tools.Tool, param string) string {
	t.Helper()
	props, ok := tool.Parameters()["properties"].(map[string]any)
	require.True(t, ok, "%s: Parameters() declares no properties object", tool.Name())
	p, ok := props[param].(map[string]any)
	require.True(t, ok, "%s: no %q parameter is declared", tool.Name(), param)
	s, _ := p["description"].(string)
	require.NotEmpty(t, s, "%s: %q has no description", tool.Name(), param)
	return s
}

// paramNames lists every parameter a tool declares, sorted.
func paramNames(t *testing.T, tool tools.Tool) []string {
	t.Helper()
	props, ok := tool.Parameters()["properties"].(map[string]any)
	require.True(t, ok, "%s: Parameters() declares no properties object", tool.Name())
	out := make([]string, 0, len(props))
	for k := range props {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// knowledge_configure — the property types (the checkbox defect)
// ---------------------------------------------------------------------------

// TestConfigureDescription_PropertyTypesAreDerivedNotTranscribed is the
// regression for the checkbox defect, written against records.PropertyTypes
// rather than against a list of eight names: adding a ninth type without
// naming it in the description fails HERE, at the moment the set grows,
// instead of shipping a type no agent can declare.
func TestConfigureDescription_PropertyTypesAreDerivedNotTranscribed(t *testing.T) {
	desc := paramDescription(t, NewConfigureTool(AuthoringDeps{}), "definition")

	for _, pt := range records.PropertyTypes {
		require.Containsf(t, desc, string(pt),
			"property type %q is accepted by records.ParseSchema but is not named in "+
				"knowledge_configure's `definition` description, which is the only place a "+
				"model can learn it exists", pt)
	}

	// The COUNT, not only the names. "seven" was the wrong half of the old
	// sentence, and a stale count is an instruction to stop reading the list.
	require.Containsf(t, desc, fmt.Sprintf("The %d property types are", len(records.PropertyTypes)),
		"the description must state the count records.PropertyTypes actually holds (%d)",
		len(records.PropertyTypes))

	// The specific false claim that made `checkbox` unreachable. Asserted as
	// its own case so a future edit that re-adds a closed-world sentence over
	// a stale list is caught by name.
	for _, banned := range []string{"there is no eighth", "The seven property types"} {
		require.NotContainsf(t, desc, banned,
			"the description still carries %q, a hand-counted closed-world claim over a set "+
				"that has already grown once", banned)
	}
}

// TestConfigureDescription_OperatorsAreDerivedNotTranscribed — the description
// used to say "in the ten SQL operators", which is a number a model cannot act
// on: it still has to guess the spelling, and `contains` / `is_null` are both
// refused. Every accepted spelling is now named, from records.OperatorNames().
func TestConfigureDescription_OperatorsAreDerivedNotTranscribed(t *testing.T) {
	desc := paramDescription(t, NewConfigureTool(AuthoringDeps{}), "definition")
	for _, op := range records.OperatorNames() {
		require.Containsf(t, desc, op,
			"operator %q is accepted in a filter leaf but never named in the description", op)
	}
}

// TestKnowledgeConfigure_CheckboxPropertyIsReachableEndToEnd proves the two
// halves of "reachable" together: the description names the type, AND the
// write path accepts a schema declaring it. Before the fix the second half
// passed on its own — which is precisely why a description-only defect can
// survive a green test suite.
func TestKnowledgeConfigure_CheckboxPropertyIsReachableEndToEnd(t *testing.T) {
	desc := paramDescription(t, NewConfigureTool(AuthoringDeps{}), "definition")
	require.Contains(t, desc, string(records.TypeCheckbox),
		"an agent cannot declare a checkbox property it is never told exists")

	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	res := NewConfigureTool(deps).Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "shipment",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties": map[string]any{
				"delivered": map[string]any{"type": string(records.TypeCheckbox)},
			},
		},
	})
	require.Falsef(t, res.IsError, "the write path refused a checkbox property: %s", res.ForLLM)
}

// ---------------------------------------------------------------------------
// knowledge_configure — the view keys
// ---------------------------------------------------------------------------

// viewKeysNotForAuthoring are the ViewDef wire keys knowledge_configure's
// `definition` description deliberately does NOT invite an agent to send,
// each with the reason. Everything else in the contract must be named.
//
// This map is the opt-out list the test below reads. Adding a key here is a
// decision that an agent should not author it; forgetting to name a key
// anywhere is what the test catches.
var viewKeysNotForAuthoring = map[string]string{
	"name":         "taken from the `view` argument, and the description says so",
	"source":       "importer provenance (FR-102) — the file an imported view came from",
	"untranslated": "importer residue (FR-101) — expressions the importer could not translate",
	"disabled":     "FR-105's import kill switch; an agent that does not want a view deletes it",
}

// TestConfigureDescription_AccountsForEveryViewKey walks generated.ViewDef's
// own JSON tags — the closed key set records.ParseView enforces with
// DisallowUnknownFields — and requires each one to be either NAMED in the
// description or listed above with a reason.
//
// The importer half of this exact drift shipped: `formulas` was advertised as
// supported, written successfully, and then reported by knowledge_find as a
// view that does not exist. A key added to the contract and never mentioned
// here is the same defect waiting to happen in the other direction.
func TestConfigureDescription_AccountsForEveryViewKey(t *testing.T) {
	desc := paramDescription(t, NewConfigureTool(AuthoringDeps{}), "definition")
	var missing []string
	for _, key := range viewDefJSONKeys(t) {
		if _, exempt := viewKeysNotForAuthoring[key]; exempt {
			continue
		}
		if !strings.Contains(desc, key) {
			missing = append(missing, key)
		}
	}
	require.Emptyf(t, missing,
		"generated.ViewDef accepts %s, and knowledge_configure's `definition` description "+
			"neither names them nor lists them in viewKeysNotForAuthoring — a key a model is "+
			"never told about is a key no model will ever send", strings.Join(missing, ", "))
}

// viewDefJSONKeys reads the wire keys off generated.ViewDef itself — through
// the SAME reflection knowledge_describe's own coverage ledger uses — rather
// than off a list somebody maintains beside it.
func viewDefJSONKeys(t *testing.T) []string {
	t.Helper()
	keys := viewDefWireKeys()
	require.NotEmpty(t, keys, "reflection found no JSON keys on generated.ViewDef; "+
		"every check built on it would be vacuously green")
	return keys
}

// ---------------------------------------------------------------------------
// knowledge_configure — a stored view that knowledge_find will not serve
// ---------------------------------------------------------------------------

// TestKnowledgeConfigure_WriteView_FormulaBearingViewIsReportedUnservable —
// finding 13, the write half.
//
// A view carrying `formulas` parses, validates and is written. knowledge_find
// then refuses it and says "no saved view named X; defined: <everything
// else>", which is false about a file that is on disk and that
// knowledge_describe lists in full. The refusal is correct — a
// VaultFindRequest has no formulas map, so every `formula.<name>` would
// resolve against nothing — but it arrived at the wrong end of the workflow.
// The write now states it, in the loader's own words.
func TestKnowledgeConfigure_WriteView_FormulaBearingViewIsServable(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := NewConfigureTool(deps)
	require.False(t, tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "deal",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties":     map[string]any{"amount": map[string]any{"type": "integer"}},
		},
	}).IsError)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "doubled",
		"definition": map[string]any{
			"type":     "deal",
			"formulas": map[string]any{"twice": "amount * 2"},
		},
	})
	require.Falsef(t, res.IsError, "a formula-bearing view is legal to STORE: %s", res.ForLLM)
	// The seam CLOSED: knowledge_find now serves a view carrying `formulas`,
	// evaluating them per query. Marking it unservable would send an agent
	// away from a view that works — the opposite of this section's purpose.
	// The descending-grouping sibling still asserts the mark appears where it
	// SHOULD, so the machinery is not going untested by this inversion.
	require.NotContains(t, res.ForLLM, "NOT SERVABLE by knowledge_find",
		"a formula-bearing view is servable; the write must not warn otherwise")
	// The remedy line ("query the view's underlying type directly") is
	// deliberately NOT asserted any more: it is emitted only with a refusal,
	// and there is no longer a refusal to remedy. Its sibling test still pins
	// that a real refusal carries one, so the remedy contract stays guarded.
}

// TestKnowledgeConfigure_WriteView_DescendingGroupingIsReportedUnservable —
// the second half of the same seam. VaultFindRequest.group_by is a bare
// []string, so serving this view would flatten the direction to ascending in
// silence.
func TestKnowledgeConfigure_WriteView_DescendingGroupingIsReportedUnservable(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := NewConfigureTool(deps)
	require.False(t, tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "deal",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties":     map[string]any{"stage": map[string]any{"type": "text"}},
		},
	}).IsError)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "by-stage-desc",
		"definition": map[string]any{
			"type":     "deal",
			"grouping": []any{map[string]any{"property": "stage", "direction": "desc"}},
		},
	})
	require.Falsef(t, res.IsError, "%s", res.ForLLM)
	require.Contains(t, res.ForLLM, "NOT SERVABLE by knowledge_find")
	require.Contains(t, res.ForLLM, string(records.ServeRefusalGroupDirection))
}

// TestKnowledgeConfigure_WriteView_ServableViewIsNotLabelledUnservable is the
// negative control. Without it the two tests above would pass against a
// response that printed the warning unconditionally, which would be a new way
// of saying nothing.
func TestKnowledgeConfigure_WriteView_ServableViewIsNotLabelledUnservable(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := NewConfigureTool(deps)
	require.False(t, tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "deal",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties":     map[string]any{"stage": map[string]any{"type": "text"}},
		},
	}).IsError)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "by-stage",
		"definition": map[string]any{
			"type":     "deal",
			"grouping": []any{map[string]any{"property": "stage", "direction": "asc"}},
		},
	})
	require.Falsef(t, res.IsError, "%s", res.ForLLM)
	require.NotContains(t, res.ForLLM, "NOT SERVABLE",
		"a view knowledge_find CAN serve must not be labelled unservable")
}

// TestKnowledgeConfigure_WriteView_UntypedViewIsNamedAsUntyped — FR-018b makes
// `type` optional and ParseView refuses only a PRESENT-but-blank one, so an
// untyped view reaches the renderer routinely. It used to render as
// `querying record type ""`, which reads as a bug rather than as the legal,
// vault-spanning view it is.
func TestKnowledgeConfigure_WriteView_UntypedViewIsNamedAsUntyped(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)

	res := NewConfigureTool(deps).Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "everything",
		"definition": map[string]any{"label": "Everything"},
	})
	require.Falsef(t, res.IsError, "an untyped view is legal (FR-018b): %s", res.ForLLM)
	require.NotContains(t, res.ForLLM, `record type ""`,
		"an untyped view must not be reported as querying an empty type name")
	require.Contains(t, res.ForLLM, "untyped")
}

// ---------------------------------------------------------------------------
// knowledge_describe — the same fact, on the surface that says "look here first"
// ---------------------------------------------------------------------------

// TestDescribeViews_UnservableViewIsMarkedInTheListing — knowledge_describe's
// own head line says "ask for one by name before inventing a filter". For a
// formula-bearing view that instruction fails at the next call. The listing
// now says so, in the same words the write and the query path use.
func TestDescribeViews_UnservableViewIsMarkedInTheListing(t *testing.T) {
	root := t.TempDir()
	writeUnderMarker(t, root, "records", "widget.yaml", describeViewWidgetSchema)
	schemas, _, err := records.LoadSchemas(root)
	require.NoError(t, err)
	// A DESCENDING GROUPING, not a formula: formula-bearing views became
	// SERVABLE when that seam closed, so using one here would assert the
	// opposite of the truth. VaultFindRequest.group_by is a bare []string, so
	// serving this one would flatten the direction to ascending in silence —
	// which is what the mark exists to warn an agent about.
	writeUnderMarker(t, root, "views", "twice.yaml",
		"name: twice\ntype: widget\ngrouping:\n  - property: batch\n    direction: desc\n")
	writeUnderMarker(t, root, "views", "plain.yaml", "name: plain\ntype: widget\n")
	views, report, err := records.LoadViews(root, schemas)
	require.NoError(t, err)
	require.Truef(t, report.OK(), "the fixture views were rejected: %v", report.Rejections)

	var b strings.Builder
	renderViews(&b, DescribeData{Views: views})
	out := b.String()

	require.Contains(t, out, "NOT SERVABLE by knowledge_find",
		"a view knowledge_find refuses must be marked in the listing an agent reads first")
	require.Contains(t, out, string(records.ServeRefusalGroupDirection))

	// The servable view must NOT carry the mark — otherwise the mark means
	// nothing and both assertions above would pass against a renderer that
	// stamped every view.
	require.Equal(t, 1, strings.Count(out, "NOT SERVABLE"),
		"exactly one of the two views is unservable; got:\n%s", out)
	require.NotContains(t, viewSegment(t, out, "plain"), "NOT SERVABLE",
		"a servable view was labelled unservable")
	require.Contains(t, viewSegment(t, out, "twice"), "NOT SERVABLE",
		"the mark must sit under the view it describes, not somewhere else in the listing")
}

// viewSegment returns the lines renderViews emitted for ONE view: its head
// line and everything indented under it, up to the next view's head line.
//
// Asserting on the whole listing would let a mark land under the wrong view
// and still pass, which is the same class of confident-wrong-answer these
// tests exist for.
func viewSegment(t *testing.T, listing, viewName string) string {
	t.Helper()
	lines := strings.Split(listing, "\n")
	head := "  " + viewName + "  "
	start := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, head) {
			start = i
			break
		}
	}
	require.GreaterOrEqualf(t, start, 0, "no head line for view %q in:\n%s", viewName, listing)
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		// A head line is indented by exactly two spaces; every continuation
		// renderViews emits is indented by four.
		if strings.HasPrefix(lines[i], "  ") && !strings.HasPrefix(lines[i], "   ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// ---------------------------------------------------------------------------
// Every tool: the declared schema and the accepted argument set are ONE set
// ---------------------------------------------------------------------------

// TestToolParameterSchemaMatchesAcceptedArguments closes the drift in both
// directions at once, for every knowledge tool registered with an agent
// (pkg/agent/knowledge_tools.go):
//
//	a name in Parameters() but not accepted  → every call following the schema
//	                                           is refused as "unknown argument"
//	a name accepted but not in Parameters()  → the capability is unreachable;
//	                                           no model will ever send it
//
// The accepted set is the SAME slice Execute passes to unknownArgs, so this
// cannot be satisfied by updating a copy.
func TestToolParameterSchemaMatchesAcceptedArguments(t *testing.T) {
	cases := []struct {
		tool     tools.Tool
		accepted []string
		// alsoRefused are names Execute refuses with a DEDICATED message
		// ahead of the generic unknown-argument sweep, and which must
		// therefore stay OUT of the declared schema (AC-C3 / AC-X3).
		alsoRefused []string
	}{
		{tool: NewDescribeTool(ToolDeps{}, nil), accepted: describeArgNames},
		{tool: NewReadTool(ToolDeps{}), accepted: readArgNames},
		{tool: NewEditTool(AuthoringDeps{}), accepted: editArgNames},
		{
			tool: NewRestructureTool(AuthoringDeps{}), accepted: restructureArgNames,
			alsoRefused: []string{"expect_version"},
		},
		{
			tool: NewConfigureTool(AuthoringDeps{}), accepted: configureArgNames,
			alsoRefused: []string{"expect_version"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.tool.Name(), func(t *testing.T) {
			declared := paramNames(t, tc.tool)
			want := append([]string(nil), tc.accepted...)
			sort.Strings(want)
			require.Equal(t, want, declared,
				"%s: the parameters the model is shown and the arguments Execute accepts "+
					"must be one set", tc.tool.Name())
			for _, banned := range tc.alsoRefused {
				require.NotContains(t, declared, banned,
					"%s: %q is refused with a dedicated message, so it must not be advertised",
					tc.tool.Name(), banned)
			}
		})
	}
}

// TestRestructure_TrashedAtNamesAReachableSource — knowledge_describe renders
// four sections (index, types, views, templates) and NONE of them reads the
// trash directory, so "one of the timestamps knowledge_describe's trash
// listing reports" sent an agent to a surface that would never answer. The
// same false pointer stood in the restore refusal, where it was the entire
// remedy.
func TestRestructure_TrashedAtNamesAReachableSource(t *testing.T) {
	desc := paramDescription(t, NewRestructureTool(AuthoringDeps{}), "trashed_at")
	require.NotContains(t, desc, "knowledge_describe",
		"knowledge_describe has no trash listing; its sections are %s",
		strings.Join(describeSectionOrder, ", "))

	// Whatever it points at instead must be something that exists. The trash
	// response's own line and the refusal's available-timestamp list are the
	// two real sources.
	require.Contains(t, desc, "trashed at")
}

// ---------------------------------------------------------------------------
// knowledge_describe — the sweep it advertises, and the count it promises
// ---------------------------------------------------------------------------

// integrityCategoryPhrase maps every check_integrity category to the words
// knowledge_describe's own Description() uses for it.
//
// The description cannot simply quote the category constants — "orphan row"
// means nothing to a reader who does not know the properties index exists —
// so the mapping is explicit. What matters is that it is CHECKED: adding a
// seventh category without telling an agent the sweep covers it means the
// sweep may as well not cover it, because nothing else says so.
var integrityCategoryPhrase = map[IntegrityCategory]string{
	CategoryDuplicateID:        "duplicate identifiers",
	CategoryUnresolvedRelation: "relations resolving to nothing",
	CategoryWrongType:          "the wrong type",
	CategoryBrokenLink:         "broken wikilinks",
	CategoryOrphan:             "orphan notes",
	CategoryOrphanRow:          "index rows with no note",
}

// TestDescribeDescription_NamesEveryIntegrityCategory — FR-079 requires the
// description to name the WIDEST operation the tool grants, and check_integrity
// IS that operation. A category it sweeps but never mentions is a capability
// no agent will ever ask for.
func TestDescribeDescription_NamesEveryIntegrityCategory(t *testing.T) {
	desc := NewDescribeTool(ToolDeps{}, nil).Description()

	require.Len(t, integrityCategoryPhrase, len(IntegrityCategories),
		"IntegrityCategories has %d members and this mapping has %d; a category with no "+
			"phrase is a check nobody is told about", len(IntegrityCategories), len(integrityCategoryPhrase))

	for _, cat := range IntegrityCategories {
		phrase, ok := integrityCategoryPhrase[cat]
		require.Truef(t, ok, "check_integrity sweeps for %q and no phrase is mapped for it", cat)
		require.Containsf(t, desc, phrase,
			"check_integrity sweeps for %q, and knowledge_describe's description — the only "+
				"place a model learns what the sweep covers — never says %q", cat, phrase)
	}
}

// TestIncludeParametersStateTheRealSectionCount — both `include` parameters
// promise "Default: all four". The enum beside each is already derived from
// the real list, so the ENUM cannot drift; the sentence can, and a wrong count
// is an instruction to stop reading the enum.
func TestIncludeParametersStateTheRealSectionCount(t *testing.T) {
	for _, tc := range []struct {
		tool    tools.Tool
		members []string
	}{
		{NewDescribeTool(ToolDeps{}, nil), describeSectionOrder},
		{NewReadTool(ToolDeps{}), ReadIncludeOrder},
	} {
		t.Run(tc.tool.Name(), func(t *testing.T) {
			desc := paramDescription(t, tc.tool, "include")
			require.Equalf(t, 4, len(tc.members),
				"the description says \"all four\"; the real list now has %d members, so "+
					"either the list or the sentence must change", len(tc.members))
			require.Contains(t, desc, "all four")
		})
	}
}
