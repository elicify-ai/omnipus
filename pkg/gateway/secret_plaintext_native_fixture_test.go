package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	q3PublicControl  = "WU4B-Q3-PUBLIC-CONTROL"
	q3TurnFinished   = "WU4B-Q3-TURN-FINISHED"
	q3PlaintextValue = "WU4B-Q3-REJECTED-PLAINTEXT"
	q3ForbiddenCall  = "wu4b-q3-forbidden"
	q3ControlTool    = "wu4b_q3_public_echo"
)

// Only the model process edge is scripted. Inputs are captured before the
// next reply; the scripted final text cannot impersonate a tool refusal.
type q3NativeProvider struct {
	mu      sync.Mutex
	tool    string
	args    map[string]any
	prompts [][]providers.Message
	inputs  [][]byte
}

func (*q3NativeProvider) GetDefaultModel() string { return "test-model" }
func (p *q3NativeProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	input, err := q3SnapshotMessages(messages)
	if err != nil {
		return nil, err
	}
	p.inputs = append(p.inputs, input)
	p.prompts = append(p.prompts, append([]providers.Message(nil), messages...))
	if len(p.prompts) == 1 {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{
			{ID: "wu4b-q3-control", Type: "function", Name: q3ControlTool, Arguments: map[string]any{"label": q3PublicControl}},
			{ID: q3ForbiddenCall, Type: "function", Name: p.tool, Arguments: p.args},
		}}, nil
	}
	return &providers.LLMResponse{Content: q3TurnFinished, FinishReason: "stop"}, nil
}

// The positive control is unrelated to credential selection. A global scrub
// that hides all arguments or refuses all tools would break this real call.
type q3PublicEcho struct{ tools.BaseTool }

func (*q3PublicEcho) Name() string           { return q3ControlTool }
func (*q3PublicEcho) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (*q3PublicEcho) Description() string    { return "QA public non-secret echo control" }
func (*q3PublicEcho) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"label": map[string]any{"type": "string"}}, "required": []string{"label"}}
}
func (*q3PublicEcho) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	label, ok := args["label"].(string)
	if !ok {
		return tools.ErrorResult("public control label is required")
	}
	return tools.NewToolResult(label)
}

type q3ApprovalEdge struct {
	mu       sync.Mutex
	requests []agent.PolicyApprovalReq
}

func (a *q3ApprovalEdge) RequestApproval(_ context.Context, req agent.PolicyApprovalReq) (bool, string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, req)
	return false, "user", false // deterministic UI denial if incorrectly reached
}

// ToolCall.Name/Arguments are json:"-". Explicitly inspect them as well as
// the ordinary message JSON so the capture cannot overlook raw internal args.
func q3SnapshotMessages(messages []providers.Message) ([]byte, error) {
	raw, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	var all bytes.Buffer
	all.Write(raw)
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			args, marshalErr := json.Marshal(call.Arguments)
			if marshalErr != nil {
				return nil, marshalErr
			}
			all.Write(args)
			if call.Function != nil {
				all.WriteString(call.Function.Arguments)
			}
		}
	}
	return all.Bytes(), nil
}

func q3AssertValueFree(t *testing.T, label string, raw []byte) {
	t.Helper()
	assert.Zero(t, bytes.Count(raw, []byte(q3PlaintextValue)), "rejected plaintext leaked into %s", label)
}

func q3AssertPersistedTree(t *testing.T, home string) {
	t.Helper()
	controlFiles := 0
	require.NoError(t, filepath.WalkDir(home, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		q3AssertValueFree(t, "persisted file "+path, raw)
		if bytes.Contains(raw, []byte(q3PublicControl)) {
			controlFiles++
		}
		return nil
	}))
	assert.Positive(t, controlFiles, "instrument: public control really reached a persisted artifact")
}

func q3AwaitCompletedTurn(t *testing.T, msgBus *bus.MessageBus) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case message := <-msgBus.OutboundChan():
			q3AssertValueFree(t, "outbound message", []byte(message.Content))
			if message.Content == q3TurnFinished {
				return
			}
		case <-deadline.C:
			t.Fatal("real native turn did not publish its completed reply; no no-echo pass can be inferred")
		}
	}
}

// Keep this fixture's network edge typed to the actual provider interface.
func q3OfflineListing(context.Context, catalog.Protocol, string, string, providers.URLChecker) ([]string, error) {
	return []string{"fixture-model"}, nil
}

type q3NativeFixture struct {
	memoryBefore                         []byte
	home, sid, logPath, configPath       string
	al                                   *agent.AgentLoop
	bus                                  *bus.MessageBus
	handler                              *WSHandler
	conn                                 *wsConn
	provider                             *q3NativeProvider
	store                                *credentials.Store
	approver                             *q3ApprovalEdge
	saves, reloads, reconciles, listings int
	configBefore, storeBefore            []byte
	cancel                               context.CancelFunc
	runDone                              chan error
	joined                               bool
}

func newQ3NativeFixture(t *testing.T, name, policy string) *q3NativeFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	args := map[string]any{"name": "openai", "api_key": q3PlaintextValue, "expected_revision": "absent"}
	if name == "add_mcp_server" {
		args = map[string]any{"name": "q3-server", "command": "q3-never-spawned", "env": map[string]any{"TOKEN": q3PlaintextValue}, "expected_revision": "absent"}
	}
	if name == "configure_channel" {
		args = map[string]any{"id": "telegram", "token": q3PlaintextValue, "expected_revision": "absent"}
	}
	provider := &q3NativeProvider{tool: name, args: args}
	cfg := secretCardAgentConfig("omnipus", "Omnipus", false)
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
	cfg.Sandbox.ToolPolicies[name] = policy
	cfg.Sandbox.ToolPolicies[q3ControlTool] = "allow"
	cfg.Sandbox.AuditLog = true
	cfg.Providers = []*config.ModelConfig{}
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{}
	cfg.Channels = map[string]config.ChannelInstanceConfig{}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, provider)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x58}, 32)))
	t.Cleanup(store.Close)
	require.NoError(t, store.Set("q3-preexisting-control", "WU4B-Q3-EXISTING-CONTROL"))
	f := &q3NativeFixture{home: home, al: al, bus: msgBus, provider: provider, store: store, approver: &q3ApprovalEdge{}, configPath: filepath.Join(home, "config.json"), logPath: filepath.Join(home, "native-turn.jsonl")}
	var mutationMu sync.Mutex
	al.WireSysagentDeps(&systools.Deps{
		Home: home, ConfigPath: f.configPath, GetCfg: al.GetConfig, CredStore: store,
		MutateConfig: func(fn func(*config.Config) error) error {
			mutationMu.Lock()
			defer mutationMu.Unlock()
			return fn(al.GetConfig())
		},
		SaveConfigLocked: func(c *config.Config) error { f.saves++; return config.SaveConfig(f.configPath, c) },
		ReloadFunc:       func() error { f.reloads++; return nil },
		ReconcileMCP:     func(context.Context) error { f.reconciles++; return nil },
		ListProviderModels: func(ctx context.Context, p catalog.Protocol, base, key string, checker providers.URLChecker) ([]string, error) {
			f.listings++
			return q3OfflineListing(ctx, p, base, key, checker)
		},
	})
	al.RegisterTool(&q3PublicEcho{})
	require.Equal(t, policy, al.ResolveApprovalToolPolicy("omnipus", name), "instrument: real call uses the requested policy")
	al.SetToolApprover(f.approver)
	main, err := al.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, "omnipus")
	require.NoError(t, err)
	f.sid = main.ID
	owner := "wu4b-q3-owner"
	require.NoError(t, al.GetSessionStore().SetMeta(main.ID, session.MetaPatch{Owner: &owner}))
	require.NoError(t, config.SaveConfig(f.configPath, al.GetConfig()))
	f.memoryBefore, err = json.Marshal(al.GetConfig())
	require.NoError(t, err)
	f.configBefore, err = os.ReadFile(f.configPath)
	require.NoError(t, err)
	f.storeBefore, err = os.ReadFile(filepath.Join(home, "credentials.json"))
	require.NoError(t, err)
	f.handler = newWSHandler(msgBus, al, "")
	f.conn = makeTestConn()
	f.conn.userID = owner
	bindTestConnToSession(f.handler, "wu4b-q3-chat", f.sid, f.conn)
	t.Cleanup(f.conn.close)
	level := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	t.Cleanup(func() { logger.SetLevel(level) })
	require.NoError(t, logger.EnableFileLogging(f.logPath))
	t.Cleanup(logger.DisableFileLogging)
	logger.InfoCF("qa", q3PublicControl, map[string]any{"tool": name})
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel, f.runDone = cancel, make(chan error, 1)
	go func() { f.runDone <- al.Run(ctx) }()
	t.Cleanup(func() {
		if !f.joined {
			f.join(t)
		}
	})
	return f
}

func (f *q3NativeFixture) join(t *testing.T) {
	t.Helper()
	f.cancel()
	f.al.Close()
	require.NoError(t, <-f.runDone)
	f.handler.Wait()
	f.joined = true
}
