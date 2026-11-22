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
// RED today: create_agent accepts no skill_policy_changes argument
// (agentCreateArgNames) and no per-agent skill override storage exists
// (config.AgentToolsCfg has no skills member), so the call is refused —
// the RED reason is that refusal, not a missing handler.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"

	"github.com/stretchr/testify/require"
)

func TestCreateAgent_SkillPolicyChangesParity(t *testing.T) {
	if setGlobalSkillPolicyForTest == nil {
		t.Fatal("BLOCKED: the C-2 global skill policy storage is not implemented — " +
			"required by skills-tools-redesign-spec S-103's Given (global plan at Allow) " +
			"and the create_agent skill_policy_changes parity of C-21")
	}
	deps, _ := newTestDepsWithRealSave(t)
	t.Setenv("OMNIPUS_HOME", deps.Home)
	setGlobalSkillPolicyForTest(deps.GetCfg(), "plan", "allow")

	args := createNativeArgs("planwriter")
	args["skills"] = []any{"plan"}
	args["skill_policy_changes"] = map[string]any{"set": map[string]any{"plan": "ask"}}
	res := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
	require.False(t, res.IsError,
		"S-103: create_agent must accept skills + skill_policy_changes ({set} only, C-21); got: %s", res.ForLLM)

	// The created agent's id is a random UUID (agent.go::newAgentID) — read
	// it from the result body, never from the name.
	var created map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &created),
		"create_agent returns a JSON body: %s", res.ForLLM)
	agentID, _ := created["id"].(string)
	require.NotEmpty(t, agentID, "create_agent's result carries the new agent's id: %s", res.ForLLM)

	// Stored: the new agent's entity record carries tools.skills.policies.plan
	// = ask (the C-4 persisted shape S-97's gateway leg also asserts).
	raw, err := os.ReadFile(filepath.Join(deps.Home, "entities", "agents", agentID+".json"))
	require.NoError(t, err, "the created agent's entity record must exist")
	var entity map[string]any
	require.NoError(t, json.Unmarshal(raw, &entity))
	var planOverride any
	if toolsMap, ok := entity["tools"].(map[string]any); ok {
		if skills, ok := toolsMap["skills"].(map[string]any); ok {
			if policies, ok := skills["policies"].(map[string]any); ok {
				planOverride = policies["plan"]
			}
		}
	}
	require.Equal(t, "ask", planOverride,
		"S-103: the new agent's override plan: ask must be stored (C-4 shape)")

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
	_, err = agentstore.New(deps.Home).Get(agentID)
	require.NoError(t, err, "the created agent is a real stored agent")
}
