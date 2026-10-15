package systools_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// silent-failure-hunter SF-01: when the target workspace's mount record
// cannot be read, skill edit/remove/management listing must refuse with
// READ_FAILED. Treating the failure as "no project skill" would edit or remove
// a byte-identical global copy (same revision) instead of the target's own.
func TestWU3UnreadableTargetMountsRefuseInsteadOfGlobalFallback(t *testing.T) {
	d, ctx, _, target := wu3ProjectTarget(t)
	projectBody := wu3ReadFile(t, filepath.Join(target, "crm", "SKILL.md"))
	// A global copy with the same bytes, hence the same revision.
	globalDir := filepath.Join(d.Home, "skills", "crm")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "SKILL.md"), projectBody, 0o600))
	revision, err := skills.NewSkillWriter(target).SkillRevision("crm")
	require.NoError(t, err)
	globalRevision, err := skills.NewSkillWriter(filepath.Join(d.Home, "skills")).SkillRevision("crm")
	require.NoError(t, err)
	require.Equal(t, revision, globalRevision, "fixture: identical copies share a revision")

	storePath, err := workspace.MountStorePath(d.Home, "sales")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(storePath, []byte("{not json"), 0o600))

	edit := systools.NewSkillEditTool(d).Execute(ctx, map[string]any{
		"name": "crm", "content": validSkill("crm") + "\nEdited.\n", "revision": revision, "workspace_id": "sales",
	})
	body := wu3Error(t, edit, "READ_FAILED")
	assert.Contains(t, body["message"], "sales")
	wu3Outcome(t, edit, "failed: nothing saved")

	remove := systools.NewSkillRemoveTool(d).Execute(ctx, map[string]any{
		"name": "crm", "revision": revision, "confirm": true, "workspace_id": "sales",
	})
	wu3Error(t, remove, "READ_FAILED")
	wu3Outcome(t, remove, "failed: nothing saved")

	list := systools.NewSkillListTool(d).Execute(ctx, map[string]any{"scope": "management", "name": "crm", "workspace_id": "sales"})
	wu3Error(t, list, "READ_FAILED")

	assert.Equal(t, projectBody, wu3ReadFile(t, filepath.Join(target, "crm", "SKILL.md")), "project copy untouched")
	assert.Equal(t, projectBody, wu3ReadFile(t, filepath.Join(globalDir, "SKILL.md")), "global copy untouched")
}
