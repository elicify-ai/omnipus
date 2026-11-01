package agent

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
)

// Only the handler is an external edge. The callback passed to the actual cron
// service is the REAL exported ScheduledLaneLimit, not a canned limit of two.
type capRedScheduledRunner struct {
	entered  chan string
	release  chan struct{}
	callers  sync.WaitGroup
	releases sync.Map
}

func (r *capRedScheduledRunner) RunScheduled(ctx context.Context, j *cron.CronJob) (string, error) {
	one := make(chan struct{})
	r.releases.Store(j.ID, one)
	r.entered <- j.ID
	select {
	case <-one:
		return "cap-session-" + j.ID, nil
	case <-r.release:
		return "cap-session-" + j.ID, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func capRedCronWithRealSeam(t *testing.T) (*cron.CronService, *capRedScheduledRunner) {
	t.Helper()
	ctor := reflect.ValueOf(cron.NewCronService)
	args := []reflect.Value{reflect.ValueOf(filepath.Join(t.TempDir(), "cron.json"))}
	if ctor.Type().NumIn() == 2 {
		args = append(args, reflect.ValueOf(ScheduledLaneLimit).Convert(ctor.Type().In(1)))
	} else {
		t.Error("CAP-B9 RED: cron.NewCronService has no required LaneLimitFunc; real memory seam cannot be wired")
	}
	cs := ctor.Call(args)[0].Interface().(*cron.CronService)
	runner := &capRedScheduledRunner{entered: make(chan string, 20), release: make(chan struct{})}
	cs.SetRunner(runner)
	clock := &fakePlanClock{now: time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC)}
	cs.SetClock(clock)
	if err := cs.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { close(runner.release); cs.Stop(); cs.WaitForLane(); runner.callers.Wait() })
	return cs, runner
}
func capRedScheduledJob(t *testing.T, cs *cron.CronService, n int) string {
	t.Helper()
	at := time.Date(2026, 10, 31, 20, 1, 0, 0, time.UTC).UnixMilli()
	j, err := cs.AddJobFull(cron.JobSpec{Name: fmt.Sprintf("cap-job-%d", n), AgentID: "owner", Message: "hold", Schedule: cron.CronSchedule{Kind: "at", AtMS: &at}})
	if err != nil {
		t.Fatal(err)
	}
	return j.ID
}
func capRedScheduledEntries(t *testing.T, r *capRedScheduledRunner, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		select {
		case id := <-r.entered:
			ids = append(ids, id)
		case <-time.After(3 * time.Second):
			t.Fatalf("scheduled handler %d/%d did not enter; fixed limit must not replace unlimited healthy lane", i+1, n)
		}
	}
	return ids
}

func TestCapRED_ScheduledLane_RealExportedSeam(t *testing.T) {
	for _, tc := range []struct {
		name     string
		high, ok bool
		limit    int
		cause    string
	}{
		{"healthy", false, true, 0, ""},
		{"pressure", true, true, 2, "memory_pressure"},
		{"unverifiable", false, false, 2, "memory_unverifiable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			high, ok := tc.high, tc.ok
			capRedMemory(t, &high, &ok)
			limit, cause := ScheduledLaneLimit()
			capRedAssert(t, "S56 real exported fold", []any{limit, cause}, []any{tc.limit, tc.cause})
		})
	}
	// D8: the exact shared threshold is strict. Do not emulate that comparison.
	restore := config.SetMemoryProviderForTest(config.MemoryPressureHighFromSignalsForTest(15, 100), func() (uint64, bool) { return 15, true })
	defer restore()
	limit, cause := ScheduledLaneLimit()
	capRedAssert(t, "exact .85 remains unlimited", []any{limit, cause}, []any{0, ""})
}

func TestCapRED_ScheduledLane_HealthyTwelveThroughRealSeam(t *testing.T) {
	for _, runNow := range []bool{false, true} {
		t.Run(fmt.Sprintf("runNow_%t", runNow), func(t *testing.T) {
			high, ok := false, true
			capRedMemory(t, &high, &ok)
			cs, runner := capRedCronWithRealSeam(t)
			ids := make([]string, 12)
			for i := range ids {
				ids[i] = capRedScheduledJob(t, cs, i)
			}
			logs := capRedLogs(t)
			if runNow {
				runner.callers.Add(len(ids))
				for _, id := range ids {
					go func(id string) {
						defer runner.callers.Done()
						_, _, err := cs.RunNow(id)
						if err != nil {
							t.Errorf("RunNow: %v", err)
						}
					}(id)
				}
			} else {
				cs.SetClock(&fakePlanClock{now: time.Date(2026, 10, 31, 20, 1, 1, 0, time.UTC)})
				cs.RunDueJobs()
			}
			// The fixture cleanup releases before joining every synchronous caller,
			// including the RED path where the ninth handler never entered.
			got := capRedScheduledEntries(t, runner, 12)
			seen := map[string]bool{}
			for _, id := range got {
				if seen[id] {
					t.Errorf("same handler entered twice: %s", id)
				}
				seen[id] = true
			}
			capRedAssert(t, "S17 all twelve independent handlers admitted", len(seen), 12)
			for _, line := range logs() {
				if text, _ := line["message"].(string); strings.Contains(text, "schedule run is waiting") {
					t.Errorf("healthy unlimited lane emitted wait line: %q", text)
				}
			}
		})
	}
}

// Preserve the real log path, and expose its required append-time wait event as
// a deterministic barrier. No sleep is used to infer that a handler is queued.
type capRedScheduleObserver struct {
	base    slog.Handler
	records chan slog.Record
}

func (h capRedScheduleObserver) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}
func (h capRedScheduleObserver) Handle(ctx context.Context, r slog.Record) error {
	err := h.base.Handle(ctx, r)
	if strings.HasPrefix(r.Message, "cron: schedule run is waiting —") {
		h.records <- r.Clone()
	}
	return err
}
func (h capRedScheduleObserver) WithAttrs(a []slog.Attr) slog.Handler {
	return capRedScheduleObserver{base: h.base.WithAttrs(a), records: h.records}
}
func (h capRedScheduleObserver) WithGroup(g string) slog.Handler {
	return capRedScheduleObserver{base: h.base.WithGroup(g), records: h.records}
}

func TestCapRED_ScheduledLane_PressureWaitsBothEntryPaths(t *testing.T) {
	for _, unverifiable := range []bool{false, true} {
		for _, runNow := range []bool{false, true} {
			t.Run(fmt.Sprintf("unverifiable_%t_runNow_%t", unverifiable, runNow), func(t *testing.T) {
				high, ok := !unverifiable, !unverifiable
				capRedMemory(t, &high, &ok)
				cs, runner := capRedCronWithRealSeam(t)
				ids := []string{capRedScheduledJob(t, cs, 0), capRedScheduledJob(t, cs, 1)}
				later := time.Date(2026, 10, 31, 20, 2, 0, 0, time.UTC).UnixMilli()
				third, err := cs.AddJobFull(cron.JobSpec{Name: "cap-third", AgentID: "owner", Message: "hold", Schedule: cron.CronSchedule{Kind: "at", AtMS: &later}})
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, third.ID)
				records := make(chan slog.Record, 8)
				prior := slog.Default()
				slog.SetDefault(slog.New(capRedScheduleObserver{base: prior.Handler(), records: records}))
				t.Cleanup(func() { slog.SetDefault(prior) })
				start := func(id string) {
					runner.callers.Add(1)
					go func() {
						defer runner.callers.Done()
						_, _, err := cs.RunNow(id)
						if err != nil {
							t.Errorf("RunNow: %v", err)
						}
					}()
				}
				if runNow {
					start(ids[0])
					start(ids[1])
					capRedScheduledEntries(t, runner, 2)
					start(ids[2])
				} else {
					// Drive the real scheduler, with only two jobs due in the first event.
					cs.SetClock(&fakePlanClock{now: time.Date(2026, 10, 31, 20, 1, 1, 0, time.UTC)})
					cs.RunDueJobs()
					capRedScheduledEntries(t, runner, 2)
					cs.SetClock(&fakePlanClock{now: time.Date(2026, 10, 31, 20, 2, 1, 0, time.UTC)})
					cs.RunDueJobs()
				}
				select {
				case id := <-runner.entered:
					t.Fatalf("S18/S19 pressured third handler entered instead of waiting: %s", id)
				case record := <-records:
					cause := "this machine is low on memory"
					if unverifiable {
						cause = "this machine cannot report its available memory"
					}
					want := fmt.Sprintf("cron: schedule run is waiting — %s, so new scheduled runs are held at 2 at a time (2 in flight)", cause)
					capRedAssert(t, "exact M14a/M14b", record.Message, want)
					attrs := map[string]any{}
					record.Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value.Any(); return true })
					capRedAssert(t, "wait reports correct job", attrs["job_id"], ids[2])
					capRedAssert(t, "decision limit", fmt.Sprint(attrs["run_limit"]), "2")
				case <-time.After(3 * time.Second):
					t.Fatal("third handler neither entered nor emitted required wait evidence")
				}
				held, _ := runner.releases.Load(ids[0])
				close(held.(chan struct{}))
				got := capRedScheduledEntries(t, runner, 1)
				capRedAssert(t, "handler return releases lane", got, []string{ids[2]})
				select {
				case r := <-records:
					t.Errorf("same wait emitted a second line: %s", r.Message)
				default:
				}
			})
		}
	}
}
