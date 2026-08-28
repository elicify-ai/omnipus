// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// approval_grant_survival_fix_test.go — regression coverage for FIX-7's
// Defect 2: an "Always Allow" grant recorded while a tool-approval request
// is raised from WITHIN a delegated child's own turn is filed only under
// that child's single-use session id (pkg/gateway/rest_tool_registry.go's
// HandleToolApprovals), which is then wiped the moment the child's own
// "delegate_terminal" CloseSession teardown fires (pkg/agent/subturn.go).
//
// UPDATED 2026-08 (fix-wave finding #2, security): the original fix also
// propagated the grant onto the delegating PARENT's OWN agent identity
// (AgentForSession(meta.ParentSessionID)) so it would land exactly on
// InheritFrom's source key and auto-flow into every future sibling
// delegation. That was itself a security defect — the approval modal named
// the CHILD agent, and recording under the PARENT's identity silently
// escalated a child-scoped decision across an agent boundary the user never
// reviewed (see rest_tool_registry.go's recordGrantOnDelegationParent doc
// comment for the full rationale). The fix now keys the durable record by
// (meta.ParentSessionID, entry.AgentID) — the parent's persistent SESSION id
// paired with the SAME agent identity the modal displayed — trading away
// automatic propagation into a DIFFERENT future delegation (InheritFrom's
// source is always the parent's OWN agent id, never a prior child's, so it
// no longer finds this narrower key) for not crossing that agent boundary.
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
	"encoding/json"
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

// TestToolApproval_AlwaysAllow_SurvivesChildTeardown_ScopedToChildAgent is
// the Defect 2 / security-narrowing red/green pair, end to end through the
// real production call chain: HandleToolApprovals (the grant WRITER) ->
// ApprovalGrantStore (the real registry) -> CloseSession/ClearSession (the
// real child teardown). It proves the grant survives the acting child
// session's own teardown (Defect 2's original point) while staying scoped
// to the CHILD's own agent identity — never escalating onto the delegating
// PARENT's own agent id (the security fix).
func TestToolApproval_AlwaysAllow_SurvivesChildTeardown_ScopedToChildAgent(t *testing.T) {
	api := newDefect2TestRestAPI(t)
	reg := newApprovalRegistryV2(64, 300*time.Second)
	api.approvalReg = reg

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "test harness must wire a real session store")

	grants := api.agentLoop.ApprovalGrants()
	require.NotNil(t, grants, "test harness must wire a real ApprovalGrantStore")

	// Root chat session: Jim's own real, durable session — mirrors a real
	// user chat turn that delegates to Ava.
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
	require.False(t, grants.IsAllowed(child1ID, "ava", "bash", nil))
	require.False(t, grants.IsAllowed(parentSessionID, "ava", "bash", nil))
	require.False(t, grants.IsAllowed(parentSessionID, "jim", "bash", nil))

	// --- Act 1: an approval raised from WITHIN Ava's own child turn (the
	// acting session is child1ID, per approvalEntry.SessionID's own doc
	// comment), resolved with "always". The approval modal named "ava" —
	// entry.AgentID — never "jim". ---
	lsArgs := map[string]any{"command": "ls"}
	entry, accepted := reg.requestApproval(
		"tc-1", "bash", lsArgs, "ava", child1ID, "turn-1",
	)
	require.True(t, accepted)
	w := postToolApproval(t, api, entry.ApprovalID, "always")
	require.Equal(t, http.StatusOK, w.Code)
	var happyResp struct {
		GrantRecorded *bool `json:"grant_recorded"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &happyResp))
	require.NotNil(t, happyResp.GrantRecorded, "always must report whether the durable grant stuck")
	assert.True(t, *happyResp.GrantRecorded, "child + parent writes both succeeded — Always Allow stuck")

	// (1) Immediate effect preserved: this exact child turn's own further
	// calls within the SAME turn are still covered.
	assert.True(t, grants.IsAllowed(child1ID, "ava", "bash", lsArgs),
		"the acting session's own key must still be granted for within-turn reuse")

	// (2) THE FIX (narrowed 2026-08): the grant must ALSO now be recorded
	// under the delegating parent's own durable SESSION id, but scoped to
	// entry.AgentID ("ava") — the SAME identity the approval modal named —
	// not the parent's own driving agent ("jim").
	assert.True(t, grants.IsAllowed(parentSessionID, "ava", "bash", lsArgs),
		"the grant must propagate onto the parent session under the CHILD's own agent identity")

	// Security boundary proof: propagation must NEVER escalate onto the
	// delegating parent's own agent identity — that is exactly the
	// cross-agent-boundary leak the 2026-08 security review closed. Before
	// that fix this assertion failed (the grant landed on "jim").
	assert.False(t, grants.IsAllowed(parentSessionID, "jim", "bash", lsArgs),
		"security regression: the grant must not cross into the delegating PARENT's own agent identity")

	// Scoping proof: propagation must not leak to an unrelated tool.
	assert.False(t, grants.IsAllowed(parentSessionID, "ava", "read_file", nil),
		"propagation must not leak to a different tool")

	// --- Act 2: mirror subturn.go's real teardown for child1
	// (al.CloseSession(childID, "delegate_terminal"), called unconditionally
	// in spawnSubTurn's cleanup defer). ---
	api.agentLoop.CloseSession(child1ID, "delegate_terminal")

	// Sanity: the child's OWN key is correctly cleared by its own session
	// teardown (ClearSession) — this part was never broken.
	assert.False(t, grants.IsAllowed(child1ID, "ava", "bash", lsArgs),
		"sanity: the child's own key must be cleared by its own session teardown")

	// THE CRUX (Defect 2's original point, still holds under the narrowed
	// key): the parent-session-scoped key must SURVIVE the CHILD's own
	// teardown — before Defect 2's fix, ClearSession(child1ID) was the ONLY
	// place the grant had ever been recorded, so this assertion is exactly
	// where the original bug lost it.
	assert.True(t, grants.IsAllowed(parentSessionID, "ava", "bash", lsArgs),
		"Defect 2 violated: the parent-scoped durable grant was wiped by the CHILD's own session teardown")

	// --- Act 3: the accepted trade-off of the 2026-08 narrowing — a NEW
	// sibling delegation, via subturn.go's real spawn-time InheritFrom call,
	// sources from (parentSessionID, "jim") — the PARENT's OWN agent id,
	// per InheritFrom's documented contract — never from the narrower
	// (parentSessionID, "ava") key this fix now writes to. That key holds no
	// grants, so nothing is copied into the new child: the standard
	// cross-delegation auto-inheritance path no longer fires for this grant.
	// This is intentional — see recordGrantOnDelegationParent's doc comment
	// ("Trade-off accepted deliberately") — re-prompting on the next
	// delegation is the accepted cost of not crossing the agent boundary the
	// user never reviewed. ---
	child2Meta, err := store.NewSession(session.SessionTypeDelegate, "delegate", "ava")
	require.NoError(t, err)
	child2ID := child2Meta.ID
	require.NoError(t, store.SetMeta(child2ID, session.MetaPatch{ParentSessionID: &parentSessionID}))

	grants.InheritFrom(parentSessionID, "jim", child2ID, "ava")

	assert.False(t, grants.IsAllowed(child2ID, "ava", "bash", lsArgs),
		"standard spawn-time InheritFrom sources from the PARENT's own agent id, which this fix "+
			"deliberately does not write to — the next sibling delegation must re-prompt rather than "+
			"silently inherit trust that was never reviewed for it")

	// --- Act 4: the path the narrowed key DOES still serve — the SAME
	// agent identity ("ava") later becomes the directly-active agent ON the
	// parent's own persistent session (e.g. an in-chat agent switch), rather
	// than a freshly-spawned child. pkg/agent/loop.go's per-turn approval
	// check keys off (ts.transcriptSessionID, ts.agentID) — which now
	// matches (parentSessionID, "ava") exactly, so the durable grant this
	// fix recorded is not dead weight: it is reachable via same-identity
	// reuse on the parent session itself. ---
	assert.True(t, grants.IsAllowed(parentSessionID, "ava", "bash", lsArgs),
		"the narrowed key must still resolve when the SAME agent identity is later active directly "+
			"on the parent's own session — the one path this fix intentionally preserves")
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

	assert.True(t, grants.IsAllowed(rootID, "jim", "bash", nil),
		"a root (non-delegated) session's own Always-Allow grant must still work exactly as before this fix")
}

// TestToolApproval_AlwaysAllow_ParentUnresolved_GrantRecordedFalse is the
// honesty contract for a delegated child whose parent write cannot land:
// this call is still approved, but grant_recorded must be false so the
// modal can say Always Allow did not stick. The child session is torn
// down at the end of the turn; a true here would lie.
func TestToolApproval_AlwaysAllow_ParentUnresolved_GrantRecordedFalse(t *testing.T) {
	api := newDefect2TestRestAPI(t)
	reg := newApprovalRegistryV2(64, 300*time.Second)
	api.approvalReg = reg

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	grants := api.agentLoop.ApprovalGrants()
	require.NotNil(t, grants)

	// Parent session owned by an agent that is NOT in the registry, so
	// AgentForSession fails the liveness gate and the parent write is skipped.
	parentMeta, err := store.NewSession(session.SessionTypeChat, "web", "ghost")
	require.NoError(t, err)
	parentSessionID := parentMeta.ID

	childMeta, err := store.NewSession(session.SessionTypeDelegate, "delegate", "ava")
	require.NoError(t, err)
	childID := childMeta.ID
	require.NoError(t, store.SetMeta(childID, session.MetaPatch{ParentSessionID: &parentSessionID}))

	lsArgs := map[string]any{"command": "ls"}
	entry, accepted := reg.requestApproval("tc-ghost-parent", "bash", lsArgs, "ava", childID, "turn-ghost")
	require.True(t, accepted)
	w := postToolApproval(t, api, entry.ApprovalID, "always")
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Action        string `json:"action"`
		Status        string `json:"status"`
		GrantRecorded *bool  `json:"grant_recorded"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "always", resp.Action)
	assert.Equal(t, "ok", resp.Status)
	require.NotNil(t, resp.GrantRecorded, "always must report the grant outcome")
	assert.False(t, *resp.GrantRecorded,
		"parent write skipped — Always Allow will not survive this child's teardown")

	// Child Record still succeeded (this-turn reuse works); parent did not.
	assert.True(t, grants.IsAllowed(childID, "ava", "bash", lsArgs),
		"this child turn's own key must still be granted")
	assert.False(t, grants.IsAllowed(parentSessionID, "ava", "bash", lsArgs),
		"the parent key must not have been written")
}
