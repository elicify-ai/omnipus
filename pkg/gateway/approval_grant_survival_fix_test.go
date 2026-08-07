// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// approval_grant_survival_fix_test.go — regression coverage for FIX-7's
// Defect 2: an "Always Allow" grant recorded while a tool-approval request
// is raised from WITHIN a delegated child's own turn is filed only under
// that child's single-use session id (pkg/gateway/rest_tool_registry.go's
// HandleToolApprovals), which is then wiped the moment the child's own
// "delegate_terminal" CloseSession teardown fires (pkg/agent/subturn.go).
// Since InheritFrom (pkg/security/approvalgrants.go) always seeds a NEW
// child's grant set from the DELEGATING PARENT's own (session, agent)
// identity, a grant that only ever lived under the child's key can never
// reach the next sibling delegation — the user is prompted again on every
// single delegation, forever.
//
// This file is a NEW addition alongside pkg/agent/session_end_fix_test.go
// (the fix-wave brief's named new-test file). Defect 2's fix lives entirely
// in pkg/gateway/rest_tool_registry.go and pkg/gateway/approvals.go — both
// exclusively owned by this same fix — and HandleToolApprovals/
// approvalRegistryV2 are package-private, so proving the fix against the
// REAL production entry point (not a re-implementation of its logic)
// requires an internal `package gateway` test. No existing test file is
// modified; this is a distinct, uniquely-named file.
//
// Binding rules: every assertion below lands on a REAL *session.UnifiedStore
// (via a real *agent.AgentLoop's GetSessionStore()), a REAL
// *security.ApprovalGrantStore (via ApprovalGrants()), and the REAL
// HandleToolApprovals HTTP handler driven through httptest — never a mock or
// a spy that merely records its argument.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestToolApproval_AlwaysAllow' -p 1 ./pkg/gateway/
package gateway

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// newDefect2TestRestAPI mirrors rest_extra_test.go's newTestRestAPIWithHome
// but additionally seeds cfg.Agents.List with two real agents ("jim", "ava")
// so agent.NewAgentRegistry registers them for real — AgentForSession (used
// by the fix under test to resolve the delegating parent's own agent
// identity) requires a REAL registry entry, not just a session-meta string.
func newDefect2TestRestAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:      tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
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

// TestToolApproval_AlwaysAllow_SurvivesAcrossSiblingDelegations is the
// Defect 2 red/green pair, end to end through the real production call
// chain: HandleToolApprovals (the grant WRITER) -> ApprovalGrantStore (the
// real registry) -> CloseSession/ClearSession (the real child teardown) ->
// InheritFrom (the real next-delegation seeding).
func TestToolApproval_AlwaysAllow_SurvivesAcrossSiblingDelegations(t *testing.T) {
	api := newDefect2TestRestAPI(t)
	reg := newApprovalRegistryV2(64, 300*time.Second)
	api.approvalReg = reg

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "test harness must wire a real session store")

	grants := api.agentLoop.ApprovalGrants()
	require.NotNil(t, grants, "test harness must wire a real ApprovalGrantStore")

	// Root chat session: Jim's own real, durable session — mirrors a real
	// user chat turn that will delegate to Ava multiple times.
	parentMeta, err := store.NewSession(session.SessionTypeChat, "web", "jim")
	require.NoError(t, err)
	parentSessionID := parentMeta.ID

	// First delegated child: Ava's own real session, with ParentSessionID
	// stamped exactly as pkg/agent/subturn.go's spawnSubTurn does via its
	// own SetMeta(childID, MetaPatch{ParentSessionID: &parentSessionID}) call.
	child1Meta, err := store.NewSession(session.SessionTypeDelegate, "delegate", "ava")
	require.NoError(t, err)
	child1ID := child1Meta.ID
	require.NoError(t, store.SetMeta(child1ID, session.MetaPatch{ParentSessionID: &parentSessionID}))

	// Precondition: no grants exist anywhere yet.
	require.False(t, grants.IsAllowed(child1ID, "ava", "bash"))
	require.False(t, grants.IsAllowed(parentSessionID, "jim", "bash"))

	// --- Act 1: an approval raised from WITHIN Ava's own child turn (the
	// acting session is child1ID, per approvalEntry.SessionID's own doc
	// comment), resolved with "always". ---
	entry, accepted := reg.requestApproval(
		"tc-1", "bash", map[string]any{"command": "ls"}, "ava", child1ID, "turn-1",
	)
	require.True(t, accepted)
	w := postToolApproval(t, api, entry.ApprovalID, "always")
	require.Equal(t, http.StatusOK, w.Code)

	// (1) Immediate effect preserved: this exact child turn's own further
	// calls within the SAME turn are still covered.
	assert.True(t, grants.IsAllowed(child1ID, "ava", "bash"),
		"the acting session's own key must still be granted for within-turn reuse")

	// (2) THE FIX: the grant must ALSO now be recorded under the delegating
	// PARENT's own durable (session, agent) identity — exactly the source
	// key InheritFrom reads for every future sibling delegation Jim makes.
	assert.True(t, grants.IsAllowed(parentSessionID, "jim", "bash"),
		"Defect 2 violated: the grant did not propagate onto the delegating parent's durable identity")

	// Scoping proof: propagation must not leak to an unrelated tool or agent.
	assert.False(t, grants.IsAllowed(parentSessionID, "jim", "read_file"),
		"propagation must not leak to a different tool")
	assert.False(t, grants.IsAllowed(parentSessionID, "ava", "bash"),
		"propagation must land on the PARENT's own agent id (jim), not the acting child's (ava)")

	// --- Act 2: mirror subturn.go's real teardown for child1
	// (al.CloseSession(childID, "delegate_terminal"), called unconditionally
	// in spawnSubTurn's cleanup defer). ---
	api.agentLoop.CloseSession(child1ID, "delegate_terminal")

	// Sanity: the child's OWN key is correctly cleared by its own session
	// teardown (ClearSession) — this part was never broken.
	assert.False(t, grants.IsAllowed(child1ID, "ava", "bash"),
		"sanity: the child's own key must be cleared by its own session teardown")

	// THE CRUX: the parent's durable key must SURVIVE the CHILD's teardown —
	// before the fix, ClearSession(child1ID) was the ONLY place the grant
	// had ever been recorded, so this assertion is exactly where the old
	// code lost it.
	assert.True(t, grants.IsAllowed(parentSessionID, "jim", "bash"),
		"Defect 2 violated: the parent's durable grant was wiped by the CHILD's own session teardown")

	// --- Act 3: the NEXT sibling delegation — a brand-new child session,
	// with spawnSubTurn's real InheritFrom(source=parent, dest=child2) call
	// reproduced verbatim. ---
	child2Meta, err := store.NewSession(session.SessionTypeDelegate, "delegate", "ava")
	require.NoError(t, err)
	child2ID := child2Meta.ID
	require.NoError(t, store.SetMeta(child2ID, session.MetaPatch{ParentSessionID: &parentSessionID}))

	grants.InheritFrom(parentSessionID, "jim", child2ID, "ava")

	// THE PAYOFF: the second delegation's child inherits the grant WITHOUT a
	// fresh approval prompt. Before the fix, this assertion fails — the
	// exact user-visible MAJOR regression Defect 2 describes ("prompted
	// again on the very next delegation, forever").
	assert.True(t, grants.IsAllowed(child2ID, "ava", "bash"),
		"Defect 2 violated: the next sibling delegation did not inherit the Always-Allow grant — "+
			"the user would be prompted again on every single delegation, forever")
}

// TestToolApproval_AlwaysAllow_RootSessionNoSpuriousParentWrite proves the
// fix is a clean no-op for the common case: an approval raised directly in a
// real, non-delegated user chat turn (no ParentSessionID) must behave
// exactly as it did before this fix — a single grant under its own key, no
// panic, no fabricated second key.
func TestToolApproval_AlwaysAllow_RootSessionNoSpuriousParentWrite(t *testing.T) {
	api := newDefect2TestRestAPI(t)
	reg := newApprovalRegistryV2(64, 300*time.Second)
	api.approvalReg = reg

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	grants := api.agentLoop.ApprovalGrants()
	require.NotNil(t, grants)

	rootMeta, err := store.NewSession(session.SessionTypeChat, "web", "jim")
	require.NoError(t, err)
	rootID := rootMeta.ID

	entry, accepted := reg.requestApproval("tc-root", "bash", map[string]any{}, "jim", rootID, "turn-root")
	require.True(t, accepted)
	w := postToolApproval(t, api, entry.ApprovalID, "always")
	require.Equal(t, http.StatusOK, w.Code)

	assert.True(t, grants.IsAllowed(rootID, "jim", "bash"),
		"a root (non-delegated) session's own Always-Allow grant must still work exactly as before this fix")
}
