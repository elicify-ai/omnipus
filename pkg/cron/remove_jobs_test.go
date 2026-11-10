package cron

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The boot-duplicate repair must either persist the requested deletion or
// return an error without changing the live jobs. It must never remove another
// job's state or history. This is the checked persistence boundary used by the
// task-trigger restart regression; faults here must prevent a false cleanup.
func TestCronRemoveJobs(t *testing.T) {
	fixture := func(t *testing.T) (*CronService, string, []CronJob) {
		t.Helper()
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("OMNIPUS_HOME", home)
		path := filepath.Join(home, "cron", "jobs.json")
		cs := NewCronService(path)
		due := time.Now().Add(48 * time.Hour).UnixMilli()
		for _, name := range []string{"original", "duplicate", "unrelated"} {
			_, err := cs.AddJobFull(JobSpec{
				Name: name, Message: name, AgentID: "agent-a",
				Schedule: CronSchedule{Kind: "at", AtMS: &due},
			})
			require.NoError(t, err)
		}
		return cs, path, append([]CronJob(nil), cs.ListJobs(true)...)
	}

	t.Run("only_requested_jobs_removed_and_survivors_preserved", func(t *testing.T) {
		cs, path, before := fixture(t)
		require.NoError(t, cs.RemoveJobs([]string{before[1].ID, before[1].ID, "absent-id"}))
		want := []CronJob{before[0], before[2]}
		assert.Equal(t, want, cs.ListJobs(true))
		loaded := NewCronService(path)
		require.NoError(t, loaded.Load())
		assert.Equal(t, want, loaded.ListJobs(true), "the removal must actually be durable")
		once, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, cs.RemoveJobs([]string{before[1].ID}))
		twice, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, once, twice, "repeating the deletion is a no-op")
	})

	t.Run("failed_write_returns_cause_and_rolls_back_memory", func(t *testing.T) {
		cs, path, before := fixture(t)
		diskBefore, err := os.ReadFile(path)
		require.NoError(t, err)
		blocker := filepath.Join(t.TempDir(), "not-a-directory")
		require.NoError(t, os.WriteFile(blocker, []byte("block"), 0o600))
		cs.storePath = filepath.Join(blocker, "jobs.json") // deterministic on all supported OSes, even as root
		err = cs.RemoveJobs([]string{before[1].ID})
		require.Error(t, err)
		assert.True(t, strings.HasPrefix(err.Error(), "cron: remove jobs: failed to create directory:"), err.Error())
		var pathErr *os.PathError
		require.True(t, errors.As(err, &pathErr), "preserve the filesystem error for the caller")
		assert.Equal(t, "mkdir", pathErr.Op)
		assert.Equal(t, blocker, pathErr.Path)
		assert.Equal(t, before, cs.ListJobs(true), "a failed cleanup must not discard live jobs")
		diskAfter, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, diskBefore, diskAfter, "the original store remains intact")
	})

	t.Run("empty_or_unknown_set_does_not_try_to_write", func(t *testing.T) {
		cs, _, before := fixture(t)
		blocker := filepath.Join(t.TempDir(), "not-a-directory")
		require.NoError(t, os.WriteFile(blocker, []byte("block"), 0o600))
		cs.storePath = filepath.Join(blocker, "jobs.json")
		require.NoError(t, cs.RemoveJobs(nil))
		require.NoError(t, cs.RemoveJobs([]string{"absent-id"}))
		assert.Equal(t, before, cs.ListJobs(true))
	})
}
