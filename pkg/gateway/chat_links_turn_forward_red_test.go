package gateway

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// T3 / BS-6.1. A registered show_to_user call on the user's own web chat is
// delivered by the production hub tap (SetEventSyncTap → hubSyncTap) to the
// connection bound to that chat. The test does not build a ToolExecEndPayload.
func TestShowToUser_RootTurnReachesBoundConnection(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall(tools.ShowToUserToolName, `{"ref":"notes/plan.md"}`).
		WithText("shown")
	al, bus, handler := chatLinksShowLoop(t, provider)
	meta := chatLinksWebSession(t, al)
	chatLinksPlanFile(t)
	conn := chatLinksBind(t, handler, meta.ID)
	ends := chatLinksTap(al, handler)
	chatLinksRun(t, al)

	chatLinksPublishWeb(t, al, bus, meta.ID, "show the plan")
	end := chatLinksAwaitShow(t, ends)
	if end.IsError || end.OpenInLibrary == nil {
		t.Fatalf("root show_to_user = error:%v result:%q target:%v, want an open target (BS-6.1)", end.IsError, end.Result, end.OpenInLibrary)
	}
	opens := chatLinksOpenFrames(chatLinksFrames(conn))
	if len(opens) != 1 {
		t.Fatalf("bound connection got %d open_in_library frames, want 1 (BS-6.1)", len(opens))
	}
	frame := opens[0]
	if frame["session_id"] != meta.ID || frame["workspace_id"] != testHarnessWorkspaceMembershipID || frame["path"] != "notes/plan.md" {
		t.Fatalf("open frame = %#v, want session %s workspace %s path notes/plan.md", frame, meta.ID, testHarnessWorkspaceMembershipID)
	}
	requestID, _ := frame["request_id"].(string)
	if _, err := hex.DecodeString(requestID); err != nil || len(requestID) != 32 {
		t.Fatalf("request_id = %q, want 32 lowercase hex characters", requestID)
	}
}

// T3 / SEC-14. The child turn is admitted by Launch + Dispatch, which rebuilds
// the turn from the lifecycle record (reconstructSteeredTurn). The root
// connection, and the child's own connection, receive no open_in_library frame.
// The tool end is the one the turn emitted; this test does not build one.
func TestShowToUser_ReconstructedChildOpensNothing(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall(tools.ShowToUserToolName, `{"ref":"notes/plan.md"}`).
		WithText("child done")
	al, _, handler := chatLinksShowLoop(t, provider)
	parent := chatLinksWebSession(t, al)
	chatLinksPlanFile(t)
	classifier := agent.NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	launched, err := agent.NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parent.ID,
		TargetAgentID:     "mia",
		Task:              "Show the plan to the user.",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "chat-links-child"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(launched.SessionID)
	if err != nil || rec.SteeredBy == nil || rec.SteeredBy.RootSessionID != parent.ID || launched.SessionID == parent.ID {
		t.Fatalf("child record = %+v / %v, want a reconstructed descendant of %s", rec, err, parent.ID)
	}
	rootConn := chatLinksBind(t, handler, parent.ID)
	childConn := chatLinksBind(t, handler, launched.SessionID)
	ends := chatLinksTap(al, handler)
	chatLinksRun(t, al)
	dispatched, err := agent.NewSteerLauncher(al).Dispatch(context.Background(), launched.SessionID, launched.Generation)
	if err != nil || dispatched.State != steer.DispatchRunning {
		t.Fatalf("Dispatch = %+v / %v, want running", dispatched, err)
	}
	end := chatLinksAwaitShow(t, ends)
	const want = "show_to_user: only the chat the user is in can open the Library."
	if !end.IsError || end.Result != want || end.OpenInLibrary != nil {
		t.Fatalf("child show_to_user = error:%v result:%q target:%v, want the worker refusal and no open target (BS-6.9 / SEC-14)", end.IsError, end.Result, end.OpenInLibrary)
	}
	if n := len(chatLinksOpenFrames(chatLinksFrames(rootConn))); n != 0 {
		t.Fatalf("root connection got %d open_in_library frames, want 0 (SEC-14)", n)
	}
	if n := len(chatLinksOpenFrames(chatLinksFrames(childConn))); n != 0 {
		t.Fatalf("child connection got %d open_in_library frames, want 0 (SEC-14)", n)
	}
}

func chatLinksShowLoop(t *testing.T, provider *testutil.ScenarioProvider) (*agent.AgentLoop, *bus.MessageBus, *WSHandler) {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         1024,
				MaxToolIterations: 4,
			},
			List: []config.AgentConfig{{
				ID:    "mia",
				Name:  "Mia",
				Model: &config.AgentModelConfig{Primary: "scripted-model"},
			}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, provider)
	al.RegisterTool(tools.NewShowToUserTool(config.OmnipusHomeDir(), false, tools.NewShowToUserRateLimiter(nil)))
	for _, id := range al.GetRegistry().ListAgentIDs() {
		inst, ok := al.GetRegistry().GetAgent(id)
		if !ok {
			continue
		}
		inst.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{tools.ShowToUserToolName: config.ToolPolicyAllow},
		})
	}
	return al, msgBus, &WSHandler{hubs: newHubRegistry("chat-links-turn"), agentLoop: al}
}

func chatLinksWebSession(t *testing.T, al *agent.AgentLoop) *session.UnifiedMeta {
	t.Helper()
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	owner := "chat-links-owner"
	ws := testHarnessWorkspaceMembershipID
	if err := al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{Owner: &owner, WorkspaceID: &ws}); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	return meta
}

func chatLinksPlanFile(t *testing.T) {
	t.Helper()
	work, err := workspace.EnsureWorkDir(config.OmnipusHomeDir(), testHarnessWorkspaceMembershipID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "notes", "plan.md"), []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func chatLinksBind(t *testing.T, handler *WSHandler, sessionID string) *wsConn {
	t.Helper()
	conn := &wsConn{sendCh: make(chan []byte, 16), doneCh: make(chan struct{})}
	if _, ok := handler.hubs.getOrCreate(sessionID).bindLive(conn); !ok {
		t.Fatalf("bindLive %s", sessionID)
	}
	return conn
}

func chatLinksTap(al *agent.AgentLoop, handler *WSHandler) chan agent.ToolExecEndPayload {
	ends := make(chan agent.ToolExecEndPayload, 4)
	al.SetEventSyncTap(func(evt agent.Event) {
		if p, ok := evt.Payload.(agent.ToolExecEndPayload); ok && p.Tool == tools.ShowToUserToolName {
			ends <- p
		}
		handler.hubSyncTap(evt)
	})
	return ends
}

func chatLinksRun(t *testing.T, al *agent.AgentLoop) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- al.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("loop shutdown: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("loop did not stop")
		}
	})
}

func chatLinksPublishWeb(t *testing.T, al *agent.AgentLoop, msgBus *bus.MessageBus, sessionID, content string) {
	t.Helper()
	if err := al.GetSessionStore().AppendTranscriptStrict(sessionID, session.TranscriptEntry{
		ID: "chat-links-user", Role: "user", AgentID: "mia", Content: content, Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("transcript: %v", err)
	}
	err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:       "webchat",
		ChatID:        "chat-show-root",
		SessionID:     sessionID,
		SessionKey:    "agent:mia:session:" + sessionID,
		GatewayUserID: "chat-links-owner",
		UserInitiated: true,
		Sender:        bus.SenderInfo{CanonicalID: "human:chat-links-owner"},
		Content:       content,
	})
	if err != nil {
		t.Fatalf("PublishInbound: %v", err)
	}
}

func chatLinksAwaitShow(t *testing.T, ends <-chan agent.ToolExecEndPayload) agent.ToolExecEndPayload {
	t.Helper()
	select {
	case end := <-ends:
		return end
	case <-time.After(15 * time.Second):
		t.Fatal("show_to_user did not finish on the real turn (BS-6.1 / SEC-14)")
	}
	return agent.ToolExecEndPayload{}
}
