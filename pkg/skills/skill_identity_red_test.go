package skills

// RED tests — landing/skills (T9): skill identity, one rule (spec §5.13),
// test row 29 / T-K1; scenarios S-6.7, S-6.8, S-6.9, E-10, E-11; dataset D-1.
// Traces to: skills-spec-r15.md §5.13, §5.10, §16 S-6.7–S-6.9, §17.1 row 29,
// §17.2 D-1.
//
// RED expectation: every test in this file FAILS on the baseline because the
// case-insensitive one-identity behaviour does not exist (B-01: grants and
// resolution ignore case, reservation/collision/dedup do not).

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeSkillFixture writes a minimal valid user skill folder under root.
func writeSkillFixture(t *testing.T, root, slug string) string {
	t.Helper()
	dir := filepath.Join(root, slug)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	skillMD := "---\nname: " + slug + "\ndescription: fixture skill " + slug + "\n---\n\nBody of " + slug + ".\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644))
	return dir
}

// builtinMarkerFixture marks dir as a built-in folder (the stamp the pinned
// writer writes; SyncBuiltins keeps its meaning, §8.1).
func builtinMarkerFixture(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".omnipus-builtin"), []byte("omnipus\n"), 0o644))
}

// TestSkillKey_IsTheLowercasedSlug pins the one identity function (§5.13).
// BDD: Given any slug spelling, When SkillKey is applied, Then equal keys.
// Traces to: skills-spec-r15.md §5.13 (SkillKey = strings.ToLower).
func TestSkillKey_IsTheLowercasedSlug(t *testing.T) {
	// RED: scaffold returns "" — the identity function is not implemented.
	assert.Equal(t, "plan", SkillKey("plan"), "lowercase stays itself")
	assert.Equal(t, "plan", SkillKey("Plan"), "Plan shares plan's key")
	assert.Equal(t, "plan", SkillKey("PLAN"), "PLAN shares plan's key")
	assert.Equal(t, "plan", SkillKey("pLaN"), "mixed case shares plan's key")
	assert.Equal(t, "my-skill", SkillKey("My-Skill"), "hyphens preserved, case folded")
}

// TestCreate_CaseTwin_OfUserSkill_Refused_SKILL_EXISTS (S-6.9, E-11, D-1 5b).
// BDD: Given user skill foo installed on a case-sensitive disk, When a create
// or copy targets Foo/FOO (same key), Then SKILL_EXISTS naming the on-disk
// slug foo, and no second folder exists.
// Traces to: skills-spec-r15.md §5.13 table (create/copy rows), S-6.9.
func TestCreate_CaseTwin_OfUserSkill_Refused_SKILL_EXISTS(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("case-sensitive-disk scenario (S-6.9): needs a case-sensitive volume (Linux)")
	}
	root := t.TempDir()
	writeSkillFixture(t, root, "foo")
	exportShipped(t, "plan", filepath.Join(root, "plan")) // a valid copy source, independent of the target collision

	newSkillMD := "---\nname: Foo\ndescription: case twin\n---\nbody\n"
	cases := []struct {
		name string
		req  ChangeRequest
	}{
		{
			name: "write_file Foo/SKILL.md",
			req: ChangeRequest{
				Kind: KindCreate, Slug: "Foo", Content: []byte(newSkillMD),
				Expect: Expectation{Absent: true},
				Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
			},
		},
		{
			name: "edit_skill copy plan to=FOO",
			req: ChangeRequest{
				Kind: KindCreate, Slug: "plan", To: "FOO",
				Expect: Expectation{Absent: true},
				Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ApplyChange(root, tc.req)
			require.Error(t, err, "a case twin of an installed skill must be refused")
			assert.True(t, errors.Is(err, ErrSkillExists),
				"want SKILL_EXISTS, got: %v", err)
			assert.Contains(t, err.Error(), "skill 'foo' already exists; read it first",
				"SKILL_EXISTS must name the folder that is on disk (foo), byte-exact §5.10")
			_, statErr := os.Stat(filepath.Join(root, "Foo"))
			_, statErr2 := os.Stat(filepath.Join(root, "FOO"))
			assert.True(t, os.IsNotExist(statErr), "no stray Foo folder may exist after refusal")
			assert.True(t, os.IsNotExist(statErr2), "no stray FOO folder may exist after refusal")
		})
	}
}

// TestBuiltinNameReserved_CaseInsensitive_AllEntryPoints (S-6.7, E-10, D-1 5a/7).
// BDD: Given built-in plan, When any create/copy/install targets Plan, PLAN
// or pLaN, Then BUILTIN_NAME_RESERVED with the byte-exact text, no folder,
// no grant change.
// Traces to: skills-spec-r15.md §5.13 (IsBuiltin row), §5.10, S-6.7.
func TestBuiltinNameReserved_CaseInsensitive_AllEntryPoints(t *testing.T) {
	root := t.TempDir()

	for _, target := range []string{"Plan", "PLAN", "pLaN"} {
		t.Run("copy to "+target, func(t *testing.T) {
			_, err := ApplyChange(root, ChangeRequest{
				Kind: KindCreate, Slug: "plan", To: target,
				Expect: Expectation{Absent: true},
				Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
			})
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrBuiltinNameReserved), "want BUILTIN_NAME_RESERVED, got %v", err)
			assert.Contains(t, err.Error(),
				"'"+strings.ToLower(target)+"' is the name of a built-in skill and cannot be used for another skill. Choose a different name.",
				"byte-exact §5.10 BUILTIN_NAME_RESERVED text, naming the built-in slug")
			_, statErr := os.Stat(filepath.Join(root, target))
			assert.True(t, os.IsNotExist(statErr), "no folder created for reserved name")
		})
	}

	t.Run("write_file Plan/SKILL.md", func(t *testing.T) {
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindCreate, Slug: "Plan",
			Content: []byte("---\nname: Plan\ndescription: x\n---\nbody\n"),
			Expect:  Expectation{Absent: true},
			Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
		})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrBuiltinNameReserved), "want BUILTIN_NAME_RESERVED, got %v", err)
	})

	t.Run("install PLAN", func(t *testing.T) {
		staged := t.TempDir()
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindInstall, Slug: "PLAN", StagedDir: staged,
			Expect: Expectation{Absent: true},
			Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "install_skill"},
		})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrBuiltinNameReserved), "want BUILTIN_NAME_RESERVED, got %v", err)
	})

	t.Run("no grant change", func(t *testing.T) {
		editor := &recordingGrants{}
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindCreate, Slug: "Plan",
			Content: []byte("---\nname: Plan\ndescription: x\n---\nbody\n"),
			Expect:  Expectation{Absent: true},
			Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
			Grants:  editor,
		})
		require.Error(t, err)
		assert.Empty(t, editor.removed, "a refused create must not touch any grant list")
	})
}

// TestWrite_IntoBuiltinFolder_BUILTIN_READ_ONLY covers the write side of the
// identity rule (S-6.1 partial, S-6.8's Linux-visible half): a write whose
// slug key matches a built-in folder on disk is refused BUILTIN_READ_ONLY
// with the byte-exact §5.10 text, before the lock (§7.1 step 0).
// Traces to: skills-spec-r15.md §7.1 step 0, §5.10 BUILTIN_READ_ONLY, S-6.1.
func TestWrite_IntoBuiltinFolder_BUILTIN_READ_ONLY(t *testing.T) {
	root := t.TempDir()
	builtinDir := writeSkillFixture(t, root, "plan")
	builtinMarkerFixture(t, builtinDir)

	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "plan", Path: "notes.md",
		Content: []byte("notes\n"),
		Expect:  Expectation{Revision: "r1"},
		Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBuiltinReadOnly), "want BUILTIN_READ_ONLY, got %v", err)
	assert.Contains(t, err.Error(),
		"'plan' is a built-in skill. Built-in skills are read-only and are replaced with the shipped version on every Omnipus update, so this change was not saved.",
		"byte-exact §5.10 BUILTIN_READ_ONLY text")
	assert.Contains(t, err.Error(),
		"Copy it first: edit_skill action=copy skill=plan to=<new-name>, then edit the copy and grant it to the agents that should use it.",
		"byte-exact §5.10 copy hint")
	// Nothing written: the folder still holds only the fixture files.
	entries, _ := os.ReadDir(builtinDir)
	assert.Len(t, entries, 2, "only SKILL.md and the stamp remain after the refusal")
}

// TestCreate_ValidSlugBoundaries (D-1 rows 1–4, 6): the slug rule is unchanged
// (namePattern, ≤ 64); copy/create enforce it before anything else.
// Traces to: skills-spec-r15.md §17.2 D-1, §5.4 copy precondition (to valid).
func TestCreate_ValidSlugBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		to      string
		wantErr bool
	}{
		{"min length a", "a", false},
		{"min+1 ab", "ab", false},
		{"max-1 63", strings.Repeat("a", 63), false},
		{"max length 64", strings.Repeat("a", 64), false},
		{"uppercase permitted by D-1 row 5", "My-Skill", false},
		{"max+1 65 chars", strings.Repeat("a", 65), true},
		{"double hyphen", "my--skill", true},
		{"leading hyphen", "-x", true},
		{"trailing hyphen", "x-", true},
		{"dot name", ".hidden", true},
		{"dot dot", "..", true},
		{"separator", "a/b", true},
		{"colon", "a:b", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// A valid shipped source is a prerequisite, not the subject of
			// this target-name test (§7.2 requires built-in/shipped equality).
			root := t.TempDir()
			exportShipped(t, "plan", filepath.Join(root, "plan"))
			source := readFile(t, filepath.Join(root, "plan", "SKILL.md"))
			require.Contains(t, source, "name: plan", "fixture has the source frontmatter")
			out, err := ApplyChange(root, ChangeRequest{
				Kind: KindCreate, Slug: "plan", To: tc.to,
				Expect: Expectation{Absent: true},
				Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
			})
			if tc.wantErr {
				require.Error(t, err, "invalid copy target %q must be refused", tc.to)
				if len(tc.to) == 65 {
					assert.Contains(t, err.Error(), "INVALID_INPUT", "D-1 row 3 fixes the max+1 error code")
				}
				entries, readErr := os.ReadDir(root)
				require.NoError(t, readErr)
				var names []string
				for _, entry := range entries {
					names = append(names, entry.Name())
				}
				assert.Equal(t, []string{"plan"}, names, "invalid target leaves no staged or published second folder")
				assert.Equal(t, source, readFile(t, filepath.Join(root, "plan", "SKILL.md")), "refusal never rewrites the source")
				return
			}
			// Valid target: the copy must actually commit and land on disk
			// (a refusal here would be a slug-rule regression).
			require.NoError(t, err, "valid copy target %q must not be refused", tc.to)
			assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
			assert.Equal(t, strings.Replace(source, "name: plan", "name: "+tc.to, 1),
				readFile(t, filepath.Join(root, tc.to, "SKILL.md")),
				"the real copy contains the shipped source with only the declared name transform")
		})
	}
	_ = time.Now // keep time import stable for date-stamped fixtures in this file's siblings
}

// recordingGrants is a GrantEditor test double that records removals and
// models stored grant state (seeded) for the §5.13a tests.
type recordingGrants struct {
	removed []string
	err     error // when set, RemoveGrant fails (fault leg, §5.13a)
	seeded  map[string]bool
}

func (r *recordingGrants) RemoveGrant(name string) error {
	if r.err != nil {
		return r.err
	}
	r.removed = append(r.removed, strings.ToLower(name))
	return nil
}

// holdsGrant reports whether the fake store still lists name.
func (r *recordingGrants) holdsGrant(name string) bool {
	key := strings.ToLower(name)
	for _, g := range r.removed {
		if g == key {
			return false
		}
	}
	return r.seeded[key]
}

// seeded lets a test pre-fill grant state without mutating removed.
func (r *recordingGrants) seededSet(names ...string) {
	if r.seeded == nil {
		r.seeded = map[string]bool{}
	}
	for _, n := range names {
		r.seeded[strings.ToLower(n)] = true
	}
}
