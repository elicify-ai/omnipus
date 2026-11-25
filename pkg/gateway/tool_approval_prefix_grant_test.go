// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ADR-092 D4, as carried by skills-tools-redesign-spec.md C-26: "Allow for"
// with scope=prefix on a bash approval records a prefix allowance, so a later
// call with the same program and leading words skips the prompt, while an
// unrelated call does not. The retired "allow" action is refused with 400 (C-26,
// "Refusals on POST /tool-approvals/{approval_id}"); the request contract keeps
// `scope` and says it "now applies to `allow_for` on a bash call" (C-26 contract
// table, ToolApprovalActionRequest). The answer is therefore allow_for with a
// duration; 1h is the shortest of the three listed lengths (C-26, "Lifetime
// and scope", Length). These tests assert the recorded scope and what it
// covers, not the length.

func postScopedApproval(t *testing.T, api *restAPI, approvalID, scope string) map[string]any {
	t.Helper()
	body := `{"action":"allow_for","duration":"1h","scope":"` + scope + `"}`
	w := postToolApprovalRaw(t, api, approvalID, body)
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
