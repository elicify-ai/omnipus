package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillsInfoValidate(t *testing.T) {
	testcases := []struct {
		name        string
		skillName   string
		description string
		wantErr     bool
		errContains []string
	}{
		{
			name:        "valid-skill",
			skillName:   "valid-skill",
			description: "a valid skill description",
			wantErr:     false,
		},
		{
			name:        "empty-name",
			skillName:   "",
			description: "description without name",
			wantErr:     true,
			errContains: []string{"name is required"},
		},
		{
			name:        "empty-description",
			skillName:   "skill-without-description",
			description: "",
			wantErr:     true,
			errContains: []string{"description is required"},
		},
		{
			name:        "empty-both",
			skillName:   "",
			description: "",
			wantErr:     true,
			errContains: []string{"name is required", "description is required"},
		},
		{
			name:        "name-with-spaces",
			skillName:   "skill with spaces",
			description: "invalid name with spaces",
			wantErr:     true,
			errContains: []string{"name must be alphanumeric with hyphens"},
		},
		{
			name:        "name-with-underscore",
			skillName:   "skill_underscore",
			description: "invalid name with underscore",
			wantErr:     true,
			errContains: []string{"name must be alphanumeric with hyphens"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			info := SkillInfo{
				Name:        tc.skillName,
				Description: tc.description,
			}
			err := info.validate()
			if tc.wantErr {
				assert.Error(t, err)
				for _, msg := range tc.errContains {
					assert.ErrorContains(t, err, msg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExtractFrontmatter(t *testing.T) {
	sl := &SkillsLoader{}

	testcases := []struct {
		name           string
		content        string
		expectedName   string
		expectedDesc   string
		lineEndingType string
	}{
		{
			name:           "unix-line-endings",
			lineEndingType: "Unix (\\n)",
			content:        "---\nname: test-skill\ndescription: A test skill\n---\n\n# Skill Content",
			expectedName:   "test-skill",
			expectedDesc:   "A test skill",
		},
		{
			name:           "windows-line-endings",
			lineEndingType: "Windows (\\r\\n)",
			content:        "---\r\nname: test-skill\r\ndescription: A test skill\r\n---\r\n\r\n# Skill Content",
			expectedName:   "test-skill",
			expectedDesc:   "A test skill",
		},
		{
			name:           "classic-mac-line-endings",
			lineEndingType: "Classic Mac (\\r)",
			content:        "---\rname: test-skill\rdescription: A test skill\r---\r\r# Skill Content",
			expectedName:   "test-skill",
			expectedDesc:   "A test skill",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			// Extract frontmatter
			frontmatter := sl.extractFrontmatter(tc.content)
			assert.NotEmpty(t, frontmatter, "Frontmatter should be extracted for %s line endings", tc.lineEndingType)

			// Parse YAML to get name and description (parseSimpleYAML now handles all line ending types)
			yamlMeta := sl.parseSimpleYAML(frontmatter)
			assert.Equal(
				t,
				tc.expectedName,
				yamlMeta["name"],
				"Name should be correctly parsed from frontmatter with %s line endings",
				tc.lineEndingType,
			)
			assert.Equal(
				t,
				tc.expectedDesc,
				yamlMeta["description"],
				"Description should be correctly parsed from frontmatter with %s line endings",
				tc.lineEndingType,
			)
		})
	}
}

// createSkillDir creates a skill directory with a SKILL.md file containing the given frontmatter.
func createSkillDir(t *testing.T, base, dirName, name, description string) {
	t.Helper()
	dir := filepath.Join(base, dirName)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n# " + name
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644))
}

// TestListSkillsParsesAuthorAndVersion verifies that the optional author/version
// YAML frontmatter keys are parsed through SkillMetadata into SkillInfo.
func TestListSkillsParsesAuthorAndVersion(t *testing.T) {
	tmp := t.TempDir()
	builtin := filepath.Join(tmp, "builtin")
	dir := filepath.Join(builtin, "fancy")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	content := "---\n" +
		"name: fancy\n" +
		"description: A fancy skill.\n" +
		"author: Jane Doe\n" +
		"version: 2.4.1\n" +
		"---\n\n# fancy\n\nDoes fancy things.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644))

	sl := NewSkillsLoader(filepath.Join(tmp, "ws"), filepath.Join(tmp, "global"), builtin)
	skills := sl.ListSkills()

	require.Len(t, skills, 1)
	assert.Equal(t, "fancy", skills[0].Name)
	assert.Equal(t, "builtin", skills[0].Source)
	assert.Equal(t, "Jane Doe", skills[0].Author)
	assert.Equal(t, "2.4.1", skills[0].Version)
}

// TestListSkillsSeparatesIDFromDisplayName verifies that a skill whose
// frontmatter name is a proper English display name (with spaces/capitals) is
// loaded successfully — its ID stays the directory slug while Name carries the
// display name. The display name must NOT be slug-validated (spaces are legal),
// but the slug ID still gates validation.
func TestListSkillsSeparatesIDFromDisplayName(t *testing.T) {
	tmp := t.TempDir()
	builtin := filepath.Join(tmp, "builtin")
	dir := filepath.Join(builtin, "daily-briefing")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	content := "---\n" +
		"name: Daily Briefing\n" +
		"description: Assemble a concise daily briefing.\n" +
		"---\n\n# Daily Briefing\n\nProduce a short briefing.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644))

	sl := NewSkillsLoader(filepath.Join(tmp, "ws"), filepath.Join(tmp, "global"), builtin)
	skills := sl.ListSkills()

	require.Len(t, skills, 1, "a skill with a spaced display name must still load")
	assert.Equal(t, "daily-briefing", skills[0].ID, "ID must be the directory slug")
	assert.Equal(t, "Daily Briefing", skills[0].Name, "Name must be the frontmatter display name")
	assert.Equal(t, "builtin", skills[0].Source)
}

// TestEmbeddedDefaultsHaveProperDisplayNames verifies the four embedded default
// skills ship with proper English display names while keeping their slug IDs.
func TestEmbeddedDefaultsHaveProperDisplayNames(t *testing.T) {
	tmp := t.TempDir()
	dest := filepath.Join(tmp, "skills")
	_, err := SeedDefaults(dest)
	require.NoError(t, err)

	sl := NewSkillsLoader(filepath.Join(tmp, "ws"), dest, "")
	bySlug := make(map[string]SkillInfo)
	for _, s := range sl.ListSkills() {
		bySlug[s.ID] = s
	}

	want := map[string]string{
		"daily-briefing":  "Daily Briefing",
		"plan":            "Plan",
		"skill-authoring": "Skill Authoring",
		"summarize":       "Summarize",
	}
	for slug, display := range want {
		got, ok := bySlug[slug]
		require.True(t, ok, "embedded default %q must be present", slug)
		assert.Equal(t, slug, got.ID, "ID is the slug")
		assert.Equal(t, display, got.Name, "display name for %q", slug)
	}
}

// TestListSkillsAuthorVersionAbsent verifies absent author/version yields empty
// strings (no error, back-compatible).
func TestListSkillsAuthorVersionAbsent(t *testing.T) {
	tmp := t.TempDir()
	builtin := filepath.Join(tmp, "builtin")
	createSkillDir(t, builtin, "plain", "plain", "A plain skill.")

	sl := NewSkillsLoader(filepath.Join(tmp, "ws"), filepath.Join(tmp, "global"), builtin)
	skills := sl.ListSkills()

	require.Len(t, skills, 1)
	assert.Empty(t, skills[0].Author)
	assert.Empty(t, skills[0].Version)
}

func TestListSkillsWorkspaceOverridesGlobal(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	global := filepath.Join(tmp, "global")

	createSkillDir(t, filepath.Join(ws, "skills"), "my-skill", "my-skill", "workspace version")
	createSkillDir(t, global, "my-skill", "my-skill", "global version")

	sl := NewSkillsLoader(ws, global, "")
	skills := sl.ListSkills()

	assert.Len(t, skills, 1)
	assert.Equal(t, "workspace", skills[0].Source)
	assert.Equal(t, "workspace version", skills[0].Description)
}

func TestListSkillsGlobalOverridesBuiltin(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	global := filepath.Join(tmp, "global")
	builtin := filepath.Join(tmp, "builtin")

	createSkillDir(t, global, "my-skill", "my-skill", "global version")
	createSkillDir(t, builtin, "my-skill", "my-skill", "builtin version")

	sl := NewSkillsLoader(ws, global, builtin)
	skills := sl.ListSkills()

	assert.Len(t, skills, 1)
	assert.Equal(t, "global", skills[0].Source)
	assert.Equal(t, "global version", skills[0].Description)
}

// TestListSkillsDistinctIDsAreDistinctSkills verifies that two skills with
// different directory slugs (IDs) are surfaced as two separate skills, even
// when their frontmatter display names happen to match. ID is the stable
// identifier (the directory name); Name is human-readable display text and
// may legitimately collide across distinct skills. This used to dedup by
// Name and was the root cause of the per-agent allowlist returning
// duplicate slugs — fixed by deduping on ID.
func TestListSkillsDistinctIDsAreDistinctSkills(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	global := filepath.Join(tmp, "global")

	// Different directory names (IDs) but same metadata display name.
	createSkillDir(t, filepath.Join(ws, "skills"), "dir-a", "shared-name", "workspace version")
	createSkillDir(t, global, "dir-b", "shared-name", "global version")

	sl := NewSkillsLoader(ws, global, "")
	skills := sl.ListSkills()

	assert.Len(t, skills, 2, "different IDs are distinct skills, regardless of Name")
	sources := map[string]string{}
	for _, s := range skills {
		sources[s.Source] = s.ID
	}
	assert.Equal(t, "dir-a", sources["workspace"])
	assert.Equal(t, "dir-b", sources["global"])
}

func TestListSkillsMultipleDistinctSkills(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	global := filepath.Join(tmp, "global")
	builtin := filepath.Join(tmp, "builtin")

	createSkillDir(t, filepath.Join(ws, "skills"), "skill-a", "skill-a", "desc a")
	createSkillDir(t, global, "skill-b", "skill-b", "desc b")
	createSkillDir(t, builtin, "skill-c", "skill-c", "desc c")

	sl := NewSkillsLoader(ws, global, builtin)
	skills := sl.ListSkills()

	assert.Len(t, skills, 3)
	names := map[string]string{}
	for _, s := range skills {
		names[s.Name] = s.Source
	}
	assert.Equal(t, "workspace", names["skill-a"])
	assert.Equal(t, "global", names["skill-b"])
	assert.Equal(t, "builtin", names["skill-c"])
}

func TestListSkillsInvalidSkillSkipped(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	global := filepath.Join(tmp, "global")

	// Invalid name (underscore)
	createSkillDir(t, filepath.Join(ws, "skills"), "bad_skill", "bad_skill", "desc")
	// Valid skill
	createSkillDir(t, global, "good-skill", "good-skill", "desc")

	sl := NewSkillsLoader(ws, global, "")
	skills := sl.ListSkills()

	assert.Len(t, skills, 1)
	assert.Equal(t, "good-skill", skills[0].Name)
}

func TestListSkillsEmptyAndNonexistentDirs(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	emptyDir := filepath.Join(tmp, "empty")
	require.NoError(t, os.MkdirAll(emptyDir, 0o755))

	sl := NewSkillsLoader(ws, emptyDir, filepath.Join(tmp, "nonexistent"))
	skills := sl.ListSkills()

	assert.Empty(t, skills)
}

func TestListSkillsDirWithoutSkillMD(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	global := filepath.Join(tmp, "global")

	// Directory exists but has no SKILL.md
	require.NoError(t, os.MkdirAll(filepath.Join(global, "no-skillmd"), 0o755))
	// Valid skill alongside
	createSkillDir(t, global, "real-skill", "real-skill", "desc")

	sl := NewSkillsLoader(ws, global, "")
	skills := sl.ListSkills()

	assert.Len(t, skills, 1)
	assert.Equal(t, "real-skill", skills[0].Name)
}

func TestStripFrontmatter(t *testing.T) {
	sl := &SkillsLoader{}

	testcases := []struct {
		name            string
		content         string
		expectedContent string
		lineEndingType  string
	}{
		{
			name:            "unix-line-endings",
			lineEndingType:  "Unix (\\n)",
			content:         "---\nname: test-skill\ndescription: A test skill\n---\n\n# Skill Content",
			expectedContent: "# Skill Content",
		},
		{
			name:            "windows-line-endings",
			lineEndingType:  "Windows (\\r\\n)",
			content:         "---\r\nname: test-skill\r\ndescription: A test skill\r\n---\r\n\r\n# Skill Content",
			expectedContent: "# Skill Content",
		},
		{
			name:            "classic-mac-line-endings",
			lineEndingType:  "Classic Mac (\\r)",
			content:         "---\rname: test-skill\rdescription: A test skill\r---\r\r# Skill Content",
			expectedContent: "# Skill Content",
		},
		{
			name:            "unix-line-endings-without-trailing-newline",
			lineEndingType:  "Unix (\\n) without trailing newline",
			content:         "---\nname: test-skill\ndescription: A test skill\n---\n# Skill Content",
			expectedContent: "# Skill Content",
		},
		{
			name:            "windows-line-endings-without-trailing-newline",
			lineEndingType:  "Windows (\\r\\n) without trailing newline",
			content:         "---\r\nname: test-skill\r\ndescription: A test skill\r\n---\r\n# Skill Content",
			expectedContent: "# Skill Content",
		},
		{
			name:            "no-frontmatter",
			lineEndingType:  "No frontmatter",
			content:         "# Skill Content\n\nSome content here.",
			expectedContent: "# Skill Content\n\nSome content here.",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			result := sl.stripFrontmatter(tc.content)
			assert.Equal(
				t,
				tc.expectedContent,
				result,
				"Frontmatter should be stripped correctly for %s",
				tc.lineEndingType,
			)
		})
	}
}

func TestSkillRootsTrimsWhitespaceAndDedups(t *testing.T) {
	tmp := t.TempDir()
	workspace := filepath.Join(tmp, "workspace")
	global := filepath.Join(tmp, "global")
	builtin := filepath.Join(tmp, "builtin")

	sl := NewSkillsLoader(workspace, "  "+global+"  ", "\t"+builtin+"\n")
	roots := sl.SkillRoots()

	assert.Equal(t, []string{
		filepath.Join(workspace, "skills"),
		global,
		builtin,
	}, roots)
}

func TestGetSkillMetadata_UsesMarkdownParagraphWhenNoFrontmatter(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "workspace", "skills", "plain-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	content := "# Plain Skill\n\nThis is parsed from markdown paragraph.\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644))

	sl := &SkillsLoader{}
	meta := sl.getSkillMetadata(filepath.Join(skillDir, "SKILL.md"))
	require.NotNil(t, meta)
	assert.Equal(t, "plain-skill", meta.Name)
	assert.Equal(t, "This is parsed from markdown paragraph.", meta.Description)
}

func TestGetSkillMetadata_FrontmatterOverridesMarkdown(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "workspace", "skills", "plain-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	content := "---\nname: frontmatter-skill\ndescription: frontmatter description\n---\n\n# Plain Skill\n\nBody description.\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644))

	sl := &SkillsLoader{}
	meta := sl.getSkillMetadata(filepath.Join(skillDir, "SKILL.md"))
	require.NotNil(t, meta)
	assert.Equal(t, "frontmatter-skill", meta.Name)
	assert.Equal(t, "frontmatter description", meta.Description)
}

func TestGetSkillMetadata_YAMLMultilineDescription(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "workspace", "skills", "plain-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	content := "---\nname: frontmatter-skill\ndescription: |\n  line 1: with colon\n  line 2\n---\n\n# Plain Skill\n\nBody description.\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644))

	sl := &SkillsLoader{}
	meta := sl.getSkillMetadata(filepath.Join(skillDir, "SKILL.md"))
	require.NotNil(t, meta)
	assert.Equal(t, "frontmatter-skill", meta.Name)
	assert.Equal(t, "line 1: with colon\nline 2", meta.Description)
}

func TestGetSkillMetadata_InvalidHeadingNameFallsBackToDirName(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "workspace", "skills", "valid-name")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	content := "# Invalid Heading Name\n\nBody description.\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644))

	sl := &SkillsLoader{}
	meta := sl.getSkillMetadata(filepath.Join(skillDir, "SKILL.md"))
	require.NotNil(t, meta)
	assert.Equal(t, "valid-name", meta.Name)
	assert.Equal(t, "Body description.", meta.Description)
}

func TestGetSkillMetadata_IgnoresHTMLCommentBlocks(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "workspace", "skills", "biomed-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	content := "<!--\n# COPYRIGHT NOTICE\n# This file is part of the \"Universal Biomedical Skills\" project.\n# Copyright (c) 2026 MD BABU MIA, PhD <md.babu.mia@mssm.edu>\n# All Rights Reserved.\n#\n# This code is proprietary and confidential.\n# Unauthorized copying of this file, via any medium is strictly prohibited.\n#\n# Provenance: Authenticated by MD BABU MIA\n\n-->\n\n# Biomed Skill\n\nSummarize biomedical papers.\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644))

	sl := &SkillsLoader{}
	meta := sl.getSkillMetadata(filepath.Join(skillDir, "SKILL.md"))
	require.NotNil(t, meta)
	assert.Equal(t, "biomed-skill", meta.Name)
	assert.Equal(t, "Summarize biomedical papers.", meta.Description)
}

// TestListSkills_SkipsDotPrefixedDirectories is the defensive-guard proof for
// the install_skill staging fix (pkg/tools/skills_install.go): a force=true
// reinstall stages its download under skillsDir/.staging/<slug>.install-XXXX/
// before renaming it into place. Even if that staging directory somehow ended
// up with a SKILL.md in it (a fully-extracted-but-not-yet-renamed skill, or a
// future staging-path change that lands directly in skillsDir), ListSkills
// must never surface it as an installed skill — a concurrent list_skills call
// or a system-prompt build mid-install must not see a phantom entry, and a
// staging directory orphaned by a crash must not become a permanent phantom.
func TestListSkills_SkipsDotPrefixedDirectories(t *testing.T) {
	tmp := t.TempDir()
	global := filepath.Join(tmp, "global")

	// A real, well-formed skill — the control.
	createSkillDir(t, global, "real-skill", "real-skill", "an actual installed skill")

	// A staging directory sitting directly in the scanned root, complete with
	// a SKILL.md, simulating either (a) the in-flight window of an install
	// before the .staging fix, or (b) a crash-orphaned leftover. Its name
	// pattern matches what os.MkdirTemp(skillsDir, "."+slug+".install-")
	// used to produce.
	createSkillDir(t, global, ".sneaky-skill.install-abc123", "sneaky-skill", "should never be listed")

	// The new dedicated staging root itself, with an in-flight install nested
	// inside it — must also never surface, even though .staging itself has no
	// SKILL.md directly inside it.
	createSkillDir(t, filepath.Join(global, ".staging"), "sneaky-skill.install-def456", "sneaky-skill", "mid-install, must not be listed")

	sl := NewSkillsLoader(tmp, global, "")
	infos := sl.ListSkills()

	ids := make([]string, 0, len(infos))
	for _, info := range infos {
		ids = append(ids, info.ID)
	}

	assert.Contains(t, ids, "real-skill", "the real skill must still be listed")
	assert.Len(t, ids, 1, "only the real skill may be listed; got: %v", ids)
	for _, id := range ids {
		assert.False(t, strings.HasPrefix(id, "."), "no dot-prefixed directory may ever be surfaced as a skill, got id %q", id)
	}
}
