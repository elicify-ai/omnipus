// Omnipus — integration tests for ADR-072 R3 fix: edit_skill/remove_skill
// resolving against the current workspace's mounted project skills before
// falling back to the central registry.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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

// TestSkillEditTool_ProjectShelf_WritesIntoMount is the R3 regression test
// for edit_skill: before this fix, edit_skill/create_skill/remove_skill never
// called skills.ResolveProjectSkillWriter (zero production callers) —
// confirmed live in
// docs/internal/qa/uat-report-skill-activation-batch2-groupD-2026-09-02.md
// (S27: edit_skill(name="db-migrate", ...) -> NOT_FOUND against a real
// mounted skill, mount file untouched). This drives the REAL tool
// (SkillEditTool.Execute, wired the way production wires it — via
// tools.WithWorkspaceID(ctx, wsID), the same context key the agent loop
// carries — against a real workspace/mount record and a real SKILL.md on
// disk) and proves the edit lands DIRECTLY in the mount, per D6.1 ("the
// write goes into that project's own file... it does not fork a copy into
// the central registry").
func TestSkillEditTool_ProjectShelf_WritesIntoMount(t *testing.T) {
	home := t.TempDir()
	mountRoot := t.TempDir()
	globalDir := t.TempDir() // the ordinary (registry) SkillWriter root

	const wsID = "01SKILLPROJSHELF00000001"
	seedProjectShelfFixture(t, home, wsID, "acme", mountRoot, "db-migrate", "Use when the user asks to run a database migration")

	deps := newAuthoringDeps(t, globalDir, "")
	deps.Home = home

	edit := systools.NewSkillEditTool(deps)
	ctx := tools.WithWorkspaceID(context.Background(), wsID)
	newContent := "---\nname: db-migrate\ndescription: EDITED-BY-R3-TEST, long enough to pass validation.\n---\n\nEdited body.\n"
	res := edit.Execute(ctx, map[string]any{"name": "db-migrate", "content": newContent})

	m := parseSuccess(t, res.ForLLM)
	if m["shelf"] != "project" {
		t.Fatalf("expected shelf=project, got %v (resp=%s)", m["shelf"], res.ForLLM)
	}
	if m["mount"] != "acme" {
		t.Fatalf("expected mount=acme, got %v (resp=%s)", m["mount"], res.ForLLM)
	}
	if m["action"] != "edited" {
		t.Fatalf("expected action=edited, got %v (resp=%s)", m["action"], res.ForLLM)
	}

	// The mount's own file must carry the new content.
	mountSkillPath := filepath.Join(mountRoot, ".claude", "skills", "db-migrate", "SKILL.md")
	got, err := os.ReadFile(mountSkillPath)
	if err != nil {
		t.Fatalf("read mount SKILL.md: %v", err)
	}
	if string(got) != newContent {
		t.Fatalf("mount SKILL.md not updated:\ngot:  %q\nwant: %q", string(got), newContent)
	}

	// D6.1: no shadow copy in the central registry.
	if _, err := os.Stat(filepath.Join(globalDir, "db-migrate")); !os.IsNotExist(err) {
		t.Fatalf("edit_skill must NOT fork a copy into the central registry; found err=%v", err)
	}
}

// TestSkillEditTool_ProjectShelf_UnmountedSlugFallsThroughToRegistry proves
// the fallback direction still works: a slug absent from the current
// workspace's project shelf resolves through the ordinary global-root path
// exactly as it did before this fix.
func TestSkillEditTool_ProjectShelf_UnmountedSlugFallsThroughToRegistry(t *testing.T) {
	home := t.TempDir()
	mountRoot := t.TempDir()
	globalDir := t.TempDir()

	const wsID = "01SKILLPROJSHELF00000002"
	seedProjectShelfFixture(t, home, wsID, "acme", mountRoot, "db-migrate", "Use when the user asks to run a database migration")

	deps := newAuthoringDeps(t, globalDir, "")
	deps.Home = home

	create := systools.NewSkillCreateTool(deps)
	ctx := tools.WithWorkspaceID(context.Background(), wsID)
	createRes := create.Execute(ctx, map[string]any{
		"name":    "unrelated-skill",
		"content": validSkill("unrelated-skill"),
	})
	cm := parseSuccess(t, createRes.ForLLM)
	if cm["action"] != "created" {
		t.Fatalf("expected create to succeed via the registry path, got resp=%s", createRes.ForLLM)
	}

	edit := systools.NewSkillEditTool(deps)
	res := edit.Execute(ctx, map[string]any{
		"name":    "unrelated-skill",
		"content": "---\nname: unrelated-skill\ndescription: Edited via the registry path, long enough to pass.\n---\n\nEdited.\n",
	})
	m := parseSuccess(t, res.ForLLM)
	if _, hasShelf := m["shelf"]; hasShelf {
		t.Fatalf("a non-project skill must not carry a shelf=project tag; resp=%s", res.ForLLM)
	}
	if m["action"] != "edited" {
		t.Fatalf("expected action=edited via the registry path, got %v (resp=%s)", m["action"], res.ForLLM)
	}
	if _, err := os.Stat(filepath.Join(globalDir, "unrelated-skill", "SKILL.md")); err != nil {
		t.Fatalf("registry-shelf edit must still land in the global dir: %v", err)
	}
}

// TestSkillRemoveTool_ProjectShelf_DeletesMountFile is the R3 regression test
// for remove_skill (D6.1: "remove_skill follows the same rule — deleting a
// project skill deletes the project's file"). Before this fix, remove_skill
// only ever called deps.SkillInstaller.Uninstall against the central
// registry, so a mounted project skill returned NOT_FOUND
// (docs/internal/qa/uat-report-skill-activation-batch2-groupD-2026-09-02.md
// S29) even though it plainly existed on disk.
func TestSkillRemoveTool_ProjectShelf_DeletesMountFile(t *testing.T) {
	home := t.TempDir()
	mountRoot := t.TempDir()

	const wsID = "01SKILLPROJSHELF00000003"
	seedProjectShelfFixture(t, home, wsID, "acme", mountRoot, "db-migrate", "Use when the user asks to run a database migration")

	deps, _ := newTestDeps()
	deps.Home = home
	// remove_skill's project-shelf branch does not need SkillInstaller at
	// all — deliberately leave it nil to prove that.

	remove := systools.NewSkillRemoveTool(deps)
	ctx := tools.WithWorkspaceID(context.Background(), wsID)
	res := remove.Execute(ctx, map[string]any{"name": "db-migrate", "confirm": true})

	m := parseSuccess(t, res.ForLLM)
	if m["shelf"] != "project" {
		t.Fatalf("expected shelf=project, got %v (resp=%s)", m["shelf"], res.ForLLM)
	}
	if m["mount"] != "acme" {
		t.Fatalf("expected mount=acme, got %v (resp=%s)", m["mount"], res.ForLLM)
	}

	mountSkillDir := filepath.Join(mountRoot, ".claude", "skills", "db-migrate")
	if _, err := os.Stat(mountSkillDir); !os.IsNotExist(err) {
		t.Fatalf("expected the mount's own skill directory to be removed, stat err=%v", err)
	}
}
