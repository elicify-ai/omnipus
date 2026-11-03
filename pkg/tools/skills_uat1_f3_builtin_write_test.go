package tools

// UAT-1 F-3 (skills spec r15 §7.1 step 0, §10.1): a write_file or edit_file
// to a built-in skill with no earlier read returns BUILTIN_READ_ONLY with the
// copy hint, never SKILL_NOT_READ (a built-in read cannot satisfy the read
// precondition, so the agent looped on it).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinSkillWriteWithoutPriorRead_IsReadOnlyNotNotRead(t *testing.T) {
	ctx, home, agentHome := skillsTurnFixture(t, true)
	dir := filepath.Join(home, "skills", "plan")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	body := "---\nname: plan\ndescription: built-in fixture\n---\nBODY\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".omnipus-builtin"), []byte("1\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".omnipus-identity"), []byte("11111111-2222-3333-4444-555555555555\n"), 0o644))
	path := filepath.Join(dir, "SKILL.md")
	approved := WithSkillWriteApproval(ctx, "plan") // the loop's composition step (editor: edit_skill ask)

	cases := map[string]*ToolResult{
		"write_file": NewWriteFileTool(agentHome, true).Execute(approved, map[string]any{"path": path, "content": "CHANGED\n", "overwrite": true}),
		"edit_file":  NewEditFileTool(agentHome, true).Execute(approved, map[string]any{"path": path, "old_text": "BODY", "new_text": "CHANGED"}),
	}
	for name, res := range cases {
		t.Run(name, func(t *testing.T) {
			require.True(t, res.IsError, "the write is refused: %s", res.ForLLM)
			assert.NotContains(t, res.ForLLM, "SKILL_NOT_READ", "a built-in is read-only, not unread")
			assert.Contains(t, res.ForLLM, "BUILTIN_READ_ONLY")
			assert.Contains(t, res.ForLLM, "edit_skill action=copy skill=plan", "the copy hint")
		})
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, body, string(raw), "nothing changed")
}
