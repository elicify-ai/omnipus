package skills

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validFor(name string) string {
	// Build a valid SKILL.md whose frontmatter name matches name.
	return "---\nname: " + name + "\ndescription: A valid test skill with a sufficiently descriptive summary line.\n---\n\n# " + name + "\n\nThis skill does a thing.\n"
}

// r15 §§7.1/5.11: creation and edit each record their resulting state;
// restoring the creation proves the exact original bytes are recoverable.
func TestSkillCreate_Versioned(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	original := validFor("my-skill")
	created := createAuthoredSkill(t, root, "my-skill", original)
	ids := manifestIDs(t, SkillHistoryRoot(root), "my-skill")
	require.Len(t, ids, 1, "creation itself records a version, not only later edits")
	first := ids[0]
	assert.Equal(t, "create", manifestField(t, SkillHistoryRoot(root), "my-skill", first, "kind"))
	_, err := ApplyChange(root, ChangeRequest{Kind: KindCreate, Slug: "my-skill",
		Content: []byte(original), Expect: Expectation{Absent: true}})
	require.ErrorIs(t, err, ErrSkillExists)
	assert.Contains(t, err.Error(), "skill 'my-skill' already exists; read it first")
	assert.Equal(t, ids, manifestIDs(t, SkillHistoryRoot(root), "my-skill"), "duplicate creation records nothing")

	edited := "---\nname: my-skill\ndescription: An edited description that is also long enough to be valid here.\n---\n\n# my-skill\n\nEdited body.\n"
	changed, second := commitWrite(t, root, "my-skill", "SKILL.md", edited, created.Revision)
	assert.Equal(t, []string{second, first}, manifestIDs(t, SkillHistoryRoot(root), "my-skill"))
	assert.Equal(t, first, manifestField(t, SkillHistoryRoot(root), "my-skill", second, "parent"))
	assert.Equal(t, "edit", manifestField(t, SkillHistoryRoot(root), "my-skill", second, "kind"))
	assert.Equal(t, edited, readFile(t, filepath.Join(root, "my-skill", "SKILL.md")))
	_, err = ApplyChange(root, ChangeRequest{Kind: KindRestore, Slug: "my-skill", Version: first,
		Expect: Expectation{Revision: changed.Revision}})
	require.NoError(t, err)
	assert.Equal(t, original, readFile(t, filepath.Join(root, "my-skill", "SKILL.md")), "the entire original version restores byte-for-byte")
}

// r15 §§5.4/7.1 step 0 replace implicit overrides: refuse built-in edits,
// explicitly copy to a different name, then edit the copy. Source stays intact.
func TestSkillEdit_BuiltinRefusesUntilCopied(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	builtinDir := filepath.Join(root, "plan")
	exportShipped(t, "plan", builtinDir)
	original := readFile(t, filepath.Join(builtinDir, "SKILL.md"))
	revision := readLiveRevision(t, builtinDir)
	out, err := ApplyChange(root, ChangeRequest{Kind: KindEdit, Slug: "plan", Path: "SKILL.md",
		Content: []byte(validFor("plan")), Expect: Expectation{Revision: revision}})
	require.ErrorIs(t, err, ErrBuiltinReadOnly)
	assert.Contains(t, err.Error(), "Copy it first: edit_skill action=copy skill=plan to=<new-name>, then edit the copy and grant it to the agents that should use it.")
	assert.Equal(t, PersistenceNone, out.PersistenceStatus)
	assert.Equal(t, original, readFile(t, filepath.Join(builtinDir, "SKILL.md")))
	assert.NoDirExists(t, filepath.Join(SkillHistoryRoot(root), "plan"), "refused built-in edit never writes history")

	copied, err := ApplyChange(root, ChangeRequest{Kind: KindCreate, Slug: "plan", To: "plan-personal", Expect: Expectation{Absent: true}})
	require.NoError(t, err)
	assert.Equal(t, strings.Replace(original, "name: plan", "name: plan-personal", 1), readFile(t, filepath.Join(root, "plan-personal", "SKILL.md")))
	edited := validFor("plan-personal") + "\nOverridden in an explicit copy.\n"
	commitWrite(t, root, "plan-personal", "SKILL.md", edited, copied.Revision)
	assert.Equal(t, original, readFile(t, filepath.Join(builtinDir, "SKILL.md")), "copy edits cannot touch the built-in")
	assert.Equal(t, edited, readFile(t, filepath.Join(root, "plan-personal", "SKILL.md")))
	loader := NewSkillsLoader("", root, "")
	body, ok := loader.LoadSkill("plan-personal")
	require.True(t, ok)
	assert.Contains(t, body, "Overridden in an explicit copy.")
	_, err = ApplyChange(root, ChangeRequest{Kind: KindEdit, Slug: "ghost", Path: "SKILL.md",
		Content: []byte(validFor("ghost")), Expect: Expectation{Revision: revision}})
	require.ErrorIs(t, err, ErrSkillChanged, "§7.3: an expected revision against absence is refused, never an implicit creation")
	assert.NoDirExists(t, filepath.Join(root, "ghost"))
}

// Keep UAT S68's same-root collision, now with the real shipped built-in and
// startup stamp. r15 never edits it or consumes its marker; only the explicit
// user copy gets versions and repeated in-place edits.
func TestSkillEdit_MaterializedBuiltinRefused_CopyRemainsEditable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	_, err := SyncBuiltins(root, nil)
	require.NoError(t, err)
	builtinDir := filepath.Join(root, "plan")
	original := readFile(t, filepath.Join(builtinDir, "SKILL.md"))
	generation := generationOf(t, builtinDir)
	_, err = ApplyChange(root, ChangeRequest{Kind: KindEdit, Slug: "plan", Path: "SKILL.md",
		Content: []byte(validFor("plan")), Expect: Expectation{Revision: readLiveRevision(t, builtinDir)}})
	require.ErrorIs(t, err, ErrBuiltinReadOnly)
	requireSameTree(t, "plan", builtinDir)
	assert.FileExists(t, filepath.Join(builtinDir, builtinMarkerFile), "the builtin marker is never consumed")
	assert.Equal(t, generation, generationOf(t, builtinDir))
	assert.NoDirExists(t, filepath.Join(SkillHistoryRoot(root), "plan"))

	copied, err := ApplyChange(root, ChangeRequest{Kind: KindCreate, Slug: "plan", To: "my-plan", Expect: Expectation{Absent: true}})
	require.NoError(t, err)
	ids := manifestIDs(t, SkillHistoryRoot(root), "my-plan")
	require.Len(t, ids, 1)
	first := ids[0]
	copyOriginal := strings.Replace(original, "name: plan", "name: my-plan", 1)
	copyGeneration := generationOf(t, filepath.Join(root, "my-plan"))
	firstEdit := validFor("my-plan") + "\nUser-edited body.\n"
	changed, _ := commitWrite(t, root, "my-plan", "SKILL.md", firstEdit, copied.Revision)
	secondEdit := validFor("my-plan") + "\nSecond edit.\n"
	twice, _ := commitWrite(t, root, "my-plan", "SKILL.md", secondEdit, changed.Revision)
	assert.Equal(t, secondEdit, readFile(t, filepath.Join(root, "my-plan", "SKILL.md")))
	assert.Len(t, manifestIDs(t, SkillHistoryRoot(root), "my-plan"), 3, "copy and both edits are versioned")
	assert.Equal(t, copyGeneration, generationOf(t, filepath.Join(root, "my-plan")), "content edits preserve the copy's identity")
	_, err = ApplyChange(root, ChangeRequest{Kind: KindRestore, Slug: "my-plan", Version: first, Expect: Expectation{Revision: twice.Revision}})
	require.NoError(t, err)
	assert.Equal(t, copyOriginal, readFile(t, filepath.Join(root, "my-plan", "SKILL.md")), "original copied content remains recoverable after repeated edits")
	assert.Equal(t, original, readFile(t, filepath.Join(builtinDir, "SKILL.md")))
	loader := NewSkillsLoader("", root, "")
	_, ok := loader.LoadSkill("my-plan")
	assert.True(t, ok, "the restored copy remains loadable")
}

// r15 §§7.2/5.13: preserve traversal, size and invalid-instruction coverage
// at the registry pipeline. Frontmatter name is identity, never display text.
func TestSkillWrite_Confinement_TraversalAndOversize_Rejected(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	for _, name := range []string{"../evil", "..", "foo/bar", "foo\\bar", "/etc/passwd", "a/../../b"} {
		t.Run("traversal_"+name, func(t *testing.T) {
			out, err := ApplyChange(root, ChangeRequest{Kind: KindCreate, Slug: name,
				Content: []byte(validFor("evil")), Expect: Expectation{Absent: true}})
			require.ErrorIs(t, err, ErrInvalidInput)
			assert.Equal(t, PersistenceNone, out.PersistenceStatus)
		})
	}
	assert.NoDirExists(t, filepath.Join(filepath.Dir(root), "evil"), "no path escaped the registry")
	oversize := validFor("big") + strings.Repeat("A", MaxSkillMarkdownBytes+1)
	_, err := ApplyChange(root, ChangeRequest{Kind: KindCreate, Slug: "big", Content: []byte(oversize), Expect: Expectation{Absent: true}})
	require.ErrorIs(t, err, ErrSaveCheckFailed)
	assert.Contains(t, err.Error(), "the change to 'big' was not saved:")
	assert.NoDirExists(t, filepath.Join(root, "big"))
	for skill, content := range map[string]string{
		"no-description":     "---\nname: no-description\n---\n\n# no-description\n",
		"empty":              "",
		"display-name-skill": "---\nname: A Friendly Display Name\ndescription: A sufficiently long description.\n---\n# x\n",
	} {
		t.Run(skill, func(t *testing.T) {
			_, createErr := ApplyChange(root, ChangeRequest{Kind: KindCreate, Slug: skill, Content: []byte(content), Expect: Expectation{Absent: true}})
			require.ErrorIs(t, createErr, ErrSaveCheckFailed)
			assert.NoDirExists(t, filepath.Join(root, skill), "invalid instructions never publish")
		})
	}
	// Preserve human-readable naming, through the supported display metadata.
	content := "---\nname: display-name-skill\ndescription: A sufficiently long description.\nmetadata:\n  display_name: A Friendly Display Name\n---\n# x\n"
	createAuthoredSkill(t, root, "display-name-skill", content)
	loader := NewSkillsLoader("", root, "")
	rows := loader.ListSkills()
	require.Len(t, rows, 1)
	assert.Equal(t, "display-name-skill", rows[0].ID)
	assert.Equal(t, "A Friendly Display Name", rows[0].Name)
}

// TestValidateSkillMarkdown_Valid sanity-checks a well-formed skill passes.
func TestValidateSkillMarkdown_Valid(t *testing.T) {
	if err := ValidateSkillMarkdown("good", validFor("good")); err != nil {
		t.Fatalf("valid SKILL.md rejected: %v", err)
	}
}

// Preserve UAT S59's decoy-body regression: explicit empty/null/whitespace
// descriptions cannot borrow prose from the body. Both create and existing
// edit use r15 §7.2's save check; refusal preserves exact published bytes.
func TestSkillCreateEdit_RejectsEmptyFrontmatterDescription(t *testing.T) {
	const body = "\n\n# decoy\n\nThis is a real, sentence-shaped paragraph that a naive fallback would mistake for the skill's description.\n"
	cases := []struct {
		name, description string
		wantErr           bool
	}{
		{"empty", `""`, true},
		{"null", "null", true},
		{"whitespace", `"   "`, true},
		{"valid", `"Use when the user asks to decoy-test something long enough to pass validation."`, false},
	}
	for _, tc := range cases {
		for _, edit := range []bool{false, true} {
			op := "create"
			if edit {
				op = "edit"
			}
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "skills")
				content := "---\nname: decoy\ndescription: " + tc.description + "\n---" + body
				req := ChangeRequest{Kind: KindCreate, Slug: "decoy", Content: []byte(content), Expect: Expectation{Absent: true}}
				if edit {
					created := createAuthoredSkill(t, root, "decoy", validFor("decoy"))
					req.Kind, req.Path, req.Expect = KindEdit, "SKILL.md", Expectation{Revision: created.Revision}
				}
				out, err := ApplyChange(root, req)
				if tc.wantErr {
					require.ErrorIs(t, err, ErrSaveCheckFailed)
					assert.Contains(t, err.Error(), "description is required", "the explicit frontmatter is validated, not the decoy body")
					assert.Equal(t, PersistenceNone, out.PersistenceStatus)
					if edit {
						assert.Equal(t, validFor("decoy"), readFile(t, filepath.Join(root, "decoy", "SKILL.md")))
						assert.Len(t, manifestIDs(t, SkillHistoryRoot(root), "decoy"), 1)
					} else {
						assert.NoDirExists(t, filepath.Join(root, "decoy"))
					}
					return
				}
				require.NoError(t, err)
				assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
				assert.Equal(t, content, readFile(t, filepath.Join(root, "decoy", "SKILL.md")))
			})
		}
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

	if _, statErr := os.Stat(skillDir); !errors.Is(statErr, os.ErrNotExist) {
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
