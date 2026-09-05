// Omnipus — tests for knowledge_configure (ADR-068 D15.6, spec §4.1.6): the
// control plane — record-type and saved-view authoring, and the cascade
// report every write in this tool must state in counts.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestKnowledgeConfigure' ./pkg/knowledge/
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

// kcTool builds knowledge_configure over a fresh fixture's deps.
func kcTool(deps AuthoringDeps) *ConfigureTool { return NewConfigureTool(deps) }

// ---------------------------------------------------------------------------
// create_record_type
// ---------------------------------------------------------------------------

func TestKnowledgeConfigure_CreateRecordType_Success(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, audit := a4Deps(home)
	tool := kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget",
		"definition": map[string]any{
			"schema_version": float64(1),
			"label":          "Widget",
			"properties": map[string]any{
				"status": map[string]any{"type": "enum", "values": []any{"draft", "shipped"}, "required": true},
			},
		},
	})
	require.False(t, res.IsError, "unexpected refusal: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, `record type "widget" created`)
	require.Contains(t, res.ForLLM, "CASCADE (meaning): 0 note(s) now match record type \"widget\"")

	// The file actually landed, and is a valid schema_version 1 file.
	raw, err := os.ReadFile(filepath.Join(root, ".omnipus-vault", "records", "widget.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(raw), "schema_version: 1")

	// FR-077/AC-C5: every call is audited, applied or refused, naming the
	// operation, agent, workspace and target.
	applied := audit.applied()
	require.Len(t, applied, 1)
	require.EqualValues(t, "knowledge.configure", applied[0].Operation)
	require.Equal(t, "mia", applied[0].AgentID)
	require.Equal(t, ws, applied[0].WorkspaceID)
	require.Contains(t, applied[0].Paths, ".omnipus-vault/records/widget.yaml")
}

// TestKnowledgeConfigure_CreateRecordType_CascadeCountsPreExistingNotes is
// AC-C1: declaring a type over notes that already carry `type: widget` in
// their frontmatter must report the conversion count and name the ones that
// newly fail validation — never just "type created".
func TestKnowledgeConfigure_CreateRecordType_CascadeCountsPreExistingNotes(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	a4Note(t, root, "one.md", "---\ntype: widget\nstatus: draft\n---\nbody\n")
	a4Note(t, root, "two.md", "---\ntype: widget\nstatus: shipped\n---\nbody\n")
	a4Note(t, root, "three.md", "---\ntype: widget\n---\nbody without status\n")
	// A note of a DIFFERENT type must not be counted.
	a4Note(t, root, "other.md", "---\ntype: gadget\n---\nbody\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties": map[string]any{
				"status": map[string]any{"type": "enum", "values": []any{"draft", "shipped"}, "required": true},
			},
		},
	})
	require.False(t, res.IsError, "unexpected refusal: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "CASCADE (meaning): 3 note(s) now match record type \"widget\"")
	require.Contains(t, res.ForLLM, "2 validate clean")
	require.Contains(t, res.ForLLM, "1 newly reported:")
	require.Contains(t, res.ForLLM, "three.md")
	require.Contains(t, res.ForLLM, "status")
	require.Contains(t, res.ForLLM, "0 record(s) lost validity")
}

func TestKnowledgeConfigure_CreateRecordType_AlreadyExists_Refused(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, audit := a4Deps(home)
	tool := kcTool(deps)

	def := map[string]any{
		"schema_version": float64(1),
		"properties":     map[string]any{"name": map[string]any{"type": "text"}},
	}
	first := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget", "definition": def,
	})
	require.False(t, first.IsError)

	second := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget", "definition": def,
	})
	require.True(t, second.IsError)
	require.Contains(t, second.ForLLM, "already declared")
	require.Contains(t, second.ForLLM, "edit_record_type")

	refusals := audit.refusals()
	require.Len(t, refusals, 1)
	require.EqualValues(t, "knowledge.configure", refusals[0].Operation)
}

func TestKnowledgeConfigure_CreateRecordType_UnknownPropertyType_Refused(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties": map[string]any{
				"closed": map[string]any{"type": "boolean"},
			},
		},
	})
	require.True(t, res.IsError)
	require.Contains(t, res.ForLLM, `"boolean"`)
	// The shape+remedy clause (R-F): every permitted type is named.
	for _, want := range []string{"text", "enum", "relation", "date", "integer", "decimal", "person"} {
		require.Contains(t, res.ForLLM, want)
	}

	// Nothing was written — a refused declaration must not land on disk.
	col := a4Scoped(t, home, ws, "kb")
	_, err := os.Stat(filepath.Join(col.Root, ".omnipus-vault", "records", "widget.yaml"))
	require.True(t, os.IsNotExist(err))
}

func TestKnowledgeConfigure_CreateRecordType_MissingSchemaVersion_Refused(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget",
		"definition": map[string]any{
			"properties": map[string]any{"name": map[string]any{"type": "text"}},
		},
	})
	require.True(t, res.IsError)
	require.Contains(t, res.ForLLM, "schema_version is missing")
}

func TestKnowledgeConfigure_CreateRecordType_TypeMismatch_Refused(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget",
		"definition": map[string]any{
			"schema_version": float64(1),
			"type":           "gadget",
			"properties":     map[string]any{"name": map[string]any{"type": "text"}},
		},
	})
	require.True(t, res.IsError)
	require.Contains(t, res.ForLLM, `"widget"`)
	require.Contains(t, res.ForLLM, `"gadget"`)
}

// ---------------------------------------------------------------------------
// edit_record_type
// ---------------------------------------------------------------------------

// TestKnowledgeConfigure_EditRecordType_LostValidityCounted is D15.6's
// central promise for an edit: a note that validated cleanly under the OLD
// declaration and does not under the NEW one is named as having LOST
// validity, distinctly from a note that was already broken.
func TestKnowledgeConfigure_EditRecordType_LostValidityCounted(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	create := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties": map[string]any{
				"name": map[string]any{"type": "text", "required": true},
			},
		},
	})
	require.False(t, create.IsError, create.ForLLM)

	// Valid under the old schema: has `name`, no `owner`.
	a4Note(t, root, "clean.md", "---\ntype: widget\nname: Alpha\n---\n")
	// ALREADY invalid under the old schema (missing `name`) — must not be
	// double-counted as "lost" by the edit that follows.
	a4Note(t, root, "already-broken.md", "---\ntype: widget\n---\n")

	edit := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "edit_record_type", "type": "widget",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties": map[string]any{
				"name":  map[string]any{"type": "text", "required": true},
				"owner": map[string]any{"type": "text", "required": true},
			},
		},
	})
	require.False(t, edit.IsError, edit.ForLLM)
	require.Contains(t, edit.ForLLM, "CASCADE (meaning): 2 note(s) now match record type \"widget\"")
	require.Contains(t, edit.ForLLM, "0 validate clean")
	require.Contains(t, edit.ForLLM, "1 newly reported:")
	require.Contains(t, edit.ForLLM, "1 record(s) lost validity")
	require.Contains(t, edit.ForLLM, "clean.md")
	require.NotContains(t, strings.SplitN(edit.ForLLM, "1 newly reported:", 2)[1], "already-broken.md")
}

func TestKnowledgeConfigure_EditRecordType_UnknownType_Refused(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "edit_record_type", "type": "nosuch",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties":     map[string]any{"name": map[string]any{"type": "text"}},
		},
	})
	require.True(t, res.IsError)
	require.Contains(t, res.ForLLM, `no record type "nosuch" is declared`)
}

// ---------------------------------------------------------------------------
// delete_record_type
// ---------------------------------------------------------------------------

func TestKnowledgeConfigure_DeleteRecordType_RevertsCountAndAllowsRecreate(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	def := map[string]any{
		"schema_version": float64(1),
		"properties":     map[string]any{"name": map[string]any{"type": "text"}},
	}
	require.False(t, tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget", "definition": def,
	}).IsError)

	a4Note(t, root, "a.md", "---\ntype: widget\nname: A\n---\n")
	a4Note(t, root, "b.md", "---\ntype: widget\nname: B\n---\n")

	del := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "delete_record_type", "type": "widget",
	})
	require.False(t, del.IsError, del.ForLLM)
	require.Contains(t, del.ForLLM, `record type "widget" deleted`)
	require.Contains(t, del.ForLLM, "CASCADE (meaning): 2 record(s) revert to ordinary notes")

	_, err := os.Stat(filepath.Join(root, ".omnipus-vault", "records", "widget.yaml"))
	require.True(t, os.IsNotExist(err))

	// Deleted, so it can be freely re-declared — proves the file is really
	// gone, not merely reported gone.
	recreate := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget", "definition": def,
	})
	require.False(t, recreate.IsError, recreate.ForLLM)
}

func TestKnowledgeConfigure_DeleteRecordType_UnknownType_Refused(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "delete_record_type", "type": "nosuch",
	})
	require.True(t, res.IsError)
	require.Contains(t, res.ForLLM, `no record type "nosuch" is declared`)
}

// ---------------------------------------------------------------------------
// write_view / delete_view
// ---------------------------------------------------------------------------

func TestKnowledgeConfigure_WriteView_SuccessThenDelete(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	require.False(t, tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties": map[string]any{
				"status": map[string]any{"type": "enum", "values": []any{"draft", "shipped"}},
			},
		},
	}).IsError)

	write := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "open-widgets",
		"definition": map[string]any{
			"type":   "widget",
			"filter": map[string]any{"property": "status", "op": "=", "value": "draft"},
		},
	})
	require.False(t, write.IsError, write.ForLLM)
	require.Contains(t, write.ForLLM, `view "open-widgets" saved`)
	require.Contains(t, write.ForLLM, `"widget"`)

	raw, err := os.ReadFile(filepath.Join(root, ".omnipus-vault", "views", "open-widgets.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(raw), "name: open-widgets")

	del := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "delete_view", "view": "open-widgets",
	})
	require.False(t, del.IsError, del.ForLLM)
	_, err = os.Stat(filepath.Join(root, ".omnipus-vault", "views", "open-widgets.yaml"))
	require.True(t, os.IsNotExist(err))
}

func TestKnowledgeConfigure_WriteView_UnknownProperty_Refused(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	require.False(t, tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties":     map[string]any{"name": map[string]any{"type": "text"}},
		},
	}).IsError)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "bad-view",
		"definition": map[string]any{
			"type":     "widget",
			"grouping": []any{map[string]any{"property": "nosuchproperty"}},
		},
	})
	require.True(t, res.IsError)
	require.Contains(t, res.ForLLM, "nosuchproperty")
	require.Contains(t, res.ForLLM, "declared:")
}

func TestKnowledgeConfigure_DeleteView_UnknownName_Refused(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "delete_view", "view": "nosuch",
	})
	require.True(t, res.IsError)
	require.Contains(t, res.ForLLM, `no view "nosuch" is declared`)
}

// ---------------------------------------------------------------------------
// Cross-cutting: expect_version, redirects, unknown args, unknown op
// ---------------------------------------------------------------------------

// TestKnowledgeConfigure_ExpectVersion_Refused is FR-018a/AC-C3: the
// parameter cannot exist here, and the refusal explains why rather than
// reading as a generic "unknown argument".
func TestKnowledgeConfigure_ExpectVersion_Refused(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget",
		"expect_version": "v1:deadbeef",
		"definition":     map[string]any{"schema_version": float64(1), "properties": map[string]any{"name": map[string]any{"type": "text"}}},
	})
	require.True(t, res.IsError)
	require.Contains(t, res.ForLLM, "expect_version")
	require.Contains(t, res.ForLLM, "cannot guard a")

	// AC-C3: the parameter must not even be IN the declared tool schema.
	params := tool.Parameters()
	props, _ := params["properties"].(map[string]any)
	_, has := props["expect_version"]
	require.False(t, has, "expect_version must not appear in the tool's own parameter schema")
}

func TestKnowledgeConfigure_NoteOps_RedirectToKnowledgeEdit(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	for _, op := range []string{"create", "set_property", "append_section", "link", "replace_body"} {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{"collection": "kb", "op": op})
		require.True(t, res.IsError, "op=%s should be refused", op)
		require.Contains(t, res.ForLLM, "knowledge_edit", "op=%s", op)
	}
}

func TestKnowledgeConfigure_CascadeOps_RedirectToKnowledgeRestructure(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	for _, op := range []string{"rename", "move", "trash", "restore"} {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{"collection": "kb", "op": op})
		require.True(t, res.IsError, "op=%s should be refused", op)
		require.Contains(t, res.ForLLM, "knowledge_restructure", "op=%s", op)
	}
}

func TestKnowledgeConfigure_UnknownOp_NamesSupportedSet(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{"collection": "kb", "op": "frobnicate"})
	require.True(t, res.IsError)
	for _, want := range vaultConfigureOps {
		require.Contains(t, res.ForLLM, want)
	}
}

func TestKnowledgeConfigure_UnknownArgument_Refused(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget",
		"definition": map[string]any{"schema_version": float64(1), "properties": map[string]any{"name": map[string]any{"type": "text"}}},
		"bogus":      "x",
	})
	require.True(t, res.IsError)
	require.Contains(t, res.ForLLM, "bogus")
}

// present just to keep the records import in use when a subset of the file
// is commented out during development; harmless once every test above is
// active, since records.ValidateOptions etc. are already referenced.
var _ = records.ValidateOptions{}

// ---------------------------------------------------------------------------
// The view/type name is a FILENAME, and an agent supplies it
// ---------------------------------------------------------------------------

// TestKnowledgeConfigure_WriteView_NameEscapingTheViewsDir_Refused is the
// write_view half of the traversal finding.
//
// `view` is joined straight onto records.ViewsDir(root) and given a ".yaml"
// suffix, so a name carrying a path separator names a file the views
// directory does not contain. "../records/widget" reaches the record-type
// SCHEMA for `widget` and overwrites it with a view document — every gate in
// this tool passes, because none of them ever looked at the name's shape.
// Deeper chains leave the vault altogether.
//
// The oracle is not "some refusal happened": it is that the schema file still
// holds its own bytes afterwards, and that the escaped path was never
// created at all.
func TestKnowledgeConfigure_WriteView_NameEscapingTheViewsDir_Refused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	require.False(t, tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties":     map[string]any{"name": map[string]any{"type": "text"}},
		},
	}).IsError)

	schemaPath := filepath.Join(root, ".omnipus-vault", "records", "widget.yaml")
	before, err := os.ReadFile(schemaPath)
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		view string
	}{
		{"climbs into the sibling records directory", "../records/widget"},
		{"escapes the vault entirely", "../../../../pwned"},
		{"windows-style separator", `..\records\widget`},
		{"absolute path", "/tmp/omnipus-pwned"},
		{"bare dot", "."},
		{"bare dotdot", ".."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := tool.Execute(a4Ctx("mia", ws), map[string]any{
				"collection": "kb", "op": "write_view", "view": tc.view,
				"definition": map[string]any{"type": "widget"},
			})
			require.True(t, res.IsError, "must be refused, got: %s", res.ForLLM)
			require.Contains(t, res.ForLLM, "must be a name, not a path",
				"the refusal must name the rule it enforced")

			after, rerr := os.ReadFile(schemaPath)
			require.NoError(t, rerr, "the record-type schema must still exist")
			require.Equal(t, string(before), string(after),
				"the record-type schema must not have been overwritten")
		})
	}

	// Nothing at all was written anywhere the names pointed.
	for _, escaped := range []string{
		filepath.Join(root, ".omnipus-vault", "pwned.yaml"),
		filepath.Join(filepath.Dir(root), "pwned.yaml"),
		"/tmp/omnipus-pwned.yaml",
	} {
		_, serr := os.Stat(escaped)
		require.True(t, os.IsNotExist(serr), "nothing may be written at %s", escaped)
	}

	// The control case: an ordinary name still writes, so the guard refuses
	// traversal rather than refusing write_view.
	ok := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "plain-widgets",
		"definition": map[string]any{"type": "widget"},
	})
	require.False(t, ok.IsError, ok.ForLLM)
	_, err = os.Stat(filepath.Join(root, ".omnipus-vault", "views", "plain-widgets.yaml"))
	require.NoError(t, err)
}

// TestKnowledgeConfigure_CreateRecordType_NameEscapingTheRecordsDir_Refused
// is the same unguarded join one door over: `type` becomes a filename under
// records.SchemaDir the same way `view` does under ViewsDir. The write there
// is O_EXCL, so it cannot overwrite — it can still CREATE a file anywhere the
// process can write.
func TestKnowledgeConfigure_CreateRecordType_NameEscapingTheRecordsDir_Refused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	for _, typeName := range []string{"../views/planted", "../../../../planted", `..\views\planted`, "."} {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_record_type", "type": typeName,
			"definition": map[string]any{
				"schema_version": float64(1),
				"properties":     map[string]any{"name": map[string]any{"type": "text"}},
			},
		})
		require.True(t, res.IsError, "type=%q must be refused, got: %s", typeName, res.ForLLM)
		require.Contains(t, res.ForLLM, "must be a name, not a path")
	}

	for _, escaped := range []string{
		filepath.Join(root, ".omnipus-vault", "views", "planted.yaml"),
		filepath.Join(filepath.Dir(root), "planted.yaml"),
	} {
		_, serr := os.Stat(escaped)
		require.True(t, os.IsNotExist(serr), "nothing may be written at %s", escaped)
	}
}
