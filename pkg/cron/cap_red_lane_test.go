package cron

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The cron unit tests inject only the external limit reader, handler and clock.
// TestCapRED_ScheduledLane_RealExportedSeam (pkg/agent) separately proves the
// real exported memory seam; this file tests the actual cron lane, never a fake.
type capRedLaneLog struct {
	mu      sync.Mutex
	lines   []slog.Record
	waiting chan string
}

func (h *capRedLaneLog) Enabled(context.Context, slog.Level) bool { return true }
func (h *capRedLaneLog) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.lines = append(h.lines, r.Clone())
	h.mu.Unlock()
	if strings.HasPrefix(r.Message, "cron: schedule run is waiting —") {
		id := ""
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "job_id" {
				id = a.Value.String()
			}
			return true
		})
		h.waiting <- id
	}
	return nil
}
func (h *capRedLaneLog) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capRedLaneLog) WithGroup(string) slog.Handler      { return h }

type capRedLaneRunner struct {
	mu          sync.RWMutex
	entered     chan string
	releases    map[string]chan struct{}
	active, max atomic.Int32
}

func (r *capRedLaneRunner) RunScheduled(ctx context.Context, j *CronJob) (string, error) {
	n := r.active.Add(1)
	defer r.active.Add(-1)
	for old := r.max.Load(); n > old && !r.max.CompareAndSwap(old, n); old = r.max.Load() {
	}
	r.mu.RLock()
	release := r.releases[j.ID]
	r.mu.RUnlock()
	r.entered <- j.ID
	select {
	case <-release:
		return "session-" + j.ID, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type capRedLaneFixture struct {
	cs      *CronService
	runner  *capRedLaneRunner
	logs    *capRedLaneLog
	limit   atomic.Int64
	samples chan struct{}
	done    []chan struct{}
	names   map[string]string
}

func capRedNewLane(t *testing.T, initial int) *capRedLaneFixture {
	t.Helper()
	f := &capRedLaneFixture{runner: &capRedLaneRunner{entered: make(chan string, 64), releases: map[string]chan struct{}{}}, logs: &capRedLaneLog{waiting: make(chan string, 64)}, samples: make(chan struct{}, 256), names: map[string]string{}}
	f.limit.Store(int64(initial))
	provider := func() (int, string) {
		n := int(f.limit.Load())
		select {
		case f.samples <- struct{}{}:
		default:
		}
		cause := ""
		if n == 2 {
			cause = "memory_pressure"
		}
		return n, cause
	}
	ctor := reflect.ValueOf(NewCronService)
	args := []reflect.Value{reflect.ValueOf(filepath.Join(t.TempDir(), "jobs.json"))}
	if ctor.Type().NumIn() == 2 {
		args = append(args, reflect.ValueOf(provider).Convert(ctor.Type().In(1)))
	} else {
		t.Error("CAP-B9 RED: NewCronService lacks its required LaneLimitFunc constructor argument")
	}
	f.cs = ctor.Call(args)[0].Interface().(*CronService)
	f.cs.SetRunner(f.runner)
	if err := f.cs.startNoLoop(); err != nil {
		t.Fatal(err)
	}
	previous := slog.Default()
	slog.SetDefault(slog.New(f.logs))
	t.Cleanup(func() {
		f.runner.mu.RLock()
		for _, ch := range f.runner.releases {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
		f.runner.mu.RUnlock()
		f.cs.Stop()
		f.cs.WaitForLane()
		for _, done := range f.done {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("RunNow caller failed to join")
			}
		}
		slog.SetDefault(previous)
	})
	return f
}
func (f *capRedLaneFixture) job(t *testing.T, name string) string {
	t.Helper()
	j, err := f.cs.AddJobFull(JobSpec{Name: name, AgentID: "cap-owner", Message: "hold dispatch", Schedule: CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}})
	if err != nil {
		t.Fatal(err)
	}
	f.runner.mu.Lock()
	f.runner.releases[j.ID] = make(chan struct{})
	f.runner.mu.Unlock()
	f.names[j.ID] = name
	return j.ID
}
func (f *capRedLaneFixture) launch(t *testing.T, id string, runNow bool, ctx context.Context) {
	t.Helper()
	if runNow {
		done := make(chan struct{})
		f.done = append(f.done, done)
		go func() {
			defer close(done)
			_, _, err := f.cs.RunNow(id)
			if err != nil && ctx.Err() == nil {
				t.Errorf("real RunNow failed: %v", err)
			}
		}()
	} else {
		f.cs.mu.Lock()
		win := laneWindow{ctx: ctx, sem: f.cs.laneSem, runs: f.cs.laneRuns}
		f.cs.laneAddUnsafe(win.runs, 1)
		f.cs.mu.Unlock()
		f.cs.dispatch(id, win)
	}
}
func (f *capRedLaneFixture) await(t *testing.T, ch <-chan string, want, label string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("%s got %s (%s), want %s (%s)", label, got, f.names[got], want, f.names[want])
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("%s: required observable event missing for %s", label, f.names[want])
	}
}
func (f *capRedLaneFixture) start(t *testing.T, id string, runNow bool) {
	f.launch(t, id, runNow, context.Background())
	f.await(t, f.runner.entered, id, "handler entry")
}
func (f *capRedLaneFixture) wait(t *testing.T, id string, runNow bool, ctx context.Context) {
	f.launch(t, id, runNow, ctx)
	f.await(t, f.logs.waiting, id, "one append-time wait log")
}

func TestCapRED_Cron_LaneFairness_OlderWaitersBothEntryPaths(t *testing.T) {
	f := capRedNewLane(t, 2)
	h0, h1 := f.job(t, "holder0"), f.job(t, "holder1")
	f.start(t, h0, false)
	f.start(t, h1, true)
	a, b, c, d, e := f.job(t, "old-a"), f.job(t, "old-b"), f.job(t, "cancelled-c"), f.job(t, "new-d"), f.job(t, "newer-e")
	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.wait(t, a, false, context.Background())
	f.wait(t, b, true, context.Background())
	f.wait(t, c, false, cancelCtx)
	f.wait(t, d, true, context.Background())
	close(f.runner.releases[h0])
	f.await(t, f.runner.entered, a, "S57 release1 oldest")
	f.wait(t, e, false, context.Background())
	close(f.runner.releases[h1])
	f.await(t, f.runner.entered, b, "S57 release2 oldest")
	cancel()
	close(f.runner.releases[a])
	f.await(t, f.runner.entered, d, "S57 release3 skips cancelled waiter")
	close(f.runner.releases[b])
	f.await(t, f.runner.entered, e, "S57 release4 no starvation")
	if n := f.runner.max.Load(); n != 2 {
		t.Errorf("S57 highest concurrent dispatches=%d, want 2", n)
	}
	f.logs.mu.Lock()
	defer f.logs.mu.Unlock()
	counts := map[string]int{}
	for _, r := range f.logs.lines {
		if strings.HasPrefix(r.Message, "cron: schedule run is waiting —") {
			r.Attrs(func(a slog.Attr) bool {
				if a.Key == "job_id" {
					counts[a.Value.String()]++
				}
				return true
			})
		}
	}
	for _, id := range []string{a, b, c, d, e} {
		if counts[id] != 1 {
			t.Errorf("one line per wait for %s: got %d", f.names[id], counts[id])
		}
	}
}

func TestCapRED_Cron_LaneReleaseRechecksLowerLimitAndDrainsRecovery(t *testing.T) {
	for _, runNow := range []bool{false, true} {
		t.Run(fmt.Sprintf("runNow_%t", runNow), func(t *testing.T) {
			f := capRedNewLane(t, 0)
			holders := make([]string, 8)
			for i := range holders {
				holders[i] = f.job(t, fmt.Sprintf("holder%d", i))
				f.start(t, holders[i], runNow)
			}
			f.limit.Store(2)
			q := []string{f.job(t, "q0"), f.job(t, "q1"), f.job(t, "q2")}
			for _, id := range q {
				f.wait(t, id, runNow, context.Background())
			}
			// Discard acquisition samples; only a completion may drive each next sample.
			for len(f.samples) > 0 {
				<-f.samples
			}
			for i := 0; i < 6; i++ {
				close(f.runner.releases[holders[i]])
				select {
				case <-f.samples:
				case <-time.After(3 * time.Second):
					t.Fatal("release did not consult current limit")
				}
				// The limit is consulted under this SAME lane mutex. Reacquiring it is a
				// completion barrier for the comparison, not a stopwatch as an oracle.
				f.cs.mu.Lock()
				f.cs.mu.Unlock()
				select {
				case id := <-f.runner.entered:
					t.Errorf("S57a premature grant after completion %d: %s", i+1, f.names[id])
				default:
				}
			}
			close(f.runner.releases[holders[6]])
			f.await(t, f.runner.entered, q[0], "S57a seventh completion reaches floor")
			f.limit.Store(0)
			newcomer := f.job(t, "recovery-newcomer")
			f.wait(t, newcomer, runNow, context.Background())
			close(f.runner.releases[holders[7]])
			for _, id := range []string{q[1], q[2], newcomer} {
				f.await(t, f.runner.entered, id, "S57a one release drains unlimited FIFO")
			}
		})
	}
}

func TestCapRED_Cron_WaitLineExactCauseAndNumbers(t *testing.T) {
	f := capRedNewLane(t, 2)
	a, b, c := f.job(t, "a"), f.job(t, "b"), f.job(t, "waiting")
	f.start(t, a, false)
	f.start(t, b, true)
	f.wait(t, c, true, context.Background())
	f.logs.mu.Lock()
	defer f.logs.mu.Unlock()
	found := 0
	for _, r := range f.logs.lines {
		if strings.HasPrefix(r.Message, "cron: schedule run is waiting —") {
			found++
			want := "cron: schedule run is waiting — this machine is low on memory, so new scheduled runs are held at 2 at a time (2 in flight)"
			if r.Message != want {
				t.Errorf("M14a got %q, want %q", r.Message, want)
			}
			fields := map[string]any{}
			r.Attrs(func(a slog.Attr) bool { fields[a.Key] = a.Value.Any(); return true })
			if fields["job_id"] != c {
				t.Errorf("wait names wrong job: %#v", fields)
			}
			if fmt.Sprint(fields["run_limit"]) != "2" {
				t.Errorf("run_limit not copied from decision: %#v", fields)
			}
		}
	}
	if found != 1 {
		t.Errorf("exactly one initial wait line, got %d", found)
	}
}
