//go:build goolm && stdjson

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// approval_grant_survival_fix_test.go — FIX-7 Defect 2 (an approval answered
// from within a delegated child's own turn must outlive that child's
// "delegate_terminal" teardown) and its 2026-08 security narrowing (it must
// never reach the delegating agent), migrated to the timed, agent-keyed
// allowances of Skills & Tools C-26 (founder R2-Q1, 2026-11-22).
//
// WHY THE EXPECTATIONS CHANGED. The three original tests answered a card with
// "allow" (Always Allow) and pinned a grant filed under the acting session plus
// a second record under the delegating parent's session
// (rest_tool_registry.go::recordGrantOnDelegationParent). C-26 retires both:
//   - the contract (ToolApprovalActionRequest) refuses "allow" with 400 "This
//     choice is no longer offered. Choose Approve Once or a time limit."; the
//     answer is now allow_for with a duration, which records ONE allowance for
//     entry.AgentID, "the agent named on the card" (C-26, Lifetime and scope,
//     row Key);
//   - row Key: "The session id is no longer part of the key"; row Covers:
//     "every session of that agent ... A helper of another agent, or a
//     delegation to another agent, is not covered and asks as usual";
//   - code-path rows: ApprovalGrantStore.ClearSession "no longer clears
//     allowances (they belong to the agent)"; ApprovalGrantStore.InheritFrom
//     "copies no grants (self-helpers share the key; other agents get
//     nothing)"; HandleToolApprovals "records one timed allowance for
//     entry.AgentID" (the parent-session record is gone);
//   - security point 5: "a delegation to another agent no longer inherits the
//     delegating chat's grants".
//
// DISPOSITION (old test -> new test)
//
//	AlwaysAllow_SurvivesChildTeardown_ScopedToChildAgent ->
//	AllowFor_SurvivesChildTeardown_ScopedToCardAgent
//	  KEPT: Defect 2 (the allowance outlives the child's own teardown); the
//	  2026-08 narrowing (it never reaches the delegating agent, Jim); tool
//	  scoping (another tool is not allowed); the immediate within-turn effect;
//	  grant_recorded true.
//	  FLIPPED (ClearSession row): "the child's own key is cleared by its own
//	  session teardown" - a chat close ends no allowance.
//	  FLIPPED (Covers): "the next sibling delegation must re-prompt" - a new
//	  delegate session of the same agent is covered, with no copy at spawn; the
//	  spawn-time InheritFrom call is kept and now changes nothing.
//	  RETIRED: the write under the parent's session as a mechanism. Its
//	  observable (the card's agent is covered on the parent's session id) stays,
//	  now because the session id is not part of the key.
//	  ADDED: a shell card stays narrow - another command of the same tool is not
//	  covered (contract: scope "exact" is the default when omitted).
//	AlwaysAllow_RootSessionNoSpuriousParentWrite ->
//	AllowFor_RootSessionRecordsOnlyForCardAgent
//	  KEPT: a root chat's own answer works; no fabricated second key (now an
//	  assertion on other agents and other tools). ADDED (security point 5): Jim's
//	  allowance does not reach Ava through a delegation.
//	AlwaysAllow_ParentUnresolved_GrantRecordedFalse ->
//	AllowFor_ParentUnresolved_StillRecordsForCardAgent
//	  FLIPPED: with no parent record there is no step to skip, so an unresolvable
//	  parent changes nothing and grant_recorded is true. RETIRED: "grant_recorded
//	  false because the parent write was skipped".
//	NEW AllowFor_NotStored_ReportsGrantRecordedFalse
//	  The honesty contract the retired test guarded ("a true here would lie"),
//	  kept with a trigger C-26 still has: an answer whose allowance cannot be
//	  keyed (the card names no agent). Contract: grant_recorded false means "this
//	  call was approved once, but the next matching call will ask again".
//
// Binding rules (unchanged): every assertion lands on a REAL
// *session.UnifiedStore (through a real *agent.AgentLoop), the REAL
// *security.ApprovalGrantStore (ApprovalGrants()) and the REAL
// HandleToolApprovals handler driven through httptest; the cards come from the
// real registry. GREEN, implementation mutation and proof of failability are
// deferred to CHECK.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestToolApproval_AllowFor' -p 1 ./pkg/gateway/

package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// newDefect2TestRestAPI mirrors rest_extra_test.go's newTestRestAPIWithHome
// but additionally seeds cfg.Agents.List with two real agents ("jim", "ava"),
// so the delegation under test runs between two registered agents.
func newDefect2TestRestAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "jim", Name: "Jim"},
				{ID: "ava", Name: "Ava"},
			},
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
	}
}

// survivalGrants is the lookup every assertion below reads through: the
// store's own IsAllowed, never a test-owned shadow of it.
type survivalGrants interface {
	IsAllowed(sessionID, agentID, tool string, args map[string]any) bool
}

// survivalLookup names one lookup: the chat a call would run in and the agent
// making it.
type survivalLookup struct{ sessionID, agentID string }

func requireNothingAllowed(
	t *testing.T, grants survivalGrants, tool string, args map[string]any, at ...survivalLookup,
) {
	t.Helper()
	for _, l := range at {
		require.False(t, grants.IsAllowed(l.sessionID, l.agentID, tool, args),
			"precondition: nothing may be allowed yet (session %s, agent %s, tool %s)", l.sessionID, l.agentID, tool)
	}
}

func assertAllowedAt(
	t *testing.T, grants survivalGrants, tool string, args map[string]any, why string, at ...survivalLookup,
) {
	t.Helper()
	for _, l := range at {
		assert.True(t, grants.IsAllowed(l.sessionID, l.agentID, tool, args),
			"%s (session %s, agent %s, tool %s)", why, l.sessionID, l.agentID, tool)
	}
}

func assertNotAllowedAt(
	t *testing.T, grants survivalGrants, tool string, args map[string]any, why string, at ...survivalLookup,
) {
	t.Helper()
	for _, l := range at {
		assert.False(t, grants.IsAllowed(l.sessionID, l.agentID, tool, args),
			"%s (session %s, agent %s, tool %s)", why, l.sessionID, l.agentID, tool)
	}
}

func newChatSession(t *testing.T, store *session.UnifiedStore, agentID string) string {
	t.Helper()
	meta, err := store.NewSession(session.SessionTypeChat, "web", agentID)
	require.NoError(t, err)
	return meta.ID
}

// newDelegateSession mirrors pkg/agent/subturn.go's spawnSubTurn: a delegate
// session of agentID whose parent session id is stamped through SetMeta.
func newDelegateSession(t *testing.T, store *session.UnifiedStore, agentID, parentSessionID string) string {
	t.Helper()
	meta, err := store.NewSession(session.SessionTypeDelegate, "delegate", agentID)
	require.NoError(t, err)
	require.NoError(t, store.SetMeta(meta.ID, session.MetaPatch{ParentSessionID: &parentSessionID}))
	return meta.ID
}

// postAllowFor answers the card with a one-hour time choice through the real
// handler and decodes the response. It checks only what every accepted time
// choice shares: 200, the echoed action and status "ok".
func postAllowFor(t *testing.T, api *restAPI, approvalID string) gen.ToolApprovalResponse {
	t.Helper()
	w := postToolApprovalRaw(t, api, approvalID, `{"action":"allow_for","duration":"1h"}`)
	require.Equal(t, http.StatusOK, w.Code,
		"C-26: allow_for with a duration answers a tool card; got %d: %s", w.Code, w.Body.String())
	var resp gen.ToolApprovalResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, approvalID, resp.ApprovalId)
	assert.Equal(t, gen.ToolApprovalResponseActionAllowFor, resp.Action, "the response echoes the applied action")
	assert.Equal(t, gen.ToolApprovalResponseStatusOk, resp.Status)
	return resp
}

// requireCallApproved reads the outcome the handler delivered to the waiting
// tool call. The registry pushes it on the entry's buffered channel before the
// handler returns, so a non-blocking read is exact: no wait and no tolerance.
func requireCallApproved(t *testing.T, entry *approvalEntry) {
	t.Helper()
	select {
	case out := <-entry.resultCh:
		assert.True(t, out.Approved, "a time choice approves this call too (outcome reason %q)", out.Reason)
	default:
		t.Fatal("the handler answered the card but delivered no outcome to the waiting call")
	}
}

// answerAllowForRecorded answers the card and checks the "it stuck" half of the
// contract: grant_recorded true, an end time, no partial-result reason, and the
// waiting call released as approved.
func answerAllowForRecorded(t *testing.T, api *restAPI, entry *approvalEntry) gen.ToolApprovalResponse {
	t.Helper()
	resp := postAllowFor(t, api, entry.ApprovalID)
	require.NotNil(t, resp.GrantRecorded, "a time choice must report whether the allowance stuck")
	assert.True(t, *resp.GrantRecorded, "the allowance was stored for the agent named on the card")
	assert.NotNil(t, resp.ExpiresAt, "a recorded allowance reports its end (contract: expires_at)")
	assert.Nil(t, resp.NotAppliedReason, "nothing was left unstored, so there is no partial-result reason")
	requireCallApproved(t, entry)
	return resp
}

// TestToolApproval_AllowFor_SurvivesChildTeardown_ScopedToCardAgent is the
// Defect 2 / security-narrowing pair, end to end through the real call chain:
// HandleToolApprovals (the writer) -> ApprovalGrantStore (the real registry) ->
// CloseSession (the real child teardown). The allowance answered from within
// Ava's delegated turn belongs to Ava, covers every session of Ava, outlives
// the child's own teardown, and never reaches Jim, who delegated.
func TestToolApproval_AllowFor_SurvivesChildTeardown_ScopedToCardAgent(t *testing.T) {
	api := newDefect2TestRestAPI(t)
	reg := newApprovalRegistryV2(64, 300*time.Second)
	api.approvalReg = reg

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "test harness must wire a real session store")
	grants := api.agentLoop.ApprovalGrants()
	require.NotNil(t, grants, "test harness must wire a real ApprovalGrantStore")

	// Root chat session: Jim's own real, durable session; Jim delegates to Ava.
	parentSessionID := newChatSession(t, store, "jim")
	// First delegated child: Ava's own real session, its parent stamped as
	// spawnSubTurn does.
	child1ID := newDelegateSession(t, store, "ava", parentSessionID)
	// Chats outside this delegation: another chat of the card's agent and
	// another chat of the delegating agent.
	avaChatID := newChatSession(t, store, "ava")
	jimChatID := newChatSession(t, store, "jim")

	lsArgs := map[string]any{"command": "ls"}
	rmArgs := map[string]any{"command": "rm -rf /"}
	avaEverywhere := []survivalLookup{{child1ID, "ava"}, {parentSessionID, "ava"}, {avaChatID, "ava"}}
	jimEverywhere := []survivalLookup{{parentSessionID, "jim"}, {jimChatID, "jim"}}

	// Instrument: nothing is allowed anywhere yet, so every positive below is
	// the answer's doing.
	requireNothingAllowed(t, grants, "bash", lsArgs,
		append(append([]survivalLookup{}, avaEverywhere...), jimEverywhere...)...)

	// An approval raised from WITHIN Ava's own child turn (the acting session is
	// child1ID, per approvalEntry.SessionID's own doc comment); the card names
	// "ava", never "jim".
	entry, accepted := reg.requestApproval("tc-1", "bash", lsArgs, "ava", child1ID, "turn-1")
	require.True(t, accepted)
	resp := answerAllowForRecorded(t, api, entry)
	require.NotNil(t, resp.Scope, "a recorded bash allowance reports the scope actually recorded")
	assert.Equal(t, gen.ToolApprovalResponseScopeExact, *resp.Scope,
		"contract: scope exact is the default when omitted, and a shell card stays narrow")

	// (1) Immediate effect preserved: this child turn's own further calls are covered.
	assertAllowedAt(t, grants, "bash", lsArgs,
		"the acting session must hold the card agent's allowance for within-turn reuse", survivalLookup{child1ID, "ava"})

	// (2) C-26 Key and Covers: the allowance belongs to the card's agent, so Ava
	// holds it in every session - the parent's session id and a chat outside the
	// delegation included - without any record under the parent.
	assertAllowedAt(t, grants, "bash", lsArgs,
		"C-26: the session id is no longer part of the key", avaEverywhere...)

	// Security boundary (2026-08 narrowing; C-26 Covers): the allowance must
	// never reach the delegating parent's own agent identity.
	assertNotAllowedAt(t, grants, "bash", lsArgs,
		"security regression: the allowance must not cross into the delegating agent", jimEverywhere...)

	// Scoping: another tool is not covered; a shell card stays narrow, so another
	// command of the same tool is not covered either.
	assertNotAllowedAt(t, grants, "read_file", nil,
		"the allowance must not leak to a different tool", avaEverywhere...)
	assertNotAllowedAt(t, grants, "bash", rmArgs,
		"a shell card's allowance is the exact command; another command of the same tool is not covered",
		avaEverywhere...)

	// Mirror subturn.go's real teardown of the child (CloseSession, called
	// unconditionally in spawnSubTurn's cleanup defer).
	api.agentLoop.CloseSession(child1ID, "delegate_terminal")

	// FLIPPED (ClearSession row): the chat close ends no allowance.
	assertAllowedAt(t, grants, "bash", lsArgs,
		"C-26: ClearSession no longer clears allowances; they belong to the agent", survivalLookup{child1ID, "ava"})
	// THE CRUX (Defect 2): the allowance survives the child's own teardown, on
	// the parent's session id and in a chat that never took part.
	assertAllowedAt(t, grants, "bash", lsArgs,
		"Defect 2 violated: the allowance was wiped by the child's own session teardown",
		survivalLookup{parentSessionID, "ava"}, survivalLookup{avaChatID, "ava"})

	// FLIPPED (Covers): a NEW delegate session of the same agent is covered. The
	// spawn-time InheritFrom stays in the flow (it sources from the delegating
	// agent, Jim) and now changes nothing: the allowance needs no copy.
	child2ID := newDelegateSession(t, store, "ava", parentSessionID)
	grants.InheritFrom(parentSessionID, "jim", child2ID, "ava")
	assertAllowedAt(t, grants, "bash", lsArgs,
		"C-26 Covers: a new delegate session of the same agent holds the allowance, with no copy at spawn",
		survivalLookup{child2ID, "ava"})

	// Neither the teardown nor the spawn moved the allowance to Jim.
	assertNotAllowedAt(t, grants, "bash", lsArgs,
		"security regression after teardown and spawn: the allowance must not cross into the delegating agent",
		jimEverywhere...)
}

// TestToolApproval_AllowFor_RootSessionRecordsOnlyForCardAgent proves the
// common case: an approval raised directly in a real, non-delegated chat (no
// ParentSessionID) records the card agent's allowance, no panic, and nothing
// else - no other agent and no other tool gains anything, and a delegation to
// another agent inherits nothing of it.
func TestToolApproval_AllowFor_RootSessionRecordsOnlyForCardAgent(t *testing.T) {
	api := newDefect2TestRestAPI(t)
	reg := newApprovalRegistryV2(64, 300*time.Second)
	api.approvalReg = reg

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	grants := api.agentLoop.ApprovalGrants()
	require.NotNil(t, grants)

	rootID := newChatSession(t, store, "jim")
	otherJimChatID := newChatSession(t, store, "jim")
	// Jim's chat delegates to Ava: a delegation to ANOTHER agent.
	avaHelperID := newDelegateSession(t, store, "ava", rootID)
	requireNothingAllowed(t, grants, "bash", nil,
		survivalLookup{rootID, "jim"}, survivalLookup{otherJimChatID, "jim"},
		survivalLookup{rootID, "ava"}, survivalLookup{avaHelperID, "ava"})

	entry, accepted := reg.requestApproval("tc-root", "bash", map[string]any{}, "jim", rootID, "turn-root")
	require.True(t, accepted)
	answerAllowForRecorded(t, api, entry)

	// KEPT: a root chat's own answer works.
	assertAllowedAt(t, grants, "bash", nil,
		"a root (non-delegated) chat's own answer must work", survivalLookup{rootID, "jim"})
	// C-26 Key and Covers: it is the agent's, so Jim's other chat holds it too.
	assertAllowedAt(t, grants, "bash", nil,
		"C-26: the same agent in another chat holds the allowance", survivalLookup{otherJimChatID, "jim"})

	// No fabricated second key: no other agent, no other tool.
	assertNotAllowedAt(t, grants, "bash", nil,
		"no allowance may appear for an agent the card did not name",
		survivalLookup{rootID, "ava"}, survivalLookup{otherJimChatID, "ava"})
	assertNotAllowedAt(t, grants, "read_file", nil,
		"no allowance may appear for a tool the card did not name",
		survivalLookup{rootID, "jim"}, survivalLookup{otherJimChatID, "jim"})

	// Security point 5: the spawn-time InheritFrom of Jim's delegation to Ava
	// copies nothing of Jim's allowance to Ava.
	grants.InheritFrom(rootID, "jim", avaHelperID, "ava")
	assertNotAllowedAt(t, grants, "bash", nil,
		"C-26 security point 5: a delegation to another agent no longer inherits the delegating chat's grants",
		survivalLookup{avaHelperID, "ava"})
	assertAllowedAt(t, grants, "bash", nil,
		"the delegation must not take the allowance away from the delegating agent", survivalLookup{rootID, "jim"})
}

// TestToolApproval_AllowFor_ParentUnresolved_StillRecordsForCardAgent covers a
// delegated child whose parent cannot be resolved (its owner is not a
// registered agent). The old design needed the parent to store the grant and
// reported grant_recorded false when it could not. C-26 removes the parent
// record: the allowance belongs to the card's agent, so the answer is recorded
// whatever the parent is, and grant_recorded must say so - and be true to it, in
// another chat and after the child's teardown.
func TestToolApproval_AllowFor_ParentUnresolved_StillRecordsForCardAgent(t *testing.T) {
	api := newDefect2TestRestAPI(t)
	reg := newApprovalRegistryV2(64, 300*time.Second)
	api.approvalReg = reg

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	grants := api.agentLoop.ApprovalGrants()
	require.NotNil(t, grants)

	// Parent session owned by an agent that is NOT in the registry.
	parentSessionID := newChatSession(t, store, "ghost")
	childID := newDelegateSession(t, store, "ava", parentSessionID)
	avaChatID := newChatSession(t, store, "ava")

	lsArgs := map[string]any{"command": "ls"}
	requireNothingAllowed(t, grants, "bash", lsArgs,
		survivalLookup{childID, "ava"}, survivalLookup{avaChatID, "ava"},
		survivalLookup{parentSessionID, "ava"}, survivalLookup{parentSessionID, "ghost"})

	entry, accepted := reg.requestApproval("tc-ghost-parent", "bash", lsArgs, "ava", childID, "turn-ghost")
	require.True(t, accepted)
	answerAllowForRecorded(t, api, entry)

	// The flag said "stuck": the store must agree, now and after the child is gone.
	assertAllowedAt(t, grants, "bash", lsArgs,
		"grant_recorded was true, so the child's own session must hold the allowance", survivalLookup{childID, "ava"})
	api.agentLoop.CloseSession(childID, "delegate_terminal")
	assertAllowedAt(t, grants, "bash", lsArgs,
		"grant_recorded was true, so the allowance must survive the child's teardown in another chat",
		survivalLookup{avaChatID, "ava"})
	// FLIPPED: the old test asserted that the parent key was NOT written. The
	// lookup is true now because the session id is no longer part of the key,
	// not because anything was written under the parent.
	assertAllowedAt(t, grants, "bash", lsArgs,
		"C-26: the session id is no longer part of the key", survivalLookup{parentSessionID, "ava"})
	// Never into the unresolvable parent's own agent.
	assertNotAllowedAt(t, grants, "bash", lsArgs,
		"the allowance must not cross into the parent's agent", survivalLookup{parentSessionID, "ghost"})
}

// TestToolApproval_AllowFor_NotStored_ReportsGrantRecordedFalse is the honesty
// contract the retired ParentUnresolved test guarded: when the allowance cannot
// be stored, the call is still approved once, but the response must not say the
// allowance stuck (contract, ToolApprovalResponse.grant_recorded: "False means
// this call was approved once, but the next matching call will ask again; the
// allowance did not stick (see not_applied_reason)"). The trigger is one C-26
// keeps: a card that names no agent, which no (agent id, kind, subject) key can
// hold - the store's empty-key fail-safe.
func TestToolApproval_AllowFor_NotStored_ReportsGrantRecordedFalse(t *testing.T) {
	api := newDefect2TestRestAPI(t)
	reg := newApprovalRegistryV2(64, 300*time.Second)
	api.approvalReg = reg

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	grants := api.agentLoop.ApprovalGrants()
	require.NotNil(t, grants)

	chatID := newChatSession(t, store, "ava")
	otherChatID := newChatSession(t, store, "ava")
	lsArgs := map[string]any{"command": "ls"}
	everywhere := []survivalLookup{{chatID, "ava"}, {otherChatID, "ava"}, {chatID, "jim"}, {chatID, ""}}
	requireNothingAllowed(t, grants, "bash", lsArgs, everywhere...)

	entry, accepted := reg.requestApproval("tc-no-agent", "bash", lsArgs, "", chatID, "turn-no-agent")
	require.True(t, accepted)
	resp := postAllowFor(t, api, entry.ApprovalID)

	require.NotNil(t, resp.GrantRecorded, "a time choice must report whether the allowance stuck")
	assert.False(t, *resp.GrantRecorded, "nothing could be stored, so the response must not say it was")
	assert.Nil(t, resp.ExpiresAt, "no allowance was recorded, so there is no end time")
	assert.Nil(t, resp.Scope, "contract: scope is omitted unless the allowance was recorded")
	require.NotNil(t, resp.NotAppliedReason, "a partial result must carry its reason")
	const prefix = "Approved for this request only. The time limit was not saved: "
	reason := *resp.NotAppliedReason
	assert.True(t, strings.HasPrefix(reason, prefix), "reason %q must start with %q", reason, prefix)
	assert.True(t, strings.HasSuffix(reason, "."), "reason %q must end with a full stop", reason)
	assert.Greater(t, len(reason), len(prefix)+1, "the line must name a reason after the prefix: %q", reason)
	assert.LessOrEqual(t, len(reason), 500, "contract: not_applied_reason is at most 500 characters")

	// This call is still approved once ...
	requireCallApproved(t, entry)
	// ... and nothing sticks, for any agent in any chat.
	assertNotAllowedAt(t, grants, "bash", lsArgs,
		"the response said the allowance did not stick, so the next matching call must ask again", everywhere...)
}
