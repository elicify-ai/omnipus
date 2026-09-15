// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// knowledge_edit_relation_identity_test.go — Codex review 2026-09-14 finding
// #8 (Medium): relation add/remove must match a stored edge by the NOTE it
// resolves to, not by the exact bytes it was spelled with.
//
// D-94 made op "relation" store a path-form target in the vault's own
// bare-name form ("People/Acme Ltd.md" → "[[Acme Ltd]]"). That normalisation
// applied to REMOVE as well, while the stored-value comparison stayed
// textual — so a link written before D-94 as "[[People/Acme Ltd]]" could no
// longer be removed by naming it (the request was shortened to "[[Acme Ltd]]"
// first, which matched nothing), and an add of the same note produced a
// second edge to it. Every case below stores its link the PRE-normalisation
// way, by writing the bytes directly.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func relationOp(t *testing.T, tool *EditTool, ws, root, rel, verb string, targets ...string) (string, string) {
	t.Helper()
	list := make([]any, 0, len(targets))
	for _, tg := range targets {
		list = append(list, tg)
	}
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": rel, "property": "company",
		"relation_op": verb, "targets": list, "expect_version": a4Version(t, root, rel),
	})
	require.False(t, res.IsError, res.ForLLM)
	return res.ForLLM, a4Read(t, root, rel)
}

// TestRelation_RemoveMatchesAPathFormLinkStoredBeforeNormalisation is the
// reviewer's scenario end to end: remove by the path the link was stored
// with, and remove by the bare name it now normalises to.
func TestRelation_RemoveMatchesAPathFormLinkStoredBeforeNormalisation(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "People/Acme Ltd.md", "# Acme\n")
	a4Note(t, root, "People/Beta GmbH.md", "# Beta\n")

	// Stored before D-94: path form, no extension.
	const stored = "---\ncompany:\n  - \"[[People/Acme Ltd]]\"\n  - \"[[People/Beta GmbH]]\"\n---\nBody.\n"
	a4Note(t, root, "P.md", stored)

	// (a) remove by the path it was stored with (with its extension, as a
	//     caller copying from a listing would send it).
	reply, got := relationOp(t, tool, ws, root, "P.md", "remove", "People/Acme Ltd.md")
	require.Contains(t, reply, "(changed)", "the stored edge must be found and removed: %s", reply)
	require.NotContains(t, got, "[[People/Acme Ltd]]", "the exact stored representation must be gone: %s", got)
	require.Contains(t, got, "[[People/Beta GmbH]]", "the other edge's bytes must be untouched: %s", got)

	// (b) remove by the BARE name for a link stored in path form.
	reply, got = relationOp(t, tool, ws, root, "P.md", "remove", "Beta GmbH")
	require.Contains(t, reply, "(changed)", reply)
	require.NotContains(t, got, "Beta GmbH", got)
	require.Contains(t, got, "company: []", "the property stays declared and empty: %s", got)
}

// TestRelation_AddDoesNotDuplicateAnEdgeStoredInAnotherSpelling — the other
// half of the finding: an add of a note already linked under a different
// spelling is the idempotent no-op the verb documents, not a second edge.
func TestRelation_AddDoesNotDuplicateAnEdgeStoredInAnotherSpelling(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "People/Acme Ltd.md", "# Acme\n")
	const stored = "---\ncompany:\n  - \"[[People/Acme Ltd]]\"\n---\nBody.\n"
	a4Note(t, root, "P.md", stored)

	for _, spelling := range []string{"Acme Ltd", "People/Acme Ltd.md", "[[Acme Ltd]]", "People/Acme Ltd"} {
		reply, got := relationOp(t, tool, ws, root, "P.md", "add", spelling)
		require.Contains(t, reply, "unchanged", "adding %q must be a no-op over an existing edge to the same note: %s", spelling, reply)
		require.Equal(t, 1, strings.Count(got, "Acme Ltd"), "no second edge for %q: %s", spelling, got)
		require.Equal(t, stored, got, "an idempotent add leaves the file byte-identical")
	}
}

// TestRelation_IdentityMatchNeverConflatesTwoNotesSharingABasename — matching
// by resolved identity must be exact: two notes named Smith in different
// folders are two notes, and removing one must not remove the other.
func TestRelation_IdentityMatchNeverConflatesTwoNotesSharingABasename(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "People/Smith.md", "# P\n")
	a4Note(t, root, "Clients/Smith.md", "# C\n")
	const stored = "---\ncompany:\n  - \"[[People/Smith]]\"\n  - \"[[Clients/Smith]]\"\n---\nBody.\n"
	a4Note(t, root, "P.md", stored)

	reply, got := relationOp(t, tool, ws, root, "P.md", "remove", "People/Smith.md")
	require.Contains(t, reply, "(changed)", reply)
	require.NotContains(t, got, "[[People/Smith]]", got)
	require.Contains(t, got, "[[Clients/Smith]]", "the other Smith must survive: %s", got)

	// Adding the ambiguous bare name is a NEW edge (it resolves to neither
	// stored path by identity: the tie-break picks one, but the request is
	// ambiguous and the vault's own form for it is the bare name).
	_, got = relationOp(t, tool, ws, root, "P.md", "add", "Clients/Smith.md")
	require.Equal(t, 1, strings.Count(got, "Clients/Smith"), "already linked in path form — no duplicate: %s", got)
}

// TestRelation_ScalarRemoveMatchesByIdentity — the single-slot (many: false)
// path has its own textual comparison; it must match by identity too.
func TestRelation_ScalarRemoveMatchesByIdentity(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	uatProjectSchema(t, root) // owner: person, single-valued
	a4Note(t, root, "People/Tobias.md", "# T\n")
	a4Note(t, root, "P.md", "---\ntype: project\nid: PRJ-0001\nowner: \"[[People/Tobias]]\"\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "P.md", "property": "owner",
		"relation_op": "remove", "targets": []any{"Tobias"}, "expect_version": a4Version(t, root, "P.md"),
	})
	require.False(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, "(changed)", res.ForLLM)
	require.NotContains(t, a4Read(t, root, "P.md"), "Tobias")
}
