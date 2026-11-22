// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 14.
// TestAgentToolsPut_SkillsPolicies: the C-4 envelope's skills member on
// PUT /agents/{id}/tools — per-agent skill overrides stored as given,
// clearable, and the Omnipus per-agent Ask rule applied to skills.
//
// Oracles: skills-tools-redesign-spec.md C-4 (AgentToolsUpdateRequest gains
// optional `skills: {policies}`; skills omitted = overrides unchanged;
// skills.policies: {} = clear every skill override; otherwise the complete
// set; AgentToolsResponse gains required `skills: AgentSkillEntry[]` with
// key/display_name/configured_policy/global_policy/effective_policy — the
// generated contract at pkg/api/generated lists one entry per skill the
// agent can use: its granted installed skills and the project skills of its
// mounts), the Omnipus rule of S-22 (422 with the Omnipus text at the start
// of the message; Deny accepted), BDD S-21, S-22, S-23, S-25. The locked
// system-agent 403 guard (S-23) already exists for tools; its row here
// passes today and pins that the guard is unchanged by skills.
//
// The handler does not read `skills` at this commit
// (pkg/gateway/rest_tools.go::updateAgentTools persists only builtin + mcp)
// and the response's skills array is never populated; per the qa-lead RED
// rule the test fails LOUD for that missing behaviour.

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

// installPlanSkillFixture writes the plan skill under the harness home so
// C-23's known-skill rule accepts the key and the grants bind.
func installPlanSkillFixture(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, "skills", "plan")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: plan\ndescription: Planning helper\nmetadata:\n  display_name: Plan\n---\nBody."), 0o644))
}

// grantPlanSkill grants the installed plan skill to the agent through the
// real update path, so C-4's listing rule (granted skills) selects it.
func grantPlanSkill(t *testing.T, api *restAPI, agentID string) {
	t.Helper()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+agentID, strings.NewReader(`{"skills":["plan"]}`))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.updateAgent(w, r, agentID)
	require.Equal(t, http.StatusOK, w.Code, "setup: grant plan to %s: %s", agentID, w.Body.String())
}

// putAgentSkills PUTs the agent's tools with a skills member through the
// revisioned mutation helper. The builtin scaffold mirrors the l1 write
// fixture (l1_wu2_identity_rest_test.go): the complete catalog is built from
// the LIVE ceiling, not from a GET echo — an echoed map can carry stale
// inherited entries that trip the ceiling-changed guard (409) before the
// skills handling is reached. The endpoint rejects a body carrying neither
// builtin nor config.builtin, so the skills shape must still carry the
// complete builtin catalog. An empty skillsJSON omits skills (the C-4
// skills-omitted shape).
func putAgentSkills(t *testing.T, api *restAPI, agentID, skillsJSON string) *httptest.ResponseRecorder {
	t.Helper()
	full := map[string]string{}
	for name := range buildKnownBuiltinToolNames() {
		// Request scaffolding, not an oracle: inherited echoes must match
		// this fixture's current ceiling (ADR-090).
		full[name] = api.agentLoop.GetConfig().Sandbox.ToolPolicies[name]
	}
	policies, err := json.Marshal(full)
	require.NoError(t, err)
	body := `{"override_names":[],"builtin":{"policies":` + string(policies) + `}`
	if skillsJSON != "" {
		body += `,"skills":` + skillsJSON
	}
	body += `}`
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+agentID+"/tools", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)
	return w
}

// readAgentToolsSkills GETs the agent's tools and returns the decoded
// AgentToolsResponse's skills entries as raw JSON.
func readAgentToolsSkills(t *testing.T, api *restAPI, agentID string) []any {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+agentID+"/tools", nil)
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleAgentToolsRegistry(w, r, agentID)
	require.Equal(t, http.StatusOK, w.Code, "GET tools failed: %s", w.Body.String())
	var resp struct {
		Skills []any `json:"skills"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "body: %s", w.Body.String())
	require.NotNil(t, resp.Skills, "C-4: AgentToolsResponse.skills is required (never null)")
	return resp.Skills
}

// TestAgentToolsPut_SkillsPolicies (order 14).
// Traces to: BDD S-21, S-22, S-23, S-25; spec C-4.
func TestAgentToolsPut_SkillsPolicies(t *testing.T) {
	t.Run("S-25 a skill override is stored as given and read back", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		grantPlanSkill(t, api, "test-agent")
		w := putAgentSkills(t, api, "test-agent", `{"policies":{"plan":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		skills := readAgentToolsSkills(t, api, "test-agent")
		require.Len(t, skills, 1, "C-4: one entry for the granted skill: %v", skills)
		entry, ok := skills[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "plan", entry["key"], "C-23 key")
		require.Equal(t, "deny", entry["configured_policy"], "the override is stored as given (C-4)")
		require.Equal(t, "Plan", entry["display_name"], "C-4 display_name")
		require.Equal(t, true, entry["granted"], "a granted installed skill reports granted:true")
	})

	t.Run("S-21 an empty skills.policies clears every skill override", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		grantPlanSkill(t, api, "test-agent")
		w := putAgentSkills(t, api, "test-agent", `{"policies":{"plan":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: %s", w.Body.String())
		w = putAgentSkills(t, api, "test-agent", `{"policies":{}}`)
		require.Equal(t, http.StatusOK, w.Code, "clear: %s", w.Body.String())
		skills := readAgentToolsSkills(t, api, "test-agent")
		for _, s := range skills {
			entry := s.(map[string]any)
			require.Nil(t, entry["configured_policy"], "S-21: no override remains after the clear: %v", entry)
		}
	})

	t.Run("S-22 Omnipus refuses a per-agent Ask on a skill", func(t *testing.T) {
		api := l1WU2OmnipusWriteAPI(t)
		installPlanSkillFixture(t, api.homePath)
		w := putAgentSkills(t, api, "omnipus", `{"policies":{"plan":"ask"}}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code,
			"the Omnipus per-agent Ask rule applies to skills (S-22); got %d body: %s", w.Code, w.Body.String())
		require.True(t, strings.HasPrefix(w.Body.String(), `{"error":"Omnipus takes no per-agent Ask; set Ask globally or use Deny.`),
			"the Omnipus text starts the message (S-22); body: %s", w.Body.String())
		// Deny is accepted (S-22's positive control) and persists. The stored
		// shape is read from the entity file: the config.AgentToolsCfg Go
		// struct has no skills field at this commit, so the test reads the
		// persisted JSON rather than a field GREEN will add.
		w = putAgentSkills(t, api, "omnipus", `{"policies":{"plan":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "Deny is accepted for Omnipus: %s", w.Body.String())
		raw, err := os.ReadFile(filepath.Join(api.homePath, "entities", "agents", "omnipus.json"))
		require.NoError(t, err, "the omnipus entity record must exist")
		var stored struct {
			Agent struct {
				Tools struct {
					Skills struct {
						Policies map[string]string `json:"policies"`
					} `json:"skills"`
				} `json:"tools"`
			} `json:"agent"`
		}
		require.NoError(t, json.Unmarshal(raw, &stored), "entity JSON: %s", string(raw))
		require.Equal(t, "deny", stored.Agent.Tools.Skills.Policies["plan"],
			"the Deny override persists on the agent's tools (C-4); entity: %s", string(raw))
	})

	t.Run("S-23 a locked system agent stays read-only", func(t *testing.T) {
		api := newSeededJudgeAPI(t)
		installPlanSkillFixture(t, api.homePath)
		w := putAgentSkills(t, api, "judge", `{"policies":{"plan":"deny"}}`)
		require.Equal(t, http.StatusForbidden, w.Code,
			"S-23: locked system agent tools stay read-only; got %d body: %s", w.Code, w.Body.String())
	})

	t.Run("C-4 skills omitted leaves skill overrides unchanged", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		grantPlanSkill(t, api, "test-agent")
		w := putAgentSkills(t, api, "test-agent", `{"policies":{"plan":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: %s", w.Body.String())
		// A skills-less PUT (the C-4 envelope keeps revision + override_names
		// for tools): the stored skill override survives.
		w = putAgentSkills(t, api, "test-agent", "")
		require.Equal(t, http.StatusOK, w.Code, "skills-less PUT: %s", w.Body.String())
		skills := readAgentToolsSkills(t, api, "test-agent")
		require.Len(t, skills, 1, "the override survives a skills-less PUT (C-4)")
		entry := skills[0].(map[string]any)
		require.Equal(t, "deny", entry["configured_policy"], "the stored override is kept (C-4)")
	})
}
