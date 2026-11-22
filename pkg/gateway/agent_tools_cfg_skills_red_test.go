//go:build goolm && stdjson

package gateway

// Order 42 — TestUpdateAgentToolsCfg_PreservesSkillPolicies (S-97; C-21, C-4).
// Oracle: skills-tools-redesign-spec.md S-97 — "Given Mia with skill
// override plan: deny; When PUT /agents/mia sends tools_cfg with only mcp;
// Then Mia's plan: deny override is still stored." The C-21 write path for
// per-agent skill permissions on this route is PUT /agents/{id} `tools_cfg`,
// and the C-21 set-row shape ("set | skills.policies is the complete set of
// overrides") makes tools_cfg.skills.policies the full set of the agent's
// skill overrides.
//
// Harness: the package's own PUT /agents/{id} mechanics —
// rest_agent_executor_test.go::buildExecutorTestAPI and
// rest_agent_revision_fixture_test.go::revisionedAgentMutationRequest — with
// the raw entity read TestUpdateAgent_ToolsCfg_PersistsUnderToolsKey uses
// (entities/agents/<id>.json), so the assertion sees exactly what is
// persisted, not a type that may itself be missing the member.
//
// RED today: neither the wire type (gen.AgentToolsCfg) nor the config type
// (config.AgentToolsCfg) carries a skills member, so leg 1's
// tools_cfg.skills.policies is silently dropped by the lenient decode and
// the override never lands; leg 2 then has nothing to preserve.
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateAgentToolsCfg_PreservesSkillPolicies(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// skillOverrides reads the persisted entity record and returns the
	// agent's stored skill overrides (tools.skills.policies), nil when the
	// member is absent. Raw JSON, per TestUpdateAgent_ToolsCfg_PersistsUnderToolsKey.
	skillOverrides := func(t *testing.T) map[string]any {
		t.Helper()
		entityPath := filepath.Join(api.homePath, "entities", "agents", "test-agent.json")
		raw, err := os.ReadFile(entityPath)
		require.NoError(t, err)
		var agentMap map[string]any
		require.NoError(t, json.Unmarshal(raw, &agentMap))
		toolsMap, _ := agentMap["tools"].(map[string]any)
		if toolsMap == nil {
			return nil
		}
		skills, _ := toolsMap["skills"].(map[string]any)
		if skills == nil {
			return nil
		}
		policies, _ := skills["policies"].(map[string]any)
		return policies
	}
	want := map[string]any{"plan": "deny"}

	// Leg 1 — the C-21 set path: tools_cfg.skills.policies is the complete
	// set of skill overrides. This PUT stores the S-97 Given.
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent",
		strings.NewReader(`{"tools_cfg":{"skills":{"policies":{"plan":"deny"}}}}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	require.Equal(t, want, skillOverrides(t),
		"S-97 Given: the PUT with tools_cfg.skills.policies must store the plan override (C-21 set path)")

	// Leg 2 — S-97 proper: an unrelated update whose tools_cfg carries only
	// mcp must keep the stored skill override.
	w = httptest.NewRecorder()
	r = revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent",
		strings.NewReader(`{"tools_cfg":{"mcp":{"servers":[]}}}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	require.Equal(t, want, skillOverrides(t),
		"S-97: the unrelated tools_cfg (only mcp) must keep the plan: deny override")
}
