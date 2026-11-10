// Skills r15 §§5.1–5.5, §15: project skills are plain mounted files.
// Retired slug-to-project mutation routing is replaced by explicit file paths;
// deletion confinement is exercised at the existing project-writer boundary.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// testMountRecordForSkills mirrors pkg/workspace/mountstore.go's unexported
// on-disk JSON shape, exactly as pkg/agent/project_shelf_wiring_test.go's
// identically-named-in-spirit fixture already does for the R1 tests — kept
// package-local here because pkg/workspace's shape is unexported and this
// package cannot import a test helper from pkg/agent.
type testMountRecordForSkills struct {
	WorkspaceID string              `json:"workspace_id"`
	Mounts      []testMountForSkill `json:"mounts"`
}

type testMountForSkill struct {
	Name     string `json:"name"`
	HostPath string `json:"host_path"`
}

// seedProjectShelfFixture writes a minimal workspace record plus a mount
// record for wsID under home (mirroring the real on-disk shapes
// workspace.LoadMounts/workspace.ResolveDefaultID read), and a real project
// skill on disk at <mountRoot>/.claude/skills/<slug>/SKILL.md.
func seedProjectShelfFixture(t *testing.T, home, wsID, mountName, mountRoot, slug, description string) {
	t.Helper()

	wsDir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspaces: %v", err)
	}
	record := `{"id":"` + wsID + `","is_default":false}`
	if err := os.WriteFile(filepath.Join(wsDir, wsID+".json"), []byte(record), 0o644); err != nil {
		t.Fatalf("write workspace record: %v", err)
	}

	mountsDir := filepath.Join(home, "entities", "mounts")
	if err := os.MkdirAll(mountsDir, 0o700); err != nil {
		t.Fatalf("mkdir mounts: %v", err)
	}
	rec := testMountRecordForSkills{
		WorkspaceID: wsID,
		Mounts:      []testMountForSkill{{Name: mountName, HostPath: mountRoot}},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal mount record: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mountsDir, wsID+".json"), data, 0o600); err != nil {
		t.Fatalf("write mount record: %v", err)
	}

	skillDir := filepath.Join(mountRoot, ".claude", "skills", slug)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "---\nname: " + slug + "\ndescription: " + description + "\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

// Project writes use the real file tool. No registry shadow or history.
func TestSkillFileTool_ProjectShelf_WritesIntoMount(t *testing.T) {
	home, mountRoot, globalDir := t.TempDir(), t.TempDir(), t.TempDir()
	const wsID = "01SKILLPROJSHELF00000001"
	seedProjectShelfFixture(t, home, wsID, "acme", mountRoot, "db-migrate", "A project migration")
	path := filepath.Join(mountRoot, ".claude", "skills", "db-migrate", "SKILL.md")
	ctx := tools.WithWorkspaceID(context.Background(), wsID)
	content := validSkill("db-migrate") + "\nEdited in the mount.\n"
	result := tools.NewWriteFileTool(mountRoot, true).Execute(ctx, map[string]any{"path": path, "content": content, "overwrite": true})
	if result.IsError {
		t.Fatal(result.ForLLM)
	}
	if string(wu3ReadFile(t, path)) != content {
		t.Fatalf("mount bytes not changed: %s", result.ForLLM)
	}
	if _, err := os.Stat(filepath.Join(globalDir, "db-migrate")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project write forked registry shadow: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "system", "skill-history")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project must not enter registry pipeline: %v", err)
	}
}

// The former implicit global-fallback behavior is withdrawn (§5.4); the
// supported reader explicitly selects the installed object, independent of
// the current project shelf. Its data and session observation are real.
func TestGetSkill_ProjectNameDoesNotRetargetInstalledRead(t *testing.T) {
	home, mountRoot := t.TempDir(), t.TempDir()
	globalDir := filepath.Join(home, "skills")
	const wsID = "01SKILLPROJSHELF00000002"
	seedProjectShelfFixture(t, home, wsID, "acme", mountRoot, "db-migrate", "A project migration")
	validSkillFixture(t, globalDir, "db-migrate", "The separate installed skill")
	deps := newAuthoringDeps(t, globalDir, "")
	deps.Home = home
	deps.ResolveToolPolicy = func(_, _ string) (string, bool) { return "allow", true }
	ctx := tools.WithWorkspaceID(readCtx("editor"), wsID)
	result := systools.NewGetSkillTool(deps).Execute(ctx, map[string]any{"id": "db-migrate", "fields": []any{"description", "shelf"}})
	body := wu3Success(t, result)
	if body["shelf"] != "registry" || body["description"] != "The separate installed skill" {
		t.Fatalf("get_skill retargeted to project: %s", result.ForLLM)
	}
}

// Project skills have no T9 object mutation API. Keep actual directory deletion
// and confinement coverage at the existing project writer, not a fake router.
func TestProjectSkillWriter_DeletesOnlyMountSkill(t *testing.T) {
	home, mountRoot := t.TempDir(), t.TempDir()
	const wsID = "01SKILLPROJSHELF00000003"
	seedProjectShelfFixture(t, home, wsID, "acme", mountRoot, "db-migrate", "A project migration")
	skillRoot := filepath.Join(mountRoot, ".claude", "skills")
	operator := filepath.Join(mountRoot, "operator.txt")
	if err := os.WriteFile(operator, []byte("operator data"), 0o600); err != nil {
		t.Fatal(err)
	}
	writer := skills.NewSkillWriter(skillRoot)
	revision, err := writer.SkillRevision("db-migrate")
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.RemoveSkillReviewed("db-migrate", revision); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(skillRoot, "db-migrate")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project skill not removed: %v", err)
	}
	if string(wu3ReadFile(t, operator)) != "operator data" {
		t.Fatal("project deletion changed operator data")
	}
}
