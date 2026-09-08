// Omnipus — tests for knowledge_base_create (KB-1,
// defect-list-knowledge-base-ux-2026-09-08.md, founder-ratified 2026-09-08).
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// TestCreateBase_CreatesAtWorkspaceRoot is the happy path: no parent_path,
// just a name. The marker must land in the workspace's own work tree, and
// the SAME workspace's scope resolution (what every other knowledge_* tool
// uses) must find it afterward by the name this call returns.
func TestCreateBase_CreatesAtWorkspaceRoot(t *testing.T) {
	home := a4Home(t)
	ws := a4Workspace(t, home)
	deps, rec := a4Deps(home)
	tool := NewCreateBaseTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{"name": "Project Notes"})
	require.False(t, res.IsError, "unexpected refusal: %s", res.ForLLM)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &payload))
	assert.Equal(t, "Project Notes", payload["collection"])
	assert.Equal(t, "Project Notes", payload["path"])
	assert.Equal(t, true, payload["created"])

	workDir, err := workspace.SafeWorkDir(home, ws)
	require.NoError(t, err)
	assert.DirExists(t, filepath.Join(workDir, "Project Notes", MarkerDirName))

	// The tool the agent would actually reach for NEXT must see it, by the
	// exact name this call returned.
	col := a4Scoped(t, home, ws, "Project Notes")
	assert.Equal(t, WorkTreeOrigin, col.Origin)

	require.Len(t, rec.applied(), 1)
	assert.Equal(t, "mia", rec.applied()[0].AgentID)
	assert.Equal(t, ws, rec.applied()[0].WorkspaceID)
	assert.Equal(t, authorOpCreateBase, rec.applied()[0].Operation)
}

// TestCreateBase_CreatesUnderParentPath proves parent_path is honoured and
// composed correctly, matching the REST handler's own join.
func TestCreateBase_CreatesUnderParentPath(t *testing.T) {
	home := a4Home(t)
	ws := a4Workspace(t, home)
	deps, _ := a4Deps(home)
	tool := NewCreateBaseTool(deps)

	workDir, err := workspace.SafeWorkDir(home, ws)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "clients", "acme"), 0o755))

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"name": "Acme KB", "parent_path": "clients/acme",
	})
	require.False(t, res.IsError, "unexpected refusal: %s", res.ForLLM)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &payload))
	assert.Equal(t, "clients/acme/Acme KB", payload["path"])
	assert.DirExists(t, filepath.Join(workDir, "clients", "acme", "Acme KB", MarkerDirName))
}

// TestCreateBase_RefusesWhenTargetAlreadyExists is the collision case: a
// PLAIN folder (not a knowledge base) already sits at the target. The tool
// must refuse rather than silently adopting it as a knowledge base.
func TestCreateBase_RefusesWhenTargetAlreadyExists(t *testing.T) {
	home := a4Home(t)
	ws := a4Workspace(t, home)
	deps, rec := a4Deps(home)
	tool := NewCreateBaseTool(deps)

	workDir, err := workspace.SafeWorkDir(home, ws)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "Existing"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "Existing", "note.txt"), []byte("x"), 0o644))

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{"name": "Existing"})
	require.True(t, res.IsError, "must refuse when something already exists at the target")
	assert.Contains(t, res.ForLLM, "already exists")

	// Nothing was converted: the folder must still hold exactly its
	// original file, no marker planted.
	assert.NoDirExists(t, filepath.Join(workDir, "Existing", MarkerDirName))
	assert.FileExists(t, filepath.Join(workDir, "Existing", "note.txt"))

	require.Len(t, rec.refusals(), 1)
	assert.Contains(t, rec.refusals()[0].Reason, "already exists")
}

// TestCreateBase_RefusesWhenAlreadyAKnowledgeBase is the same collision
// check against a target that is ALREADY a knowledge base, not merely a
// plain folder — CreateInWorkspace's own ErrAlreadyKnowledgeBase path.
func TestCreateBase_RefusesWhenAlreadyAKnowledgeBase(t *testing.T) {
	home := a4Home(t)
	ws := a4Workspace(t, home)
	deps, _ := a4Deps(home)
	tool := NewCreateBaseTool(deps)

	workDir, err := workspace.SafeWorkDir(home, ws)
	require.NoError(t, err)
	_, cerr := CreateInWorkspace(home, ws, "AlreadyKB", Marker{DisplayName: "AlreadyKB"})
	require.NoError(t, cerr)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{"name": "AlreadyKB"})
	require.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "already exists")
	_ = workDir
}

// TestCreateBase_RefusesEmptyOrInvalidName covers the name-shape refusals a
// model could plausibly send: empty, ".", "..", and a name containing a
// path separator (which would otherwise silently compose a nested path the
// caller did not ask for).
func TestCreateBase_RefusesEmptyOrInvalidName(t *testing.T) {
	home := a4Home(t)
	ws := a4Workspace(t, home)
	deps, _ := a4Deps(home)
	tool := NewCreateBaseTool(deps)

	for _, name := range []string{"", "  ", ".", "..", "a/b", "a\\b"} {
		t.Run(name, func(t *testing.T) {
			res := tool.Execute(a4Ctx("mia", ws), map[string]any{"name": name})
			assert.Truef(t, res.IsError, "name %q must be refused", name)
		})
	}
}

// TestCreateBase_RefusesPathEscapingViaParentPath proves a parent_path
// crafted to escape the workspace is refused, not silently clamped or
// followed — the "path escapes the workspace" half of KB-1's requirement.
func TestCreateBase_RefusesPathEscapingViaParentPath(t *testing.T) {
	home := a4Home(t)
	ws := a4Workspace(t, home)
	deps, rec := a4Deps(home)
	tool := NewCreateBaseTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"name": "Escaped", "parent_path": "../../etc",
	})
	require.True(t, res.IsError, "a parent_path attempting to leave the workspace must be refused")

	// Nothing was created outside the workspace's own work tree.
	workDir, err := workspace.SafeWorkDir(home, ws)
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(filepath.Dir(filepath.Dir(workDir)), "etc", "Escaped"))
	require.Len(t, rec.refusals(), 1)
}

// TestCreateBase_RefusesWithNoWorkspace is US-9's own shape applied to
// creation: a turn that carries no workspace id can create nothing, rather
// than falling back to some default location.
func TestCreateBase_RefusesWithNoWorkspace(t *testing.T) {
	home := a4Home(t)
	deps, _ := a4Deps(home)
	tool := NewCreateBaseTool(deps)

	res := tool.Execute(a4Ctx("mia", ""), map[string]any{"name": "Nowhere"})
	require.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "workspace")
}

// TestCreateBase_RefusesWithoutAnAuditSink mirrors every other mutating
// tool in this package (FR-090): a nil Audit sink refuses the whole call
// rather than writing unaudited.
func TestCreateBase_RefusesWithoutAnAuditSink(t *testing.T) {
	home := a4Home(t)
	ws := a4Workspace(t, home)
	tool := NewCreateBaseTool(AuthoringDeps{Home: home}) // no Audit

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{"name": "Unaudited"})
	require.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "FR-090")

	workDir, err := workspace.SafeWorkDir(home, ws)
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(workDir, "Unaudited"))
}

// TestCreateBase_UnknownArgumentIsRefused matches this package's other
// tools' "a silently ignored argument is a caller that believes it narrowed
// something" rule.
func TestCreateBase_UnknownArgumentIsRefused(t *testing.T) {
	home := a4Home(t)
	ws := a4Workspace(t, home)
	deps, _ := a4Deps(home)
	tool := NewCreateBaseTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{"name": "X", "collection": "should-not-exist"})
	require.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "unknown argument")
}

// TestCreateBase_ToolIdentity pins the registered name and basic tool-picker
// surface (description, object schema, category, scope) the way this
// package's other tools pin their own.
func TestCreateBase_ToolIdentity(t *testing.T) {
	tool := NewCreateBaseTool(AuthoringDeps{})
	assert.Equal(t, "knowledge_base_create", tool.Name())
	assert.NotEmpty(t, tool.Description())
	assert.Equal(t, "object", tool.Parameters()["type"])
	assert.Contains(t, tool.Parameters()["required"], "name")
}
