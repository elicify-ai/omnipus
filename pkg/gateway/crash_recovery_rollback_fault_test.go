//go:build goolm && stdjson

package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/tests/testhooks"
)

func TestCrashRecovery_RetryRollbackStorageFailureNeedsBoundary(t *testing.T) {
	// DESIGN Decision 4: refusal alone is not confirmation that rollback saved.
	// The control proves this exact real Board Retry normally restores Failed.
	for _, failWrite := range []bool{false, true} {
		name := "ordinary_refusal_rolls_back"
		if failWrite {
			name = "rollback_atomic_write_fails"
		}
		t.Run(name, func(t *testing.T) {
			api, old, closed := crashTaskAfterBoot(t)
			api.taskExecutor = nil // Real degraded-gateway refusal, not a fake launcher.
			path := filepath.Join(api.taskStore.Dir(), old.ID+".json")
			backup := path + ".before-rollback-fault"
			var savedInProgress []byte
			var boundaryHits int
			faultInstalled := false
			restore := func() {
				if !faultInstalled {
					return
				}
				require.NoError(t, os.Remove(path)) // Remove our empty directory only.
				require.NoError(t, os.Rename(backup, path))
				faultInstalled = false
			}
			t.Cleanup(restore)
			if failWrite {
				testhooks.ObserveTaskAtomicWrite(t, func(target string, data []byte) {
					if target != path {
						return
					}
					var proposed task.Task
					require.NoError(t, json.Unmarshal(data, &proposed))
					if proposed.Status != old.Status {
						return // Initial In Progress save must really succeed first.
					}
					boundaryHits++
					require.Equal(t, 1, boundaryHits, "one rollback write, no hidden retry")
					var current task.Task
					raw, err := os.ReadFile(path)
					require.NoError(t, err)
					require.NoError(t, json.Unmarshal(raw, &current))
					require.Equal(t, task.StatusInProgress, current.Status, "observer runs after the winning transition was durably saved")
					savedInProgress = append([]byte(nil), raw...)
					require.NoError(t, os.Rename(path, backup))
					faultInstalled = true
					require.NoError(t, os.Mkdir(path, 0700))
					// Return normally. The REAL atomic rename must now fail because
					// its destination is a directory; no error result is fabricated.
				})
			}
			response := patchTask(t, api, old.ID, `{"status":"in_progress"}`)
			if failWrite {
				assert.Equal(t, 1, boundaryHits, "required taskBeforeAtomicWriteTestHook did not observe the real rollback write")
				assert.Equal(t, http.StatusInternalServerError, response.Code, "rollback storage failure is not an ordinary executor-unavailable response")
				var body gen.ErrorResponse
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
				assert.Contains(t, body.Error, "could not be reverted", "the user must know the saved state is unconfirmed")
				assert.Contains(t, body.Error, "storage", "report the failed persistence, not just the unavailable executor")
				restore()
			} else {
				assert.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
			}
			fresh, err := api.taskStore.Get(old.ID)
			require.NoError(t, err)
			if failWrite {
				assert.Equal(t, task.StatusInProgress, fresh.Status, "failed atomic rollback did not land a Failed state")
				raw, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, savedInProgress, raw, "the failed write leaves the exact last committed bytes untouched")
			} else {
				assert.Equal(t, task.StatusFailed, fresh.Status)
				assert.Empty(t, fresh.StartedAt, "ordinary refusal clears the timer")
			}
			runs, err := api.taskStore.ListRuns(old.ID)
			require.NoError(t, err)
			assert.Equal(t, []task.TaskRun{closed}, runs, "neither refusal path admits a phantom run")
		})
	}
}
