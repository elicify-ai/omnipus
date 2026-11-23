package gateway

// Actual registered peer-tool/router/ledger/gateway-admission/receiver fixture.
// Only the model and external gh check executable are process-edge fakes.
import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/elicify-ai/omnipus/pkg/providers"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

type programsPeerProvider struct {
	*wu5GatewayProvider
	origins map[string][]bus.Provenance // Guarded by the embedded provider's mutex.
}

func (p *programsPeerProvider) Chat(ctx context.Context, messages []providers.Message, definitions []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	sid := tools.ToolTranscriptSessionID(ctx)
	p.mu.Lock()
	p.origins[sid] = append(p.origins[sid], tools.ToolTurnProvenance(ctx))
	p.mu.Unlock()
	return p.wu5GatewayProvider.Chat(ctx, messages, definitions, model, options)
}

func (p *programsPeerProvider) result(sid, call string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, messages := range p.prompts[sid] {
		for _, message := range messages {
			if message.Role == "tool" && message.ToolCallID == call {
				return message.Content
			}
		}
	}
	return ""
}

func (p *programsPeerProvider) observedOrigins(sid string) []bus.Provenance {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]bus.Provenance(nil), p.origins[sid]...)
}

type programsPeerFixture struct {
	al                   *agent.AgentLoop
	bus                  *bus.MessageBus
	provider             *programsPeerProvider
	home, work, dev, ops string
	events               agent.EventSubscription
	shutdown             func() // Idempotent cancel/join/close for an actual runtime restart.
}

func newProgramsPeerFixture(t *testing.T) *programsPeerFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Fatal("BLOCKED: S76/S82's actual peer fixture still needs a native account-check executable; this POSIX fixture does not certify Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxToolIterations = 8
	cfg.Agents.List = []config.AgentConfig{{ID: "dev", Name: "Dev", Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()}, {ID: "ops", Name: "Ops", Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg(), Programs: []string{"github"}}}
	for _, tool := range []string{"bash", "send_message", "answer_request"} {
		cfg.Sandbox.ToolPolicies[tool] = "allow"
	}
	// The scenario's actors can run commands. The custom-agent constructor
	// deliberately seeds bash:deny; grant it explicitly on BOTH policy layers
	// before building the actual agents, not by bypassing the policy check.
	for i := range cfg.Agents.List {
		cfg.Agents.List[i].Tools.Builtin.Policies["bash"] = config.ToolPolicyAllow
	}
	cfg.Programs.WebChatAllowed = true
	cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": {Type: "telegram", WorkspaceID: "sales", Identity: &config.ChannelIdentity{Kind: "agent", ID: "dev"}, ProgramsAllowed: false}}
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: "sales", Name: "Sales", Status: "active", CoreTeam: []string{"dev", "ops"}}))
	p := &programsPeerProvider{wu5GatewayProvider: newWU5GatewayProvider(), origins: map[string][]bus.Provenance{}}
	b := bus.NewMessageBus()
	t.Cleanup(b.Close)
	al := mustAgentLoop(t, cfg, b, p)
	t.Cleanup(al.Close)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x39}, 32)))
	t.Cleanup(store.Close)
	al.WireSysagentDeps(&systools.Deps{Home: home, GetCfg: al.GetConfig, CredStore: store})
	h := newWSHandler(b, al, "")
	h.home = home
	al.SetAddressDeps(gatewayAddressDeps{h: h})
	bin := t.TempDir()
	fake := "#!/bin/sh\n[ \"$#\" -eq 4 ] && [ \"$1\" = api ] && [ \"$2\" = user ] && [ \"$3\" = --jq ] && [ \"$4\" = .login ] || exit 81\n[ \"$GH_TOKEN\" = tok_valid_12345 ] || exit 82\nprintf 'octocat\\n'\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "gh"), []byte(fake), 0700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	svc := programs.NewService(al.GetConfig, func() (*credentials.Store, error) { return store, nil }, filepath.Join(home, "programs", "connections.json"))
	_, err := svc.Publish("github", "tok_valid_12345", nil, nil)
	require.NoError(t, err)
	svc.Wait()
	require.Equal(t, "connected", svc.Connection("github").State)
	f := &programsPeerFixture{al: al, bus: b, provider: p, home: home, work: filepath.Join(home, "workspaces", "sales", "work")}
	require.NoError(t, os.MkdirAll(f.work, 0700))
	dev, err := al.GetSessionStore().GetOrCreateMainSession("sales", "dev")
	require.NoError(t, err)
	ops, err := al.GetSessionStore().GetOrCreateMainSession("sales", "ops")
	require.NoError(t, err)
	f.dev, f.ops = dev.ID, ops.ID
	f.events = al.SubscribeEvents(256)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- al.Run(ctx) }()
	var shutdown sync.Once
	f.shutdown = func() {
		shutdown.Do(func() {
			cancel()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(10 * time.Second):
				t.Fatal("real peer fixture's running loop did not stop")
			}
			al.UnsubscribeEvents(f.events.ID)
			al.Close()
			store.Close()
			b.Close()
		})
	}
	t.Cleanup(f.shutdown)
	return f
}

func (f *programsPeerFixture) waitEnds(t *testing.T, sessionIDs ...string) {
	t.Helper()
	pending := map[string]bool{}
	for _, id := range sessionIDs {
		pending[id] = true
	}
	deadline := time.After(15 * time.Second)
	for len(pending) > 0 {
		select {
		case event := <-f.events.C:
			if event.Kind != agent.EventKindTurnEnd {
				continue
			}
			payload, ok := event.Payload.(agent.TurnEndPayload)
			require.True(t, ok)
			if pending[payload.SessionID] {
				require.Equal(t, agent.TurnEndStatusCompleted, payload.Status, "the actual request/command turn completed normally")
				delete(pending, payload.SessionID)
			}
		case <-deadline:
			t.Fatalf("real peer turns did not end: %v", pending)
		}
	}
}

func (f *programsPeerFixture) scriptCount(sid, call string) string {
	path := filepath.Join(f.work, call+"-count")
	command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; printf 'PROGRAM_PEER_CHILD_RAN'", path)
	f.provider.setScript(sid, []providers.LLMResponse{{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "bash", Arguments: map[string]any{"command": command}}}}, wu5StopGW("peer count completed")})
	return path
}

func programsPeerCount(t *testing.T, f *programsPeerFixture, path, call string) (string, string) {
	t.Helper()
	result := f.provider.result(f.ops, call)
	require.Contains(t, result, "PROGRAM_PEER_CHILD_RAN", "instrument: real Ops bash executed; a refused/missed request cannot establish zero")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.TrimSpace(string(raw)), result
}
