//go:build goolm && stdjson

package gateway

// C-26/D17 observers are concrete calls to the loop-owned store and its actual
// next-call approval gate. No assignable boolean decides allowance coverage or
// token revocation. Only the missing native duration-bearing seed is a handoff;
// it receives inputs and must make these independent production observers true.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"github.com/stretchr/testify/require"
)

const permissionChangeSession = "permission-change-origin"

// Provisional input-only binding, NOT a spec API. The same test author must
// reconcile this with the actual timed recording/card API after implementation.
// A test closure cannot stand in for it; a named no-op fails the positive real
// store assertions in seedRunningAllowance before any ending assertion runs.
var seedNativePermissionAllowance func(*agent.AgentLoop, string, string, string, string) error

// requireNativePermissionSeed is the loud developer handoff. Each subtest calls
// it FIRST, so a missing seed is the first failure, never hidden behind a setup
// write (such as the not-yet-implemented C-2 PATCH writer) that fails earlier.
func requireNativePermissionSeed(t *testing.T) {
	t.Helper()
	nativebinding.Require(t, "pkg/agent", seedNativePermissionAllowance,
		"C-26/S-128/D17: actual loop-owned duration-bearing allowance input; no fabricated coverage or expiry")
}

func seedRunningAllowance(t *testing.T, api *restAPI, agentID, subject string) {
	t.Helper()
	requireNativePermissionSeed(t)
	require.NoError(t, seedNativePermissionAllowance(api.agentLoop, permissionChangeSession, agentID, subject, "3h"))
	assertPermissionAllowance(t, api, agentID, subject, true)
}

func permissionChangeCall(subject string) (string, map[string]any) {
	switch subject {
	case "plan":
		return "Skill", map[string]any{"name": "plan"}
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

// Config-file edits are inputs to the real fourth writer. Save to the actual
// home, then use the same refresh/rewire path as gateway config mutations; never
// substitute an in-memory map edit for a reload.
func applyConfigReload(t *testing.T, api *restAPI, mutate func(*config.Config)) {
	t.Helper()
	path := filepath.Join(api.homePath, "config.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var cfg config.Config
	require.NoError(t, json.Unmarshal(raw, &cfg))
	mutate(&cfg)
	require.NoError(t, config.SaveConfig(path, &cfg))
	require.NoError(t, api.refreshConfigAndRewireServices(path), "native config reload, not a test callback returning success")
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
// through the store's current API. Those records are only a stand-in for live
// store state; this is the one place that changes when the actual duration-bearing
// recording API lands (the same author reconciles it with seedNativePermissionAllowance).
func seedLegacySessionAllowances(t *testing.T, store *security.ApprovalGrantStore, agentID string) {
	t.Helper()
	for _, sid := range permissionChangeSessions {
		for _, subject := range permissionChangeSubjects {
			switch subject {
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
