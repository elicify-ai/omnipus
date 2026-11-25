//go:build goolm && stdjson

package systools_test

// Order 42 — TestCreateAgent_SkillPolicyChangesParity (S-103; C-21).
// Oracle: skills-tools-redesign-spec.md S-103 — "Given global plan at Allow;
// When Omnipus calls create_agent with skills: ["plan"] and
// skill_policy_changes: {set: {plan: ask}}; Then the new agent's override
// plan: ask is stored and get_agent reports it." C-21: create_agent gains
// skill_policy_changes ({set} only), listed in
// pkg/sysagent/tools/agent_apply_args.go::agentCreateArgNames.
//
// Harness: the package's own tool-test mechanics —
// agent_test.go::newTestDepsWithRealSave, the part-1 seam
// setGlobalSkillPolicyForTest (S-103's Given is the same global skill
// storage order 15's S-70 uses), agent_adr090_config_test.go::createNativeArgs
// for the minimal native create set. The created agent's id is a random
// UUID (agent.go::newAgentID), so it is read from the create result body,
// never guessed from the name.
//
// Fixture (CHECK round 4 N20): the request carries skills: ["plan"], and
// create_agent refuses ANY explicit skills list when Deps.AgentConfigInventory
// is not wired ("skill inventory is unavailable",
// agent_inventory.go::validateSkillAssignment). newTestDepsWithRealSave wires
// none, so the earlier fixture could never get past the create call even
// with a correct implementation. s103Deps wires the listing the way boot
// does: the shipped built-in packages are synced into <home>/skills
// (skills.SyncBuiltins) and the ids are read back with
// agent.InstalledSkillIDs, the function the production inventory builder
// (pkg/gateway/sysagent_inventory.go::sysagentAgentConfigInventory) uses.
// plan is therefore installed because the product ships it, not because this
// test says so. The instrument subtests run before the seam guard, so they
// are observed on today's product: a plain plan grant (no override) is
// accepted with the inventory and refused without it, which shows the
// inventory is the one input that flips the outcome.
//
// Until GREEN binds setGlobalSkillPolicyForTest the S-103 flow stops at the
// loud BLOCKED guard. Once it is bound the RED reason is that create_agent
// accepts no skill_policy_changes argument (agentCreateArgNames) and no
// per-agent skill override storage exists (config.AgentToolsCfg has no
// skills member), so the call is refused — not a missing handler, and no
// longer a missing inventory.
import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/tests/helpers/agentrecord"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"

	"github.com/stretchr/testify/require"
)

// s103Deps is the S-103 fixture: real saved config plus the installed-skill
// inventory create_agent needs before it accepts an explicit skills list.
func s103Deps(t *testing.T) *systools.Deps {
	t.Helper()
	deps, _ := newTestDepsWithRealSave(t)
	t.Setenv("OMNIPUS_HOME", deps.Home)
	_, err := skills.SyncBuiltins(filepath.Join(deps.Home, "skills"), nil)
	require.NoError(t, err, "the shipped built-in skills install into the home the way boot installs them")
	installed := agent.InstalledSkillIDs(deps.Home)
	require.Contains(t, installed, "plan",
		"the shipped plan skill is in the installed listing the inventory is built from: %v", installed)
	deps.AgentConfigInventory = testInventory(installed, nil)
	return deps
}

// s103CreatedAgentID reads the new agent's id from a create_agent result body.
func s103CreatedAgentID(t *testing.T, body string) string {
	t.Helper()
	var created map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &created), "create_agent returns a JSON body: %s", body)
	id, _ := created["id"].(string)
	require.NotEmpty(t, id, "create_agent's result carries the new agent's id: %s", body)
	return id
}

// s103PlainPlanGrant is S-103's valid-assignment precondition, independent of
// the feature under test: with the inventory wired, a create_agent that
// grants the installed plan skill and asks for no override is accepted, and
// the grant is stored and bound to the installed folder. A refusal here is a
// broken fixture, not a missing feature.
func s103PlainPlanGrant(t *testing.T, deps *systools.Deps) string {
	t.Helper()
	args := createNativeArgs("plan grant control")
	args["skills"] = []any{"plan"}
	res := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
	require.False(t, res.IsError,
		"precondition: granting the installed plan skill at create time is accepted before any override is asked for; got: %s",
		res.ForLLM)
	agentID := s103CreatedAgentID(t, res.ForLLM)
	stored, err := agentstore.New(deps.Home).Get(agentID)
	require.NoError(t, err, "the control agent is a real stored agent")
	require.Equal(t, []string{"plan"}, stored.Skills, "the plan grant is stored on the agent")
	require.NotEmpty(t, stored.SkillGrants[skills.SkillKey("plan")],
		"the grant is bound to the installed plan folder's generation (an absent folder would persist it unbound): %v",
		stored.SkillGrants)
	return agentID
}

func TestCreateAgent_SkillPolicyChangesParity(t *testing.T) {
	t.Run("instrument the installed plan inventory lets a plain plan grant through", func(t *testing.T) {
		s103PlainPlanGrant(t, s103Deps(t))
	})
	t.Run("instrument the same plan grant is refused when the inventory is the missing input", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			hook func() systools.AgentConfigInventory
			want string
		}{
			{"no inventory wired", nil, "unavailable"},
			{"known-empty installed listing", testInventory(nil, nil), `unknown skill id: "plan"`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				deps := s103Deps(t)
				deps.AgentConfigInventory = tc.hook
				before := wu3DurableInventory(t, deps.Home)
				args := createNativeArgs("plan grant refused")
				args["skills"] = []any{"plan"}
				res := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
				require.True(t, res.IsError, "the same grant must be refused without the inventory; got: %s", res.ForLLM)
				code, msg := toolErrorCode(t, res.ForLLM)
				require.Equal(t, "INVALID_INPUT", code, "refusal body: %s", res.ForLLM)
				require.Contains(t, msg, tc.want, "the refusal names the missing inventory, not the grant itself")
				requireNoDurableChange(t, deps.Home, before, "a refused create writes no agent")
			})
		}
	})

	// The same guard as the S-70 parity test: a test-owned closure, a nil or a
	// bound-method wrapper is not the native global skill map input; the actual
	// create_agent writer and the stored agent entity are the observers.
	nativebinding.Require(t, "pkg/config", setGlobalSkillPolicyForTest,
		"C-2/C-21/S-103: native global skill map input (S-103's Given: global plan at Allow); actual create_agent writer/store is the observer")
	deps := s103Deps(t)
	setGlobalSkillPolicyForTest(deps.GetCfg(), "plan", "allow")

	args := createNativeArgs("planwriter")
	args["skills"] = []any{"plan"}
	args["skill_policy_changes"] = map[string]any{"set": map[string]any{"plan": "ask"}}
	res := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
	require.False(t, res.IsError,
		"S-103: create_agent must accept skills + skill_policy_changes ({set} only, C-21) on the fixture whose plain plan grant is accepted; got: %s",
		res.ForLLM)

	// The created agent's id is a random UUID (agent.go::newAgentID) — read
	// it from the result body, never from the name.
	agentID := s103CreatedAgentID(t, res.ForLLM)

	// Stored: the new agent's entity record carries tools.skills.policies.plan
	// = ask (the C-4 persisted shape S-97's gateway leg also asserts), read
	// through the shared flat-record reader (tests/helpers/agentrecord).
	stored := agentrecord.Policies(t, deps.Home, agentID, agentrecord.Skills)
	require.Equal(t, "ask", stored["plan"],
		"S-103: the new agent's override plan: ask must be stored (C-4 shape); entity: %s",
		agentrecord.Raw(t, deps.Home, agentID))

	// Reported: get_agent fields [skill_permissions] shows the override.
	getRes := systools.NewAgentGetTool(deps).Execute(context.Background(), map[string]any{
		"id":     agentID,
		"fields": []any{"skill_permissions"},
	})
	require.False(t, getRes.IsError, "get_agent must answer: %s", getRes.ForLLM)
	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(getRes.ForLLM), &detail),
		"get_agent returns a JSON body: %s", getRes.ForLLM)
	rows, _ := detail["skill_permissions"].([]any)
	require.NotEmpty(t, rows,
		"S-103: get_agent fields [skill_permissions] must report rows: %s", getRes.ForLLM)
	found := false
	for _, rowAny := range rows {
		row, _ := rowAny.(map[string]any)
		// AgentSkillPermission's required fields are skill/effective/override
		// (contracts/components/schemas/AgentSkillPermission.yaml) — the row
		// is selected by its `skill` key, not a `name` (CHECK round 1
		// finding 12: the earlier select read a field the carrier has never
		// carried).
		if row == nil || row["skill"] != "plan" {
			continue
		}
		found = true
		require.Equal(t, "ask", row["override"],
			"S-103: get_agent must report the plan override ask (row: %v)", row)
	}
	require.True(t, found, "S-103: get_agent's skill_permissions must include plan: %s", getRes.ForLLM)

	// The store round-trips: the entity is readable through the real store.
	_, err := agentstore.New(deps.Home).Get(agentID)
	require.NoError(t, err, "the created agent is a real stored agent")
}
