package skills

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validFor(name string) string {
	// Build a valid SKILL.md whose frontmatter name matches name.
	return "---\nname: " + name + "\ndescription: A valid test skill with a sufficiently descriptive summary line.\n---\n\n# " + name + "\n\nThis skill does a thing.\n"
}

// TestSkillCreate_Versioned covers the create path and verifies that a
// subsequent edit snapshots the prior version (rollback recoverable).
//
// Traces to: Spec-6 BDD "Authoring a skill is consent-gated and versioned".
// (The consent gate itself is enforced by the agent-loop approval hook; this
// test exercises the write + versioning the tool performs once dispatched.)
func TestSkillCreate_Versioned(t *testing.T) {
	root := t.TempDir()
	w := NewSkillWriter(root)

	path, err := w.CreateSkill("my-skill", validFor("my-skill"))
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("created SKILL.md missing: %v", statErr)
	}

	// Creating the same skill again must be rejected.
	if _, err := w.CreateSkill("my-skill", validFor("my-skill")); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists on duplicate create, got %v", err)
	}

	// Versions are empty until the first edit.
	vers, err := w.ListVersions("my-skill")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vers) != 0 {
		t.Fatalf("expected 0 versions before edit, got %d", len(vers))
	}

	// Edit snapshots the prior version.
	edited := "---\nname: my-skill\ndescription: An edited description that is also long enough to be valid here.\n---\n\n# my-skill\n\nEdited body.\n"
	if _, _, err := w.EditSkill("my-skill", edited, false); err != nil {
		t.Fatalf("EditSkill: %v", err)
	}

	vers, err = w.ListVersions("my-skill")
	if err != nil {
		t.Fatalf("ListVersions after edit: %v", err)
	}
	if len(vers) != 1 {
		t.Fatalf("expected 1 snapshot after edit, got %d (%v)", len(vers), vers)
	}

	// The snapshot must hold the ORIGINAL content (rollback recoverable).
	snap, err := w.ReadVersion("my-skill", vers[0])
	if err != nil {
		t.Fatalf("ReadVersion: %v", err)
	}
	if !strings.Contains(snap, "This skill does a thing.") {
		t.Errorf("snapshot does not contain original body; got:\n%s", snap)
	}

	// The live file holds the edited content.
	live, err := os.ReadFile(filepath.Join(root, "my-skill", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(live), "Edited body.") {
		t.Errorf("live SKILL.md was not updated by edit")
	}
}

// TestSkillEdit_BuiltinOverride verifies FR-9.2 AC-2: editing a skill that
// exists only as a built-in produces a user override in the writer's root and
// never mutates the built-in source in place.
//
// Traces to: Spec-6 BDD "Editing a built-in creates an override".
func TestSkillEdit_BuiltinOverride(t *testing.T) {
	builtinRoot := t.TempDir()
	globalRoot := t.TempDir()

	// Stage a built-in skill the user does not have locally.
	builtinSkillDir := filepath.Join(builtinRoot, "briefing")
	if err := os.MkdirAll(builtinSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	builtinContent := validFor("briefing")
	if err := os.WriteFile(filepath.Join(builtinSkillDir, "SKILL.md"), []byte(builtinContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// The writer is rooted at the GLOBAL dir (as in production), so any write is
	// an override and the builtin is untouchable through it.
	w := NewSkillWriter(globalRoot)

	override := "---\nname: briefing\ndescription: An overridden briefing skill that the user has customised locally.\n---\n\n# briefing\n\nOverridden.\n"
	path, createdOverride, err := w.EditSkill("briefing", override, true /* allowCreateOverride: builtin exists */)
	if err != nil {
		t.Fatalf("EditSkill (override): %v", err)
	}
	if !createdOverride {
		t.Errorf("expected createdOverride=true when editing a builtin-only skill")
	}
	// The override must live under the global root, not the builtin root.
	if !strings.HasPrefix(path, globalRoot) {
		t.Errorf("override written outside global root: %s", path)
	}

	// The built-in source must be byte-for-byte unchanged.
	after, err := os.ReadFile(filepath.Join(builtinSkillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != builtinContent {
		t.Errorf("built-in SKILL.md was mutated in place — must remain unchanged")
	}

	// A loader with workspace>global>builtin priority must now resolve the override.
	loader := NewSkillsLoader("", globalRoot, builtinRoot)
	body, ok := loader.LoadSkill("briefing")
	if !ok {
		t.Fatal("loader could not load briefing after override")
	}
	if !strings.Contains(body, "Overridden.") {
		t.Errorf("loader returned builtin body, not the override")
	}

	// Editing a skill that exists NOWHERE without allowCreateOverride is ErrNotFound.
	if _, _, err := w.EditSkill("ghost", validFor("ghost"), false); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound editing non-existent skill, got %v", err)
	}
}

// TestSkillWrite_Confinement_TraversalAndOversize_Rejected covers TDD #11 / M-6:
// path traversal, oversize SKILL.md, and invalid frontmatter are all rejected.
//
// Traces to: Spec-6 BDD "consent-gated…" (M-6).
func TestSkillWrite_Confinement_TraversalAndOversize_Rejected(t *testing.T) {
	root := t.TempDir()
	w := NewSkillWriter(root)

	// 1. Path traversal via "..".
	traversalNames := []string{
		"../evil",
		"..",
		"foo/bar",
		"foo\\bar",
		"/etc/passwd",
		"a/../../b",
	}
	for _, name := range traversalNames {
		if _, err := w.CreateSkill(name, validFor("evil")); err == nil {
			t.Errorf("traversal name %q must be rejected, but CreateSkill succeeded", name)
		}
		// Confirm nothing escaped the root.
	}
	// Specifically assert the canonical ".." form maps to a confinement error.
	if _, err := w.CreateSkill("..", validFor("x")); err == nil {
		t.Errorf("'..' must be rejected")
	}

	// Nothing should have been written outside root.
	parent := filepath.Dir(root)
	if _, err := os.Stat(filepath.Join(parent, "evil")); err == nil {
		t.Errorf("traversal wrote a file outside the skills root")
	}

	// 2. Oversize SKILL.md.
	oversize := "---\nname: big\ndescription: ok and long enough to be valid here for sure.\n---\n\n# big\n\n" +
		strings.Repeat("A", MaxSkillMarkdownBytes+1)
	if _, err := w.CreateSkill("big", oversize); !errors.Is(err, ErrOversize) {
		t.Errorf("oversize SKILL.md must be ErrOversize, got %v", err)
	}

	// 3. Invalid frontmatter / missing required fields.
	invalids := map[string]string{
		"no-description": "---\nname: no-description\n---\n\n# no-description\n",
		"empty":          "",
		"name-mismatch":  "---\nname: somethingelse\ndescription: a description long enough to be valid for this test.\n---\n\n# x\n",
	}
	for skill, content := range invalids {
		if _, err := w.CreateSkill(skill, content); err == nil {
			t.Errorf("invalid SKILL.md %q must be rejected, but CreateSkill succeeded", skill)
		}
	}
}

// TestValidateSkillMarkdown_Valid sanity-checks a well-formed skill passes.
func TestValidateSkillMarkdown_Valid(t *testing.T) {
	if err := ValidateSkillMarkdown("good", validFor("good")); err != nil {
		t.Fatalf("valid SKILL.md rejected: %v", err)
	}
}
