// Omnipus — tests for knowledge_edit's op "link" after its `relation` mode was
// removed.
//
// op "link" once had TWO modes, selected by whether a `relation` argument was
// present: without it, a body wikilink; with it, a splice into that
// frontmatter relation property. The second mode is gone — op "relation"
// (knowledge_edit_relation.go) is the single way in for a relation property,
// because it carries the whole verb set (add / remove / replace) where link
// only ever added.
//
// This file therefore pins TWO things that must not drift apart:
//
//  1. THE REMOVAL IS REAL, NOT COSMETIC. The refusal tests below assert on the
//     BYTES OF THE NOTE, not merely on res.IsError. A refusal that still wrote
//     the property would satisfy an IsError-only assertion, and so would a
//     reinstated relation branch that happened to also report an error. The
//     file must come back byte-identical.
//  2. THE BODY-WIKILINK MODE IS UNTOUCHED. op "link" WITHOUT `relation` is a
//     different feature that was never in scope for the removal, and the
//     surviving tests here fail if stripping the relation branch damaged it.
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// veRelationSchema writes a "widget" record schema declaring TWO relation
// properties: "related" as many-valued, "owner" as single-valued (many
// absent, which FR-006 says means scalar). Both cardinalities are kept
// because the removed branch treated them DIFFERENTLY (add vs. overwrite),
// so a reinstated branch could come back in either shape — and each must be
// caught.
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

// ---------------------------------------------------------------------------
// The removal — `relation` is refused, and nothing is written
// ---------------------------------------------------------------------------

// TestKnowledgeEditLink_RelationArgumentRefusedAndNoteUntouched is the
// the test that must DIE if the removed branch is ever restored. It runs the
// exact call shapes that branch used to serve — declared many-valued,
// declared single-valued, and undeclared — and requires, for every one of
// them, that the note's bytes are EXACTLY what they were before the call.
//
// Why the byte comparison rather than `require.False(t, changed)`: the removed
// code path reported its writes through the SAME success/failure channel as
// everything else, so any assertion phrased in terms of the tool's own report
// can be satisfied by a reinstated branch that reports honestly. The file on
// disk cannot be talked around. Restore knowledgeEditLinkPropertyEdit and
// re-wire execLink's `relation != ""` branch and every subtest here fails on
// the content assertion, not merely on the error one.
func TestKnowledgeEditLink_RelationArgumentRefusedAndNoteUntouched(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		relation string
		schema   bool
	}{
		{
			name:     "declared_many_valued",
			body:     "---\ntype: widget\nrelated:\n  - \"[[A]]\"\n  - \"[[C]]\"\n---\nBody.\n",
			relation: "related",
			schema:   true,
		},
		{
			name:     "declared_single_valued",
			body:     "---\ntype: widget\nowner: \"[[A]]\"\n---\nBody.\n",
			relation: "owner",
			schema:   true,
		},
		{
			name:     "undeclared_property_no_schema_at_all",
			body:     "---\nrelated:\n  - \"[[A]]\"\n  - \"[[C]]\"\n---\nBody.\n",
			relation: "related",
			schema:   false,
		},
		{
			name:     "undeclared_property_absent_key",
			body:     "---\nstatus: draft\n---\nBody.\n",
			relation: "related",
			schema:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, ws, root := a4Fixture(t, "kb")
			if tc.schema {
				veRelationSchema(t, root)
			}
			deps, _ := a4Deps(home)
			tool := veTool(deps)
			a4Note(t, root, "Real.md", tc.body)

			before := a4Read(t, root, "Real.md")
			v := a4Version(t, root, "Real.md")

			res := tool.Execute(a4Ctx("mia", ws), map[string]any{
				"collection": "kb", "op": "link", "path": "Real.md",
				"target": "B", "relation": tc.relation, "expect_version": v,
			})

			// The BYTES are checked FIRST, and deliberately so. "The write
			// did not land" is the actual claim; the error code is only how
			// the caller is told. Asserting IsError first would abort the
			// subtest on a regression and leave the far more informative
			// evidence — what the note now contains — unreported, and it
			// would leave the byte check entirely unexercised by exactly the
			// regression it exists to catch.
			after := a4Read(t, root, "Real.md")
			assert.Equal(t, before, after,
				"a refused op link must not write ANY byte of the note")
			assert.NotContains(t, after, "[[B]]",
				"the relation-writing branch of op link appears to be back")

			assert.True(t, res.IsError,
				"op link must refuse 'relation': the frontmatter-property mode was removed, got: %s",
				res.ForLLM)
		})
	}
}

// TestKnowledgeEditLink_RelationRefusalNamesTheReplacement pins the CONTENT of
// the refusal, not just its existence.
//
// An agent that has learned the old form has to be able to retry correctly
// from this message alone — it cannot read an ADR, and a bare "link does not
// read relation" (which is what the generic per-op argument sweep would have
// produced had the dedicated refusal not been added ahead of it) tells it
// what is wrong without telling it what to do instead. Every argument name
// the replacement call needs is required here, so that rewording the message
// into something less actionable fails.
func TestKnowledgeEditLink_RelationRefusalNamesTheReplacement(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Deal.md", "---\ntype: deal\npartners:\n  - \"[[Alpha]]\"\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "link", "path": "Deal.md",
		"target": "Beta", "relation": "partners",
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.True(t, res.IsError)

	for _, want := range []string{
		`op "relation"`, // the op to use
		"property",      // where the old 'relation' value goes
		"relation_op",   // the verb argument
		`"add"`,         // the verbs themselves
		`"remove"`,
		`"replace"`,
		"targets", // the plural, list-shaped target argument
	} {
		require.Contains(t, res.ForLLM, want,
			"the refusal must name %s so an agent can retry from the error text alone: %s",
			want, res.ForLLM)
	}
}

// TestKnowledgeEditLink_RelationRefusedEvenWhenEmpty pins that PRESENCE, not
// emptiness, is what is refused.
//
// `relation: ""` is a caller whose property name resolved to nothing —
// a template that did not expand, a variable that came back empty. Running it
// as a body wikilink would do something other than what it asked and report
// success, which is the silent-wrong-write class this package refuses
// everywhere else.
func TestKnowledgeEditLink_RelationRefusedEvenWhenEmpty(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Real.md", "---\nstatus: draft\n---\nBody.\n")

	before := a4Read(t, root, "Real.md")
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "link", "path": "Real.md",
		"target": "B", "relation": "", "expect_version": a4Version(t, root, "Real.md"),
	})

	require.True(t, res.IsError, "an empty 'relation' is still 'relation': %s", res.ForLLM)
	require.Equal(t, before, a4Read(t, root, "Real.md"),
		"a refused call must not fall through to the body-wikilink mode")
}

// TestKnowledgeEditLink_RelationNotAdvertisedInSchema pins that the tool stops
// OFFERING the argument, not merely refusing it.
//
// A model picks its arguments from the parameter schema. Leaving 'relation'
// declared while refusing it at runtime would keep every model sending it
// forever and turn a removal into a permanent error loop.
func TestKnowledgeEditLink_RelationNotAdvertisedInSchema(t *testing.T) {
	require.NotContains(t, editArgNames, "relation",
		"'relation' is refused by name; it must not be an accepted argument")
	require.NotContains(t, editOpArgs[opLink], "relation",
		"op link's own argument set must no longer carry 'relation'")

	params := NewEditTool(AuthoringDeps{}).Parameters()
	props, ok := params["properties"].(map[string]any)
	require.True(t, ok, "parameters must declare a properties object")
	require.NotContains(t, props, "relation",
		"the model-facing schema must not advertise an argument the tool refuses")
}

// ---------------------------------------------------------------------------
// The scope boundary — the BODY wikilink mode is untouched
// ---------------------------------------------------------------------------

// TestKnowledgeEditLink_BodyWikilinkStillWorks is the other half of the
// removal's contract, and the reason the removal was scoped the way it was.
//
// op "link" WITHOUT `relation` inserts a wikilink into the note's BODY. It
// shares nothing with the removed mode but the op name — a different
// primitive (AddWikilink, which splices a markdown list item under a heading)
// writing to a different part of the file. If stripping the relation branch
// had taken this with it, this test is what says so.
func TestKnowledgeEditLink_BodyWikilinkStillWorks(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Real.md", "---\nstatus: draft\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "link", "path": "Real.md",
		"target": "Somewhere Else", "expect_version": a4Version(t, root, "Real.md"),
	})
	require.False(t, res.IsError, "the body-wikilink mode must keep working: %s", res.ForLLM)

	got := a4Read(t, root, "Real.md")
	require.Contains(t, got, "[[Somewhere Else]]")
	// The link belongs in the BODY. Frontmatter ends at the second fence;
	// everything the wikilink mode writes must land after it — this is the
	// assertion that distinguishes the surviving feature from the removed one
	// rather than merely finding the text anywhere in the file.
	_, body, found := strings.Cut(strings.TrimPrefix(got, "---\n"), "\n---\n")
	require.True(t, found, "note should still have terminated frontmatter: %s", got)
	require.Contains(t, body, "[[Somewhere Else]]",
		"op link writes a BODY wikilink; it must not land in frontmatter")
}

// TestKnowledgeEditLink_BodyWikilinkHonoursAliasAndSection pins the two
// arguments that only the surviving mode reads. They were declared alongside
// `relation` in the same parameter block, so a removal that over-reached
// would most plausibly have taken one of them too.
func TestKnowledgeEditLink_BodyWikilinkHonoursAliasAndSection(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Real.md", "---\nstatus: draft\n---\nBody.\n\n## See also\n\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "link", "path": "Real.md",
		"target": "Target Note", "alias": "the target", "section": "See also",
		"expect_version": a4Version(t, root, "Real.md"),
	})
	require.False(t, res.IsError, "alias and section must still be read: %s", res.ForLLM)

	got := a4Read(t, root, "Real.md")
	require.Contains(t, got, "[[Target Note|the target]]", "alias must be rendered")
	idx := strings.Index(got, "## See also")
	require.Positive(t, idx, "the section heading should still be present: %s", got)
	require.Contains(t, got[idx:], "[[Target Note|the target]]",
		"the link must be placed under the named section")
}
