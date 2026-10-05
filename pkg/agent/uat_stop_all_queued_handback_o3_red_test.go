package agent

// UAT O3. Frozen ADR-20260928 D5/D6/D7: Stop all supersedes earlier pending
// input; a stopped parent runs no compute and keeps upward messages for resume.
// Drive a real human root, registered delegate.run, real completed child/outbox/
// inbox, and the actual system-wake inbound entry. No execution identities,
// lifecycle records, wake payloads or stop notes are manufactured by this pack.
// Only the external provider is controlled. GREEN/mutations belong to CHECK.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

const uatO3ChildAnswer = "The delegated helper finished its requested work."
const uatO3ParentAnswer = "The parent processed the queued helper hand-back."

type uatO3ParentProvider struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
	once    sync.Once
}

func (p *uatO3ParentProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	call := p.calls.Add(1)
	if call == 1 {
		p.once.Do(func() { close(p.entered) })
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.release:
		}
	}
	return &providers.LLMResponse{Content: uatO3ParentAnswer}, nil
}

func (*uatO3ParentProvider) GetDefaultModel() string { return "uat-o3-parent-probe" }

type uatO3ChildProvider struct{ calls atomic.Int32 }

func (p *uatO3ChildProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	return &providers.LLMResponse{Content: uatO3ChildAnswer}, nil
}

func (*uatO3ChildProvider) GetDefaultModel() string { return "uat-o3-real-child" }

type uatO3Fixture struct {
	al         *AgentLoop
	msgBus     *bus.MessageBus
	parent     *uatO3ParentProvider
	rootID     string
	childID    string
	rootResult *rootHumanResult
	release    func()
	joinRoot   func()
}

func newUATO3QueuedHandback(t *testing.T) *uatO3Fixture {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	p := &uatO3ParentProvider{entered: make(chan struct{}), release: make(chan struct{})}
	al, msgBus, childAgent, parentAgent := newAsyncResultTestLoop(t, p)
	childProvider := &uatO3ChildProvider{}
	childAgent.Provider = childProvider
	home := al.GetConfig().Agents.Defaults.Home
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "session_messages")), session.NewLifecycleStore(filepath.Join(home, "session_lifecycle")))
	boot := session.NewBootEpochStore(home)
	_, err := boot.Mint()
	require.NoError(t, err)
	al.SetBootEpochStore(boot)
	wireSteerCompletionDeps(t, al)
	al.SetSteerSessionLauncher(NewSteerLauncher(al))
	// Operator configuration is real and written through the owning store.
	// The existing fixture seeded both registered agents into this workspace.
	require.NoError(t, workspace.SaveDelegation(omnipusHome(), testHarnessWorkspaceMembershipID, []workspace.DelegationEdge{{
		FromAgent: parentAgent.ID, ToAgent: childAgent.ID, Modes: []workspace.DelegationMode{workspace.ModeDirect},
	}}))
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", parentAgent.ID)
	require.NoError(t, err)
	owner := "uat-o3-owner"
	ws := testHarnessWorkspaceMembershipID
	require.NoError(t, al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &ws, Owner: &owner}))
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(p.release) }) }
	rootDone := make(chan struct{})
	rootResult := &rootHumanResult{}
	go func() {
		defer close(rootDone)
		rootResult.response, _, rootResult.err = al.processMessage(context.Background(), bus.InboundMessage{
			Channel: "webchat", ChatID: meta.ID, SessionID: meta.ID,
			Sender: bus.SenderInfo{CanonicalID: owner}, GatewayUserID: owner, UserInitiated: true,
			Content: "Wait for the requested helper work", Metadata: map[string]string{"agent_id": parentAgent.ID, "workspace_id": ws},
		})
	}()
	joinRoot := func() {
		select {
		case <-rootDone: // Includes the ordinary execution's disposition tail.
		case <-time.After(5 * time.Second):
			t.Fatal("SETUP: actual root execution and its disposal failed to join")
		}
	}
	t.Cleanup(func() { release(); joinRoot() })
	select {
	case <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: real human root never reached its provider")
	}
	root, err := al.GetSessionLifecycleStore().Load(meta.ID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleRunning, root.State)
	require.NotNil(t, root.ExecutionID, "the real root admission must own a production-minted execution")
	registered, ok := parentAgent.Tools.Get("delegate")
	require.True(t, ok, "BLOCKED: registered delegate tool missing — required by UAT O3 / ADR D7")
	dt, ok := registered.(*tools.DelegateTool)
	require.True(t, ok)
	ctx := tools.WithTranscriptSessionID(tools.WithAgentID(tools.WithWorkspaceID(context.Background(), ws), parentAgent.ID), meta.ID)
	ctx = tools.WithToolCallID(ctx, "uat-o3-real-delegate")
	result := dt.Execute(ctx, map[string]any{"action": "run", "agent_id": childAgent.ID, "task": "Finish the requested helper work", "label": "UAT O3 helper"})
	require.NotNil(t, result)
	require.False(t, result.IsError, "SETUP: actual delegate.run was refused: %s", result.ForLLM)
	joinGoalFixtureRuns(t, al) // Join actual launch, model, commit, publication and retirement.
	children, err := al.GetSessionLifecycleStore().List(session.LifecycleFilter{SteeringSessionID: meta.ID})
	require.NoError(t, err)
	require.Len(t, children, 1, "one actual delegate.run creates exactly one helper")
	child := children[0]
	require.Equal(t, session.LifecycleCompleted, child.State, "the queued event must come from a genuinely completed helper")
	require.NotNil(t, child.FinalDelivery, "the final must be committed before the wake is published")
	require.Equal(t, int32(1), childProvider.calls.Load(), "the real helper must run exactly once")
	pending, _, _, err := al.GetMessageInboxStore().Drain(meta.ID, child.SessionID, "", 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	handback, err := pending[0].AsSessionMessageHandback()
	require.NoError(t, err)
	require.Equal(t, uatO3ChildAnswer, handback.ResultSoFar)
	require.Equal(t, fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation), handback.MessageId)
	// No Run consumer has been started: the ACTUAL runtime-produced wake
	// remains queued on the real inbound bus until after the Stop-all boundary.
	require.Equal(t, 1, len(msgBus.InboundChan()), "SETUP: real completed helper must have queued one unconsumed system wake")
	return &uatO3Fixture{al: al, msgBus: msgBus, parent: p, rootID: meta.ID, childID: child.SessionID, rootResult: rootResult, release: release, joinRoot: joinRoot}
}

func (f *uatO3Fixture) queuedWake(t *testing.T) bus.InboundMessage {
	t.Helper()
	select {
	case wake := <-f.msgBus.InboundChan():
		require.Equal(t, "system", wake.Channel)
		require.Equal(t, f.rootID, wake.AsyncTranscriptSessionID)
		require.Equal(t, f.childID+":1:final", inboundMetadata(wake, "steer_message_id"), "this must be the runtime's actual helper-final wake, not a synthetic event")
		return wake
	default:
		t.Fatal("SETUP: the completed helper's actual queued wake disappeared")
		return bus.InboundMessage{}
	}
}

func TestUATO3_StopAllSupersedesQueuedHandbackWithoutAnotherParentTurn(t *testing.T) {
	f := newUATO3QueuedHandback(t)
	res, err := f.al.StopSession(context.Background(), StopRequest{SessionID: f.rootID,
		By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "uat-o3-owner"}, Channel: "webchat", Tree: true})
	require.NoError(t, err)
	require.NoError(t, res.RootErr)
	require.Empty(t, res.StillRunning(), "Stop all may not acknowledge an incomplete cascade")
	f.release()
	f.joinRoot()
	require.ErrorIs(t, f.rootResult.err, context.Canceled, "the original root's real Stop-all error must be preserved, not swallowed")
	before, err := f.al.GetSessionLifecycleStore().Load(f.rootID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleStopped, before.State, "SETUP: Stop all must have landed before the old queued wake is admitted")
	require.Nil(t, before.Stop)
	callsAtStop := f.parent.calls.Load()
	require.Equal(t, int32(1), callsAtStop, "only the original human root call has executed")
	wake := f.queuedWake(t)
	_, _, wakeErr := f.al.processMessage(context.Background(), wake) // Actual system inbound/admission path.
	t.Logf("actual queued wake after Stop all: response error=%v", wakeErr)
	if calls := f.parent.calls.Load(); calls != callsAtStop {
		t.Errorf("UAT O3: queued helper hand-back started %d new parent provider turn(s) after Stop all landed; want 0 — the stopped chat must stay quiet until an explicit resume", calls-callsAtStop)
	}
	entries, err := f.al.GetSessionStore().ReadTranscript(f.rootID)
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.Content == "consumed "+inboundMetadata(wake, "steer_message_id") {
			t.Errorf("UAT O3: stopped parent consumed its superseded queued hand-back %q without an explicit resume", entry.Content)
		}
	}
	pending, _, _, err := f.al.GetMessageInboxStore().Drain(f.rootID, f.childID, "", 10)
	require.NoError(t, err)
	if len(pending) != 1 {
		t.Errorf("UAT O3: queued hand-back entries remaining for explicit resume=%d, want exactly 1; Stop all must suppress compute, not discard the helper's durable result", len(pending))
	}
	if after, loadErr := f.al.GetSessionLifecycleStore().Load(f.rootID); loadErr != nil {
		t.Fatal(loadErr)
	} else if after.State != session.LifecycleStopped || after.Generation != before.Generation {
		t.Errorf("UAT O3: old system wake changed stopped root to %q/generation %d; want stopped/generation %d", after.State, after.Generation, before.Generation)
	}
}

func TestUATO3_UnstoppedQueuedHandbackActuallyRunsOneParentTurn(t *testing.T) {
	// Positive instrument control: the identical real queued event reaches the
	// real provider without Stop all. A missing/unwired consumer cannot pass O3.
	f := newUATO3QueuedHandback(t)
	f.release()
	f.joinRoot()
	require.NoError(t, f.rootResult.err, "the unstopped original root must really finish successfully")
	require.Equal(t, uatO3ParentAnswer, f.rootResult.response)
	wake := f.queuedWake(t)
	response, _, err := f.al.processMessage(context.Background(), wake)
	require.NoError(t, err)
	require.Equal(t, uatO3ParentAnswer, response)
	require.Equal(t, int32(2), f.parent.calls.Load(), "one original root plus exactly one actual queued hand-back turn")
	entries, err := f.al.GetSessionStore().ReadTranscript(f.rootID)
	require.NoError(t, err)
	consumed := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Content, "consumed ") && entry.Content == "consumed "+inboundMetadata(wake, "steer_message_id") {
			consumed++
		}
	}
	require.Equal(t, 1, consumed, "positive control must consume the actual helper-final id exactly once")
	pending, _, _, err := f.al.GetMessageInboxStore().Drain(f.rootID, f.childID, "", 10)
	require.NoError(t, err)
	require.Empty(t, pending, "positive control's actual running parent acknowledges its consumed hand-back")
}
