// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the Central Tool Registry REST+WS layer (A3 lane).
//
// Required test functions (spec revision 6):
//   - TestREST_GetTools_FullSnapshot
//   - TestREST_GetAgentTools_FilteredView
//   - TestREST_GetBuiltinTools_Returns404
//   - TestREST_ApproveAuth_Unauthenticated401
//   - TestREST_ApproveDenyCancel_StateTransitions
//   - TestREST_LateApprove_Returns410
//   - TestApprovalRegistry_SaturationDefault64
//   - TestApprovalRegistry_BatchShortCircuit_MixedPolicy
//   - TestWS_SessionStatePayloadSchema
//   - TestWS_SessionState_SeesAllPendingApprovals
//   - TestWS_ToolApprovalRequired_ExpiresInMs

package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- helpers ---

// newTestRestAPIWithApprovalReg returns a restAPI with a live approvalRegistryV2 wired in.
// Uses the same minimal setup as newTestRestAPIWithHome.
func newTestRestAPIWithApprovalReg(t *testing.T) (*restAPI, *approvalRegistryV2) {
	t.Helper()
	api := newTestRestAPIWithHome(t)
	reg := newApprovalRegistryV2(64, 300*time.Second)
	api.approvalReg = reg
	return api, reg
}

// postToolApproval sends a POST to HandleToolApprovals and returns the recorder.
// Single-user model: HandleToolApprovals does not role-gate, so the caller is
// always injected via withAdminRole (an authenticated user for audit attribution).
func postToolApproval(
	t *testing.T,
	api *restAPI,
	approvalID, action string,
) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"action":"` + action + `"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tool-approvals/"+approvalID, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	// Set the URL path explicitly so TrimPrefix extracts the right ID.
	r.URL.Path = "/api/v1/tool-approvals/" + approvalID
	api.HandleToolApprovals(w, r)
	return w
}

// --- REST: GET /api/v1/tools ---

// TestREST_GetTools_FullSnapshot verifies GET /api/v1/tools returns a JSON array
// with at least one tool entry containing the required fields.
// BDD: Given a running gateway with at least one registered tool,
// When GET /api/v1/tools is called with a valid auth token,
// Then 200 with a JSON array where every entry has {name, description, scope, category, source}.
// Traces to: tool-registry-redesign-spec.md FR-027
func TestREST_GetTools_FullSnapshot(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil)
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleToolsRegistry(w, r)

	require.Equal(t, http.StatusOK, w.Code, "GET /api/v1/tools must return 200: %s", w.Body)

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries), "must unmarshal as array")

	// Empty slice is acceptable when agent loop has no tools loaded, but the response
	// must be a JSON array (not null).
	assert.NotNil(t, entries, "tools array must not be null")

	for i, entry := range entries {
		assert.Contains(t, entry, "name", "entry %d missing 'name'", i)
		assert.Contains(t, entry, "description", "entry %d missing 'description'", i)
		assert.Contains(t, entry, "scope", "entry %d missing 'scope'", i)
		assert.Contains(t, entry, "category", "entry %d missing 'category'", i)
		assert.Contains(t, entry, "source", "entry %d missing 'source'", i)
	}
}

// TestREST_GetTools_MethodNotAllowed verifies that non-GET methods are rejected.
func TestREST_GetTools_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/tools", nil)
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleToolsRegistry(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// --- REST: GET /api/v1/agents/{id}/tools ---

// TestREST_GetAgentTools_FilteredView verifies GET /api/v1/agents/{id}/tools returns
// the per-agent filtered view required by FR-086.
// BDD: Given a registered agent ID,
// When GET /api/v1/agents/{id}/tools is called,
// Then 200 with {agent_type, config, effective_tools} where every effective tool has
// {name, configured_policy, effective_policy, manifest_tier}.
// Traces to: tool-registry-redesign-spec.md FR-028, FR-086; Gap 3 manifest_tier.
func TestREST_GetAgentTools_FilteredView(t *testing.T) {
	// newTestRestAPIWithHome seeds no agents (the retired "main" sentinel used
	// to be registered implicitly regardless of cfg); use the
	// newTestRestAPIWithHomeAndAgent variant (rest_tasks_occurrences_test.go)
	// which seeds a real chat-target agent ("mia") instead.
	api := newTestRestAPIWithHomeAndAgent(t)

	agentID := "mia"
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+agentID+"/tools", nil)
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleAgentToolsRegistry(w, r, agentID)

	require.Equal(t, http.StatusOK, w.Code, "GET agent tools must return 200: %s", w.Body)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "must unmarshal as object")

	// Top-level shape (FR-028).
	assert.Contains(t, resp, "agent_type", "response must include agent_type")
	assert.Contains(t, resp, "config", "response must include config")
	assert.Contains(t, resp, "tools", "response must include tools")

	toolsArr, ok := resp["tools"].([]any)
	require.True(t, ok, "tools must be an array")

	validTiers := map[string]bool{"full": true, "compressed": true, "infra": true}

	for i, raw := range toolsArr {
		entry, ok := raw.(map[string]any)
		require.True(t, ok, "tools[%d] must be an object", i)
		assert.Contains(t, entry, "name", "entry %d missing 'name'", i)
		assert.Contains(t, entry, "configured_policy", "entry %d missing 'configured_policy'", i)
		assert.Contains(t, entry, "effective_policy", "entry %d missing 'effective_policy'", i)
		// Gap 3: every entry must carry a valid manifest_tier.
		tier, hasTier := entry["manifest_tier"].(string)
		assert.True(t, hasTier, "entry %d missing 'manifest_tier'", i)
		assert.True(t, validTiers[tier], "entry %d has invalid manifest_tier %q", i, tier)
	}
}

// TestREST_GetAgentTools_ManifestTierValues verifies that well-known tools carry the
// correct manifest_tier classification in the per-agent tools view.
// BDD: Given an agent that has read_file (full), workspace.shell (compressed), and
// possibly ToolSearch (infra) in its tool set,
// When GET /api/v1/agents/{id}/tools is called,
// Then read_file has manifest_tier="full", workspace.shell has "compressed", and
// if ToolSearch is present it has "infra".
// Traces to: tool-manifest-optimization-2026-06.md Gap 3.
func TestREST_GetAgentTools_ManifestTierValues(t *testing.T) {
	api := newTestRestAPIWithHomeAndAgent(t)

	agentID := "mia"
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+agentID+"/tools", nil)
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleAgentToolsRegistry(w, r, agentID)

	require.Equal(t, http.StatusOK, w.Code, "GET agent tools must return 200: %s", w.Body)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "must unmarshal as object")

	toolsArr, ok := resp["tools"].([]any)
	require.True(t, ok, "tools must be an array")

	// Build a name→tier index from the response.
	tierByName := make(map[string]string)
	for _, raw := range toolsArr {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		tier, _ := entry["manifest_tier"].(string)
		if name != "" {
			tierByName[name] = tier
		}
	}

	// read_file is in the full set (high-frequency core tool).
	if tier, found := tierByName["read_file"]; found {
		assert.Equal(t, "full", tier, "read_file must have manifest_tier=full")
	}

	// write_file is also in the full set.
	if tier, found := tierByName["write_file"]; found {
		assert.Equal(t, "full", tier, "write_file must have manifest_tier=full")
	}

	// workspace.shell is a management tool — lazy/compressed.
	if tier, found := tierByName["workspace.shell"]; found {
		assert.Equal(t, "compressed", tier, "workspace.shell must have manifest_tier=compressed")
	}

	// ToolSearch is an infra tool when registered (compressed mode on).
	if tier, found := tierByName["ToolSearch"]; found {
		assert.Equal(t, "infra", tier, "ToolSearch must have manifest_tier=infra")
	}

	// search_tools_bm25 is an infra tool when registered.
	if tier, found := tierByName["search_tools_bm25"]; found {
		assert.Equal(t, "infra", tier, "search_tools_bm25 must have manifest_tier=infra")
	}

	// delegate (ADR-036 merge of spawn/run_subagent/check_spawn_status) is a
	// delegation tool — lazy/compressed.
	if tier, found := tierByName["delegate"]; found {
		assert.Equal(t, "compressed", tier, "delegate must have manifest_tier=compressed")
	}

	// Every entry must have a valid tier value.
	validTiers := map[string]bool{"full": true, "compressed": true, "infra": true}
	for name, tier := range tierByName {
		assert.True(t, validTiers[tier], "tool %q has invalid manifest_tier %q", name, tier)
	}
}

// --- REST: GET /api/v1/tools/builtin → 404 ---

// TestREST_GetBuiltinTools_Returns404 verifies that the deprecated builtin catalog
// endpoint returns HTTP 404 and an error body.
// BDD: Given any caller,
// When GET /api/v1/tools/builtin is called,
// Then 404 with a JSON error body.
// Traces to: tool-registry-redesign-spec.md FR-029
func TestREST_GetBuiltinTools_Returns404(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/tools/builtin", nil)
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleBuiltinToolsDeprecated(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code, "GET /api/v1/tools/builtin must return 404")

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "error", "response must have an 'error' field")
	errMsg, _ := resp["error"].(string)
	assert.Contains(t, strings.ToLower(errMsg), "use", "error must mention the replacement endpoint")
}

// --- REST: POST /api/v1/tool-approvals auth checks ---

// TestREST_ApproveAuth_Unauthenticated401 verifies that HandleToolApprovals is wired
// with withAuth: an unauthenticated request returns 401.
// BDD: Given no Authorization header and OMNIPUS_BEARER_TOKEN set,
// When POST /api/v1/tool-approvals/{id} is called without a token,
// Then 401 Unauthorized.
// Traces to: tool-registry-redesign-spec.md FR-014
func TestREST_ApproveAuth_Unauthenticated401(t *testing.T) {
	// When OMNIPUS_BEARER_TOKEN is set, withAuth requires a matching header.
	// A request without Authorization must get 401.
	t.Setenv("OMNIPUS_BEARER_TOKEN", "test-secret-token")

	api := newTestRestAPIWithHome(t)

	// withAuth reads OMNIPUS_BEARER_TOKEN from the environment at request time.
	handler := api.withAuth(api.HandleToolApprovals)

	body := bytes.NewBufferString(`{"action":"approve"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tool-approvals/some-id", body)
	// No Authorization header — withAuth must reject.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code, "missing auth must return 401")
}

// --- REST: state transitions ---

// TestREST_ApproveDenyCancel_StateTransitions verifies that approve, deny, and cancel
// actions each return HTTP 200 and transition the approval to the expected terminal state.
// BDD: Given a pending approval,
// When approve/deny/cancel is posted,
// Then 200 and the resultCh delivers the correct outcome.
// Traces to: tool-registry-redesign-spec.md FR-011, FR-017
func TestREST_ApproveDenyCancel_StateTransitions(t *testing.T) {
	cases := []struct {
		action         string
		expectedReason string
	}{
		{"approve", "approved"},
		{"deny", "user"},
		{"cancel", "cancel"},
	}

	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			api, reg := newTestRestAPIWithApprovalReg(t)

			entry, accepted := reg.requestApproval(
				"tc-"+tc.action, "read_file",
				map[string]any{"path": "/tmp/test"},
				"agent-2", "sess-2", "turn-2",
			)
			require.True(t, accepted)

			// Drain the resultCh in background so it doesn't block.
			var gotOutcome ApprovalOutcome
			doneCh := make(chan struct{})
			go func() {
				gotOutcome = <-entry.resultCh
				close(doneCh)
			}()

			w := postToolApproval(t, api, entry.ApprovalID, tc.action)
			require.Equal(t, http.StatusOK, w.Code, "action %q must return 200: %s", tc.action, w.Body)

			// Confirm the outcome was delivered.
			select {
			case <-doneCh:
				assert.Equal(t, tc.expectedReason, gotOutcome.Reason,
					"action %q must deliver reason %q", tc.action, tc.expectedReason)
			case <-time.After(2 * time.Second):
				t.Fatalf("action %q: resultCh never received outcome", tc.action)
			}

			// Response body must include approval_id and action.
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, entry.ApprovalID, resp["approval_id"])
			assert.Equal(t, tc.action, resp["action"])
		})
	}
}

// --- REST: 410 Gone on resolved approval ---

// TestREST_LateApprove_Returns410 verifies that acting on an already-resolved approval
// returns HTTP 410 Gone (FR-018).
// BDD: Given a pending approval that has already been approved,
// When a second POST /api/v1/tool-approvals/{id} with action="approve" is sent,
// Then 410 Gone.
// Traces to: tool-registry-redesign-spec.md FR-018
func TestREST_LateApprove_Returns410(t *testing.T) {
	api, reg := newTestRestAPIWithApprovalReg(t)

	entry, accepted := reg.requestApproval(
		"tc-late", "search_web",
		map[string]any{"query": "golang"},
		"agent-3", "sess-3", "turn-3",
	)
	require.True(t, accepted)

	// Drain outcome to prevent resultCh blockage.
	go func() { <-entry.resultCh }()

	// First action — must succeed.
	w1 := postToolApproval(t, api, entry.ApprovalID, "approve")
	require.Equal(t, http.StatusOK, w1.Code, "first approve must return 200: %s", w1.Body)

	// Second action on the same (now terminal) approval — must return 410.
	w2 := postToolApproval(t, api, entry.ApprovalID, "deny")
	assert.Equal(t, http.StatusGone, w2.Code, "second action on resolved approval must return 410: %s", w2.Body)
}

// --- Approval registry: saturation default 64 ---

// TestApprovalRegistry_SaturationDefault64 verifies that a registry with cap=64
// accepts exactly 64 requests and saturates on the 65th with a pre-delivered
// denied_saturated synthetic entry.
// Note: cap=0 means UNLIMITED per FR-016 (WARN emitted by ValidateSaturationCap);
// the gateway default is 32 (not 64). This test uses an explicit cap of 64.
// BDD: Given a registry with cap=64,
// When 65 approvals are requested,
// Then the first 64 succeed (accepted=true) and the 65th has accepted=false with
// denied_saturated state.
// Traces to: tool-registry-redesign-spec.md FR-016, MAJ-009
func TestApprovalRegistry_SaturationDefault64(t *testing.T) {
	// Explicit cap=64 (cap=0 means unlimited since FR-016 redesign).
	reg := newApprovalRegistryV2(64, 300*time.Second)

	const wantCap = 64
	entries := make([]*approvalEntry, 0, wantCap+1)

	for i := range wantCap {
		e, accepted := reg.requestApproval(
			"tc-sat-"+string(rune('A'+i%26)), "read_file",
			map[string]any{},
			"agent-sat", "sess-sat", "turn-sat",
		)
		require.True(t, accepted, "request %d (of %d) must be accepted", i+1, wantCap)
		require.NotNil(t, e)
		entries = append(entries, e)
	}

	// 65th request must be saturated.
	saturated, accepted := reg.requestApproval(
		"tc-sat-overflow", "read_file",
		map[string]any{},
		"agent-sat", "sess-sat", "turn-sat",
	)
	assert.False(t, accepted, "65th request must be rejected (saturated)")
	require.NotNil(t, saturated)
	assert.Equal(t, ApprovalStateDeniedSaturated, saturated.state,
		"saturated entry must have state denied_saturated")

	// Pre-delivered outcome must be immediately available (no blocking).
	select {
	case outcome := <-saturated.resultCh:
		assert.False(t, outcome.Approved)
		assert.Equal(t, "saturated", outcome.Reason)
	default:
		t.Fatal("saturated entry resultCh must have pre-delivered outcome")
	}

	// Cleanup: drain all 64 pending entries so timers don't fire after test.
	for _, e := range entries {
		go func(e *approvalEntry) {
			reg.resolve(e.ApprovalID, ApprovalActionCancel)
			<-e.resultCh
		}(e)
	}
}

// --- Approval registry: batch short-circuit ---

// TestApprovalRegistry_BatchShortCircuit_MixedPolicy verifies that
// cancelBatchShortCircuit transitions a pending entry to denied_batch_short_circuit
// and pre-delivers the outcome with Reason="batch_short_circuit".
// BDD: Given three pending approvals in the same batch,
// When the first is denied and the remaining two are batch-short-circuited,
// Then the two short-circuited entries have state denied_batch_short_circuit and
// their resultCh delivers Approved=false, Reason="batch_short_circuit".
// Traces to: tool-registry-redesign-spec.md FR-065
func TestApprovalRegistry_BatchShortCircuit_MixedPolicy(t *testing.T) {
	reg := newApprovalRegistryV2(64, 300*time.Second)

	makeEntry := func(id string) *approvalEntry {
		e, accepted := reg.requestApproval(
			"tc-"+id, "exec",
			map[string]any{"cmd": "ls"},
			"agent-batch", "sess-batch", "turn-batch",
		)
		require.True(t, accepted, "entry %s must be accepted", id)
		return e
	}

	// Create three pending approvals.
	e1 := makeEntry("batch-1")
	e2 := makeEntry("batch-2")
	e3 := makeEntry("batch-3")

	// Deny the first (user action).
	go func() { <-e1.resultCh }()
	ok, gone := reg.resolve(e1.ApprovalID, ApprovalActionDeny)
	require.True(t, ok, "deny e1 must succeed")
	require.False(t, gone)

	// Short-circuit the remaining two.
	sc2 := reg.cancelBatchShortCircuit(e2.ApprovalID)
	sc3 := reg.cancelBatchShortCircuit(e3.ApprovalID)

	assert.True(t, sc2, "cancelBatchShortCircuit e2 must return true")
	assert.True(t, sc3, "cancelBatchShortCircuit e3 must return true")

	// Both must have pre-delivered outcomes.
	checkOutcome := func(t *testing.T, e *approvalEntry, label string) {
		t.Helper()
		select {
		case outcome := <-e.resultCh:
			assert.False(t, outcome.Approved, "%s: must not be approved", label)
			assert.Equal(t, "batch_short_circuit", outcome.Reason, "%s: wrong reason", label)
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: resultCh never received outcome", label)
		}
	}
	checkOutcome(t, e2, "e2")
	checkOutcome(t, e3, "e3")

	// Verify states directly.
	e2s := reg.get(e2.ApprovalID)
	e3s := reg.get(e3.ApprovalID)
	require.NotNil(t, e2s)
	require.NotNil(t, e3s)
	assert.Equal(t, ApprovalStateDeniedBatchShortCircuit, e2s.state)
	assert.Equal(t, ApprovalStateDeniedBatchShortCircuit, e3s.state)

	// Further short-circuit on terminal state must return false.
	assert.False(t, reg.cancelBatchShortCircuit(e2.ApprovalID),
		"cancelBatchShortCircuit on terminal entry must return false")
}

// --- Approval state machine: all-transitions test (FR-070, M6) ---

// TestApprovalRegistry_AllTransitions exercises every valid state transition in
// the 8-state approval state machine:
//
//  1. pending → approved           (approve action)
//  2. pending → denied_user        (deny action)
//  3. pending → denied_cancel      (cancel action)
//  4. pending → denied_timeout     (timer fires)
//  5. pending → denied_restart     (cancelAllPendingForRestart)
//  6. pending → denied_saturated   (cap exceeded; skip-pending path)
//  7. pending → denied_batch_short_circuit (cancelBatchShortCircuit)
//  8. terminal → HTTP 410 Gone     (any action on a terminal entry)
//
// Traces to: tool-registry-redesign-spec.md FR-070, FR-016, FR-017, FR-018, FR-065
func TestApprovalRegistry_AllTransitions(t *testing.T) {
	t.Run("pending→approved", func(t *testing.T) {
		reg := newApprovalRegistryV2(64, 300*time.Second)
		e, accepted := reg.requestApproval("tc-ap", "read_file", map[string]any{}, "a", "s", "t")
		require.True(t, accepted)
		go func() { <-e.resultCh }()
		ok, gone := reg.resolve(e.ApprovalID, ApprovalActionApprove)
		require.True(t, ok)
		require.False(t, gone)
		require.Equal(t, ApprovalStateApproved, reg.get(e.ApprovalID).state)
	})

	t.Run("pending→denied_user", func(t *testing.T) {
		reg := newApprovalRegistryV2(64, 300*time.Second)
		e, accepted := reg.requestApproval("tc-du", "read_file", map[string]any{}, "a", "s", "t")
		require.True(t, accepted)
		go func() { <-e.resultCh }()
		ok, gone := reg.resolve(e.ApprovalID, ApprovalActionDeny)
		require.True(t, ok)
		require.False(t, gone)
		require.Equal(t, ApprovalStateDeniedUser, reg.get(e.ApprovalID).state)
	})

	t.Run("pending→denied_cancel", func(t *testing.T) {
		reg := newApprovalRegistryV2(64, 300*time.Second)
		e, accepted := reg.requestApproval("tc-dc", "exec", map[string]any{}, "a", "s", "t")
		require.True(t, accepted)
		go func() { <-e.resultCh }()
		ok, gone := reg.resolve(e.ApprovalID, ApprovalActionCancel)
		require.True(t, ok)
		require.False(t, gone)
		require.Equal(t, ApprovalStateDeniedCancel, reg.get(e.ApprovalID).state)
	})

	t.Run("pending→denied_timeout", func(t *testing.T) {
		// Very short timeout so the test completes quickly.
		reg := newApprovalRegistryV2(64, 50*time.Millisecond)
		e, accepted := reg.requestApproval("tc-dt", "search_web", map[string]any{}, "a", "s", "t")
		require.True(t, accepted)

		// Wait for the outcome: must be denied_timeout.
		select {
		case outcome := <-e.resultCh:
			assert.False(t, outcome.Approved)
			assert.Equal(t, "timeout", outcome.Reason)
		case <-time.After(2 * time.Second):
			t.Fatal("timeout transition did not fire within 2 s")
		}
		// State must be terminal after outcome is delivered.
		e2 := reg.get(e.ApprovalID)
		if e2 != nil {
			assert.Equal(t, ApprovalStateDeniedTimeout, e2.state)
		}
	})

	t.Run("pending→denied_restart", func(t *testing.T) {
		reg := newApprovalRegistryV2(64, 300*time.Second)
		e1, _ := reg.requestApproval("tc-dr-1", "exec", map[string]any{}, "a", "s", "t")
		e2, _ := reg.requestApproval("tc-dr-2", "read_file", map[string]any{}, "a", "s", "t")

		canceled := reg.cancelAllPendingForRestart()
		require.Len(t, canceled, 2)

		// Both entries must have pre-delivered denied_restart outcomes.
		for _, e := range []*approvalEntry{e1, e2} {
			select {
			case outcome := <-e.resultCh:
				assert.False(t, outcome.Approved)
				assert.Equal(t, "restart", outcome.Reason)
			default:
				t.Fatalf("cancelAllPendingForRestart: entry %s has no pre-delivered outcome", e.ApprovalID)
			}
		}
	})

	t.Run("pending→denied_saturated", func(t *testing.T) {
		reg := newApprovalRegistryV2(1, 300*time.Second)
		e1, accepted1 := reg.requestApproval("tc-sat-1", "exec", map[string]any{}, "a", "s", "t")
		require.True(t, accepted1, "first entry must be accepted with cap=1")

		// Second request exceeds cap → saturated.
		e2, accepted2 := reg.requestApproval("tc-sat-2", "exec", map[string]any{}, "a", "s", "t")
		require.False(t, accepted2, "second entry must be rejected (saturated)")
		require.Equal(t, ApprovalStateDeniedSaturated, e2.state)

		select {
		case outcome := <-e2.resultCh:
			assert.False(t, outcome.Approved)
			assert.Equal(t, "saturated", outcome.Reason)
		default:
			t.Fatal("saturated entry must have pre-delivered outcome")
		}
		// Cleanup.
		go func() { reg.resolve(e1.ApprovalID, ApprovalActionCancel); <-e1.resultCh }()
	})

	t.Run("pending→denied_batch_short_circuit", func(t *testing.T) {
		reg := newApprovalRegistryV2(64, 300*time.Second)
		e, accepted := reg.requestApproval("tc-bsc", "exec", map[string]any{}, "a", "s", "t")
		require.True(t, accepted)

		ok := reg.cancelBatchShortCircuit(e.ApprovalID)
		require.True(t, ok)
		require.Equal(t, ApprovalStateDeniedBatchShortCircuit, reg.get(e.ApprovalID).state)

		select {
		case outcome := <-e.resultCh:
			assert.False(t, outcome.Approved)
			assert.Equal(t, "batch_short_circuit", outcome.Reason)
		default:
			t.Fatal("batch_short_circuit entry must have pre-delivered outcome")
		}
	})

	t.Run("terminal→410_Gone", func(t *testing.T) {
		reg := newApprovalRegistryV2(64, 300*time.Second)
		e, accepted := reg.requestApproval("tc-term", "read_file", map[string]any{}, "a", "s", "t")
		require.True(t, accepted)

		// Approve it to put it in a terminal state.
		go func() { <-e.resultCh }()
		ok, _ := reg.resolve(e.ApprovalID, ApprovalActionApprove)
		require.True(t, ok)

		// Any subsequent resolve must return gone=true (HTTP 410 semantics).
		_, gone := reg.resolve(e.ApprovalID, ApprovalActionDeny)
		assert.True(t, gone, "resolve on terminal entry must return gone=true (HTTP 410 semantics)")
	})
}

// --- WS: session_state payload schema ---

// TestWS_SessionStatePayloadSchema verifies that the session_state frame emitted on
// WS connect conforms to the binding schema in FR-081:
// {type, user_id, pending_approvals: [], emitted_at}.
// BDD: Given a WebSocket connection that authenticates in dev-mode-bypass,
// When the connection is established,
// Then the first server frame has type="session_state", user_id, pending_approvals (array),
// and emitted_at (RFC3339).
// Traces to: tool-registry-redesign-spec.md FR-052, FR-081
func TestWS_SessionStatePayloadSchema(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	// Attach an approval registry so emitSessionState has a valid registry.
	handler.approvalRegV2 = newApprovalRegistryV2(64, 300*time.Second)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	// Authenticate — triggers emitSessionState after ping pump starts.
	sendWSAuthFrameDevMode(t, conn)

	// Read frames until we receive session_state or timeout.
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var sessionStateFrame map[string]any
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var f map[string]any
		if jsonErr := json.Unmarshal(data, &f); jsonErr != nil {
			continue
		}
		if f["type"] == "session_state" {
			sessionStateFrame = f
			break
		}
	}

	require.NotNil(t, sessionStateFrame, "must receive a session_state frame after WS auth")

	// Validate required fields (FR-081 binding schema).
	assert.Equal(t, "session_state", sessionStateFrame["type"])
	assert.Contains(t, sessionStateFrame, "user_id", "must contain user_id")
	assert.Contains(t, sessionStateFrame, "emitted_at", "must contain emitted_at")
	assert.Contains(t, sessionStateFrame, "pending_approvals", "must contain pending_approvals")

	// pending_approvals must be an array (not null) even when empty.
	approvals, ok := sessionStateFrame["pending_approvals"].([]any)
	assert.True(t, ok, "pending_approvals must be a JSON array, got %T", sessionStateFrame["pending_approvals"])
	assert.NotNil(t, approvals, "pending_approvals must not be null")

	// emitted_at must be parseable as RFC3339.
	emittedAt, _ := sessionStateFrame["emitted_at"].(string)
	require.NotEmpty(t, emittedAt, "emitted_at must not be empty")
	_, parseErr := time.Parse(time.RFC3339, emittedAt)
	assert.NoError(t, parseErr, "emitted_at must be valid RFC3339: %q", emittedAt)
}

// --- WS: session_state sees every pending approval (single-user model) ---

// TestWS_SessionState_SeesAllPendingApprovals verifies that any connection
// receives every pending approval in session_state — FR-073's admin/non-admin
// scoping was removed under the single-user model (see ws_tool_approval.go).
// BDD: Given a pending approval in the registry and a WS connection,
// When the connection is established,
// Then session_state.pending_approvals contains the pending entry.
// Traces to: tool-registry-redesign-spec.md FR-073, FR-081
func TestWS_SessionState_SeesAllPendingApprovals(t *testing.T) {
	reg := newApprovalRegistryV2(64, 300*time.Second)

	// Register one pending approval.
	pendingEntry, accepted := reg.requestApproval(
		"tc-scope", "read_file",
		map[string]any{"path": "/etc/passwd"},
		"agent-scope", "sess-scope", "turn-scope",
	)
	require.True(t, accepted)
	t.Cleanup(func() {
		go func() { reg.resolve(pendingEntry.ApprovalID, ApprovalActionCancel) }()
	})

	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	handler.approvalRegV2 = reg

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	wc := &wsConn{
		sendCh: make(chan []byte, 16),
		doneCh: make(chan struct{}),
		userID: "testuser",
	}

	handler.emitSessionState(wc)

	var frame map[string]any
	select {
	case raw := <-wc.sendCh:
		require.NoError(t, json.Unmarshal(raw, &frame))
	case <-time.After(2 * time.Second):
		t.Fatal("emitSessionState never sent a frame")
	}

	require.Equal(t, "session_state", frame["type"])
	approvals, ok := frame["pending_approvals"].([]any)
	require.True(t, ok)
	assert.NotEmpty(t, approvals, "the connection must see the pending approval")
}

// --- WS: tool_approval_required with expires_in_ms ---

// TestWS_ToolApprovalRequired_ExpiresInMs verifies that the tool_approval_required
// frame carries expires_in_ms (relative milliseconds) instead of expires_at (OBS-004),
// and that the value is within the expected range for a 300 s timeout.
// BDD: Given a pending approval with a 300 s timeout,
// When broadcastToolApprovalRequired is called on a WSHandler with one connected session,
// Then the sendCh receives a frame with type="tool_approval_required" and
// expires_in_ms in [0, 300_000].
// Traces to: tool-registry-redesign-spec.md FR-011, FR-082, OBS-004
func TestWS_ToolApprovalRequired_ExpiresInMs(t *testing.T) {
	reg := newApprovalRegistryV2(64, 300*time.Second)

	entry, accepted := reg.requestApproval(
		"tc-broadcast", "exec",
		map[string]any{"cmd": "whoami"},
		"agent-br", "sess-br", "turn-br",
	)
	require.True(t, accepted)
	t.Cleanup(func() {
		go func() { reg.resolve(entry.ApprovalID, ApprovalActionCancel) }()
	})

	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	handler.approvalRegV2 = reg

	// Inject a fake wsConn into handler.sessions directly.
	wc := &wsConn{
		sendCh: make(chan []byte, 16),
		doneCh: make(chan struct{}),
		userID: "broadcast-test",
	}
	handler.mu.Lock()
	if handler.sessions == nil {
		handler.sessions = make(map[string]*wsConn)
	}
	handler.sessions["broadcast-test"] = wc
	handler.mu.Unlock()

	// Broadcast the tool_approval_required frame.
	handler.broadcastToolApprovalRequired(entry)

	// The frame must be in the sendCh immediately.
	var frame map[string]any
	select {
	case raw := <-wc.sendCh:
		require.NoError(t, json.Unmarshal(raw, &frame))
	case <-time.After(2 * time.Second):
		t.Fatal("broadcastToolApprovalRequired never put frame into sendCh")
	}

	// Validate frame type.
	assert.Equal(t, "tool_approval_required", frame["type"])

	// expires_in_ms must be present and in [0, 300_000] (300 s * 1000 ms/s).
	assert.NotContains(t, frame, "expires_at", "frame must not contain expires_at (OBS-004)")
	expiresInMsRaw, ok := frame["expires_in_ms"]
	require.True(t, ok, "frame must contain expires_in_ms")
	expiresInMs, ok := expiresInMsRaw.(float64) // JSON numbers decode as float64
	require.True(t, ok, "expires_in_ms must be a number, got %T", expiresInMsRaw)
	assert.GreaterOrEqual(t, expiresInMs, float64(0), "expires_in_ms must be >= 0")
	assert.LessOrEqual(t, expiresInMs, float64(300_000), "expires_in_ms must be <= 300_000")

	// Verify the other required fields.
	assert.Equal(t, entry.ApprovalID, frame["approval_id"])
	assert.Equal(t, entry.ToolName, frame["tool_name"])
	assert.Equal(t, entry.AgentID, frame["agent_id"])
	assert.Equal(t, entry.SessionID, frame["session_id"])
	assert.Equal(t, entry.TurnID, frame["turn_id"])
}

// TestWS_ToolApprovalRequired_NilArgsBecomesEmptyObject locks the invariant that a
// tool invocation with no arguments serializes to "args": {}, never "args": null.
// Regression: the SPA's ToolApprovalModal calls Object.keys(args) directly; null
// would crash with "null is not an object (evaluating 'Object.keys(r)')".
// cloneStringAnyMap in pkg/agent/hooks.go returns nil for empty input, so the
// broadcast site must defensively coerce nil to an empty map before marshaling.
func TestWS_ToolApprovalRequired_NilArgsBecomesEmptyObject(t *testing.T) {
	reg := newApprovalRegistryV2(64, 300*time.Second)

	// Request approval with nil args — mirrors what cloneStringAnyMap produces
	// when the LLM invokes a tool with no parameters (e.g. list_agents).
	entry, accepted := reg.requestApproval(
		"tc-nil-args", "list_agents",
		nil, // <-- nil args
		"agent-na", "sess-na", "turn-na",
	)
	require.True(t, accepted)
	t.Cleanup(func() {
		go func() { reg.resolve(entry.ApprovalID, ApprovalActionCancel) }()
	})

	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	handler.approvalRegV2 = reg

	wc := &wsConn{
		sendCh: make(chan []byte, 16),
		doneCh: make(chan struct{}),
		userID: "nil-args-test",
	}
	handler.mu.Lock()
	if handler.sessions == nil {
		handler.sessions = make(map[string]*wsConn)
	}
	handler.sessions["nil-args-test"] = wc
	handler.mu.Unlock()

	handler.broadcastToolApprovalRequired(entry)

	var raw []byte
	select {
	case raw = <-wc.sendCh:
	case <-time.After(2 * time.Second):
		t.Fatal("broadcastToolApprovalRequired never put frame into sendCh")
	}

	// The raw JSON must contain "args":{}, never "args":null.
	assert.Contains(t, string(raw), `"args":{}`,
		"args field must serialize as empty object, not null, when the tool has no parameters")
	assert.NotContains(t, string(raw), `"args":null`,
		"args field must never be null on the wire — SPA's Object.keys(args) would crash")
}

// --- registry unit: timeout fires denied_timeout ---

// TestApprovalRegistry_TimeoutTransition verifies that the timeout timer fires and
// transitions a pending approval to denied_timeout, delivering the outcome.
func TestApprovalRegistry_TimeoutTransition(t *testing.T) {
	reg := newApprovalRegistryV2(64, 50*time.Millisecond) // very short timeout for test

	entry, accepted := reg.requestApproval(
		"tc-timeout", "exec",
		map[string]any{},
		"agent-t", "sess-t", "turn-t",
	)
	require.True(t, accepted)

	select {
	case outcome := <-entry.resultCh:
		assert.False(t, outcome.Approved, "timeout must result in denial")
		assert.Equal(t, "timeout", outcome.Reason)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout did not fire within 2s")
	}

	// State must now be terminal.
	got := reg.get(entry.ApprovalID)
	require.NotNil(t, got)
	assert.True(t, got.state.isTerminal(), "post-timeout state must be terminal")
}

// --- registry unit: cancelAllPendingForRestart ---

// TestApprovalRegistry_CancelAllPendingForRestart verifies that a graceful shutdown
// transitions all pending approvals to denied_restart (FR-013, FR-048).
func TestApprovalRegistry_CancelAllPendingForRestart(t *testing.T) {
	reg := newApprovalRegistryV2(64, 300*time.Second)

	entries := make([]*approvalEntry, 0, 3)
	for i := range 3 {
		e, accepted := reg.requestApproval(
			"tc-restart-"+string(rune('0'+i)), "exec",
			map[string]any{},
			"agent-r", "sess-r", "turn-r",
		)
		require.True(t, accepted)
		entries = append(entries, e)
	}

	canceled := reg.cancelAllPendingForRestart()
	assert.Len(t, canceled, 3, "must have canceled all 3 entries")

	for i, e := range entries {
		select {
		case outcome := <-e.resultCh:
			assert.False(t, outcome.Approved, "entry %d: must not be approved on restart", i)
			assert.Equal(t, "restart", outcome.Reason, "entry %d: reason must be restart", i)
		case <-time.After(2 * time.Second):
			t.Fatalf("entry %d: resultCh never received restart outcome", i)
		}
	}
}

// --- HandleToolApprovals: unknown action returns 400 ---

// TestREST_HandleToolApprovals_UnknownAction verifies that an unknown action string
// returns HTTP 400 Bad Request.
func TestREST_HandleToolApprovals_UnknownAction(t *testing.T) {
	api, reg := newTestRestAPIWithApprovalReg(t)

	entry, accepted := reg.requestApproval(
		"tc-unk", "read_file",
		map[string]any{},
		"agent-u", "sess-u", "turn-u",
	)
	require.True(t, accepted)
	t.Cleanup(func() { go func() { reg.resolve(entry.ApprovalID, ApprovalActionCancel) }() })

	body := bytes.NewBufferString(`{"action":"teleport"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tool-approvals/"+entry.ApprovalID, body)
	r = withAdminRole(r)
	r.URL.Path = "/api/v1/tool-approvals/" + entry.ApprovalID
	w := httptest.NewRecorder()
	api.HandleToolApprovals(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "unknown action must return 400: %s", w.Body)
}

// --- HandleToolApprovals: nil registry returns 503 ---

// TestREST_HandleToolApprovals_NilRegistry verifies that a missing approvalReg returns 503.
func TestREST_HandleToolApprovals_NilRegistry(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	// Intentionally leave api.approvalReg = nil.

	// We need a valid UUID-like ID to pass validateEntityID.
	validID := "12345678-1234-4234-8234-123456789abc"
	body := bytes.NewBufferString(`{"action":"approve"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tool-approvals/"+validID, body)
	r = withAdminRole(r)
	r.URL.Path = "/api/v1/tool-approvals/" + validID
	w := httptest.NewRecorder()
	api.HandleToolApprovals(w, r)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"nil registry must return 503: %s", w.Body)
}

// --- HandleToolApprovals: missing approval_id returns 404 ---

// TestREST_HandleToolApprovals_NotFound404 verifies that an unknown approval_id returns 404.
func TestREST_HandleToolApprovals_NotFound404(t *testing.T) {
	api, _ := newTestRestAPIWithApprovalReg(t)

	unknownID := "00000000-0000-4000-8000-000000000000"
	body := bytes.NewBufferString(`{"action":"approve"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tool-approvals/"+unknownID, body)
	r = withAdminRole(r)
	r.URL.Path = "/api/v1/tool-approvals/" + unknownID
	w := httptest.NewRecorder()
	api.HandleToolApprovals(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code, "unknown approval_id must return 404: %s", w.Body)
}

// --- WS: broadcastToolApprovalRequired with nil entry is a no-op ---

// TestWS_BroadcastToolApprovalRequired_NilEntry verifies nil entry is handled safely.
func TestWS_BroadcastToolApprovalRequired_NilEntry(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	// Should not panic.
	handler.broadcastToolApprovalRequired(nil)
}

// --- REST: POST /api/v1/tool-approvals/{id} with action:"always" (ADR-036 §3.4) ---

// TestApproveTool_ActionAlways_RecordsGrantAndApproves proves the generic REST
// approval path fully supports the "always" action: a POST with
// {"action":"always"} BOTH (a) approves the pending tool call — an identical
// terminal transition to "approve", so the blocked loop goroutine proceeds —
// AND (b) records a session-scoped "Always Allow" grant for the approval's
// (session, agent, tool), queryable via ApprovalGrantStore.IsAllowed afterward.
//
// This restores, on the generic path, the grant that was previously reachable
// only via the retired exec_approval_response{decision:"always"} WS frame
// (ws_approval.go) — the mechanism agent-delegation-spec.md's FR-D8
// grant-inheritance depends on.
func TestApproveTool_ActionAlways_RecordsGrantAndApproves(t *testing.T) {
	api, reg := newTestRestAPIWithApprovalReg(t)

	const (
		sessionID = "session-always-1"
		agentID   = "agent-a"
		toolName  = "bash"
	)
	grants := api.agentLoop.ApprovalGrants()
	require.NotNil(t, grants, "test harness must wire a real ApprovalGrantStore")

	// Precondition: no grant exists for this (session, agent, tool) yet.
	require.False(t, grants.IsAllowed(sessionID, agentID, toolName, nil),
		"precondition: no Always-Allow grant may exist before the request")

	lsArgs := map[string]any{"command": "ls"}
	entry, accepted := reg.requestApproval(
		"tc-always", toolName, lsArgs, agentID, sessionID, "turn-1",
	)
	require.True(t, accepted)

	// Act: resolve via the generic REST endpoint with action:"always".
	w := postToolApproval(t, api, entry.ApprovalID, "always")

	// (a1) HTTP 200 with the echoed action and ok status.
	require.Equal(t, http.StatusOK, w.Code, "always must be accepted with 200")
	var resp struct {
		ApprovalID    string `json:"approval_id"`
		Action        string `json:"action"`
		Status        string `json:"status"`
		GrantRecorded *bool  `json:"grant_recorded"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, entry.ApprovalID, resp.ApprovalID)
	assert.Equal(t, "always", resp.Action, "response must echo the always action")
	assert.Equal(t, "ok", resp.Status)
	require.NotNil(t, resp.GrantRecorded, "always must report whether the grant stuck")
	assert.True(t, *resp.GrantRecorded, "a complete identity must record the grant")

	// (a2) The pending tool call proceeds: entry transitioned to approved and the
	// blocked loop goroutine receives an Approved outcome on its result channel.
	require.NotNil(t, reg.get(entry.ApprovalID))
	assert.Equal(t, ApprovalStateApproved, reg.get(entry.ApprovalID).state,
		"always must resolve the pending call as approved (same as approve)")
	select {
	case outcome := <-entry.resultCh:
		assert.True(t, outcome.Approved, "the pending call must be approved")
		assert.Equal(t, "approved", outcome.Reason)
	default:
		t.Fatal("expected an approved outcome buffered on the entry's resultCh")
	}

	// (b) A session-scoped grant was recorded and is queryable via IsAllowed.
	assert.True(t, grants.IsAllowed(sessionID, agentID, toolName, lsArgs),
		"always must record a grant for the approval's exact arguments")
	assert.False(t, grants.IsAllowed(sessionID, agentID, toolName, map[string]any{"command": "rm -rf /"}),
		"a grant for {command:ls} must not approve a different argv")

	// Scoping proof (consent boundary): the grant must NOT leak to a different
	// agent, session, or tool.
	assert.False(t, grants.IsAllowed(sessionID, "agent-b", toolName, lsArgs),
		"grant must not apply to a different agent")
	assert.False(t, grants.IsAllowed("session-other", agentID, toolName, lsArgs),
		"grant must not apply to a different session")
	assert.False(t, grants.IsAllowed(sessionID, agentID, "read_file", lsArgs),
		"grant must not apply to a different tool")
}

// HTTP 200 + grant_recorded:false is the honesty contract: this call is
// approved, but Always Allow did not stick because the approval had no
// session identity. requestApproval still accepts an empty session id
// (it increments missingActingSessionID) — that is the production shape
// of a grant that cannot be stored.
func TestApproveTool_ActionAlways_GrantNotRecordedWhenSessionMissing(t *testing.T) {
	api, reg := newTestRestAPIWithApprovalReg(t)
	grants := api.agentLoop.ApprovalGrants()
	require.NotNil(t, grants)

	lsArgs := map[string]any{"command": "ls"}
	entry, accepted := reg.requestApproval(
		"tc-always-nosess", "bash", lsArgs, "agent-a", "", "turn-1",
	)
	require.True(t, accepted)

	w := postToolApproval(t, api, entry.ApprovalID, "always")
	require.Equal(t, http.StatusOK, w.Code, "the call itself is still approved")

	var resp struct {
		Action        string `json:"action"`
		Status        string `json:"status"`
		GrantRecorded *bool  `json:"grant_recorded"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "always", resp.Action)
	assert.Equal(t, "ok", resp.Status)
	require.NotNil(t, resp.GrantRecorded, "always must report the grant outcome")
	assert.False(t, *resp.GrantRecorded, "an empty session id cannot store a standing grant")

	require.NotNil(t, reg.get(entry.ApprovalID))
	assert.Equal(t, ApprovalStateApproved, reg.get(entry.ApprovalID).state)
	assert.False(t, grants.IsAllowed("", "agent-a", "bash", lsArgs),
		"Record must refuse an empty session key")
}

// Compile-time check that wsConn has the userID field used by emitSessionState.
var _ = wsConn{userID: ""}

// Verify the websocket sessions map key type is string (used by broadcastToolApprovalRequired test).
var _ = func() {
	var m map[string]*wsConn
	_ = m
}
