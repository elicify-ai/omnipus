package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Oracle: Train 3 UAT row 9.3 and the Stop-helper failure dispatch. The
// approval is terminal denied_cancel; the cancelled execution settles stopped.
// A stopped session itself stays resumable, per the existing control-plane D2.
// Only the model is scripted: launch, Ask, file writes, Stop and frames are real.
func TestStopOwningChat_CancelsHelperApprovals(t *testing.T) {
	f := newStopHelperApprovalFixture(t)
	parent := f.newChat(t)
	siblingChat := f.newChat(t)
	parentCtx, parentDone := f.startParent(t, parent)
	child := f.launch(t, parent, "ASK:child.txt")
	childApproval := f.awaitApproval(t, child)
	grandchild := f.launch(t, child, "ASK:grandchild.txt")
	grandchildApproval := f.awaitApproval(t, grandchild)
	sibling := f.launch(t, siblingChat, "ASK:sibling.txt")
	siblingApproval := f.awaitApproval(t, sibling)
	working := f.launch(t, parent, "WAIT:helper")
	workingCtx := f.awaitProvider(t)

	// Before Stop the card really belongs to this parent, not the acting
	// helper; the sibling uses the SAME agent/tool, so ID isolation matters.
	for _, entry := range []*approvalEntry{childApproval, grandchildApproval} {
		require.Equal(t, parent, f.h.approvalSessionContext(entry.SessionID).RootSessionID)
		require.Equal(t, ApprovalStatePending, f.state(entry.ApprovalID))
	}
	require.Equal(t, siblingChat, f.h.approvalSessionContext(sibling).RootSessionID)
	f.drainFrames(t)

	// Omitted scope is the ordinary first Stop, not Stop all. Exercise the
	// production frame dispatcher all the way into StopSession/RequestCancel.
	data, err := json.Marshal(generated.CancelFrame{Type: "cancel", SessionId: parent})
	require.NoError(t, err)
	reader := &wsHandlerReadLoop{h: f.h, wc: f.wc, ctx: context.Background()}
	require.Equal(t, wsHandlerReadLoopNext, reader.dispatchFrame(data, wsTypeOnly{Type: "cancel"}))

	for _, entry := range []*approvalEntry{childApproval, grandchildApproval} {
		assert.Equal(t, ApprovalStateDeniedCancel, f.state(entry.ApprovalID),
			"Stop in the owning chat must cancel the pending Ask of helper %s", entry.SessionID)
	}
	require.Equal(t, ApprovalStatePending, f.state(siblingApproval.ApprovalID),
		"Stop must not consume another chat's approval, even for the same agent and tool")
	require.NoError(t, workingCtx.Err(), "plain Stop must not become Stop all for a helper without a pending approval")
	require.Equal(t, context.Canceled, parentCtx.Err(), "the owning chat's real running turn must be interrupted")
	select {
	case <-parentDone:
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("owning chat's cancelled turn did not exit")
	}
	if t.Failed() {
		return // Preserve the immediate pending-vs-denied failure, not a later wait deadline.
	}

	for _, id := range []string{child, grandchild} {
		f.awaitState(t, id, session.LifecycleStopped)
		require.Nil(t, f.al.GetActiveTurnBySession(id), "cancelled helper must retire its real turn")
	}
	resolved := f.drainFrames(t)
	for _, entry := range []*approvalEntry{childApproval, grandchildApproval} {
		var matching []generated.ToolApprovalResolvedFrame
		for _, frame := range resolved {
			if frame.ApprovalId == entry.ApprovalID {
				matching = append(matching, frame)
			}
		}
		require.Len(t, matching, 1, "the owning approval card must receive exactly one resolution")
		assert.Equal(t, "denied_cancel", matching[0].State)
		require.NotNil(t, matching[0].SessionId)
		assert.Equal(t, entry.SessionID, *matching[0].SessionId)
		require.NotNil(t, matching[0].RootSessionId)
		assert.Equal(t, parent, *matching[0].RootSessionId)
	}
	for _, frame := range resolved {
		assert.NotEqual(t, siblingApproval.ApprovalID, frame.ApprovalId, "unrelated card must not resolve")
	}
	for _, name := range []string{"child.txt", "grandchild.txt", "sibling.txt"} {
		_, statErr := os.Stat(filepath.Join(f.workDir, name))
		require.ErrorIs(t, statErr, os.ErrNotExist, "unapproved write must never execute: %s", name)
	}

	// Positive control: this very Ask gate can execute the real write_file.
	// The unrelated approval is still usable after the other chat was stopped.
	ok, gone := f.reg.resolve(siblingApproval.ApprovalID, ApprovalActionApprove, false)
	require.True(t, ok)
	require.False(t, gone)
	f.awaitState(t, sibling, session.LifecycleCompleted)
	content, err := os.ReadFile(filepath.Join(f.workDir, "sibling.txt"))
	require.NoError(t, err)
	assert.Equal(t, "approved helper content\n", string(content))
	assert.Equal(t, ApprovalStateApproved, f.state(siblingApproval.ApprovalID))

	// Explicit Deny stays a refusal, not a Stop of the helper's conversation.
	denied := f.launch(t, siblingChat, "ASK:denied.txt")
	deniedApproval := f.awaitApproval(t, denied)
	ok, gone = f.reg.resolve(deniedApproval.ApprovalID, ApprovalActionDeny, false)
	require.True(t, ok)
	require.False(t, gone)
	f.awaitState(t, denied, session.LifecycleCompleted)
	assert.Equal(t, ApprovalStateDeniedUser, f.state(deniedApproval.ApprovalID))
	_, err = os.Stat(filepath.Join(f.workDir, "denied.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)

	close(f.provider.finishHelper)
	f.awaitState(t, working, session.LifecycleCompleted)
}

// stopHelperApprovalProvider replaces only the external model. It deliberately
// completes normally after ANY tool result: Stop must be enforced by the real
// runtime, never by a model that helpfully pretends a cancellation occurred.
type stopHelperApprovalProvider struct {
	entered      chan context.Context
	release      chan struct{}
	finishHelper chan struct{}
}

func (*stopHelperApprovalProvider) GetDefaultModel() string { return "stop-helper-approval" }

func (p *stopHelperApprovalProvider) Chat(ctx context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	// A real Resume retains earlier tool history. Only a tool result after
	// the latest scripted instruction completes this request, not an old run.
	instruction := -1
	for i, msg := range msgs {
		if msg.Role == "user" && (strings.Contains(msg.Content, "ASK:") || strings.Contains(msg.Content, "WAIT:")) {
			instruction = i
		}
	}
	for _, msg := range msgs[instruction+1:] {
		if msg.Role == "tool" {
			return &providers.LLMResponse{Content: "Finished after the tool result."}, nil
		}
	}
	for _, name := range []string{"child.txt", "grandchild.txt", "sibling.txt", "denied.txt", "parent.txt"} {
		if instruction >= 0 {
			if msg := msgs[instruction]; strings.Contains(msg.Content, "ASK:"+name) {
				return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
					ID: "write-" + name, Type: "function", Name: "write_file",
					Arguments: map[string]any{"path": name, "content": "approved helper content\n"},
				}}}, nil
			}
		}
	}
	p.entered <- ctx
	for _, msg := range msgs {
		if msg.Role == "user" && strings.Contains(msg.Content, "WAIT:helper") {
			select {
			case <-p.finishHelper:
				return &providers.LLMResponse{Content: "Ordinary helper completed."}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-p.release:
				return nil, context.Canceled
			}
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return nil, context.Canceled
	}
}

type stopHelperApprovalFixture struct {
	al       *agent.AgentLoop
	h        *WSHandler
	wc       *wsConn
	reg      *approvalRegistryV2
	provider *stopHelperApprovalProvider
	workDir  string
}

func newStopHelperApprovalFixture(t *testing.T) *stopHelperApprovalFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "stop-helper-approval"}, MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: "mia", Home: home}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 6, MaxDelegationDepth: 3},
		Sandbox: config.OmnipusSandboxConfig{
			Mode:         config.SandboxModeOff,
			ToolPolicies: map[string]string{"write_file": "ask"},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	provider := &stopHelperApprovalProvider{
		entered: make(chan context.Context, 16), release: make(chan struct{}), finishHelper: make(chan struct{}),
	}
	al := mustAgentLoop(t, cfg, msgBus, provider)
	lifecycle := al.GetSessionLifecycleStore()
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel))
	t.Cleanup(func() { gatewaySteerCancellers.Delete(al) })
	al.SetSteerAudienceDeps(
		agent.NewSteerAudienceResolver(agent.NewSteerRecordClassifier(lifecycle, al.GetSessionStore())),
		steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	h := newWSHandler(msgBus, al, "")
	wc, _ := makeForwarderTestConn(256)
	wc.userID = "stop-helper-owner"
	h.sessions[wc.userID] = wc
	h.notifStore = notifications.NewStore(t.TempDir())
	al.SetEventSyncTap(func(event agent.Event) { forwardStopTestEvent(h, wc, event) })
	reg := newApprovalRegistryV2(64, 10*time.Minute)
	// Keep resolved entries for exact-state assertions; cleanup removes timers.
	reg.terminalRetention = time.Hour
	h.approvalRegV2 = reg
	reg.setResolutionListener(h.broadcastToolApprovalResolved)
	al.SetToolApprover(newPolicyApproverAdapter(reg, h))
	t.Cleanup(func() {
		close(provider.release)
		reg.cancelAllPendingForRestart()
	})
	workDir, err := workspace.EnsureWorkDir(home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	return &stopHelperApprovalFixture{al: al, h: h, wc: wc, reg: reg, provider: provider, workDir: workDir}
}

func (f *stopHelperApprovalFixture) newChat(t *testing.T) string {
	t.Helper()
	meta, err := f.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, f.al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: strPtr(testHarnessWorkspaceMembershipID)}))
	require.NoError(t, f.al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: testHarnessWorkspaceMembershipID,
		AgentID: "mia", Origin: &session.Origin{Kind: session.OriginKindChat},
	}))
	return meta.ID
}

func (f *stopHelperApprovalFixture) startParent(t *testing.T, id string) (context.Context, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		_, runErr = f.al.ProcessScheduled(ctx, "mia", id, "WAIT:parent", "webchat", id)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				t.Errorf("parent turn failed: %v", runErr)
			}
		case <-time.After(cancelTestTurnStartDeadline):
			t.Error("parent turn did not join during cleanup")
		}
	})
	return f.awaitProvider(t), done
}

func (f *stopHelperApprovalFixture) awaitProvider(t *testing.T) context.Context {
	t.Helper()
	select {
	case ctx := <-f.provider.entered:
		return ctx
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("SETUP: real turn never entered the model boundary")
		return nil
	}
}

func (f *stopHelperApprovalFixture) launch(t *testing.T, parent, task string) string {
	t.Helper()
	launcher := agent.NewSteerLauncher(f.al)
	launched, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parent, TargetAgentID: "mia", Task: task,
		Origin: steer.Origin{Kind: steer.OriginKindDelegate},
	})
	require.NoError(t, err)
	dispatched, err := launcher.Dispatch(context.Background(), launched.SessionID, launched.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, dispatched.State, "SETUP: real helper must run, not queue")
	return launched.SessionID
}

func (f *stopHelperApprovalFixture) awaitApproval(t *testing.T, id string) *approvalEntry {
	t.Helper()
	var entry *approvalEntry
	require.Eventually(t, func() bool {
		for _, pending := range f.reg.pendingApprovals() {
			if pending.SessionID == id {
				entry = pending
				return true
			}
		}
		return false
	}, cancelTestTurnStartDeadline, 10*time.Millisecond, "SETUP: helper %s must reach the real Ask gate", id)
	require.Equal(t, "write_file", entry.ToolName)
	require.NotEmpty(t, entry.TurnID)
	return entry
}

func (f *stopHelperApprovalFixture) state(id string) ApprovalState {
	f.reg.mu.Lock()
	defer f.reg.mu.Unlock()
	if entry := f.reg.entries[id]; entry != nil {
		return entry.state
	}
	return ApprovalState("missing")
}

func (f *stopHelperApprovalFixture) awaitState(t *testing.T, id string, want session.LifecycleState) {
	t.Helper()
	var got *session.LifecycleRecord
	var err error
	require.Eventually(t, func() bool {
		got, err = f.al.GetSessionLifecycleStore().Load(id)
		return err == nil && got.State == want && f.al.GetActiveTurnBySession(id) == nil
	}, cancelTestTurnStartDeadline, 10*time.Millisecond, "helper %s must settle %s, not keep running or complete as if approved", id, want)
	require.NoError(t, err)
	require.Equal(t, want, got.State)
}

func (f *stopHelperApprovalFixture) drainFrames(t *testing.T) []generated.ToolApprovalResolvedFrame {
	t.Helper()
	var resolved []generated.ToolApprovalResolvedFrame
	for {
		select {
		case raw := <-f.wc.sendCh:
			var frame generated.ToolApprovalResolvedFrame
			require.NoError(t, json.Unmarshal(raw, &frame), fmt.Sprintf("invalid frame: %s", raw))
			if frame.Type == "tool_approval_resolved" {
				resolved = append(resolved, frame)
			}
		default:
			return resolved
		}
	}
}
