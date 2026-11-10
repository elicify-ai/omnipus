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
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// r15 §§5.1–5.5/§15: preserve the real content-read fault separately
// from resolution failure and invalid metadata. File edits, project writer
// deletion and management discovery must not change the identically named
// global/anchor copies. The old automatic mutation fallback is withdrawn;
// supported installed reads/deletes name the registry object explicitly.
// The fault is root-independent: a directory replaces a readable SKILL.md.

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
	t.Run("explicit project file edit refuses without touching global", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, wu3fix5MakeProjectContentUnreadable)
		edit := tools.NewEditFileTool(s.mountRoot, true).Execute(s.ctx, map[string]any{"path": s.projectSkill, "old_text": string(s.projectBefore), "new_text": "Must not land globally."})
		require.True(t, edit.IsError, "unreadable project file must refuse: %s", edit.ForLLM)
		wu3fix5AssertRefusalChangedNothing(t, s)
	})
	t.Run("project writer deletion refuses without touching global", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, wu3fix5MakeProjectContentUnreadable)
		require.Error(t, skills.NewSkillWriter(s.projectRoot).RemoveSkillReviewed("crm", s.revision))
		wu3fix5AssertRefusalChangedNothing(t, s)
	})
	t.Run("management list refuses incomplete shelf", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, wu3fix5MakeProjectContentUnreadable)
		result := systools.NewSkillListTool(s.deps).Execute(s.ctx, map[string]any{"scope": "management", "workspace_id": "sales"})
		wu3Error(t, result, "READ_FAILED")
		wu3fix5AssertRefusalChangedNothing(t, s)
	})
	t.Run("checked discovery reports read failure rather than complete scan", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, wu3fix5MakeProjectContentUnreadable)
		_, _, err := skills.MergeProjectSkillsChecked([]skills.ProjectMount{{Name: "sales", Root: s.mountRoot}})
		require.Error(t, err)
		wu3fix5AssertRefusalChangedNothing(t, s)
	})
	t.Run("healthy project edit changes only explicit mount file", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, nil)
		shelf, collisions, err := skills.LookupProjectSkillChecked([]skills.ProjectMount{{Name: "sales", Root: s.mountRoot}}, "crm")
		require.NoError(t, err)
		require.Len(t, shelf, 1)
		assert.Empty(t, collisions)
		content := string(s.projectBefore) + "\nEdited Sales only.\n"
		result := tools.NewEditFileTool(s.mountRoot, true).Execute(s.ctx, map[string]any{"path": s.projectSkill, "old_text": string(s.projectBefore), "new_text": content})
		require.False(t, result.IsError, result.ForLLM)
		assert.Equal(t, content, string(wu3ReadFile(t, s.projectSkill)))
		assert.Equal(t, s.globalBefore, wu3ReadFile(t, s.globalSkill))
		assert.Equal(t, s.anchorBefore, wu3ReadFile(t, s.anchorSkill))
	})
	t.Run("absent project is complete and installed read stays explicit", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, func(t *testing.T, s wu3fix5ContentFault) {
			require.NoError(t, os.RemoveAll(s.projectDir))
			_, err := os.Stat(s.projectSkill)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
		shelf, _, err := skills.LookupProjectSkillChecked([]skills.ProjectMount{{Name: "sales", Root: s.mountRoot}}, "crm")
		require.NoError(t, err)
		assert.Empty(t, shelf)
		result := systools.NewGetSkillTool(s.deps).Execute(s.ctx, map[string]any{"id": "crm", "fields": []any{"shelf", "instructions"}})
		body := wu3Success(t, result)
		assert.Equal(t, "registry", body["shelf"])
		assert.Contains(t, body["instructions"], "sales-only-body")
		assert.Equal(t, s.globalBefore, wu3ReadFile(t, s.globalSkill))
		assert.Equal(t, s.anchorBefore, wu3ReadFile(t, s.anchorSkill))
	})
	t.Run("explicit installed delete works despite absent project", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, func(t *testing.T, s wu3fix5ContentFault) { require.NoError(t, os.RemoveAll(s.projectDir)) })
		s.ctx = observeSkillForMutation(t, s.deps, readCtx("omnipus"), "crm")
		result := systools.NewSkillEditTool(s.deps).Execute(s.ctx, map[string]any{"action": "delete", "skill": "crm"})
		wu3Success(t, result)
		wu3Outcome(t, result, "working")
		assert.NoFileExists(t, s.globalSkill)
		assert.Equal(t, s.anchorBefore, wu3ReadFile(t, s.anchorSkill))
	})
	t.Run("readable invalid metadata is excluded without read failure", func(t *testing.T) {
		s := wu3fix5SalesShelfWithContentFault(t, func(t *testing.T, s wu3fix5ContentFault) {
			require.NoError(t, os.WriteFile(s.projectSkill, []byte(wu3fix5InvalidNoDescription), 0o600))
		})
		assert.Equal(t, wu3fix5InvalidNoDescription, string(wu3ReadFile(t, s.projectSkill)), "readable but invalid, not an I/O fault")
		shelf, _, err := skills.LookupProjectSkillChecked([]skills.ProjectMount{{Name: "sales", Root: s.mountRoot}}, "crm")
		require.NoError(t, err)
		assert.Empty(t, shelf)
		result := systools.NewSkillListTool(s.deps).Execute(s.ctx, map[string]any{"scope": "management", "workspace_id": "sales"})
		body := wu3Success(t, result)
		rows, ok := body["skills"].([]any)
		require.True(t, ok)
		for _, value := range rows {
			row := wu3Object(t, value)
			if row["id"] == "crm" {
				assert.NotEqual(t, "project", row["origin"], "invalid project candidate cannot appear as valid")
			}
		}
		assert.Equal(t, wu3fix5InvalidNoDescription, string(wu3ReadFile(t, s.projectSkill)))
		assert.Equal(t, s.globalBefore, wu3ReadFile(t, s.globalSkill))
		assert.Equal(t, s.anchorBefore, wu3ReadFile(t, s.anchorSkill))
	})
}
