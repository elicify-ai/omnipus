package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CHECK R3-01 (silent-failure-hunter SF-01, remaining part): a mount whose
// recognised skills folder cannot be INSPECTED is not a confirmed absence.
// The fixture reproduces the finding's deterministic fault — the .claude
// parent of the recognised skills folder is replaced by a regular file while
// the real project tree is moved aside intact — so resolving
// .claude/skills fails with a non-NotExist error (ENOTDIR on an intermediate
// component). A workspace-scoped edit_skill / remove_skill / management list
// must then refuse READ_FAILED and change nothing; it must never fall back to
// the byte-identical installation-wide copy (same revision) and report
// working. Each action runs on its own fixture so a first mutation can never
// hide the next action's behaviour.
//
// Oracles: FR-032 effectful outcomes (READ_FAILED refusal, "failed: nothing
// saved"); ADR-072 D4.1/D6 project-shelf precedence — only a COMPLETE scan
// may prove a slug absent, so a healthy confirmed absence (the recognised
// directory genuinely gone, NotExist) still falls back to the global copy.
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
	t.Run("edit refuses instead of editing the identical global copy", func(t *testing.T) {
		s := wu3fix4FaultedSalesShelf(t, wu3fix4BreakParentWithRegularFile)
		edit := systools.NewSkillEditTool(s.deps).Execute(s.ctx, map[string]any{
			"name": "crm", "content": string(s.projectBefore) + "\nMust not land globally.\n",
			"revision": s.revision, "workspace_id": "sales",
		})
		wu3Error(t, edit, "READ_FAILED")
		wu3Outcome(t, edit, "failed: nothing saved")
		wu3fix4AssertRefusalChangedNothing(t, s)
	})

	t.Run("remove refuses instead of deleting the identical global copy", func(t *testing.T) {
		s := wu3fix4FaultedSalesShelf(t, wu3fix4BreakParentWithRegularFile)
		remove := systools.NewSkillRemoveTool(s.deps).Execute(s.ctx, map[string]any{
			"name": "crm", "revision": s.revision, "confirm": true, "workspace_id": "sales",
		})
		wu3Error(t, remove, "READ_FAILED")
		wu3Outcome(t, remove, "failed: nothing saved")
		wu3fix4AssertRefusalChangedNothing(t, s)
	})

	t.Run("management listing refuses instead of presenting an incomplete shelf", func(t *testing.T) {
		s := wu3fix4FaultedSalesShelf(t, wu3fix4BreakParentWithRegularFile)
		list := systools.NewSkillListTool(s.deps).Execute(s.ctx, map[string]any{
			"scope": "management", "name": "crm", "workspace_id": "sales",
		})
		wu3Error(t, list, "READ_FAILED")
		wu3fix4AssertRefusalChangedNothing(t, s)
	})

	// Control (healthy confirmed absence): with the recognised skills folder
	// genuinely gone — NotExist, not an unreadable parent — the documented
	// fallback to the installation-wide copy keeps working. The refusal above
	// must stay tied to the error class, never to any project-directory
	// oddity.
	t.Run("confirmed absence of the recognised directory still falls back to global", func(t *testing.T) {
		s := wu3fix4FaultedSalesShelf(t, func(t *testing.T, s wu3fix4Shelf) {
			require.NoError(t, os.RemoveAll(filepath.Join(s.claudeDir, "skills")))
		})
		content := string(s.projectBefore) + "\nEdited globally after confirmed absence.\n"
		edit := systools.NewSkillEditTool(s.deps).Execute(s.ctx, map[string]any{
			"name": "crm", "content": content, "revision": s.revision, "workspace_id": "sales",
		})
		wu3Outcome(t, edit, "working")
		assert.Equal(t, content, string(wu3ReadFile(t, s.globalSkill)),
			"absence is confirmed, so the documented global fallback applies")
	})
}
