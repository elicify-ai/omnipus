package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder 2026-10-29 q1 a / fix-round-3, plus the explicit storage-error
// clarification: ALL later cleanup passes must run, even if a genuine session
// storage error still makes the session sweep fail. No ticker, fake session
// sweep/error, fake tool sweep or fake task-run pruning is involved.
func TestRetentionHistoryGateway_TickContinuesRealCleanupForUnavailableChat(t *testing.T) {
	for _, reason := range []string{"catchup_limit", "index_inconsistent", "index_missing", "storage_error", "healthy"} {
		t.Run(reason, func(t *testing.T) {
			logs := retentionGatewayCapture(t)
			f := retentionGatewayReopenedFixture(t, reason)
			retentionGatewayRequireP3(t, logs, f, reason) // boot already warned once, unless healthy
			index := retentionGatewayFiles(t, f.indexDir())
			canonical := retentionGatewayCanonical(t, f)

			home := t.TempDir()
			tools := newToolResultStore(home)
			freshBody := []byte(`{"owned":"fresh tool result"}`)
			freshRef, ok := tools.saveJSON(f.sid, freshBody)
			require.True(t, ok)
			fresh, err := tools.readByRef(f.sid, freshRef)
			require.NoError(t, err)
			require.Equal(t, freshBody, fresh)
			var toolDays, toolRemoved []int
			oldTools := retentionToolResultSweepFn
			retentionToolResultSweepFn = func(days int) (int, error) {
				toolDays = append(toolDays, days)
				removed, err := tools.retentionSweep(days)
				toolRemoved = append(toolRemoved, removed)
				retentionGatewayRequireSummaryCount(t, logs, len(toolDays)-1)
				return removed, err
			}
			t.Cleanup(func() { retentionToolResultSweepFn = oldTools })

			tasks, taskDir := newTestTaskStore(t)
			taskID := mustCreateTestTask(t, tasks, "owned retention isolation task")
			now := time.Now().UTC()
			agedAt := now.Add(-30 * 24 * time.Hour)
			freshRun := writeClosedRunDayFile(t, taskDir, taskID, now, now)
			freshRunBytes, err := os.ReadFile(freshRun)
			require.NoError(t, err)
			var taskCutoffs []time.Time
			var taskVisited []int
			oldTasks := retentionTaskRunSweepFn
			retentionTaskRunSweepFn = func(cutoff time.Time) (int, error) {
				taskCutoffs = append(taskCutoffs, cutoff)
				visited, err := pruneAllTaskRuns(tasks, cutoff)
				taskVisited = append(taskVisited, visited)
				retentionGatewayRequireSummaryCount(t, logs, len(taskCutoffs)-1)
				return visited, err
			}
			t.Cleanup(func() { retentionTaskRunSweepFn = oldTasks })

			for repeat := 1; repeat <= 2; repeat++ {
				agedSession := retentionGatewayAgedSession(t, f.store)
				agedRef, saved := tools.saveJSON(f.sid, []byte(`{"owned":"aged tool result"}`))
				require.True(t, saved)
				agedTool := filepath.Join(home, "tool_results", f.sid, agedRef+".json")
				require.NoError(t, os.Chtimes(agedTool, agedAt, agedAt))
				agedBody, readErr := tools.readByRef(f.sid, agedRef)
				require.NoError(t, readErr, "positive control: aged tool result really exists before tick")
				require.JSONEq(t, `{"owned":"aged tool result"}`, string(agedBody))
				agedRun := writeClosedRunDayFile(t, taskDir, taskID, agedAt, agedAt)
				require.FileExists(t, agedRun)

				before := time.Now()
				executeSweepTick(f.store, enabledCfg(7)) // REAL default session.RetentionSweep; never replace retentionSweepFn
				after := time.Now()

				require.NoDirExists(t, agedSession, "unavailable history must not preserve another chat's aged session")
				wantDays, wantOne := make([]int, repeat), make([]int, repeat)
				for i := range wantDays {
					wantDays[i], wantOne[i] = 7, 1
				}
				assert.Equal(t, wantDays, toolDays, "tool pass must run ONCE per tick with the same7-day window")
				assert.Equal(t, wantOne, toolRemoved, "real tool sweep removes exactly the new aged result each tick")
				_, readErr = tools.readByRef(f.sid, agedRef)
				assert.ErrorIs(t, readErr, ErrToolResultNotFound, "aged result must actually be deleted, not a mocked count")
				fresh, err = tools.readByRef(f.sid, freshRef)
				require.NoError(t, err)
				require.Equal(t, freshBody, fresh, "fresh tool result survives unchanged")

				assert.Len(t, taskCutoffs, repeat, "task-run pass still runs after any session-sweep error")
				assert.Equal(t, wantOne, taskVisited, "real fan-out visits the persisted task each tick")
				if len(taskCutoffs) == repeat {
					// This brackets the actual clock call, not a stopwatch tolerance:
					// cutoff must be NOW minus exactly the configured7 days.
					cutoff := taskCutoffs[repeat-1]
					require.False(t, cutoff.Before(before.Add(-7*24*time.Hour)))
					require.False(t, cutoff.After(after.Add(-7*24*time.Hour)))
				}
				assert.NoFileExists(t, agedRun, "actual aged task-run day file expires")
				afterRun, err := os.ReadFile(freshRun)
				require.NoError(t, err)
				require.Equal(t, freshRunBytes, afterRun, "fresh closed run remains intact")

				failures := retentionGatewayEvents(t, logs, "retention_sweep: sweep failed")
				if reason == "storage_error" {
					assert.Len(t, failures, repeat, "real disk errors remain visible on EVERY failed sweep")
					for _, failure := range failures {
						require.Equal(t, "ERROR", failure["level"])
						require.Equal(t, "retention_sweep_failed", failure["event"])
						require.Equal(t, "chat history unavailable", failure["error"])
					}
				} else {
					assert.Empty(t, failures, "a per-chat index-state warning is not a global sweep failure")
					completed := retentionGatewayEvents(t, logs, "retention_sweep: completed")
					assert.Len(t, completed, repeat)
					for _, event := range completed {
						require.Equal(t, json.Number("1"), event["removed"])
						require.Equal(t, json.Number("1"), event["tool_result_removed"])
						require.Equal(t, json.Number("1"), event["task_runs_visited"])
					}
				}
				require.Empty(t, retentionGatewayEvents(t, logs, "retention_sweep: tick panic recovered"))
				retentionGatewayRequireP3(t, logs, f, reason)
				require.Equal(t, index, retentionGatewayFiles(t, f.indexDir()), "the tick must NOT heal the unavailable chat")
				require.Equal(t, canonical, retentionGatewayCanonical(t, f), "fresh canonical history survives unchanged")
				retentionGatewayRequireTickSummary(t, logs, reason, repeat)
			}
		})
	}
}
