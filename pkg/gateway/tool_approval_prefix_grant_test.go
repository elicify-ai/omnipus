// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ADR-092 D4: "Allow" with scope=prefix on a bash approval records a prefix
// grant, so a later call with the same program and leading words skips the
// prompt, while an unrelated call does not.

func postScopedApproval(t *testing.T, api *restAPI, approvalID, scope string) map[string]any {
	t.Helper()
	body := `{"action":"allow","scope":"` + scope + `"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tool-approvals/"+approvalID, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleToolApprovals(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

func approveBash(t *testing.T, command, scope string) (*restAPI, map[string]any) {
	t.Helper()
	api, reg := newTestRestAPIWithApprovalReg(t)
	entry, accepted := reg.requestApproval("tc-prefix", "bash",
		map[string]any{"command": command}, "agent-p", "sess-p", "turn-p")
	require.True(t, accepted)
	go func() { <-entry.resultCh }()
	return api, postScopedApproval(t, api, entry.ApprovalID, scope)
}

func prefixCovers(api *restAPI, command string) bool {
	return tools.BashPrefixGrantCheck(api.agentLoop.ApprovalGrants(), "sess-p", "agent-p", "bash",
		map[string]any{"command": command})
}

func TestToolApproval_PrefixScopeRecordsPrefixGrant(t *testing.T) {
	api, resp := approveBash(t, "echo hello world", "prefix")
	assert.Equal(t, "prefix", resp["scope"])
	assert.Equal(t, true, resp["grant_recorded"])

	assert.True(t, prefixCovers(api, "echo hello world again"), "same program + leading words must be covered")
	assert.False(t, prefixCovers(api, "echo hello"), "a shorter argument list is not covered")
	assert.False(t, prefixCovers(api, "echo hello worldwide"), "token-boundary match (FR-024)")
}

func TestToolApproval_ExactScopeRecordsNoPrefixGrant(t *testing.T) {
	api, resp := approveBash(t, "echo hello world", "exact")
	assert.Equal(t, "exact", resp["scope"])
	assert.False(t, prefixCovers(api, "echo hello world again"))
}

func TestToolApproval_PrefixFallsBackToExactWhenUnsafe(t *testing.T) {
	for _, cmd := range []string{
		"echo hello && echo world", // chained: a prefix would span into the next command
		"echo",                     // bare program: a prefix would approve every use
		"sudo echo hi",             // wrapper: a prefix would approve anything run through it
	} {
		t.Run(cmd, func(t *testing.T) {
			api, resp := approveBash(t, cmd, "prefix")
			assert.Equal(t, "exact", resp["scope"], "the response must report the scope actually recorded")
			assert.True(t, api.agentLoop.ApprovalGrants().IsAllowed("sess-p", "agent-p", "bash",
				map[string]any{"command": cmd}), "the exact grant must still be recorded")
		})
	}
}
