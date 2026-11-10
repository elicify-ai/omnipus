package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// r15 §§5.1–5.5/§15: project files stay ordinary mounted files. Preserve
// the deterministic intermediate-component fault and unchanged-byte checks at
// real file edit, project writer and checked management-list boundaries.
// Removed implicit mutation fallback is explicitly retired in traceability.
const wu3fix4PlantedFile = "regular file where the .claude directory used to be"

// wu3fix4Shelf is one fixture: the sales mount of wu3ProjectTarget with the
// installation-wide crm copy seeded byte-identical to the sales project skill
// (identical bytes, so the shared revision makes a wrong global fallback
// genuinely reachable rather than refused as stale).
type wu3fix4Shelf struct {
	deps         *systools.Deps
	ctx          context.Context
	globalSkill  string // installation-wide crm/SKILL.md
	projectSkill string // sales project crm/SKILL.md at its normal path
	anchorSkill  string // default workspace's project crm/SKILL.md
	claudeDir    string // the sales mount's .claude parent (the faulted path)
	asideDir     string // where the intact .claude tree waits while faulted
	asideSkill   string // the displaced project copy's path while faulted
	revision     string // the shared revision of the identical copies

	globalBefore  []byte
	projectBefore []byte
	anchorBefore  []byte
}

// wu3fix4FaultedSalesShelf builds the fixture and then applies break_ to it.
func wu3fix4FaultedSalesShelf(t *testing.T, break_ func(t *testing.T, s wu3fix4Shelf)) wu3fix4Shelf {
	t.Helper()
	d, ctx, anchor, target := wu3ProjectTarget(t)
	s := wu3fix4Shelf{
		deps:         d,
		ctx:          ctx,
		projectSkill: filepath.Join(target, "crm", "SKILL.md"),
		anchorSkill:  filepath.Join(anchor, "crm", "SKILL.md"),
		claudeDir:    filepath.Dir(target),
	}
	s.asideDir = s.claudeDir + "-aside"
	s.asideSkill = filepath.Join(s.asideDir, "skills", "crm", "SKILL.md")
	globalDir := filepath.Join(d.Home, "skills", "crm")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	s.globalSkill = filepath.Join(globalDir, "SKILL.md")
	s.projectBefore = wu3ReadFile(t, s.projectSkill)
	s.globalBefore = s.projectBefore // seeded byte-identical to the project copy
	s.anchorBefore = wu3ReadFile(t, s.anchorSkill)
	require.NoError(t, os.WriteFile(s.globalSkill, s.globalBefore, 0o600),
		"the global copy is seeded byte-identical to the project skill")
	revision, err := skills.NewSkillWriter(filepath.Join(d.Home, "skills")).SkillRevision("crm")
	require.NoError(t, err)
	s.revision = revision
	break_(t, s)
	return s
}

// wu3fix4BreakParentWithRegularFile is the finding's fault: the .claude
// parent of the recognised skills folder becomes a regular file while the
// real project tree sits intact beside it (recoverable). Resolving
// .claude/skills now fails on the INTERMEDIATE component — a different error
// class from NotExist and from the last-component faults the existing
// negative test already covers.
func wu3fix4BreakParentWithRegularFile(t *testing.T, s wu3fix4Shelf) {
	t.Helper()
	require.NoError(t, os.Rename(s.claudeDir, s.asideDir))
	require.NoError(t, os.WriteFile(s.claudeDir, []byte(wu3fix4PlantedFile), 0o600))
}

// wu3fix4AssertRefusalChangedNothing proves the refusal changed nothing
// anywhere: both copies byte-identical, the anchor untouched, nothing written
// through the faulted parent, and the displaced project tree intact and
// recoverable through its normal path after the restore.
func wu3fix4AssertRefusalChangedNothing(t *testing.T, s wu3fix4Shelf) {
	t.Helper()
	assert.Equal(t, string(s.globalBefore), string(wu3ReadFile(t, s.globalSkill)), "the identical global copy is untouched")
	assert.Equal(t, string(s.projectBefore), string(wu3ReadFile(t, s.asideSkill)), "the displaced project copy is intact and recoverable")
	assert.Equal(t, string(s.anchorBefore), string(wu3ReadFile(t, s.anchorSkill)), "the anchor workspace's project copy is untouched")
	assert.Equal(t, wu3fix4PlantedFile, string(wu3ReadFile(t, s.claudeDir)), "nothing wrote through the faulted parent")
	require.NoError(t, os.Remove(s.claudeDir), "remove the planted file")
	require.NoError(t, os.Rename(s.asideDir, s.claudeDir), "restore the displaced project tree")
	assert.Equal(t, string(s.projectBefore), string(wu3ReadFile(t, s.projectSkill)),
		"the project copy reads unchanged at its normal path after the restore")
}

func TestWU3IntermediateResolutionFailureRefusesWorkspaceSkillActions(t *testing.T) {
	t.Run("explicit project file edit refuses without touching global", func(t *testing.T) {
		s := wu3fix4FaultedSalesShelf(t, wu3fix4BreakParentWithRegularFile)
		edit := tools.NewEditFileTool(filepath.Dir(s.claudeDir), true).Execute(s.ctx, map[string]any{"path": s.projectSkill, "old_text": string(s.projectBefore), "new_text": "Must not land globally."})
		require.True(t, edit.IsError, "unresolvable project file must refuse: %s", edit.ForLLM)
		_, _, err := skills.LookupProjectSkillChecked([]skills.ProjectMount{{Name: "sales", Root: filepath.Dir(s.claudeDir)}}, "crm")
		require.Error(t, err, "intermediate resolution failure must not become confirmed absence")
		wu3fix4AssertRefusalChangedNothing(t, s)
	})
	t.Run("project writer deletion refuses without touching global", func(t *testing.T) {
		s := wu3fix4FaultedSalesShelf(t, wu3fix4BreakParentWithRegularFile)
		writer := skills.NewSkillWriter(filepath.Join(s.claudeDir, "skills"))
		require.Error(t, writer.RemoveSkillReviewed("crm", s.revision))
		wu3fix4AssertRefusalChangedNothing(t, s)
	})
	t.Run("management list refuses incomplete shelf", func(t *testing.T) {
		s := wu3fix4FaultedSalesShelf(t, wu3fix4BreakParentWithRegularFile)
		listed := systools.NewSkillListTool(s.deps).Execute(s.ctx, map[string]any{"scope": "management", "workspace_id": "sales"})
		wu3Error(t, listed, "READ_FAILED")
		wu3fix4AssertRefusalChangedNothing(t, s)
	})
	t.Run("confirmed absence is complete and installed read is explicit", func(t *testing.T) {
		s := wu3fix4FaultedSalesShelf(t, func(t *testing.T, s wu3fix4Shelf) {
			require.NoError(t, os.RemoveAll(filepath.Join(s.claudeDir, "skills")))
		})
		shelf, collisions, err := skills.LookupProjectSkillChecked([]skills.ProjectMount{{Name: "sales", Root: filepath.Dir(s.claudeDir)}}, "crm")
		require.NoError(t, err)
		assert.Empty(t, shelf)
		assert.Empty(t, collisions)
		result := systools.NewGetSkillTool(s.deps).Execute(s.ctx, map[string]any{"id": "crm", "fields": []any{"instructions", "shelf"}})
		body := wu3Success(t, result)
		assert.Equal(t, "registry", body["shelf"])
		assert.Contains(t, body["instructions"], "sales-only-body")
		assert.Equal(t, s.globalBefore, wu3ReadFile(t, s.globalSkill))
		assert.Equal(t, s.anchorBefore, wu3ReadFile(t, s.anchorSkill))
	})
}
