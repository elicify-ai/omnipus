package systools_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Train 1 UAT item 4: reading one named skill must not read an unrelated
// sibling's contents. FR-042 still requires a failed target read to refuse,
// never to fall through to a global copy. Real tools and shelves throughout.
func TestTrain1ManagementNamedLookupIsolatesUnreadableSibling(t *testing.T) {
	t.Run("root-independent content failure", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, nil)
		// Keep loader workspace/global roots distinct: this read must prove
		// fallback to a user skill, not the legacy workspace loader shelf.
		s.deps.SkillsLoader = skills.NewSkillsLoader(t.TempDir(), filepath.Join(s.deps.Home, "skills"), "")
		const sibling = "train1-readable-control"
		const projectBody = "# Readable project control\n\nSales project content.\n"
		writeManagementSkill(t, s.projectRoot, sibling, projectBody)
		writeManagementSkill(t, filepath.Join(s.deps.Home, "skills"), sibling, "Global shadow must not win.\n")
		writeManagementSkill(t, filepath.Join(s.deps.Home, "skills"), "global-control", "Global control body.\n")
		revision, err := skills.NewSkillWriter(s.projectRoot).SkillRevision(sibling)
		require.NoError(t, err)

		wu3fix5MakeProjectContentUnreadable(t, s)
		_, err = os.ReadDir(s.projectRoot)
		require.NoError(t, err, "the shelf must still list, so only the other skill's content is unreadable")

		tool := systools.NewSkillListTool(s.deps)
		for _, name := range []string{sibling, "TRAIN1-READABLE-CONTROL"} {
			t.Run(name, func(t *testing.T) {
				result := tool.Execute(s.ctx, map[string]any{"scope": "management", "workspace_id": "sales", "name": name})
				body := wu3Success(t, result)
				require.Equal(t, float64(1), body["count"])
				items, ok := body["skills"].([]any)
				require.True(t, ok)
				require.Len(t, items, 1)
				assert.Equal(t, map[string]any{
					"id": sibling, "name": sibling, "description": "managed " + sibling,
					"origin": "project", "shared_impact": "workspace_members", "mount": "project",
					"author": "public-author", "revision": revision, "content": projectBody,
				}, wu3Object(t, items[0]))
			})
		}

		t.Run("global control is not masked by unrelated project failure", func(t *testing.T) {
			body := wu3Success(t, tool.Execute(s.ctx, map[string]any{
				"scope": "management", "workspace_id": "sales", "name": "global-control",
			}))
			require.Equal(t, float64(1), body["count"])
			items, ok := body["skills"].([]any)
			require.True(t, ok)
			require.Len(t, items, 1)
			item := wu3Object(t, items[0])
			assert.Equal(t, "global-control", item["id"])
			assert.Equal(t, "user", item["origin"])
			assert.Equal(t, "Global control body.\n", item["content"])
		})
		t.Run("confirmed missing name stays not found", func(t *testing.T) {
			wu3Error(t, tool.Execute(s.ctx, map[string]any{
				"scope": "management", "workspace_id": "sales", "name": "absent-control",
			}), "NOT_FOUND")
		})
		t.Run("unreadable target never falls back to global", func(t *testing.T) {
			wu3Error(t, tool.Execute(s.ctx, map[string]any{
				"scope": "management", "workspace_id": "sales", "name": "crm",
			}), "READ_FAILED")
			assert.Equal(t, s.globalBefore, wu3ReadFile(t, s.globalSkill))
		})
		t.Run("full inventory still refuses incomplete scan", func(t *testing.T) {
			wu3Error(t, tool.Execute(s.ctx, map[string]any{
				"scope": "management", "workspace_id": "sales",
			}), "READ_FAILED")
		})
	})
}
