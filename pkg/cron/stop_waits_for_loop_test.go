package cron

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Issue #1450. Requirement (oracle): CronService.Stop returns only after the
// background run-loop goroutine (runLoop) has exited. After Stop returns, no
// goroutine started by the service is still alive. Stop must also stay safe
// when called twice, when the service never started, and when it races a Start,
// and it must keep draining the worker pool within its existing bound.

const runLoopFrame = "(*CronService).runLoop"

// runLoopGoroutines returns the ids of every goroutine currently inside
// runLoop, with its stack text. Tests compare against a baseline taken before
// the service under test starts, so goroutines of other tests never count.
func runLoopGoroutines() map[string]string {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	out := make(map[string]string)
	for _, g := range strings.Split(string(buf), "\n\n") {
		if !strings.Contains(g, runLoopFrame) {
			continue
		}
		id := g
		if i := strings.Index(g, "\n"); i > 0 {
			id = g[:i] // "goroutine 123 [select]:"
		}
		if i := strings.Index(id, " ["); i > 0 {
			id = id[:i]
		}
		out[id] = g
	}
	return out
}

func newRunLoopLeaks(before map[string]string) []string {
	var leaked []string
	for id, g := range runLoopGoroutines() {
		if _, existed := before[id]; !existed {
			leaked = append(leaked, g)
		}
	}
	return leaked
}

// parkingClock is a Clock whose Now() parks the runLoop goroutine, and only
// that goroutine, once armed. It lets a test hold the loop in the middle of a
// tick deterministically, with no sleeps, while Stop is called.
type parkingClock struct {
	armed   atomic.Bool
	parked  chan struct{}
	release chan struct{}
	once    sync.Once
	rel     sync.Once
}

func newParkingClock() *parkingClock {
	return &parkingClock{parked: make(chan struct{}), release: make(chan struct{})}
}

func (c *parkingClock) Release() { c.rel.Do(func() { close(c.release) }) }

func (c *parkingClock) Now() time.Time {
	if c.armed.Load() {
		buf := make([]byte, 8192)
		n := runtime.Stack(buf, false)
		if strings.Contains(string(buf[:n]), runLoopFrame) {
			c.once.Do(func() { close(c.parked) })
			<-c.release
		}
	}
	return time.Now()
}

func newLoopService(t *testing.T, everyMS int64) *CronService {
	t.Helper()
	cs := NewCronService(filepath.Join(t.TempDir(), "cron", "jobs.json"))
	if _, err := cs.AddJobFull(JobSpec{
		Name:     "stop-test",
		Schedule: CronSchedule{Kind: "every", EveryMS: int64Ptr(everyMS)},
		Message:  "m",
		AgentID:  "u6-test-owner",
	}); err != nil {
		t.Fatalf("SETUP: AddJobFull: %v", err)
	}
	return cs
}

// Deterministic RED for #1450: the loop is parked mid-tick, Stop is called.
// Stop must not return while the loop goroutine is still alive.
func TestCronService_Stop_DoesNotReturnWhileRunLoopIsAlive(t *testing.T) {
	before := runLoopGoroutines()
	pc := newParkingClock()
	cs := newLoopService(t, 20)
	cs.SetClock(pc)
	t.Cleanup(func() {
		pc.Release()
		cs.Stop()
	})

	if err := cs.Start(); err != nil {
		t.Fatalf("SETUP: Start: %v", err)
	}
	pc.armed.Store(true)
	select {
	case <-pc.parked:
	case <-time.After(10 * time.Second):
		t.Fatal("SETUP: the run loop never reached its tick, so it could not be parked")
	}

	stopReturned := make(chan struct{})
	go func() {
		cs.Stop()
		close(stopReturned)
	}()

	returnedWhileParked := false
	select {
	case <-stopReturned:
		returnedWhileParked = true
	case <-time.After(300 * time.Millisecond):
	}
	alive := len(newRunLoopLeaks(before))
	pc.Release()
	select {
	case <-stopReturned:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return after the parked run loop was released: Stop deadlocks")
	}

	if returnedWhileParked {
		t.Errorf("#1450: Stop returned while the run loop goroutine was still alive (parked mid-tick); "+
			"%d runLoop goroutine(s) still running at that moment", alive)
	}
	// Whenever Stop does return, the loop must already be gone: no polling.
	if leaked := newRunLoopLeaks(before); len(leaked) != 0 {
		t.Errorf("#1450: Stop returned but %d runLoop goroutine(s) are still alive:\n%s",
			len(leaked), strings.Join(leaked, "\n\n"))
	}
}

// Repetition test for #1450: many Start/Stop cycles over a job that is due on
// every tick; after every Stop, no runLoop goroutine may be left. Reports how
// many cycles violated the contract.
func TestCronService_StartStopCycles_LeaveNoRunLoopGoroutine(t *testing.T) {
	const cycles = 400
	before := runLoopGoroutines()
	cs := newLoopService(t, 1)
	violations, firstLeak := 0, ""
	for i := 0; i < cycles; i++ {
		if err := cs.Start(); err != nil {
			t.Fatalf("SETUP: cycle %d Start: %v", i, err)
		}
		runtime.Gosched()
		cs.Stop()
		if leaked := newRunLoopLeaks(before); len(leaked) != 0 {
			violations++
			if firstLeak == "" {
				firstLeak = leaked[0]
			}
		}
	}
	// Let any straggler exit so it cannot disturb later tests.
	deadline := time.Now().Add(5 * time.Second)
	for len(newRunLoopLeaks(before)) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if violations != 0 {
		t.Errorf("#1450: %d of %d Start/Stop cycles returned from Stop with a runLoop goroutine still alive; first:\n%s",
			violations, cycles, firstLeak)
	}
}

// Stop twice, and Stop on a service that never started, must be harmless.
func TestCronService_Stop_IsIdempotentAndSafeBeforeStart(t *testing.T) {
	before := runLoopGoroutines()

	never := NewCronService(filepath.Join(t.TempDir(), "never.json"))
	never.Stop()
	never.Stop()

	cs := newLoopService(t, 20)
	if err := cs.Start(); err != nil {
		t.Fatalf("SETUP: Start: %v", err)
	}
	done := make(chan struct{})
	go func() {
		cs.Stop()
		cs.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a second Stop deadlocked")
	}
	if leaked := newRunLoopLeaks(before); len(leaked) != 0 {
		t.Errorf("#1450: after Stop twice, %d runLoop goroutine(s) still alive:\n%s", len(leaked), strings.Join(leaked, "\n\n"))
	}
}

// A service started without the loop (test seam) has nothing to wait for; Stop
// must return and must not hang waiting for a loop that never existed.
func TestCronService_Stop_OnServiceWithoutLoopReturns(t *testing.T) {
	cs := newLoopService(t, 20)
	if err := cs.startNoLoop(); err != nil {
		t.Fatalf("SETUP: startNoLoop: %v", err)
	}
	done := make(chan struct{})
	go func() { cs.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop hung on a service that has no run loop")
	}
}

// Stop racing Start, from several goroutines at once: no deadlock, no panic,
// and once the dust settles one last Stop leaves no runLoop goroutine behind.
func TestCronService_StopRacingStart_NoDeadlockNoLeak(t *testing.T) {
	before := runLoopGoroutines()
	cs := newLoopService(t, 1)

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = cs.Start()
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				cs.Stop()
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("Start/Stop racing each other deadlocked")
	}
	cs.Stop()
	if leaked := newRunLoopLeaks(before); len(leaked) != 0 {
		t.Errorf("#1450: after racing Start/Stop and a final Stop, %d runLoop goroutine(s) still alive:\n%s",
			len(leaked), strings.Join(leaked, "\n\n"))
	}
}

// Existing contract kept: Stop cancels the lane context, drains the worker
// pool, and never waits on a job that ignores cancellation beyond the drain
// bound. (Guard test: green today and must stay green after the #1450 fix.)
type stubbornRunner struct {
	started chan struct{}
	release chan struct{}
	ctxErr  atomic.Value
}

func (r *stubbornRunner) RunScheduled(ctx context.Context, _ *CronJob) (string, error) {
	close(r.started)
	<-ctx.Done()
	r.ctxErr.Store(ctx.Err())
	<-r.release // ignores cancellation after noticing it
	return "", nil
}

func TestCronService_Stop_CancelsRunsAndKeepsItsDrainBound(t *testing.T) {
	before := runLoopGoroutines()
	cs := newLoopService(t, 20)
	runner := &stubbornRunner{started: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		close(runner.release)
		cs.WaitForLane() // the released job writes the store; finish before TempDir removal
	})
	cs.SetRunner(runner)
	cs.stopDrainTimeout = 150 * time.Millisecond

	if err := cs.Start(); err != nil {
		t.Fatalf("SETUP: Start: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(10 * time.Second):
		t.Fatal("SETUP: the job never started")
	}

	begin := time.Now()
	done := make(chan struct{})
	go func() { cs.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop waited on a running job that ignores cancellation, beyond the drain bound (150ms)")
	}
	if elapsed := time.Since(begin); elapsed < 100*time.Millisecond {
		t.Errorf("Stop returned in %v, before the in-flight job had a chance to drain (bound 150ms)", elapsed)
	}
	if err, _ := runner.ctxErr.Load().(error); !errors.Is(err, context.Canceled) {
		t.Errorf("Stop must cancel the running job's context; got %v", err)
	}
	if leaked := newRunLoopLeaks(before); len(leaked) != 0 {
		t.Errorf("#1450: Stop returned with a runLoop goroutine still alive:\n%s", strings.Join(leaked, "\n\n"))
	}
}
