package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-040/041; BDD-4.4; Q1 workspace_id: effect is not the session anchor.
// Main-chat provisioning is separately driven through the real gateway wiring.
func TestWU3CreateAgentJoinsExplicitTargetNotCallerAnchor(t *testing.T) {
	d, _ := wu3Deps(t)
	wu3Workspace(t, d, "default", "omnipus")
	wu3Workspace(t, d, "sales")
	anchor := wu3ReadFile(t, filepath.Join(d.Home, "workspaces", "default.json"))
	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "omnipus"), "default")
	args := wu3CreateArgs("Sales colleague")
	args["workspace_id"] = "sales"
	body := wu3Success(t, systools.NewAgentCreateTool(d).Execute(ctx, args))
	id := wu3String(t, body["id"])
	read := wu3Success(t, systools.NewWorkspaceGetTool(d).Execute(ctx, map[string]any{"id": "sales"}))
	assert.Equal(t, []any{id}, read["core_team"], "only target gets the new agent")
	assert.Equal(t, anchor, wu3ReadFile(t, filepath.Join(d.Home, "workspaces", "default.json")))
	assert.Equal(t, "default", tools.ToolWorkspaceID(ctx), "the caller context must not be retargeted")
	assert.Equal(t, "joined_workspace", body["status"])
	wu3Outcome(t, systools.NewAgentCreateTool(d).Execute(ctx, map[string]any{"name": ""}), "failed: nothing saved")
}

// FR-043 still governs agent provisioning. r15 §5.4 retires skill
// workspace_id/content/revision; no removed request may mutate a global skill.
func TestWU3UnknownTargetRefusesEverySetupWriteWithoutFallback(t *testing.T) {
	for _, target := range []string{"missing-workspace", "", " ", "../sales", "sales\\escape"} {
		t.Run(target, func(t *testing.T) {
			d, _ := wu3Deps(t)
			wu3Workspace(t, d, "default", "omnipus")
			content := validSkill("crm")
			_, _, err := d.SkillWriter.CreateSkillReviewed("crm", content)
			require.NoError(t, err)
			anchorPath := filepath.Join(d.Home, "workspaces", "default.json")
			before := wu3ReadFile(t, anchorPath)
			ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "omnipus"), "default")
			args := wu3CreateArgs("Must not exist")
			args["workspace_id"] = target
			result := systools.NewAgentCreateTool(d).Execute(ctx, args)
			require.True(t, result.IsError, "invalid explicit target must refuse: %s", result.ForLLM)
			wu3Outcome(t, result, "failed: nothing saved")
			assertRemovedSkillMutationArgs(t, d, ctx, "crm", target)
			assert.Equal(t, before, wu3ReadFile(t, anchorPath))
			assert.Equal(t, content, string(wu3ReadFile(t, filepath.Join(d.Home, "skills", "crm", "SKILL.md"))))
			_, statErr := os.Stat(filepath.Join(d.Home, "skills", "new-skill"))
			assert.ErrorIs(t, statErr, os.ErrNotExist)
			agents, skipped, listErr := agentstore.New(d.Home).List()
			require.NoError(t, listErr)
			require.Empty(t, skipped)
			assert.Empty(t, agents)
		})
	}
}

// Every withdrawn parameter is checked separately: a rejection of one must
// not accidentally hide silent acceptance of another. This invokes real tools.
func assertRemovedSkillMutationArgs(t *testing.T, d *systools.Deps, ctx context.Context, slug, target string) {
	t.Helper()
	for _, extra := range []struct {
		name  string
		value any
	}{{"workspace_id", target}, {"content", validSkill(slug)}, {"revision", "not-a-supported-input"}} {
		result := systools.NewSkillEditTool(d).Execute(ctx, map[string]any{"action": "delete", "skill": slug, extra.name: extra.value})
		body := wu3Error(t, result, "INVALID_INPUT")
		require.Contains(t, body["message"], extra.name, "§5.4 refusal names the removed input")
	}
}

func wu3ProjectTarget(t *testing.T) (*systools.Deps, context.Context, string, string) {
	t.Helper()
	d, cfg := wu3Deps(t)
	coreagent.SeedConfig(cfg)
	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "omnipus"), "default")
	roots := map[string]string{}
	for _, id := range []string{"default", "sales"} {
		wu3Workspace(t, d, id)
		root := t.TempDir()
		_, _, err := workspace.CreateMount(d.Home, id, "project", root)
		require.NoError(t, err)
		roots[id] = filepath.Join(root, ".claude", "skills")
		_, _, err = skills.NewSkillWriter(roots[id]).CreateSkillReviewed("crm", validSkill("crm")+"\n"+id+"-only-body\n")
		require.NoError(t, err)
	}
	d.ResolveToolPolicy = func(id, name string) (string, bool) {
		policy, kind := tools.BuildFallbackPolicyCfg(cfg, id)
		return tools.EffectiveToolPolicy(policy, tools.ScopeCore, kind, name), true
	}
	return d, ctx, roots["default"], roots["sales"]
}

// r15 §15: explicit project file paths replace withdrawn slug routing.
func TestWU3ProjectFileEditUsesExplicitTargetAndPreservesAnchor(t *testing.T) {
	d, ctx, anchor, target := wu3ProjectTarget(t)
	path := filepath.Join(target, "crm", "SKILL.md")
	before := wu3ReadFile(t, filepath.Join(anchor, "crm", "SKILL.md"))
	content := validSkill("crm") + "\nEdited Sales only.\n"
	result := tools.NewWriteFileTool(filepath.Dir(filepath.Dir(target)), true).Execute(ctx, map[string]any{"path": path, "content": content, "overwrite": true})
	require.False(t, result.IsError, result.ForLLM)
	assert.Equal(t, content, string(wu3ReadFile(t, path)))
	assert.Equal(t, before, wu3ReadFile(t, filepath.Join(anchor, "crm", "SKILL.md")))
	assert.Equal(t, "default", tools.ToolWorkspaceID(ctx))
	assert.NoDirExists(t, filepath.Join(d.Home, "skills", "crm"))
}

func TestWU3ProjectFileCreateUsesExplicitTargetWithoutGlobalShadow(t *testing.T) {
	d, ctx, anchor, target := wu3ProjectTarget(t)
	content := validSkill("sales-new")
	result := tools.NewWriteFileTool(filepath.Dir(filepath.Dir(target)), true).Execute(ctx, map[string]any{"path": filepath.Join(target, "sales-new", "SKILL.md"), "content": content})
	require.False(t, result.IsError, result.ForLLM)
	assert.Equal(t, content, string(wu3ReadFile(t, filepath.Join(target, "sales-new", "SKILL.md"))))
	for _, path := range []string{filepath.Join(anchor, "sales-new"), filepath.Join(d.Home, "skills", "sales-new")} {
		assert.NoDirExists(t, path, "no implicit anchor/global copy")
	}
	assert.Equal(t, "default", tools.ToolWorkspaceID(ctx))
	assert.NoDirExists(t, filepath.Join(d.Home, "system", "skill-history"), "project creation has no registry pipeline")
}

func TestWU3ProjectWriterRemoveUsesTargetAndProtectsSameSlugAnchor(t *testing.T) {
	_, ctx, anchor, target := wu3ProjectTarget(t)
	before := wu3ReadFile(t, filepath.Join(anchor, "crm", "SKILL.md"))
	writer := skills.NewSkillWriter(target)
	revision, err := writer.SkillRevision("crm")
	require.NoError(t, err)
	require.NoError(t, writer.RemoveSkillReviewed("crm", revision))
	assert.NoDirExists(t, filepath.Join(target, "crm"))
	assert.Equal(t, before, wu3ReadFile(t, filepath.Join(anchor, "crm", "SKILL.md")))
	assert.Equal(t, "default", tools.ToolWorkspaceID(ctx))
}

func TestWU3ProjectDiscoveryListsTargetAndFileReadUsesExplicitPath(t *testing.T) {
	d, ctx, _, target := wu3ProjectTarget(t)
	listed := systools.NewSkillListTool(d).Execute(ctx, map[string]any{"scope": "management", "workspace_id": "sales"})
	body := wu3Success(t, listed)
	items, ok := body["skills"].([]any)
	require.True(t, ok)
	var project map[string]any
	for _, value := range items {
		item := wu3Object(t, value)
		if item["id"] == "crm" && item["origin"] == "project" {
			project = item
		}
	}
	require.NotNil(t, project, "pure list includes target project")
	assert.NotContains(t, project, "content", "§5.3 pure list never returns content")
	revision, err := skills.ContentRevision(filepath.Join(target, "crm"))
	require.NoError(t, err)
	assert.Equal(t, revision, project["revision"])
	result := tools.NewReadFileTool(filepath.Dir(filepath.Dir(target)), true, 0).Execute(ctx, map[string]any{"path": filepath.Join(target, "crm", "SKILL.md")})
	require.False(t, result.IsError, result.ForLLM)
	assert.Contains(t, result.ForLLM, "sales-only-body")
	assert.NotContains(t, result.ForLLM, "default-only-body")
	assert.Equal(t, "default", tools.ToolWorkspaceID(ctx))
}

// BDD-11.5/11.10: independent winner read-back, not just a conflict flag.
func TestWU3StaleAgentAndGlobalSkillWritesKeepWinner(t *testing.T) {
	t.Run("agent", func(t *testing.T) {
		d, cfg := wu3Deps(t)
		id := wu3CreateAgent(t, d, cfg, "Shared colleague")
		before := wu3AgentRead(t, d, id)
		tool := systools.NewAgentUpdateTool(d)
		wu3Success(t, tool.Execute(context.Background(), map[string]any{"id": id, "revision": before["revision"], "description": "Winner"}))
		winner := wu3AgentRead(t, d, id)
		wu3Error(t, tool.Execute(context.Background(), map[string]any{"id": id, "revision": before["revision"], "description": "Loser"}), "CONFLICT")
		assert.Equal(t, winner, wu3AgentRead(t, d, id))
		assert.Equal(t, "Winner", winner["description"])
	})
	t.Run("global_skill", func(t *testing.T) {
		d, _ := wu3Deps(t)
		root := filepath.Join(d.Home, "skills")
		_, _, err := d.SkillWriter.CreateSkillReviewed("crm", validSkill("crm"))
		require.NoError(t, err)
		ctx := observeSkillForMutation(t, d, readCtx("editor"), "crm")
		// An outside winner changes bytes after the session's supported read.
		winner := validSkill("crm") + "\nWinner\n"
		path := filepath.Join(root, "crm", "SKILL.md")
		require.NoError(t, os.WriteFile(path, []byte(winner), 0o600))
		result := systools.NewSkillEditTool(d).Execute(ctx, map[string]any{"action": "delete", "skill": "crm"})
		wu3Error(t, result, "SKILL_CHANGED")
		assert.Equal(t, winner, string(wu3ReadFile(t, path)), "stale session cannot delete the winner")
	})
}
