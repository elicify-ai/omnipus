package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// A delegated helper inherits its parent's per-chat Auto-approve modifier
// with no per-agent exception (#1221). These tests pin the boundaries that
// inherited Auto must NOT cross.

// An unattended run (AutoDenyAsk: nobody to answer a prompt) whose session
// inherited an Auto-on modifier still runs what Auto may run, and still
// auto-DENIES — without consulting any approver — a call Auto classifies as
// needing a human.
func TestInheritedAutoApprove_UnattendedRunStillAutoDeniesAsks(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCalls([]providers.ToolCall{
			autoToolCall("u-run", "knowledge_edit", `{}`),
			autoToolCall("u-ask", "delete_task", `{}`),
		}).
		WithText("done")
	al := newAutoTestLoop(t, provider, false, nil) // global Auto OFF
	stubs := installAutoStubs(t, al, "mia", []string{"knowledge_edit", "delete_task"})
	approver := &autoRecordingApprover{approve: true} // would approve anything it were asked
	al.SetToolApprover(approver)

	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err)
	// The session's Auto-on modifier arrives the way a delegate's does.
	const parentSession = "unattended-parent"
	al.SessionModes().Set(parentSession, true)
	al.inheritSessionPermissions(parentSession, "mia", meta.ID, "mia")

	_, err = al.ProcessScheduled(context.Background(), "mia", meta.ID, "go", "scheduled", meta.ID)
	require.NoError(t, err)

	assert.Equal(t, int32(1), stubs["knowledge_edit"].calls.Load(),
		"the inherited Auto-on modifier must still run an Auto-RUNS tool unprompted")
	assert.Zero(t, stubs["delete_task"].calls.Load(), "an ask Auto cannot clear must be denied in an unattended run")
	assert.Zero(t, approver.countFor("delete_task"), "auto-deny must short-circuit before any approver is consulted")
}

// God Mode has no Auto machinery: an inherited Auto-on modifier changes
// nothing for a delegate — Auto is inactive and bash resolves God, exactly as
// for a non-delegate. The control (God Mode off) proves the same setup does
// turn Auto on, so the assertion can fail.
func TestInheritedAutoApprove_GodModeDelegateUnaffected(t *testing.T) {
	const parentSession, childSession = "god-parent", "god-child"
	for _, tc := range []struct {
		name     string
		godMode  bool
		wantAuto bool
		wantMode tools.ShellMode
	}{
		{"god mode off: inherited modifier turns Auto on", false, true, tools.ShellModeAuto},
		{"god mode on: Auto inactive, bash is God", true, false, tools.ShellModeGod},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withKernelSandbox(t)
			withGodModeAvailable(t, true)
			al := newGateTestLoopCfg(t, func(c *config.Config) {
				c.Agents.List = append(c.Agents.List, config.AgentConfig{ID: "worker"})
				c.Sandbox.ToolPolicies = map[string]string{"bash": "ask"}
				c.Sandbox.GodMode = tc.godMode
			})
			al.SessionModes().Set(parentSession, true)
			al.inheritSessionPermissions(parentSession, testDefaultAgentID, childSession, "worker")

			assert.Equal(t, tc.wantAuto, al.autoApproveActive(childSession))
			assert.Equal(t, tc.wantMode, al.shellGate.ResolveShellMode(context.Background(), "worker", childSession))
		})
	}
}

// A helper's own explicit per-agent policy is a floor under inheritance: a
// per-agent deny stays denied, and a per-agent ask on a tool Auto classifies
// as needing a human still prompts, even with the parent's Auto-on modifier.
func TestInheritedAutoApprove_HelperOwnPolicyStillBinds(t *testing.T) {
	t.Run("per-agent deny stays denied", func(t *testing.T) {
		approver, stub := delegateToolUnderAutoChat(t, "knowledge_edit", config.ToolPolicyDeny)
		assert.Zero(t, stub.calls.Load(), "a per-agent deny must not run under inherited Auto")
		assert.Zero(t, approver.countFor("knowledge_edit"), "a denied tool never reaches the approver")
	})
	t.Run("per-agent ask on an always-ask tool still prompts", func(t *testing.T) {
		approver, stub := delegateToolUnderAutoChat(t, "delete_task", config.ToolPolicyAsk)
		assert.Equal(t, 1, approver.countFor("delete_task"), "Auto cannot clear delete_task: it must prompt")
		assert.Zero(t, stub.calls.Load(), "the prompt was denied, so the tool did not run")
	})
}
