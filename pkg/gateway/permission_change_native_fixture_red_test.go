//go:build goolm && stdjson

package gateway

// C-26/D17 observers are concrete calls to the loop-owned store and its actual
// next-call approval gate. No assignable boolean decides allowance coverage or
// token revocation. The running allowance each ending row starts from is seeded
// through the store's own duration-bearing recording API (fix round 4, part E):
// the same methods a card answer reaches, so the seed fabricates no coverage or
// expiry, and every observer below reads the loop's own store.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

const permissionChangeSession = "permission-change-origin"

// permissionChangeSkill is the one skill subject of the observers. A skill's allowance lives
// under the skill's own key in the agent's store (C-24, C-26: the skill key, not a tool call), so
// it is recorded by RecordSkillAllowance and read by IsSkillAllowed.
const permissionChangeSkill = "plan"

// permissionChangeAllowanceLength is the length of every seeded allowance: S-128 starts from
// "running allowances ... answered with 'Allow for 3 hours'".
const permissionChangeAllowanceLength = 3 * time.Hour

// seedNativePermissionAllowance records one running allowance of agentID for subject, lasting d,
// through the store's real duration-bearing recording API (C-26: an allowance belongs to the agent,
// so no session is passed). The subject names are the observers' own: an exact tool, the skill, one
// named address, or one command family. A refusal by the store is returned, never ignored, so a
// seed that recorded nothing cannot make a later "ended" assertion pass.
func seedNativePermissionAllowance(al *agent.AgentLoop, agentID, subject string, d time.Duration) error {
	store := al.ApprovalGrants()
	if store == nil {
		return fmt.Errorf("the loop has no approval grant store")
	}
	var recorded bool
	switch subject {
	case permissionChangeSkill:
		_, recorded = store.RecordSkillAllowance(agentID, subject, d)
	case "bash addresses example.com":
		_, recorded = store.RecordNetworkGrantFor(agentID, []string{"example.com"}, d)
	case "bash family npm run test":
		_, recorded = store.RecordPrefixGrantFor(agentID, "bash", security.ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test"}, d)
	default:
		tool, args := permissionChangeCall(subject)
		_, recorded = store.RecordFor(agentID, tool, args, d)
	}
	if !recorded {
		return fmt.Errorf("the store refused to record a %s allowance of %q for agent %q", d, subject, agentID)
	}
	return nil
}

func seedRunningAllowance(t *testing.T, api *restAPI, agentID, subject string) {
	t.Helper()
	require.NoError(t, seedNativePermissionAllowance(api.agentLoop, agentID, subject, permissionChangeAllowanceLength))
	assertPermissionAllowance(t, api, agentID, subject, true)
}

func permissionChangeCall(subject string) (string, map[string]any) {
	switch subject {
	case "bash addresses example.com":
		return "bash", map[string]any{"command": "curl https://example.com"}
	case "bash family npm run test":
		return "bash", map[string]any{"command": "npm run test"}
	default:
		return subject, map[string]any{"task_id": "permission-change-task"}
	}
}

// Both the source session and a different session consult the REAL store. The
// C-26 agent scope must cover both; no stored record is inferred from a seeder's
// return value. Expected active/inactive is supplied only to assertions.
func assertPermissionAllowance(t *testing.T, api *restAPI, agentID, subject string, active bool, because ...string) {
	t.Helper()
	for _, reason := range because {
		t.Log(reason)
	}
	store := api.agentLoop.ApprovalGrants()
	require.NotNil(t, store, "loop-owned actual grant store is required")
	for _, sid := range []string{permissionChangeSession, "permission-change-next-chat"} {
		switch subject {
		case permissionChangeSkill:
			// The skill's own reader: the store method the Skill load decision consults
			// (pkg/agent/skill_load_policy.go), agent-scoped, so both chats see the same answer.
			require.Equal(t, active, store.IsSkillAllowed(agentID, subject), "agent=%s session=%s skill allowance", agentID, sid)
		case "bash addresses example.com":
			require.Equal(t, active, store.HasNetworkGrant(sid, agentID), "agent=%s session=%s network grant", agentID, sid)
			if active {
				require.Equal(t, []string{"example.com"}, store.NetworkHostsFor(sid, agentID))
			} else {
				require.Empty(t, store.NetworkHostsFor(sid, agentID), "the actual named-host record ended")
			}
		case "bash family npm run test":
			require.Equal(t, active, store.IsPrefixAllowed(sid, agentID, "bash", "/usr/bin/npm", []string{"run", "test", "--", "--watch"}, false), "agent=%s session=%s command-family grant", agentID, sid)
		default:
			tool, args := permissionChangeCall(subject)
			require.Equal(t, active, store.IsAllowed(sid, agentID, tool, args), "agent=%s session=%s subject=%s actual allowance", agentID, sid, subject)
		}
	}
}

// This is the actual consultation point used by subsequent Ask calls, not an
// invented decision adapter. It must either return from the actual standing
// grant or create a native pending card. It does not certify the entire model
// turn (nor replace the separate exact-cut engine UAT).
func assertPermissionNextGate(t *testing.T, api *restAPI, agentID, subject string, allowed bool) {
	t.Helper()
	// A brand-new chat of the agent: an agent-scoped allowance (C-26) must cover it.
	meta, err := api.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", agentID)
	require.NoError(t, err)
	assertPermissionNextGateIn(t, api, meta.ID, agentID, subject, allowed)
}

func assertPermissionNextGateIn(t *testing.T, api *restAPI, sessionID, agentID, subject string, allowed bool) {
	t.Helper()
	if subject == permissionChangeSkill {
		// A skill's allowance never passes through a tool's grant check: it is consulted by the
		// Skill load decision (pkg/agent/skill_load_policy.go: "A running timed allowance settles
		// the permission check", C-12/C-24), which only a real model turn reaches; the card or
		// no-card of a real load is observed by the D10 rows of skill_consent_table_red_test.go.
		// The first drafts of these observers sent the skill as a call of the Skill tool through
		// CheckGrantOrRequestApproval; the Skill tool accepts only Allow (C-6) and never holds a
		// grant of its own, so that call raised a card for ANY state of the skill's allowance and
		// could not tell a kept allowance from an ended one (it could never meet D17 row 20's "no
		// card" either): a fixture mis-model, corrected here with the allowance's own reader. Strength
		// is unchanged or higher: the reader answers true only while a running allowance exists.
		require.Equal(t, allowed, api.agentLoop.ApprovalGrants().IsSkillAllowed(agentID, subject),
			"agent=%s session=%s: the next Skill load's permission check %s", agentID, sessionID,
			map[bool]string{true: "is settled by the running allowance", false: "finds no running allowance"}[allowed])
		return
	}
	require.NotNil(t, api.approvalReg)
	tool, args := permissionChangeCall(subject)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type decision struct {
		approved, recorded bool
		reason             string
	}
	result := make(chan decision, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		yes, reason, recorded := api.agentLoop.CheckGrantOrRequestApproval(ctx, sessionID, agentID, tool, "permission-next-"+sessionID, "permission-turn-"+sessionID, args)
		result <- decision{approved: yes, recorded: recorded, reason: reason}
	}()
	defer func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("native next-call gate did not join")
		}
	}()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if entries := api.approvalReg.pendingApprovals(); len(entries) > 0 {
			require.False(t, allowed, "a retained allowance must settle the actual next call without a card")
			require.Len(t, entries, 1)
			entry := entries[0]
			require.Equal(t, agentID, entry.AgentID)
			require.Equal(t, tool, entry.ToolName)
			require.Equal(t, args, entry.Args)
			w := postToolApprovalRaw(t, api, entry.ApprovalID, `{"action":"deny"}`)
			require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
			select {
			case got := <-result:
				require.False(t, got.approved)
				require.False(t, got.recorded)
				require.Equal(t, "user", got.reason)
			case <-deadline.C:
				t.Fatal("actual next-call card answer failed to release its native waiter")
			}
			return
		}
		select {
		case got := <-result:
			require.True(t, allowed, "an ended allowance cannot settle the actual next call without a card")
			require.Equal(t, decision{approved: true}, got)
			require.Empty(t, api.approvalReg.pendingApprovals())
			return
		case <-deadline.C:
			t.Fatal("UNVERIFIED: native next-call gate reached neither card nor completion")
		case <-tick.C:
		}
	}
}

// Config-file edits are inputs to the real fourth writer (C-26 writer 4; spec line "A permission
// change": "a config reload that changes a stored value"). Save the edit to the actual home,
// then reload the way the gateway's reload does; never substitute an in-memory map edit for a
// reload.
//
// Fix round 4, part E (fixture defect found in the fix4e3 run): the first version of this helper
// drove restAPI.refreshConfigAndRewireServices, the Settings-save refresh. That path publishes
// the saved config through SwapWrittenConfig and never reaches the code that ends allowances on
// a reload (pkg/agent/allowance_end.go::AgentLoop.endAllowancesOnReload, called only from
// AgentLoop.ReloadProviderAndConfig), so the writer under test could not be observed and the
// "Auto-approve and God Mode end nothing" control passed without testing anything.
//
// The seam used now is AgentLoop.ReloadProviderAndConfig, the call the gateway's reload makes
// (pkg/gateway/gateway_reload.go::handleConfigReload) with the config its loader built
// (pkg/gateway/gateway.go::newReloadConfigLoader): load config.json, refill the roster from the
// agent store (config.LoadConfig empties agents.list on every load, and a reload that kept the
// empty roster would affect no agent), then swap. The service restart around it does not take
// part in ending allowances and is not reproduced. Whether the Settings-save refresh should also
// end allowances is not stated by the spec; reported to team-lead as a spec question.
func applyConfigReload(t *testing.T, api *restAPI, mutate func(*config.Config)) {
	t.Helper()
	path := filepath.Join(api.homePath, "config.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var edited config.Config
	require.NoError(t, json.Unmarshal(raw, &edited))
	mutate(&edited)
	require.NoError(t, config.SaveConfig(path, &edited))
	candidate, err := config.LoadConfig(path)
	require.NoError(t, err, "the reload loads the edited config.json")
	require.NoError(t, populateAgentsListFromEntityStoreStrict(candidate, api.homePath),
		"the reload fills the roster from the agent store, as the production reload loader does")
	require.NotEmpty(t, candidate.Agents.List, "the reloaded config must carry the stored agents, as in production")
	require.NoError(t, api.agentLoop.ReloadProviderAndConfig(context.Background(), &restMockProvider{}, candidate),
		"native loop reload, not a test callback returning success")
}

// useLoopAuditor wires the REST writers to the agent loop's own audit logger, as the gateway
// does (pkg/gateway/gateway_boot.go: auditor = agentLoop.AuditLogger()), and returns that
// logger's directory. A reload writes its approval.allowance_ended entries through the loop's
// logger (AgentLoop.endAllowancesOnReload passes al.auditLogger), so the reload rows read this
// directory, not the separate logger attachTestAuditor gives the REST writers.
func useLoopAuditor(t *testing.T, api *restAPI) string {
	t.Helper()
	logger := api.agentLoop.AuditLogger()
	require.NotNil(t, logger, "the loop must own an audit logger: a reload writes its entries through it")
	api.auditor = logger
	return filepath.Join(api.homePath, "system")
}

// The two chats whose records the observers read, and every subject they cover:
// exact tools, one named address, one command family.
var (
	permissionChangeSessions = []string{permissionChangeSession, "permission-change-next-chat"}
	permissionChangeSubjects = []string{"delete_task", "plan", "serve_web", "bash addresses example.com", "bash family npm run test"}
)

// permissionChangeUnanswered is an exact tool no seed ever records. An allowance
// for the other subjects must not cover it: a never-answered subject of the SAME
// agent is real state that is inactive under every lifetime rule.
const permissionChangeUnanswered = "create_task"

// seedLegacySessionAllowances records every subject in both chats for agentID
// through the store's session-carrying legacy API (the one that predates timed answers; it
// records an allowance with no end). Those records stand in for live store state so the
// observers can be shown against both outcomes. A skill has no legacy recording call, only the
// timed one, so it is recorded for an hour, far longer than any of these instrument tests runs.
func seedLegacySessionAllowances(t *testing.T, store *security.ApprovalGrantStore, agentID string) {
	t.Helper()
	for _, sid := range permissionChangeSessions {
		for _, subject := range permissionChangeSubjects {
			switch subject {
			case permissionChangeSkill:
				_, recorded := store.RecordSkillAllowance(agentID, subject, time.Hour)
				require.True(t, recorded)
			case "bash addresses example.com":
				require.True(t, store.RecordNetworkGrantWithHosts(sid, agentID, []string{"example.com"}))
			case "bash family npm run test":
				require.True(t, store.RecordPrefixGrant(sid, agentID, "bash", security.ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test"}))
			default:
				tool, args := permissionChangeCall(subject)
				require.True(t, store.Record(sid, agentID, tool, args))
			}
		}
	}
}

// Instrument, not timed RED: legacy session records are only a stand-in for live
// store state, so that EVERY observer branch (exact, named-host, command-family)
// and the next-call gate (no card, card) are shown against both outcomes. The
// inactive half is real state that was never seeded — another agent, and a subject
// the same agent never answered — and never a chat close: C-26 says closing a chat
// no longer ends an agent's allowance (TestPermissionChangeObservers_ChatCloseKeepsTheAgentAllowance
// requires that). A helper that always expected active, always expected ended,
// ignored the agent key or the subject, or skipped the gate would fail here.
func TestPermissionChangeObservers_Fixture(t *testing.T) {
	api := newPermissionChangeAPI(t)
	store := api.agentLoop.ApprovalGrants()
	seedLegacySessionAllowances(t, store, "mia")
	for _, subject := range permissionChangeSubjects {
		assertPermissionAllowance(t, api, "mia", subject, true, "instrument positive: "+subject)
		assertPermissionAllowance(t, api, "jim", subject, false, "instrument other-agent negative: "+subject)
	}
	assertPermissionAllowance(t, api, "mia", permissionChangeUnanswered, false, "instrument unanswered-subject negative: "+permissionChangeUnanswered)

	// The next-call gate, both outcomes, on one real chat of the agent.
	meta, err := api.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	tool, args := permissionChangeCall("delete_task")
	require.True(t, store.Record(meta.ID, "mia", tool, args))
	assertPermissionNextGateIn(t, api, meta.ID, "mia", "delete_task", true)               // a standing allowance settles it: no card
	assertPermissionNextGateIn(t, api, meta.ID, "mia", permissionChangeUnanswered, false) // the same chat, a subject never answered: the card
	assertPermissionNextGate(t, api, "jim", "plan", false)                                // a fresh chat of an agent holding nothing: a card

	// A real token witness uses a fresh connection and complete upstream bytes,
	// so transport errors and a merely changed boolean cannot imitate revocation.
	proxy, request, host := newLifecycleProxy(t)
	token := store.SessionEgressToken(permissionChangeSession, "mia")
	require.NotEmpty(t, token)
	proxy.GrantRunHosts(token, []string{host})
	request(token, true)
	proxy.RevokeRunToken(token)
	request(token, false)
}

// C-26, "Lifetime and scope" (End): an allowance ends at its time, at a restart,
// when the agent is deleted or when the permission it answers changes — closing a
// chat is not on that list — and the ClearSession impact row: "no longer clears
// allowances (they belong to the agent)". This closes a real chat through the
// product's own path (AgentLoop.CloseSession, which calls the store's ClearSession),
// then reads the allowance through the same real-store observers and next-call gate
// the ending rows use. Only the closed chat's own egress tokens stay session-specific
// (pkg/security's token test requires that). RED today: the close wipes the chat's
// records, so the observer finds the allowance gone.
func TestPermissionChangeObservers_ChatCloseKeepsTheAgentAllowance(t *testing.T) {
	api := newPermissionChangeAPI(t)
	store := api.agentLoop.ApprovalGrants()
	seedLegacySessionAllowances(t, store, "mia")
	for _, subject := range permissionChangeSubjects {
		assertPermissionAllowance(t, api, "mia", subject, true, "precondition, before the chat closes: "+subject)
	}

	api.agentLoop.CloseSession(permissionChangeSession, "test-chat-close")

	for _, subject := range permissionChangeSubjects {
		assertPermissionAllowance(t, api, "mia", subject, true, "C-26: closing a chat does not end the agent's allowance: "+subject)
		assertPermissionAllowance(t, api, "jim", subject, false, "closing Mia's chat gives another agent nothing: "+subject)
	}
	assertPermissionAllowance(t, api, "mia", permissionChangeUnanswered, false, "closing a chat widens nothing: "+permissionChangeUnanswered)
	assertPermissionNextGate(t, api, "mia", "delete_task", true)               // a brand-new chat of the agent after the close: no card
	assertPermissionNextGate(t, api, "mia", permissionChangeUnanswered, false) // a subject never answered still asks
}

// C-26, the same ClearSession impact row, second half: the close "still revokes the
// session's egress tokens (NetworkTokensForSession -> RevokeRunToken)". A token
// authorises one chat's running shell, not the agent, so it is the one thing that
// stays per chat. This is the control that keeps the survival test above from being
// satisfied by dropping the whole close-time cleanup: it runs on a real egress
// proxy wired into the loop, through the loop's own CloseSession, with a fresh
// connection and complete upstream bytes as the witness, and it shows another chat's
// token is untouched. It holds on today's product and must keep holding.
func TestPermissionChangeObservers_ChatCloseRevokesOnlyThatChatsEgressToken(t *testing.T) {
	api := newPermissionChangeAPI(t)
	store := api.agentLoop.ApprovalGrants()
	proxy, request, host := newLifecycleProxy(t)
	api.agentLoop.WireTier13Deps(agent.Tier13Deps{EgressProxy: proxy})

	closing := store.SessionEgressToken(permissionChangeSession, "mia")
	otherChat := store.SessionEgressToken("permission-change-next-chat", "mia")
	require.NotEmpty(t, closing, "actual issued token, never a test-owned revoked verdict")
	require.NotEmpty(t, otherChat, "actual issued token for another chat")
	require.NotEqual(t, closing, otherChat, "one token per chat")
	for _, token := range []string{closing, otherChat} {
		proxy.GrantRunHosts(token, []string{host})
		request(token, true)
	}

	api.agentLoop.CloseSession(permissionChangeSession, "test-chat-close")

	request(closing, false)  // the closed chat's exact token opens no new connection
	request(otherChat, true) // another chat's token is not the closed chat's
}
