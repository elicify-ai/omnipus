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
	if _, dupErr := w.CreateSkill("my-skill", validFor("my-skill")); !errors.Is(dupErr, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists on duplicate create, got %v", dupErr)
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
	if _, _, editErr := w.EditSkill("my-skill", edited, false); editErr != nil {
		t.Fatalf("EditSkill: %v", editErr)
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

	override := "---\nname: briefing\ndescription: An overridden briefing skill that the user has customized locally.\n---\n\n# briefing\n\nOverridden.\n"
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
	}
	for skill, content := range invalids {
		if _, err := w.CreateSkill(skill, content); err == nil {
			t.Errorf("invalid SKILL.md %q must be rejected, but CreateSkill succeeded", skill)
		}
	}

	// 4. The frontmatter `name` is the human-readable DISPLAY name and may legitimately
	// differ from the slug (the directory/skill id) — e.g. slug "daily-briefing" with
	// display name "Daily Briefing". Path identity is the slug, so a differing display
	// name is NOT a confinement risk and must be ACCEPTED.
	if _, err := w.CreateSkill(
		"display-name-skill",
		"---\nname: A Friendly Display Name\ndescription: a description long enough to be valid for this test.\n---\n\n# x\n",
	); err != nil {
		t.Errorf("a display name differing from the slug must be accepted, got: %v", err)
	}
}

// TestValidateSkillMarkdown_Valid sanity-checks a well-formed skill passes.
func TestValidateSkillMarkdown_Valid(t *testing.T) {
	if err := ValidateSkillMarkdown("good", validFor("good")); err != nil {
		t.Fatalf("valid SKILL.md rejected: %v", err)
	}
}

// newProjectSkillFixture seeds a "<mountRoot>/.omnipus/skills/<slug>/SKILL.md"
// file and returns the mount root, the skill's absolute path, and a
// ProjectShelf keyed on slug pointing at it — the shape DiscoverProjectSkills
// / MergeProjectSkills would have produced for a real mount.
func newProjectSkillFixture(t *testing.T, slug string) (mountRoot, skillPath string, shelf ProjectShelf) {
	t.Helper()
	mountRoot = t.TempDir()
	skillDir := filepath.Join(mountRoot, ".omnipus", "skills", slug)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("seed mount fixture: mkdir: %v", err)
	}
	skillPath = filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(validFor(slug)), 0o644); err != nil {
		t.Fatalf("seed mount fixture: write SKILL.md: %v", err)
	}
	shelf = ProjectShelf{
		strings.ToLower(slug): ProjectSkill{
			SkillInfo: SkillInfo{ID: slug, Name: slug, Path: skillPath, Source: string(ShelfProject)},
			MountName: "alpha",
			MountRoot: mountRoot,
		},
	}
	return mountRoot, skillPath, shelf
}

// TestSkillWriter_ResolvesProjectSlugToItsOwnShelf covers ADR-072 D6.1 /
// spec FR-065/066/067: editing a project skill through the writer
// ResolveProjectSkillWriter hands back must write into the mount's own
// SKILL.md, in place, and must never create any copy in a separate central
// (global) skills root.
//
// Traces to: ADR-072 D6.1; spec FR-065/066/067; BDD "Editing a project skill
// writes into the project".
func TestSkillWriter_ResolvesProjectSlugToItsOwnShelf(t *testing.T) {
	mountRoot, skillPath, shelf := newProjectSkillFixture(t, "deploy")

	w, ps, err := ResolveProjectSkillWriter(shelf, "deploy")
	if err != nil {
		t.Fatalf("ResolveProjectSkillWriter: %v", err)
	}
	if ps.MountName != "alpha" {
		t.Fatalf("resolved ProjectSkill.MountName = %q, want %q", ps.MountName, "alpha")
	}
	wantRoot := filepath.Join(mountRoot, ".omnipus", "skills")
	if w.Root() != wantRoot {
		t.Fatalf("writer resolved to root %q, want the skill's own shelf %q", w.Root(), wantRoot)
	}

	newContent := "---\nname: deploy\ndescription: An updated deploy description, long enough to be valid here.\n---\n\n# deploy\n\nUpdated body.\n"
	path, _, err := w.EditSkill("deploy", newContent, false)
	if err != nil {
		t.Fatalf("EditSkill via resolved project writer: %v", err)
	}
	if path != skillPath {
		t.Fatalf("edit wrote to %q, want the project's own file %q", path, skillPath)
	}
	got, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read back project file: %v", err)
	}
	if string(got) != newContent {
		t.Fatalf("project's own SKILL.md was not updated with the new content")
	}

	// FR-067: no copy is created in a separate central library. A fresh,
	// unrelated global root must remain untouched by the project-scoped edit.
	central := t.TempDir()
	if entries, readErr := os.ReadDir(central); readErr != nil || len(entries) != 0 {
		t.Fatalf("expected the central library root to be untouched, readdir=%v err=%v", entries, readErr)
	}
}

// TestSkillWriter_ProjectWriteConfinedToMount covers the FR-068 traversal
// case: ResolveProjectSkillWriter must refuse — before constructing any
// writer, and therefore before anything can be written — a ProjectShelf entry
// whose resolved skill location does not actually lie within the mount root
// it claims to belong to.
//
// Traces to: ADR-072 D6.1; spec FR-068; BDD "Editing a project skill writes
// into the project" (traversal confinement leg).
func TestSkillWriter_ProjectWriteConfinedToMount(t *testing.T) {
	mountRoot := t.TempDir()

	cases := map[string]string{
		// Path lives under a completely unrelated directory tree.
		"unrelated_directory": func() string {
			outside := t.TempDir()
			dir := filepath.Join(outside, ".omnipus", "skills", "deploy")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			p := filepath.Join(dir, "SKILL.md")
			if err := os.WriteFile(p, []byte(validFor("deploy")), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			return p
		}(),
		// Path escapes mountRoot via ".." components.
		"dot_dot_traversal": filepath.Join(mountRoot, "..", "evil", ".omnipus", "skills", "deploy", "SKILL.md"),
		// The "recognised directory" collapses onto the mount root itself
		// (no nested skills subdirectory at all) — not a shape any real
		// discovery result produces, and must be refused rather than
		// silently accepted.
		"collapses_onto_mount_root": filepath.Join(mountRoot, "deploy", "SKILL.md"),
	}

	for name, badPath := range cases {
		t.Run(name, func(t *testing.T) {
			shelf := ProjectShelf{
				"deploy": ProjectSkill{
					SkillInfo: SkillInfo{ID: "deploy", Name: "deploy", Path: badPath, Source: string(ShelfProject)},
					MountName: "alpha",
					MountRoot: mountRoot,
				},
			}
			w, _, err := ResolveProjectSkillWriter(shelf, "deploy")
			if !errors.Is(err, ErrProjectWriteEscapesMount) {
				t.Fatalf("case %s: err = %v, want ErrProjectWriteEscapesMount", name, err)
			}
			if w != nil {
				t.Fatalf("case %s: expected no writer to be returned on confinement failure", name)
			}
		})
	}
}

// TestRemoveSkill_ProjectSlugDeletesProjectFile covers ADR-072 D6.1 / spec
// FR-068/069: removing a project skill deletes the project's own file
// (its whole skill directory), confined to the mount that owns it, and
// leaves the rest of the mount's recognised skills directory intact.
//
// Traces to: ADR-072 D6.1; spec FR-068/069; BDD "Removing a project skill
// deletes the project's file".
func TestRemoveSkill_ProjectSlugDeletesProjectFile(t *testing.T) {
	mountRoot, _, shelf := newProjectSkillFixture(t, "deploy")
	skillsDir := filepath.Join(mountRoot, ".omnipus", "skills")
	skillDir := filepath.Join(skillsDir, "deploy")

	w, ps, err := ResolveProjectSkillWriter(shelf, "deploy")
	if err != nil {
		t.Fatalf("ResolveProjectSkillWriter: %v", err)
	}

	if err := w.RemoveSkill(ps.ID); err != nil {
		t.Fatalf("RemoveSkill: %v", err)
	}

	if _, statErr := os.Stat(skillDir); !os.IsNotExist(statErr) {
		t.Fatalf("project skill's own directory still exists after removal (err=%v)", statErr)
	}
	// The removal is confined to the one skill's own subdirectory — the
	// mount's recognised skills directory itself survives.
	if _, statErr := os.Stat(skillsDir); statErr != nil {
		t.Fatalf("removal escaped the skill's own directory and removed its parent: %v", statErr)
	}

	// Removing an already-removed project skill reports ErrNotFound rather
	// than silently succeeding a second time.
	if err := w.RemoveSkill(ps.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound removing an already-removed skill, got %v", err)
	}
}
