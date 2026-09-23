// shell_permission_gate_test.go: functional proof for ShellPermissionGate
// (loop_policy.go) — the ADR-092 D1/FR-039 adapter connecting pkg/tools'
// bash-tool enforcement to AgentLoop's existing mode-resolution and
// grant-consultation primitives, and for the CheckGrantOrRequestApproval
// bash-prefix-grant extension (also loop_policy.go, this lane's own file).

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newGateTestLoop builds an AgentLoop through NewAgentLoop with the bash
// ceiling and Auto-approve settings the case needs set BEFORE construction,
// because ResolveApprovalToolPolicy reads the policy snapshot each agent
// instance takes at construction time.
func newGateTestLoop(t *testing.T, bashPolicy string, autoApprove bool, agents ...config.AgentConfig) *AgentLoop {
	t.Helper()
	return newGateTestLoopCfg(t, func(c *config.Config) {
		c.Agents.List = append(c.Agents.List, agents...)
		c.Sandbox.ToolPolicies = map[string]string{"bash": bashPolicy}
		c.Sandbox.AutoApprove = autoApprove
	})
}

// newGateTestLoopCfg builds a loop through NewAgentLoop after mutate has
// shaped the config.
func newGateTestLoopCfg(t *testing.T, mutate func(*config.Config)) *AgentLoop {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	cfg := &config.Config{}
	cfg.Agents.Defaults = config.AgentDefaults{
		Home:              home,
		DefaultModel:      config.DefaultModel{Model: "test-model"},
		MaxTokens:         4096,
		MaxToolIterations: 10,
	}
	cfg.Agents.List = []config.AgentConfig{{ID: testDefaultAgentID, Home: home}}
	mutate(cfg)
	return mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
}

// withKernelSandbox registers a per-turn kernel policy base for the test's
// duration — the predicate Auto needs (FR-008) — and removes it afterwards.
func withKernelSandbox(t *testing.T) {
	t.Helper()
	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: t.TempDir()})
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })
}

func TestShellPermissionGate_LiveMode(t *testing.T) {
	ctx := context.Background()

	t.Run("ask policy + Auto-approve on + kernel sandbox resolves Auto", func(t *testing.T) {
		withKernelSandbox(t)
		al := newGateTestLoop(t, "ask", true)
		assert.Equal(t, tools.ShellModeAuto, al.shellGate.ResolveShellMode(ctx, testDefaultAgentID, "sess"))
	})
	t.Run("no kernel sandbox degrades Auto to Ask (FR-008)", func(t *testing.T) {
		sandbox.RegisterTurnPolicyBase(nil)
		al := newGateTestLoop(t, "ask", true)
		assert.Equal(t, tools.ShellModeAsk, al.shellGate.ResolveShellMode(ctx, testDefaultAgentID, "sess"))
	})
	t.Run("global Auto-approve off resolves Ask", func(t *testing.T) {
		withKernelSandbox(t)
		al := newGateTestLoop(t, "ask", false)
		assert.Equal(t, tools.ShellModeAsk, al.shellGate.ResolveShellMode(ctx, testDefaultAgentID, "sess"))
	})
	t.Run("per-agent off-switch resolves Ask", func(t *testing.T) {
		withKernelSandbox(t)
		al := newGateTestLoop(t, "ask", true, config.AgentConfig{ID: "jim", AutoApproveDisabled: true})
		assert.Equal(t, tools.ShellModeAsk, al.shellGate.ResolveShellMode(ctx, "jim", "sess"))
		assert.Equal(t, tools.ShellModeAuto, al.shellGate.ResolveShellMode(ctx, testDefaultAgentID, "sess"),
			"another agent still follows the global default")
	})
	t.Run("per-chat on loosens past a global off", func(t *testing.T) {
		withKernelSandbox(t)
		al := newGateTestLoop(t, "ask", false)
		al.SessionModes().Set("chat-on", true)
		assert.Equal(t, tools.ShellModeAuto, al.shellGate.ResolveShellMode(ctx, testDefaultAgentID, "chat-on"))
		assert.Equal(t, tools.ShellModeAsk, al.shellGate.ResolveShellMode(ctx, testDefaultAgentID, "other-chat"))
	})
	t.Run("per-chat off tightens a global on", func(t *testing.T) {
		withKernelSandbox(t)
		al := newGateTestLoop(t, "ask", true)
		al.SessionModes().Set("chat-off", false)
		assert.Equal(t, tools.ShellModeAsk, al.shellGate.ResolveShellMode(ctx, testDefaultAgentID, "chat-off"))
	})
	t.Run("allow policy never gets the Auto machinery", func(t *testing.T) {
		withKernelSandbox(t)
		al := newGateTestLoop(t, "allow", true)
		assert.Equal(t, tools.ShellModeAsk, al.shellGate.ResolveShellMode(ctx, testDefaultAgentID, "sess"))
	})
	t.Run("a mode pinned by the loop wins over the live value", func(t *testing.T) {
		withKernelSandbox(t)
		al := newGateTestLoop(t, "ask", true)
		pinned := withPinnedShellMode(ctx, tools.ShellModeAsk)
		assert.Equal(t, tools.ShellModeAsk, al.shellGate.ResolveShellMode(pinned, testDefaultAgentID, "sess"))
	})
}

func TestShellPermissionGate_ResolveShellMode_NilReceiverFailsClosed(t *testing.T) {
	var gate *ShellPermissionGate
	assert.Equal(t, tools.ShellModeAsk, gate.ResolveShellMode(context.Background(), "a", "s"))
	assert.Equal(t, tools.ShellModeAsk, (&ShellPermissionGate{}).ResolveShellMode(context.Background(), "a", "s"))
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
