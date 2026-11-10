package systools_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Train 1 UAT item 4's read-isolation intent survives r15 §§5.2/5.3/§15:
// installed objects use get_skill, project files use explicit file reads, and
// an incomplete pure management list refuses. Named-list content lookup and
// its implicit project-to-global fallback are withdrawn, not reimplemented.
func TestTrain1SelectedSkillReadsIsolateUnreadableSibling(t *testing.T) {
	s := wu3fix5SalesShelfWithContentFault(t, nil)
	s.deps.SkillsLoader = skills.NewSkillsLoader(t.TempDir(), filepath.Join(s.deps.Home, "skills"), "")
	const sibling = "train1-readable-control"
	const projectBody = "# Readable project control\n\nSales project content.\n"
	writeManagementSkill(t, s.projectRoot, sibling, projectBody)
	writeManagementSkill(t, filepath.Join(s.deps.Home, "skills"), sibling, "Separate installed object.\n")
	writeManagementSkill(t, filepath.Join(s.deps.Home, "skills"), "global-control", "Global control body.\n")
	projectPath := filepath.Join(s.projectRoot, sibling, "SKILL.md")
	projectBefore := wu3ReadFile(t, projectPath)
	// Snapshot AFTER all fixture additions, before the operation/fault.
	s.inventoryBefore = wu3fix5DurableInventory(t, s.deps.Home)
	wu3fix5MakeProjectContentUnreadable(t, s)
	_, err := os.ReadDir(s.projectRoot)
	require.NoError(t, err, "the shelf still lists; only crm's instruction bytes are unreadable")

	t.Run("explicit healthy project file read does not read broken sibling", func(t *testing.T) {
		result := tools.NewReadFileTool(s.mountRoot, true, 0).Execute(s.ctx, map[string]any{"path": projectPath})
		require.False(t, result.IsError, result.ForLLM)
		assert.Contains(t, result.ForLLM, projectBody)
		assert.NotContains(t, result.ForLLM, "Separate installed object.")
	})
	for _, name := range []string{sibling, "TRAIN1-READABLE-CONTROL"} {
		t.Run("installed_"+name, func(t *testing.T) {
			result := systools.NewGetSkillTool(s.deps).Execute(s.ctx, map[string]any{"id": name, "fields": []any{"instructions", "shelf"}})
			body := wu3Success(t, result)
			assert.Equal(t, sibling, body["id"], "§5.13 case-folded identity returns disk spelling")
			assert.Equal(t, "registry", body["shelf"])
			assert.Equal(t, "Separate installed object.\n", body["instructions"])
		})
	}
	t.Run("installed control is not masked by unrelated project failure", func(t *testing.T) {
		body := wu3Success(t, systools.NewGetSkillTool(s.deps).Execute(s.ctx, map[string]any{"id": "global-control", "fields": []any{"instructions", "shelf"}}))
		assert.Equal(t, "global-control", body["id"])
		assert.Equal(t, "registry", body["shelf"])
		assert.Equal(t, "Global control body.\n", body["instructions"])
	})
	t.Run("authorized missing installed object stays not found", func(t *testing.T) {
		body := wu3Error(t, systools.NewGetSkillTool(s.deps).Execute(s.ctx, map[string]any{"id": "absent-control", "fields": []any{"instructions"}}), "NOT_FOUND")
		assert.Equal(t, "skill \"absent-control\" is not installed", body["message"])
	})
	t.Run("unreadable explicit project target cannot return global bytes", func(t *testing.T) {
		result := tools.NewReadFileTool(s.mountRoot, true, 0).Execute(s.ctx, map[string]any{"path": s.projectSkill})
		require.True(t, result.IsError, "directory-at-instruction fault must refuse: %s", result.ForLLM)
		assert.NotContains(t, result.ForLLM, string(s.globalBefore))
		assert.Equal(t, s.globalBefore, wu3ReadFile(t, s.globalSkill))
	})
	t.Run("named list API is explicitly retired", func(t *testing.T) {
		body := wu3Error(t, systools.NewSkillListTool(s.deps).Execute(s.ctx, map[string]any{"scope": "management", "workspace_id": "sales", "name": sibling}), "INVALID_INPUT")
		assert.Equal(t, "name was removed from list_skills; use get_skill with id and fields", body["message"])
	})
	t.Run("full inventory still refuses incomplete scan", func(t *testing.T) {
		wu3Error(t, systools.NewSkillListTool(s.deps).Execute(s.ctx, map[string]any{"scope": "management", "workspace_id": "sales"}), "READ_FAILED")
	})
	assert.Equal(t, projectBefore, wu3ReadFile(t, projectPath))
	wu3fix5AssertRefusalChangedNothing(t, s)
}
