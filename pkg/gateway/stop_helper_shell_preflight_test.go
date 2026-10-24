package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Oracle: round-2 requirement 2 / pr-test-analyzer F2. A tool-internal
// pre-flight approval must cancel the real waiting execution, not merely
// refuse the command and let the scripted model report ordinary completion.
func TestStopHelperShellPreflight_CancelsAndRetiresExecution(t *testing.T) {
	f, canary, expected := newStopShellPreflightFixture(t)
	parent := f.newChat(t)
	f.startParent(t, parent)
	sub := f.al.SubscribeEvents(128)
	t.Cleanup(func() { f.al.UnsubscribeEvents(sub.ID) })
	child := f.launch(t, parent, "Run the harmless shell pre-flight probe once.")
	entry := awaitStopShellPreflight(t, f, child)
	var observedTurn string
	deadline := time.NewTimer(cancelTestTurnStartDeadline)
	defer deadline.Stop()
	for observedTurn == "" {
		select {
		case event := <-sub.C:
			if event.Kind == agent.EventKindTurnStart && event.Meta.SessionKey == child {
				observedTurn = event.Meta.TurnID
			}
		case <-deadline.C:
			t.Fatal("real helper turn_start never reached independent observer")
		}
	}
	require.NotEmpty(t, observedTurn)
	require.Equal(t, observedTurn, entry.TurnID, "pre-flight must retain its actual execution/turn identity")
	_, err := os.Stat(canary)
	require.ErrorIs(t, err, os.ErrNotExist, "the real shell must not start before approval")
	f.drainFrames(t)
	data, err := json.Marshal(generated.CancelFrame{Type: "cancel", SessionId: parent})
	require.NoError(t, err)
	reader := &wsHandlerReadLoop{h: f.h, wc: f.wc, ctx: context.Background()}
	require.Equal(t, wsHandlerReadLoopNext, reader.dispatchFrame(data, wsTypeOnly{Type: "cancel"}))
	require.Equal(t, ApprovalStateDeniedCancel, f.state(entry.ApprovalID))
	var settled *session.LifecycleRecord
	require.Eventually(t, func() bool {
		var loadErr error
		settled, loadErr = f.al.GetSessionLifecycleStore().Load(child)
		return loadErr == nil && (settled.State == session.LifecycleStopped || settled.Terminal()) && f.al.GetActiveTurnBySession(child) == nil
	}, cancelTestTurnStartDeadline, 10*time.Millisecond, "pre-flight execution must settle and retire")
	require.Equal(t, session.LifecycleStopped, settled.State, "a cancelled pre-flight must not complete as ordinary work")
	require.Nil(t, f.al.GetActiveTurnBySession(child), "pre-flight helper execution must actually retire")
	_, err = os.Stat(canary)
	require.ErrorIs(t, err, os.ErrNotExist, "cancelled shell command must never execute")
	var resolved []generated.ToolApprovalResolvedFrame
	for _, frame := range f.drainFrames(t) {
		if frame.ApprovalId == entry.ApprovalID {
			resolved = append(resolved, frame)
		}
	}
	require.Len(t, resolved, 1)
	require.Equal(t, "denied_cancel", resolved[0].State)
	late, gone := f.reg.resolve(entry.ApprovalID, ApprovalActionApprove, true)
	require.False(t, late)
	require.True(t, gone)

	// Positive control: the exact real command can run after explicit approval
	// in another chat. File absence above is not a broken shell/target fixture.
	other := f.newChat(t)
	control := f.launch(t, other, "Run the same harmless shell pre-flight probe once.")
	controlEntry := awaitStopShellPreflight(t, f, control)
	approved, gone := f.reg.resolve(controlEntry.ApprovalID, ApprovalActionApprove, false)
	require.True(t, approved)
	require.False(t, gone)
	f.awaitState(t, control, session.LifecycleCompleted)
	content, err := os.ReadFile(canary)
	require.NoError(t, err)
	require.Equal(t, expected, string(content))
}

type stopShellPreflightProvider struct {
	parent  *stopHelperApprovalProvider
	command string
}

func (p *stopShellPreflightProvider) GetDefaultModel() string { return "stop-shell-preflight" }

func (p *stopShellPreflightProvider) Chat(ctx context.Context, msgs []providers.Message, defs []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	for _, msg := range msgs {
		if msg.Role == "user" && strings.Contains(msg.Content, "WAIT:parent") {
			return p.parent.Chat(ctx, msgs, defs, model, options)
		}
	}
	return (&adr092TurnIDProvider{command: p.command}).Chat(ctx, msgs, defs, model, options)
}

func newStopShellPreflightFixture(t *testing.T) (*stopHelperApprovalFixture, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	canary := filepath.Join(t.TempDir(), "preflight-canary.txt")
	const marker = "shell-preflight-canary"
	command := "printf '%s\\n' '" + marker + "' > '" + strings.ReplaceAll(canary, "'", "'\\''") + "'"
	expected := marker + "\n"
	if runtime.GOOS == "windows" {
		command = "echo " + marker + ">\"" + canary + "\""
		expected = marker + "\r\n"
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "stop-shell-preflight"}, MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: "mia", Home: home}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 4, MaxDelegationDepth: 2},
		Sandbox:     config.OmnipusSandboxConfig{Mode: config.SandboxModeOff, AutoApprove: true, ToolPolicies: map[string]string{"bash": "ask"}},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	parent := &stopHelperApprovalProvider{entered: make(chan context.Context, 16), release: make(chan struct{}), finishHelper: make(chan struct{})}
	al := mustAgentLoop(t, cfg, msgBus, &stopShellPreflightProvider{parent: parent, command: command})
	lifecycle := al.GetSessionLifecycleStore()
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel))
	t.Cleanup(func() { gatewaySteerCancellers.Delete(al) })
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(agent.NewSteerRecordClassifier(lifecycle, al.GetSessionStore())),
		steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	h := newWSHandler(msgBus, al, "")
	wc, _ := makeForwarderTestConn(256)
	wc.userID = "stop-shell-owner"
	h.sessions[wc.userID] = wc
	reg := newApprovalRegistryV2(64, 10*time.Minute)
	h.approvalRegV2 = reg
	reg.setResolutionListener(h.broadcastToolApprovalResolved)
	al.SetToolApprover(newPolicyApproverAdapter(reg, h))
	t.Cleanup(func() { close(parent.release); reg.cancelAllPendingForRestart() })
	workDir, err := workspace.EnsureWorkDir(home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	require.False(t, strings.HasPrefix(canary, workDir+string(filepath.Separator)), "probe must really be outside the workspace")
	return &stopHelperApprovalFixture{al: al, h: h, wc: wc, reg: reg, provider: parent, workDir: workDir}, canary, expected
}

func awaitStopShellPreflight(t *testing.T, f *stopHelperApprovalFixture, id string) *approvalEntry {
	t.Helper()
	var found *approvalEntry
	require.Eventually(t, func() bool {
		for _, entry := range f.reg.pendingApprovals() {
			if entry.SessionID == id {
				found = entry
				return true
			}
		}
		return false
	}, cancelTestTurnStartDeadline, 10*time.Millisecond, "real bash pre-flight must raise an approval")
	require.Equal(t, "bash", found.ToolName)
	require.Equal(t, "fs_preflight", found.Args["adr092_kind"], "must be a tool-internal filesystem pre-flight, not a classic bash Ask")
	return found
}
