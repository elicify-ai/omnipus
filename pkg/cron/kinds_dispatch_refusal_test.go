package cron

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Traces to: schedules-t10-spec-r6.md T10-FR-02 — "the dispatcher ... fail[s]
// closed on an empty, unknown, or retired-agent_turn kind (error record, no
// dispatch, loud log)". A job can only reach the dispatcher with such a kind
// through an in-place update after load, so the test re-stamps a live job.
func TestDispatcher_FailsClosedOnUnregisteredKind(t *testing.T) {
	for _, kind := range []string{"", "agent_turn", "unknown"} {
		t.Run("kind="+kind, func(t *testing.T) {
			cs, clk, path := t10Seed(t, KindHeartbeat)
			var calls atomic.Int32
			cs.SetRunner(t10RunnerFunc(func(context.Context, *CronJob) (string, error) {
				calls.Add(1)
				return "never", nil
			}))
			t10Start(t, cs)
			job, ok := cs.GetJob("heartbeat-0")
			require.True(t, ok)
			job.Payload.Kind = kind
			require.NoError(t, cs.UpdateJob(&job))

			old := slog.Default()
			var logs bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(old) })

			clk.Set(t10T0.Add(time.Minute))
			cs.RunDueJobs(clk.Now())
			cs.WaitForLane()

			assert.Equal(t, int32(0), calls.Load(), "a non-registered kind must never be dispatched")
			got, ok := cs.GetJob("heartbeat-0")
			require.True(t, ok)
			assert.False(t, got.Enabled, "the refused job is disarmed")
			assert.Nil(t, got.State.NextRunAtMS)
			assert.Equal(t, "error", got.State.LastStatus)
			assert.Contains(t, got.State.LastError, "not registered")
			assert.Empty(t, got.State.History, "a refusal is not a run")

			data, err := os.ReadFile(path)
			require.NoError(t, err)
			var disk CronStore
			require.NoError(t, json.Unmarshal(data, &disk))
			require.Len(t, disk.Jobs, 1)
			assert.False(t, disk.Jobs[0].Enabled, "the refusal is persisted")

			errorRecords := 0
			for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
				var rec map[string]any
				if json.Unmarshal([]byte(line), &rec) != nil {
					continue
				}
				if rec["job_id"] == "heartbeat-0" && rec["level"] == "ERROR" {
					errorRecords++
				}
			}
			assert.Equal(t, 1, errorRecords, "one loud error record names the refused job")

			// It does not fire again.
			clk.Set(t10T0.Add(3 * time.Minute))
			cs.RunDueJobs(clk.Now())
			cs.WaitForLane()
			assert.Equal(t, int32(0), calls.Load())
		})
	}
}

// RegisterKindHandler accepts only registered kinds and a non-nil handler.
func TestRegisterKindHandler_RefusesUnknownKindAndNilHandler(t *testing.T) {
	cs, _, _ := t10Seed(t)
	h := t10RunnerFunc(func(context.Context, *CronJob) (string, error) { return "", nil })
	for _, kind := range []string{"", "agent_turn", "alien"} {
		err := cs.RegisterKindHandler(kind, h)
		require.Error(t, err, kind)
		assert.ErrorIs(t, err, ErrUnregisteredKind)
	}
	assert.Error(t, cs.RegisterKindHandler(KindLoop, nil))
	assert.NoError(t, cs.RegisterKindHandler(KindLoop, h))
}
