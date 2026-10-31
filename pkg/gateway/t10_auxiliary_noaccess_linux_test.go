//go:build linux

package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// Traces to: schedules-t10-spec-r6.md lines 1087–1097; TDD 17, D-10.2/3.
// IN_OPEN observes reads that a byte-identity-only oracle would miss. No polling,
// permissions shortcut (the pod runs as root), migration, or fake filesystem.
func TestT10_AuxiliarySchedulerFilesAreNeverOpened(t *testing.T) {
	for _, dirname := range []string{"tasks_triggers", "loops"} {
		t.Run(dirname, func(t *testing.T) {
			st := t10BootStage(t)
			path := filepath.Join(st.homePath, dirname, "jobs.json")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
			seed, err := json.Marshal(cron.CronStore{Version: 1, Jobs: []cron.CronJob{{ID: "untouchable-" + dirname, Name: "auxiliary sentinel", Enabled: true, Payload: cron.CronPayload{Kind: "unknown-residue"}}}})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, seed, 0600))
			fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
			require.NoError(t, err)
			defer unix.Close(fd)
			_, err = unix.InotifyAddWatch(fd, path, unix.IN_OPEN|unix.IN_MODIFY|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF|unix.IN_ATTRIB)
			require.NoError(t, err)
			_, stop, bootErr := st.startSchedulers()
			require.NoError(t, bootErr)
			require.False(t, stop)
			buf := make([]byte, 8192)
			n, readErr := unix.Read(fd, buf)
			if readErr != unix.EAGAIN {
				require.NoError(t, readErr)
				assert.Zero(t, n, "boot accessed or replaced %s; byte equality alone would not detect a read", path)
			}
			// Observation is complete. Do not remove a watch that a forbidden
			// atomic replacement may already have removed (IN_IGNORED). The test's
			// own read below is after the captured event count and cannot taint it.
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, seed, after)
		})
	}
}
