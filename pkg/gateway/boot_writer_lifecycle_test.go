package gateway

// Lifecycle contracts for the boot-stage writer drains (TempDir-cleanup
// family #1644/#1695/#1598, strengthened by CHECK 1 findings F2/F4/F5,
// 2026-11-08). Two layers per writer:
//
//   - a fake-handles CONTRACT test against the drain function: cancel is
//     delivered and the join waits, whatever the writer's exit latency —
//     these are what kill the drain mutants (a drain that only cancels, or
//     only joins, or drops one writer's handles, fails its deterministic
//     150 ms witness);
//   - a REAL-goroutine test against startBackgroundServices: the real boot
//     attaches both handles and the real orphan-GC goroutine (whose first
//     act, the uncancelable library association repair, runs while the
//     join is waiting) settles inside the shared drain even when the parent
//     context stays live.
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

// TestStopBootWritersOnCleanup_BudgetStartsAtCleanup pins CHECK 1 finding F4:
// the drain's shutdown budget must start when CLEANUP RUNS, not when it is
// registered. A test still busy 10 s after registering the cleanup (other
// teardown steps, a slow assertion) must still get a full budget: the writer
// below acknowledges its cancel and takes a finite 25 ms to exit, so a drain
// handed an expired context returns before done closes and the oracle fires.
// The long subtest sleeps past the real budget between registration and
// cleanup — the exact shape whose drain used to be dead on arrival.
func TestStopBootWritersOnCleanup_BudgetStartsAtCleanup(t *testing.T) {
	for _, long := range []bool{false, true} {
		name := "short-control"
		if long {
			name = "registration-older-than-budget"
		}
		done := make(chan struct{})
		cancelled := make(chan struct{})
		rc := &runContextWithOptions{bootBackgroundCancel: func() { close(cancelled) }, bootBackgroundDone: done}
		go func() { <-cancelled; time.Sleep(25 * time.Millisecond); close(done) }()
		t.Run(name, func(t *testing.T) {
			stopBootWritersOnCleanup(t, rc)
			if long {
				time.Sleep(bootBackgroundStopTimeout + 100*time.Millisecond) // outlive a registration-time budget
			}
		})
		select {
		case <-done:
		default:
			t.Errorf("%s: test cleanup returned before the cancelled writer exited; its shutdown context expired while the test was still running", name)
		}
		<-done // the instrument itself never leaks a writer into later tests
	}
}

// fakeWriterDone returns a cancel spy and a done channel that closes only
// after the cancel AND a finite 150 ms exit latency — long enough that a
// drain returning without the join fails the oracle deterministically.
func fakeWriterDone() (cancelCalls *int, done chan struct{}, cancel func()) {
	calls := new(int)
	done = make(chan struct{})
	return calls, done, func() {
		*calls++
		go func() {
			time.Sleep(150 * time.Millisecond)
			close(done)
		}()
	}
}

// TestDrainBootBackgroundWriters_JoinsCatalogRefresh kills the drain mutant
// that drops the catalog refresh loop's cancel+join (CHECK 1 finding F5):
// the shared drain must cancel the loop and wait for its done.
func TestDrainBootBackgroundWriters_JoinsCatalogRefresh(t *testing.T) {
	calls, done, cancel := fakeWriterDone()
	rs := &services{catalogRefreshCancel: cancel, catalogRefreshDone: done}
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer drainCancel()
	drainBootBackgroundWriters(drainCtx, rs)
	require.Equal(t, 1, *calls, "shared drain must cancel the catalog refresh loop")
	select {
	case <-done:
	default:
		t.Fatal("shared drain returned with the provider-catalog refresh loop still running")
	}
}

// TestDrainBootBackgroundWriters_CancelsAndJoinsOrphanGC is the fake-handles
// half of CHECK 1 finding F2's fix: the shared drain cancels the orphan-GC
// goroutine (its own handle — the parent context is NOT cancelling it here)
// and then joins it. Kills the drain mutant that drops the orphan join, and
// fails on the pre-fix shape where the goroutine had no cancel handle at all.
func TestDrainBootBackgroundWriters_CancelsAndJoinsOrphanGC(t *testing.T) {
	calls, done, cancel := fakeWriterDone()
	rs := &services{orphanGCCancel: cancel, orphanGCDone: done}
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer drainCancel()
	drainBootBackgroundWriters(drainCtx, rs)
	require.Equal(t, 1, *calls, "shared drain must cancel the orphan-GC goroutine before joining it")
	select {
	case <-done:
	default:
		t.Fatal("shared drain returned with the orphan-GC goroutine still running")
	}
}

// TestSharedDrainCancelsLiveParentOrphanGC drives the REAL
// startBackgroundServices orphan-GC goroutine with a parent context that
// stays LIVE, then runs the shared drain on a short budget. Pre-fix, the
// goroutine had no cancel handle: its exit signal was the parent ctx only,
// so the drain could join done but never end the goroutine — it came back
// with the writer alive until the budget expired. Post-fix the drain's own
// cancel handle ends the goroutine inside the budget.
func TestSharedDrainCancelsLiveParentOrphanGC(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rs := &services{}
	stg := &setupAndStartServicesState{ctx: ctx, runningServices: rs}
	_, err := stg.startBackgroundServices()
	require.NoError(t, err)
	require.NotNil(t, rs.orphanGCCancel, "real boot must attach the orphan-GC cancel handle")
	require.NotNil(t, rs.orphanGCDone, "real boot must attach the orphan-GC done handle")
	defer func() {
		cancel()
		select {
		case <-rs.orphanGCDone:
		case <-time.After(5 * time.Second):
			t.Error("instrument: orphan-GC goroutine did not exit after the parent context was cancelled")
		}
	}()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer stopCancel()
	drainBootBackgroundWriters(stopCtx, rs)
	select {
	case <-rs.orphanGCDone:
	default:
		t.Fatal("shared drain returned with orphan GC alive: it waits on done but has no cancellation handle; parent context is still live")
	}
}

// TestSharedDrainJoinsInFlightLibraryRepair covers the uncancelable phase
// the cancel CANNOT stop: the orphan-GC goroutine's FIRST act is the one-time
// library association repair, a synchronous writer under workspaces/
// (#1598's "workspaces: directory not empty"). 2000 files in the workspace's
// .library folder keep the repair hashing for tens of milliseconds — far
// longer than the drain's own return — so a teardown that did not WAIT for
// the goroutine would be back while the repair was still writing. Post-fix
// the drain returns only after done is closed, i.e. after the repair's
// durable marker has landed; a reopen of the library below proves the flag.
func TestSharedDrainJoinsInFlightLibraryRepair(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", DevModeBypass: true},
		Agents: config.AgentsConfig{
			// Defaults.Home is the BASE ("<home>/workspace"), matching every
			// other harness: AgentLoop derives its homePath as Dir() of it, so
			// a bare home here would put the workspace libraries one level up.
			Defaults: config.AgentDefaults{Home: filepath.Join(home, "workspace"), DefaultModel: config.DefaultModel{Model: "repair-model"}, MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: "repair-agent", Name: "Repair Agent", Home: home}},
		},
	}
	b := bus.NewMessageBus()
	al, loopErr := agent.NewAgentLoop(cfg, b, &restMockProvider{})
	require.NoError(t, loopErr, "SETUP: real agent loop for the workspace library")
	defer al.Close()
	const wsID = "repair-ws"
	seedBindingWorkspace(t, home, wsID)
	workDir, workErr := workspace.SafeWorkDir(home, wsID)
	require.NoError(t, workErr)
	libDir := filepath.Join(workDir, agent.LibraryDirName())
	require.NoError(t, os.MkdirAll(libDir, 0o700))
	// No manifest entry matches these, so no association is recorded — but
	// the repair still READS AND HASHES every file before it concludes that.
	// 2000 files ≈ tens of milliseconds of uncancelable work per boot pass.
	payload := make([]byte, 4096)
	for i := range payload {
		payload[i] = byte(i)
	}
	for i := 0; i < 2000; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(libDir, fmt.Sprintf("legacy-upload-%04d.bin", i)), payload, 0o600))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rs := &services{}
	stg := &setupAndStartServicesState{ctx: ctx, cfg: cfg, agentLoop: al, msgBus: b, homePath: home, runningServices: rs}
	_, err := stg.startBackgroundServices()
	require.NoError(t, err)

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer drainCancel()
	drainBootBackgroundWriters(drainCtx, rs)

	select {
	case <-rs.orphanGCDone:
	default:
		t.Fatal("shared drain returned while the orphan-GC goroutine was still running its in-flight library repair")
	}
	// The repair's durable completion marker: the one-time flag persisted in
	// the workspace library manifest. Reopened fresh, it must be set — the
	// repair ran to completion INSIDE the join the drain waited on.
	lib, libErr := library.New(home, wsID)
	require.NoError(t, libErr)
	require.True(t, lib.LibraryPathsRepaired(), "association repair must have completed before the drain returned")
}
