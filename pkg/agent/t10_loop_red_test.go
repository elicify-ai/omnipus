//go:build goolm && stdjson

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type t10LoopProvider struct {
	calls   atomic.Int32
	failure error
	onChat  func(context.Context)
}

func (p *t10LoopProvider) GetDefaultModel() string { return "test-model" }
func (p *t10LoopProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	if p.onChat != nil {
		p.onChat(ctx)
	}
	if p.failure != nil {
		return nil, p.failure
	}
	return &providers.LLMResponse{Content: "t10 real turn result\nLOOP_NEXT: 2m — continue"}, nil
}

type t10LoopFixture struct {
	al    *AgentLoop
	ls    *LoopScheduler
	clk   *triggerFakeClock
	store *session.UnifiedStore
	sid   string
	inst  *AgentInstance
	p     *t10LoopProvider
	path  string
}

func t10LoopSetup(t *testing.T, worker bool) *t10LoopFixture {
	t.Helper()
	p := &t10LoopProvider{}
	al, _ := newGoalLoopTestLoop(t, p, func(c *config.Config) {
		if !worker {
			c.Agents.List[0].Type = "custom"
		}
	})
	store, sid := newGoalTestSession(t, al, "native-agent")
	inst, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok)
	path := filepath.Join(t.TempDir(), "cron", "jobs.json")
	ls := t10NewLoop(t, path, al)
	clk := newTriggerFakeClock()
	ls.SetClock(clk)
	al.SetLoopScheduler(ls)
	t.Cleanup(func() { ls.Stop(); ls.WaitForLane() })
	return &t10LoopFixture{al, ls, clk, store, sid, inst, p, path}
}
func (f *t10LoopFixture) command(t *testing.T, text string) {
	t.Helper()
	matched, handled, reply := f.al.applyLoopCommandPrompt(context.Background(), bus.InboundMessage{Content: text}, f.inst, &processOptions{UserInitiated: true, TranscriptStore: f.store, TranscriptSessionID: f.sid})
	require.True(t, matched)
	require.True(t, handled)
	require.NotContains(t, reply, "Could not")
	require.NotContains(t, reply, "unavailable")
}
func (f *t10LoopFixture) meta(t *testing.T) *session.UnifiedMeta {
	t.Helper()
	m, e := f.store.GetMeta(f.sid)
	require.NoError(t, e)
	require.NotNil(t, m)
	return m
}
func t10SessionLoopJobs(s *LoopScheduler, sid string) []cron.CronJob {
	var out []cron.CronJob
	for _, j := range s.cs.ListJobs(true) {
		if j.Payload.Kind == "loop" && j.Payload.Message == sid {
			out = append(out, j)
		}
	}
	return out
}

// Traces to: schedules-t10-spec-r6.md lines 645, 1646; TDD 8, D-3.6.
func TestLoopBootReconcile_ReArmsOverdueOneShot(t *testing.T) {
	for _, delay := range []int64{1000, 0, -1, 120000} {
		t.Run(time.Duration(delay*time.Millisecond.Nanoseconds()).String(), func(t *testing.T) {
			f := t10LoopSetup(t, false)
			f.command(t, "/loop check build")
			initial := f.meta(t)
			assert.Equal(t, int64(firstSelfPacedRunDelayMS), initial.LoopNextDelayMS, "real /loop creation must persist its initial positive delay")
			oldID := initial.LoopJobID
			if delay != 1000 {
				require.NoError(t, f.store.SetMeta(f.sid, session.MetaPatch{LoopNextDelayMS: &delay}))
			}
			f.clk.Advance(20 * time.Minute)
			boot := f.clk.Now()
			require.NoError(t, t10LoopRecovery(t, f.ls))
			jobs := t10SessionLoopJobs(f.ls, f.sid)
			require.Len(t, jobs, 1)
			assert.NotEqual(t, oldID, jobs[0].ID, "overdue at must be replaced with a fresh future at")
			want := delay
			if want <= 0 {
				want = firstSelfPacedRunDelayMS
			}
			assert.Equal(t, "at", jobs[0].Schedule.Kind)
			require.NotNil(t, jobs[0].State.NextRunAtMS)
			assert.Equal(t, boot.Add(time.Duration(want)*time.Millisecond).UnixMilli(), *jobs[0].State.NextRunAtMS)
			assert.Equal(t, jobs[0].ID, f.meta(t).LoopJobID)
			// Real runner proves recovery did more than write a plausible job shape.
			f.clk.Advance(time.Duration(want) * time.Millisecond)
			_, err := f.ls.RunScheduled(context.Background(), &jobs[0])
			require.NoError(t, err)
			assert.Equal(t, int32(1), f.p.calls.Load())
			assert.Equal(t, 1, f.meta(t).LoopRunCount)
			assert.Equal(t, int64(120000), f.meta(t).LoopNextDelayMS, "LOOP_NEXT chaining must resume")
		})
	}
}

// Traces to: schedules-t10-spec-r6.md lines 652, 661, 685; TDD 8b, D-3b.1–6/8–10/13/16.
func TestLoopBootReconcile_OwnershipAndCrashWindows(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		successor, metaJ2, missing, orphan bool
		count                              int
		mode                               string
	}{
		{name: "D3b-1-successor-before-meta", successor: true},
		{name: "D3b-2-meta-before-predecessor-delete", successor: true, metaJ2: true},
		{name: "D3b-3-before-successor"},
		{name: "D3b-4-orphan", orphan: true},
		{name: "D3b-8-limit", count: 3},
		{name: "D3b-9-limit-minus-one", count: 2},
		{name: "D3b-13-no-job-below-limit", missing: true},
		{name: "D3b-13-no-job-at-limit", missing: true, count: 3},
		{name: "D3b-16-interval-missing", missing: true, mode: "interval"},
		{name: "D3b-16-self-paced-twin", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := t10LoopSetup(t, false)
			command := "/loop check build"
			if tc.mode == "interval" {
				command = "/loop every 15m check build"
			}
			f.command(t, command)
			m := f.meta(t)
			maxRuns := 3
			require.NoError(t, f.store.SetMeta(f.sid, session.MetaPatch{LoopRunCount: &tc.count, LoopMaxRuns: &maxRuns}))
			first, _ := f.ls.cs.GetJob(m.LoopJobID)
			var latest string
			if tc.successor {
				var err error
				latest, err = f.ls.AddOneShot("native-agent", f.sid, int64(40*time.Minute/time.Millisecond))
				require.NoError(t, err)
				if tc.metaJ2 {
					require.NoError(t, f.store.SetMeta(f.sid, session.MetaPatch{LoopJobID: &latest}))
				}
			}
			if tc.missing {
				require.True(t, f.ls.RemoveJob(m.LoopJobID))
			}
			if tc.orphan {
				empty := ""
				require.NoError(t, f.store.SetMeta(f.sid, session.MetaPatch{LoopMode: &empty}))
			}
			f.clk.Advance(20 * time.Minute)
			require.NoError(t, t10LoopRecovery(t, f.ls))
			jobs := t10SessionLoopJobs(f.ls, f.sid)
			if tc.orphan || tc.count == 3 {
				assert.Empty(t, jobs)
				assert.Empty(t, f.meta(t).LoopMode)
				assert.Zero(t, f.p.calls.Load())
				return
			}
			require.Len(t, jobs, 1)
			assert.Equal(t, jobs[0].ID, f.meta(t).LoopJobID)
			if tc.successor {
				assert.Equal(t, latest, jobs[0].ID)
			} else {
				assert.NotEqual(t, first.ID, jobs[0].ID)
			}
			if tc.mode == "interval" {
				assert.Equal(t, "every", jobs[0].Schedule.Kind)
				require.NotNil(t, jobs[0].Schedule.EveryMS)
				assert.Equal(t, int64(900000), *jobs[0].Schedule.EveryMS)
			} else {
				assert.Equal(t, "at", jobs[0].Schedule.Kind)
			}
			before := jobs
			beforeMeta := *f.meta(t)
			require.NoError(t, t10LoopRecovery(t, f.ls))
			assert.Equal(t, before, t10SessionLoopJobs(f.ls, f.sid), "D3b-10: repeated recovery is idempotent")
			assert.Equal(t, beforeMeta, *f.meta(t))
			if tc.mode == "interval" {
				for i := 0; i < 2; i++ {
					f.clk.Advance(15 * time.Minute)
					_, err := f.ls.RunScheduled(context.Background(), &jobs[0])
					require.NoError(t, err)
				}
				assert.Equal(t, int32(2), f.p.calls.Load(), "at least TWO intervals, not a plausible one-shot that stalls")
			}
			if tc.count == maxRuns-1 {
				// D-3b.9 is an execution bound, not only a recovered job shape.
				require.NoError(t, f.ls.Start())
				require.NotNil(t, jobs[0].State.NextRunAtMS)
				f.clk.Advance(time.UnixMilli(*jobs[0].State.NextRunAtMS).Sub(f.clk.Now()))
				f.ls.RunDueJobs(f.clk.Now())
				f.ls.WaitForLane()
				assert.Equal(t, int32(1), f.p.calls.Load(), "last permitted step must actually execute")
				assert.Empty(t, f.meta(t).LoopMode, "saved limit is exhausted after exactly one more step")
				assert.Empty(t, t10SessionLoopJobs(f.ls, f.sid))
				f.clk.Advance(24 * time.Hour)
				f.ls.RunDueJobs(f.clk.Now())
				f.ls.WaitForLane()
				assert.Equal(t, int32(1), f.p.calls.Load(), "no step beyond the saved limit")
			}
			for _, stopFirst := range []bool{false, true} {
				if stopFirst {
					f.command(t, "/loop stop")
				}
				f.command(t, "/loop replacement")
				remaining := t10SessionLoopJobs(f.ls, f.sid)
				require.Len(t, remaining, 1)
				assert.NotEqual(t, jobs[0].ID, remaining[0].ID)
				assert.Equal(t, remaining[0].ID, f.meta(t).LoopJobID)
			}
		})
	}
}

// Traces to: schedules-t10-spec-r6.md lines 668, 1196; TDD 8b, D-3b.14/15.
func TestT10_LoopRevokedCallbackHasNoPostTurnEffects(t *testing.T) {
	for _, variant := range []string{"replace", "stop-then-start"} {
		t.Run(variant, func(t *testing.T) {
			f := t10LoopSetup(t, false)
			f.command(t, "/loop old")
			one := 1
			require.NoError(t, f.store.SetMeta(f.sid, session.MetaPatch{LoopMaxRuns: &one}))
			old, ok := f.ls.cs.GetJob(f.meta(t).LoopJobID)
			require.True(t, ok)
			entered, release := make(chan struct{}), make(chan struct{})
			var gate sync.Once
			t10PostTurnGate(t, f.ls, func() { gate.Do(func() { close(entered); <-release }) })
			done := make(chan error, 1)
			go func() { _, err := f.ls.RunScheduled(context.Background(), &old); done <- err }()
			var unblock sync.Once
			t.Cleanup(func() { unblock.Do(func() { close(release) }) })
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("post-return hook never reached; no-op turn cannot satisfy this test")
			}
			assert.Equal(t, int32(1), f.p.calls.Load(), "old turn completed, not cancelled")
			if variant == "stop-then-start" {
				f.command(t, "/loop stop")
			}
			f.command(t, "/loop replacement")
			before := *f.meta(t)
			jobs := t10SessionLoopJobs(f.ls, f.sid)
			require.Len(t, jobs, 1)
			require.NotEqual(t, old.ID, jobs[0].ID)
			sub := f.al.SubscribeEvents(32)
			defer f.al.UnsubscribeEvents(sub.ID)
			unblock.Do(func() { close(release) })
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("old callback failed to settle")
			}
			after := *f.meta(t)
			assert.Equal(t, before.LoopRunCount, after.LoopRunCount)
			assert.Equal(t, before.LoopLastActivityAt, after.LoopLastActivityAt)
			assert.Equal(t, before.LoopJobID, after.LoopJobID)
			assert.Equal(t, before.LoopNextDelayMS, after.LoopNextDelayMS)
			assert.Equal(t, before.LoopMode, after.LoopMode, "stale run-cap brake cannot stop replacement")
			assert.Equal(t, jobs, t10SessionLoopJobs(f.ls, f.sid), "no stale successor chain")
			for len(sub.C) > 0 {
				e := <-sub.C
				assert.NotEqual(t, EventKindLoopStatusChanged, e.Kind, "revoked callback must publish no loop frame")
			}
			// Positive continuation: a no-op runner cannot pass only the negative assertions.
			require.NoError(t, f.ls.Start())
			require.NotNil(t, jobs[0].State.NextRunAtMS)
			f.clk.Advance(time.UnixMilli(*jobs[0].State.NextRunAtMS).Sub(f.clk.Now()))
			f.ls.RunDueJobs(f.clk.Now())
			f.ls.WaitForLane()
			assert.Equal(t, int32(2), f.p.calls.Load())
			assert.Equal(t, 1, f.meta(t).LoopRunCount)
			assert.Len(t, t10SessionLoopJobs(f.ls, f.sid), 1)
		})
	}
}

// Traces to: schedules-t10-spec-r6.md lines 668, 485; entry-time half of FR-05(g).
func TestT10_LoopNonSelectedJobRunsNoTurnAndRemovesItself(t *testing.T) {
	f := t10LoopSetup(t, false)
	f.command(t, "/loop current")
	oldID := f.meta(t).LoopJobID
	old, ok := f.ls.cs.GetJob(oldID)
	require.True(t, ok)
	current, err := f.ls.AddOneShot("native-agent", f.sid, 600000)
	require.NoError(t, err)
	// LoopJobID intentionally stays stale: authority is selection, not the pointer.
	before := *f.meta(t)
	_, _ = f.ls.RunScheduled(context.Background(), &old)
	assert.Zero(t, f.p.calls.Load(), "a non-selected callback must not run an agent turn")
	assert.Equal(t, before, *f.meta(t), "no count/activity/identity mutation is allowed")
	jobs := t10SessionLoopJobs(f.ls, f.sid)
	require.Len(t, jobs, 1)
	assert.Equal(t, current, jobs[0].ID)
}

// Traces to: schedules-t10-spec-r6.md lines 677, 1189, 1193–1199; TDD 8b, D-3b.7/11/12/13/17.
func TestT10_LoopRecoveryPersistenceFailures(t *testing.T) {
	for _, row := range []string{"D3b-7-single-repair-failure", "D3b-11-sustained-failure", "D3b-12-repeat-failure", "D3b-13-removal-before-persist", "D3b-17-next-boot-retries-single-failure"} {
		t.Run(row, func(t *testing.T) {
			f := t10LoopSetup(t, false)
			f.command(t, "/loop persistent")
			m := f.meta(t)
			// Force identity repair: metadata still names J1, latest J2 survives.
			_, err := f.ls.AddOneShot("native-agent", f.sid, 600000)
			require.NoError(t, err)
			if row == "D3b-13-removal-before-persist" {
				for _, j := range t10SessionLoopJobs(f.ls, f.sid) {
					require.True(t, f.ls.RemoveJob(j.ID))
				}
			}
			path := filepath.Join(f.store.BaseDir(), f.sid, "loop.json")
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			injected := errors.New("T10 loop recovery identity persistence failed")
			writes, failures := 0, 0
			sustained := strings.Contains(row, "sustained") || strings.Contains(row, "repeat")
			restore := t10FailSingleRecoveryWrite(t, f.store, func(sid string, p session.MetaPatch) error {
				if sid != f.sid {
					return nil
				}
				writes++
				if sustained || (p.LoopJobID != nil && failures == 0) {
					failures++
					return injected
				}
				return nil // a fallback stop WOULD succeed in single-fault rows
			})
			bootErr := t10LoopRecovery(t, f.ls)
			require.Error(t, bootErr, "no-op reconciliation cannot pass a failed required write")
			assert.Contains(t, bootErr.Error(), f.sid)
			assert.ErrorIs(t, bootErr, injected)
			assert.Positive(t, failures, "real persistence edge must have been reached")
			assert.Empty(t, t10SessionLoopJobs(f.ls, f.sid), "failed selected/created job must be removed")
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after, "no stopped-state fallback or partial identity")
			assert.Equal(t, m.LoopMode, f.meta(t).LoopMode)
			assert.Equal(t, m.LoopJobID, f.meta(t).LoopJobID)
			assert.Zero(t, f.p.calls.Load())
			if sustained {
				again := t10LoopRecovery(t, f.ls)
				require.Error(t, again)
				assert.Contains(t, again.Error(), f.sid)
				bytes, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, before, bytes)
				assert.Empty(t, t10SessionLoopJobs(f.ls, f.sid))
			}
			restore()
			require.Positive(t, writes)
			require.NoError(t, t10LoopRecovery(t, f.ls), "next boot retries durable active metadata")
			jobs := t10SessionLoopJobs(f.ls, f.sid)
			require.Len(t, jobs, 1)
			assert.Equal(t, jobs[0].ID, f.meta(t).LoopJobID)
			assert.Equal(t, m.LoopMode, f.meta(t).LoopMode)
			assert.NotEqual(t, m.LoopJobID, jobs[0].ID)
			// Successful recovery must lead to actual progress, not just no errors.
			require.NoError(t, f.ls.Start())
			require.NotNil(t, jobs[0].State.NextRunAtMS)
			f.clk.Advance(time.UnixMilli(*jobs[0].State.NextRunAtMS).Sub(f.clk.Now()))
			f.ls.RunDueJobs(f.clk.Now())
			f.ls.WaitForLane()
			assert.Equal(t, int32(1), f.p.calls.Load())
			assert.Equal(t, m.LoopRunCount+1, f.meta(t).LoopRunCount)
		})
	}
}

// Traces to: schedules-t10-spec-r6.md lines 790, 796; TDD 13. Owner errors are distinct from admission.
func TestLoopRunner_G5Protections(t *testing.T) {
	for _, tc := range []struct {
		name, owner, reason string
		worker              bool
	}{
		{"missing-owner", "does-not-exist", "owner", false}, {"worker-owner", "native-agent", "worker", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := t10LoopSetup(t, tc.worker)
			f.command(t, "/loop check owner")
			jobs := t10SessionLoopJobs(f.ls, f.sid)
			require.Len(t, jobs, 1)
			job := jobs[0]
			job.AgentID = tc.owner
			_, err := f.ls.RunScheduled(context.Background(), &job)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.reason)
			assert.Zero(t, f.p.calls.Load(), "G5 owner refusal must happen before any agent turn")
		})
	}
	t.Run("S-F.4-child-cleanup-success", func(t *testing.T) { t10LoopChildCleanup(t, false) })
	t.Run("S-F.4-child-cleanup-error", func(t *testing.T) { t10LoopChildCleanup(t, true) })
	t.Run("S-F.5-audit-job-identity", t10LoopAuditIdentity)
}

// Traces to: schedules-t10-spec-r6.md lines 652–658; D-3b.5/6, FR-05(d).
func TestT10_LoopStopAndReplaceRemoveEverySessionJob(t *testing.T) {
	for _, action := range []string{"stop", "replace"} {
		t.Run(action, func(t *testing.T) {
			f := t10LoopSetup(t, false)
			f.command(t, "/loop original")
			duplicate, err := f.ls.AddOneShot("native-agent", f.sid, 600000)
			require.NoError(t, err)
			other, err := f.ls.AddOneShot("native-agent", "another-session", 1200000)
			require.NoError(t, err)
			// The metadata still names the predecessor, reproducing the interrupted
			// repair shape. Cleanup cannot trust LoopJobID as its sole authority.
			if action == "stop" {
				f.command(t, "/loop stop")
				assert.Empty(t, t10SessionLoopJobs(f.ls, f.sid))
			} else {
				f.command(t, "/loop replacement")
				jobs := t10SessionLoopJobs(f.ls, f.sid)
				require.Len(t, jobs, 1)
				assert.Equal(t, f.meta(t).LoopJobID, jobs[0].ID)
				assert.NotEqual(t, duplicate, jobs[0].ID)
			}
			_, exists := f.ls.cs.GetJob(duplicate)
			assert.False(t, exists, "orphan successor must be removed by session")
			_, exists = f.ls.cs.GetJob(other)
			assert.True(t, exists, "cleanup must not touch another session's loop")
		})
	}
}
