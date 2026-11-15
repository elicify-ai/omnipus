// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR-092 D4: shell forms record exact consent, including the working folder.
// This calls the real recorder, store and approval gate; no script is run.
func TestToolApproval_ShellExactGrantWorkingFolder(t *testing.T) {
	cases := []struct {
		name, command string
		shell         bool
	}{
		{"plain_script", "sh sample.sh", true},
		{"folded_script", "ſh sample.sh", true},
		{"plain_inline", `sh -c "printf q43-control"`, true},
		{"folded_inline", `ſh -c "printf q43-control"`, true},
		{"ordinary_prefix_control", "reviewer sample.sh", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api, approver, workspace := t11ShellGrantFixture(t)
			first := map[string]any{"command": c.command, "cwd": "folder-a", "run_in_background": false}
			later := map[string]any{"command": c.command, "cwd": "folder-b", "run_in_background": false}
			wantPrefix := !c.shell && runtime.GOOS != "windows"
			wantScope := gen.ToolApprovalResponseScopeExact
			if wantPrefix {
				wantScope = gen.ToolApprovalResponseScopePrefix
			}
			entry := &approvalEntry{AgentID: "agent-a", ToolName: "bash", Args: first}
			recordGrant, scope := api.approvalGrantRecorder(entry, gen.ToolApprovalActionRequestScopePrefix)
			recorded := recordGrant("session-a")
			grants := api.agentLoop.ApprovalGrants()
			exactFirst := grants.IsAllowed("session-a", "agent-a", "bash", first)
			exactLater := grants.IsAllowed("session-a", "agent-a", "bash", later)
			prefixFirst := tools.BashPrefixGrantCheck(grants, "session-a", "agent-a", "bash", first)
			prefixLater := tools.BashPrefixGrantCheck(grants, "session-a", "agent-a", "bash", later)
			sameApproved, sameReason, sameRecord := api.agentLoop.CheckGrantOrRequestApproval(
				context.Background(), "session-a", "agent-a", "bash", "same-call", "turn-a", first)
			sameCalls := approver.callCount()
			laterApproved, laterReason, laterRecord := api.agentLoop.CheckGrantOrRequestApproval(
				context.Background(), "session-a", "agent-a", "bash", "changed-folder", "turn-a", later)
			record, err := json.Marshal(map[string]any{
				"name": c.name, "first_args": first, "later_args": later, "workspace": workspace,
				"want_scope": wantScope, "scope": scope, "recorded": recorded, "want_prefix": wantPrefix,
				"exact_first": exactFirst, "exact_later": exactLater, "prefix_first": prefixFirst, "prefix_later": prefixLater,
				"same_approved": sameApproved, "same_reason": sameReason, "same_record": sameRecord, "same_calls": sameCalls,
				"later_approved": laterApproved, "later_reason": laterReason, "later_record": laterRecord, "approval_calls": approver.callCount(),
			})
			require.NoError(t, err, "fixture: record actual scope, storage and approval-gate decisions before assertions")
			t.Logf("SHELL_GRANT_REUSE_CASE %s", record)
			require.True(t, recorded, "grant scope: a successful approval must actually be remembered")
			assert.Equal(t, wantScope, scope, "grant scope: recognized shell forms must fall back to exact consent")
			assert.Equal(t, !wantPrefix, exactFirst, "grant scope: exact storage must agree with the reported scope")
			assert.False(t, exactLater, "exact consent: the changed working-folder argument must not match")
			assert.Equal(t, wantPrefix, prefixFirst, "grant scope: no prefix grant may be recorded for a shell form")
			assert.Equal(t, wantPrefix, prefixLater, "grant scope: shell consent cannot cover another working folder")
			require.True(t, sameApproved, "same-call control: unchanged consent is reusable")
			require.Equal(t, "", sameReason, "same-call control: no denial reason")
			require.False(t, sameRecord, "same-call control: reusing consent does not request a new grant")
			require.Equal(t, 0, sameCalls, "same-call control: no fresh approval is needed")
			assert.Equal(t, wantPrefix, laterApproved, "exact consent: a different working folder must ask again")
			require.False(t, laterRecord, "the test approver makes no new persistent approval")
			if wantPrefix {
				require.Equal(t, "", laterReason, "ordinary prefix control: reuse stays prompt-free")
				require.Equal(t, 0, approver.callCount(), "ordinary prefix control: safe prefix reuse ignores the working folder")
			} else {
				assert.Equal(t, "test approval required", laterReason, "exact consent: use the fresh approver's response")
				require.Equal(t, 1, approver.callCount(), "exact consent: reach the actual interactive approval boundary once")
				request := approver.calls[0]
				require.Equal(t, later, request.Args, "fresh approval must carry the changed working folder and command")
				require.Equal(t, "session-a", request.SessionID, "fresh approval: session identity")
				require.Equal(t, "agent-a", request.AgentID, "fresh approval: agent identity")
				require.Equal(t, "bash", request.ToolName, "fresh approval: tool identity")
				require.Equal(t, "changed-folder", request.ToolCallID, "fresh approval: call identity")
				require.Equal(t, "turn-a", request.TurnID, "fresh approval: turn identity")
			}
		})
	}
}

func t11ShellGrantFixture(t *testing.T) (*restAPI, *fakePolicyApprover, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	workspace := filepath.Join(home, "workspace")
	for _, folder := range []string{"folder-a", "folder-b"} {
		dir := filepath.Join(workspace, folder)
		require.NoError(t, os.MkdirAll(dir, 0o700), "fixture: create the permitted working folder")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "sample.sh"), []byte("printf q43-"+folder+"\n"), 0o600), "fixture: create harmless script text that is never executed")
	}
	first, err := os.ReadFile(filepath.Join(workspace, "folder-a", "sample.sh"))
	require.NoError(t, err, "fixture: read the first inert script")
	later, err := os.ReadFile(filepath.Join(workspace, "folder-b", "sample.sh"))
	require.NoError(t, err, "fixture: read the second inert script")
	require.NotEqual(t, first, later, "fixture: identical script basenames identify different test files")
	bin := filepath.Join(home, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o700), "fixture: create isolated PATH")
	for _, name := range []string{"sh", "ſh", "reviewer"} {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte("inert metadata; never executed\n"), 0o700), "fixture: create %q", name)
	}
	t.Setenv("PATH", bin)
	for _, name := range []string{"sh", "ſh", "reviewer"} {
		resolved, err := shellrule.ResolveBinary(name, bin)
		require.NoError(t, err, "fixture: %q must resolve before grant derivation", name)
		t.Logf("GRANT_RESOLVE name=%q path=%q", name, resolved)
	}
	cfg := minimalAgentLoopCfg(t, config.AgentConfig{ID: "agent-a", Tools: &config.AgentToolsCfg{
		Builtin: config.AgentBuiltinToolsCfg{Policies: map[string]config.ToolPolicy{"bash": config.ToolPolicyAsk}},
	}})
	cfg.Agents.Defaults.Home = workspace
	cfg.Sandbox.ToolPolicies = map[string]string{"bash": string(config.ToolPolicyAsk)}
	cfg.Sandbox.AutoApprove = false
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	require.Equal(t, "ask", al.ResolveApprovalToolPolicy("agent-a", "bash"), "fixture: grant consumption is exercised under Ask policy")
	approver := &fakePolicyApprover{approved: false, reason: "test approval required"}
	al.SetToolApprover(approver)
	return &restAPI{agentLoop: al}, approver, workspace
}
