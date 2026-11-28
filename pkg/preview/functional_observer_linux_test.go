//go:build linux

package preview

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

type functionalSocket struct {
	Inode string
	Path  string
	Mode  os.FileMode
}

type functionalObservation struct {
	Sockets map[string]functionalSocket
	Starts  map[int]string
	Created map[string]int
	Pending map[int]error
	Errors  []error
	Samples int
}

func functionalUnixTable() (map[string]functionalSocket, error) {
	raw, err := os.ReadFile("/proc/net/unix")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 || !strings.Contains(lines[0], "Inode") {
		return nil, errors.New("Unix socket table has no Inode header")
	}
	table := map[string]functionalSocket{}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			return nil, fmt.Errorf("malformed Unix socket row: %q", line)
		}
		path := ""
		if len(fields) > 7 {
			path = strings.Join(fields[7:], " ")
		}
		table[fields[6]] = functionalSocket{Inode: fields[6], Path: path}
	}
	return table, nil
}

func functionalProcessSockets(pid int, table map[string]functionalSocket) ([]functionalSocket, error) {
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var sockets []functionalSocket
	for _, entry := range entries {
		link, err := os.Readlink(filepath.Join(dir, entry.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		inode, ok := strings.CutPrefix(link, "socket:[")
		if !ok {
			continue
		}
		inode = strings.TrimSuffix(inode, "]")
		if socket, found := table[inode]; found {
			if socket.Path != "" && !strings.HasPrefix(socket.Path, "@") {
				info, statErr := os.Lstat(socket.Path)
				if statErr == nil {
					socket.Mode = info.Mode()
				} else if !errors.Is(statErr, os.ErrNotExist) {
					return nil, statErr
				}
			}
			sockets = append(sockets, socket)
		}
	}
	return sockets, nil
}

func functionalOfficePIDs(root string) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if functionalOfficeProcess(filepath.Join("/proc", entry.Name()), root) {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// functionalOfficeProcess decides office membership by properties readable
// across the whole process life. The exe link alone misses real office
// processes (D10): it is unreadable during an exec transition and points at
// the interpreter while the launcher script phase runs, and dropping such a
// pid loses a real start-time observation for a correct conversion. A LIVE
// process's command line (this test runs as root) always names the office
// path. Kernel threads and zombies have no command line and are not office
// processes; they are excluded exactly as before.
func functionalOfficeProcess(procDir, root string) bool {
	if link, err := os.Readlink(filepath.Join(procDir, "exe")); err == nil {
		if strings.HasPrefix(strings.TrimSuffix(link, " (deleted)"), root+"/") {
			return true
		}
	}
	raw, err := os.ReadFile(filepath.Join(procDir, "cmdline"))
	if err != nil || len(raw) == 0 {
		return false
	}
	return bytes.Contains(raw, []byte(root+"/"))
}

func functionalArmTmpWatch(t *testing.T) int {
	t.Helper()
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	require.NoError(t, err, "T-115: a missing inotify instrument is fatal, not a skip")
	_, err = unix.InotifyAddWatch(fd, "/tmp", unix.IN_CREATE|unix.IN_DELETE|unix.IN_MOVED_TO|unix.IN_MOVED_FROM)
	if err != nil {
		_ = unix.Close(fd)
		t.Fatalf("T-115: arm /tmp watch before the call: %v", err)
	}
	return fd
}

func functionalDrainWatch(fd int, observation *functionalObservation) {
	var buffer [64 << 10]byte
	for {
		n, err := unix.Read(fd, buffer[:])
		if errors.Is(err, unix.EAGAIN) {
			return
		}
		if err != nil {
			observation.Errors = append(observation.Errors, err)
			return
		}
		if n == 0 {
			observation.Errors = append(observation.Errors, errors.New("inotify returned unexpected EOF"))
			return
		}
		for offset := 0; offset < n; {
			if offset+unix.SizeofInotifyEvent > n {
				observation.Errors = append(observation.Errors, errors.New("short inotify event"))
				return
			}
			event := (*unix.InotifyEvent)(unsafe.Pointer(&buffer[offset]))
			end := offset + unix.SizeofInotifyEvent + int(event.Len)
			if end > n {
				observation.Errors = append(observation.Errors, errors.New("truncated inotify name"))
				return
			}
			if event.Mask&(unix.IN_Q_OVERFLOW|unix.IN_IGNORED|unix.IN_UNMOUNT) != 0 {
				observation.Errors = append(observation.Errors, errors.New("T-115: inotify overflow or watch lost; observation is invalid"))
			}
			name := strings.TrimRight(string(buffer[offset+unix.SizeofInotifyEvent:end]), "\x00")
			if event.Mask&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0 {
				observation.Created[filepath.Join("/tmp", name)]++
			}
			offset = end
		}
	}
}

// T-115: the watch is armed synchronously BEFORE Service.Preview. The parent
// records real PID+start-time identities; the confined recorder never reads
// /proc or owner.json. Missed samples/events fail instead of making a green.
func functionalObserve(t *testing.T, root string) func() functionalObservation {
	t.Helper()
	fd := functionalArmTmpWatch(t)
	stop := make(chan struct{})
	done := make(chan functionalObservation, 1)
	go func() {
		defer func() { _ = unix.Close(fd) }()
		observation := functionalObservation{Sockets: map[string]functionalSocket{}, Starts: map[int]string{}, Created: map[string]int{}, Pending: map[int]error{}}
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			functionalDrainWatch(fd, &observation)
			functionalSampleOffice(root, &observation)
			select {
			case <-stop:
				functionalDrainWatch(fd, &observation)
				for pid, pending := range observation.Pending {
					if !functionalProcessExited(pid) {
						observation.Errors = append(observation.Errors, pending)
					}
				}
				done <- observation
				return
			case <-tick.C:
			}
		}
	}()
	var once sync.Once
	var result functionalObservation
	return func() functionalObservation {
		once.Do(func() { close(stop); result = <-done })
		return result
	}
}

func functionalSampleOffice(root string, observation *functionalObservation) {
	table, err := functionalUnixTable()
	if err != nil {
		observation.Errors = append(observation.Errors, err)
		return
	}
	pids, err := functionalOfficePIDs(root)
	if err != nil {
		observation.Errors = append(observation.Errors, err)
		return
	}
	for _, pid := range pids {
		start, startErr := processStart(pid)
		if startErr == nil {
			if old, found := observation.Starts[pid]; found && old != start {
				observation.Errors = append(observation.Errors, fmt.Errorf("instrument: PID %d recycled during observation (%s -> %s)", pid, old, start))
			} else {
				observation.Starts[pid] = start
			}
		} else if errors.Is(startErr, os.ErrNotExist) || errors.Is(startErr, unix.ESRCH) {
			// D10 family: the process exited between its identification and
			// the stat read; /proc answers ENOENT or ESRCH for a task lost
			// mid-read. Its lifecycle completed inside the observation —
			// not an observer failure. Nothing is recorded, and the
			// launch-side assertion settles such a pid with the same
			// strength (gone passes, alive fails).
		} else {
			observation.Errors = append(observation.Errors, startErr)
		}
		sockets, socketErr := functionalProcessSockets(pid, table)
		if errors.Is(socketErr, os.ErrNotExist) {
			continue
		}
		if socketErr != nil {
			// procfs access can change during exec/exit. Retain the error
			// and settle it after return: ONLY a verified exited task can
			// explain it. A still-live unreadable task remains fatal. This
			// never substitutes for required live socket/PID observations.
			if errors.Is(socketErr, os.ErrPermission) {
				observation.Pending[pid] = socketErr
				continue
			}
			observation.Errors = append(observation.Errors, socketErr)
		}
		for _, socket := range sockets {
			previous := observation.Sockets[socket.Inode]
			socket.Mode |= previous.Mode
			observation.Sockets[socket.Inode] = socket
		}
	}
	observation.Samples++
}

func functionalProcessExited(pid int) bool {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	end := bytes.LastIndexByte(raw, ')')
	if end < 0 {
		return false
	}
	fields := strings.Fields(string(raw[end+1:]))
	return len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X")
}

// functionalAssertProcessGone proves the launched office process is gone
// after the call. The start-time observation normally comes from the /proc
// poller; a pid can end up unpolled without any defect (D10: the process
// lived and died entirely inside one 1 ms poll gap). Such a pid is settled
// from its LAUNCH-SIDE START IDENTITY — the kernel start time the child
// fixture recorded into the receipt, stable across execve: gone passes; a
// pid that is STILL ALIVE fails, whether it is the same task (identical
// start) or unverifiable (no recorded identity, unreadable start). A
// changed executable or command line proves nothing and never passes a
// live task (CHECK F1).
func functionalAssertProcessGone(t *testing.T, pid int, launchStart string, starts map[int]string) {
	t.Helper()
	start, observed := starts[pid]
	if !observed {
		functionalAssertLaunchPIDGone(t, pid, launchStart)
		return
	}
	current, err := processStart(pid)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	require.NoError(t, err, "T-115: cannot verify pid identity")
	assert.NotEqual(t, start, current, "T-115: original pid %d with start %s is still alive", pid, start)
}

// functionalAssertLaunchPIDGone settles an unpolled launch-side pid. The
// pid is real: the child wrote it into the receipt through the descriptor
// the test held open before the start. The task must be gone; a live task
// fails unless the recorded launch identity proves the original one exited
// and the pid was recycled to a different task.
func functionalAssertLaunchPIDGone(t *testing.T, pid int, launchStart string) {
	t.Helper()
	if functionalProcessExited(pid) {
		t.Logf("T-115: pid %d was never polled; its task is gone (lived inside one poll gap) — launch-side identity verified against the live /proc table", pid)
		return
	}
	current, err := processStart(pid)
	if err != nil {
		t.Fatalf("T-115: launch-side pid %d is still alive and its start identity is unreadable (%v); refusing to pass an unverifiable task", pid, err)
	}
	if launchStart == "" {
		t.Fatalf("T-115: launch-side pid %d is still alive and carries no recorded start identity (start %s); a live task is not proven recycled — failing as unverifiable", pid, current)
	}
	require.NotEqual(t, launchStart, current,
		"T-115: the launch-side pid %d with start %s is STILL ALIVE after the call — same task, identity unchanged", pid, launchStart)
	t.Logf("T-115: pid %d was never polled; the original task (start %s) exited and the pid now belongs to a different task (start %s)", pid, launchStart, current)
}

func functionalSocketInstrumentControl(t *testing.T) {
	t.Helper()
	id, err := randomID(4)
	require.NoError(t, err)
	path := "/tmp/office-functional-control-" + id
	fd := functionalArmTmpWatch(t)
	defer func() { _ = unix.Close(fd) }()
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	table, err := functionalUnixTable()
	require.NoError(t, err)
	sockets, err := functionalProcessSockets(os.Getpid(), table)
	require.NoError(t, err)
	seen := make([]string, 0, len(sockets))
	for _, socket := range sockets {
		seen = append(seen, socket.Path)
	}
	require.Contains(t, seen, path, "instrument: see a live process-owned socket before trusting empty post-call scans")
	observation := functionalObservation{Created: map[string]int{}}
	functionalDrainWatch(fd, &observation)
	require.Empty(t, observation.Errors)
	require.Equal(t, 1, observation.Created[path], "instrument: inotify must see the same socket being created")
	t.Log("T-115 instrument control: live process-owned Unix socket and its inotify creation both observed")
}
