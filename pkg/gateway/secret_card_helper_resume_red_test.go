package gateway

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

type helperSecretCall struct {
	sessionID string
	text      string
	index     int
}
type helperSecretProvider struct {
	mu        sync.Mutex
	calls     map[string]int
	events    chan helperSecretCall
	requested bool
}

func (*helperSecretProvider) GetDefaultModel() string { return "test-model" }
func (p *helperSecretProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	sid := tools.ToolTranscriptSessionID(ctx)
	var text strings.Builder
	for _, msg := range messages {
		text.WriteString(msg.Role + ":" + msg.Content + "\n")
	}
	p.mu.Lock()
	p.calls[sid]++
	index := p.calls[sid]
	request := !p.requested
	p.requested = true
	p.mu.Unlock()
	p.events <- helperSecretCall{sid, text.String(), index}
	if request {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "helper-secret-tool", Type: "function", Name: "request_secret", Arguments: map[string]any{"purpose": "Helper OpenAI key", "endpoint": secretCardEndpoint}}}}, nil
	}
	return &providers.LLMResponse{Content: "The helper resumed.", FinishReason: "stop"}, nil
}

// BDD-6.11 / FR-078/087: use an ACTUAL steered native helper, not just a
// synthetic parent-session pointer. Card placement, real K5 submit, resumed
// model input and the durable request-id consumption marker must all agree.
func TestSecretCard_RealOmnipusHelperResumesAndClaimsRequestExactlyOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", cardMatrixHTTPBearer)
	p := &helperSecretProvider{calls: make(map[string]int), events: make(chan helperSecretCall, 32)}
	cfg := secretCardAgentConfig("omnipus", "Omnipus", false)
	cfg.Gateway.DevModeBypass = false
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Sandbox.ToolPolicies["delegate"] = "allow"
	cfg.Sandbox.ToolPolicies["message_parent"] = "allow"
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, p)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x71}, 32)))
	t.Cleanup(store.Close)
	al.WireSysagentDeps(&systools.Deps{Home: home, GetCfg: al.GetConfig, CredStore: store})
	lifecycle := al.GetSessionLifecycleStore()
	classifier := agent.NewSteerRecordClassifier(lifecycle, al.GetSessionStore())
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	main, err := al.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, "omnipus")
	require.NoError(t, err)
	owner := "_env_token"
	require.NoError(t, al.GetSessionStore().SetMeta(main.ID, session.MetaPatch{Owner: &owner}))
	handler := newWSHandler(msgBus, al, "")
	tabs := apprResAttachConns(t, handler, 1)
	tabs[0].userID = owner
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(ctx) }()
	t.Cleanup(func() { cancel(); al.Close(); require.NoError(t, <-runDone); handler.Wait() })
	launcher := agent.NewSteerLauncher(al)
	child, err := launcher.Launch(ctx, steer.LaunchRequest{
		SteeringSessionID: main.ID, TargetAgentID: "omnipus", Label: "Secret setup helper", Task: "Request the setup key on the secure card.",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "real-secret-helper"},
	})
	require.NoError(t, err, "fixture: existing real self-delegation must be available")
	dispatched, err := launcher.Dispatch(ctx, child.SessionID, child.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, dispatched.State)
	frame, ok := readFrameType(t, tabs[0], "secret_request", 8*time.Second)
	require.True(t, ok, "BDD-6.11: a real Omnipus helper must raise its secure card")
	id, ok := frame["request_id"].(string)
	require.True(t, ok)
	require.NotEmpty(t, id)
	require.Equal(t, main.ID, frame["root_session_id"], "helper card belongs in the Omnipus main chat")
	require.Equal(t, child.SessionID, frame["session_id"])
	require.Equal(t, "Omnipus", frame["acting_agent_name"])
	rec, err := al.SecretRequests().Get(id, owner)
	require.NoError(t, err)
	api := &restAPI{agentLoop: al, homePath: home, credStore: store}
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	f := &cardMatrixHTTP{api: api, mux: mux, al: al, bus: msgBus, store: store, rec: rec}
	response := f.call(t, http.MethodPost, f.valuesBody(t, map[string]string{"api_key": cardMatrixHTTPValue}))
	state := requireCardMatrixState(t, response, http.StatusOK, id, gen.SecretRequestStateStateSubmitted)
	require.NotNil(t, state.ReferenceNames)
	require.Len(t, *state.ReferenceNames, 1)
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	var continuation helperSecretCall
	for continuation.index == 0 {
		select {
		case call := <-p.events:
			if call.sessionID == child.SessionID && call.index == 2 {
				continuation = call
			}
		case <-deadline.C:
			t.Fatal("FR-087: submitted helper card never resumed the helper's model turn (published bus input is not helper consumption)")
		}
	}
	require.Contains(t, continuation.text, (*state.ReferenceNames)[0])
	require.NotContains(t, continuation.text, cardMatrixHTTPValue)
	entries, err := al.GetSessionStore().ReadTranscript(child.SessionID)
	require.NoError(t, err)
	var markers []session.TranscriptEntry
	for _, entry := range entries {
		if entry.ID == secretrequest.ResumeEntryID(id) {
			markers = append(markers, entry)
		}
	}
	require.Len(t, markers, 1, "helper steering must not bypass the durable request-id ClaimResume boundary")
	require.Contains(t, markers[0].Content, (*state.ReferenceNames)[0])
	require.NotContains(t, markers[0].Content, cardMatrixHTTPValue)
	_, claimed, err := al.SecretRequests().ClaimResume(id, child.SessionID)
	require.NoError(t, err)
	require.False(t, claimed, "already executed helper continuation must never be claimed again")
	requireCardMatrixState(t, f.call(t, http.MethodPost, f.valuesBody(t, map[string]string{"api_key": "FAKE-DUPLICATE-HELPER"})), http.StatusConflict, id, gen.SecretRequestStateStateSubmitted)
}
