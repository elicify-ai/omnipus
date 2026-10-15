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

// FR-043: each explicit invalid target refuses before any creation/membership.
func TestWU3UnknownTargetRefusesEverySetupWriteWithoutFallback(t *testing.T) {
	for _, verb := range []string{"create_agent", "create_skill", "edit_skill", "remove_skill"} {
		for _, target := range []string{"missing-workspace", "", " ", "../sales", "sales\\escape"} {
			t.Run(verb+"_"+target, func(t *testing.T) {
				d, _ := wu3Deps(t)
				wu3Workspace(t, d, "default", "omnipus")
				content := validSkill("crm")
				_, revision, err := d.SkillWriter.CreateSkillReviewed("crm", content)
				require.NoError(t, err)
				anchorPath := filepath.Join(d.Home, "workspaces", "default.json")
				before := wu3ReadFile(t, anchorPath)
				ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "omnipus"), "default")
				var tool tools.Tool
				args := map[string]any{"name": "crm", "content": validSkill("crm") + "\nMust not land.\n", "revision": revision, "workspace_id": target}
				switch verb {
				case "create_agent":
					tool = systools.NewAgentCreateTool(d)
					args = wu3CreateArgs("Must not exist")
					args["workspace_id"] = target
				case "create_skill":
					tool = systools.NewSkillCreateTool(d)
					args["name"] = "new-skill"
					args["content"] = validSkill("new-skill")
					delete(args, "revision")
				case "edit_skill":
					tool = systools.NewSkillEditTool(d)
				case "remove_skill":
					tool = systools.NewSkillRemoveTool(d)
					delete(args, "content")
					args["confirm"] = true
				}
				result := tool.Execute(ctx, args)
				assert.True(t, result.IsError, "invalid explicit target must be refused, not treated as omitted: %s", result.ForLLM)
				assert.Equal(t, before, wu3ReadFile(t, anchorPath))
				assert.Equal(t, content, string(wu3ReadFile(t, filepath.Join(d.Home, "skills", "crm", "SKILL.md"))))
				_, statErr := os.Stat(filepath.Join(d.Home, "skills", "new-skill"))
				assert.ErrorIs(t, statErr, os.ErrNotExist)
				agents, skipped, listErr := agentstore.New(d.Home).List()
				require.NoError(t, listErr)
				require.Empty(t, skipped, "an unreadable partial entity cannot hide a rejected-target write")
				assert.Empty(t, agents, "invalid target must save no agent entity")
				wu3Outcome(t, result, "failed: nothing saved")
			})
		}
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

// FR-042 / BDD-4.5/4.8: a same-slug negative control in the anchor shelf.
func TestWU3ProjectSkillEditUsesTargetShelfAndPreservesAnchor(t *testing.T) {
	d, ctx, anchor, target := wu3ProjectTarget(t)
	before := wu3ReadFile(t, filepath.Join(anchor, "crm", "SKILL.md"))
	revision, err := skills.NewSkillWriter(target).SkillRevision("crm")
	require.NoError(t, err)
	content := validSkill("crm") + "\nEdited Sales only.\n"
	result := systools.NewSkillEditTool(d).Execute(ctx, map[string]any{"name": "crm", "content": content, "revision": revision, "workspace_id": "sales"})
	wu3Success(t, result)
	assert.Equal(t, content, string(wu3ReadFile(t, filepath.Join(target, "crm", "SKILL.md"))))
	assert.Equal(t, before, wu3ReadFile(t, filepath.Join(anchor, "crm", "SKILL.md")))
	assert.Equal(t, "default", tools.ToolWorkspaceID(ctx))
	wu3Outcome(t, result, "working")
}

func TestWU3ProjectSkillCreateUsesTargetWithoutGlobalShadow(t *testing.T) {
	d, ctx, anchor, target := wu3ProjectTarget(t)
	content := validSkill("sales-new")
	result := systools.NewSkillCreateTool(d).Execute(ctx, map[string]any{"name": "sales-new", "content": content, "workspace_id": "sales"})
	wu3Success(t, result)
	assert.Equal(t, content, string(wu3ReadFile(t, filepath.Join(target, "sales-new", "SKILL.md"))))
	for _, path := range []string{filepath.Join(anchor, "sales-new"), filepath.Join(d.Home, "skills", "sales-new")} {
		_, err := os.Stat(path)
		assert.ErrorIs(t, err, os.ErrNotExist, "target create must not fork an anchor/global copy")
	}
	assert.Equal(t, "default", tools.ToolWorkspaceID(ctx))
}

func TestWU3ProjectSkillRemoveUsesTargetAndProtectsSameSlugAnchor(t *testing.T) {
	d, ctx, anchor, target := wu3ProjectTarget(t)
	before := wu3ReadFile(t, filepath.Join(anchor, "crm", "SKILL.md"))
	revision, err := skills.NewSkillWriter(target).SkillRevision("crm")
	require.NoError(t, err)
	result := systools.NewSkillRemoveTool(d).Execute(ctx, map[string]any{"name": "crm", "revision": revision, "confirm": true, "workspace_id": "sales"})
	wu3Success(t, result)
	_, err = os.Stat(filepath.Join(target, "crm"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Equal(t, before, wu3ReadFile(t, filepath.Join(anchor, "crm", "SKILL.md")))
	assert.Equal(t, "default", tools.ToolWorkspaceID(ctx))
}

func TestWU3ProjectSkillDiscoveryReadsTargetContentAndRevision(t *testing.T) {
	d, ctx, _, target := wu3ProjectTarget(t)
	result := systools.NewSkillListTool(d).Execute(ctx, map[string]any{"scope": "management", "name": "crm", "workspace_id": "sales"})
	body := wu3Success(t, result)
	items, ok := body["skills"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	item := wu3Object(t, items[0])
	assert.Equal(t, "crm", item["id"])
	assert.Equal(t, "project", item["origin"])
	assert.Contains(t, item["content"], "sales-only-body")
	assert.NotContains(t, item["content"], "default-only-body")
	revision, err := skills.NewSkillWriter(target).SkillRevision("crm")
	require.NoError(t, err)
	assert.Equal(t, revision, item["revision"])
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
		_, revision, err := d.SkillWriter.CreateSkillReviewed("crm", validSkill("crm"))
		require.NoError(t, err)
		tool := systools.NewSkillEditTool(d)
		winner := validSkill("crm") + "\nWinner\n"
		wu3Success(t, tool.Execute(context.Background(), map[string]any{"name": "crm", "revision": revision, "content": winner}))
		wu3Error(t, tool.Execute(context.Background(), map[string]any{"name": "crm", "revision": revision, "content": validSkill("crm") + "\nLoser\n"}), "CONFLICT")
		assert.Equal(t, winner, string(wu3ReadFile(t, filepath.Join(d.Home, "skills", "crm", "SKILL.md"))))
	})
}
