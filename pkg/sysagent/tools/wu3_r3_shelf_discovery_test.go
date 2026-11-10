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
// had entries dropped as invalid. r15 §5.3 management listing must refuse
// READ_FAILED; §5.4 removed mutation targeting must reject its old arguments.
// The old automatic mutation fallback has no r15 contract.
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
			_, err := skills.NewSkillWriter(filepath.Join(d.Home, "skills")).SkillRevision("crm")
			require.NoError(t, err)

			tc.break_(t, d.Home, target)

			assertRemovedSkillMutationArgs(t, d, ctx, "crm", "sales")
			list := systools.NewSkillListTool(d).Execute(ctx, map[string]any{"scope": "management", "workspace_id": "sales"})
			wu3Error(t, list, "READ_FAILED")

			assert.Equal(t, globalBody, wu3ReadFile(t, filepath.Join(globalDir, "SKILL.md")), "global copy untouched")
		})
	}
}

// A healthy complete project scan can establish absence; the explicit
// installed reader still works. No obsolete slug-to-project write fallback.
func TestWU3HealthyProjectWithoutTheSkillAllowsInstalledRead(t *testing.T) {
	d, ctx, _, target := wu3ProjectTarget(t)
	root := filepath.Join(d.Home, "skills")
	_, _, err := skills.NewSkillWriter(root).CreateSkillReviewed("solo", validSkill("solo"))
	require.NoError(t, err)
	shelf, collisions, err := skills.LookupProjectSkillChecked([]skills.ProjectMount{{Name: "sales", Root: filepath.Dir(filepath.Dir(target))}}, "solo")
	require.NoError(t, err)
	assert.Empty(t, shelf)
	assert.Empty(t, collisions)
	result := systools.NewGetSkillTool(d).Execute(ctx, map[string]any{"id": "solo", "fields": []any{"instructions", "shelf"}})
	body := wu3Success(t, result)
	assert.Equal(t, "registry", body["shelf"])
	assert.Contains(t, body["instructions"], "Does the thing.")
	assert.Equal(t, validSkill("solo"), string(wu3ReadFile(t, filepath.Join(root, "solo", "SKILL.md"))))
}
