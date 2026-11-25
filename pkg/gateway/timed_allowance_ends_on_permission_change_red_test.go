//go:build goolm && stdjson

package gateway

// Order 63 — TestTimedAllowance_EndsOnPermissionChange (S-128; Dataset D17
// rows 10, 11 and 14–20). Oracle: skills-tools-redesign-spec.md —
//   C-26 "A permission change": any write that changes a tool's or skill's
//        stored permission ends that tool's or skill's running allowances
//        for every agent the change affects — a global change → every
//        agent; a change of one agent's own value, including clearing it →
//        that agent; setting the same value again ends nothing. The ending
//        happens after the write is durable and before the response; each
//        ended allowance writes one `approval.allowance_ended` audit entry
//        naming the changed key and the actor. Ending a bash allowance also
//        revokes the agent's live egress tokens. Trust (`skill_trust`),
//        levels, Auto-approve and God Mode are not permissions and end
//        nothing.
//   Writers: PATCH /security/permissions (C-2, body PermissionSettingsPatch
//        — each map names only the keys to change), PUT /agents/{id}/tools
//        (rest_tools.go::updateAgentTools), set_agent_permissions
//        (pkg/sysagent/tools/agent_permissions.go), and a config reload
//        that changes a stored value.
//
// Harness: concrete writers, loop-owned store lookups and real next-call
// approval cards. The running allowance each row starts from is seeded through the store's own
// duration-bearing recording API (seedNativePermissionAllowance, fix round 4 part E; it used to be
// a loud BLOCKED developer handoff until that API landed); no boolean observer or reload callback
// can supply an answer, and a seed the store refuses fails the row before any ending assertion.
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"

	"github.com/stretchr/testify/require"
)

func patchPermissionSettings(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", permissionsTestBearer)
	routes := routeTable{}
	api.registerAdditionalEndpoints(routes)
	return patchPermissions(t, api, routes, body)
}

// putAgentToolValue PUTs the agent's tools with ONE explicit tool override
// through the real PUT /agents/{id}/tools writer (CHECK round 1 finding 16:
// the earlier leg sent the inherited echo only, which may not change the
// stored value at all). The catalog stays ceiling-complete; the override is
// the deliberate stored change.
func putAgentToolValue(t *testing.T, api *restAPI, agentID, tool, policy string) *httptest.ResponseRecorder {
	t.Helper()
	cfg := api.agentLoop.GetConfig()
	config.ReconcileToolPolicyCeiling(cfg, buildKnownBuiltinToolNames())
	full := map[string]string{}
	for name := range buildKnownBuiltinToolNames() {
		full[name] = cfg.Sandbox.ToolPolicies[name]
	}
	full[tool] = policy
	policies, err := json.Marshal(full)
	require.NoError(t, err)
	names, err := json.Marshal([]string{tool})
	require.NoError(t, err)
	body := `{"override_names":` + string(names) + `,"builtin":{"policies":` + string(policies) + `}}`
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+agentID+"/tools", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.updateAgentTools(w, r, agentID)
	return w
}

// permissionChangeAudit asserts the ONE entry a change must write, with the
// exact changed key, old and new values and the actor (CHECK round 1
// finding 16: reason-and-actor-only passed a wrong-keyed entry).
func permissionChangeAudit(t *testing.T, auditDir, key, oldV, newV string) {
	t.Helper()
	entries := allowanceEndedEntriesFor(t, auditDir)
	require.Len(t, entries, 1, "exactly one approval.allowance_ended entry for the change")
	if len(entries) == 1 {
		payload := allowanceEndedPayload(entries[0])
		require.Equal(t, key, payload["key"], "the entry names the changed key")
		require.Equal(t, oldV, payload["old"], "the entry names the old value")
		require.Equal(t, newV, payload["new"], "the entry names the new value")
		require.NotEmpty(t, payload["actor"], "the entry names the actor of the change")
	}
}

// allowanceEndedPayload returns the map that carries the content of one approval.allowance_ended
// entry. Spec C-26 (Audit paragraph) fixes the CONTENT — "agent, subject, the changed key, old and
// new value, the actor of the change and reason permission_changed" — and FR-096 the count, one per
// ended allowance; it does not fix the container. The audit log has two shapes for a decision-like
// event, the audit.Record envelope (payload under "fields", the shape skill.trust_approved and the
// approval-decision entry use) and a flat record with the payload at the top level (the shape of
// the security_setting_change record, contracts AuditEntry.actor). This reader accepts exactly those
// two and then holds the payload to the spec's content: a payload in neither place, or a field
// missing from the one that is present, fails the asserting test. Fix round 4, part D item 5: the
// two tests that read this event (this file and the D10#31 row of skill_consent_table_red_test.go)
// each assumed a different container — the envelope in one, a flat record in the other — so one of
// them was wrong for every writer; both now read through this one helper. Spec gap, reported to
// team-lead: the container is unspecified; pin one in C-26 and this helper tightens to it.
func allowanceEndedPayload(entry map[string]any) map[string]any {
	if fields, ok := entry["fields"].(map[string]any); ok {
		return fields
	}
	return entry
}

func allowanceEndedEntriesFor(t *testing.T, auditDir string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, e := range readAllAuditEntries(t, auditDir) {
		if e["event"] == "approval.allowance_ended" {
			out = append(out, e)
		}
	}
	return out
}

// Seed both actual actors, not just buildExecutorTestAPI's test-agent. All
// writers share these durable records and the loop registry built from them.
func newPermissionChangeAPI(t *testing.T) *restAPI {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.json"))
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = nil
	cfg.Sandbox.AutoApprove, cfg.Sandbox.GodMode = false, false
	cfg.Sandbox.ToolPolicies["delete_task"] = "ask"
	for _, id := range []string{"mia", "jim"} {
		record := config.AgentConfig{ID: id, Name: id, Type: config.AgentTypeCustom}
		require.NoError(t, agentstore.New(home).Create(id, &record))
		cfg.Agents.List = append(cfg.Agents.List, record)
	}
	require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	reg := newApprovalRegistryV2(64, 30*time.Second)
	handler := newWSHandler(msgBus, al, "")
	handler.approvalRegV2 = reg
	al.SetToolApprover(newPolicyApproverAdapter(reg, handler))
	t.Cleanup(handler.Wait)
	return &restAPI{agentLoop: al, homePath: home, approvalReg: reg}
}

// This instrument must execute even before the timed store exists: a PUT
// with override_names: [] only echoes the ceiling and does not create Ask.
func TestPermissionChangeToolWriter_Fixture(t *testing.T) {
	api := newPermissionChangeAPI(t)
	w := putAgentToolValue(t, api, "mia", "delete_task", "ask")
	require.Equal(t, http.StatusOK, w.Code, "actual explicit-override PUT: %s", w.Body.String())
	stored, err := agentstore.New(api.homePath).ReadState("mia")
	require.NoError(t, err)
	require.NotNil(t, stored.Agent.Tools)
	require.Equal(t, config.ToolPolicyAsk, stored.Agent.Tools.Builtin.Policies["delete_task"], "native durable record, not echoed response")
}

func TestTimedAllowance_EndsOnPermissionChange(t *testing.T) {
	t.Run("D17#10: a global Deny ends Mia's allowance, once audited", func(t *testing.T) {
		api := newPermissionChangeAPI(t)
		auditDir := attachTestAuditor(t, api)

		// Pin the floor the Deny changes: the stored global is Ask first, so
		// the audit entry's old/new are exact fixture values, not assumptions
		// about the test install's ceiling (CHECK round 1 finding 16).
		w := patchPermissionSettings(t, api, `{"policies":{"delete_task":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code,
			"setup: the floor stores as Ask, got %d, body: %s", w.Code, w.Body.String())
		seedRunningAllowance(t, api, "mia", "delete_task")
		assertPermissionAllowance(t, api, "mia", "delete_task", true, "positive running allowance before the change")

		w = patchPermissionSettings(t, api, `{"policies":{"delete_task":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code,
			"setup: the C-2 PATCH writer stores the Deny, got %d, body: %s", w.Code, w.Body.String())

		assertPermissionAllowance(t, api, "mia", "delete_task", false,
			"D17#10: the Deny ended the running allowance in the real grant store; the gate is checked once Ask returns")
		permissionChangeAudit(t, auditDir, "delete_task", "ask", "deny")
		w = patchPermissionSettings(t, api, `{"policies":{"delete_task":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		assertPermissionNextGate(t, api, "mia", "delete_task", false)
	})

	t.Run("D17#11: after the Deny ends it, setting Ask back does not revive it", func(t *testing.T) {
		api := newPermissionChangeAPI(t)
		seedRunningAllowance(t, api, "mia", "delete_task")

		w := patchPermissionSettings(t, api, `{"policies":{"delete_task":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: the Deny stores, body: %s", w.Body.String())
		w = patchPermissionSettings(t, api, `{"policies":{"delete_task":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: back to Ask stores, body: %s", w.Body.String())

		assertPermissionAllowance(t, api, "mia", "delete_task", false,
			"D17#11: the allowance ended at the Deny — lifting it back to Ask brings a card again")
		assertPermissionNextGate(t, api, "mia", "delete_task", false)
	})

	t.Run("D17#14 and #15: a global change ends every agent it affects, then cards return", func(t *testing.T) {
		api := newPermissionChangeAPI(t)
		auditDir := attachTestAuditor(t, api)
		w := patchPermissionSettings(t, api, `{"policies":{"delete_task":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup Ask before allowances: %s", w.Body.String())
		seedRunningAllowance(t, api, "mia", "delete_task")
		seedRunningAllowance(t, api, "jim", "delete_task")

		w = patchPermissionSettings(t, api, `{"policies":{"delete_task":"allow"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: the global Allow stores, body: %s", w.Body.String())

		assertPermissionAllowance(t, api, "mia", "delete_task", false,
			"D17#14: Mia's allowance ended (the calls run anyway under Allow)")
		assertPermissionAllowance(t, api, "jim", "delete_task", false,
			"D17#14: Jim's allowance ended too")
		require.Len(t, allowanceEndedEntriesFor(t, auditDir), 2,
			"D17#14: one audit entry per ended allowance — two agents, two entries")

		w = patchPermissionSettings(t, api, `{"policies":{"delete_task":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: back to Ask, body: %s", w.Body.String())
		assertPermissionAllowance(t, api, "mia", "delete_task", false,
			"D17#15: with the global value back at Ask, Mia gets a card")
		assertPermissionAllowance(t, api, "jim", "delete_task", false,
			"D17#15: and so does Jim")
		assertPermissionNextGate(t, api, "mia", "delete_task", false)
		assertPermissionNextGate(t, api, "jim", "delete_task", false)
	})

	t.Run("D17#16: the agent's own PUT ends only that agent's allowances", func(t *testing.T) {
		api := newPermissionChangeAPI(t)
		auditDir := attachTestAuditor(t, api)
		seedRunningAllowance(t, api, "mia", "delete_task")
		seedRunningAllowance(t, api, "jim", "delete_task")

		// The real PUT /agents/{id}/tools writer with ONE explicit override:
		// Mia's own value for delete_task becomes an explicit ask (CHECK
		// round 1 finding 16 — the earlier leg sent only the inherited
		// echo, which changes nothing on the C-4 envelope).
		w := putAgentToolValue(t, api, "mia", "delete_task", "ask")
		require.Equal(t, http.StatusOK, w.Code,
			"setup: the agent's own PUT stores, got %d, body: %s", w.Code, w.Body.String())

		assertPermissionAllowance(t, api, "mia", "delete_task", false,
			"D17#16: Mia gets a card — her own value changed")
		assertPermissionAllowance(t, api, "jim", "delete_task", true,
			"D17#16: Jim runs with no card — his allowance is kept")
		assertPermissionNextGate(t, api, "mia", "delete_task", false)
		assertPermissionNextGate(t, api, "jim", "delete_task", true)
		ended := allowanceEndedEntriesFor(t, auditDir)
		require.Len(t, ended, 1,
			"D17#16: exactly one audit entry — Jim's allowance did not end")
		if len(ended) == 1 {
			payload := allowanceEndedPayload(ended[0])
			require.Equal(t, "delete_task", payload["key"], "D17#16: the entry names the changed key")
			require.Contains(t, payload, "old", "D17#16: the entry names the old value (unset → present)")
			require.Equal(t, "ask", payload["new"], "D17#16: the entry names the new value")
			require.NotEmpty(t, payload["actor"], "D17#16: the entry names the actor")
		}
	})

	t.Run("D17#17: the sysagent writer ends a skill allowance, and clearing keeps it ended", func(t *testing.T) {
		api := newPermissionChangeAPI(t)
		installPlanSkillFixture(t, api.homePath)
		grantPlanSkill(t, api, "mia")
		seedRunningAllowance(t, api, "mia", "plan")

		deps := sysDepsFor(t, api)
		res := sysSetAgentPermissions(t, deps, "mia", map[string]any{"set": map[string]any{"plan": "deny"}})
		require.False(t, res.IsError, "setup: the Deny stores: %s", res.ForLLM)
		assertPermissionAllowance(t, api, "mia", "plan", false,
			"D17#17: the Deny ended the skill allowance — the next load is refused")

		res = sysSetAgentPermissions(t, deps, "mia", map[string]any{"clear": true})
		require.False(t, res.IsError, "setup: the clear stores: %s", res.ForLLM)
		assertPermissionAllowance(t, api, "mia", "plan", false,
			"D17#17: clearing to Inherit does not revive the ended allowance — a card appears")
		assertPermissionNextGate(t, api, "mia", "plan", false)
	})

	t.Run("D17#18 control: sending the stored value ends nothing", func(t *testing.T) {
		api := newPermissionChangeAPI(t)
		auditDir := attachTestAuditor(t, api)
		w := patchPermissionSettings(t, api, `{"policies":{"delete_task":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup stored Ask before allowance: %s", w.Body.String())
		seedRunningAllowance(t, api, "mia", "delete_task")

		w = patchPermissionSettings(t, api, `{"policies":{"delete_task":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code,
			"D17#18: the same-value request is valid — 200, got %d, body: %s", w.Code, w.Body.String())
		assertPermissionAllowance(t, api, "mia", "delete_task", true,
			"D17#18: setting the same value again is not a change and ends nothing")
		require.Empty(t, allowanceEndedEntriesFor(t, auditDir),
			"D17#18: no audit entry for a same-value write")
		assertPermissionNextGate(t, api, "mia", "delete_task", true)
	})

	t.Run("D17#19: ending bash allowances revokes the agent's live egress tokens", func(t *testing.T) {
		api := newPermissionChangeAPI(t)
		seedRunningAllowance(t, api, "mia", "bash addresses example.com")
		seedRunningAllowance(t, api, "mia", "bash family npm run test")
		proxy, request, host := newLifecycleProxy(t)
		api.agentLoop.WireTier13Deps(agent.Tier13Deps{EgressProxy: proxy})
		token := api.agentLoop.ApprovalGrants().SessionEgressToken(permissionChangeSession, "mia")
		require.NotEmpty(t, token, "actual issued token, never a test-owned revoked verdict")
		proxy.GrantRunHosts(token, []string{host})
		request(token, true)

		w := patchPermissionSettings(t, api, `{"policies":{"bash":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: the bash Deny stores, body: %s", w.Body.String())

		assertPermissionAllowance(t, api, "mia", "bash addresses example.com", false,
			"D17#19: the address allowance ended")
		assertPermissionAllowance(t, api, "mia", "bash family npm run test", false,
			"D17#19: the command-family allowance ended")
		request(token, false) // the same exact token cannot open a fresh proxy connection

		w = patchPermissionSettings(t, api, `{"policies":{"bash":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: back to Ask, body: %s", w.Body.String())
		assertPermissionAllowance(t, api, "mia", "bash addresses example.com", false,
			"D17#19: the next curl https://example.com asks")
	})

	t.Run("D17#20 control: a trust change ends nothing", func(t *testing.T) {
		api := newPermissionChangeAPI(t)
		installPlanSkillFixture(t, api.homePath)
		grantPlanSkill(t, api, "mia")
		seedRunningAllowance(t, api, "mia", "plan")

		w := patchPermissionSettings(t, api, `{"skill_trust":"allow"}`)
		require.Equal(t, http.StatusOK, w.Code,
			"setup: the trust change stores, got %d, body: %s", w.Code, w.Body.String())
		assertPermissionAllowance(t, api, "mia", "plan", true,
			"D17#20: trust (sandbox.skill_trust) is not a permission — the allowance is kept")
		assertPermissionNextGate(t, api, "mia", "plan", true)
	})

	// CHECK round 1 finding 16: the fourth writer and the non-permission
	// controls. A config reload that changes a stored PERMISSION value ends
	// the allowances it affects; a level change, the Auto-approve switch and
	// God Mode are not permissions and end nothing.
	t.Run("D17: a config reload that changes the stored value ends the allowance (C-26 writer 4)", func(t *testing.T) {
		api := newPermissionChangeAPI(t)
		auditDir := useLoopAuditor(t, api) // a reload writes through the loop's own logger
		w := patchPermissionSettings(t, api, `{"policies":{"delete_task":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: the floor stores as Ask, body: %s", w.Body.String())
		seedRunningAllowance(t, api, "mia", "delete_task")
		assertPermissionAllowance(t, api, "mia", "delete_task", true, "positive running allowance before native reload")

		applyConfigReload(t, api, func(cfg *config.Config) {
			cfg.Sandbox.ToolPolicies["delete_task"] = "deny"
		})

		assertPermissionAllowance(t, api, "mia", "delete_task", false,
			"C-26: the reload that changed the stored permission ends the allowance like any writer")
		permissionChangeAudit(t, auditDir, "delete_task", "ask", "deny")
		w = patchPermissionSettings(t, api, `{"policies":{"delete_task":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		assertPermissionNextGate(t, api, "mia", "delete_task", false)
	})

	t.Run("D17 control: a level change is not a permission and ends nothing", func(t *testing.T) {
		api := newPermissionChangeAPI(t)
		auditDir := attachTestAuditor(t, api)
		seedRunningAllowance(t, api, "mia", "serve_web")

		w := patchPermissionSettings(t, api, `{"visibility":{"serve_web":"hidden"}}`)
		require.Equal(t, http.StatusOK, w.Code,
			"setup: the level change stores, got %d, body: %s", w.Code, w.Body.String())
		assertPermissionAllowance(t, api, "mia", "serve_web", true,
			"C-26: a level (visibility) is not a permission — the allowance is kept")
		require.Empty(t, allowanceEndedEntriesFor(t, auditDir),
			"C-26: no audit entry for a non-permission change")
		assertPermissionNextGate(t, api, "mia", "serve_web", true)
	})

	// This control reads the same loop-owned audit directory as the reload row above, whose
	// positive result (exactly one approval.allowance_ended entry) shows the reader can see the
	// event; and it now runs through the reload seam that ends allowances, so an ending on an
	// Auto/God change would be seen (through the earlier Settings-refresh path it could not be).
	t.Run("D17 control: the Auto-approve switch and God Mode end nothing", func(t *testing.T) {
		api := newPermissionChangeAPI(t)
		auditDir := useLoopAuditor(t, api)
		seedRunningAllowance(t, api, "mia", "delete_task")

		applyConfigReload(t, api, func(cfg *config.Config) {
			cfg.Sandbox.AutoApprove = true
			cfg.Sandbox.GodMode = true
		})

		assertPermissionAllowance(t, api, "mia", "delete_task", true,
			"C-26: Auto-approve and God Mode are not permissions — the allowance is kept")
		require.Empty(t, allowanceEndedEntriesFor(t, auditDir),
			"C-26: no audit entry for the Auto/God switches")
	})
}
