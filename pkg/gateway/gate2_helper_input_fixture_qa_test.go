package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/gorilla/websocket"
)

type qa2HelperInputProvider struct {
	mu       sync.Mutex
	requests [][]providers.Message
	entered  chan int
	release  []chan struct{}
}

func (p *qa2HelperInputProvider) GetDefaultModel() string { return "qa2-helper-input-model" }
func (p *qa2HelperInputProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	index := len(p.requests)
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	p.mu.Unlock()
	if index >= len(p.release) {
		return nil, fmt.Errorf("helper-input fixture: unexpected model turn %d", index+1)
	}
	p.entered <- index
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release[index]:
		return &providers.LLMResponse{Content: "The helper's answer is ready.", FinishReason: "stop"}, nil
	}
}
func (p *qa2HelperInputProvider) open(index int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.release[index]:
	default:
		close(p.release[index])
	}
}
func (p *qa2HelperInputProvider) snapshot() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]providers.Message(nil), p.requests...)
}

type qa2HelperInputFixture struct {
	handler *WSHandler
	server  *httptest.Server
	loop    *agent.AgentLoop
	child   *session.LifecycleRecord
	p       *qa2HelperInputProvider
	tokens  map[string]string
}

// Real cookie authentication, real gateway frame handling, real loop, and
// really admitted worker helper. Only the external model is staged. The
// parent owner stamp is copied by actual Launch, never forged onto the child.
func newQA2HelperInputFixture(t *testing.T) *qa2HelperInputFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tokens := map[string]string{"qa2-owner": "qa2-test-owner-cookie", "qa2-other": "qa2-test-other-cookie"}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, Users: []config.UserConfig{
			{Username: "qa2-owner", SessionTokenHash: mustBcryptHash(t, tokens["qa2-owner"])},
			{Username: "qa2-other", SessionTokenHash: mustBcryptHash(t, tokens["qa2-other"])},
		}},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "qa2-helper-input-model"}, DefaultAgentID: "mia", MaxTokens: 4096, MaxToolIterations: 10},
			List:     []config.AgentConfig{{ID: "mia", Type: config.AgentTypeCore}, {ID: "hans", Type: config.AgentTypeWorker}},
		},
	}
	p := &qa2HelperInputProvider{entered: make(chan int, 2), release: []chan struct{}{make(chan struct{}), make(chan struct{})}}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, p)
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
	al.SetSessionMessagingStores(inbox, lifecycle)
	boot := session.NewBootEpochStore(home)
	if minted, err := boot.Mint(); err != nil || minted == 0 {
		t.Fatalf("SETUP real boot epoch = %d/%v, want minted", minted, err)
	}
	al.SetBootEpochStore(boot)
	classifier := agent.NewSteerRecordClassifier(lifecycle, al.GetSessionStore())
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	al.SetSteeringMode(agent.SteeringAll)
	parent, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatal(err)
	}
	owner := "qa2-owner"
	if metaErr := al.GetSessionStore().SetMeta(parent.ID, session.MetaPatch{Owner: &owner}); metaErr != nil {
		t.Fatal(metaErr)
	}
	launched, err := agent.NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parent.ID, TargetAgentID: "hans", Task: "Original helper instruction.",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "qa2-a8-real-worker-helper"},
	})
	if err != nil {
		t.Fatalf("SETUP actual delegated worker Launch: %v", err)
	}
	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(ctx) }()
	t.Cleanup(func() {
		p.open(0)
		p.open(1)
		cancel()
		select {
		case runErr := <-runDone:
			if runErr != nil {
				t.Errorf("real loop shutdown: %v", runErr)
			}
		case <-time.After(10 * time.Second):
			t.Error("real helper-input loop did not finish shutdown")
		}
	})
	dispatch, err := agent.NewSteerLauncher(al).Dispatch(context.Background(), launched.SessionID, launched.Generation)
	if err != nil || dispatch.State != steer.DispatchRunning {
		t.Fatalf("SETUP real worker Dispatch = %+v/%v, want running", dispatch, err)
	}
	qa2AwaitHelperProvider(t, p, 0)
	child, err := lifecycle.Load(launched.SessionID)
	if err != nil || child.State != session.LifecycleRunning || child.ExecutionID == nil || child.SteeredBy == nil || child.AgentID != "hans" {
		t.Fatalf("SETUP actual worker helper = %+v/%v, want admitted live steered hans", child, err)
	}
	meta, err := al.GetSessionStore().GetMeta(child.SessionID)
	if err != nil || meta.Owner != owner || meta.AgentID != "hans" || !isWorkerAgentID(cfg, "hans") {
		t.Fatalf("SETUP owned worker profile = %+v/%v, want real worker hans owned by %s", meta, err, owner)
	}
	t.Cleanup(handler.Wait)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &qa2HelperInputFixture{handler: handler, server: server, loop: al, child: child, p: p, tokens: tokens}
}

func qa2AwaitHelperProvider(t *testing.T, p *qa2HelperInputProvider, want int) {
	t.Helper()
	select {
	case got := <-p.entered:
		if got != want {
			t.Fatalf("provider turn index=%d, want exactly %d", got, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("provider did not receive required helper input for call %d; requests=%d", want, len(p.snapshot()))
	}
}

func qa2AuthenticatedHelperSocket(t *testing.T, f *qa2HelperInputFixture, user string) *websocket.Conn {
	t.Helper()
	conn := dialTestWSWithCookie(t, f.server, f.tokens[user])
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close authenticated test socket: %v", err)
		}
	})
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		f.handler.mu.Lock()
		authenticated := false
		for _, wc := range f.handler.sessions {
			authenticated = authenticated || wc.userID == user
		}
		f.handler.mu.Unlock()
		if authenticated {
			return conn
		}
		select {
		case <-deadline.C:
			t.Fatalf("SETUP real cookie auth did not resolve principal %s", user)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func qa2SendHelperInput(t *testing.T, conn *websocket.Conn, sessionID, agentID, content, clientID string) (bool, string) {
	t.Helper()
	frame := generated.MessageFrame{Type: string(generated.WsFrameTypeMessage), Content: content, ClientMessageId: &clientID}
	if sessionID != "" {
		frame.SessionId = &sessionID
	}
	if agentID != "" {
		frame.AgentId = &agentID
	}
	if err := conn.WriteJSON(frame); err != nil {
		t.Fatalf("send actual helper message frame: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("no actual gateway message disposition: %v", err)
		}
		var envelope wsTypeOnly
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		switch envelope.Type {
		case string(generated.WsFrameTypeError):
			var refusal generated.ErrorFrame
			if err := json.Unmarshal(raw, &refusal); err != nil {
				t.Fatal(err)
			}
			return false, refusal.Message
		case string(generated.WsFrameTypeMessageStatus):
			var status generated.MessageStatusFrame
			if err := json.Unmarshal(raw, &status); err != nil {
				t.Fatal(err)
			}
			if status.ClientMessageId == clientID && status.State == "received" {
				return true, ""
			}
		}
	}
}
