package gateway

// r15 S-11.13/S-11.17, FR-022/032/035/036/046; CHECK-06.
// Real returned-error publication, fully closed gateway loop, actual restart,
// revisioned REST grant, rebuilt instance and session-bound read/delete.
// Model transport and the adapters' allow-policy answer are injected edges.
// Startup, stores, REST regrant and seen paths are real; no grant/seen row is fabricated.
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func partialRecoveryGrant(t *testing.T, rc *runContextWithOptions, home string) *agent.AgentInstance {
	t.Helper()
	api := &restAPI{agentLoop: rc.agentLoop, homePath: home, credStore: rc.credStore}
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/agent-a", strings.NewReader(`{"skills":["foo"]}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "the supported REST grant path: %s", w.Body.String())
	raw, err := os.ReadFile(filepath.Join(home, "entities", "agents", "agent-a.json"))
	require.NoError(t, err)
	var row map[string]any
	require.NoError(t, json.Unmarshal(raw, &row))
	bindings, ok := row["skill_grants"].(map[string]any)
	require.True(t, ok, "REST persist must bind generation, never only a name")
	stamp, err := os.ReadFile(filepath.Join(home, "skills", "foo", ".omnipus-identity"))
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(string(stamp)), bindings["foo"], "the observed live generation, not an inherited old one")
	inst, ok := rc.agentLoop.GetRegistry().GetAgent("agent-a")
	require.True(t, ok)
	return inst
}

func partialRecoveryWrite(t *testing.T, rc *runContextWithOptions, provider *testutil.ScenarioProvider, path, body string) string {
	t.Helper()
	provider.Reset()
	args, err := json.Marshal(map[string]any{"path": path, "content": body})
	require.NoError(t, err)
	provider.WithToolCall("write_file", string(args)).WithText("scripted turn completed")
	final, err := rc.agentLoop.ProcessDirectAs(context.Background(), "agent-a", "perform the requested write", "skills-absence-recovery")
	require.NoError(t, err)
	require.Equal(t, "scripted turn completed", final)
	messages := provider.LastMessages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" && messages[i].ToolCallID == "write_file-0" {
			return messages[i].Content
		}
	}
	t.Fatal("BLOCKED: actual loop did not return write_file result — r15 S-11.13")
	return ""
}

func partialRecoveryContext(t *testing.T, rc *runContextWithOptions, inst *agent.AgentInstance) context.Context {
	t.Helper()
	meta, err := rc.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", inst.ID)
	require.NoError(t, err)
	return tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), inst.ID), meta.ID)
}

func TestSkillsPartialAbsence_ActualRestartRESTGrantReadAndDelete(t *testing.T) {
	for _, operation := range []string{"first-create", "restore-deleted"} {
		t.Run(operation, func(t *testing.T) {
			home, cfg := bootFixture(t)
			cfg.Agents.List[0].Skills = []string{}
			cfg.Agents.List[0].Model = &config.AgentModelConfig{Primary: "skills-scripted-model"} // ScenarioProvider transport only
			cfg.Sandbox.AutoApprove = false
			for _, name := range []string{"write_file", "read_file", "edit_skill", "get_skill", "ToolSearch"} {
				cfg.Sandbox.ToolPolicies[name] = "allow"
			}
			cfg.Agents.List[0].Tools = &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: map[string]config.ToolPolicy{"write_file": config.ToolPolicyAllow, "edit_skill": config.ToolPolicyAllow, "get_skill": config.ToolPolicyAllow}}}
			_, err := agentstore.New(home).Update("agent-a", func(a *config.AgentConfig) error { *a = cfg.Agents.List[0]; return nil })
			require.NoError(t, err)
			require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
			// Construction-time membership fixture; the mount is still persisted
			// by the real mount API and resolved by the real turn constructor.
			require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: "skills-recovery", Name: "Recovery fixture", Status: "active", CoreTeam: []string{"agent-a"}, CreatedAt: "2026-10-31T00:00:00Z", UpdatedAt: "2026-10-31T00:00:00Z"}))
			_, err = workspace.EnsureWorkDir(home, "skills-recovery")
			require.NoError(t, err)
			var before []map[string]any
			var contentID, deletionID string
			if !t.Run("returned-error-publication", func(t *testing.T) {
				provider := testutil.NewScenario()
				rc, inst, deps := crashBoot(t, home, provider)
				root := filepath.Join(home, "skills")
				live := filepath.Join(root, "foo", "SKILL.md")
				ctx := partialRecoveryContext(t, rc, inst)
				if operation == "restore-deleted" {
					_, createErr := skills.ApplyChange(root, skills.ChangeRequest{Kind: skills.KindCreate, Slug: "foo", Content: []byte(crashSkillMD("PARTIAL-RECOVERY")), Expect: skills.Expectation{Absent: true}, Actor: skills.ChangeActor{Kind: "person", Tool: "write_file"}})
					require.NoError(t, createErr, "BLOCKED: committed content prerequisite — r15 S-11.17")
					rows := crashVersionRows(t, home)
					require.Len(t, rows, 1)
					contentID = rows[0]["id"].(string)
					inst = partialRecoveryGrant(t, rc, home)
					loaded := crashActualSkill(t, inst, ctx)
					require.False(t, loaded.IsError)
					require.Contains(t, loaded.ForLLM, `revision="`, "real seen prerequisite")
					deleted := systools.NewSkillEditTool(deps).Execute(ctx, map[string]any{"action": "delete", "skill": "foo"})
					require.False(t, deleted.IsError, "%s", deleted.ForLLM)
					require.NoDirExists(t, filepath.Join(root, "foo"))
				}
				before = crashVersionRows(t, home)
				if operation == "restore-deleted" {
					require.Len(t, before, 2)
					for _, row := range before {
						if row["kind"] == "delete_skill" {
							deletionID = row["id"].(string)
						}
					}
					require.NotEmpty(t, deletionID, "real delete committed its head")
				} else {
					require.Empty(t, before, "true first create has no history")
				}
				fired := 0
				old := skills.FaultManifestWrite
				t.Cleanup(func() { skills.FaultManifestWrite = old })
				skills.FaultManifestWrite = func(path string) error {
					body, readErr := os.ReadFile(live)
					if filepath.Dir(path) == filepath.Join(home, "system", "skill-history", "foo", "versions") && readErr == nil && string(body) == crashSkillMD("PARTIAL-RECOVERY") {
						fired++
						return errors.New("injected post-publication manifest failure")
					}
					return nil
				}
				var text string
				if operation == "first-create" {
					ws, ok := workspace.FindForAgent(home, inst.ID)
					require.True(t, ok)
					_, _, mountErr := workspace.CreateMount(home, ws, "enclosing-home", filepath.Dir(home))
					require.NoError(t, mountErr)
					text = partialRecoveryWrite(t, rc, provider, live, crashSkillMD("PARTIAL-RECOVERY"))
				} else {
					res := systools.NewSkillEditTool(deps).Execute(ctx, map[string]any{"action": "restore", "skill": "foo", "version": contentID})
					require.True(t, res.IsError, "a partial restore is not reported as full success")
					text = res.ForLLM
				}
				require.Equal(t, 1, fired, "BLOCKED: exact post-publication manifest stage was not reached — r15 S-11.13/17; result=%s", text)
				assert.Contains(t, text, "SKILL_PARTIALLY_SAVED")
				if operation == "first-create" {
					assert.Contains(t, text, "No version history exists for this skill; remove the skill with edit_skill action=delete (or REST DELETE) to undo.")
				} else {
					assert.Contains(t, text, "The previous live state was absent (the skill had been deleted); the skill's earlier history is unchanged.")
					assert.NotContains(t, text, "No version history exists")
				}
				body, readErr := os.ReadFile(live)
				require.NoError(t, readErr)
				assert.Equal(t, crashSkillMD("PARTIAL-RECOVERY"), string(body))
				assert.Equal(t, before, crashVersionRows(t, home), "failed manifest changes no committed chain")
				skills.FaultManifestWrite = old
				if operation == "first-create" {
					text = partialRecoveryWrite(t, rc, provider, live, crashSkillMD("UNREAD-RETRY"))
				} else {
					text = systools.NewSkillEditTool(deps).Execute(ctx, map[string]any{"action": "delete", "skill": "foo"}).ForLLM
				}
				assert.Contains(t, text, "SKILL_NOT_READ", "partial publication clears seen; never a stale-revision alternative")
				assert.Equal(t, before, crashVersionRows(t, home))
			}) {
				return
			} // the failed prerequisite is loud; do not invent restart evidence
			t.Run("actual-restart-and-supported-regrant", func(t *testing.T) {
				partialRecoveryVerifyRestart(t, home, operation, before, contentID, deletionID)
			})
		})
	}
}

func partialRecoveryVerifyRestart(t *testing.T, home, operation string, before []map[string]any, contentID, deletionID string) {
	t.Helper()
	rc, inst, deps := crashBoot(t, home)
	assert.Equal(t, before, crashVersionRows(t, home), "startup is not ordinary-skill detection")
	ctx := partialRecoveryContext(t, rc, inst)
	edit := systools.NewSkillEditTool(deps)
	unread := edit.Execute(ctx, map[string]any{"action": "delete", "skill": "foo"})
	require.True(t, unread.IsError)
	assert.Contains(t, unread.ForLLM, "SKILL_NOT_READ")
	assert.Equal(t, before, crashVersionRows(t, home), "refused unread write records nothing")
	denied := crashActualSkill(t, inst, ctx)
	require.True(t, denied.IsError, "recreation/restore never inherits the retired generation's grant")
	assert.NotContains(t, denied.ForLLM, "Body PARTIAL-RECOVERY.")
	assert.Equal(t, before, crashVersionRows(t, home), "authorization precedes detection")
	inst = partialRecoveryGrant(t, rc, home)
	fresh := crashActualSkill(t, inst, ctx)
	require.False(t, fresh.IsError, "%s", fresh.ForLLM)
	require.Contains(t, fresh.ForLLM, "Body PARTIAL-RECOVERY.")
	require.Contains(t, fresh.ForLLM, `revision="`)
	rows := crashVersionRows(t, home)
	require.Len(t, rows, len(before)+1, "one authorized detection, no startup/refusal versions")
	kind := "baseline"
	if operation == "restore-deleted" {
		kind = "external_change"
	}
	var detected []map[string]any
	for _, row := range rows {
		if row["kind"] == kind {
			detected = append(detected, row)
		}
	}
	require.Len(t, detected, 1)
	if operation == "restore-deleted" {
		assert.Equal(t, deletionID, detected[0]["parent"], "detection extends, never orphans, the deletion chain")
		page := systools.NewGetSkillTool(deps).Execute(ctx, map[string]any{"id": "foo", "fields": []any{"versions"}})
		require.False(t, page.IsError, "%s", page.ForLLM)
		var result map[string]any
		require.NoError(t, json.Unmarshal([]byte(page.ForLLM), &result))
		versions, ok := result["versions"].(map[string]any)
		require.True(t, ok)
		items, ok := versions["items"].([]any)
		require.True(t, ok)
		var ids []string
		for _, item := range items {
			ids = append(ids, item.(map[string]any)["id"].(string))
		}
		assert.Contains(t, ids, contentID)
		assert.Contains(t, ids, deletionID, "retained does not mean restorable")
		invalid := edit.Execute(ctx, map[string]any{"action": "restore", "skill": "foo", "version": deletionID})
		require.True(t, invalid.IsError)
		assert.Contains(t, invalid.ForLLM, "SAVE_CHECK_FAILED")
		assert.Contains(t, invalid.ForLLM, "the change to 'foo' was not saved: version "+deletionID+" records the deleted state; use edit_skill action=delete to remove the skill")
	}
	deleted := edit.Execute(ctx, map[string]any{"action": "delete", "skill": "foo"})
	require.False(t, deleted.IsError, "fresh authorized read makes the named undo executable: %s", deleted.ForLLM)
	assert.NoDirExists(t, filepath.Join(home, "skills", "foo"))
	assert.Len(t, crashVersionRows(t, home), len(rows)+1, "undo appends; earlier chain remains")
}
