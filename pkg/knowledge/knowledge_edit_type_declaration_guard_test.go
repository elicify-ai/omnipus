// Omnipus — Claude review round 3, finding C7: knowledgeEditValidateValue's
// `type` short-circuit stamped the caller's value as governed with no schema
// lookup, so a record could be retyped to an undeclared type or a typo
// (`dael`) and keep its old-type identifier. After the fix, the only accepted
// `type` values are declared schema types; the typo case gets a plain refusal
// naming the declared types, and a DECLARED type still goes through the
// identity rule (commit 2faacf492's cross-type refusal).
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestSetProperty_Type|^TestKnowledgeEdit_Create_FrontmatterType' ./pkg/knowledge/
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

// c7TwoTypeSchema declares both "deal" and "task", so a test can ask for a
// declared-but-different type (the identity rule's case) as well as a typo.
func c7TwoTypeSchema(t *testing.T, root string) {
	t.Helper()
	dir := schemaDir(t, root)
	deal := "schema_version: 1\n" +
		"type: deal\n" +
		"identity:\n  prefix: DEAL\n" +
		"properties:\n" +
		"  status: { type: enum, values: [prospect, won, lost] }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deal.yaml"), []byte(deal), 0o600))
	task := "schema_version: 1\n" +
		"type: task\n" +
		"identity:\n  prefix: TASK\n" +
		"properties:\n" +
		"  title: { type: text }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "task.yaml"), []byte(task), 0o600))

	set, report, err := records.LoadSchemas(root)
	require.NoError(t, err)
	require.Emptyf(t, report.Rejections,
		"the fixture schemas must LOAD, or the refusals below fire for the wrong reason: %+v", report.Rejections)
	_, hasDeal := set.Get("deal")
	_, hasTask := set.Get("task")
	require.True(t, hasDeal && hasTask, "fixture must declare both types")
}

func TestSetProperty_TypeMustNameADeclaredRecordType(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	c7TwoTypeSchema(t, root)
	a4Note(t, root, "Plain.md", "---\ntitle: Something\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Plain.md",
		"property": "type", "value": "dael", "expect_version": a4Version(t, root, "Plain.md"),
	})
	require.True(t, res.IsError, "a typo'd type name must be refused, not written: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, `"dael"`, "the refusal must quote the name as sent: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "deal", "the refusal must name the declared types: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "task", "the refusal must name EVERY declared type: %s", res.ForLLM)
	got := a4Read(t, root, "Plain.md")
	require.NotContains(t, got, "dael", "the typo must not be written:\n%s", got)
}

func TestSetProperty_DeclaredTypeStillGoesThroughTheIdentityRule(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	c7TwoTypeSchema(t, root)
	a4Note(t, root, "D.md", "---\ntype: deal\nid: DEAL-0001\nstatus: prospect\n---\nBody.\n")

	// `task` IS declared — so the only refusal available is the identity rule
	// (2faacf492): a record cannot change type and carry its id into it.
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "D.md",
		"property": "type", "value": "task", "expect_version": a4Version(t, root, "D.md"),
	})
	require.True(t, res.IsError, "a cross-type change on a record is refused: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "already a deal record",
		"the refusal must be the identity rule's own wording: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "DEAL-0001", "the identity rule names the identifier: %s", res.ForLLM)
	require.NotContains(t, res.ForLLM, "declared record types",
		"a declared type must not be refused as an unknown one: %s", res.ForLLM)
	require.Contains(t, a4Read(t, root, "D.md"), "type: deal", "the record must be untouched")
}

func TestSetProperty_TypeNamingARejectedSchemaIsRefusedWithTheLoadFailure(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	veBrokenDealSchema(t, root) // "deal" is a candidate type whose schema is rejected
	a4Note(t, root, "Plain.md", "---\ntitle: Something\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Plain.md",
		"property": "type", "value": "deal", "expect_version": a4Version(t, root, "Plain.md"),
	})
	require.True(t, res.IsError, "a type whose schema failed to load cannot be adopted: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "schema_version",
		"the refusal must echo the real load failure, not read like a typo: %s", res.ForLLM)
	require.NotContains(t, a4Read(t, root, "Plain.md"), "type: deal")
}

func TestSetProperty_TypeWithNoDeclaredTypesAnywhereIsRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps) // no schema files at all
	a4Note(t, root, "Plain.md", "---\ntitle: Something\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Plain.md",
		"property": "type", "value": "task", "expect_version": a4Version(t, root, "Plain.md"),
	})
	require.True(t, res.IsError, "with no declared types there is nothing to promote to: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "create_record_type",
		"the refusal must name the way forward: %s", res.ForLLM)
}

func TestKnowledgeEdit_Create_FrontmatterTypeMustBeDeclared(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	c7TwoTypeSchema(t, root)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "Typo.md",
		"frontmatter": map[string]any{"type": "dael"}, "body": "Body.\n",
	})
	require.True(t, res.IsError, "create must not establish an undeclared type either: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, `"dael"`, res.ForLLM)
	require.Contains(t, res.ForLLM, "deal", res.ForLLM)
	_, statErr := os.Stat(filepath.Join(root, "Typo.md"))
	require.True(t, os.IsNotExist(statErr), "nothing must be written on the refusal")
}
