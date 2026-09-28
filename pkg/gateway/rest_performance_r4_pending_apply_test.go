// rest_performance_r4_pending_apply_test.go — #904 gate round 3:
//
//   - item 2a: the "saved but not applied yet" state is kept server-side
//     (PerformanceSettings.pending_apply, contracts/components/schemas/
//     PerformancePendingApply.yaml) and cleared only when a later refresh AND
//     registry reload both succeed (a later PUT, or a manual/automatic
//     reload);
//   - item 2b: a registry reload that RAN but whose rebuild failed is the
//     reload stage of performance_reload_failed, not a 200;
//   - item 3: the real cause of a config.json write failure is logged while
//     the response keeps the fixed cause class.
//
// Oracles come from the contract text of PerformancePendingApply and the PUT
// /performance description in contracts/openapi.yaml.

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// r4RebuildSecret stands in for credential-derived text a rebuild error can
// carry; it must never reach a PUT /performance response or log line.
const r4RebuildSecret = "r4-secret-ref-name"

// r4ReloadPipeline wires the production reload path into a test restAPI:
// newReloadTrigger -> manualReloadChan -> runReloadCycle, with an exec whose
// rebuild fails while fail is set. It shares one reloadOutcomeTracker with
// the restAPI and installs the pending-apply success hook, as boot does.
type r4ReloadPipeline struct {
	fail atomic.Bool
	runs atomic.Int32
}

func wireR4ReloadPipeline(t *testing.T, api *restAPI) *r4ReloadPipeline {
	t.Helper()
	p := &r4ReloadPipeline{}
	svc := &services{reloadOutcome: &reloadOutcomeTracker{}}
	svc.reloadOutcome.onSuccess = api.pendingApply.clearAfterReload
	api.reloadOutcome = svc.reloadOutcome
	svc.manualReloadChan = make(chan struct{}, 1)
	svc.reloadTrigger = newReloadTrigger(svc, api.agentLoop)
	api.agentLoop.SetReloadFunc(svc.reloadTrigger)
	exec := func(*config.Config) error {
		p.runs.Add(1)
		if p.fail.Load() {
			return errors.New("injected rebuild failure for " + r4RebuildSecret)
		}
		return nil
	}
	loadNext := func() (*config.Config, error) { return api.agentLoop.GetConfig(), nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-svc.manualReloadChan:
				runReloadCycle(api.agentLoop, svc, nil, 0, exec, loadNext)
			}
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
	return p
}

// r4Pending decodes GET /performance strictly into the generated type and
// returns its pending_apply (nil when absent).
func r4Pending(t *testing.T, api *restAPI) *gen.PerformancePendingApply {
	t.Helper()
	w := httptest.NewRecorder()
	api.HandlePerformance(w, httptest.NewRequest(http.MethodGet, "/api/v1/performance", nil))
	require.Equal(t, http.StatusOK, w.Code, "GET /performance: %s", w.Body.String())
	return r4DecodePending(t, w.Body.Bytes())
}

func r4DecodePending(t *testing.T, body []byte) *gen.PerformancePendingApply {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var ps gen.PerformanceSettings
	require.NoError(t, dec.Decode(&ps), "body must match PerformanceSettings: %s", body)
	return ps.PendingApply
}

// ---------------------------------------------------------------------------
// Item 2b: a failed rebuild is the reload stage, not 200.
// ---------------------------------------------------------------------------

func TestPerformancePut_RegistryRebuildFailure_IsReloadStage(t *testing.T) {
	api := newMTIAPI(t, "200")
	p := wireR4ReloadPipeline(t, api)
	p.fail.Store(true)
	logs := captureMTILogs(t)

	w := mtiPutPerf(t, api, `{"max_tool_iterations":300}`)
	require.Equal(t, http.StatusInternalServerError, w.Code,
		"a reload whose rebuild failed must not answer 200; body: %s", w.Body.String())
	require.GreaterOrEqual(t, p.runs.Load(), int32(1), "instrument: the reload pipeline must have run the rebuild")
	got := decodeReloadFailed(t, w.Body.Bytes())
	assert.Equal(t, gen.PerformanceReloadFailedDetailsStageReload, got.Details.Stage)
	assert.Equal(t, []gen.PerformanceReloadFailedDetailsChangedFields{
		gen.PerformanceReloadFailedDetailsChangedFieldsMaxToolIterations,
	}, got.Details.ChangedFields)
	assert.NotContains(t, w.Body.String(), r4RebuildSecret, "the rebuild's cause text must not reach the response")

	logs.mu.Lock()
	all := logs.buf.String()
	logs.mu.Unlock()
	for _, l := range strings.Split(all, "\n") {
		if strings.Contains(l, "rest: PUT /performance") {
			assert.NotContains(t, l, r4RebuildSecret, "PUT /performance log line carries the rebuild cause: %s", l)
		}
	}
}

// ---------------------------------------------------------------------------
// Item 2a: server-side pending-apply state.
// ---------------------------------------------------------------------------

func TestPerformancePendingApply_AbsentWhenEverythingApplied(t *testing.T) {
	api := newMTIAPI(t, "200")
	assert.Nil(t, r4Pending(t, api), "fresh gateway: nothing pending")
	w := mtiPutPerf(t, api, `{"max_tool_iterations":300}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Nil(t, r4DecodePending(t, w.Body.Bytes()), "a fully applied PUT carries no pending_apply")
}

func TestPerformancePendingApply_RefreshStage_ClearedByLaterSuccessfulPut(t *testing.T) {
	api := newMTIAPI(t, "200")
	breakInMemoryRefresh(t, api)
	w := mtiPutPerf(t, api, `{"max_parallel_agents":3}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())

	pending := r4Pending(t, api)
	require.NotNil(t, pending, "a refresh-stage failure must leave pending_apply on GET")
	assert.Equal(t, gen.PerformancePendingApplyStageRefresh, pending.Stage)
	assert.Equal(t, []gen.PerformancePendingApplyChangedFields{
		gen.PerformancePendingApplyChangedFieldsMaxParallelAgents,
	}, pending.ChangedFields)

	// Repair the cause, then a PUT that on its own needs no registry reload
	// (tools_on_demand only). While something is pending it must reload.
	require.NoError(t, os.Remove(filepath.Join(api.homePath, "entities", "agents", "broken.json")))
	var reloads atomic.Int32
	api.agentLoop.SetReloadFunc(func() error { reloads.Add(1); return nil })

	w = mtiPutPerf(t, api, `{"tools_on_demand":true}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.EqualValues(t, 1, reloads.Load(), "a PUT while settings are pending apply must reload the agent registry")
	assert.Nil(t, r4DecodePending(t, w.Body.Bytes()), "the successful PUT response carries no pending_apply")
	assert.Nil(t, r4Pending(t, api), "GET after the successful PUT: nothing pending")
	assert.EqualValues(t, 3, mtiGetPerf(t, api)["max_parallel_agents"], "the earlier saved value is now in force")
}

func TestPerformancePendingApply_ReloadStage_UnionUntilApplied(t *testing.T) {
	api := newMTIAPI(t, "200")
	api.agentLoop.SetReloadFunc(func() error { return errors.New("injected reload failure") })

	w := mtiPutPerf(t, api, `{"max_tool_iterations":300}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	w = mtiPutPerf(t, api, `{"goal_max_rounds":9}`)
	require.Equal(t, http.StatusInternalServerError, w.Code,
		"with a save pending, the next PUT reloads and so fails too; body: %s", w.Body.String())

	pending := r4Pending(t, api)
	require.NotNil(t, pending)
	assert.Equal(t, gen.PerformancePendingApplyStageReload, pending.Stage)
	assert.Equal(t, []gen.PerformancePendingApplyChangedFields{
		gen.PerformancePendingApplyChangedFieldsGoalMaxRounds,
		gen.PerformancePendingApplyChangedFieldsMaxToolIterations,
	}, pending.ChangedFields, "union of every failed PUT, in contract enum order")

	api.agentLoop.SetReloadFunc(func() error { return nil })
	w = mtiPutPerf(t, api, `{"tools_on_demand":false}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Nil(t, r4Pending(t, api), "a later PUT whose refresh and reload succeed clears pending_apply")
}

func TestPerformancePendingApply_ClearedByLaterSuccessfulReload(t *testing.T) {
	api := newMTIAPI(t, "200")
	p := wireR4ReloadPipeline(t, api)
	p.fail.Store(true)

	w := mtiPutPerf(t, api, `{"max_tool_iterations":300}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	require.NotNil(t, r4Pending(t, api), "instrument: the failed rebuild left the save pending")

	// A failing manual reload keeps it pending.
	require.Error(t, waitForReloadOrRebuildFailure(api))
	require.NotNil(t, r4Pending(t, api), "a reload whose rebuild failed must not clear pending_apply")

	// A successful manual/automatic reload (not a PUT) clears it.
	p.fail.Store(false)
	require.NoError(t, waitForReloadOrRebuildFailure(api))
	assert.Nil(t, r4Pending(t, api), "a successful reload started after the failure clears pending_apply")
}

// waitForReloadOrRebuildFailure triggers a reload the way POST /reload does
// (the shared trigger) and waits for it, reporting a failed rebuild.
func waitForReloadOrRebuildFailure(api *restAPI) error {
	if err := waitForReload(api.agentLoop); err != nil {
		return err
	}
	if api.reloadOutcome.lastFailed() {
		return errRegistryRebuildFailed
	}
	return nil
}

// A reload that started before the save was marked read an older config: its
// success must not clear the state; a PUT whose refresh predates a later mark
// must not clear it either.
//
// readSeq is held fixed at 1 against configReadsAtMark=0 throughout (1 > 0
// always holds), so only the reloadsStarted/markedAtReload dimension this
// test targets can decide the outcome — the readSeq/markedAtConfigRead
// dimension (round-4 finding: a reload cycle's start-sequence number alone
// does not prove its config was read after the mark) is covered on its own
// in rest_performance_r5_pending_apply_test.go.
func TestPerformancePendingApply_OnlyLaterApplyClears(t *testing.T) {
	var p performancePendingApply
	p.mark(gen.PerformanceReloadFailedDetailsStageReload,
		[]gen.PerformanceReloadFailedDetailsChangedFields{gen.PerformanceReloadFailedDetailsChangedFieldsToolsOnDemand}, 3, 0)

	p.clearAfterReload(3, 1)
	require.NotNil(t, p.snapshot(), "reload #3 started before the mark: must not clear")
	p.clearAfterReload(4, 1)
	require.Nil(t, p.snapshot(), "reload #4 started after the mark: clears")

	stale := p.epochNow()
	p.mark(gen.PerformanceReloadFailedDetailsStageRefresh, nil, 4, 1)
	p.clearIfEpoch(stale)
	require.NotNil(t, p.snapshot(), "a refresh that predates the latest mark must not clear it")
	p.clearIfEpoch(p.epochNow())
	require.Nil(t, p.snapshot())
}
