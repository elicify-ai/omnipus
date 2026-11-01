//go:build linux

package gateway

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// Watch the directory, not the old inode: atomic rename replaces the inode.
// IN_ALL_EVENTS retains temp create/close/move events between publications so
// Linux cannot coalesce two adjacent MOVED_TO jobs.json records into one.
func t10WatchPublishes(t *testing.T, path string) func() int {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	require.NoError(t, err)
	t.Cleanup(func() { unix.Close(fd) })
	_, err = unix.InotifyAddWatch(fd, filepath.Dir(path), unix.IN_ALL_EVENTS)
	require.NoError(t, err)
	return func() int {
		count := 0
		buf := make([]byte, 65536)
		for {
			n, e := unix.Read(fd, buf)
			if e == unix.EAGAIN {
				break
			}
			require.NoError(t, e)
			require.Positive(t, n)
			for offset := 0; offset < n; {
				mask := binary.NativeEndian.Uint32(buf[offset+4:])
				size := int(binary.NativeEndian.Uint32(buf[offset+12:]))
				require.Zero(t, mask&unix.IN_Q_OVERFLOW, "overflow invalidates the observation")
				name := strings.TrimRight(string(buf[offset+16:offset+16+size]), "\x00")
				if name == filepath.Base(path) && mask&unix.IN_MOVED_TO != 0 {
					count++
				}
				offset += 16 + size
			}
		}
		return count
	}
}

// Instrument control: the same atomic publisher used by the scheduler really
// produces TWO distinguishable publications. This rejects a final-state-only spy.
func TestT10_FirstWriteObserverDetectsRestamping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	count := t10WatchPublishes(t, path)
	require.NoError(t, fileutil.WriteFileAtomic(path, []byte(`{"kind":"agent_turn"}`), 0600))
	require.NoError(t, fileutil.WriteFileAtomic(path, []byte(`{"kind":"heartbeat"}`), 0600))
	assert.Equal(t, 2, count())
}

// Traces to: schedules-t10-spec-r6.md S-D.1, D-4.1–4.4, TDD 5.
// One observed publication + exact final content jointly prove the creating
// publication had the final kind. A wrong-kind write followed by repair is two
// publications and fails even if our read happens after the second rename.
func TestT10_ProducersPublishFinalKindExactlyOnce(t *testing.T) {
	for _, producer := range []string{"heartbeat", "interval", "one-shot", "task"} {
		t.Run(producer, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cron", "jobs.json")
			cs := cron.NewCronService(path)
			var create func()
			kind := "loop"
			switch producer {
			case "heartbeat":
				kind = "heartbeat"
				create = func() {
					require.NoError(t, ReconcileHeartbeatSchedules(cs, []workspace.Workspace{{ID: "t10-w", CoreTeam: []string{"mia"}, MemberConfigs: buildMemberConfigs("mia", true, 5, "T10 body")}}, neverWorker))
				}
			case "interval", "one-shot":
				// Both production constructors currently take a path. This existing binding
				// is type checked; GREEN must pass its shared cs to the constructor instead.
				ls := agent.NewLoopScheduler(path, nil)
				create = func() {
					var err error
					if producer == "interval" {
						_, err = ls.AddInterval("mia", "t10-session", 60000)
					} else {
						_, err = ls.AddOneShot("mia", "t10-session", 60000)
					}
					require.NoError(t, err)
				}
			case "task":
				kind = "task"
				store := task.New(filepath.Join(t.TempDir(), "tasks"))
				at := time.Now().Add(24 * time.Hour).UnixMilli()
				require.NoError(t, store.Create(&task.Task{ID: "t10-first-task", Title: "first write", AgentID: "mia", WorkspaceID: "t10-w", Status: task.StatusInbox, Trigger: &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}}))
				s := agent.NewTaskTriggerScheduler(path, store, nil)
				create = func() { require.NoError(t, s.Reconcile()) }
			}
			count := t10WatchPublishes(t, path)
			create()
			assert.Equal(t, 1, count(), "creating %s must publish once; a second kind-stamp write is forbidden", producer)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			var disk cron.CronStore
			require.NoError(t, json.Unmarshal(data, &disk))
			require.Len(t, disk.Jobs, 1, "no-op producer cannot pass")
			assert.Equal(t, kind, disk.Jobs[0].Payload.Kind)
			assert.NotEmpty(t, disk.Jobs[0].ID)
		})
	}
}

// Traces to: schedules-t10-spec-r6.md S-A.1 (fresh-path contrast to seeded no-access).
func TestT10_FreshBootCreatesNoAuxiliarySchedulerFiles(t *testing.T) {
	st := t10BootStage(t)
	t10SeedRecurringTask(t, st)
	for _, dir := range []string{"tasks_triggers", "loops"} {
		_, err := os.Stat(filepath.Join(st.homePath, dir, "jobs.json"))
		require.True(t, os.IsNotExist(err))
	}
	_, stop, err := st.startSchedulers()
	require.NoError(t, err)
	require.False(t, stop)
	for _, dir := range []string{"tasks_triggers", "loops"} {
		_, err := os.Stat(filepath.Join(st.homePath, dir, "jobs.json"))
		assert.True(t, os.IsNotExist(err), "fresh boot must not create %s/jobs.json", dir)
	}
	shared, err := os.ReadFile(filepath.Join(st.homePath, "cron", "jobs.json"))
	require.NoError(t, err)
	var disk cron.CronStore
	require.NoError(t, json.Unmarshal(shared, &disk))
	taskJobs := 0
	for _, j := range disk.Jobs {
		if j.Payload.Kind == "task" {
			taskJobs++
		}
	}
	assert.Equal(t, 1, taskJobs, "positive control: boot really reconciled into shared persistence")
}
