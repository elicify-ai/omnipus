// shell_permission_gate_test.go: functional proof for ShellPermissionGate
// (loop_policy.go) — the ADR-092 D1/FR-039 adapter connecting pkg/tools'
// bash-tool enforcement to AgentLoop's existing mode-resolution and
// grant-consultation primitives, and for the CheckGrantOrRequestApproval
// bash-prefix-grant extension (also loop_policy.go, this lane's own file).

package agent

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestShellPermissionGate_ResolveShellMode_GlobalOnly(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	cfg.Sandbox.ToolPolicies = map[string]string{"bash": "allow"}

	gate := &ShellPermissionGate{Loop: al}
	got := gate.ResolveShellMode(context.Background(), "some-agent", "some-session")
	assert.Equal(t, tools.ShellModeAuto, got, "bash=allow, no GodMode -> Auto")
}

func TestShellPermissionGate_ResolveShellMode_GodMode(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	cfg.Sandbox.ToolPolicies = map[string]string{"bash": "allow"}
	cfg.Sandbox.GodMode = true

	gate := &ShellPermissionGate{Loop: al}
	got := gate.ResolveShellMode(context.Background(), "some-agent", "some-session")
	assert.Equal(t, tools.ShellModeGod, got)
}

func TestShellPermissionGate_ResolveShellMode_AskWhenBashPolicyNotAllow(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	cfg.Sandbox.ToolPolicies = map[string]string{"bash": "ask"}

	gate := &ShellPermissionGate{Loop: al}
	got := gate.ResolveShellMode(context.Background(), "some-agent", "some-session")
	assert.Equal(t, tools.ShellModeAsk, got)
}

func TestShellPermissionGate_ResolveShellMode_AgentOverrideTightensAuto(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	cfg.Sandbox.ToolPolicies = map[string]string{"bash": "allow"}
	agentID := "tightened-agent"
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID: agentID,
		Tools: &config.AgentToolsCfg{
			Builtin: config.AgentBuiltinToolsCfg{
				Policies: map[string]config.ToolPolicy{"bash": config.ToolPolicyAsk},
			},
		},
	})

	gate := &ShellPermissionGate{Loop: al}
	got := gate.ResolveShellMode(context.Background(), agentID, "some-session")
	assert.Equal(t, tools.ShellModeAsk, got, "the agent's own stricter bash override must tighten the global Auto default")
}

func TestShellPermissionGate_ResolveShellMode_ChatModifierTightens(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	cfg.Sandbox.ToolPolicies = map[string]string{"bash": "allow"}

	store := NewSessionModeStore()
	store.Set("chat-session", ShellModeAsk)
	gate := &ShellPermissionGate{Loop: al, ModeStore: store}

	got := gate.ResolveShellMode(context.Background(), "some-agent", "chat-session")
	assert.Equal(t, tools.ShellModeAsk, got, "the per-chat modifier must tighten the global Auto default")
}

func TestShellPermissionGate_ResolveShellMode_NilReceiverFailsClosed(t *testing.T) {
	var gate *ShellPermissionGate
	got := gate.ResolveShellMode(context.Background(), "a", "s")
	assert.Equal(t, tools.ShellModeAsk, got)

	gate2 := &ShellPermissionGate{} // Loop is nil
	got2 := gate2.ResolveShellMode(context.Background(), "a", "s")
	assert.Equal(t, tools.ShellModeAsk, got2)
}

// TestShellPermissionGate_RequestShellApproval_ReachesCheckGrantOrRequestApproval
// proves the FR-039 forwarding: an existing exact grant short-circuits
// through the SAME consultation function the classic ask-policy path uses.
func TestShellPermissionGate_RequestShellApproval_ReachesCheckGrantOrRequestApproval(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	grants := security.NewApprovalGrantStore()
	require.True(t, grants.Record("sess-1", "agent-1", "bash", map[string]any{"command": "true"}))
	al.approvalGrants = grants

	gate := &ShellPermissionGate{Loop: al}
	approved, reason := gate.RequestShellApproval(
		context.Background(), "sess-1", "agent-1", "bash", "call-1", "turn-1",
		map[string]any{"command": "true"})
	assert.True(t, approved)
	assert.Empty(t, reason)
}

func TestShellPermissionGate_RequestShellApproval_NilReceiverFailsClosed(t *testing.T) {
	var gate *ShellPermissionGate
	approved, reason := gate.RequestShellApproval(context.Background(), "s", "a", "bash", "c", "t", nil)
	assert.False(t, approved)
	assert.NotEmpty(t, reason)
}

// --- CheckGrantOrRequestApproval's bash prefix-grant extension ---

func TestCheckGrantOrRequestApproval_BashPrefixGrantSuppressesPrompt(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	// Resolve "true" the same way tools.BashPrefixGrantCheck will, so the
	// grant is recorded against the REAL resolved binary path (D3's own
	// resolve-and-verify discipline), not the bare name.
	resolvedTrue, argWords, runInBackground, ok := bashPrefixMatchInputsForTest(t, "true")
	require.True(t, ok)
	require.Empty(t, argWords)
	require.False(t, runInBackground)

	grants := security.NewApprovalGrantStore()
	require.True(t, grants.RecordPrefixGrant("sess-1", "agent-1", "bash", security.ShellPrefixGrant{Binary: resolvedTrue}))
	al.approvalGrants = grants

	approved, reason := al.CheckGrantOrRequestApproval(
		context.Background(), "sess-1", "agent-1", "bash", "call-1", "turn-1",
		map[string]any{"command": "true"})
	assert.True(t, approved, "a D4 prefix grant must suppress the classic ask-policy dialog for a bash call")
	assert.Empty(t, reason)
}

func TestCheckGrantOrRequestApproval_BashPrefixGrantDoesNotApplyToOtherTools(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	grants := security.NewApprovalGrantStore()
	grants.RecordPrefixGrant("sess-1", "agent-1", "bash", security.ShellPrefixGrant{Binary: "/bin/true"})
	al.approvalGrants = grants

	// No PolicyApprover wired -> nopPolicyApprover denies with
	// "no_approver_configured", proving this reaches the interactive
	// fallback rather than being spuriously approved by the bash-only check.
	approved, reason := al.CheckGrantOrRequestApproval(
		context.Background(), "sess-1", "agent-1", "some_other_tool", "call-1", "turn-1",
		map[string]any{"command": "true"})
	assert.False(t, approved, "the bash prefix-grant extension must never apply to a non-bash tool")
	assert.Equal(t, "no_approver_configured", reason)
}

// bashPrefixMatchInputsForTest resolves command's head the SAME way
// tools.BashPrefixGrantCheck's own unexported resolver does
// (shellrule.ResolveBinary against os.Getenv("PATH")) so a grant recorded
// against the returned resolvedBinary is guaranteed to match on the read
// side — proven below via a probe call through the real, exported
// tools.BashPrefixGrantCheck rather than assumed.
func bashPrefixMatchInputsForTest(t *testing.T, command string) (resolvedBinary string, argWords []string, runInBackground bool, ok bool) {
	t.Helper()
	resolved, err := shellrule.ResolveBinary(command, os.Getenv("PATH"))
	if err != nil {
		return "", nil, false, false
	}
	probe := security.NewApprovalGrantStore()
	probe.RecordPrefixGrant("s", "a", "bash", security.ShellPrefixGrant{Binary: resolved})
	matched := tools.BashPrefixGrantCheck(probe, "s", "a", "bash", map[string]any{"command": command})
	return resolved, nil, false, matched
}
