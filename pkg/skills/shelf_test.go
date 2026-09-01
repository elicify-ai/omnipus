package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeSkillFile creates <dir>/<slug>/SKILL.md with the given description,
// creating parent directories as needed. Shared test helper for this file
// and the sibling *_test.go files added alongside it in this package.
func writeSkillFile(t *testing.T, dir, slug, description string) string {
	t.Helper()
	skillDir := filepath.Join(dir, slug)
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	content := "---\nname: " + slug + "\ndescription: " + description + "\n---\n\n# " + slug + "\n"
	path := filepath.Join(skillDir, "SKILL.md")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestResolveSkillName_AllowsProjectSlugWithoutGrant (test 11, ADR-072 T3a)
// verifies ADR-072 D4.1's headline claim: a mount is its own grant
// instrument. An agent with an EMPTY registry/builtin allowlist must still
// resolve a project skill discovered under its workspace's mount, with no
// separate slug grant.
// Traces to: spec scenario "A mounted project's skills appear without any grant" (FR-026, FR-027).
func TestResolveSkillName_AllowsProjectSlugWithoutGrant(t *testing.T) {
	mountRoot := t.TempDir()
	writeSkillFile(t, filepath.Join(mountRoot, ".claude", "skills"), "onboarding", "Use when onboarding a new teammate")

	projectShelf, collisions := MergeProjectSkills([]ProjectMount{{Name: "acme", Root: mountRoot}})
	require.Empty(t, collisions)
	require.Contains(t, projectShelf, "onboarding")

	// Deny-all allowlist: no registry/builtin slug is granted.
	denyAll := func(string) bool { return false }

	resolved, ok, collision := ResolveSkillName(nil, denyAll, projectShelf, "onboarding")
	require.True(t, ok, "a project skill must resolve with no registry/builtin grant at all")
	assert.Nil(t, collision)
	assert.Equal(t, "onboarding", resolved.Slug)
	assert.Equal(t, ShelfProject, resolved.Shelf)
	assert.Equal(t, "acme", resolved.MountName)

	// A nil allowed predicate (an agent with no allowlist mechanism wired at
	// all) must behave identically for the project shelf: the mount is the
	// grant, not the predicate.
	resolvedNilAllowed, okNilAllowed, _ := ResolveSkillName(nil, nil, projectShelf, "onboarding")
	require.True(t, okNilAllowed)
	assert.Equal(t, ShelfProject, resolvedNilAllowed.Shelf)
}

// TestShelfResolution_ProjectCannotShadowGrantedRegistrySlug (test 12,
// ADR-072 T3b) verifies D4.2's carve-out: a project skill may add a new
// slug, but must never resolve in place of a slug the agent already holds a
// grant for on the registry (or builtin) shelf. The registry skill must win,
// and the collision must be recorded naming both locations.
// Traces to: spec scenario "A project skill cannot shadow a granted registry slug" (FR-028, FR-030).
func TestShelfResolution_ProjectCannotShadowGrantedRegistrySlug(t *testing.T) {
	registryPath := "/omnipus/skills/release-notes/SKILL.md"
	registryAndBuiltin := []SkillInfo{
		{ID: "release-notes", Name: "release-notes", Path: registryPath, Source: "global", Description: "Use when cutting a release"},
	}
	allowedReleaseNotes := func(slug string) bool { return slug == "release-notes" }

	mountRoot := t.TempDir()
	writeSkillFile(t, filepath.Join(mountRoot, ".claude", "skills"), "release-notes", "A different, project-authored release-notes skill")
	projectShelf, _ := MergeProjectSkills([]ProjectMount{{Name: "repo", Root: mountRoot}})
	require.Contains(t, projectShelf, "release-notes")

	resolved, ok, collision := ResolveSkillName(registryAndBuiltin, allowedReleaseNotes, projectShelf, "release-notes")
	require.True(t, ok)
	assert.Equal(t, ShelfRegistry, resolved.Shelf, "the GRANTED registry skill must win, never the project one")
	assert.Equal(t, registryPath, resolved.Path)

	require.NotNil(t, collision, "the shadow attempt must be recorded, not silently resolved")
	assert.Equal(t, "release-notes", collision.Slug)
	require.Len(t, collision.Locations, 2)
	var sawRegistry, sawProject bool
	for _, loc := range collision.Locations {
		if loc.Path == registryPath {
			sawRegistry = true
		}
		if loc.Path == projectShelf["release-notes"].Path {
			sawProject = true
		}
	}
	assert.True(t, sawRegistry, "collision must name the registry location")
	assert.True(t, sawProject, "collision must name the project location")
}

// TestResolveSkillName_DeniesUngrantedRegistrySlug (test 10, ADR-072 D4/T2)
// verifies the resolution door: a registry slug that IS installed but that
// the acting agent has not been granted must resolve to not-found — a
// present-but-ungranted registry entry must never win step 1's match just
// because it exists in registryAndBuiltin, and it must not fall through to
// resolving as though it were a project skill either (it isn't one).
// Traces to: spec scenario "Loading an ungranted skill is refused by name" (FR-021).
func TestResolveSkillName_DeniesUngrantedRegistrySlug(t *testing.T) {
	registryAndBuiltin := []SkillInfo{
		{ID: "ungranted-skill", Name: "ungranted-skill", Path: "/omnipus/skills/ungranted-skill/SKILL.md", Source: "global", Description: "Installed but never granted to this agent"},
	}
	denyAll := func(string) bool { return false }

	resolved, ok, collision := ResolveSkillName(registryAndBuiltin, denyAll, nil, "ungranted-skill")
	assert.False(t, ok, "an installed-but-ungranted registry slug must not resolve")
	assert.Nil(t, collision)
	assert.Equal(t, ResolvedSkill{}, resolved)

	// Case-insensitive input must be denied identically — the grant gate, not
	// the name match, is what refuses this slug.
	resolvedCased, okCased, _ := ResolveSkillName(registryAndBuiltin, denyAll, nil, "Ungranted-Skill")
	assert.False(t, okCased)
	assert.Equal(t, ResolvedSkill{}, resolvedCased)
}

// TestResolveSkillName_ProjectShelfMatchesByDisplayName (ADR-072 Finding B)
// verifies ResolveSkillName's own doc comment promise — matching "a skill
// slug (or its display name — matched case-insensitively against either)
// uniformly across shelves" — actually holds for the project shelf, not just
// the registry/builtin branch. A project skill whose SKILL.md sets a `name:`
// distinct from its directory slug must be resolvable by that display name,
// exactly as a registry/builtin skill already is.
func TestResolveSkillName_ProjectShelfMatchesByDisplayName(t *testing.T) {
	mountRoot := t.TempDir()
	skillDir := filepath.Join(mountRoot, ".claude", "skills", "deploy")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	content := "---\nname: Deploy Helper\ndescription: Use when deploying this project\n---\n\n# Deploy Helper\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o600))

	projectShelf, collisions := MergeProjectSkills([]ProjectMount{{Name: "repo", Root: mountRoot}})
	require.Empty(t, collisions)
	require.Contains(t, projectShelf, "deploy")
	require.Equal(t, "Deploy Helper", projectShelf["deploy"].Name)

	denyAll := func(string) bool { return false }

	// Resolving by the display name — not the slug — must succeed and return
	// the canonical slug, mirroring the registry/builtin branch's contract
	// (ResolvedSkill.Slug is always the canonical slug, never the free-form
	// display name, even when the caller searched by display name).
	resolved, ok, collision := ResolveSkillName(nil, denyAll, projectShelf, "Deploy Helper")
	require.True(t, ok, "a project skill must resolve by its display name, not just its slug")
	assert.Nil(t, collision)
	assert.Equal(t, "deploy", resolved.Slug)
	assert.Equal(t, ShelfProject, resolved.Shelf)
	assert.Equal(t, "repo", resolved.MountName)

	// Case-insensitive, per the doc comment.
	resolvedCased, okCased, _ := ResolveSkillName(nil, denyAll, projectShelf, "deploy helper")
	require.True(t, okCased)
	assert.Equal(t, "deploy", resolvedCased.Slug)

	// The slug itself must still resolve too (display-name matching is
	// additive, not a replacement for the existing slug lookup).
	resolvedBySlug, okBySlug, _ := ResolveSkillName(nil, denyAll, projectShelf, "deploy")
	require.True(t, okBySlug)
	assert.Equal(t, "deploy", resolvedBySlug.Slug)

	// A name that matches neither slug nor display name still fails.
	_, okMiss, _ := ResolveSkillName(nil, denyAll, projectShelf, "nonexistent")
	assert.False(t, okMiss)
}

// TestResolve_DanglingRegistryGrantDoesNotShadowProjectSkill (test 51g,
// MAJ-003, FR-028a) verifies the one-sided nature of D4.2's carve-out: it
// protects a slug the agent's grant currently REACHES (i.e. still installed),
// not the bare name. When the grant names a registry slug that has since
// been uninstalled, a same-slug project skill must resolve normally, with no
// not-found error and no phantom shadow.
// Traces to: spec scenario "A dangling registry grant does not shadow a present project skill" (FR-028a).
func TestResolve_DanglingRegistryGrantDoesNotShadowProjectSkill(t *testing.T) {
	// The agent's grant list still names "deploy" (a slug it was once
	// explicitly granted), but the registry no longer carries an installed
	// skill by that name — ListSkills would no longer return it, so
	// registryAndBuiltin correctly contains no "deploy" entry at all.
	registryAndBuiltin := []SkillInfo{
		{ID: "unrelated", Name: "unrelated", Path: "/omnipus/skills/unrelated/SKILL.md", Source: "global", Description: "Some other skill"},
	}
	grantsDeployAndUnrelated := func(slug string) bool {
		return slug == "deploy" || slug == "unrelated"
	}

	mountRoot := t.TempDir()
	writeSkillFile(t, filepath.Join(mountRoot, ".claude", "skills"), "deploy", "Use when deploying this project")
	projectShelf, _ := MergeProjectSkills([]ProjectMount{{Name: "repo", Root: mountRoot}})

	resolved, ok, collision := ResolveSkillName(registryAndBuiltin, grantsDeployAndUnrelated, projectShelf, "deploy")
	require.True(t, ok, "the dangling grant must not produce a not-found result")
	assert.Nil(t, collision, "a name the grant can no longer reach is not a competing location")
	assert.Equal(t, ShelfProject, resolved.Shelf, "the project skill must resolve normally, unshadowed")
	assert.Equal(t, "deploy", resolved.Slug)
}
