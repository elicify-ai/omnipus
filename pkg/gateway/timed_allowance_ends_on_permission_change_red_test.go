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
// Harness: the REAL writers — PATCH through patchPermissionSettings (the
// C-2 route; GREEN wires the seam to its dispatch, nil keeps the RED run
// loud), PUT /agents/{id}/tools through the order-54 pack's real
// putAgentSkills, and the real set_agent_permissions tool through
// sysSetAgentPermissions. The running allowances and their end are
// order 57's store surface — seedRunningAllowance / agentAllowanceActive /
// agentEgressTokenRevoked below are that seam. The audit entries are read
// through the real audit dir (attachTestAuditor + readAllAuditEntries).
//
// RED today: nothing seeds a running allowance (the C-26 store does not
// exist) and no writer ends one — every leg is BLOCKED at its first seam.
import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// The seams GREEN wires: patchPermissionSettings drives the real C-2 PATCH
// /security/permissions route with the given PermissionSettingsPatch body;
// the allowance trio drives the C-26 store (seed answers a card with
// "Allow for 3 hours" per S-128's Given). Declared after the test so the
// RED failure names the missing behaviour first.
var (
	patchPermissionSettings func(t testing.TB, api *restAPI, body string) *httptest.ResponseRecorder
	seedRunningAllowance    func(t testing.TB, api *restAPI, agentID, subject string)
	agentAllowanceActive    func(t testing.TB, api *restAPI, agentID, subject string) bool
	agentEgressTokenRevoked func(t testing.TB, api *restAPI, agentID string) bool
)

func TestTimedAllowance_EndsOnPermissionChange(t *testing.T) {
	allowanceEndedEntries := func(t *testing.T, auditDir string) []map[string]any {
		t.Helper()
		var out []map[string]any
		for _, e := range readAllAuditEntries(t, auditDir) {
			if e["event"] == "approval.allowance_ended" {
				out = append(out, e)
			}
		}
		return out
	}

	t.Run("D17#10: a global Deny ends Mia's allowance, once audited", func(t *testing.T) {
		if patchPermissionSettings == nil || seedRunningAllowance == nil || agentAllowanceActive == nil {
			t.Fatal("BLOCKED: the C-26 running-allowance store and its permission-change " +
				"ending (C-26 'A permission change' row) are not implemented — required by " +
				"D17 row 10")
		}
		api := buildExecutorTestAPI(t)
		auditDir := attachTestAuditor(t, api)
		seedRunningAllowance(t, api, "mia", "delete_task")

		w := patchPermissionSettings(t, api, `{"policies":{"delete_task":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code,
			"setup: the C-2 PATCH writer stores the Deny, got %d, body: %s", w.Code, w.Body.String())

		require.False(t, agentAllowanceActive(t, api, "mia", "delete_task"),
			"D17#10: the Deny ended the allowance — the next call is refused naming the global Deny, no card")
		ended := allowanceEndedEntries(t, auditDir)
		require.Len(t, ended, 1,
			"D17#10: exactly one approval.allowance_ended entry per ended allowance, got %d", len(ended))
		if len(ended) == 1 {
			require.Contains(t, ended[0]["reason"], "permission_changed",
				"D17#10: the entry names its reason")
			require.NotEmpty(t, ended[0]["actor"], "S-128: the entry names the actor of the change")
		}
	})

	t.Run("D17#11: after the Deny ends it, setting Ask back does not revive it", func(t *testing.T) {
		if patchPermissionSettings == nil || seedRunningAllowance == nil || agentAllowanceActive == nil {
			t.Fatal("BLOCKED: the C-26 store and ending are not implemented — required by D17 row 11")
		}
		api := buildExecutorTestAPI(t)
		seedRunningAllowance(t, api, "mia", "delete_task")

		w := patchPermissionSettings(t, api, `{"policies":{"delete_task":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: the Deny stores, body: %s", w.Body.String())
		w = patchPermissionSettings(t, api, `{"policies":{"delete_task":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: back to Ask stores, body: %s", w.Body.String())

		require.False(t, agentAllowanceActive(t, api, "mia", "delete_task"),
			"D17#11: the allowance ended at the Deny — lifting it back to Ask brings a card again")
	})

	t.Run("D17#14 and #15: a global change ends every agent it affects, then cards return", func(t *testing.T) {
		if patchPermissionSettings == nil || seedRunningAllowance == nil || agentAllowanceActive == nil {
			t.Fatal("BLOCKED: the C-26 store and ending are not implemented — required by D17 rows 14 and 15")
		}
		api := buildExecutorTestAPI(t)
		auditDir := attachTestAuditor(t, api)
		seedRunningAllowance(t, api, "mia", "delete_task")
		seedRunningAllowance(t, api, "jim", "delete_task")

		w := patchPermissionSettings(t, api, `{"policies":{"delete_task":"allow"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: the global Allow stores, body: %s", w.Body.String())

		require.False(t, agentAllowanceActive(t, api, "mia", "delete_task"),
			"D17#14: Mia's allowance ended (the calls run anyway under Allow)")
		require.False(t, agentAllowanceActive(t, api, "jim", "delete_task"),
			"D17#14: Jim's allowance ended too")
		require.Len(t, allowanceEndedEntries(t, auditDir), 2,
			"D17#14: one audit entry per ended allowance — two agents, two entries")

		w = patchPermissionSettings(t, api, `{"policies":{"delete_task":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: back to Ask, body: %s", w.Body.String())
		require.False(t, agentAllowanceActive(t, api, "mia", "delete_task"),
			"D17#15: with the global value back at Ask, Mia gets a card")
		require.False(t, agentAllowanceActive(t, api, "jim", "delete_task"),
			"D17#15: and so does Jim")
	})

	t.Run("D17#16: the agent's own PUT ends only that agent's allowances", func(t *testing.T) {
		if seedRunningAllowance == nil || agentAllowanceActive == nil {
			t.Fatal("BLOCKED: the C-26 store and ending are not implemented — required by D17 row 16")
		}
		api := buildExecutorTestAPI(t)
		auditDir := attachTestAuditor(t, api)
		seedRunningAllowance(t, api, "mia", "delete_task")
		seedRunningAllowance(t, api, "jim", "delete_task")

		// The real PUT /agents/{id}/tools writer: Mia's own value for
		// delete_task becomes an explicit ask (the inherited echo map makes
		// the request complete; the stored change is Mia's unset → ask).
		w := putAgentSkills(t, api, "mia", "")
		require.Equal(t, http.StatusOK, w.Code,
			"setup: the agent's own PUT stores, got %d, body: %s", w.Code, w.Body.String())

		require.False(t, agentAllowanceActive(t, api, "mia", "delete_task"),
			"D17#16: Mia gets a card — her own value changed")
		require.True(t, agentAllowanceActive(t, api, "jim", "delete_task"),
			"D17#16: Jim runs with no card — his allowance is kept")
		require.Len(t, allowanceEndedEntries(t, auditDir), 1,
			"D17#16: exactly one audit entry — Jim's allowance did not end")
	})

	t.Run("D17#17: the sysagent writer ends a skill allowance, and clearing keeps it ended", func(t *testing.T) {
		if seedRunningAllowance == nil || agentAllowanceActive == nil {
			t.Fatal("BLOCKED: the C-26 store and ending are not implemented — required by D17 row 17")
		}
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		grantPlanSkill(t, api, "mia")
		seedRunningAllowance(t, api, "mia", "plan")

		deps := sysDepsFor(t, api)
		res := sysSetAgentPermissions(t, deps, "mia", map[string]any{"plan": "deny"})
		require.False(t, res.IsError, "setup: the Deny stores: %s", res.ForLLM)
		require.False(t, agentAllowanceActive(t, api, "mia", "plan"),
			"D17#17: the Deny ended the skill allowance — the next load is refused")

		res = sysSetAgentPermissions(t, deps, "mia", map[string]any{"clear": true})
		require.False(t, res.IsError, "setup: the clear stores: %s", res.ForLLM)
		require.False(t, agentAllowanceActive(t, api, "mia", "plan"),
			"D17#17: clearing to Inherit does not revive the ended allowance — a card appears")
	})

	t.Run("D17#18 control: sending the stored value ends nothing", func(t *testing.T) {
		if patchPermissionSettings == nil || seedRunningAllowance == nil || agentAllowanceActive == nil {
			t.Fatal("BLOCKED: the C-26 store and ending are not implemented — required by D17 row 18")
		}
		api := buildExecutorTestAPI(t)
		auditDir := attachTestAuditor(t, api)
		seedRunningAllowance(t, api, "mia", "delete_task")

		w := patchPermissionSettings(t, api, `{"policies":{"delete_task":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code,
			"D17#18: the same-value request is valid — 200, got %d, body: %s", w.Code, w.Body.String())
		require.True(t, agentAllowanceActive(t, api, "mia", "delete_task"),
			"D17#18: setting the same value again is not a change and ends nothing")
		require.Empty(t, allowanceEndedEntries(t, auditDir),
			"D17#18: no audit entry for a same-value write")
	})

	t.Run("D17#19: ending bash allowances revokes the agent's live egress tokens", func(t *testing.T) {
		if patchPermissionSettings == nil || seedRunningAllowance == nil || agentAllowanceActive == nil || agentEgressTokenRevoked == nil {
			t.Fatal("BLOCKED: the C-26 store, its ending and the egress-token revocation " +
				"(C-26 'A permission change': ending a bash allowance also revokes the " +
				"agent's live egress tokens, FR-094) are not implemented — required by D17 row 19")
		}
		api := buildExecutorTestAPI(t)
		seedRunningAllowance(t, api, "mia", "bash addresses example.com")
		seedRunningAllowance(t, api, "mia", "bash family npm run test")

		w := patchPermissionSettings(t, api, `{"policies":{"bash":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: the bash Deny stores, body: %s", w.Body.String())

		require.False(t, agentAllowanceActive(t, api, "mia", "bash addresses example.com"),
			"D17#19: the address allowance ended")
		require.False(t, agentAllowanceActive(t, api, "mia", "bash family npm run test"),
			"D17#19: the command-family allowance ended")
		require.True(t, agentEgressTokenRevoked(t, api, "mia"),
			"D17#19: Mia's live egress token is revoked")

		w = patchPermissionSettings(t, api, `{"policies":{"bash":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: back to Ask, body: %s", w.Body.String())
		require.False(t, agentAllowanceActive(t, api, "mia", "bash addresses example.com"),
			"D17#19: the next curl https://example.com asks")
	})

	t.Run("D17#20 control: a trust change ends nothing", func(t *testing.T) {
		if patchPermissionSettings == nil || seedRunningAllowance == nil || agentAllowanceActive == nil {
			t.Fatal("BLOCKED: the C-26 store and ending are not implemented — required by D17 row 20")
		}
		api := buildExecutorTestAPI(t)
		installPlanSkillFixture(t, api.homePath)
		grantPlanSkill(t, api, "mia")
		seedRunningAllowance(t, api, "mia", "plan")

		w := patchPermissionSettings(t, api, `{"skill_trust":"allow"}`)
		require.Equal(t, http.StatusOK, w.Code,
			"setup: the trust change stores, got %d, body: %s", w.Code, w.Body.String())
		require.True(t, agentAllowanceActive(t, api, "mia", "plan"),
			"D17#20: trust (sandbox.skill_trust) is not a permission — the allowance is kept")
	})
}
