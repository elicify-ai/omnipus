package systools_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// silent-failure-hunter SF-01 (remaining part): the mount record is readable,
// but discovery inside the mounted project is incomplete — the project folder
// is unavailable, its recognised skills folder cannot be listed, or the record
// had entries dropped as invalid. A workspace-specific edit/remove/listing must
// then refuse with READ_FAILED, never fall back to an identical global copy
// (same revision) and report working.
func TestWU3IncompleteProjectDiscoveryRefusesInsteadOfGlobalFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(t *testing.T, home, targetSkills string)
	}{
		{"project folder unavailable", func(t *testing.T, _, targetSkills string) {
			root := filepath.Dir(filepath.Dir(targetSkills))
			require.NoError(t, os.Rename(root, root+"-gone"))
		}},
		{"skills folder cannot be listed", func(t *testing.T, _, targetSkills string) {
			require.NoError(t, os.RemoveAll(targetSkills))
			require.NoError(t, os.WriteFile(targetSkills, []byte("not a directory"), 0o600))
		}},
		{"mount record dropped an invalid entry", func(t *testing.T, home, _ string) {
			path, err := workspace.MountStorePath(home, "sales")
			require.NoError(t, err)
			var rec map[string]any
			require.NoError(t, json.Unmarshal(wu3ReadFile(t, path), &rec))
			mounts, ok := rec["mounts"].([]any)
			require.True(t, ok)
			rec["mounts"] = append(mounts, mounts[0]) // a duplicate name is dropped on load
			data, err := json.Marshal(rec)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, data, 0o600))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, ctx, _, target := wu3ProjectTarget(t)
			globalDir := filepath.Join(d.Home, "skills", "crm")
			require.NoError(t, os.MkdirAll(globalDir, 0o700))
			globalBody := wu3ReadFile(t, filepath.Join(target, "crm", "SKILL.md"))
			require.NoError(t, os.WriteFile(filepath.Join(globalDir, "SKILL.md"), globalBody, 0o600))
			revision, err := skills.NewSkillWriter(filepath.Join(d.Home, "skills")).SkillRevision("crm")
			require.NoError(t, err)

			tc.break_(t, d.Home, target)

			edit := systools.NewSkillEditTool(d).Execute(ctx, map[string]any{
				"name": "crm", "content": validSkill("crm") + "\nEdited.\n", "revision": revision, "workspace_id": "sales",
			})
			wu3Error(t, edit, "READ_FAILED")
			wu3Outcome(t, edit, "failed: nothing saved")
			remove := systools.NewSkillRemoveTool(d).Execute(ctx, map[string]any{
				"name": "crm", "revision": revision, "confirm": true, "workspace_id": "sales",
			})
			wu3Error(t, remove, "READ_FAILED")
			list := systools.NewSkillListTool(d).Execute(ctx, map[string]any{"scope": "management", "name": "crm", "workspace_id": "sales"})
			wu3Error(t, list, "READ_FAILED")
			assert.Equal(t, globalBody, wu3ReadFile(t, filepath.Join(globalDir, "SKILL.md")), "global copy untouched")
		})
	}
}

// Control: a healthy, fully inspected project that has no such skill still
// falls back to the installation-wide copy, as documented.
func TestWU3HealthyProjectWithoutTheSkillStillFallsBackToGlobal(t *testing.T) {
	d, ctx, _, _ := wu3ProjectTarget(t)
	globalRoot := filepath.Join(d.Home, "skills")
	_, revision, err := skills.NewSkillWriter(globalRoot).CreateSkillReviewed("solo", validSkill("solo"))
	require.NoError(t, err)
	content := validSkill("solo") + "\nEdited globally.\n"
	res := systools.NewSkillEditTool(d).Execute(ctx, map[string]any{
		"name": "solo", "content": content, "revision": revision, "workspace_id": "sales",
	})
	wu3Outcome(t, res, "working")
	assert.Equal(t, content, string(wu3ReadFile(t, filepath.Join(globalRoot, "solo", "SKILL.md"))))
}
