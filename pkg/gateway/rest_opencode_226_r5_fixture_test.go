package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Harmless fixture input, not provider credentials or host-CLI options. The
// synthetic CLI echoes these across a process boundary without a model call.
const (
	opencode226R5OriginalArgs  = "--oc226-setting before"
	opencode226R5EnvKey        = "OC226_WORKER_SENTINEL"
	opencode226R5OriginalModel = "openrouter/oc226-r5-before"
	opencode226R5NextModel     = "openrouter/oc226-r5-after"
	opencode226R5Answer        = "CAPTURE-OK" // supplied plain.stdout.ndjson's one text event
)

func opencode226R5OriginalEnv() map[string]string {
	return map[string]string{opencode226R5EnvKey: "r5-env-before", "OC226_WORKER_KEEP": "r5-env-kept"}
}

func opencode226R5WorkerAPI(t *testing.T) (*restAPI, string) {
	t.Helper()
	api := buildExecutorTestAPI(t)
	const id = "oc226-r5-worker"
	record := config.AgentConfig{
		ID: id, Name: "Round-five external worker", Type: config.AgentTypeWorker,
		Description: "Exercise saved external worker settings.",
		Home:        filepath.Join(api.homePath, "agents", id),
		Model:       &config.AgentModelConfig{Primary: opencode226R5OriginalModel},
		Tools:       api.agentLoop.GetConfig().Agents.List[0].Tools,
		Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{
			Kind: config.ExecutorKindExternalCLI, CLI: "opencode",
			CLIArgs: opencode226R5OriginalArgs, EnvOverrides: opencode226R5OriginalEnv(),
		}},
	}
	require.NoError(t, agentstore.New(api.homePath).Create(id, &record))
	cfg := api.agentLoop.GetConfig()
	cfg.Agents.List = append(cfg.Agents.List, record) // fixture setup, before any run
	_, err := api.agentLoop.UpsertAgentFast(cfg, id)
	require.NoError(t, err, "instrument: construct the initial live instance through the real fast path")
	seedTestWorkspaceMembershipForIDs(t, []string{id})
	seedDefaultDelegationGraph(t, []string{"test-agent", id})
	classifier := agent.NewSteerRecordClassifier(api.agentLoop.GetSessionLifecycleStore(), api.agentLoop.GetSessionStore())
	api.agentLoop.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	return api, id
}

func opencode226R5EnvMap(t *testing.T, entries []string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		require.True(t, ok, "child environment entry must have a name/value separator")
		_, duplicate := out[key]
		assert.False(t, duplicate, "child environment name %q appears twice", key)
		out[key] = value
	}
	return out
}

func opencode226R5Parent(t *testing.T, api *restAPI) string {
	t.Helper()
	store := api.agentLoop.GetSessionStore()
	parent, err := store.NewSession(session.SessionTypeChat, "webchat", "test-agent")
	require.NoError(t, err)
	workspaceID := testHarnessWorkspaceMembershipID
	require.NoError(t, store.SetMeta(parent.ID, session.MetaPatch{WorkspaceID: &workspaceID}))
	return parent.ID
}

func opencode226R5Dispatch(t *testing.T, api *restAPI, parent, id, task string) steer.LaunchResult {
	t.Helper()
	launcher := agent.NewSteerLauncher(api.agentLoop)
	child, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parent, TargetAgentID: id, Task: task,
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: task},
	})
	require.NoError(t, err, "instrument: launch a real external-CLI delegation")
	result, err := launcher.Dispatch(context.Background(), child.SessionID, child.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, result.State)
	return child
}

func opencode226R5AssertCompleted(t *testing.T, api *restAPI, child steer.LaunchResult) {
	t.Helper()
	// A deadline is only a liveness guard. Success means the durable outcome
	// AND the real turn's teardown completed, never merely that time passed.
	require.Eventually(t, func() bool {
		record, err := api.agentLoop.GetSessionLifecycleStore().Load(child.SessionID)
		return err == nil && record.Terminal() && api.agentLoop.GetActiveTurnBySession(child.SessionID) == nil
	}, 10*time.Second, 5*time.Millisecond, "real delegated run must settle after its process is released")
	record, err := api.agentLoop.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleCompleted, record.State, "the original run must finish, not be cancelled/restarted by a settings save")
	assert.Equal(t, child.Generation, record.Generation, "settings save must not revive or replace the run")
	// Exact assistant-message multiplicity is independently asserted by D-C;
	// keeping it there prevents that defect from masking D-A's next-run oracle.
}

func opencode226R5FlagValues(args []string, flag string) []string {
	var values []string
	for i, arg := range args {
		if arg == "--" {
			break // never mistake the composed task text for a CLI flag
		}
		if arg == flag && i+1 < len(args) {
			values = append(values, args[i+1])
		}
	}
	return values
}
