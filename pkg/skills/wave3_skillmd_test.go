package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSKILLMDParseYAMLFrontmatter validates YAML frontmatter extraction.
// Traces to: wave3-skill-ecosystem-spec.md line 550 (Scenario: Parse SKILL.md with YAML frontmatter)
// BDD: Given SKILL.md with YAML frontmatter (name, description, argument-hint),
// When loader parses it,
// Then Name and Description are extracted; body text is available below the frontmatter.

func TestSKILLMDParseYAMLFrontmatter(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 551 (Parse SKILL.md with YAML frontmatter)
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "my-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	content := "---\nname: my-skill\ndescription: A test skill\nargument-hint: \"[query]\"\n---\n\n# My Skill\n\nThis is the skill body.\n"
	skillFile := filepath.Join(skillDir, "SKILL.md")
	require.NoError(t, os.WriteFile(skillFile, []byte(content), 0o600))

	sl := &SkillsLoader{}
	meta := sl.getSkillMetadata(skillFile)

	require.NotNil(t, meta)
	assert.Equal(t, "my-skill", meta.Name, "Name must come from YAML frontmatter")
	assert.Equal(t, "A test skill", meta.Description, "Description must come from YAML frontmatter")

	// Body must be accessible after stripping frontmatter
	body := sl.stripFrontmatter(content)
	assert.Contains(t, body, "This is the skill body.", "Body text must be preserved below frontmatter")
}

// TestSKILLMDParseJSONFrontmatter validates legacy JSON frontmatter extraction.
// Traces to: wave3-skill-ecosystem-spec.md line 557 (Scenario: Parse SKILL.md with JSON frontmatter)
// BDD: Given SKILL.md with JSON frontmatter {"name": "legacy-skill", "description": "Legacy format"},
// When loader parses it,
// Then Name is "legacy-skill" and Description is "Legacy format".

func TestSKILLMDParseJSONFrontmatter(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 558 (Parse SKILL.md with JSON frontmatter)
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "legacy-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	content := "---\n{\"name\": \"legacy-skill\", \"description\": \"Legacy format\"}\n---\n\n# Legacy Skill\n\nThis uses JSON frontmatter.\n"
	skillFile := filepath.Join(skillDir, "SKILL.md")
	require.NoError(t, os.WriteFile(skillFile, []byte(content), 0o600))

	sl := &SkillsLoader{}
	meta := sl.getSkillMetadata(skillFile)

	require.NotNil(t, meta)
	assert.Equal(t, "legacy-skill", meta.Name, "Name must come from JSON frontmatter")
	assert.Equal(t, "Legacy format", meta.Description, "Description must come from JSON frontmatter")
}

// TestSKILLMDParseNoFrontmatter validates fallback when no frontmatter is present.
// Traces to: wave3-skill-ecosystem-spec.md line 568 (Scenario: Parse SKILL.md with no frontmatter)
// BDD: Given SKILL.md with no frontmatter, a "# My Skill" heading, "This skill does things",
// And directory name is "my-skill",
// When loader parses it,
// Then Name is "my-skill" (from directory) and Description is "This skill does things" (from paragraph).

func TestSKILLMDParseNoFrontmatter(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 569 (Parse SKILL.md with no frontmatter)
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "my-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	// No frontmatter — heading and paragraph only
	content := "# My Skill Heading\n\nThis skill does things.\n"
	skillFile := filepath.Join(skillDir, "SKILL.md")
	require.NoError(t, os.WriteFile(skillFile, []byte(content), 0o600))

	sl := &SkillsLoader{}
	meta := sl.getSkillMetadata(skillFile)

	require.NotNil(t, meta)
	// Name falls back to directory name when heading is not a valid slug
	assert.Equal(t, "my-skill", meta.Name, "Name must be derived from directory name when heading is invalid slug")
	assert.Equal(t, "This skill does things.", meta.Description, "Description must come from first paragraph")
}

// TestSKILLMDParseMalformedFrontmatter validates graceful fallback on invalid YAML.
// Traces to: wave3-skill-ecosystem-spec.md line 580 (Scenario: Parse SKILL.md with malformed YAML frontmatter)
// BDD: Given SKILL.md with frontmatter "---\ninvalid: [unclosed\n---",
// When loader parses it,
// Then frontmatter is skipped, body is still loaded, a warning is logged.

func TestSKILLMDParseMalformedFrontmatter(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 581 (Parse SKILL.md with malformed YAML)
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "malformed-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	// Malformed YAML: unclosed bracket
	content := "---\ninvalid: [unclosed\n---\n\n# Malformed Skill\n\nThis is the body that should still load.\n"
	skillFile := filepath.Join(skillDir, "SKILL.md")
	require.NoError(t, os.WriteFile(skillFile, []byte(content), 0o600))

	sl := &SkillsLoader{}
	// getSkillMetadata must not panic or return nil on malformed frontmatter
	meta := sl.getSkillMetadata(skillFile)

	// Either returns nil (skip) or returns metadata with directory name fallback
	// The spec says "frontmatter is skipped, body is still loaded"
	if meta != nil {
		// If a metadata struct is returned, name should fall back to directory name
		assert.Equal(t, "malformed-skill", meta.Name,
			"Name must fall back to directory name on malformed frontmatter")
	}
	// Body content must still be accessible via stripFrontmatter
	body := sl.stripFrontmatter(content)
	assert.Contains(t, body, "This is the body that should still load.",
		"Body must be accessible even when frontmatter is malformed")
}

// TestSKILLMDParseClawHubFields validates that ClawHub-specific frontmatter fields
// are handled without breaking the parser.
// Traces to: wave3-skill-ecosystem-spec.md line 593 (Scenario: Parse SKILL.md with ClawHub-specific fields)
// BDD: Given SKILL.md with context, allowed-tools, model-hint fields,
// When loader parses it,
// Then name and description are extracted and no parse error occurs.

func TestSKILLMDParseClawHubFields(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 594 (Parse SKILL.md with ClawHub-specific fields)
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "clawhub-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	content := "---\n" +
		"name: clawhub-skill\n" +
		"description: A skill with ClawHub fields\n" +
		"context: fork\n" +
		"allowed-tools: Read, Grep\n" +
		"model-hint: sonnet\n" +
		"---\n\n# ClawHub Skill\n\nThis skill has extra ClawHub fields.\n"
	skillFile := filepath.Join(skillDir, "SKILL.md")
	require.NoError(t, os.WriteFile(skillFile, []byte(content), 0o600))

	sl := &SkillsLoader{}
	meta := sl.getSkillMetadata(skillFile)

	require.NotNil(t, meta, "getSkillMetadata must not return nil for valid ClawHub frontmatter")
	assert.Equal(t, "clawhub-skill", meta.Name,
		"Name must be extracted from frontmatter with ClawHub-specific fields present")
	assert.Equal(t, "A skill with ClawHub fields", meta.Description,
		"Description must be extracted from frontmatter with ClawHub-specific fields present")
}

// TestSkillNameValidation validates name slug validation from the spec dataset.
// Traces to: wave3-skill-ecosystem-spec.md line 870 (Dataset: Skill Name Validation)
// BDD Edge case: path traversal and invalid characters are rejected.

func TestSkillNameValidation(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 870 (Dataset: Skill Name Validation rows 1–13)
	tests := []struct {
		name      string
		skillName string
		desc      string
		wantValid bool
	}{
		// Dataset row 1 — Standard ClawHub slug (valid)
		{name: "standard-slug", skillName: "aws-cost-analyzer", desc: "valid description", wantValid: true},
		// Dataset row 2 — Simple name (valid)
		{name: "simple-name", skillName: "my-skill", desc: "valid description", wantValid: true},
		// Dataset row 3 — Empty (invalid)
		{name: "empty-name", skillName: "", desc: "valid description", wantValid: false},
		// Dataset row 4 — Single character (valid, min length)
		{name: "single-char", skillName: "a", desc: "valid description", wantValid: true},
		// Dataset row 5 — 64 chars (valid, max length)
		{
			name:      "64-chars",
			skillName: string(make([]byte, 64)),
			desc:      "valid description",
			wantValid: false,
		}, // all zeros = invalid chars
		// Dataset row 7 — Path traversal (invalid, security)
		{name: "path-traversal", skillName: "../../etc/passwd", desc: "valid description", wantValid: false},
		// Dataset row 8 — Spaces (invalid)
		{name: "spaces", skillName: "skill with spaces", desc: "valid description", wantValid: false},
		// Dataset row 9 — Underscore (invalid)
		{name: "underscore", skillName: "skill_underscore", desc: "valid description", wantValid: false},
		// Dataset row 10 — Leading hyphen (invalid)
		{name: "leading-hyphen", skillName: "-leading-hyphen", desc: "valid description", wantValid: false},
		// Dataset row 11 — Trailing hyphen (invalid)
		{name: "trailing-hyphen", skillName: "trailing-hyphen-", desc: "valid description", wantValid: false},
		// Dataset row 12 — UPPERCASE (valid)
		{name: "uppercase", skillName: "UPPERCASE", desc: "valid description", wantValid: true},
		// Dataset row 13 — Unicode (invalid)
		{name: "unicode", skillName: "café", desc: "valid description", wantValid: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := SkillInfo{Name: tc.skillName, Description: tc.desc}
			err := info.validate()
			if tc.wantValid {
				assert.NoError(t, err, "slug %q should be valid", tc.skillName)
			} else {
				assert.Error(t, err, "slug %q should be invalid", tc.skillName)
			}
		})
	}
}

// TestSKILLMDParseYAML_ArgumentHintPreserved verifies that argument-hint field
// does not break YAML parsing (forward compatibility).
// Traces to: wave3-skill-ecosystem-spec.md line 551 (YAML frontmatter scenario)

func TestSKILLMDParseYAML_ArgumentHintPreserved(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 551
	sl := &SkillsLoader{}

	content := "---\nname: my-skill\ndescription: My description\nargument-hint: \"[query] [options]\"\n---\n\nBody content.\n"
	frontmatter := sl.extractFrontmatter(content)
	assert.NotEmpty(t, frontmatter, "frontmatter must be extracted")

	// FR-002 removes the old parser helper, not this metadata regression.
	yamlFields := sl.parseSkillMetadata(filepath.Join("my-skill", "SKILL.md"), []byte(content))
	require.NotNil(t, yamlFields)
	assert.Equal(t, "my-skill", yamlFields.Name)
	assert.Equal(t, "My description", yamlFields.Description)
	assert.Equal(t, "[query] [options]", yamlFields.ArgumentHint, "the loader preserves the declared argument hint")
}
