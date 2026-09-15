// This test file uses //go:build !cgo so it compiles when CGO is disabled.
// When CGO is enabled, pkg/gateway imports pkg/channels/matrix which requires
// the libolm system library (olm/olm.h). If that library is installed,
// remove this build constraint and run tests normally.
//
// Regression coverage for the createAgent/updateAgent/updateAgentTools "reload
// race": POST /api/v1/agents (and its siblings that persist agent config and
// call TriggerReload) historically fired the config reload and returned
// success WITHOUT waiting for the in-memory AgentRegistry to actually pick up
// the change. In production, TriggerReload's underlying reloadFunc only
// enqueues work onto a buffered channel (runningServices.manualReloadChan,
// pkg/gateway/gateway.go) consumed by a separate goroutine that performs the
// real registry rebuild (executeReload -> handleConfigReload ->
// ReloadProviderAndConfig) — so "TriggerReload returned nil" and "the
// registry now contains the new/updated agent" are DIFFERENT events,
// separated by however long that consumer goroutine takes to run (observed
// live: up to ~300ms). A client that creates an agent and immediately opens a
// session against it (POST /api/v1/sessions -> GetAgentStore -> the SAME
// registry) could get a spurious 400 "agent not found" for an agent that had
// already been durably persisted and 201-Created.
//
// deleteAgent already closed this gap via triggerReloadAndWait (rest_auth.go),
// which polls IsReloadPending() until the SAME registry swap actually
// completes (or a 5s deadline). createAgent, updateAgent (its Soul-triggered
// reload branch), and updateAgentTools did not use it — this file proves the
// race on createAgent end-to-end via the real HTTP path, and proves the same
// "handler returns before the registry sync completes" defect on updateAgent
// and updateAgentTools via the IsReloadPending() observable.

package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// wireAsyncReload wires api.agentLoop's reload function to mimic the shape of
// the REAL production reload pipeline: the function returns immediately
// (like runningServices.reloadTrigger's buffered channel send in
// pkg/gateway/gateway.go), while the actual registry rebuild — reading the
// just-persisted config.json and swapping the live registry via
// ReloadProviderAndConfig, then ClearReloadPending() — happens on a separate
// goroutine after reloadDelay. This reproduces the real gap (TriggerReload
// returning success is not the same event as "the registry observed the
// change") deterministically instead of relying on incidental goroutine
// scheduling: reloadDelay (a handful of milliseconds) is comfortably larger
// than the microseconds an httptest round trip through the handler takes, so
// an immediate follow-up call is guaranteed to run before the fake worker's
// registry swap — a bug in the handler under test (not a flaky race) is what
// makes the assertions below fail without the fix. This delay lives only in
// the test's stand-in for the real gateway's manualReloadChan consumer loop —
// it is not, and must not become, a time.Sleep in the production fix itself.
func wireAsyncReload(t *testing.T, api *restAPI, reloadDelay time.Duration) {
	t.Helper()
	api.agentLoop.SetReloadFunc(func() error {
		go func() {
			time.Sleep(reloadDelay)
			newCfg, err := config.LoadConfig(api.configPath())
			if err != nil {
				api.agentLoop.ClearReloadPending()
				return
			}
			// ReloadProviderAndConfig performs the same atomic registry swap
			// production's handleConfigReload does (pkg/agent/loop.go); the mock
			// provider mirrors what mustAgentLoop already wired the loop with.
			if rlErr := api.agentLoop.ReloadProviderAndConfig(
				context.Background(), &restMockProvider{}, newCfg,
			); rlErr != nil {
				api.agentLoop.ClearReloadPending()
				return
			}
			// Mirrors executeReload's deferred ClearReloadPending: fires only
			// after the registry swap above has fully completed.
			api.agentLoop.ClearReloadPending()
		}()
		return nil
	})
}
