// Omnipus — tests for vault_edit's op: link relation semantics (code review
// B finding 5): vaultEditLinkPropertyEdit's arity decision (ADD vs.
// overwrite) must be driven by whether the record's OWN schema actually
// declares the property, not by collapsing "explicitly declared
// single-valued" and "nothing declared at all" into the same answer. See
// vaultEditLinkPropertyEdit's doc comment in vault_edit_schema.go for the
// full spec argument (D5: "Cardinality is declared and enforced").
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// veRelationSchema writes a "widget" record schema declaring TWO relation
// properties: "related" as many-valued, "owner" as single-valued (many
// absent, which FR-006 says means scalar) — so a test can pick which
// declared cardinality it wants against the same collection.
func veRelationSchema(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, records.VaultMarkerDirName, records.RecordsDirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	yaml := "schema_version: 1\n" +
		"type: widget\n" +
		"properties:\n" +
		"  name:    { type: text }\n" +
		"  related: { type: relation, to: widget, many: true }\n" +
		"  owner:   { type: relation, to: widget }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "widget.yaml"), []byte(yaml), 0o600))
}

// TestVaultEditLink_DeclaredMany_AppendsExistingList is the case the code
// already handled correctly before this fix (many: true), kept as a
// not-a-regression anchor: the list must survive and grow by one, in
// either YAML style.
func TestVaultEditLink_DeclaredMany_AppendsExistingList(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"flow", "---\ntype: widget\nrelated: [\"[[A]]\", \"[[C]]\"]\n---\nBody.\n"},
		{"block", "---\ntype: widget\nrelated:\n  - \"[[A]]\"\n  - \"[[C]]\"\n---\nBody.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, ws, root := a4Fixture(t, "kb")
			veRelationSchema(t, root)
			deps, _ := a4Deps(home)
			tool := veTool(deps)
			a4Note(t, root, "Real.md", tc.body)
			v := a4Version(t, root, "Real.md")

			res := tool.Execute(a4Ctx("mia", ws), map[string]any{
				"collection": "kb", "op": "link", "path": "Real.md",
				"target": "B", "relation": "related", "expect_version": v,
			})
			require.False(t, res.IsError, "declared many:true must not be refused: %s", res.ForLLM)
			got := a4Read(t, root, "Real.md")
			for _, want := range []string{"[[A]]", "[[C]]", "[[B]]"} {
				if !strings.Contains(got, want) {
					t.Fatalf("expected %s to survive/appear, got: %s", want, got)
				}
			}
			if !strings.Contains(res.ForLLM, "LINK related -> B (changed)") {
				t.Fatalf("expected an additive-reading reply, got: %s", res.ForLLM)
			}
		})
	}
}

// TestVaultEditLink_DeclaredSingle_OverwritesDeclaredSlot is the OTHER half
// of D5's "declared AND enforced": a property the schema explicitly
// declares as single-valued (no `many:`) is a cardinality-of-one slot on
// purpose, and linking a new target into it is meant to replace the one
// edge — that is what "enforced" means for a scalar relation, not a bug.
func TestVaultEditLink_DeclaredSingle_OverwritesDeclaredSlot(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veRelationSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Real.md", "---\ntype: widget\nowner: \"[[A]]\"\n---\nBody.\n")
	v := a4Version(t, root, "Real.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "link", "path": "Real.md",
		"target": "B", "relation": "owner", "expect_version": v,
	})
	require.False(t, res.IsError, "declared single-valued relation must overwrite, not refuse: %s", res.ForLLM)
	want := "---\ntype: widget\nowner: \"[[B]]\"\n---\nBody.\n"
	if got := a4Read(t, root, "Real.md"); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestVaultEditLink_DeclaredSingle_ButFileHoldsAList_Refuses is the
// interaction between this fix and finding 4's: a note whose data has
// drifted from its own schema (declared single-valued, but the file on
// disk actually holds a list) must be REFUSED, not silently clobbered —
// SetPropertyScalarChecked's list-shape guard is still the layer deciding
// this, unchanged by the arity routing fix.
func TestVaultEditLink_DeclaredSingle_ButFileHoldsAList_Refuses(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veRelationSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	body := "---\ntype: widget\nowner: [\"[[A]]\", \"[[C]]\"]\n---\nBody.\n"
	a4Note(t, root, "Real.md", body)
	v := a4Version(t, root, "Real.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "link", "path": "Real.md",
		"target": "B", "relation": "owner", "expect_version": v,
	})
	if !res.IsError {
		t.Fatalf("expected a refusal (declared single-valued but file holds a list), got success: %s", res.ForLLM)
	}
	if got := a4Read(t, root, "Real.md"); got != body {
		t.Fatalf("a refused write must leave the file byte-identical, got: %s", got)
	}
}

// TestVaultEditLink_UndeclaredProperty_AppendsRatherThanOverwrites is code
// review B finding 5's own reproduction: a property that is NOT declared by
// any resolvable schema — the case of an ordinary note (FR-005: "ordinary
// notes are unconstrained") — used to be treated as arity-false
// (vaultEditPropertyMany's old collapse) and overwritten, destroying every
// existing relation. It must now append, in both YAML styles.
func TestVaultEditLink_UndeclaredProperty_AppendsRatherThanOverwrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"flow_no_schema_at_all", "---\nrelated: [\"[[A]]\", \"[[C]]\"]\n---\nBody.\n"},
		{"block_no_schema_at_all", "---\nrelated:\n  - \"[[A]]\"\n  - \"[[C]]\"\n---\nBody.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, ws, root := a4Fixture(t, "kb")
			// Deliberately NOT calling veRelationSchema: this is an
			// ordinary, schema-free note — the exact case the finding
			// names ("EVERY property on an ordinary note").
			deps, _ := a4Deps(home)
			tool := veTool(deps)
			a4Note(t, root, "Real.md", tc.body)
			v := a4Version(t, root, "Real.md")

			res := tool.Execute(a4Ctx("mia", ws), map[string]any{
				"collection": "kb", "op": "link", "path": "Real.md",
				"target": "B", "relation": "related", "expect_version": v,
			})
			require.False(t, res.IsError, "an undeclared relation property must append, not refuse: %s", res.ForLLM)
			got := a4Read(t, root, "Real.md")
			for _, want := range []string{"[[A]]", "[[C]]", "[[B]]"} {
				if !strings.Contains(got, want) {
					t.Fatalf("finding 5: existing relation destroyed — expected %s to survive, got: %s", want, got)
				}
			}
		})
	}
}

// TestVaultEditLink_UndeclaredProperty_AbsentKey_CreatesOneItemList proves
// the failure-safe default chosen for the undeclared case (route through
// AddListValue) behaves sanely on a fresh key too: no prior value, no
// destruction question, just a new one-item list.
func TestVaultEditLink_UndeclaredProperty_AbsentKey_CreatesOneItemList(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Real.md", "---\nstatus: draft\n---\nBody.\n")
	v := a4Version(t, root, "Real.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "link", "path": "Real.md",
		"target": "B", "relation": "related", "expect_version": v,
	})
	require.False(t, res.IsError, "linking a fresh relation property must succeed: %s", res.ForLLM)
	want := "---\nstatus: draft\nrelated:\n  - \"[[B]]\"\n---\nBody.\n"
	if got := a4Read(t, root, "Real.md"); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestVaultEditLink_UndeclaredProperty_ExistingScalar_Refuses proves the
// undeclared-property default (append) does not silently promote an
// existing single value into a list either — AddListValue's own defined
// refusal for a scalar-shaped span still applies, so a genuinely
// single-valued undeclared property is protected from an accidental shape
// change, not just from destruction.
func TestVaultEditLink_UndeclaredProperty_ExistingScalar_Refuses(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	body := "---\nrelated: \"[[A]]\"\n---\nBody.\n"
	a4Note(t, root, "Real.md", body)
	v := a4Version(t, root, "Real.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "link", "path": "Real.md",
		"target": "B", "relation": "related", "expect_version": v,
	})
	if !res.IsError {
		t.Fatalf("expected a refusal (existing scalar, undeclared property), got success: %s", res.ForLLM)
	}
	if got := a4Read(t, root, "Real.md"); got != body {
		t.Fatalf("a refused write must leave the file byte-identical, got: %s", got)
	}
}
