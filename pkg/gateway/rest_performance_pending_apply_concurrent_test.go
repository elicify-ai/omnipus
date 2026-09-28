// rest_performance_pending_apply_concurrent_test.go — #904 gate, architect
// review finding 1 (docs/internal/handover/904-tool-iteration-limit-handover-2026-09-27.md,
// round-3 open findings; also the qa-lead round-3 CHECK).
//
// The pending-apply clearing protocol (rest_performance_pending_apply.go:
// performancePendingApply.epoch / markedAtReload / clearIfEpoch /
// clearAfterReload) is exercised elsewhere only SEQUENTIALLY:
//   - TestPerformancePendingApply_OnlyLaterApplyClears calls p.mark /
//     p.clearIfEpoch / p.clearAfterReload directly, one goroutine, never
//     through an HTTP handler, never through the real reload pipeline;
//   - the r4 gate tests (rest_performance_r4_pending_apply_test.go) drive
//     PUT /performance through the real handler, but one save at a time.
//
// In production, TWO real sources race on the SAME performancePendingApply:
// an HTTP handler goroutine calling putPerformance -> reloadAgentsAndConfirm
// -> pendingApply.mark / pendingApply.clearIfEpoch, and the reload-cycle
// goroutine (gateway_reload.go::runReloadCycle) calling
// reloadOutcomeTracker.finish -> the onSuccess hook -> pendingApply.
// clearAfterReload — wired at boot as
// `stg.runningServices.reloadOutcome.onSuccess = stg.api.pendingApply.clearAfterReload`
// (gateway_boot.go). No sequential, single-goroutine test can exercise that
// second goroutine's call at all.
//
// This file drives two PUT /performance saves CONCURRENTLY through the real
// handler (putPerformance -> reloadAgentsAndConfirm -> the production
// single-flight reload pipeline: newReloadTrigger, runReloadCycle), with the
// coalesced follow-up forced to fail at the reload stage, so both HTTP
// handler goroutines and the async onSuccess hook genuinely race on
// pendingApply — the race -race is for.
//
// Oracle: rest_performance_pending_apply.go's own doc comments, not this
// test's own first run —
//   - "A PUT /performance that answers performance_reload_failed has
//     committed its writes but they are not in force... The state is
//     cleared only by a refresh AND a registry reload that both succeeded
//     after it was marked."
//   - gateway_reload.go::beginReload: "a reload is already in flight... the
//     owning cycle runs an additional reload... before releasing the slot."
//   - gateway_reload.go::finishReload: "Clearing that flag BETWEEN a reload
//     and its coalesced successor would release pollers against a registry
//     rebuilt from the older config snapshot" — i.e. two overlapping PUTs
//     whose reloads coalesce share ONE fate (reloadOutcomeTracker.finish's
//     "latest seq wins"), so BOTH must see the SAME (failed) outcome here.

package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// concurrentPutResult carries a goroutine's HTTP outcome back to the test
// goroutine — required because require/t.Fatal must not be called from a
// goroutine other than the one running the test (Go testing package
// contract); every check on these results happens back on the test
// goroutine.
type concurrentPutResult struct {
	code int
	body []byte
	err  error // non-nil only for an infrastructure failure (token mint), never an HTTP status
}

// concurrentPutPerf issues PUT /performance with a freshly minted re-auth
// token, safe to call from a non-test goroutine (mirrors mtiPutPerf without
// its t.Helper()/require calls, which are not goroutine-safe).
func concurrentPutPerf(api *restAPI, body string) concurrentPutResult {
	token, err := api.reauthStoreOrInit().mint(reauthGateAdminUser)
	if err != nil {
		return concurrentPutResult{err: err}
	}
	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(reAuthHeader, token)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey{}, &config.UserConfig{Username: reauthGateAdminUser}))
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	return concurrentPutResult{code: w.Code, body: w.Body.Bytes()}
}

// TestPerformancePendingApply_ConcurrentOverlappingSaves_CoalescedFailureThenClear
// drives two real overlapping PUT /performance requests, changing
// max_tool_iterations to two different values, launched from two goroutines
// released together (sync.WaitGroup gate). Both need a registry reload
// (globalChanged). The production single-flight reload pipeline is wired
// exactly as boot wires it (newReloadTrigger, runReloadCycle,
// reloadOutcomeTracker.onSuccess = pendingApply.clearAfterReload); exec is
// the one pluggable seam (mirrors the round-4 gate tests' own
// wireR4ReloadPipeline pattern in rest_performance_r4_pending_apply_test.go,
// duplicated rather than imported since that file is under audit and this
// test needs call-order-specific control that helper does not expose):
// call 1 blocks until the test releases it then succeeds; call 2 fails;
// every later call succeeds.
//
// Blocking call 1 (instead of leaving timing to the scheduler) is what makes
// the two PUTs' reload attempts DEFINITELY coalesce into one cycle rather
// than possibly run as two separate cycles — this test needs the coalesced
// case, where both HTTP handler goroutines block on the SAME
// IsReloadPending() flag and therefore both observe the cycle's LATEST
// recorded outcome (finish's "seq >= lastSeq" rule) and both call
// pendingApply.mark concurrently. The wait after call 1 starts is a
// generous, one-directional buffer (only needs to be "long enough", never
// "short enough") for the other goroutine's own write-then-trigger to land
// while the slot is held — the same class of synchronization the existing
// frontend suite already uses for this repo (PerformanceSection.pendingApply.test.tsx
// has a bare setTimeout(700ms) for an analogous reason).
func TestPerformancePendingApply_ConcurrentOverlappingSaves_CoalescedFailureThenClear(t *testing.T) {
	api := newMTIAPI(t, "200")

	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	exec := func(*config.Config) error {
		switch calls.Add(1) {
		case 1:
			close(started)
			<-release
			return nil
		case 2:
			return errors.New("injected concurrent-save rebuild failure")
		default:
			return nil
		}
	}

	svc := &services{reloadOutcome: &reloadOutcomeTracker{}}
	svc.reloadOutcome.onSuccess = api.pendingApply.clearAfterReload
	api.reloadOutcome = svc.reloadOutcome
	svc.manualReloadChan = make(chan struct{}, 1)
	svc.reloadTrigger = newReloadTrigger(svc, api.agentLoop)
	api.agentLoop.SetReloadFunc(svc.reloadTrigger)
	loadNext := func() (*config.Config, error) { return api.agentLoop.GetConfig(), nil }
	ctx, cancel := context.WithCancel(context.Background())
	pipelineDone := make(chan struct{})
	go func() {
		defer close(pipelineDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-svc.manualReloadChan:
				runReloadCycle(api.agentLoop, svc, nil, 0, exec, loadNext)
			}
		}
	}()
	t.Cleanup(func() { cancel(); <-pipelineDone })

	// --- Phase 1: two genuinely overlapping PUTs, released together. ---
	var start sync.WaitGroup
	start.Add(1)
	resultsCh := make(chan struct {
		label string
		res   concurrentPutResult
	}, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		start.Wait()
		resultsCh <- struct {
			label string
			res   concurrentPutResult
		}{"A(300)", concurrentPutPerf(api, `{"max_tool_iterations":300}`)}
	}()
	go func() {
		defer wg.Done()
		start.Wait()
		resultsCh <- struct {
			label string
			res   concurrentPutResult
		}{"B(250)", concurrentPutPerf(api, `{"max_tool_iterations":250}`)}
	}()
	start.Done() // release both PUTs at the same instant

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: exec call 1 never started — the reload pipeline did not run")
	}
	// Generous one-directional buffer: give the other goroutine's write +
	// trigger time to land and coalesce with call 1 (see doc comment above).
	time.Sleep(300 * time.Millisecond)
	close(release) // let call 1 succeed; the coalesced follow-up (call 2) then fails

	wg.Wait()
	close(resultsCh)
	got := map[string]concurrentPutResult{}
	for r := range resultsCh {
		got[r.label] = r.res
	}
	require.Len(t, got, 2, "both PUTs must have returned a result")
	require.NoError(t, got["A(300)"].err, "instrument: token mint for A must not fail")
	require.NoError(t, got["B(250)"].err, "instrument: token mint for B must not fail")

	// Instrument check: both reload attempts really ran as SEPARATE calls
	// (not short-circuited) — otherwise this proves nothing about the race.
	require.EqualValues(t, 2, calls.Load(),
		"instrument: both overlapping PUTs' reload attempts must have reached exec as two calls")

	// gateway_reload.go::finishReload's own doc comment: a coalesced
	// follow-up shares one fate with the reload it followed. Call 2 (the
	// follow-up) failed, so reloadOutcomeTracker.finish recorded it as the
	// latest outcome (seq 2 >= seq 1) — BOTH callers waiting on the shared
	// IsReloadPending() flag must see that failure.
	for _, label := range []string{"A(300)", "B(250)"} {
		r := got[label]
		require.Equal(t, http.StatusInternalServerError, r.code, "%s: body: %s", label, r.body)
		failed := decodeReloadFailed(t, r.body)
		assert.Equal(t, gen.PerformanceReloadFailedDetailsStageReload, failed.Details.Stage,
			"%s: a coalesced reload failure is the reload stage, not refresh", label)
	}

	// Both handler goroutines called pendingApply.mark concurrently
	// (protected only by performancePendingApply.mu) — assert the state
	// converged to the single documented value, not a lost update from
	// either side racing the other.
	pending := r4Pending(t, api)
	require.NotNil(t, pending, "a failed coalesced reload must leave pending_apply set")
	assert.Equal(t, gen.PerformancePendingApplyStageReload, pending.Stage)
	assert.Equal(t, []gen.PerformancePendingApplyChangedFields{
		gen.PerformancePendingApplyChangedFieldsMaxToolIterations,
	}, pending.ChangedFields, "both A and B changed max_tool_iterations; the union is that one field")

	// --- Phase 2: a later, definitely-separate successful reload must
	// clear it. This is deliberately a bare reload trigger
	// (waitForReloadOrRebuildFailure, as TestPerformancePendingApply_
	// ClearedByLaterSuccessfulReload already uses), NOT a further PUT: a
	// PUT's own success path ALSO calls pendingApply.clearIfEpoch
	// unconditionally (rest_performance.go::putPerformance, right after the
	// reload-or-not branch) using the epoch ITS OWN refresh captured, which
	// would trivially clear this test's state on its own and mask whether
	// the async onSuccess hook (reloadOutcomeTracker.onSuccess ->
	// pendingApply.clearAfterReload, wired at boot — the thing this test
	// exists to exercise under real concurrency) did any work at all. A
	// bare reload has no PUT, hence no clearIfEpoch call: clearAfterReload
	// is the ONLY path that can clear pendingApply here.
	require.NoError(t, waitForReloadOrRebuildFailure(api), "the later reload must succeed")
	assert.Nil(t, r4Pending(t, api), "GET after the later successful reload: nothing pending")
}
