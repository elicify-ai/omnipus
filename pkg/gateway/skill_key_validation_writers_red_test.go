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
// .claude/skills/notes, W2 with .claude/skills/todo; real workspace membership
// and protected mount records grant W1 only to the test agent.
//
// RED today: no writer reads skill keys at this commit — updateAgentTools
// drops the whole skills member (order 8's RED), set_agent_permissions and
// create_agent refuse skill_policy_changes as an unknown field (order 15/42's
// RED), and the C-23 known-skill validation/reconciliation does not exist.
// D16#13 starts from an upgraded home (the real skillperm.Run), deletes W1
// through HandleWorkspaces, drives GET /skills (FR-080) and reads the key's
// absence from config.json, every agent record, the running agents and both
// read-backs; a sibling row leaves the upgrade in_progress and expects GET
// /skills to write nothing (architect Ruling 1 on handoff-8). No action or
// deletion seam may stand in for those production paths.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/skillperm"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/helpers/agentrecord"

	"github.com/stretchr/testify/require"
)

// seedKeyValidationMounts establishes real workspace membership AND protected
// mounts. AgentConfig.Home is not mount authority. Host folders stay outside
// OMNIPUS_HOME, as required by CreateMount; deleting a workspace must never
// delete the mounted host folder. Every skill's frontmatter matches its slug.
func seedKeyValidationMounts(t *testing.T, api *restAPI, agentID string) (w1, w2 string) {
	t.Helper()
	w1, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	w2, err = filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	for _, row := range []struct{ id, slug, root string }{{"w1", "notes", w1}, {"w2", "todo", w2}} {
		dir := filepath.Join(row.root, ".claude", "skills", row.slug)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
			[]byte("---\nname: "+row.slug+"\ndescription: Project skill fixture "+row.slug+".\n---\n\nBody."), 0o644))
		members := []string{}
		if row.id == "w1" {
			members = []string{agentID}
		}
		require.NoError(t, workspace.SaveRecord(api.homePath, workspace.Workspace{ID: row.id, Name: row.id, Status: "active", CoreTeam: members}))
		_, err := workspace.EnsureWorkDir(api.homePath, row.id)
		require.NoError(t, err)
		_, warning, err := workspace.CreateMount(api.homePath, row.id, "project", row.root)
		require.NoError(t, err)
		require.Empty(t, warning)
		require.Equal(t, []string{row.root}, workspace.AllowedMountRoots(api.homePath, row.id))
	}
	return w1, w2
}

// deleteKeyValidationWorkspace is a direct native action, not a replaceable
// reconciliation callback. Independent file/mount observers certify the action
// occurred and that the host folder was not deleted along with the workspace.
func deleteKeyValidationWorkspace(t *testing.T, api *restAPI, hostRoot string) {
	t.Helper()
	require.FileExists(t, filepath.Join(api.homePath, "workspaces", "w1.json"))
	deleted := httptest.NewRecorder()
	api.HandleWorkspaces(deleted, withAdminRole(httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, "w1"), nil)))
	require.Equal(t, http.StatusOK, deleted.Code, "native workspace delete: %s", deleted.Body.String())
	require.NoFileExists(t, filepath.Join(api.homePath, "workspaces", "w1.json"))
	require.NoDirExists(t, workspace.WorkspaceDir(api.homePath, "w1"))
	require.Empty(t, workspace.AllowedMountRoots(api.homePath, "w1"), "protected mount record is removed too")
	require.FileExists(t, filepath.Join(hostRoot, ".claude", "skills", "notes", "SKILL.md"), "deleting W1 preserves its mounted host folder")
}

// D16#13 (FR-080, C-19; architect Ruling 1 on handoff-8, arch/rulings-handoff-8.md).
// GET /skills reconciles the skill keys of a deleted workspace, but ONLY once the
// one-time C-19 upgrade is done: spec line 154 says the heal "runs after the one-time
// migrations of C-19", and a heal that ran first would store Ask for a skill whose
// planned value is Deny. The scenario is therefore two rows over one Given — a workspace
// W1 whose project key is stored in every place a key lives and which is then deleted
// through the real path:
//
//   - an UPGRADED home (the real skillperm.Run ran first, so the marker
//     sandbox.migrations.skill_permissions is "done"): GET /skills removes the key from
//     sandbox.skill_policies, sandbox.skill_visibility, EVERY agent record on disk and the
//     live agent copies; both read-backs of D16 are free of it; D16 row 1 now answers 400;
//   - the upgrade STILL IN PROGRESS (the real skillperm.Run stopped on a failed agent
//     write: the ledger says in_progress and the marker is absent): GET /skills writes
//     nothing, anywhere.
//
// The first row is the instrument for the second: the same GET removes the key when the
// upgrade is done, so "writes nothing" cannot pass by the GET having nothing to do.
const (
	// goneKeyOtherAgent is a second agent that carries the key, so "every agent record"
	// is more than the one the PUT wrote.
	goneKeyOtherAgent = "other-agent"
	// upgradeLedgerFile is the progress record of the C-19 upgrade, under <home>/entities
	// (pkg/skillperm/upgrade.go, ledgerName). A rename fails the Given checks loudly.
	upgradeLedgerFile = ".skill-permissions-upgrade-ledger.json"
)

// storedUpgradeMarker reads sandbox.migrations.skill_permissions from the durable
// config.json ("" when absent).
func storedUpgradeMarker(t *testing.T, home string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	var document struct {
		Sandbox struct {
			Migrations map[string]string `json:"migrations"`
		} `json:"sandbox"`
	}
	require.NoError(t, json.Unmarshal(raw, &document), "config.json: %s", raw)
	return document.Sandbox.Migrations["skill_permissions"]
}

// readUpgradeLedger reads the upgrade's progress record from disk.
func readUpgradeLedger(t *testing.T, home string) skillperm.Ledger {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "entities", upgradeLedgerFile))
	require.NoError(t, err, "Given: the upgrade left its ledger")
	var ledger skillperm.Ledger
	require.NoError(t, json.Unmarshal(raw, &ledger), "ledger: %s", raw)
	return ledger
}

// upgradeKeyValidationHome runs the real one-time C-19 upgrade over the harness home. The
// harness builds the agent loop before anything else, whereas a production start runs the
// upgrade first, so the gateway then loads the upgraded settings from disk the way a boot
// after the upgrade does; without that the live config would not carry the marker and the
// next save would drop it.
func upgradeKeyValidationHome(t *testing.T, api *restAPI) {
	t.Helper()
	require.NoError(t, skillperm.Run(api.homePath, skillperm.Options{}), "the real one-time C-19 upgrade")
	require.Equal(t, "done", storedUpgradeMarker(t, api.homePath), "Given: the upgrade marker is present")
	require.Equal(t, "done", readUpgradeLedger(t, api.homePath).State, "Given: the ledger is done")
	require.NoError(t, api.refreshConfigAndRewireServices(filepath.Join(api.homePath, "config.json")),
		"the gateway loads the upgraded settings")
	require.Equal(t, "done", api.agentLoop.GetConfig().Sandbox.Migrations["skill_permissions"],
		"Given: the live config carries the marker, so a later save keeps it")
}

// leaveKeyValidationUpgradeInProgress runs the real upgrade and stops it on a failed agent
// record write, which is how an upgrade stays unfinished: the ledger says in_progress with
// the agents pending, and the marker is never written.
func leaveKeyValidationUpgradeInProgress(t *testing.T, api *restAPI) {
	t.Helper()
	stopped := errors.New("simulated agent record write failure")
	err := skillperm.Run(api.homePath, skillperm.Options{BeforeAgentWrite: func(string) error { return stopped }})
	require.ErrorIs(t, err, stopped, "the real upgrade stops on the failed agent write")
	ledger := readUpgradeLedger(t, api.homePath)
	require.Equal(t, "in_progress", ledger.State, "Given: the ledger is in_progress")
	require.NotEmpty(t, ledger.Pending, "Given: agents are still waiting for their plan")
	require.Empty(t, storedUpgradeMarker(t, api.homePath), "Given: the upgrade marker is not written")
}

// goneKeySecondAgent is a fresh record each call (the live copy and the stored copy never
// share a map): a custom agent with complete builtin coverage that carries the project key
// and one surviving override.
func goneKeySecondAgent(mountedKey string) config.AgentConfig {
	agent := config.AgentConfig{
		ID:    goneKeyOtherAgent,
		Name:  "Other Agent",
		Type:  config.AgentTypeCustom,
		Tools: coreagent.NewCustomAgentToolsCfg(),
	}
	agent.Tools.Skills = &config.AgentSkillsCfg{Policies: map[string]config.ToolPolicy{
		mountedKey: config.ToolPolicyDeny,
		"plan":     config.ToolPolicyAllow,
	}}
	return agent
}

// goneKeyState is what the durable files say about the skill keys.
type goneKeyState struct {
	policies, visibility map[string]string         // sandbox.skill_policies, sandbox.skill_visibility
	agents               map[string]map[string]any // agent id -> tools.skills.policies of its record
}

// readGoneKeyState reads the two global maps from config.json and the own skill overrides
// of EVERY agent record under entities/agents.
func readGoneKeyState(t *testing.T, home string) goneKeyState {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &document))
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(document["sandbox"], &fields))
	state := goneKeyState{agents: map[string]map[string]any{}}
	for field, into := range map[string]*map[string]string{
		"skill_policies": &state.policies, "skill_visibility": &state.visibility,
	} {
		require.Contains(t, fields, field, "C-23 native map must exist, not be silently dropped")
		require.NoError(t, json.Unmarshal(fields[field], into))
	}
	records, err := filepath.Glob(filepath.Join(filepath.Dir(agentrecord.Path(home, "x")), "*.json"))
	require.NoError(t, err)
	for _, path := range records {
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		state.agents[id] = agentrecord.Policies(t, home, id, agentrecord.Skills)
	}
	return state
}

// liveSkillOverrides is the own skill override map the RUNNING gateway holds for an agent
// (GET /agents/{id}/tools reads this copy, not the file).
func liveSkillOverrides(api *restAPI, agentID string) map[string]config.ToolPolicy {
	for _, agent := range api.agentLoop.GetConfig().Agents.List {
		if agent.ID == agentID && agent.Tools != nil && agent.Tools.Skills != nil {
			return agent.Tools.Skills.Policies
		}
	}
	return nil
}

// getSkillsInventory drives GET /skills, the reconcile entry FR-080 names.
func getSkillsInventory(t *testing.T, api *restAPI) {
	t.Helper()
	inventory := httptest.NewRecorder()
	api.HandleSkills(inventory, withAdminRole(httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)))
	require.Equal(t, http.StatusOK, inventory.Code, "native skill-inventory reconcile: %s", inventory.Body.String())
}

// seedGoneKeyScenario stores W1's project key in every place a key lives, through the real
// writers where one exists: the agent's own override (PUT), both global maps (PATCH
// permissions) and a second agent's record. It returns W1's host folder. The key is shown
// through both read-backs first, so its later absence there means something.
func seedGoneKeyScenario(t *testing.T, api *restAPI, mountedKey string) (hostRoot string) {
	t.Helper()
	installPlanSkillFixture(t, api.homePath)
	grantPlanSkill(t, api, "test-agent")
	hostRoot, _ = seedKeyValidationMounts(t, api, "test-agent")
	second := goneKeySecondAgent(mountedKey)
	stored := goneKeySecondAgent(mountedKey)
	require.NoError(t, agentstore.New(api.homePath).Create(goneKeyOtherAgent, &stored))
	live := api.agentLoop.GetConfig()
	live.Agents.List = append(live.Agents.List, second)

	w := putAgentSkills(t, api, "test-agent", fmt.Sprintf(`{"policies":{%q:"deny","plan":"deny"}}`, mountedKey))
	require.Equal(t, http.StatusOK, w.Code, "setup: %s", w.Body.String())
	t.Setenv("OMNIPUS_BEARER_TOKEN", permissionsTestBearer)
	routes := routeTable{}
	api.registerAdditionalEndpoints(routes)
	global := patchPermissions(t, api, routes, fmt.Sprintf(
		`{"skill_policies":{%q:"ask","plan":"ask"},"skill_visibility":{%q:"name_only","plan":"full"}}`, mountedKey, mountedKey))
	require.Equal(t, http.StatusOK, global.Code, "seed ALL project-key maps: %s", global.Body.String())
	readBackThroughBothReaders(t, api, sysDepsFor(t, api), "test-agent", mountedKey, "deny")
	requireGoneKeyStoredEverywhere(t, api, mountedKey)
	return hostRoot
}

// requireGoneKeyStoredEverywhere is the Given of both rows: the key is in both global maps,
// in the record of each of the two agents, and in the live copy of each.
func requireGoneKeyStoredEverywhere(t *testing.T, api *restAPI, mountedKey string) {
	t.Helper()
	state := readGoneKeyState(t, api.homePath)
	require.Contains(t, state.policies, mountedKey, "Given: the key is in sandbox.skill_policies")
	require.Contains(t, state.visibility, mountedKey, "Given: the key is in sandbox.skill_visibility")
	ids := []string{}
	for id := range state.agents {
		ids = append(ids, id)
	}
	require.ElementsMatch(t, []string{"test-agent", goneKeyOtherAgent}, ids,
		"instrument: these two are every agent record on disk, so 'each record' means both")
	for _, id := range ids {
		require.Contains(t, state.agents[id], mountedKey, "Given: the record of %s carries the key", id)
		require.Contains(t, liveSkillOverrides(api, id), mountedKey, "Given: the running copy of %s carries the key", id)
	}
}

// testGoneKeysAfterUpgrade is the upgraded-home row.
func testGoneKeysAfterUpgrade(t *testing.T, mountedKey string) {
	t.Helper()
	api := buildExecutorTestAPI(t)
	upgradeKeyValidationHome(t, api)
	w1 := seedGoneKeyScenario(t, api, mountedKey)
	deleteKeyValidationWorkspace(t, api, w1)

	getSkillsInventory(t, api)

	// Observe the durable files, not a lossy typed config projection.
	after := readGoneKeyState(t, api.homePath)
	require.NotContains(t, after.policies, mountedKey, "D16#13: the deleted project's key left sandbox.skill_policies")
	require.NotContains(t, after.visibility, mountedKey, "D16#13: the deleted project's key left sandbox.skill_visibility")
	require.Equal(t, "ask", after.policies["plan"], "unrelated global skill permission remains")
	require.Equal(t, "full", after.visibility["plan"], "unrelated global skill level remains")
	require.Equal(t, map[string]map[string]any{
		"test-agent":      {"plan": "deny"},
		goneKeyOtherAgent: {"plan": "allow"},
	}, after.agents, "D16#13: the key left EVERY agent record on disk; each agent keeps its other override")

	// The running gateway's own copies lose the key in the same step, with no reload in
	// between. Ruling 1's condition offers two ways: "update the live agent copies in the same
	// step" or "have the test drive a reload before reading back"; the backend took the first
	// (hand-off 9: "I update the live agent copies in the same step"). Were that changed to
	// the reload, this assertion goes and the read-backs below drive a reload first; the disk
	// checks above stay.
	require.Equal(t, map[string]config.ToolPolicy{"plan": config.ToolPolicyDeny}, liveSkillOverrides(api, "test-agent"),
		"D16#13: the running copy of test-agent lost the key with no reload")
	require.Equal(t, map[string]config.ToolPolicy{"plan": config.ToolPolicyAllow}, liveSkillOverrides(api, goneKeyOtherAgent),
		"D16#13: the running copy of %s lost the key with no reload", goneKeyOtherAgent)

	// Both D16 read-backs: the surviving override is listed (the instrument), the gone key is not.
	deps := sysDepsFor(t, api)
	readBackThroughBothReaders(t, api, deps, "test-agent", "plan", "deny")
	require.NotContains(t, sysGetAgentSkillOverrides(t, deps, "test-agent"), mountedKey, "get_agent does not show the removed key")
	requireNotListedInUITools(t, api, "test-agent", mountedKey)

	// D16 row 1 now refuses the key: its workspace is gone.
	w := putAgentSkills(t, api, "test-agent", fmt.Sprintf(`{"policies":{%q:"deny"}}`, mountedKey))
	requireErrContains(t, w, http.StatusBadRequest, fmt.Sprintf("`%s` is not a skill this agent can use.", mountedKey))
}

// testGoneKeysDuringUpgrade is the in-progress row: nothing is written, anywhere.
func testGoneKeysDuringUpgrade(t *testing.T, mountedKey string) {
	t.Helper()
	api := buildExecutorTestAPI(t)
	w1 := seedGoneKeyScenario(t, api, mountedKey)
	deleteKeyValidationWorkspace(t, api, w1)
	leaveKeyValidationUpgradeInProgress(t, api)
	// The stale key is still stored everywhere, so a reconcile that ran now would have work to do.
	requireGoneKeyStoredEverywhere(t, api, mountedKey)
	before := timedCardHomeTree(t, api.homePath)

	getSkillsInventory(t, api)

	require.Equal(t, before, timedCardHomeTree(t, api.homePath),
		"D16#13: with the ledger in_progress GET /skills writes nothing (no key added, none removed, no record rewritten)")
	requireGoneKeyStoredEverywhere(t, api, mountedKey)
	require.Equal(t, "in_progress", readUpgradeLedger(t, api.homePath).State, "GET /skills did not advance the upgrade")
	require.Empty(t, storedUpgradeMarker(t, api.homePath), "GET /skills did not write the upgrade marker")
}

// Keep the native deletion/reconcile scenario separate from the writer table;
// each has one job and remains below the function-size budget.
func testDeletedWorkspaceSkillKeys(t *testing.T, mountedKey string) {
	t.Helper()
	t.Run("upgraded home: GET /skills removes the key everywhere", func(t *testing.T) {
		testGoneKeysAfterUpgrade(t, mountedKey)
	})
	t.Run("upgrade in progress: GET /skills writes nothing", func(t *testing.T) {
		testGoneKeysDuringUpgrade(t, mountedKey)
	})
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

// sysStoredSkillOverrides reads C-4's per-agent skill overrides from the flat
// durable record: tools.skills.policies at the top level of the entity file
// (config.AgentConfig.Tools is json:"tools"; the store marshals AgentConfig
// directly), never the unrelated builtin policy map, never an outer wrapper and
// never tools_cfg, which is only the REST request name for that object. This
// JSON storage observation deliberately does not depend on the old AgentConfig
// decoder, which has no skills member. The shared reader
// (tests/helpers/agentrecord) is shown against a record the real store wrote.
func sysStoredSkillOverrides(t *testing.T, home, agentID string) map[string]any {
	t.Helper()
	return agentrecord.Policies(t, home, agentID, agentrecord.Skills)
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

// sysGetAgentSkillOverrides selects C-9's generated skill_permissions, not
// the existing builtin-only stored_overrides. C-23 lists only eligible skills.
func sysGetAgentSkillOverrides(t *testing.T, deps *systools.Deps, agentID string) map[string]any {
	t.Helper()
	res := systools.NewAgentGetTool(deps).Execute(context.Background(), map[string]any{
		"id": agentID, "fields": []any{"skill_permissions"},
	})
	require.False(t, res.IsError, "get_agent skill_permissions must answer: %s", res.ForLLM)
	var body gen.AgentDetail
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &body), "get_agent body: %s", res.ForLLM)
	require.NotNil(t, body.SkillPermissions, "C-9 skill_permissions was requested, never silently omitted")
	out := map[string]any{}
	for _, permission := range *body.SkillPermissions {
		require.NotContains(t, out, permission.Skill, "C-23 lists each eligible key once")
		out[permission.Skill] = nil
		if permission.Override != nil {
			out[permission.Skill] = string(*permission.Override)
		}
	}
	return out
}

// readBackDurable asserts durable storage, including overrides on installed
// but ungranted skills which C-23 deliberately excludes from UI/read lists.
func readBackDurable(t *testing.T, deps *systools.Deps, agentID, key, want string) {
	t.Helper()
	stored := sysStoredSkillOverrides(t, deps.Home, agentID)
	if want == "" {
		require.NotContains(t, stored, key, "durable skill override is absent after clearing")
	} else {
		require.Equal(t, want, stored[key], "tools.skills.policies carries the override (flat stored record)")
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
	sys := sysGetAgentSkillOverrides(t, deps, agentID)
	require.Contains(t, sys, key, "get_agent lists the eligible skill (C-23)")
	if want == "" {
		require.Nil(t, sys[key], "get_agent reports no own override")
	} else {
		require.Equal(t, want, sys[key], "get_agent reports the own override")
	}
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

	t.Run("instrument native workspace delete removes record workdir and mount but preserves host files", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		w1, _ := seedKeyValidationMounts(t, api, "test-agent")
		deleteKeyValidationWorkspace(t, api, w1)
	})
	t.Run("instrument durable reader reads the flat carrier and not the builtin map or the old wrappers", func(t *testing.T) {
		home := t.TempDir()
		dir := filepath.Join(home, "entities", "agents")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		// The spec carrier (flat tools.skills.policies, C-4) holds ask; every other place a reader
		// once looked holds a different value for the same key: the builtin sibling (allow), the
		// REST request name tools_cfg (deny) and an outer agent wrapper (deny).
		raw := `{"id":"reader","tools":{"skills":{"policies":{"plan":"ask"}},"builtin":{"policies":{"plan":"allow","bash":"allow"}}},"tools_cfg":{"skills":{"policies":{"plan":"deny"}},"builtin":{"policies":{"plan":"deny"}}},"agent":{"tools":{"skills":{"policies":{"plan":"deny"}}}}}`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "reader.json"), []byte(raw), 0o600))
		require.Equal(t, map[string]any{"plan": "ask"}, sysStoredSkillOverrides(t, home, "reader"))
	})
	t.Run("instrument durable reader finds the flat record the real store wrote", func(t *testing.T) {
		// buildExecutorTestAPI persists test-agent through the real store with a complete
		// tools.builtin.policies map (coreagent.NewCustomAgentToolsCfg): the native positive control
		// that tools -> <carrier> -> policies is where the store puts overrides. It carries no skill
		// override, so the skills reader reports none rather than echoing the builtin map.
		api := buildExecutorTestAPI(t)
		builtin := agentrecord.Policies(t, api.homePath, "test-agent", agentrecord.Builtin)
		require.NotEmpty(t, builtin, "the real record carries the agent builtin overrides at the flat tools member: %s",
			agentrecord.Raw(t, api.homePath, "test-agent"))
		require.Empty(t, sysStoredSkillOverrides(t, api.homePath, "test-agent"),
			"a real record with no skill override reads as none")
	})

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
		require.Empty(t, sysStoredSkillOverrides(t, api.homePath, "test-agent"),
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
		seedKeyValidationMounts(t, api, "test-agent")
		deps := sysDepsFor(t, api)
		args := map[string]any{
			"name":        "keyval-create",
			"description": "created for the D16#5 row",
			"soul":        "You help.",
			"model":       "test/model",
			"color":       "#6E8BA8",
			// The existing create_agent destination joins W1, whose real mount
			// above supplies the project shelf. No invented workspace-path arg.
			"workspace_id":         "w1",
			"skill_policy_changes": map[string]any{"set": map[string]any{mountedKey: "ask"}},
		}
		// The conversation that creates the agent is W1's own. create_agent lets a conversation name
		// its own workspace; any other target needs Omnipus's cross-workspace setup authority, a
		// separate rule (omnipus-system-agent FR-006/FR-043, as quoted in
		// pkg/sysagent/tools/target_workspace.go::resolveTargetWorkspace) that this row does not test.
		// A bare context carries no workspace, so the first version of this call was refused with
		// WORKSPACE_NOT_AUTHORIZED before the project key was looked at: a red for the wrong reason.
		inW1 := tools.WithWorkspaceID(context.Background(), "w1")
		res := systools.NewAgentCreateTool(deps).Execute(inW1, args)
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
		requireErrContains(t, w, http.StatusBadRequest, "`nope` is not a skill this agent can use.")
		require.Empty(t, sysStoredSkillOverrides(t, api.homePath, "test-agent"),
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
		deps := sysDepsFor(t, api)
		state, err := agentstore.New(api.homePath).ReadState("test-agent")
		require.NoError(t, err)
		toolWrite := systools.NewAgentPermissionsTool(deps).Execute(context.Background(), map[string]any{
			"id": "test-agent", "revision": state.Revision,
			"tool_policy_changes": map[string]any{"set": map[string]any{"send_email": "deny"}},
		})
		require.False(t, toolWrite.IsError, "setup TOOL override: %s", toolWrite.ForLLM)
		state, err = agentstore.New(api.homePath).ReadState("test-agent")
		require.NoError(t, err)
		require.Equal(t, config.ToolPolicy("deny"), state.Agent.Tools.Builtin.Policies["send_email"])
		w := putAgentSkills(t, api, "test-agent", `{"policies":{"plan":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup SKILL override: %s", w.Body.String())
		readBackDurable(t, deps, "test-agent", "plan", "deny")
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
		state, err = agentstore.New(api.homePath).ReadState("test-agent")
		require.NoError(t, err)
		require.Equal(t, config.ToolPolicy("deny"), state.Agent.Tools.Builtin.Policies["send_email"],
			"the unrelated BUILTIN override survives the skills-only clear")
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
		require.Empty(t, sysStoredSkillOverrides(t, api.homePath, "test-agent"),
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
		testDeletedWorkspaceSkillKeys(t, mountedKey)
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
		require.Equal(t, map[string]any{"plan": "deny"},
			sysStoredSkillOverrides(t, api.homePath, "test-agent"),
			"D16#14 setup must persist the skill override before the unrelated MCP write")

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
