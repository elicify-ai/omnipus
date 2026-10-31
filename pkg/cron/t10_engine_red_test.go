package cron

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t10T0 = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

func t10Seed(t *testing.T, kinds ...string) (*CronService, *t10Clock, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cron", "jobs.json")
	jobs := make([]CronJob, 0, len(kinds))
	for i, kind := range kinds {
		every := int64(time.Minute / time.Millisecond)
		jobs = append(jobs, CronJob{ID: fmt.Sprintf("%s-%d", kind, i), Name: "fixture-" + kind,
			AgentID: "mia", Enabled: true, Payload: CronPayload{Kind: kind, Message: "input-" + kind},
			Schedule: CronSchedule{Kind: "every", EveryMS: &every}, SessionMode: SessionModeContinue})
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	data, err := json.Marshal(CronStore{Version: 1, Jobs: jobs})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	cs := NewCronService(path)
	clk := &t10Clock{at: t10T0}
	cs.SetClock(clk)
	return cs, clk, path
}

func t10Start(t *testing.T, cs *CronService) {
	t.Helper()
	require.NoError(t, cs.startNoLoop())
	t.Cleanup(func() { cs.Stop(); cs.WaitForLane() })
}

// Traces to: schedules-t10-spec-r6.md lines 594, 717; TDD 1, D-4.5.
func TestJobSpecKind_RequiredAndKnown(t *testing.T) {
	for _, kind := range []string{"heartbeat", "loop", "task", "", "alien", "agent_turn"} {
		t.Run("kind="+kind, func(t *testing.T) {
			cs, _, path := t10Seed(t)
			spec := t10Spec(t, kind)
			every := int64(60000)
			spec.Schedule = CronSchedule{Kind: "every", EveryMS: &every}
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			job, err := cs.AddJobFull(spec)
			if kind == "" || kind == "alien" || kind == "agent_turn" {
				require.Error(t, err, "non-registered kinds must not be creatable")
				assert.Contains(t, strings.ToLower(err.Error()), "kind")
				assert.Empty(t, cs.ListJobs(true))
				after, readErr := os.ReadFile(path)
				require.NoError(t, readErr)
				assert.Equal(t, before, after)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, job)
			assert.Equal(t, kind, job.Payload.Kind)
			assert.Equal(t, "message-"+kind, job.Payload.Message)
			stored, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			var disk CronStore
			require.NoError(t, json.Unmarshal(stored, &disk))
			require.Len(t, disk.Jobs, 1)
			assert.Equal(t, *job, disk.Jobs[0], "write/read must preserve full content")
		})
	}
}

// Traces to: schedules-t10-spec-r6.md lines 594, 1080; TDD 4/17, D-1.4/5, D-3.8, D-10.1.
func TestT10_NonRegisteredKindsDroppedOnceAtLoad(t *testing.T) {
	for _, kind := range []string{"", "unknown-kind", "agent_turn"} {
		t.Run("kind="+kind, func(t *testing.T) {
			cs, clk, path := t10Seed(t, kind, "heartbeat")
			old := slog.Default()
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(old) })
			// Constructor load predates log capture; a dropped job must already be gone.
			cs.SetRunner(t10RunnerFunc(func(_ context.Context, j *CronJob) (string, error) {
				assert.Equal(t, "heartbeat", j.Payload.Kind, "retired/unknown job must never execute")
				return "heartbeat-session", nil
			}))
			t10Start(t, cs)
			jobs := cs.ListJobs(true)
			require.Len(t, jobs, 1, "registered-kinds-only load must drop %q, not merely disable it", kind)
			assert.Equal(t, "heartbeat", jobs[0].Payload.Kind)
			for _, offset := range []time.Duration{2 * time.Hour, 3 * time.Hour} {
				clk.Set(t10T0.Add(offset))
				cs.RunDueJobs(clk.Now())
				cs.WaitForLane()
			}
			disk, err := os.ReadFile(path)
			require.NoError(t, err)
			var state CronStore
			require.NoError(t, json.Unmarshal(disk, &state))
			require.Len(t, state.Jobs, 1)
			assert.NotContains(t, logs.String(), "cron: job failed", "dropped residue must not produce repeating error runs")
		})
	}
}

// Traces to: schedules-t10-spec-r6.md lines 594, 1080; FR-25 requires counted, named, loud per-job logging.
func TestT10_LoadDropLoggingNamesEveryJobExactlyOnce(t *testing.T) {
	old := slog.Default()
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(old)
	cs, _, _ := t10Seed(t, "agent_turn", "unknown", "", "heartbeat")
	t10Start(t, cs)
	for _, id := range []string{"agent_turn-0", "unknown-1", "-2"} {
		count := 0
		for _, line := range strings.Split(logs.String(), "\n") {
			if strings.Contains(line, id) {
				var record map[string]any
				require.NoError(t, json.Unmarshal([]byte(line), &record))
				assert.Contains(t, []any{"WARN", "ERROR"}, record["level"])
				count++
			}
		}
		assert.Equal(t, 1, count, "one loud load-drop record must name %s", id)
	}
}

// Traces to: schedules-t10-spec-r6.md lines 587, 601; TDD 3.
func TestDispatcher_RoutesByKind_Exclusively(t *testing.T) {
	cs, clk, _ := t10Seed(t, "heartbeat", "loop", "task")
	var mu sync.Mutex
	got := map[string][]string{}
	handlers := map[string]ScheduledRunner{}
	for _, kind := range []string{"heartbeat", "loop", "task"} {
		kind := kind
		handlers[kind] = t10RunnerFunc(func(_ context.Context, j *CronJob) (string, error) {
			// Re-entering the engine would deadlock if the dispatcher held its lock.
			if _, found := cs.GetJob(j.ID); !found {
				return "", fmt.Errorf("dispatched job %s is missing from the live engine", j.ID)
			}
			mu.Lock()
			got[kind] = append(got[kind], j.Payload.Message)
			mu.Unlock()
			return "session-" + kind, nil
		})
	}
	t10InstallHandlers(t, cs, handlers)
	t10Start(t, cs)
	clk.Set(t10T0.Add(time.Minute))
	cs.RunDueJobs(clk.Now())
	done := make(chan struct{})
	go func() { cs.WaitForLane(); close(done) }()
	t10Await(t, done)
	assert.Equal(t, map[string][]string{"heartbeat": {"input-heartbeat"}, "loop": {"input-loop"}, "task": {"input-task"}}, got)
	for _, j := range cs.ListJobs(true) {
		assert.Equal(t, "session-"+j.Payload.Kind, j.SessionID)
		assert.Equal(t, "ok", j.State.LastStatus)
	}
}

// Traces to: schedules-t10-spec-r6.md lines 724, 731, 739, 803; TDD 11, D-2 all rows.
func TestScheduledAdmission_MemoryGoverned(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		n, want     int
	}{
		{"D2-1-no-pressure-25", "healthy", 25, 25}, {"D2-3-pressure-mixed", "pressure", 3, 2},
		{"D2-4-unmeasurable", "unmeasurable", 3, 2}, {"D2-5-burst", "pressure", 6, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kinds := make([]string, tc.n)
			for i := range kinds {
				kinds[i] = []string{"heartbeat", "task", "loop"}[i%3]
			}
			cs, clk, _ := t10Seed(t, kinds...)
			t10InstallMemoryProvider(t, cs, tc.state)
			entered := make(chan string, tc.n)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			cs.SetRunner(t10RunnerFunc(func(ctx context.Context, j *CronJob) (string, error) {
				entered <- j.ID
				select {
				case <-release:
				case <-ctx.Done():
				}
				return "actual-" + j.ID, nil
			}))
			t10Start(t, cs)
			clk.Set(t10T0.Add(time.Minute))
			cs.RunDueJobs(clk.Now())
			got := []string{}
			for i := 0; i < tc.want; i++ {
				got = append(got, t10Await(t, entered))
			}
			assert.Len(t, got, tc.want)
			if tc.want < tc.n {
				select {
				case id := <-entered:
					t.Errorf("memory floor bypassed by %s", id)
				default:
				}
			}
			unblock()
			cs.WaitForLane()
			for len(got) < tc.n {
				got = append(got, t10Await(t, entered))
			}
			sort.Strings(got)
			assert.Len(t, got, tc.n, "queued work must eventually execute, not disappear")
			for _, j := range cs.ListJobs(true) {
				assert.Equal(t, "actual-"+j.ID, j.SessionID)
				assert.Len(t, j.State.History, 1)
			}
		})
	}
}

// Traces to: schedules-t10-spec-r6.md lines 724, 1359; T10-Q8 removes the field, setter and semaphore.
func TestT10_NoFixedLaneLimitRemains(t *testing.T) {
	typ := reflect.TypeOf(CronService{})
	for _, name := range []string{"maxConcurrentRuns", "laneSem"} {
		_, found := typ.FieldByName(name)
		assert.False(t, found, "removed fixed lane field %s is still present", name)
	}
	_, setter := reflect.TypeOf((*CronService)(nil)).MethodByName("SetMaxConcurrentRuns")
	assert.False(t, setter, "removed capacity setter still exists")
}

// Traces to: schedules-t10-spec-r6.md lines 755, 768, 774, 780; TDD 12, D-1.1/3/6/8.
// PASSING preserve-behavior characterization, not manufactured RED (§11 preamble).
func TestPerKindRules_Preserved(t *testing.T) {
	t.Run("heartbeat-backoff-three-attempts-reset-success", func(t *testing.T) {
		cs, clk, _ := t10Seed(t, "heartbeat")
		attempt := 0
		cs.SetRunner(t10RunnerFunc(func(_ context.Context, j *CronJob) (string, error) {
			attempt++
			if attempt <= 3 {
				return "", &TransientError{Err: errors.New("provider overloaded")}
			}
			return "recovered-heartbeat", nil
		}))
		t10Start(t, cs)
		at := t10T0.Add(time.Minute)
		for i, delay := range []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute} {
			clk.Set(at)
			cs.RunDueJobs(at)
			cs.WaitForLane()
			j, ok := cs.GetJob("heartbeat-0")
			require.True(t, ok)
			assert.Equal(t, i+1, j.State.RetryAttempt)
			require.NotNil(t, j.State.NextRunAtMS)
			assert.Equal(t, at.Add(delay).UnixMilli(), *j.State.NextRunAtMS)
			assert.Equal(t, "provider overloaded", j.State.LastError)
			at = at.Add(delay)
		}
		clk.Set(at)
		cs.RunDueJobs(at)
		cs.WaitForLane()
		j, ok := cs.GetJob("heartbeat-0")
		require.True(t, ok)
		assert.Equal(t, 0, j.State.RetryAttempt)
		assert.Equal(t, 0, j.State.ConsecutiveFailures)
		assert.Equal(t, "recovered-heartbeat", j.SessionID)
	})
	for _, kind := range []string{"heartbeat", "loop"} {
		t.Run(kind+"-20-record-retention", func(t *testing.T) {
			cs, clk, _ := t10Seed(t, kind)
			cs.SetRunner(t10RunnerFunc(func(_ context.Context, j *CronJob) (string, error) { return "session-" + j.ID, nil }))
			t10Start(t, cs)
			for i := 1; i <= 25; i++ {
				clk.Set(t10T0.Add(time.Duration(i) * time.Minute))
				cs.RunDueJobs(clk.Now())
				cs.WaitForLane()
			}
			j, ok := cs.GetJob(kind + "-0")
			require.True(t, ok)
			require.Len(t, j.State.History, 20)
			assert.Equal(t, t10T0.Add(6*time.Minute).UnixMilli(), j.State.History[0].RanAtMs)
			assert.Equal(t, t10T0.Add(25*time.Minute).UnixMilli(), j.State.History[19].RanAtMs)
			assert.Equal(t, "session-"+j.ID, j.State.History[19].SessionID)
		})
	}
	for _, kind := range []string{"heartbeat", "loop", "task"} {
		t.Run(kind+"-no-lifetime-deadline", func(t *testing.T) {
			cs, clk, _ := t10Seed(t, kind)
			entered := make(chan context.Context, 1)
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			cs.SetRunner(t10RunnerFunc(func(ctx context.Context, j *CronJob) (string, error) {
				entered <- ctx
				<-release
				return "long-" + j.ID, nil
			}))
			t10Start(t, cs)
			clk.Set(t10T0.Add(time.Minute))
			cs.RunDueJobs(clk.Now())
			ctx := t10Await(t, entered)
			_, deadline := ctx.Deadline()
			assert.False(t, deadline, "elapsed time must not impose a lifetime deadline")
			clk.Set(clk.Now().Add(301 * time.Second))
			cs.RunDueJobs(clk.Now())
			assert.NoError(t, ctx.Err())
			once.Do(func() { close(release) })
			cs.WaitForLane()
			j, ok := cs.GetJob(kind + "-0")
			require.True(t, ok)
			assert.Equal(t, "ok", j.State.LastStatus)
			assert.Equal(t, int64(301000), j.State.History[0].DurationMs)
		})
	}
}
