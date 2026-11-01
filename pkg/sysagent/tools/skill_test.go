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
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newDepsWithSkillsLoader builds a Deps with a real SkillsLoader rooted in a
// temp dir. The caller populates the temp dir with skill fixtures as needed.
func newDepsWithSkillsLoader(t *testing.T, globalSkillsDir string) (*systools.Deps, string) {
	t.Helper()
	home := t.TempDir()
	loader := skills.NewSkillsLoader("", globalSkillsDir, "")
	deps, _ := newTestDeps(t)
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
	deps, _ := newTestDeps(t)
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
	deps.ResolveToolPolicy = func(_, _ string) (string, bool) { return "allow", true }
	result := tool.Execute(readCtx("editor"), map[string]any{"scope": "management"})
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
	deps.ResolveToolPolicy = func(_, _ string) (string, bool) { return "allow", true }
	result := tool.Execute(readCtx("editor"), map[string]any{"scope": "management"})
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

// TestSkillListTool_FiltersByGrantAndOmitsPath covers ADR-072 D4/N1 (spec
// #28, FR-025): once the acting agent is resolvable (an agent id on ctx that
// matches a real entry in the live config), list_skills must exclude any
// installed skill the agent's grant list does not name, AND no returned
// entry — granted or not — may carry a "path" key at all.
func TestSkillListTool_FiltersByGrantAndOmitsPath(t *testing.T) {
	globalDir := t.TempDir()
	granted := filepath.Join(globalDir, "granted-skill")
	if err := os.MkdirAll(granted, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(granted, "SKILL.md"),
		[]byte("---\nname: granted-skill\ndescription: the one this agent may use\n---\n# granted-skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ungranted := filepath.Join(globalDir, "ungranted-skill")
	if err := os.MkdirAll(ungranted, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ungranted, "SKILL.md"),
		[]byte("---\nname: ungranted-skill\ndescription: installed but never granted\n---\n# ungranted-skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// r15 §5.13a: a real generation binding, not just a same-name grant.
	deps := newSkillDeps(t, globalDir, skillDepsOpts{agentID: "jim", grants: []string{"granted-skill"}, policies: map[string]string{"list_skills": "allow"}})

	tool := systools.NewSkillListTool(deps)
	ctx := tools.WithAgentID(context.Background(), "jim")
	result := tool.Execute(ctx, nil)

	m := parseSuccess(t, result.ForLLM)
	skillsList, _ := m["skills"].([]any)
	if len(skillsList) != 1 {
		t.Fatalf("expected exactly 1 granted skill, got %d: %s", len(skillsList), result.ForLLM)
	}
	first, _ := skillsList[0].(map[string]any)
	if id, _ := first["id"].(string); id != "granted-skill" {
		t.Errorf("expected the granted skill, got id=%q", id)
	}
	if _, hasPath := first["path"]; hasPath {
		t.Errorf("list_skills must never return a \"path\" field, got: %v", first)
	}
	if strings.Contains(result.ForLLM, "ungranted-skill") {
		t.Errorf("the ungranted skill must not appear anywhere in the response: %s", result.ForLLM)
	}
}

// TestSkillListTool_UnresolvableAgentDeniesEverything is the D5 companion:
// an agent id that IS present on ctx but does not resolve to any entry in
// the live config roster is granted nothing, rather than falling open.
func TestSkillListTool_UnresolvableAgentDeniesEverything(t *testing.T) {
	globalDir := t.TempDir()
	deps, _ := newTestDeps(t)
	deps.SkillsLoader = skills.NewSkillsLoader("", globalDir, "")
	if err := os.MkdirAll(filepath.Join(globalDir, "some-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "some-skill", "SKILL.md"),
		[]byte("---\nname: some-skill\ndescription: an installed skill\n---\n# some-skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := systools.NewSkillListTool(deps)
	ctx := tools.WithAgentID(context.Background(), "no-such-agent")
	result := tool.Execute(ctx, nil)

	m := parseSuccess(t, result.ForLLM)
	skillsList, _ := m["skills"].([]any)
	if len(skillsList) != 0 {
		t.Fatalf("expected an unresolvable agent id to be granted nothing, got %d skills: %s",
			len(skillsList), result.ForLLM)
	}
}

// ---- r15 §5.4: deletion is edit_skill action=delete ----

func TestSkillDeleteTool_NilDeps_ReturnsNotAvailable(t *testing.T) {
	result := systools.NewSkillEditTool(nil).Execute(context.Background(), map[string]any{"action": "delete", "skill": "my-skill"})
	wu3Error(t, result, "NOT_AVAILABLE")
}

// The old confirm boolean is withdrawn. The live safety precondition is a
// supported read in this session (§7.3), not an invented approval token.
func TestSkillDeleteTool_NotRead_ReturnsSkillNotRead(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	validSkillFixture(t, root, "my-skill", "A skill to protect")
	deps := newAuthoringDeps(t, root, "")
	before := wu3ReadFile(t, filepath.Join(root, "my-skill", "SKILL.md"))
	result := systools.NewSkillEditTool(deps).Execute(readCtx("editor"), map[string]any{"action": "delete", "skill": "my-skill"})
	body := wu3Error(t, result, "SKILL_NOT_READ")
	if body["message"] != "read skill 'my-skill' first (Skill, get_skill, read_file or list_directory), then retry" {
		t.Fatalf("wrong read remedy: %s", result.ForLLM)
	}
	if string(wu3ReadFile(t, filepath.Join(root, "my-skill", "SKILL.md"))) != string(before) {
		t.Fatal("unread deletion changed skill bytes")
	}
}

func TestSkillDeleteTool_InstalledSkill_RemovesAndReturnsSuccess(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	validSkillFixture(t, root, "test-skill", "A skill to delete")
	deps := newAuthoringDeps(t, root, "")
	ctx := observeSkillForMutation(t, deps, readCtx("editor"), "test-skill")
	result := systools.NewSkillEditTool(deps).Execute(ctx, map[string]any{"action": "delete", "skill": "test-skill"})
	m := wu3Success(t, result)
	if m["success"] != true || m["skill"] != "test-skill" || m["action"] != "delete" {
		t.Fatalf("wrong successful deletion: %s", result.ForLLM)
	}
	if _, err := os.Stat(filepath.Join(root, "test-skill")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("skill still exists: %v", err)
	}
}

// §5.2: an authorized missing-object read says NOT_FOUND. A delete without
// a successful observation says SKILL_NOT_READ; no fake absent revision.
func TestSkillDeleteTool_NotInstalled_ReadNotFoundAndDeleteNotRead(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	deps := newAuthoringDeps(t, root, "")
	deps.ResolveToolPolicy = func(_, name string) (string, bool) { return "allow", true }
	ctx := readCtx("editor")
	read := systools.NewGetSkillTool(deps).Execute(ctx, map[string]any{"id": "nonexistent-skill", "fields": []any{"state"}})
	body := wu3Error(t, read, "NOT_FOUND")
	if body["message"] != `skill "nonexistent-skill" is not installed` {
		t.Fatalf("wrong missing-object remedy: %s", read.ForLLM)
	}
	result := systools.NewSkillEditTool(deps).Execute(ctx, map[string]any{"action": "delete", "skill": "nonexistent-skill"})
	wu3Error(t, result, "SKILL_NOT_READ")
	if _, err := os.Stat(filepath.Join(root, "nonexistent-skill")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing delete materialized skill: %v", err)
	}
}

// Active tool results must be real success/refusal objects, never scaffold text.
func TestSkillTools_NoStubStrings(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	deps := newAuthoringDeps(t, root, "")
	deps.ResolveToolPolicy = func(_, _ string) (string, bool) { return "allow", true }
	ctx := readCtx("editor")
	results := map[string]*tools.ToolResult{
		"list_skills": systools.NewSkillListTool(deps).Execute(ctx, map[string]any{"scope": "management"}),
		"get_skill":   systools.NewGetSkillTool(deps).Execute(ctx, map[string]any{"id": "no-such-skill", "fields": []any{"state"}}),
		"edit_skill":  systools.NewSkillEditTool(deps).Execute(ctx, map[string]any{"action": "delete", "skill": "no-such-skill"}),
	}
	wu3Success(t, results["list_skills"])
	wu3Error(t, results["get_skill"], "NOT_FOUND")
	wu3Error(t, results["edit_skill"], "SKILL_NOT_READ")
	for name, result := range results {
		if strings.Contains(strings.ToLower(result.ForLLM), "stub") {
			t.Errorf("%s result contains stub: %s", name, result.ForLLM)
		}
	}
}
