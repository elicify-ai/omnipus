package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// D-A oracle: docs/agents.md, Workers and delegation: the worker's saved
// Additional CLI arguments and Environment overrides apply to its runs.
// The registry, not the already-correct store/PUT response, supplies the
// instance read by external_dispatch.go::prepareRunOptions.
func TestOpencode226R5_ExecutorOnlyUpdateReachesLiveDelegationRegistry(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		args string
		env  map[string]string
	}{
		{
			name: "replace_arguments_only", body: `{"executor":{"cli_args":"--oc226-setting after"}}`,
			args: "--oc226-setting after", env: opencode226R5OriginalEnv(),
		},
		{
			name: "replace_environment_only", body: `{"executor":{"env_overrides":{"OC226_WORKER_SENTINEL":"r5-env-after"}}}`,
			args: opencode226R5OriginalArgs,
			env:  map[string]string{opencode226R5EnvKey: "r5-env-after", "OC226_WORKER_KEEP": "r5-env-kept"},
		},
		// ExecutorConfigUpdate replaces cli_args, and docs/agents.md says
		// removing an environment row removes that variable. Neither unlocks
		// the kind/tool: clear editable settings without inventing a runtime switch.
		{
			name: "clear_arguments_only", body: `{"executor":{"cli_args":""}}`,
			args: "", env: opencode226R5OriginalEnv(),
		},
		{
			name: "remove_environment_only", body: `{"executor":{"env_overrides_remove":["OC226_WORKER_SENTINEL"]}}`,
			args: opencode226R5OriginalArgs, env: map[string]string{"OC226_WORKER_KEEP": "r5-env-kept"},
		},
		{
			name: "clear_all_editable_settings", body: `{"executor":{"cli_args":"","env_overrides_remove":["OC226_WORKER_SENTINEL","OC226_WORKER_KEEP"]}}`,
			args: "", env: map[string]string{},
		},
		{
			name: "empty_environment_is_not_clear", body: `{"executor":{"env_overrides":{}}}`,
			args: opencode226R5OriginalArgs, env: opencode226R5OriginalEnv(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, id := opencode226R5WorkerAPI(t)
			before := liveAgent(t, api, id)
			require.NotNil(t, before.Subagents)
			require.NotNil(t, before.Subagents.Executor)
			require.Equal(t, opencode226R5OriginalArgs, before.Subagents.Executor.CLIArgs, "instrument: live worker really starts with A")
			require.Equal(t, opencode226R5OriginalEnv(), before.Subagents.Executor.EnvOverrides)
			var fullReloads atomic.Int32
			api.agentLoop.SetReloadFunc(func() error { fullReloads.Add(1); return nil })

			w := putAgentJSON(t, api, id, tc.body)
			require.Equal(t, http.StatusOK, w.Code, "executor-only PUT must save: %s", w.Body.String())
			got := liveAgent(t, api, id)
			require.NotNil(t, got.Subagents)
			require.NotNil(t, got.Subagents.Executor)
			assert.Equal(t, tc.args, got.Subagents.Executor.CLIArgs,
				"D-A: executor-only save must reach the LIVE delegation registry, not merely the store")
			if len(tc.env) == 0 {
				assert.Len(t, got.Subagents.Executor.EnvOverrides, 0,
					"D-A: clearing every saved variable must leave ZERO live worker environment overrides")
			} else {
				assert.Equal(t, tc.env, got.Subagents.Executor.EnvOverrides,
					"D-A: saved worker environment must reach the LIVE delegation registry; omitted names stay unchanged")
			}
			assert.Equal(t, config.ExecutorKindExternalCLI, got.Subagents.Executor.Kind)
			assert.Equal(t, "opencode", got.Subagents.Executor.CLI)
			assert.Zero(t, fullReloads.Load(), "executor updates must use the model-change single-agent path, not a service restart")
		})
	}
}

// D-A's literal whole-executor-clear requirement has no supported write shape.
// ExecutorConfigUpdate rejects kind/cli/cli_path; docs/agents.md locks runtime
// kind and tool. Keep the gap loud, not skipped or disguised as a supported
// editable-field clear. Resolving it needs a contract/scope decision, not a guessed payload.
func TestOpencode226R5_WholeExecutorClearBlockedByWriteContract(t *testing.T) {
	t.Fatal("BLOCKED: whole-executor clear is not implemented in ExecutorConfigUpdate; runtime kind/tool are locked — required by RED round 5 D-A; supported cli_args/env clears have separate real-REST tests")
}

// D-B oracle: docs/agents.md, Workers and delegation: Send a test message runs
// a real request through this worker's tool, with its saved environment. Only
// that external process edge is faked; HTTP routing and env preparation are real.
func TestOpencode226R5_SmokeTestUsesWorkerEnvAndScrubsGateway(t *testing.T) {
	for _, withAgent := range []bool{true, false} {
		name := "saved_worker"
		if !withAgent {
			name = "agentless_control"
		}
		t.Run(name, func(t *testing.T) {
			api, id := opencode226R5WorkerAPI(t)
			t.Setenv("OMNIPUS_R5_GATEWAY_ONLY", "r5-not-for-child")
			t.Setenv(opencode226R5EnvKey, "r5-parent-must-not-win")
			t11bSavedProgram(t, api, "opencode")
			fake := &capturingFakeDriver{}
			factoryCalls := 0
			original := smokeTestNewDriver
			smokeTestNewDriver = func(cli string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
				assert.Equal(t, "opencode", cli)
				factoryCalls++
				return fake, nil
			}
			t.Cleanup(func() { smokeTestNewDriver = original })
			request := gen.ExecutorSmokeTestRequest{Cli: gen.ExternalCliToolOpencode}
			if withAgent {
				request.AgentId = &id
			}
			raw, err := json.Marshal(request)
			require.NoError(t, err)
			code, response := postExecutorSmokeTest(t, api, string(raw))
			require.Equal(t, http.StatusOK, code)
			require.True(t, response.Ok, "smoke must reach the fake process: %+v", response.Error)
			require.NotNil(t, response.ResponseText)
			assert.Equal(t, "2", *response.ResponseText)
			assert.Nil(t, response.Error)
			require.Equal(t, 1, factoryCalls, "instrument: exactly one driver was constructed")
			fake.mu.Lock()
			opts, captured := fake.gotOpts, fake.captured
			fake.mu.Unlock()
			require.True(t, captured, "instrument: the real handler must actually call Run")
			require.NotEmpty(t, opts.Env, "instrument: a nil child env would inherit the gateway environment")
			env := opencode226R5EnvMap(t, opts.Env)
			for key := range env {
				assert.False(t, strings.HasPrefix(strings.ToUpper(key), "OMNIPUS_"),
					"D-B: gateway-only environment name %q leaked to the test-message runner", key)
			}
			require.NotEmpty(t, os.Getenv("PATH"), "instrument: parent PATH must be populated")
			assert.Equal(t, os.Getenv("PATH"), env["PATH"], "agentless and saved-worker smoke retain the normal scrubbed process env")
			if withAgent {
				assert.Equal(t, "r5-env-before", env[opencode226R5EnvKey],
					"D-B: Send a test message must pass the saved worker env_overrides to RunOptions.Env")
				assert.Equal(t, "r5-env-kept", env["OC226_WORKER_KEEP"])
			} else {
				assert.NotContains(t, env, opencode226R5EnvKey, "agentless smoke must not borrow saved worker or arbitrary parent overrides")
				assert.NotContains(t, env, "OC226_WORKER_KEEP")
			}
			assert.Equal(t, withAgent, response.UsedAgentWorkspace)
		})
	}
}
