// Omnipus — Claude review round 3, finding C6: set_property's value:null
// removal support must pass the same schema authority the write path uses.
// A relation (or person) property cannot be removed this way — removing it
// discards the whole list of edges (FR-045) — and a required property cannot
// be removed at all, because every record of the type must carry a value.
// Everything else stays removable, exactly as before (D-93's stale-key
// removal, and FR-005's unconstrained ordinary notes).
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestSetProperty_Remove' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// schemaDir creates (once) and returns the vault's record-schema directory.
func schemaDir(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, records.VaultMarkerDirName, records.RecordsDirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	return dir
}

// c6RemovalSchema declares the property mix C6 was found against: a relation,
// a person property, a REQUIRED enum, and an optional many-valued text.
//
// ⚠️ PROVE THE SCHEMA ACTUALLY LOADED, for the same reason relSchema does
// (knowledge_edit_relation_test.go): a rejected schema degrades the note to
// an unconstrained ordinary note, the refusals below never fire, and every
// refusal test sees a successful removal instead.
func c6RemovalSchema(t *testing.T, root string) {
	t.Helper()
	dir := schemaDir(t, root)
	yaml := "schema_version: 1\n" +
		"type: project\n" +
		"identity:\n  prefix: PRJ\n" +
		"properties:\n" +
		"  status: { type: enum, values: [planned, active], required: true }\n" +
		"  lead:   { type: relation, to: project }\n" +
		"  owner:  { type: person }\n" +
		"  tags:   { type: text, many: true }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.yaml"), []byte(yaml), 0o600))

	set, report, err := records.LoadSchemas(root)
	require.NoError(t, err)
	require.Emptyf(t, report.Rejections,
		"the fixture schema must LOAD, or every removal-refusal test below passes vacuously: %+v", report.Rejections)
	schema, ok := set.Get("project")
	require.True(t, ok, "the fixture schema must resolve as type \"project\"")
	for _, name := range []string{"status", "lead", "owner", "tags"} {
		prop, declared := schema.Property(name)
		require.Truef(t, declared, "fixture must declare %q", name)
		if name == "status" {
			require.True(t, prop.Required, "fixture's status must be required, or the required-refusal test proves nothing")
		}
	}
}

func TestSetProperty_RemoveRelationPropertyIsRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	c6RemovalSchema(t, root)
	a4Note(t, root, "P.md", "---\ntype: project\nid: PRJ-0001\nstatus: planned\n"+
		"lead: \"[[Ada]]\"\nowner: \"[[Grace]]\"\ntags: [alpha]\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "P.md",
		"property": "lead", "value": nil, "expect_version": a4Version(t, root, "P.md"),
	})
	require.True(t, res.IsError, "removing a relation property with value:null must be refused: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "relation", "the refusal must name what the property is: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, `op "relation"`, "the refusal must direct the caller to op relation: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "FR-045", "the refusal must cite the rule it enforces: %s", res.ForLLM)
	got := a4Read(t, root, "P.md")
	require.Contains(t, got, `lead: "[[Ada]]"`, "the edge list must be untouched:\n%s", got)
}

func TestSetProperty_RemovePersonPropertyIsRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	c6RemovalSchema(t, root)
	a4Note(t, root, "P.md", "---\ntype: project\nid: PRJ-0001\nstatus: planned\nowner: \"[[Grace]]\"\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "P.md",
		"property": "owner", "value": nil, "expect_version": a4Version(t, root, "P.md"),
	})
	require.True(t, res.IsError, "a person property is a relation property (FR-045) and must be refused: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, `op "relation"`, res.ForLLM)
	require.Contains(t, a4Read(t, root, "P.md"), `owner: "[[Grace]]"`, "the person list must be untouched")
}

func TestSetProperty_RemoveRequiredPropertyIsRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	c6RemovalSchema(t, root)
	a4Note(t, root, "P.md", "---\ntype: project\nid: PRJ-0001\nstatus: planned\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "P.md",
		"property": "status", "value": nil, "expect_version": a4Version(t, root, "P.md"),
	})
	require.True(t, res.IsError, "removing a required property must be refused: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "required", "the refusal must say WHY: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "project", "the refusal must name the record type: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, ".omnipus-vault/records/project.yaml",
		"the refusal must name the schema file an operator edits to make the property optional: %s", res.ForLLM)
	require.Contains(t, a4Read(t, root, "P.md"), "status: planned", "the value must be untouched")
}

func TestSetProperty_RemoveOptionalPropertyStillRemoves(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	c6RemovalSchema(t, root)
	a4Note(t, root, "P.md", "---\ntype: project\nid: PRJ-0001\nstatus: planned\ntags: [alpha, beta]\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "P.md",
		"property": "tags", "value": nil, "expect_version": a4Version(t, root, "P.md"),
	})
	require.False(t, res.IsError, "an optional declared property stays removable: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "REMOVED tags")
	require.NotContains(t, a4Read(t, root, "P.md"), "tags")
}

func TestSetProperty_RemoveUndeclaredKeyOnARecordStillRemoves(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	c6RemovalSchema(t, root)
	// `priority` is the D-93 pre-rename key the type no longer declares.
	a4Note(t, root, "P.md", "---\ntype: project\nid: PRJ-0001\nstatus: planned\npriority: low\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "P.md",
		"property": "priority", "value": nil, "expect_version": a4Version(t, root, "P.md"),
	})
	require.False(t, res.IsError, "removing an undeclared key must stay allowed (D-93): %s", res.ForLLM)
	require.NotContains(t, a4Read(t, root, "P.md"), "priority")
}

func TestSetProperty_RemoveOnAnOrdinaryNoteStillRemoves(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	c6RemovalSchema(t, root)
	a4Note(t, root, "Plain.md", "---\ntitle: Something\nlead: \"[[Ada]]\"\n---\nBody.\n")

	// FR-005: an ordinary note is unconstrained — even a lead: key, which on a
	// record would be a relation, is just a frontmatter key here.
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Plain.md",
		"property": "lead", "value": nil, "expect_version": a4Version(t, root, "Plain.md"),
	})
	require.False(t, res.IsError, "an ordinary note's keys stay removable (FR-005): %s", res.ForLLM)
	require.NotContains(t, a4Read(t, root, "Plain.md"), "lead:")
}
