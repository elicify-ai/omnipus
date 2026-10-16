package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

const secretWakeAcceptanceValue = "FAKE-QUEUED-RESTART-SECRET"

type secretWakeAcceptanceProvider struct {
	mu         sync.Mutex
	requests   map[string][][]providers.Message
	total      int
	blockFirst bool
	askFirst   bool
	entered    chan struct{}
	release    chan struct{}
	once       sync.Once
}

func (p *secretWakeAcceptanceProvider) GetDefaultModel() string { return "test-model" }
func (p *secretWakeAcceptanceProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	sid := tools.ToolTranscriptSessionID(ctx)
	p.mu.Lock()
	p.total++
	first := p.total == 1
	p.requests[sid] = append(p.requests[sid], append([]providers.Message(nil), messages...))
	p.mu.Unlock()
	if first && p.blockFirst {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if first && p.askFirst {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "acceptance-secret-tool", Type: "function", Name: "request_secret", Arguments: map[string]any{"purpose": "OpenAI setup key", "endpoint": "https://api.openai.com/v1"}}}}, nil
	}
	return &providers.LLMResponse{Content: "secret continuation finished", FinishReason: "stop"}, nil
}
func (p *secretWakeAcceptanceProvider) open() { p.once.Do(func() { close(p.release) }) }
func (p *secretWakeAcceptanceProvider) calls(sid string) [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]providers.Message(nil), p.requests[sid]...)
}

type secretWakeAcceptanceFixture struct {
	al       *AgentLoop
	bus      *bus.MessageBus
	store    *credentials.Store
	provider *secretWakeAcceptanceProvider
	home     string
	root     string
}

func newSecretWakeAcceptanceFixture(t *testing.T, blockFirst, askFirst bool) *secretWakeAcceptanceFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x39}, 32)))
	t.Cleanup(store.Close)
	provider := &secretWakeAcceptanceProvider{requests: make(map[string][][]providers.Message), blockFirst: blockFirst, askFirst: askFirst, entered: make(chan struct{}), release: make(chan struct{})}
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: filepath.Join(home, "workspace"), DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096, MaxToolIterations: 4},
		List:     []config.AgentConfig{{ID: "omnipus", Name: "Omnipus", Type: config.AgentTypeCore}},
	}}
	cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
	cfg.Sandbox.ToolPolicies["request_secret"] = "allow"
	cfg.Sandbox.ToolPolicies["message_parent"] = "allow"
	cfg.Performance.MaxParallelAgents = 1 // Smallest positive cap forces genuine admission queueing.
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)
	t.Cleanup(provider.open) // Release model edge BEFORE joining the real runtime.
	installTestSessionLauncher(t, al)
	wireSteerCompletionDeps(t, al)
	al.WireSysagentDeps(&systools.Deps{Home: home, GetCfg: al.GetConfig, CredStore: store})
	main, err := al.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, "omnipus")
	require.NoError(t, err)
	owner := "qa-secret-wake-owner"
	require.NoError(t, al.GetSessionStore().SetMeta(main.ID, session.MetaPatch{Owner: &owner}))
	return &secretWakeAcceptanceFixture{al: al, bus: msgBus, store: store, provider: provider, home: home, root: main.ID}
}

func (f *secretWakeAcceptanceFixture) launch(t *testing.T, callID string) steer.LaunchResult {
	t.Helper()
	child, err := NewSteerLauncher(f.al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: f.root, TargetAgentID: "omnipus", Task: "Original helper setup instruction", Label: callID,
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	require.NoError(t, err)
	return child
}

func (f *secretWakeAcceptanceFixture) pending(t *testing.T, child steer.LaunchResult) secretrequest.Record {
	t.Helper()
	rec, err := f.al.SecretRequests().Create(context.Background(), child.SessionID, "omnipus", "acceptance-chat", "", secretrequest.Input{Purpose: "OpenAI setup key", Endpoint: "https://api.openai.com/v1"})
	require.NoError(t, err)
	require.Equal(t, "pending", rec.Card.State)
	return rec
}

func (f *secretWakeAcceptanceFixture) submit(t *testing.T, rec secretrequest.Record) (secretrequest.Record, bus.InboundMessage) {
	t.Helper()
	values := map[string]string{"api_key": secretWakeAcceptanceValue}
	submitted, err := f.al.SecretRequests().Submit(rec.Card.RequestId, rec.Owner, gen.SecretSubmitRequest{Values: &values})
	require.NoError(t, err)
	require.Equal(t, "submitted", submitted.Card.State)
	wake := f.takeWake(t, rec.Card.RequestId)
	require.Equal(t, "system", wake.Channel, "real helper wake uses the existing steered system path")
	require.Equal(t, rec.Card.SessionId, wake.AsyncTranscriptSessionID)
	require.NotContains(t, wake.Content, secretWakeAcceptanceValue)
	return submitted, wake
}

func (f *secretWakeAcceptanceFixture) takeWake(t *testing.T, id string) bus.InboundMessage {
	t.Helper()
	for {
		select {
		case msg := <-f.bus.InboundChan():
			if msg.Metadata[secretResumeMetadataKey] == id {
				return msg
			}
		default:
			t.Fatal("submitted/recovered receipt did not publish its continuation")
		}
	}
}

func secretWakeClaimCount(t *testing.T, al *AgentLoop, rec secretrequest.Record) int {
	t.Helper()
	entries, err := al.GetSessionStore().ReadTranscript(rec.Card.SessionId)
	require.NoError(t, err)
	count := 0
	for _, entry := range entries {
		if entry.ID == "secret-resume-"+rec.Card.RequestId {
			count++
			require.Equal(t, "user", entry.Role)
			require.NotContains(t, entry.Content, secretWakeAcceptanceValue)
		}
	}
	return count
}

func requireSecretWakePrompt(t *testing.T, messages []providers.Message, rec secretrequest.Record) {
	t.Helper()
	var prompt strings.Builder
	for _, message := range messages {
		prompt.WriteString(message.Content + "\n")
	}
	require.Equal(t, 1, strings.Count(prompt.String(), "Secret request "+rec.Card.RequestId+" was submitted."), "one reference-only continuation injection, never duplicate model input")
	for _, ref := range rec.Card.ReferenceNames {
		require.Contains(t, prompt.String(), ref)
	}
	raw, err := json.Marshal(messages)
	require.NoError(t, err)
	require.NotContains(t, string(raw), secretWakeAcceptanceValue)
}

func (f *secretWakeAcceptanceFixture) freshRegistry(t *testing.T) *secretrequest.Registry {
	t.Helper()
	// A new real Registry drops only process-local inFlight, as a boot does;
	// all sessions, receipts and transcript claims remain on the real disk.
	f.al.secretRequests.mu.Lock()
	f.al.secretRequests.registry = nil
	f.al.secretRequests.mu.Unlock()
	reg := f.al.SecretRequests()
	require.NoError(t, reg.Recover())
	return reg
}

func (f *secretWakeAcceptanceFixture) boot(t *testing.T) {
	t.Helper()
	before := f.al.bootEpochFor()
	boot := session.NewBootEpochStore(filepath.Join(f.al.GetConfig().Agents.Defaults.Home, "boot_epoch"))
	epoch, err := boot.Mint()
	require.NoError(t, err)
	require.Greater(t, epoch, before, "a real persisted boot epoch, not a fabricated restart flag")
	f.al.SetBootEpochStore(boot)
	recovery := &SteerBootRecovery{Lifecycle: f.al.GetSessionLifecycleStore(), Sessions: f.al.GetSessionStore(), Inbox: f.al.GetMessageInboxStore(), Classifier: NewSteerRecordClassifier(f.al.GetSessionLifecycleStore(), f.al.GetSessionStore()), BootEpoch: boot}
	require.NoError(t, recovery.Run(context.Background()))
}

func (f *secretWakeAcceptanceFixture) requireNoSecretWake(t *testing.T, id string) {
	t.Helper()
	for {
		select {
		case msg := <-f.bus.InboundChan():
			require.NotEqual(t, id, msg.Metadata[secretResumeMetadataKey], "no old secret continuation may be re-dispatched")
		default:
			return
		}
	}
}

func awaitSecretWakeSignal(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s was not observed", what)
	}
}
