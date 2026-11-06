package agent

// Chief checks C1/C2: real native turns, never hand-built executor stages.
// The provider is scripted; only external bash execution / agent publication
// is paused. Admission, T8's redactor, dispatch and transcript writes stay real.
import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pause at the process edge, after beginStartedTool has saved its receipt;
// release delegates to the actual bash tool, not a manufactured tool result.
type uat6cChiefPausedBash struct {
	tools.Tool
	entered chan map[string]any
	release <-chan struct{}
}

func (tool *uat6cChiefPausedBash) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	select {
	case tool.entered <- args:
	case <-ctx.Done():
		return tools.ErrorResult("chief bash observation cancelled")
	}
	select {
	case <-tool.release:
		return tool.Tool.Execute(ctx, args)
	case <-ctx.Done():
		return tools.ErrorResult("chief bash observation cancelled")
	}
}

type uat6cChiefTurnResult struct {
	text string
	err  error
}

func uat6cChiefStartTurn(t *testing.T, al *AgentLoop, release chan struct{}) (string, <-chan uat6cChiefTurnResult, func()) {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	result, finished := make(chan uat6cChiefTurnResult, 1), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() {
		unblock()
		cancel()
		uat6cReceive(t, finished, "chief real-turn writer cleanup")
	})
	go func() {
		defer close(finished)
		text, runErr := al.runAgentLoop(ctx, inst, processOptions{
			SessionKey: "agent:mia:session:" + meta.ID, Channel: "webchat", ChatID: meta.ID,
			UserMessage: "perform the chief fixture check", UserInitiated: true, DefaultResponse: defaultResponse,
			TranscriptSessionID: meta.ID, TranscriptStore: store,
		})
		result <- uat6cChiefTurnResult{text: text, err: runErr}
	}()
	return meta.ID, result, unblock
}

func Test6cUAT_ChiefC1_RealBashTurnPersistsRunningCommand(t *testing.T) {
	// C1's independent command oracle: this exact provider input must survive
	// the real recordToolCalls/admittedToolCall/validateCall/dispatch chain.
	const command = "echo 6c-chief-command"
	provider := testutil.NewScenario().WithToolCalls([]providers.ToolCall{{
		ID: string(uat6cCall), Function: &providers.FunctionCall{Name: "bash", Arguments: `{"command":"echo 6c-chief-command"}`},
	}}).WithText("chief bash complete")
	al := newAutoTestLoop(t, provider, false, func(cfg *config.Config) {
		cfg.Sandbox.ToolPolicies = map[string]string{"bash": "allow"}
	})
	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"bash": "allow"}, Policies: map[string]config.ToolPolicy{"bash": "allow"}})
	realBash, ok := inst.Tools.Get("bash")
	require.True(t, ok)
	require.IsType(t, &tools.ExecTool{}, realBash, "instrument: delegation must reach the actual registered shell tool")
	release := make(chan struct{})
	paused := &uat6cChiefPausedBash{Tool: realBash, entered: make(chan map[string]any, 1), release: release}
	inst.Tools.RegisterReplacing(paused)
	sid, result, unblock := uat6cChiefStartTurn(t, al, release)
	args := map[string]any{"command": command}
	require.Equal(t, args, uat6cReceive(t, paused.entered, "real bash execution boundary"))
	want := session.ToolCall{ID: uat6cCall, Tool: "bash", Status: "running", Parameters: args}
	require.Equal(t, []session.ToolCall{want}, toolCallsFor(t, al.GetSessionStore(), sid, uat6cCall), "C1: the genuine started record retains the command before bash returns")
	_, source := uat6cOnlyToolSource(t, al.GetSessionStore(), sid)
	require.Equal(t, []session.ToolCall{want}, source.ToolCalls, "C1: inspect the physical running source, not a later settlement")
	t.Logf("C1 real-turn running bash Parameters=%v", source.ToolCalls[0].Parameters)
	unblock()
	got := uat6cReceive(t, result, "real bash turn completion")
	require.NoError(t, got.err)
	assert.Equal(t, "chief bash complete", got.text)
	assert.Equal(t, 2, provider.CallCount())
	assert.Contains(t, toolResultText(t, provider, string(uat6cCall)), "6c-chief-command", "instrument: the actual bash command ran after releasing the observation barrier")
}

// This canary is deliberately fake, never an environment credential.
const uat6cChiefFakeSecret = "FAKE-SECRET-VALUE-123"

func uat6cChiefCheckTranscriptBytes(t *testing.T, store *session.UnifiedStore, sid, stage string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(store.BaseDir(), sid, "transcript.jsonl"))
	require.NoError(t, err)
	require.NotEmpty(t, data, "instrument: the no-leak check must read actual persisted bytes")
	assert.Contains(t, string(data), string(uat6cCall), "instrument: the inspected transcript contains this exact call")
	assert.NotContains(t, string(data), uat6cChiefFakeSecret, "C2: no raw fake environment value may occur anywhere in persisted transcript bytes")
	assert.Contains(t, string(data), `"TOKEN":"[redacted]"`, "C2: the record contains the specified redacted value, not a missing argument")
	t.Logf("C2 %s transcript fake-secret matches=%d; redacted-token matches=%d", stage, bytes.Count(data, []byte(uat6cChiefFakeSecret)), bytes.Count(data, []byte(`"TOKEN":"[redacted]"`)))
	// Optional evidence export only; all assertions above always run. This
	// preserves the exact bytes for the chief's independent fixed-string grep.
	if dir := os.Getenv("OMNIPUS_6C_CHIEF_EVIDENCE_DIR"); dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "c2-"+stage+"-transcript.jsonl"), data, 0o600))
	}
}

func Test6cUAT_ChiefC2_RealAgentWriteRunningRecordUsesRedactedArguments(t *testing.T) {
	cfg, home := baseLoopDenialTestConfig(t)
	t.Setenv(config.EnvHome, home)
	cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
	cfg.Sandbox.ToolPolicies["update_agent"] = "allow"
	cfg.Tools.FilterSensitiveData = false // SR-1/SR-3 do not depend on the optional result filter.
	target := config.AgentConfig{ID: "chief-env-target", Name: "Chief external worker", Type: config.AgentTypeWorker, Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "codex"}}}
	cfg.Agents.List = append(cfg.Agents.List, target)
	store := agentstore.New(home)
	_, err := store.CreateState(target.ID, &target, "")
	require.NoError(t, err)
	state, err := store.ReadState(target.ID)
	require.NoError(t, err)
	args := map[string]any{"id": target.ID, "revision": state.Revision, "env_overrides": map[string]any{"TOKEN": uat6cChiefFakeSecret}}
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	provider := testutil.NewScenario().WithToolCall("ToolSearch", `{"names":["update_agent"]}`).WithToolCalls([]providers.ToolCall{{
		ID: string(uat6cCall), Function: &providers.FunctionCall{Name: "update_agent", Arguments: string(raw)},
	}}).WithText("chief update complete")
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)
	release, publishing := make(chan struct{}), make(chan string, 1)
	deps := &systools.Deps{Home: home, ConfigPath: filepath.Join(home, "config.json"), GetCfg: al.GetConfig, MutateConfig: al.MutateConfig, SaveConfigLocked: func(c *config.Config) error { return config.SaveConfig(filepath.Join(home, "config.json"), c) }}
	deps.UpsertAgentFastFunc = func(id string) error {
		publishing <- id
		<-release
		return l11274Publish(al, home, id)
	}
	deps.ToolCatalog = func() []tools.Tool { return append(systools.AllTools(deps), tools.GeneralBuiltinMetadata()...) }
	al.WireSysagentDeps(deps)
	caller, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	caller.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"ToolSearch": "allow", "update_agent": "allow"}, Policies: map[string]config.ToolPolicy{"update_agent": "allow"}})
	sid, result, unblock := uat6cChiefStartTurn(t, al, release)
	require.Equal(t, target.ID, uat6cReceive(t, publishing, "real update_agent publication"))
	stored, err := store.ReadState(target.ID)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"TOKEN": uat6cChiefFakeSecret}, stored.Agent.Subagents.Executor.EnvOverrides, "instrument: actual update_agent execution saved the original fake value, not the display mask")
	// C2 / r17 SR-1 and SR-3: derive the mask from the requirement, not a
	// production constant or observed output. Preserve every ordinary field.
	want := session.ToolCall{ID: uat6cCall, Tool: "update_agent", Status: "running", Parameters: map[string]any{
		"id": target.ID, "revision": state.Revision, "env_overrides": map[string]any{"TOKEN": "[redacted]"},
	}}
	assert.Equal(t, []session.ToolCall{want}, toolCallsFor(t, al.GetSessionStore(), sid, uat6cCall), "C2: beginStartedTool uses the real admitted redacted argument copy")
	_, source := uat6cOnlyToolSource(t, al.GetSessionStore(), sid)
	assert.Equal(t, []session.ToolCall{want}, source.ToolCalls, "C2: the physical running source never holds the execution secret")
	uat6cChiefCheckTranscriptBytes(t, al.GetSessionStore(), sid, "running")
	unblock()
	got := uat6cReceive(t, result, "real update_agent turn completion")
	require.NoError(t, got.err)
	assert.Equal(t, "chief update complete", got.text)
	assert.Equal(t, 3, provider.CallCount())
	uat6cChiefCheckTranscriptBytes(t, al.GetSessionStore(), sid, "completed")
}
