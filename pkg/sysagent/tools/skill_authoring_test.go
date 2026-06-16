// Omnipus — System Agent Skill Authoring Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/skills"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
)

func validSkill(name string) string {
	return "---\nname: " + name + "\ndescription: A valid authored skill with a long enough description to pass.\n---\n\n# " + name + "\n\nDoes the thing.\n"
}

// newAuthoringDeps wires a Deps with a SkillWriter rooted at a temp global dir
// and a SkillsLoader spanning an optional builtin dir.
func newAuthoringDeps(t *testing.T, globalDir, builtinDir string) *systools.Deps {
	t.Helper()
	deps, _ := newTestDeps()
	deps.SkillWriter = skills.NewSkillWriter(globalDir)
	deps.SkillsLoader = skills.NewSkillsLoader("", globalDir, builtinDir)
	return deps
}

// TestSkillCreateTool_WritesAndVersions verifies system.skill.create writes a
// real SKILL.md and that a subsequent system.skill.edit snapshots the prior
// version (consent-gated + versioned; the consent gate is the loop approval
// hook, exercised by policy "ask" — here we verify the write/versioning path).
func TestSkillCreateTool_WritesAndVersions(t *testing.T) {
	globalDir := t.TempDir()
	deps := newAuthoringDeps(t, globalDir, "")

	create := systools.NewSkillCreateTool(deps)
	res := create.Execute(context.Background(), map[string]any{
		"name":    "my-skill",
		"content": validSkill("my-skill"),
	})
	m := parseSuccess(t, res.ForLLM)
	if m["action"] != "created" {
		t.Fatalf("expected action=created, got %v (resp=%s)", m["action"], res.ForLLM)
	}
	if _, err := os.Stat(filepath.Join(globalDir, "my-skill", "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not written: %v", err)
	}

	// Edit it — should snapshot the prior version.
	edit := systools.NewSkillEditTool(deps)
	editRes := edit.Execute(context.Background(), map[string]any{
		"name":    "my-skill",
		"content": "---\nname: my-skill\ndescription: Edited description that is also long enough to be valid here.\n---\n\n# my-skill\n\nEdited.\n",
	})
	em := parseSuccess(t, editRes.ForLLM)
	if em["action"] != "edited" {
		t.Fatalf("expected action=edited, got %v (resp=%s)", em["action"], editRes.ForLLM)
	}
	w := skills.NewSkillWriter(globalDir)
	vers, err := w.ListVersions("my-skill")
	if err != nil || len(vers) != 1 {
		t.Fatalf("expected 1 version snapshot, got %d err=%v", len(vers), err)
	}
}

// TestSkillEditTool_BuiltinOverride verifies that editing a built-in via the
// tool creates a user override and does not mutate the built-in.
func TestSkillEditTool_BuiltinOverride(t *testing.T) {
	globalDir := t.TempDir()
	builtinDir := t.TempDir()

	bdir := filepath.Join(builtinDir, "plan")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	builtin := validSkill("plan")
	if err := os.WriteFile(filepath.Join(bdir, "SKILL.md"), []byte(builtin), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := newAuthoringDeps(t, globalDir, builtinDir)
	edit := systools.NewSkillEditTool(deps)
	res := edit.Execute(context.Background(), map[string]any{
		"name":    "plan",
		"content": "---\nname: plan\ndescription: An overridden plan skill customised by the user for this test.\n---\n\n# plan\n\nOverride.\n",
	})
	m := parseSuccess(t, res.ForLLM)
	if m["action"] != "override_created" {
		t.Fatalf("expected action=override_created, got %v (resp=%s)", m["action"], res.ForLLM)
	}

	// Built-in unchanged.
	after, _ := os.ReadFile(filepath.Join(bdir, "SKILL.md"))
	if string(after) != builtin {
		t.Errorf("built-in was mutated in place")
	}
	// Override written under global dir.
	if _, err := os.Stat(filepath.Join(globalDir, "plan", "SKILL.md")); err != nil {
		t.Errorf("override not written to global dir: %v", err)
	}
}

// TestSkillCreateTool_TraversalRejected verifies path-traversal names are
// rejected with INVALID_INPUT and nothing is written.
func TestSkillCreateTool_TraversalRejected(t *testing.T) {
	globalDir := t.TempDir()
	deps := newAuthoringDeps(t, globalDir, "")
	create := systools.NewSkillCreateTool(deps)

	res := create.Execute(context.Background(), map[string]any{
		"name":    "../escape",
		"content": validSkill("escape"),
	})
	em := parseError(t, res.ForLLM)
	errBlock, _ := em["error"].(map[string]any)
	if errBlock == nil || errBlock["code"] != "INVALID_INPUT" {
		t.Fatalf("expected INVALID_INPUT for traversal, got %s", res.ForLLM)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(globalDir), "escape")); err == nil {
		t.Errorf("traversal escaped the skills root")
	}
}

// TestSkillCreateTool_OversizeRejected verifies an oversize SKILL.md is rejected.
func TestSkillCreateTool_OversizeRejected(t *testing.T) {
	globalDir := t.TempDir()
	deps := newAuthoringDeps(t, globalDir, "")
	create := systools.NewSkillCreateTool(deps)

	oversize := "---\nname: big\ndescription: ok and long enough to be valid for this test indeed.\n---\n\n# big\n\n" +
		strings.Repeat("A", skills.MaxSkillMarkdownBytes+1)
	res := create.Execute(context.Background(), map[string]any{"name": "big", "content": oversize})
	em := parseError(t, res.ForLLM)
	errBlock, _ := em["error"].(map[string]any)
	if errBlock == nil || errBlock["code"] != "INVALID_INPUT" {
		t.Fatalf("expected INVALID_INPUT for oversize, got %s", res.ForLLM)
	}
}

// TestSkillCreateTool_NilWriter_ReturnsNotAvailable verifies the nil-dep guard.
func TestSkillCreateTool_NilWriter_ReturnsNotAvailable(t *testing.T) {
	deps, _ := newTestDeps() // no SkillWriter
	create := systools.NewSkillCreateTool(deps)
	res := create.Execute(context.Background(), map[string]any{"name": "x", "content": validSkill("x")})
	em := parseError(t, res.ForLLM)
	errBlock, _ := em["error"].(map[string]any)
	if errBlock == nil || errBlock["code"] != "NOT_AVAILABLE" {
		t.Fatalf("expected NOT_AVAILABLE, got %s", res.ForLLM)
	}
}
