// cancel_stress_test.go
//
// HIGH-CONCURRENCY stress coverage for the two just-landed cancellation
// fixes (see external_dispatch.go's "FIX 1" comment and cancel.go's
// RequestCancel doc). The correctness tests for FIX 1
// (subturn_external_cancel_test.go) drive exactly ONE external-cli childTS
// at a time through the real cancel cascade. That is enough to prove the
// mechanism works, but it does NOT exercise the actual race the fix
// introduces: runExternalCLISubTurn calling childTS.setTurnCancel /
// setProviderCancel (turn.go, under ts.mu) can now run concurrently, on a
// different goroutine, with InterruptSession / InterruptSessionHard
// (steering.go) Ranging over al.activeTurnStates and reading those same
// fields (also under ts.mu) for a turnState that may or may not have
// finished registering yet.
//
// This file registers many live external-cli childTS turnStates across
// several sessions and fires the real graceful + hard cancel cascade against
// them from multiple goroutines WHILE other goroutines are still
// registering/starting new children — the exact overlap window the fix's
// correctness depends on. Run under `go test -race` to catch:
//   - a data race on turnState.providerCancel/turnCancel
//   - a goroutine that hangs forever because a registration/cancel race was
//     lost (a correctness regression, not just a race-detector finding)
//   - a panic during the concurrent Range + registration
//
// This is deliberately NOT a substitute for subturn_external_cancel_test.go
// — it reuses that file's blockingExternalDriver fake (no build tag, same
// package, always compiled) rather than duplicating it.

package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// driverRegistry hands out a fresh blockingExternalDriver per call to the
// swapped newExternalDriver factory and tracks every instance created, so
// the stress test can verify every single sub-turn's run ctx was actually
// canceled (not just that SOME of them were, which a shared single fake
// instance would obscure).
type driverRegistry struct {
	mu      sync.Mutex
	drivers []*blockingExternalDriver
}

func (r *driverRegistry) new() *blockingExternalDriver {
	d := newBlockingExternalDriver()
	r.mu.Lock()
	r.drivers = append(r.drivers, d)
	r.mu.Unlock()
	return d
}

func (r *driverRegistry) snapshot() []*blockingExternalDriver {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*blockingExternalDriver, len(r.drivers))
	copy(out, r.drivers)
	return out
}

// TestStress_ExternalCLI_ConcurrentSpawnAndCancel is Stress A: concurrent
// spawn+cancel of external-cli sub-turns (see file doc above).
func TestStress_ExternalCLI_ConcurrentSpawnAndCancel(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	const numSessions = 8
	const perSession = 10
	const total = numSessions * perSession

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "mock"}},
			List:     []config.AgentConfig{{ID: "mia"}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &simpleMockProviderAPI{response: "ok"})

	reg := &driverRegistry{}
	prevDriver := newExternalDriver
	newExternalDriver = func(_ string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		return reg.new(), nil
	}
	defer func() { newExternalDriver = prevDriver }()

	sessionIDs := make([]string, numSessions)
	for i := range sessionIDs {
		sessionIDs[i] = fmt.Sprintf("stress-session-%d", i)
	}

	// Give every sub-turn a DISTINCT work/ dir so none of them serialize on
	// external_dispatch.go's workspaceRunLocks (keyed by workDir), which would
	// otherwise mask the true concurrency this stress test exists to exercise.
	//
	// ADR-046 P1: a dispatch's WorkDir is the acting agent's RESOLVED workspace
	// work/ dir, never agent.Home — so "distinct work dirs" is now expressed as
	// "one dedicated single-member workspace per agent". Seed those workspaces
	// up front, from the main goroutine, BEFORE any spawn goroutine resolves
	// its WorkDir (resolveTurnWorkDirOrRefuse would otherwise refuse a
	// workspaceless agent). agent.Home is still set to a distinct temp dir
	// below, purely as a realistic non-empty Home; it no longer drives WorkDir.
	//
	// NOTE (7-reviewer gate, BLOCK finding on FIX 1): this distinct-workspace
	// choice is deliberate and remains correct for THIS stress test's own
	// purpose (proving the setTurnCancel/setProviderCancel-vs-Range race is
	// race-detector-clean under high concurrency) — it is not a gap left
	// unaddressed. The CONTENDED case this choice avoids — a cancel firing
	// while a second same-workspace external-cli sub-turn is queued behind a
	// first on workspaceRunLocks — is covered explicitly by
	// TestExternalCLISubTurn_CancelDuringWorkspaceLockWait in
	// subturn_external_cancel_test.go, which was exactly the scenario the
	// BLOCK finding identified as unprotected before FIX 1's lock-ordering
	// fix (registering childTS's cancel funcs before acquiring the lock, and
	// making the lock acquisition itself cancel-aware).
	agentIDs := make([]string, total)
	for i := range agentIDs {
		agentIDs[i] = fmt.Sprintf("ext-agent-%d", i)
	}
	seedDistinctTestWorkspacesForIDs(t, agentIDs)

	workDirs := make([]string, total)
	for i := range workDirs {
		workDirs[i] = t.TempDir()
	}

	var panics int32
	recoverPanic := func(label string) {
		if r := recover(); r != nil {
			atomic.AddInt32(&panics, 1)
			t.Errorf("panic in %s: %v", label, r)
		}
	}

	resultCh := make(chan *tools.ToolResult, total)

	// Spawn goroutines: register childTS in al.activeTurnStates exactly as
	// spawnSubTurn does, then run it. Started CONCURRENTLY with the
	// cancellers below (not after they finish registering) so the
	// registration itself races the cascade's Range-and-fire.
	var spawnWG sync.WaitGroup
	for i := 0; i < total; i++ {
		sessID := sessionIDs[i%numSessions]
		spawnWG.Add(1)
		go func() {
			defer spawnWG.Done()
			defer recoverPanic("spawn")

			agent := &AgentInstance{
				ID:   agentIDs[i],
				Name: "External Agent",
				Home: workDirs[i],
				Subagents: &config.SubagentsConfig{
					Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
				},
			}
			turnKey := fmt.Sprintf("child-%d", i)
			ts := &turnState{
				agent:               agent,
				agentID:             agent.ID,
				turnID:              turnKey,
				transcriptSessionID: sessID,
				// ADR-057 FR-011/FR-015 fixture repair: activeTurnStates is
				// keyed by turnKey (not sessID), so al.Interrupt(sessID, ...)
				// (steering.go's resolveInterruptAnchors) falls through its
				// direct-key Load to the Range fallback, which now matches on
				// routingSessionID rather than transcriptSessionID.
				routingSessionID: session.RoutingSessionID(sessID),
			}
			al.activeTurnStates.Store(turnKey, ts)
			defer al.activeTurnStates.Delete(turnKey)

			res, _ := runExternalCLISubTurn(context.Background(), al, ts, "stress task", 30*time.Second)
			resultCh <- res
		}()
	}

	// Canceller goroutines: repeatedly fire the REAL graceful + hard cascade
	// for every session until every spawn goroutine has finished (or the
	// watchdog below trips). Repetition is deliberate: a single
	// InterruptSession call racing a childTS that has not yet reached
	// setTurnCancel/setProviderCancel is a legitimate, expected no-op for
	// THAT particular call — only a sustained failure to EVER cancel
	// (caught by the watchdog) indicates a real regression.
	stopCancellers := make(chan struct{})
	var cancellerWG sync.WaitGroup
	const numCancellers = 6
	for c := 0; c < numCancellers; c++ {
		cancellerWG.Add(1)
		go func() {
			defer cancellerWG.Done()
			defer recoverPanic("canceller")
			for {
				select {
				case <-stopCancellers:
					return
				default:
				}
				for _, sid := range sessionIDs {
					_, _ = al.Interrupt(sid, ScopeSubtree, "stress cancel")
					_, _ = al.InterruptSessionHard(sid, ScopeSubtree, "stress cancel")
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}

	// Watchdog: fail loud, not hang, if the spawn+cancel race deadlocks or
	// the fix regresses (a lost cancel would otherwise block this forever,
	// since blockingExternalDriver's Run never returns until its ctx is
	// Done).
	spawnersDone := make(chan struct{})
	go func() {
		spawnWG.Wait()
		close(spawnersDone)
	}()

	select {
	case <-spawnersDone:
	case <-time.After(45 * time.Second):
		close(stopCancellers)
		t.Fatal("TIMEOUT: not all external-cli sub-turns were canceled — " +
			"suspected cancel-propagation regression or deadlock in the spawn+cancel race")
	}

	close(stopCancellers)

	cancellersDone := make(chan struct{})
	go func() {
		cancellerWG.Wait()
		close(cancellersDone)
	}()
	select {
	case <-cancellersDone:
	case <-time.After(10 * time.Second):
		t.Fatal("canceller goroutines did not exit after the stop signal")
	}

	if p := atomic.LoadInt32(&panics); p > 0 {
		t.Fatalf("%d goroutine(s) panicked during the concurrent spawn+cancel wave", p)
	}

	close(resultCh)
	var canceledCount int
	for res := range resultCh {
		if res == nil || res.Err == nil || !errors.Is(res.Err, context.Canceled) {
			t.Errorf("expected a canceled ToolResult with Err=context.Canceled, got %+v", res)
			continue
		}
		canceledCount++
	}
	if canceledCount != total {
		t.Errorf("canceledCount=%d, want %d — some sub-turns finished without a canceled result", canceledCount, total)
	}

	drivers := reg.snapshot()
	// NOTE (BLOCK-fix follow-up): len(drivers) is deliberately NOT required to
	// equal `total` here. FIX 1's BLOCK-finding fix added a belt-and-suspenders
	// check (layer 3, external_dispatch.go) that skips driver instantiation
	// entirely when runCtx is already canceled at the point the lock is
	// acquired — correct behavior, since starting a driver for a run that is
	// already canceled is pure waste. Even on this test's intentionally
	// DISTINCT, uncontended workDirs (see the note above), a cancel racing in
	// aggressively enough — from the 6 canceller goroutines hammering
	// InterruptSession/InterruptSessionHard in a tight loop — can win Go's
	// select against an immediately-available lock token often enough that
	// occasionally one of the 80 sub-turns is canceled before it ever reaches
	// newExternalDriver. That sub-turn still correctly appears in
	// canceledCount above (asserted immediately above this block); it simply
	// contributes no *blockingExternalDriver to this registry. What this
	// block still asserts unconditionally: every driver that WAS actually
	// instantiated observed its own runCtx being canceled, and that
	// concurrency was genuinely exercised (at least one driver ran) rather
	// than the test accidentally becoming a no-op.
	if len(drivers) == 0 {
		t.Fatal("no sub-turn ever reached driver.Run — concurrency was not actually exercised")
	}
	for i, d := range drivers {
		if !d.ctxCanceled.Load() {
			t.Errorf("driver #%d's run ctx was never canceled — its subprocess would not have been killed", i)
		}
	}
}
