package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1331: allowed-tools accepts space-separated, comma-separated and YAML-list
// forms, trimming entries. These exact tokens come from the issue's example,
// not from DiscoverAllTools (which currently has no production caller).
func TestTrain1Issue1331AllowedToolsParsesSupportedForms(t *testing.T) {
	threeTools := []string{"Read", "Grep", "Bash"}
	for _, row := range []struct {
		name, frontmatter string
		want              []string
	}{
		{"space_separated", "allowed-tools: Read Grep Bash\n", threeTools},
		{"space_separated_with_tabs_and_padding", "allowed-tools: \"  Read\\tGrep   Bash  \"\n", threeTools},
		{"comma_separated_control", "allowed-tools: Read,Grep,Bash\n", threeTools},
		{"comma_separated_trimmed", "allowed-tools: '  Read , Grep , Bash  '\n", threeTools},
		{"yaml_block_list_control", "allowed-tools:\n  - Read\n  - Grep\n  - Bash\n", threeTools},
		{"yaml_block_list_trimmed", "allowed-tools:\n  - '  Read  '\n  - \"\\tGrep\\t\"\n  - ' Bash '\n", threeTools},
		{"yaml_flow_list_trimmed", "allowed-tools: [' Read ', 'Grep ', ' Bash']\n", threeTools},
		{"single_tool_trimmed", "allowed-tools: '  Read  '\n", []string{"Read"}},
		{"omitted_has_no_tools", "", nil},
		{"empty_scalar_has_no_tools", "allowed-tools: ''\n", nil},
		{"empty_yaml_list_has_no_tools", "allowed-tools: []\n", nil},
		{"whitespace_only_has_no_tools", "allowed-tools: \" \\t  \"\n", nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", root)
			t.Setenv("OMNIPUS_HOME", filepath.Join(root, ".omnipus"))
			skillDir := filepath.Join(root, "workspace", "skills", "allowed-tools-fixture")
			require.NoError(t, os.MkdirAll(skillDir, 0o700))
			path := filepath.Join(skillDir, "SKILL.md")
			content := "---\nname: allowed-tools-fixture\ndescription: Allowed tools parsing fixture.\n" + row.frontmatter + "---\n\n# Allowed tools fixture\n"
			require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

			loader := NewSkillsLoader(filepath.Join(root, "workspace"), filepath.Join(root, "global"), filepath.Join(root, "builtin"))
			metadata := loader.getSkillMetadata(path)
			require.NotNil(t, metadata, "the real SKILL.md must load before testing its tool list")
			assert.Equal(t, "allowed-tools-fixture", metadata.Name, "instrument control: this fixture's frontmatter was read")
			assert.Equal(t, "Allowed tools parsing fixture.", metadata.Description)
			if len(row.want) == 0 {
				// The issue specifies no tool entries, not nil vs allocated-empty.
				assert.Empty(t, metadata.AllowedTools, "empty declarations must not invent a whitespace tool name")
			} else {
				assert.Equal(t, row.want, metadata.AllowedTools, "#1331: preserve every declared tool, trimmed, in order")
			}
		})
	}
}
