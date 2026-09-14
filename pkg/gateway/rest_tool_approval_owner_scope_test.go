// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for Claude review 2026-09-14 C4: the approval DECISION
// door (POST /api/v1/tool-approvals/{id}, HandleToolApprovals) resolved any
// approval for any authenticated account — it never read the caller identity
// even though withAuth provides UserContextKey, while approvalReg.resolve is
// keyed by approval id alone. D-16 had already scoped who is SHOWN an
// approval (ws_tool_approval.go's approvalVisibleTo); these tests pin the
// same rule on the decision door: the caller must be the approval's owner,
// with the identical owner-less/identity-less widenings the WS side applies.
//
// No existing test file is modified; this is a distinct, uniquely-named file
// (same pattern as approval_grant_survival_fix_test.go), because
// rest_tool_registry_test.go is shared with other fix clusters.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestToolApproval_DecisionDoor' -p 1 ./pkg/gateway/

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// postToolApprovalAs posts a decision to HandleToolApprovals as one specific
// account. username "" sends the request with NO UserContextKey identity —
// the shape the env-token and dev-bypass authentication paths produce.
func postToolApprovalAs(
	t *testing.T,
	api *restAPI,
	username, approvalID, action string,
) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"action":"` + action + `"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tool-approvals/"+approvalID, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if username != "" {
		ctx := context.WithValue(r.Context(), UserContextKey{}, &config.UserConfig{Username: username})
		r = r.WithContext(ctx)
	}
	w := httptest.NewRecorder()
	api.HandleToolApprovals(w, r)
	return w
}

// newOwnerScopeAPI builds a restAPI with a real agent loop and session store
// (the same harness shape approval_grant_survival_fix_test.go uses), plus a
// fresh approval registry wired in.
func newOwnerScopeAPI(t *testing.T) (*restAPI, *approvalRegistryV2) {
	t.Helper()
	api := newDefect2TestRestAPI(t)
	reg := newApprovalRegistryV2(64, 300*time.Second)
	api.approvalReg = reg
	return api, reg
}

// ownedSession creates a real chat session whose meta carries the given
// owner — the exact field (*WSHandler).approvalOwner reads.
func ownedSession(t *testing.T, api *restAPI, owner string) string {
	t.Helper()
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "test harness must wire a real session store")
	meta, err := store.NewSession(session.SessionTypeChat, "web", "jim")
	require.NoError(t, err)
	if owner != "" {
		ownerCopy := owner
		require.NoError(t, store.SetMeta(meta.ID, session.MetaPatch{Owner: &ownerCopy}))
	}
	return meta.ID
}

// entryStateLocked reads an entry's state under the registry lock.
func entryStateLocked(reg *approvalRegistryV2, approvalID string) (ApprovalState, bool) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	e, ok := reg.entries[approvalID]
	if !ok {
		return "", false
	}
	return e.state, true
}

// TestToolApproval_DecisionDoorScopedToOwningAccount is the C4 oracle, for
// every action the door accepts: the owner's POST resolves it (200); a
// different authenticated account's POST is refused (403) and consumes
// nothing — the entry stays pending and the owner can still decide it.
func TestToolApproval_DecisionDoorScopedToOwningAccount(t *testing.T) {
	for _, action := range []string{"approve", "deny", "cancel", "always"} {
		t.Run(action, func(t *testing.T) {
			api, reg := newOwnerScopeAPI(t)
			sessID := ownedSession(t, api, "alice")

			entry, accepted := reg.requestApproval(
				"tc-c4-"+action, "bash", map[string]any{"command": "ls"}, "jim", sessID, "turn-c4",
			)
			require.True(t, accepted)

			// Wrong account: 403, and the attempt must not resolve anything.
			w := postToolApprovalAs(t, api, "bob", entry.ApprovalID, action)
			require.Equal(t, http.StatusForbidden, w.Code,
				"account B deciding account A's approval is the C4 defect; body: %s", w.Body.String())

			state, ok := entryStateLocked(reg, entry.ApprovalID)
			require.True(t, ok, "bob's refused attempt must not delete the entry")
			assert.Equal(t, ApprovalStatePending, state,
				"bob's refused attempt must leave the approval pending for its owner")

			// The owner decides it afterwards, successfully.
			w = postToolApprovalAs(t, api, "alice", entry.ApprovalID, action)
			require.Equal(t, http.StatusOK, w.Code, "the owner's own decision must land; body: %s", w.Body.String())
		})
	}
}

// TestToolApproval_DecisionDoorAlwaysGrantIsScopedWithTheDoor: the "always"
// grant (the durable session-scoped Always-Allow record) is written only
// after the owner gate — a wrong account's 403 must not install a grant.
func TestToolApproval_DecisionDoorAlwaysGrantIsScopedWithTheDoor(t *testing.T) {
	api, reg := newOwnerScopeAPI(t)
	sessID := ownedSession(t, api, "alice")
	grants := api.agentLoop.ApprovalGrants()
	require.NotNil(t, grants)

	lsArgs := map[string]any{"command": "ls"}
	entry, accepted := reg.requestApproval("tc-c4-grant", "bash", lsArgs, "jim", sessID, "turn-c4")
	require.True(t, accepted)

	w := postToolApprovalAs(t, api, "bob", entry.ApprovalID, "always")
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, grants.IsAllowed(sessID, "jim", "bash", lsArgs),
		"a refused cross-account 'always' must not record a grant")

	w = postToolApprovalAs(t, api, "alice", entry.ApprovalID, "always")
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		GrantRecorded *bool `json:"grant_recorded"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.GrantRecorded)
	assert.True(t, *resp.GrantRecorded, "the owner's 'always' must stick")
	assert.True(t, grants.IsAllowed(sessID, "jim", "bash", lsArgs))
}

// TestToolApproval_DecisionDoorOwnerlessAndIdentitylessWiden mirrors the WS
// side's exact widenings (approvalVisibleTo): an approval whose owner cannot
// be resolved may be decided by any account (an invisible approval is a hung
// turn), and a caller with no account identity (env-token / dev-bypass) may
// decide an owned approval (there is no account to scope by).
func TestToolApproval_DecisionDoorOwnerlessAndIdentitylessWiden(t *testing.T) {
	t.Run("ownerless approval is decidable by any account", func(t *testing.T) {
		api, reg := newOwnerScopeAPI(t)
		sessID := ownedSession(t, api, "") // no Owner stamped on the meta
		entry, accepted := reg.requestApproval("tc-c4-noowner", "bash", map[string]any{}, "jim", sessID, "t")
		require.True(t, accepted)
		w := postToolApprovalAs(t, api, "bob", entry.ApprovalID, "deny")
		assert.Equal(t, http.StatusOK, w.Code,
			"owner-unknown widens to decidable-by-anyone on the WS side; the door must match")
	})

	t.Run("identity-less caller may decide an owned approval", func(t *testing.T) {
		api, reg := newOwnerScopeAPI(t)
		sessID := ownedSession(t, api, "alice")
		entry, accepted := reg.requestApproval("tc-c4-noident", "bash", map[string]any{}, "jim", sessID, "t")
		require.True(t, accepted)
		w := postToolApprovalAs(t, api, "", entry.ApprovalID, "approve")
		assert.Equal(t, http.StatusOK, w.Code,
			"a caller with no account identity widens to may-decide on the WS side; the door must match")
	})
}
