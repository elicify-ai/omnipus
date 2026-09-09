// Omnipus — ContextBuilder.ResolveSkillName shelf-awareness tests
// (ADR-072 D4 item 4, spec gap G3/A3)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
)

// TestSlashCommand_DeniesUngrantedSlug covers ADR-072 D4's slash-command door
// (spec #29): the /<skill> human shortcut resolves through
// ContextBuilder.ResolveSkillName, which must refuse a registry slug the
// acting agent's allowlist does not grant — a person cannot push a skill
// onto an agent that is not permitted it.
func TestSlashCommand_DeniesUngrantedSlug(t *testing.T) {
	workspace := t.TempDir()
	writeSkill(t, workspace, "granted-skill")
	writeSkill(t, workspace, "ungranted-skill")

	cb := NewContextBuilder(workspace).WithSkillAllowlist([]string{"granted-skill"})

	if got, ok := cb.ResolveSkillName("granted-skill"); !ok || got != "granted-skill" {
		t.Fatalf("the granted skill must resolve: got (%q, %v)", got, ok)
	}
	if _, ok := cb.ResolveSkillName("ungranted-skill"); ok {
		t.Fatal("an installed-but-ungranted skill must NOT resolve via the slash-command door")
	}
}

// TestSlashCommand_AllowsProjectSlugInWorkspace covers ADR-072 D4.1's
// per-shelf model applied to the same slash-command door (spec #30, gap
// G3/A3): a project skill discovered only in the current workspace's mounted
// skills must resolve for the human "/<slug>" shortcut with NO registry
// grant at all — the mount is the grant (D4.1), and a human must not have
// LESS access than the agent already has via the Skill tool.
func TestSlashCommand_AllowsProjectSlugInWorkspace(t *testing.T) {
	workspace := t.TempDir()

	mountRoot := t.TempDir()
	skillDir := filepath.Join(mountRoot, ".claude", "skills", "onboarding")
	writeProjectSkillFile(t, skillDir, "onboarding", "Use when onboarding a new teammate")

	projectShelf, collisions := skills.MergeProjectSkills([]skills.ProjectMount{{Name: "acme", Root: mountRoot}})
	if len(collisions) != 0 {
		t.Fatalf("expected no collisions, got %v", collisions)
	}

	// Empty (non-nil) registry allowlist: this agent holds NO registry/builtin
	// grant whatsoever.
	cb := NewContextBuilder(workspace).WithSkillAllowlist([]string{}).WithProjectShelf(projectShelf)

	got, ok := cb.ResolveSkillName("onboarding")
	if !ok {
		t.Fatal("a project skill must resolve via the slash-command door with no registry grant at all")
	}
	if got != "onboarding" {
		t.Fatalf("expected canonical slug %q, got %q", "onboarding", got)
	}
}

// TestSlashCommand_ProjectSlugDoesNotFollowToAnotherContextBuilder is the
// negative half of the same scenario: a ContextBuilder with no project shelf
// wired (e.g. the same agent acting in a DIFFERENT workspace with no mount of
// its own) must not resolve a slug that only exists on some other
// workspace's project shelf.
func TestSlashCommand_ProjectSlugDoesNotFollowToAnotherContextBuilder(t *testing.T) {
	workspace := t.TempDir()
	cb := NewContextBuilder(workspace).WithSkillAllowlist([]string{})
	// No WithProjectShelf call at all — projectShelf stays nil.

	if _, ok := cb.ResolveSkillName("onboarding"); ok {
		t.Fatal("a project skill must not resolve for a ContextBuilder with no project shelf wired")
	}
}

// writeProjectSkillFile creates <dir>/SKILL.md — dir is already the
// per-slug directory (mirrors the shape DiscoverProjectSkills expects:
// <recognised-skills-dir>/<slug>/SKILL.md).
func writeProjectSkillFile(t *testing.T, dir, slug, description string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + slug + "\ndescription: " + description + "\n---\n\n# " + slug + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
