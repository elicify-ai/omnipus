package agent

// r17 8.8–8.9 and 10.2/10.3/10.5. These drive the real native tool loop.
// Only provider replies and the person's decision are process-edge fixtures.
// Event payloads, model requests/window/archive, transcript, and captured slog
// are checked; reconnect WebSocket snapshots and real child env are separate.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type t8DecisionRecorder struct {
	mu       sync.Mutex
	requests []PolicyApprovalReq
	allow    bool
	reason   string
}

func (a *t8DecisionRecorder) RequestApproval(_ context.Context, r PolicyApprovalReq) (bool, string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, r)
	return a.allow, a.reason, false
}
func (a *t8DecisionRecorder) snapshot() []PolicyApprovalReq {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]PolicyApprovalReq{}, a.requests...)
}
func t8WireRealAgentTools(t *testing.T, al *AgentLoop, home string) {
	t.Helper()
	deps := &systools.Deps{Home: home, ConfigPath: filepath.Join(home, "config.json"), GetCfg: al.GetConfig, MutateConfig: al.MutateConfig, SaveConfigLocked: func(c *config.Config) error { return config.SaveConfig(filepath.Join(home, "config.json"), c) }}
	deps.ToolCatalog = func() []tools.Tool { return append(systools.AllTools(deps), tools.GeneralBuiltinMetadata()...) }
	al.WireSysagentDeps(deps)
}
func t8AssertSecretAbsent(t *testing.T, label, secret string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	// Short values are searched as delimited values, never as an incidental
	// substring of an opaque UUID. Long test values cannot occur accidentally.
	pattern := regexp.MustCompile(`(^|[^A-Za-z0-9])` + regexp.QuoteMeta(secret) + `([^A-Za-z0-9]|$)`)
	assert.False(t, pattern.Match(raw), "SR-1 clear env value in %s", label)
}
func TestT8AgentEnv_RealLoopApprovalAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		name, policy, secret, reason string
		allow, filter                bool
		approvals                    int
	}{
		{"8.8 allow stores without approval", "allow", "zQ8x-longer-secret-0001", "", true, true, 0},
		{"8.9 ask stores after one approval", "ask", "zQ8x-longer-secret-0001", "", true, true, 1},
		{"10.2 short value with filtering off", "allow", "abc", "", true, false, 0},
		{"10.5 user refuses", "ask", "zQ8x-longer-secret-0001", "user", false, true, 1},
		{"10.5 cancelled approval", "ask", "zQ8x-longer-secret-0001", "cancel", false, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, home := baseLoopDenialTestConfig(t)
			t.Setenv(config.EnvHome, home)
			cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
			cfg.Sandbox.ToolPolicies["update_agent"] = tc.policy
			cfg.Tools.FilterSensitiveData = tc.filter
			target := config.AgentConfig{ID: "env-target", Name: "External worker", Type: config.AgentTypeWorker, Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "codex"}}}
			cfg.Agents.List = append(cfg.Agents.List, target)
			store := agentstore.New(home)
			_, err := store.CreateState(target.ID, &target, "")
			require.NoError(t, err)
			state, err := store.ReadState(target.ID)
			require.NoError(t, err)
			args := map[string]any{"id": target.ID, "revision": state.Revision, "env_overrides": map[string]any{"TOKEN": tc.secret}, "cli_args": "--model fixture-model"}
			raw, err := json.Marshal(args)
			require.NoError(t, err)
			prov := testutil.NewScenario().WithToolCall("ToolSearch", `{"names":["update_agent"]}`).WithToolCall("update_agent", string(raw)).WithText("write checked")
			al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), prov)
			t.Cleanup(al.Close)
			t8WireRealAgentTools(t, al, home)
			caller, ok := al.GetRegistry().GetAgent("mia")
			require.True(t, ok)
			caller.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"ToolSearch": "allow", "update_agent": config.ToolPolicy(tc.policy)}, Policies: map[string]config.ToolPolicy{"update_agent": config.ToolPolicy(tc.policy)}})
			decider := &t8DecisionRecorder{allow: tc.allow, reason: tc.reason}
			al.SetToolApprover(decider)
			sub := al.SubscribeEvents(256)
			defer al.UnsubscribeEvents(sub.ID)
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			defer slog.SetDefault(previous)
			sessionKey := agentSessionKey("mia", bus.InboundMessage{Channel: "cli", ChatID: "direct"})
			reply, err := al.ProcessDirectAs(context.Background(), "mia", "change the worker runtime", sessionKey)
			require.NoError(t, err)
			assert.Equal(t, "write checked", reply)
			requests := decider.snapshot()
			assert.Len(t, requests, tc.approvals, "exactly the ordinary update_agent policy approval, never an extra runtime gate")
			for _, request := range requests {
				assert.Equal(t, "update_agent", request.ToolName)
				t8AssertSecretAbsent(t, "approval arguments", tc.secret, request.Args)
			}
			after, err := store.ReadState(target.ID)
			require.NoError(t, err)
			if tc.allow {
				require.NotNil(t, after.Agent.Subagents)
				require.NotNil(t, after.Agent.Subagents.Executor)
				assert.Equal(t, map[string]string{"TOKEN": tc.secret}, after.Agent.Subagents.Executor.EnvOverrides, "execution receives the original value, not its display mask")
				assert.Equal(t, "--model fixture-model", after.Agent.Subagents.Executor.CLIArgs)
			} else {
				assert.Equal(t, state, after, "refused/cancelled approval persists nothing")
			}
			events := collectEventStream(sub.C)
			require.NotEmpty(t, events, "instrument: subscribed to the real loop before invoking it")
			t8AssertSecretAbsent(t, "published events", tc.secret, events)
			t8AssertSecretAbsent(t, "subsequent model requests", tc.secret, prov.AllRequests())
			t8AssertSecretAbsent(t, "slog capture", tc.secret, logs.String())
			sessions, err := al.GetSessionStore().ListSessions()
			require.NoError(t, err)
			require.NotEmpty(t, sessions)
			for _, meta := range sessions {
				transcript, transcriptErr := al.GetSessionStore().ReadTranscript(meta.ID)
				require.NoError(t, transcriptErr)
				t8AssertSecretAbsent(t, "persisted transcript", tc.secret, transcript)
			}
			// ProcessDirectAs's explicit scope key addresses the model archive;
			// transcript metadata has its own store-backed identity. Inspect the
			// actual caller's model store and the exact key supplied to this turn.
			archived, err := caller.Sessions.ReadArchive(context.Background(), sessionKey)
			require.NoError(t, err)
			require.NotEmpty(t, archived, "instrument: the archive check actually read the turn")
			t8AssertSecretAbsent(t, "model archive", tc.secret, archived)
			window := caller.Sessions.GetHistory(sessionKey)
			require.NotEmpty(t, window, "instrument: the model-window check actually read the turn")
			t8AssertSecretAbsent(t, "model window", tc.secret, window)
			files := 0
			require.NoError(t, filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				if filepath.Ext(path) != ".log" && filepath.Ext(path) != ".jsonl" {
					return nil
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				files++
				t8AssertSecretAbsent(t, fmt.Sprintf("log/archive file %s", entry.Name()), tc.secret, string(data))
				return nil
			}))
			assert.Positive(t, files, "instrument: the disk-sink search saw real artifacts")
		})
	}
}
