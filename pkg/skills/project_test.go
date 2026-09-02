package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShelfResolution_MountNameOrderWinsOnCollision (test 13, ADR-072 T3c)
// verifies D4.2's cross-mount ordering rule: when two mounts on the same
// workspace carry a project skill under the same slug, the mount that sorts
// FIRST by byte-wise ascending name (FR-029: Go's default sort.Strings,
// mirroring the D7 instruction-file ordering) wins — deterministically,
// regardless of the order the mounts were passed in.
// Traces to: spec scenario "Two mounts with the same slug resolve by mount name" (FR-029).
func TestShelfResolution_MountNameOrderWinsOnCollision(t *testing.T) {
	alphaRoot := t.TempDir()
	betaRoot := t.TempDir()
	writeSkillFile(t, filepath.Join(alphaRoot, ".claude", "skills"), "deploy", "alpha's deploy skill")
	writeSkillFile(t, filepath.Join(betaRoot, ".claude", "skills"), "deploy", "beta's deploy skill")

	// Pass beta before alpha to prove the result does not depend on input order.
	shelf, _ := MergeProjectSkills([]ProjectMount{
		{Name: "beta", Root: betaRoot},
		{Name: "alpha", Root: alphaRoot},
	})

	require.Contains(t, shelf, "deploy")
	assert.Equal(t, "alpha", shelf["deploy"].MountName, "alpha sorts before beta and must win")
	assert.Equal(t, filepath.Join(alphaRoot, ".claude", "skills", "deploy", "SKILL.md"), shelf["deploy"].Path)
}

// TestShelfResolution_CollisionIsRecorded (test 14) verifies D4.2's
// "logged, not silently picked" requirement for the same cross-mount
// scenario: MergeProjectSkills must return a SlugCollision naming BOTH
// competing mounts and paths, not just quietly resolve the winner.
// Traces to: spec scenario "Two mounts with the same slug resolve by mount name" (FR-030, collision observability).
func TestShelfResolution_CollisionIsRecorded(t *testing.T) {
	alphaRoot := t.TempDir()
	betaRoot := t.TempDir()
	writeSkillFile(t, filepath.Join(alphaRoot, ".claude", "skills"), "deploy", "alpha's deploy skill")
	writeSkillFile(t, filepath.Join(betaRoot, ".claude", "skills"), "deploy", "beta's deploy skill")

	_, collisions := MergeProjectSkills([]ProjectMount{
		{Name: "alpha", Root: alphaRoot},
		{Name: "beta", Root: betaRoot},
	})

	require.Len(t, collisions, 1)
	c := collisions[0]
	assert.Equal(t, "deploy", c.Slug)
	require.Len(t, c.Locations, 2)

	names := make([]string, 0, len(c.Locations))
	for _, loc := range c.Locations {
		names = append(names, loc.Description)
	}
	assert.Contains(t, names, "mount alpha")
	assert.Contains(t, names, "mount beta")
}

// TestProjectSkillDiscovery_RecognisedDirectoriesOnly (test 15) verifies
// ADR-072 D6/FR-036..038: project-skill discovery triggers ONLY on
// "<mount>/.claude/skills/<slug>/SKILL.md" and
// "<mount>/.omnipus/skills/<slug>/SKILL.md" — no ".git" heuristic, no
// content sniffing, no other location. Covers every row of spec Dataset C /
// the "discovery triggers only on the recognised directories" scenario
// outline that does not require symlinks (those are covered by test 30e).
// Traces to: spec scenario outline "Project-skill discovery triggers only on the recognised directories".
func TestProjectSkillDiscovery_RecognisedDirectoriesOnly(t *testing.T) {
	t.Run("claude skills dir discovered", func(t *testing.T) {
		mountRoot := t.TempDir()
		writeSkillFile(t, filepath.Join(mountRoot, ".claude", "skills"), "x", "Use when x")
		skills, collisions := DiscoverProjectSkills("m", mountRoot)
		require.Empty(t, collisions)
		require.Len(t, skills, 1)
		assert.Equal(t, "x", skills[0].ID)
	})

	t.Run("omnipus skills dir discovered", func(t *testing.T) {
		mountRoot := t.TempDir()
		writeSkillFile(t, filepath.Join(mountRoot, ".omnipus", "skills"), "x", "Use when x")
		skills, collisions := DiscoverProjectSkills("m", mountRoot)
		require.Empty(t, collisions)
		require.Len(t, skills, 1)
		assert.Equal(t, "x", skills[0].ID)
	})

	t.Run("both directories same slug: omnipus wins, recorded", func(t *testing.T) {
		mountRoot := t.TempDir()
		writeSkillFile(t, filepath.Join(mountRoot, ".omnipus", "skills"), "x", "the omnipus copy")
		writeSkillFile(t, filepath.Join(mountRoot, ".claude", "skills"), "x", "the claude copy")
		skills, collisions := DiscoverProjectSkills("m", mountRoot)
		require.Len(t, skills, 1)
		assert.Equal(t, filepath.Join(mountRoot, ".omnipus", "skills", "x", "SKILL.md"), skills[0].Path,
			".omnipus/skills must win a same-slug clash over .claude/skills")
		require.Len(t, collisions, 1)
		assert.Equal(t, "x", collisions[0].Slug)
	})

	t.Run("SKILL.md at repo root: nothing discovered", func(t *testing.T) {
		mountRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(mountRoot, "SKILL.md"), []byte("---\nname: root\ndescription: d\n---\n"), 0o600))
		skills, collisions := DiscoverProjectSkills("m", mountRoot)
		assert.Empty(t, skills)
		assert.Empty(t, collisions)
	})

	t.Run("SKILL.md under an unrecognised subdirectory: nothing discovered", func(t *testing.T) {
		mountRoot := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(mountRoot, "docs"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(mountRoot, "docs", "SKILL.md"), []byte("---\nname: docs\ndescription: d\n---\n"), 0o600))
		skills, _ := DiscoverProjectSkills("m", mountRoot)
		assert.Empty(t, skills)
	})

	t.Run("git present, no skills dir: nothing discovered, no .git heuristic", func(t *testing.T) {
		mountRoot := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(mountRoot, ".git"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(mountRoot, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600))
		skills, _ := DiscoverProjectSkills("m", mountRoot)
		assert.Empty(t, skills)
	})

	t.Run("skills dir present but empty: nothing discovered, no error", func(t *testing.T) {
		mountRoot := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(mountRoot, ".claude", "skills"), 0o755))
		skills, collisions := DiscoverProjectSkills("m", mountRoot)
		assert.Empty(t, skills)
		assert.Empty(t, collisions)
	})

	t.Run("slug directory with no SKILL.md inside: nothing discovered", func(t *testing.T) {
		mountRoot := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(mountRoot, ".claude", "skills", "x"), 0o755))
		skills, _ := DiscoverProjectSkills("m", mountRoot)
		assert.Empty(t, skills)
	})

	t.Run("content is never sniffed to classify a non-SKILL.md file", func(t *testing.T) {
		mountRoot := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(mountRoot, ".claude", "skills", "x"), 0o755))
		// A file that LOOKS like skill frontmatter but is not named SKILL.md
		// must never be classified as a skill (FR-038: no content sniffing).
		require.NoError(t, os.WriteFile(
			filepath.Join(mountRoot, ".claude", "skills", "x", "NOT_SKILL.md"),
			[]byte("---\nname: x\ndescription: d\n---\n"), 0o600))
		skills, _ := DiscoverProjectSkills("m", mountRoot)
		assert.Empty(t, skills)
	})
}

// TestDiscovery_SymlinkedSkillFileOutsideMountRefused (test 30e, MAJ-005)
// verifies FR-077/FR-078's file-level symlink confinement — distinct from
// the directory-level case: the recognised skills directory itself is real
// and legitimately inside the mount, but one slug's own SKILL.md is a
// symlink pointing OUTSIDE the mount root. That one skill must be refused
// (not discovered), while an ordinary sibling and an in-bounds symlink
// (pointing to another file inside the same mount) are unaffected.
// Traces to: spec scenario "A symlinked skill file is refused", Dataset C rows 9, 11, 12.
func TestDiscovery_SymlinkedSkillFileOutsideMountRefused(t *testing.T) {
	mountRoot := t.TempDir()
	outsideDir := t.TempDir() // a sibling directory, NOT under mountRoot

	// Row 11: skills dir is real and in-mount, but x/SKILL.md is a symlink to
	// a file OUTSIDE the mount.
	outsideFile := filepath.Join(outsideDir, "secret.md")
	require.NoError(t, os.WriteFile(outsideFile, []byte("---\nname: x\ndescription: exfiltrated content\n---\n"), 0o600))
	xDir := filepath.Join(mountRoot, ".claude", "skills", "x")
	require.NoError(t, os.MkdirAll(xDir, 0o755))
	require.NoError(t, os.Symlink(outsideFile, filepath.Join(xDir, "SKILL.md")))

	// Row 12: y/SKILL.md is a symlink to another file INSIDE the same mount —
	// must be discovered normally.
	insideTarget := filepath.Join(mountRoot, "real-skill-content.md")
	require.NoError(t, os.WriteFile(insideTarget, []byte("---\nname: y\ndescription: Use when y\n---\n"), 0o600))
	yDir := filepath.Join(mountRoot, ".claude", "skills", "y")
	require.NoError(t, os.MkdirAll(yDir, 0o755))
	require.NoError(t, os.Symlink(insideTarget, filepath.Join(yDir, "SKILL.md")))

	// An ordinary, non-symlinked sibling — must be unaffected by x's refusal.
	writeSkillFile(t, filepath.Join(mountRoot, ".claude", "skills"), "z", "Use when z")

	skills, _ := DiscoverProjectSkills("m", mountRoot)

	ids := make(map[string]bool, len(skills))
	for _, s := range skills {
		ids[s.ID] = true
	}
	assert.False(t, ids["x"], "a SKILL.md symlinked outside the mount must be refused, not discovered")
	assert.True(t, ids["y"], "a SKILL.md symlinked to another file INSIDE the mount must discover normally")
	assert.True(t, ids["z"], "an unrelated ordinary sibling must be unaffected")
}

// TestDiscovery_SkillsDirItselfSymlinkedOutsideMountRefused covers Dataset C
// row 9 directly (the directory-level case, as distinct from the file-level
// case test 30e covers): the recognised skills directory itself is a
// symlink pointing outside the mount. Nothing under it may be discovered,
// even though the target directory contains an otherwise perfectly valid
// skill.
// Traces to: spec scenario "A symlinked skill file is refused", Dataset C row 9.
func TestDiscovery_SkillsDirItselfSymlinkedOutsideMountRefused(t *testing.T) {
	mountRoot := t.TempDir()
	outsideDir := t.TempDir()
	writeSkillFile(t, outsideDir, "x", "Use when x")

	require.NoError(t, os.MkdirAll(filepath.Join(mountRoot, ".claude"), 0o755))
	require.NoError(t, os.Symlink(outsideDir, filepath.Join(mountRoot, ".claude", "skills")))

	skills, _ := DiscoverProjectSkills("m", mountRoot)
	assert.Empty(t, skills, "a skills directory that is itself a symlink outside the mount must not be followed")
}
