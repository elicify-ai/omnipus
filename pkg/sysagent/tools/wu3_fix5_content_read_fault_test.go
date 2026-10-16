package systools_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CHECK R4-01 (fix round 4 audit): a valid project skill whose path RESOLVES
// and whose directories LIST, but whose CONTENT cannot be read, is silently
// dropped from the checked shelf — the metadata reader loses the read error,
// the unknown description then fails metadata validation, and the candidate
// is skipped WITHOUT marking the scan incomplete. A workspace-scoped
// edit_skill consequently edits the byte-identical installation-wide copy
// (same revision) and reports working, changing a shared resource that other
// workspaces read. A content-read failure is neither confirmed absence nor
// invalid metadata: checked discovery must report it, and workspace-scoped
// edit_skill / remove_skill / management list_skills must refuse READ_FAILED
// ("failed: nothing saved" for the change tools) before any global resource
// is selected.
//
// The fault is a real, root-independent I/O failure: the valid SKILL.md moves
// aside inside its own skill directory and an empty directory is planted at
// its exact path, so path resolution and both directory listings still
// succeed while os.ReadFile genuinely fails on every supported platform. No
// permission bit is touched and no errno string is pinned.
//
// Oracles: FR-030 truthful change outcomes ("failed: nothing saved"); FR-042
// target-shelf resolution; ADR-072 D4.1/D6 — only a COMPLETE scan may prove a
// slug absent, so a readable valid project skill must still win over the
// identical global copy, a confirmed absence must still fall back to it, and
// a readable but invalid-metadata candidate must keep the existing
// invalid-skill exclusion instead of a read-failure refusal.

// wu3fix5InvalidNoDescription is a READABLE SKILL.md with no description in
// its frontmatter and an empty body, so the loader contract's metadata
// validation fails while the read itself succeeds — the invalid-skill class
// this file must keep distinct from the read-error class.
const wu3fix5InvalidNoDescription = "---\nname: crm\n---\n"

// wu3fix5ContentFault is one fixture: the sales mount of wu3ProjectTarget
// with the installation-wide crm copy seeded byte-identical to the sales
// project skill (identical bytes, so the shared revision makes a wrong global
// fallback genuinely reachable rather than refused as stale).
type wu3fix5ContentFault struct {
	deps         *systools.Deps
	ctx          context.Context
	mountRoot    string // the sales mount root (the parent of .claude)
	projectRoot  string // the sales mount's recognised .claude/skills directory
	projectDir   string // sales project crm/
	projectSkill string // sales project crm/SKILL.md at its normal path
	projectAside string // where the valid SKILL.md waits while unreadable
	anchorSkill  string // default workspace's project crm/SKILL.md
	globalSkill  string // installation-wide crm/SKILL.md
	revision     string // the shared revision of the identical copies

	globalBefore    []byte
	projectBefore   []byte
	anchorBefore    []byte
	inventoryBefore []string
}

// wu3fix5SalesShelfWithContentFault builds the fixture and then applies
// fault_ to it (nil keeps the fixture healthy for the precedence control).
func wu3fix5SalesShelfWithContentFault(t *testing.T, fault_ func(t *testing.T, s wu3fix5ContentFault)) wu3fix5ContentFault {
	t.Helper()
	d, ctx, anchor, target := wu3ProjectTarget(t)
	s := wu3fix5ContentFault{
		deps:         d,
		ctx:          ctx,
		mountRoot:    filepath.Dir(filepath.Dir(target)),
		projectRoot:  target,
		projectDir:   filepath.Join(target, "crm"),
		projectSkill: filepath.Join(target, "crm", "SKILL.md"),
		projectAside: filepath.Join(target, "crm", "SKILL.md.wu3fix5-aside"),
		anchorSkill:  filepath.Join(anchor, "crm", "SKILL.md"),
	}
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
	s.inventoryBefore = wu3fix5DurableInventory(t, d.Home)
	if fault_ != nil {
		fault_(t, s)
	}
	return s
}

// wu3fix5DurableInventory is this file's own durable-store snapshot. It
// records the same durable roots and agent inventory as the shared
// wu3DurableInventory, but a symlink entry is recorded by its checked link
// target instead of a content read: the real project mounts in these fixtures
// surface as directory symlinks under workspaces/<workspace>/work/ (created by
// workspace.CreateMount's os.Symlink), and filepath.WalkDir reports a symlink
// as a NON-directory entry, so the shared helper's os.ReadFile on such a link
// fails with "is a directory" — an instrument failure in fixture setup, not
// evidence about the tools. No entry is skipped and no error is swallowed: an
// unreadable regular file, an unreadable agent record or an unresolvable link
// fails the snapshot.
func wu3fix5DurableInventory(t *testing.T, home string) []string {
	t.Helper()
	agents, skipped, err := agentstore.New(home).List()
	require.NoError(t, err)
	require.Empty(t, skipped, "an unreadable agent record would hide a new entity")
	out := make([]string, 0, len(agents))
	for _, a := range agents {
		out = append(out, "agent:"+a.ID)
	}
	out = append(out, wu3fix5FileInventory(t, home)...)
	sort.Strings(out)
	return out
}

// wu3fix5FileInventory is every file (with its bytes), folder and symlink
// (with its checked link target) under the durable stores a change tool can
// write. Directory symlinks are recorded without being followed: their targets
// (the project mounts) live outside the durable stores, and writes into a
// mount are covered by this file's explicit global/displaced/anchor byte
// assertions and the empty planted-directory check, not by this snapshot.
func wu3fix5FileInventory(t *testing.T, home string) []string {
	t.Helper()
	var out []string
	for _, dir := range []string{"entities", "agents", "workspaces", "skills"} {
		root := filepath.Join(home, dir)
		walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, os.ErrNotExist) && path == root {
					return nil
				}
				return err
			}
			rel, relErr := filepath.Rel(home, path)
			if relErr != nil {
				return relErr
			}
			if entry.IsDir() {
				out = append(out, "dir:"+rel)
				return nil
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				target, readlinkErr := os.Readlink(path)
				if readlinkErr != nil {
					return readlinkErr
				}
				out = append(out, "symlink:"+rel+"->"+target)
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			out = append(out, "file:"+rel+":"+string(data))
			return nil
		})
		require.NoError(t, walkErr)
	}
	sort.Strings(out)
	return out
}

// wu3fix5MakeProjectContentUnreadable applies the finding's fault: the valid
// project SKILL.md moves aside (recoverable, same directory) and an empty
// directory takes its exact place, then proves the engaged fault is exactly
// the content-read class before the action under test runs.
func wu3fix5MakeProjectContentUnreadable(t *testing.T, s wu3fix5ContentFault) {
	t.Helper()
	require.NoError(t, os.Rename(s.projectSkill, s.projectAside), "the valid skill waits aside, recoverable")
	require.NoError(t, os.Mkdir(s.projectSkill, 0o700), "a directory takes the candidate's exact path")
	wu3fix5AssertContentOnlyFault(t, s)
}

// wu3fix5AssertContentOnlyFault is the setup instrument: the candidate path
// RESOLVES, the mount root and the recognised skills directory LIST, the
// candidate directory itself LISTS — and yet READING the SKILL.md path
// genuinely fails. That is the fault under test, engaged for real.
func wu3fix5AssertContentOnlyFault(t *testing.T, s wu3fix5ContentFault) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(s.projectSkill)
	require.NoError(t, err, "the candidate path must resolve so this is not a resolution failure")
	require.True(t, filepath.IsAbs(resolved), "the resolved candidate is a real path inside the mount")
	_, err = os.ReadDir(s.mountRoot)
	require.NoError(t, err, "the mount root must list so this is not a mount-level failure")
	entries, err := os.ReadDir(filepath.Dir(s.projectDir))
	require.NoError(t, err, "the recognised skills directory must list so this is not a listing failure")
	listed := false
	for _, entry := range entries {
		if entry.Name() == "crm" && entry.IsDir() {
			listed = true
		}
	}
	require.True(t, listed, "the crm candidate directory must still be listed for discovery")
	_, err = os.ReadDir(s.projectDir)
	require.NoError(t, err, "the candidate directory itself must list")
	_, readErr := os.ReadFile(s.projectSkill)
	require.Error(t, readErr, "reading the candidate's SKILL.md must genuinely fail — that is the fault under test")
	require.NotErrorIs(t, readErr, os.ErrNotExist, "a read failure is not the confirmed absence that may fall back to global")
	require.Equal(t, string(s.projectBefore), string(wu3ReadFile(t, s.projectAside)),
		"the valid project bytes are intact and recoverable beside the fault")
}

// wu3fix5AssertRefusalChangedNothing proves the refusal changed nothing
// anywhere: the identical global copy, the displaced valid project copy and
// the anchor workspace's copy are byte-identical, nothing hides inside the
// planted path, the valid project skill reads unchanged at its normal path
// after the restore, and the durable stores are unchanged.
func wu3fix5AssertRefusalChangedNothing(t *testing.T, s wu3fix5ContentFault) {
	t.Helper()
	assert.Equal(t, string(s.globalBefore), string(wu3ReadFile(t, s.globalSkill)), "the identical global copy is untouched")
	assert.Equal(t, string(s.projectBefore), string(wu3ReadFile(t, s.projectAside)), "the displaced valid project copy is intact while faulted")
	assert.Equal(t, string(s.anchorBefore), string(wu3ReadFile(t, s.anchorSkill)), "the anchor workspace's project copy is untouched")
	require.NoError(t, os.Remove(s.projectSkill), "the planted directory is empty — nothing wrote through the faulted path")
	require.NoError(t, os.Rename(s.projectAside, s.projectSkill), "restore the valid project skill")
	assert.Equal(t, string(s.projectBefore), string(wu3ReadFile(t, s.projectSkill)),
		"the project copy reads unchanged at its normal path after the restore")
	assert.Equal(t, s.inventoryBefore, wu3fix5DurableInventory(t, s.deps.Home), "durable stores unchanged")
}

func TestWU3ProjectContentReadFailureRefusesWorkspaceSkillActions(t *testing.T) {
	t.Run("edit refuses instead of editing the identical global copy", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, wu3fix5MakeProjectContentUnreadable)
		edit := systools.NewSkillEditTool(s.deps).Execute(s.ctx, map[string]any{
			"name":         "crm",
			"content":      string(s.projectBefore) + "\nMust not land globally.\n",
			"revision":     s.revision,
			"workspace_id": "sales",
		})
		wu3Error(t, edit, "READ_FAILED")
		wu3Outcome(t, edit, "failed: nothing saved")
		wu3fix5AssertRefusalChangedNothing(t, s)
	})

	t.Run("remove refuses instead of deleting the identical global copy", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, wu3fix5MakeProjectContentUnreadable)
		remove := systools.NewSkillRemoveTool(s.deps).Execute(s.ctx, map[string]any{
			"name": "crm", "revision": s.revision, "confirm": true, "workspace_id": "sales",
		})
		wu3Error(t, remove, "READ_FAILED")
		wu3Outcome(t, remove, "failed: nothing saved")
		wu3fix5AssertRefusalChangedNothing(t, s)
	})

	t.Run("management listing refuses instead of presenting the global copy", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, wu3fix5MakeProjectContentUnreadable)
		list := systools.NewSkillListTool(s.deps).Execute(s.ctx, map[string]any{
			"scope": "management", "name": "crm", "workspace_id": "sales",
		})
		wu3Error(t, list, "READ_FAILED")
		wu3fix5AssertRefusalChangedNothing(t, s)
	})

	// The seam the required action names: checked discovery itself must report
	// the content-read failure instead of returning an apparently complete
	// shelf — through the exported API and real stores, no production seam.
	t.Run("checked discovery reports the content-read failure instead of a complete scan", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, wu3fix5MakeProjectContentUnreadable)
		_, _, err := skills.MergeProjectSkillsChecked([]skills.ProjectMount{{Name: "sales", Root: s.mountRoot}})
		require.Error(t, err, "checked discovery must not present an incomplete scan as complete")
		wu3fix5AssertRefusalChangedNothing(t, s)
	})

	// Control (healthy precedence): with the project skill readable it still
	// wins over the byte-identical global copy. The refusal must stay tied to
	// the read-error class, never weaken project-shelf resolution.
	t.Run("readable valid project skill still wins over the identical global copy", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, nil)
		projectRevision, err := skills.NewSkillWriter(s.projectRoot).SkillRevision("crm")
		require.NoError(t, err)
		content := string(s.projectBefore) + "\nEdited in the sales mount only.\n"
		edit := systools.NewSkillEditTool(s.deps).Execute(s.ctx, map[string]any{
			"name": "crm", "content": content, "revision": projectRevision, "workspace_id": "sales",
		})
		wu3Success(t, edit)
		wu3Outcome(t, edit, "working")
		assert.Equal(t, content, string(wu3ReadFile(t, s.projectSkill)), "the readable project copy is the edit target")
		assert.Equal(t, string(s.globalBefore), string(wu3ReadFile(t, s.globalSkill)),
			"the identical global copy is untouched while the project skill is readable")
		assert.Equal(t, string(s.anchorBefore), string(wu3ReadFile(t, s.anchorSkill)), "the anchor workspace's project copy is untouched")
	})

	// Control (documented fallback, edit): with the project candidate genuinely
	// gone — NotExist, not an unreadable file — the installation-wide copy is
	// the edit target, as documented. The refusal above must stay tied to the
	// read-error class.
	t.Run("confirmed absence of the project skill still falls back to the global copy", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, func(t *testing.T, s wu3fix5ContentFault) {
			require.NoError(t, os.RemoveAll(s.projectDir))
			_, statErr := os.Stat(s.projectSkill)
			require.ErrorIs(t, statErr, os.ErrNotExist, "the project candidate is genuinely absent")
		})
		content := string(s.projectBefore) + "\nEdited globally after confirmed absence.\n"
		edit := systools.NewSkillEditTool(s.deps).Execute(s.ctx, map[string]any{
			"name": "crm", "content": content, "revision": s.revision, "workspace_id": "sales",
		})
		wu3Success(t, edit)
		wu3Outcome(t, edit, "working")
		assert.Equal(t, content, string(wu3ReadFile(t, s.globalSkill)),
			"absence is confirmed, so the documented global fallback applies")
	})

	// Control (documented fallback, remove): the same confirmed absence leaves
	// the installation-wide copy as the remove target, so the remove refusal
	// above can never be a blanket refusal of workspace-scoped removal.
	t.Run("confirmed project absence still falls back to global for remove_skill", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, func(t *testing.T, s wu3fix5ContentFault) {
			require.NoError(t, os.RemoveAll(s.projectDir))
		})
		remove := systools.NewSkillRemoveTool(s.deps).Execute(s.ctx, map[string]any{
			"name": "crm", "revision": s.revision, "confirm": true, "workspace_id": "sales",
		})
		wu3Success(t, remove)
		wu3Outcome(t, remove, "working")
		_, statErr := os.Stat(s.globalSkill)
		assert.ErrorIs(t, statErr, os.ErrNotExist, "the confirmed absence leaves the global copy as the remove target")
		assert.Equal(t, string(s.anchorBefore), string(wu3ReadFile(t, s.anchorSkill)), "the anchor workspace's project copy is untouched")
	})

	// Control (invalid metadata is not a read error): a READABLE candidate the
	// loader contract rejects for a missing description keeps the existing
	// invalid-skill exclusion — the scan stays complete and the documented
	// global fallback applies. The fix must not widen the refusal to this
	// class.
	t.Run("readable invalid-metadata candidate keeps the invalid-skill exclusion", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, func(t *testing.T, s wu3fix5ContentFault) {
			require.NoError(t, os.WriteFile(s.projectSkill, []byte(wu3fix5InvalidNoDescription), 0o600))
			data, readErr := os.ReadFile(s.projectSkill)
			require.NoError(t, readErr, "this candidate is readable — it is the invalid-metadata class, not the content-read fault")
			require.Equal(t, wu3fix5InvalidNoDescription, string(data), "the candidate parses as metadata without any description")
			found, _ := skills.DiscoverProjectSkills("sales", s.mountRoot)
			assert.Empty(t, found, "discovery excludes the invalid candidate while the read itself succeeds")
		})
		content := string(s.projectBefore) + "\nEdited globally past an invalid project candidate.\n"
		edit := systools.NewSkillEditTool(s.deps).Execute(s.ctx, map[string]any{
			"name": "crm", "content": content, "revision": s.revision, "workspace_id": "sales",
		})
		wu3Success(t, edit)
		wu3Outcome(t, edit, "working")
		assert.Equal(t, content, string(wu3ReadFile(t, s.globalSkill)),
			"the invalid-skill exclusion keeps the documented global fallback")
		assert.Equal(t, wu3fix5InvalidNoDescription, string(wu3ReadFile(t, s.projectSkill)),
			"the invalid project candidate itself is left untouched")
	})
}
