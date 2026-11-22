//go:build goolm && stdjson

package gateway

// Order 54 — TestSkillKeyValidation_AllWriters (S-116; Dataset D16; C-23).
// Oracle: skills-tools-redesign-spec.md — every writer accepts and refuses
// the SAME skill keys: an installed key (granted or not, D16#6), a project
// key of a workspace the agent is mounted on (D16#1, #3, #5), a project key
// of a workspace it is NOT mounted on (D16#2, #4 → 400 "`project:w2/todo` is
// not a skill this agent can use."), an unknown key (D16#7 → 400, nothing
// stored), `null` (D16#8 → 400 naming `skills`), empty and clear (D16#9,
// #11), remove (D16#12), the workspace-gone reconcile (D16#13), and an
// unrelated tools_cfg write that changes no skill override (D16#14). The
// read-back goes through get_agent (the system tool) AND GET
// /agents/{id}/tools (C-4's skills array) after every stored row.
//
// Harness: the package's PUT /agents/{id}/tools mechanics
// (agent_tools_skills_red_test.go::putAgentSkills / ::readAgentToolsSkills)
// and the system tools set_agent_permissions / create_agent / get_agent,
// driven through systools Deps wired to the gateway's own config (the same
// constructors pkg/sysagent/tools' tests use). Project fixtures: W1 with
// .claude/skills/notes, W2 with .claude/skills/todo; the agent's workspace
// record points at W1 (config.AgentConfig.Home, json "workspace").
//
// RED today: no writer reads skill keys at this commit — updateAgentTools
// drops the whole skills member (order 8's RED), set_agent_permissions and
// create_agent refuse skill_policy_changes as an unknown field (order 15/42's
// RED), the C-23 known-skill validation does not exist, and the D16#13
// workspace reconcile has no entry point (seam). Every stored row therefore
// fails on its missing write or its missing refusal.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"

	"github.com/stretchr/testify/require"
)

// seedKeyValidationMounts writes the W1/W2 project-skill fixtures and points
// the agent record's workspace at W1 (mounted on W1, not on W2). Each
// SKILL.md carries its directory's slug as its name — a fixture whose
// frontmatter name disagrees with the slug is not the skill the key names
// (CHECK round 1 finding 13).
func seedKeyValidationMounts(t *testing.T, api *restAPI, agentID string) (w1, w2 string) {
	t.Helper()
	w1 = filepath.Join(api.homePath, "workspaces", "w1")
	w2 = filepath.Join(api.homePath, "workspaces", "w2")
	for slug, dir := range map[string]string{
		"notes": filepath.Join(w1, ".claude", "skills", "notes"),
		"todo":  filepath.Join(w2, ".claude", "skills", "todo"),
	} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
			[]byte("---\nname: "+slug+"\ndescription: Project skill fixture "+slug+".\n---\n\nBody."), 0o644))
	}
	if _, err := agentstore.New(api.homePath).Update(agentID, func(ac *config.AgentConfig) error {
		ac.Home = w1
		return nil
	}); err != nil {
		t.Fatalf("setup: mount the agent on W1: %v", err)
	}
	return w1, w2
}

// sysDepsFor builds systools Deps wired to the gateway's own config, the same
// field set pkg/sysagent/tools' real-save harness uses.
func sysDepsFor(t *testing.T, api *restAPI) *systools.Deps {
	t.Helper()
	cfgPath := filepath.Join(api.homePath, "config.json")
	return &systools.Deps{
		Home:       api.homePath,
		ConfigPath: cfgPath,
		GetCfg:     api.agentLoop.GetConfig,
		MutateConfig: func(fn func(*config.Config) error) error {
			return api.agentLoop.MutateConfig(fn)
		},
		SaveConfigLocked: func(cfg *config.Config) error {
			return config.SaveConfig(cfgPath, cfg)
		},
	}
}

// sysStoredSkillOverrides reads the agent entity record's stored overrides
// from the FLAT durable AgentConfig — pkg/entity/store.go::Store.write
// marshals the AgentConfig directly, and the per-agent override map (tool
// keys and skill keys alike) is AgentConfig.Tools.Builtin.Policies
// (pkg/sysagent/tools/agent_read.go::storedOverrides). CHECK round 1
// finding 12: the earlier invented `agent.tools.skills` wrapper decoded
// correct flat records into zero values.
func sysStoredSkillOverrides(t *testing.T, home, agentID string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "entities", "agents", agentID+".json"))
	require.NoError(t, err, "entity record must exist")
	var ac config.AgentConfig
	require.NoError(t, json.Unmarshal(raw, &ac), "flat AgentConfig entity JSON: %s", raw)
	if ac.Tools == nil {
		return nil
	}
	if ac.Tools.Builtin.Policies == nil {
		return nil
	}
	out := make(map[string]any, len(ac.Tools.Builtin.Policies))
	for k, v := range ac.Tools.Builtin.Policies {
		out[k] = string(v)
	}
	return out
}

// sysSetAgentPermissions drives the real set_agent_permissions tool with the
// given skill_policy_changes map, reading the agent's revision first.
func sysSetAgentPermissions(t *testing.T, deps *systools.Deps, agentID string, changes map[string]any) *tools.ToolResult {
	t.Helper()
	store := agentstore.New(deps.Home)
	before, err := store.ReadState(agentID)
	require.NoError(t, err, "setup: read the agent's revision")
	res := systools.NewAgentPermissionsTool(deps).Execute(context.Background(), map[string]any{
		"id":                   agentID,
		"revision":             before.Revision,
		"skill_policy_changes": changes,
	})
	return res
}

// sysGetAgentSkillOverrides drives the real get_agent tool and returns the
// stored overrides its FLAT body reports (agent_get.go::storedDetail puts
// `stored_overrides` at the top level, next to id and skills). CHECK round 1
// finding 12: the earlier invented nested wrapper parsed zero values.
func sysGetAgentSkillOverrides(t *testing.T, deps *systools.Deps, agentID string) map[string]any {
	t.Helper()
	res := systools.NewAgentGetTool(deps).Execute(context.Background(), map[string]any{"id": agentID})
	require.False(t, res.IsError, "get_agent must answer: %s", res.ForLLM)
	var body struct {
		StoredOverrides map[string]any `json:"stored_overrides"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &body), "get_agent body: %s", res.ForLLM)
	return body.StoredOverrides
}

// readBackDurable asserts the DURABLE storage read-back through get_agent's
// flat stored_overrides: exactly the wanted value, or the key absent.
func readBackDurable(t *testing.T, deps *systools.Deps, agentID, key, want string) {
	t.Helper()
	sys := sysGetAgentSkillOverrides(t, deps, agentID)
	if want == "" {
		require.Nil(t, sys[key], "get_agent durable read-back: %v must not carry %s", sys, key)
	} else {
		require.Equal(t, want, sys[key], "get_agent durable read-back carries the override")
	}
}

// readBackEligibleRow asserts the C-4 UI listing on GET /agents/{id}/tools:
// AgentToolsResponse.skills lists GRANTED installed skills plus MOUNTED
// project skills — the eligible rows — never every stored ungranted
// override (CHECK round 1 finding 13: the earlier helper demanded a row
// whenever any override existed, which an installed-but-ungranted key can
// never satisfy). want "" asserts the row, when listed, carries no
// configured_policy.
func readBackEligibleRow(t *testing.T, api *restAPI, agentID, key, want string) {
	t.Helper()
	skills := readAgentToolsSkills(t, api, agentID)
	for _, s := range skills {
		entry, ok := s.(map[string]any)
		require.True(t, ok)
		if entry["key"] == key {
			if want == "" {
				require.Nil(t, entry["configured_policy"], "GET tools read-back: %v", entry)
			} else {
				require.Equal(t, want, entry["configured_policy"], "GET tools read-back: %v", entry)
			}
			return
		}
	}
	require.Fail(t, "GET tools read-back lists the eligible key",
		"%s must be listed (C-4 lists granted installed skills and mounted project skills): %v", key, skills)
}

// readBackThroughBothReaders asserts BOTH surfaces for an ELIGIBLE key
// (granted installed or mounted project): durable storage and the UI row.
func readBackThroughBothReaders(t *testing.T, api *restAPI, deps *systools.Deps, agentID, key, want string) {
	t.Helper()
	readBackDurable(t, deps, agentID, key, want)
	readBackEligibleRow(t, api, agentID, key, want)
}

// requireNotListedInUITools asserts an INELIGIBLE key (an installed skill
// the agent is not granted) carries no row on GET /agents/{id}/tools — the
// stored override exists durably but must not create a UI listing row.
func requireNotListedInUITools(t *testing.T, api *restAPI, agentID, key string) {
	t.Helper()
	skills := readAgentToolsSkills(t, api, agentID)
	for _, s := range skills {
		entry, ok := s.(map[string]any)
		require.True(t, ok)
		require.NotEqual(t, key, entry["key"],
			"an ungranted installed override must not appear as a UI listing row; skills: %v", skills)
	}
}

func TestSkillKeyValidation_AllWriters(t *testing.T) {
	const mountedKey = "project:w1/notes"
	const foreignKey = "project:w2/todo"
	const notMountedText = "`project:w2/todo` is not a skill this agent can use."

	t.Run("D16#1 PUT stores a mounted project key; the read-back pair shows it", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		w1, _ := seedKeyValidationMounts(t, api, "test-agent")
		_ = w1
		w := putAgentSkills(t, api, "test-agent", fmt.Sprintf(`{"policies":{%q:"deny"}}`, mountedKey))
		require.Equal(t, http.StatusOK, w.Code, "D16#1: 200, body: %s", w.Body.String())
		deps := sysDepsFor(t, api)
		readBackThroughBothReaders(t, api, deps, "test-agent", mountedKey, "deny")
	})

	t.Run("D16#2 PUT refuses a project key of a workspace the agent is not mounted on", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		seedKeyValidationMounts(t, api, "test-agent")
		w := putAgentSkills(t, api, "test-agent", fmt.Sprintf(`{"policies":{%q:"deny"}}`, foreignKey))
		require.Equal(t, http.StatusBadRequest, w.Code,
			"D16#2: 400, got %d, body: %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), notMountedText,
			"D16#2: the refusal carries the spec's text")
		require.Nil(t, sysStoredSkillOverrides(t, api.homePath, "test-agent"),
			"D16#2: nothing stored after the refusal")
	})

	t.Run("D16#3 set_agent_permissions stores the mounted project key", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		seedKeyValidationMounts(t, api, "test-agent")
		deps := sysDepsFor(t, api)
		res := sysSetAgentPermissions(t, deps, "test-agent", map[string]any{
			"set": map[string]any{mountedKey: "deny"},
		})
		require.False(t, res.IsError, "D16#3: accepted as row 1 is; got: %s", res.ForLLM)
		readBackThroughBothReaders(t, api, deps, "test-agent", mountedKey, "deny")
	})

	t.Run("D16#4 set_agent_permissions refuses the not-mounted project key with row 2's text", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		seedKeyValidationMounts(t, api, "test-agent")
		res := sysSetAgentPermissions(t, sysDepsFor(t, api), "test-agent", map[string]any{
			"set": map[string]any{foreignKey: "deny"},
		})
		require.True(t, res.IsError, "D16#4: refused; got: %s", res.ForLLM)
		require.Contains(t, res.ForLLM, notMountedText,
			"D16#4: the refusal carries row 2's text; got: %s", res.ForLLM)
	})

	t.Run("D16#5 create_agent mounted on W1 stores the mounted project key", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		w1, _ := seedKeyValidationMounts(t, api, "test-agent")
		deps := sysDepsFor(t, api)
		args := map[string]any{
			"name":        "keyval-create",
			"description": "created for the D16#5 row",
			"soul":        "You help.",
			"model":       "test/model",
			"color":       "#6E8BA8",
			// The agent is created mounted on W1 (the row's Given); GREEN
			// binds the mount through the create flow's workspace argument.
			"workspace":            w1,
			"skill_policy_changes": map[string]any{"set": map[string]any{mountedKey: "ask"}},
		}
		res := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
		require.False(t, res.IsError,
			"D16#5: create_agent must accept the mounted project key; got: %s", res.ForLLM)
		var created map[string]any
		require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &created), "body: %s", res.ForLLM)
		agentID, _ := created["id"].(string)
		require.NotEmpty(t, agentID, "create_agent returns the created id")
		stored := sysStoredSkillOverrides(t, api.homePath, agentID)
		require.Equal(t, "ask", stored[mountedKey], "D16#5: the project key is stored on the created agent")
	})

	t.Run("D16#6 an installed, not-granted key is stored durably and creates no UI row", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath) // plan installed, NOT granted
		seedKeyValidationMounts(t, api, "test-agent")
		w := putAgentSkills(t, api, "test-agent", `{"policies":{"plan":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "D16#6: stored, body: %s", w.Body.String())
		// Durable storage carries the override (CHECK finding 13: durable and
		// eligible-listing are separate surfaces).
		readBackDurable(t, sysDepsFor(t, api), "test-agent", "plan", "deny")
		// The C-4 UI listing lists granted installed skills and mounted
		// project skills — an ungranted override never creates a row.
		requireNotListedInUITools(t, api, "test-agent", "plan")
	})

	t.Run("D16#7 an unknown key is refused by any writer with nothing stored", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		seedKeyValidationMounts(t, api, "test-agent")
		w := putAgentSkills(t, api, "test-agent", `{"policies":{"nope":"deny"}}`)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"D16#7: 400, got %d, body: %s", w.Code, w.Body.String())
		require.Nil(t, sysStoredSkillOverrides(t, api.homePath, "test-agent"),
			"D16#7: nothing stored")
	})

	t.Run("D16#8 PUT refuses skills: null naming skills", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		seedKeyValidationMounts(t, api, "test-agent")
		w := putAgentSkills(t, api, "test-agent", `null`)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"D16#8: 400, got %d, body: %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "skills", "D16#8: the refusal names skills")
	})

	t.Run("D16#9 a skills-only PUT clears overrides and keeps tool overrides", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		grantPlanSkill(t, api, "test-agent")
		seedKeyValidationMounts(t, api, "test-agent")
		// Seed a positive skill override AND an unrelated TOOL override first
		// (CHECK finding 13: require a positive seeded override before testing
		// its removal, and prove the tool override survives the skills-only
		// clear).
		w := putAgentSkills(t, api, "test-agent", `{"policies":{"plan":"deny","send_email":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: %s", w.Body.String())
		readBackDurable(t, sysDepsFor(t, api), "test-agent", "plan", "deny")
		readBackDurable(t, sysDepsFor(t, api), "test-agent", "send_email", "deny")
		// skills-only envelope: revision, override_names: [], skills.policies: {}
		body := `{"override_names":[],"skills":{"policies":{}}}`
		r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent/tools", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r = withAdminRole(r)
		w2 := httptest.NewRecorder()
		api.updateAgentTools(w2, r, "test-agent")
		require.Equal(t, http.StatusOK, w2.Code, "D16#9: 200, body: %s", w2.Body.String())
		readBackThroughBothReaders(t, api, sysDepsFor(t, api), "test-agent", "plan", "")
		// The unrelated tool override survives the skills-only clear.
		readBackDurable(t, sysDepsFor(t, api), "test-agent", "send_email", "deny")
	})

	t.Run("D16#10 override_names without builtin is refused with the envelope text", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		seedKeyValidationMounts(t, api, "test-agent")
		r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent/tools",
			strings.NewReader(`{"override_names":["bash"]}`))
		r.Header.Set("Content-Type", "application/json")
		r = withAdminRole(r)
		w := httptest.NewRecorder()
		api.updateAgentTools(w, r, "test-agent")
		require.Equal(t, http.StatusBadRequest, w.Code,
			"D16#10: 400, got %d, body: %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "override_names names tools that are not in this request.",
			"D16#10: the envelope text")
	})

	t.Run("D16#11 set_agent_permissions clear removes every skill override", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		seedKeyValidationMounts(t, api, "test-agent")
		deps := sysDepsFor(t, api)
		res := sysSetAgentPermissions(t, deps, "test-agent", map[string]any{
			"set": map[string]any{"plan": "deny", mountedKey: "deny"},
		})
		require.False(t, res.IsError, "setup set: %s", res.ForLLM)
		res = sysSetAgentPermissions(t, deps, "test-agent", map[string]any{"clear": true})
		require.False(t, res.IsError, "D16#11: accepted; got: %s", res.ForLLM)
		require.Nil(t, sysStoredSkillOverrides(t, api.homePath, "test-agent"),
			"D16#11: no skill overrides after the clear")
	})

	t.Run("D16#12 remove takes out exactly the named key and keeps the others", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		grantPlanSkill(t, api, "test-agent")
		seedKeyValidationMounts(t, api, "test-agent")
		deps := sysDepsFor(t, api)
		res := sysSetAgentPermissions(t, deps, "test-agent", map[string]any{
			"set": map[string]any{"plan": "deny", mountedKey: "deny"},
		})
		require.False(t, res.IsError, "setup set: %s", res.ForLLM)
		res = sysSetAgentPermissions(t, deps, "test-agent", map[string]any{
			"remove": []any{mountedKey},
		})
		require.False(t, res.IsError, "D16#12: accepted; got: %s", res.ForLLM)
		stored := sysStoredSkillOverrides(t, api.homePath, "test-agent")
		if _, present := stored[mountedKey]; present {
			t.Fatalf("D16#12: %s must be removed; stored: %v", mountedKey, stored)
		}
		require.Equal(t, "deny", stored["plan"], "D16#12: the other override is kept; stored: %v", stored)
	})

	t.Run("D16#13 a deleted workspace reconciles its keys away everywhere", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		w1, _ := seedKeyValidationMounts(t, api, "test-agent")
		w := putAgentSkills(t, api, "test-agent", fmt.Sprintf(`{"policies":{%q:"deny"}}`, mountedKey))
		require.Equal(t, http.StatusOK, w.Code, "setup: %s", w.Body.String())

		if reconcileSkillKeysForRemovedWorkspace == nil {
			t.Fatal("BLOCKED: the C-23 reconcile that removes a deleted workspace's project keys " +
				"(D16#13: keys removed everywhere; the writer then refuses the key) has no entry " +
				"point — required by S-116 and Dataset D16 row 13")
		}
		require.NoError(t, os.RemoveAll(w1), "setup: delete W1")
		reconcileSkillKeysForRemovedWorkspace(t, api, w1)

		stored := sysStoredSkillOverrides(t, api.homePath, "test-agent")
		if _, present := stored[mountedKey]; present {
			t.Fatalf("D16#13: the deleted workspace's key must be removed everywhere; stored: %v", stored)
		}
		w2 := putAgentSkills(t, api, "test-agent", fmt.Sprintf(`{"policies":{%q:"deny"}}`, mountedKey))
		require.Equal(t, http.StatusBadRequest, w2.Code,
			"D16#13: the same write is now 400, got %d, body: %s", w2.Code, w2.Body.String())
	})

	t.Run("D16#14 an unrelated tools_cfg.mcp write changes no skill override", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		seedKeyValidationMounts(t, api, "test-agent")
		r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent",
			strings.NewReader(`{"tools_cfg":{"skills":{"policies":{"plan":"deny"}}}}`))
		r.Header.Set("Content-Type", "application/json")
		r = withAdminRole(r)
		rec := httptest.NewRecorder()
		api.HandleAgents(rec, r)
		require.Equal(t, http.StatusOK, rec.Code, "setup: %s", rec.Body.String())

		r = revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent",
			strings.NewReader(`{"tools_cfg":{"mcp":{"servers":[]}}}`))
		r.Header.Set("Content-Type", "application/json")
		r = withAdminRole(r)
		rec = httptest.NewRecorder()
		api.HandleAgents(rec, r)
		require.Equal(t, http.StatusOK, rec.Code, "D16#14: 200, body: %s", rec.Body.String())
		require.Equal(t, map[string]any{"plan": "deny"},
			sysStoredSkillOverrides(t, api.homePath, "test-agent"),
			"D16#14: the skill override is unchanged")
	})
}

// reconcileSkillKeysForRemovedWorkspace runs the C-23 reconcile that removes
// every project key of a deleted workspace from every agent record and the
// known-skill set. GREEN wires it to the workspace-deletion path's reconcile
// (or its load-time equivalent); nil keeps the RED run loud (BLOCKED), never
// a skip.
var reconcileSkillKeysForRemovedWorkspace func(t testing.TB, api *restAPI, workspaceDir string)
