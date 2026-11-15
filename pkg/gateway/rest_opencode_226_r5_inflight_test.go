package gateway

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// D-A side-effect boundary, docs/agents.md: changing a worker's runtime does
// not change work already running. Existing model changes take the SAME path:
// rest_agents_update.go::persistAndReload -> rest_agents.go::fastAgentUpsert
// -> registry.go::UpsertAgentFast publishes a NEW instance, not an in-place
// mutation or cancellation. steer_reconstruct.go::reconstructSteeredTurn keeps
// the admitted turn's instance; steer_turn_body.go::runExternalCLISteeredTurn
// snapshots it for external dispatch. model_change_control proves that path
// with a real running child before executor-only saves are held to it.
func TestOpencode226R5_ExecutorOnlySavePreservesInFlightAndUpdatesNextDelegation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		nextArg   string
		nextEnv   string
		nextModel string
	}{
		{
			name: "model_change_control", body: `{"model":"openrouter/oc226-r5-after"}`,
			nextArg: "before", nextEnv: "r5-env-before", nextModel: opencode226R5NextModel,
		},
		{
			name: "executor_only_change", body: `{"executor":{"cli_args":"--oc226-setting after","env_overrides":{"OC226_WORKER_SENTINEL":"r5-env-after"}}}`,
			nextArg: "after", nextEnv: "r5-env-after", nextModel: opencode226R5OriginalModel,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, id := opencode226R5WorkerAPI(t)
			fixture := opencode226R5NewProcessFixture(t)
			opencode226R5SaveProgram(t, api, fixture.program)
			parent := opencode226R5Parent(t, api)
			var fullReloads atomic.Int32
			api.agentLoop.SetReloadFunc(func() error { fullReloads.Add(1); return nil })

			first := opencode226R5Dispatch(t, api, parent, id, "r5-first-run")
			decoder, conn, original := fixture.accept(t)
			require.Equal(t, []string{"before"}, opencode226R5FlagValues(original.Args, "--oc226-setting"), "instrument: first real process uses arguments A")
			require.Equal(t, "r5-env-before", original.EnvValue, "instrument: first real process uses environment A")
			require.Equal(t, []string{opencode226R5OriginalModel}, opencode226R5FlagValues(original.Args, "--model"))
			active := api.agentLoop.GetActiveTurnBySession(first.SessionID)
			require.NotNil(t, active, "instrument: PUT must happen while the first real turn is in flight")

			w := putAgentJSON(t, api, id, tc.body)
			require.Equal(t, http.StatusOK, w.Code, "settings-only PUT must succeed while work is in flight: %s", w.Body.String())
			afterSave := api.agentLoop.GetActiveTurnBySession(first.SessionID)
			require.NotNil(t, afterSave, "settings save must not cancel the in-flight turn")
			assert.Equal(t, active.TurnID, afterSave.TurnID, "settings save must not restart/replace the running turn")
			assert.Equal(t, int32(1), fixture.runs.Load(), "settings save must not start a replacement CLI process")
			assert.Zero(t, fullReloads.Load(), "use the same no-service-restart rebuild as a model change")
			opencode226R5FinishProcess(t, decoder, conn, original)
			opencode226R5AssertCompleted(t, api, first)

			// A NEW delegation, not Resume: a completed external episode releases
			// its retained driver, and must obtain the current registry instance.
			next := opencode226R5Dispatch(t, api, parent, id, "r5-next-run")
			nextDecoder, nextConn, current := fixture.accept(t)
			assert.NotEqual(t, first.SessionID, next.SessionID)
			assert.NotEqual(t, original.PID, current.PID, "instrument: next delegation starts a fresh process")
			assert.Equal(t, []string{tc.nextArg}, opencode226R5FlagValues(current.Args, "--oc226-setting"),
				"D-A: the NEXT delegated process must use the just-saved worker CLI arguments")
			assert.Equal(t, tc.nextEnv, current.EnvValue,
				"D-A: the NEXT delegated process must use the just-saved worker environment")
			assert.Equal(t, []string{tc.nextModel}, opencode226R5FlagValues(current.Args, "--model"), "next delegation uses the saved model")
			opencode226R5FinishProcess(t, nextDecoder, nextConn, current)
			opencode226R5AssertCompleted(t, api, next)
			assert.Equal(t, int32(2), fixture.runs.Load(), "exactly the original run and the explicitly requested next run may execute")
			assert.Empty(t, fixture.accepted, "no unrequested restart may be waiting at the process edge")
		})
	}
}
