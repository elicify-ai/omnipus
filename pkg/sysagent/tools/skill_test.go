// Omnipus — System Agent Skill Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// newDepsWithSkillsLoader builds a Deps with a real SkillsLoader rooted in a
// temp dir. The caller populates the temp dir with skill fixtures as needed.
func newDepsWithSkillsLoader(t *testing.T, globalSkillsDir string) (*systools.Deps, string) {
	t.Helper()
	home := t.TempDir()
	loader := skills.NewSkillsLoader("", globalSkillsDir, "")
	deps, _ := newTestDeps()
	deps.Home = home
	deps.SkillsLoader = loader
	return deps, home
}

// ---- list_skills ----

// BDD: Given SkillsLoader is nil (not wired),
//
//	When the tool is executed,
//	Then it returns NOT_AVAILABLE error.
func TestSkillListTool_NilLoader_ReturnsNotAvailable(t *testing.T) {
	deps, _ := newTestDeps()
	// Do NOT set deps.SkillsLoader — it is nil.
	tool := systools.NewSkillListTool(deps)
	result := tool.Execute(context.Background(), nil)
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock == nil {
		t.Fatalf("expected error block in response: %s", result.ForLLM)
	}
	if code, _ := errBlock["code"].(string); code != "NOT_AVAILABLE" {
		t.Errorf("expected NOT_AVAILABLE, got %q", code)
	}
}

// BDD: Given a SkillsLoader with no installed skills,
//
//	When the tool is executed,
//	Then it returns an empty skills list with count=0.
func TestSkillListTool_EmptyDir_ReturnsEmptyList(t *testing.T) {
	empty := t.TempDir()
	deps, _ := newDepsWithSkillsLoader(t, empty)
	tool := systools.NewSkillListTool(deps)
	result := tool.Execute(context.Background(), nil)
	m := parseSuccess(t, result.ForLLM)
	skillsList, _ := m["skills"].([]any)
	if len(skillsList) != 0 {
		t.Errorf("expected empty skills list, got %d items", len(skillsList))
	}
	if count, _ := m["count"].(float64); count != 0 {
		t.Errorf("expected count=0, got %v", m["count"])
	}
}

// BDD: Given a SkillsLoader with one installed skill (valid SKILL.md),
//
//	When the tool is executed,
//	Then the skill appears in the list with its name and description.
func TestSkillListTool_WithInstalledSkill_ReturnsList(t *testing.T) {
	globalDir := t.TempDir()
	// Create a skill dir with a SKILL.md file.
	skillDir := filepath.Join(globalDir, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillMD := "---\nname: my-skill\ndescription: A test skill\n---\n# my-skill\n\nA test skill.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}

	deps, _ := newDepsWithSkillsLoader(t, globalDir)
	tool := systools.NewSkillListTool(deps)
	result := tool.Execute(context.Background(), nil)
	m := parseSuccess(t, result.ForLLM)
	skillsList, _ := m["skills"].([]any)
	if len(skillsList) == 0 {
		t.Fatalf("expected at least one skill, got empty list. response: %s", result.ForLLM)
	}
	first, _ := skillsList[0].(map[string]any)
	if first == nil {
		t.Fatalf("skills[0] is not a map: %v", skillsList[0])
	}
	if name, _ := first["name"].(string); name != "my-skill" {
		t.Errorf("expected name=my-skill, got %q", name)
	}
}

// ---- remove_skill ----

// BDD: Given SkillInstaller is nil (not wired),
//
//	When the tool is executed with name+confirm,
//	Then it returns NOT_AVAILABLE.
func TestSkillRemoveTool_NilInstaller_ReturnsNotAvailable(t *testing.T) {
	deps, _ := newTestDeps()
	tool := systools.NewSkillRemoveTool(deps)
	result := tool.Execute(context.Background(), map[string]any{
		"name":    "my-skill",
		"confirm": true,
	})
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if code, _ := errBlock["code"].(string); code != "NOT_AVAILABLE" {
		t.Errorf("expected NOT_AVAILABLE, got %q", code)
	}
}

// BDD: Given confirm is false,
//
//	When the tool is executed,
//	Then it returns CONFIRMATION_REQUIRED.
func TestSkillRemoveTool_NotConfirmed_ReturnsConfirmationRequired(t *testing.T) {
	deps, _ := newTestDeps()
	tool := systools.NewSkillRemoveTool(deps)
	result := tool.Execute(context.Background(), map[string]any{
		"name":    "my-skill",
		"confirm": false,
	})
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if code, _ := errBlock["code"].(string); code != "CONFIRMATION_REQUIRED" {
		t.Errorf("expected CONFIRMATION_REQUIRED, got %q", code)
	}
}

// BDD: Given a real SkillInstaller and an installed skill,
//
//	When the tool is executed with confirm=true,
//	Then it successfully removes the skill and returns success=true.
func TestSkillRemoveTool_InstalledSkill_RemovesAndReturnsSuccess(t *testing.T) {
	workspace := t.TempDir()
	// Create a fake installed skill in the workspace.
	skillDir := filepath.Join(workspace, "skills", "test-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("# test-skill\n\nA test skill.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	installer, err := skills.NewSkillInstaller(workspace, "", "")
	if err != nil {
		t.Fatalf("NewSkillInstaller: %v", err)
	}
	deps, _ := newTestDeps()
	deps.SkillInstaller = installer

	tool := systools.NewSkillRemoveTool(deps)
	result := tool.Execute(context.Background(), map[string]any{
		"name":    "test-skill",
		"confirm": true,
	})
	m := parseSuccess(t, result.ForLLM)
	if success, _ := m["success"].(bool); !success {
		t.Errorf("expected success=true, got: %s", result.ForLLM)
	}
	if name, _ := m["name"].(string); name != "test-skill" {
		t.Errorf("expected name=test-skill, got %q", name)
	}
	// Verify the skill directory was actually removed.
	if _, err := os.Stat(skillDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected skill directory to be removed, but it still exists")
	}
}

// BDD: Given a skill that is not installed,
//
//	When the remove tool is executed with confirm=true,
//	Then it returns NOT_FOUND.
func TestSkillRemoveTool_NotInstalled_ReturnsNotFound(t *testing.T) {
	workspace := t.TempDir()
	installer, err := skills.NewSkillInstaller(workspace, "", "")
	if err != nil {
		t.Fatalf("NewSkillInstaller: %v", err)
	}
	deps, _ := newTestDeps()
	deps.SkillInstaller = installer

	tool := systools.NewSkillRemoveTool(deps)
	result := tool.Execute(context.Background(), map[string]any{
		"name":    "nonexistent-skill",
		"confirm": true,
	})
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if code, _ := errBlock["code"].(string); code != "NOT_FOUND" {
		t.Errorf("expected NOT_FOUND, got %q in: %s", code, result.ForLLM)
	}
}

// ---- no-stub regression ----

// BDD: Given any active skill tool,
//
//	When its ForLLM response is examined,
//	Then it must not contain the string "stub".
//
// This is the zero-tolerance test: any leftover stub response is a build failure.
// Note: system.skill.install and system.skill.search are retired (§7); only the
// active tools (list_skills, remove_skill) are tested here.
func TestSkillTools_NoStubStrings(t *testing.T) {
	workspace := t.TempDir()
	installer, _ := skills.NewSkillInstaller(workspace, "", "")
	loader := skills.NewSkillsLoader(workspace, t.TempDir(), "")

	deps, _ := newTestDeps()
	deps.SkillInstaller = installer
	deps.SkillsLoader = loader

	ctx := context.Background()

	// List tool.
	listResult := systools.NewSkillListTool(deps).Execute(ctx, nil)
	if strings.Contains(listResult.ForLLM, "stub") {
		t.Errorf("list_skills result contains 'stub': %s", listResult.ForLLM)
	}

	// Remove tool with nonexistent skill → NOT_FOUND (real error, not stub).
	removeResult := systools.NewSkillRemoveTool(deps).Execute(ctx, map[string]any{
		"name":    "no-such-skill",
		"confirm": true,
	})
	if strings.Contains(removeResult.ForLLM, "stub") {
		t.Errorf("remove_skill result contains 'stub': %s", removeResult.ForLLM)
	}
}
