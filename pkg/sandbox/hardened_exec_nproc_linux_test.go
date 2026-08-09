package sandbox

import (
	"os"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"testing"
)

// ReadCurrentUserNProcForTest exposes the production per-UID task counter to
// the external sandbox_test package. Red-team tests MUST measure exactly the
// quantity production enforces against — keeping a private copy is how the
// fork-limit outage stayed green in CI while production was broken.
func ReadCurrentUserNProcForTest() uint64 { return readCurrentUserNProc() }

// selfTaskCount returns the number of threads in THIS process by reading
// /proc/self/task. This is ground truth for what the kernel counts toward
// the per-UID RLIMIT_NPROC for our own process.
func selfTaskCount(t *testing.T) uint64 {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/task")
	if err != nil {
		t.Skipf("cannot read /proc/self/task (%v); not a standard Linux procfs", err)
	}
	return uint64(len(entries))
}

// TestReadCurrentUserNProc_CountsThreadsNotProcesses is the regression test
// for the fork-limit outage.
//
// The bug: readCurrentUserNProc counted /proc/<pid> entries, which are
// thread-group LEADERS (i.e. processes). The kernel enforces RLIMIT_NPROC
// against the per-UID task_struct count, which includes every thread. On a
// gateway holding ~274 threads across ~99 processes the computed cap
// (99+128=227) landed BELOW the live task count, so every fork() by every
// sandboxed child returned EAGAIN — a total, silent outage of the `bash`
// tool with 3 GB of free memory and zero load.
//
// The invariant that catches it: this process alone owns selfTaskCount()
// threads, and we are one of the UID's processes, so a task-counting
// implementation MUST report at least that many. A process-counting
// implementation reports 1 for this process and therefore fails here as soon
// as the Go runtime holds more than a handful of threads (it always does).
func TestReadCurrentUserNProc_CountsThreadsNotProcesses(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	self := selfTaskCount(t)
	got := readCurrentUserNProc()
	if got == 0 {
		t.Skip("/proc unreadable in this environment")
	}
	if got < self {
		t.Fatalf("readCurrentUserNProc()=%d is below this process's own thread count %d — "+
			"it is counting processes, not tasks. RLIMIT_NPROC is enforced against tasks, so a "+
			"cap derived from this value lands below live usage and EAGAINs every child fork()", got, self)
	}
}

// TestReadCurrentUserNProc_TracksNewThreads asserts the counter actually
// responds to thread creation — a stronger form of the invariant above that
// would also catch a hard-coded or cached value.
func TestReadCurrentUserNProc_TracksNewThreads(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	before := readCurrentUserNProc()
	if before == 0 {
		t.Skip("/proc unreadable in this environment")
	}

	const extra = 24
	var wg sync.WaitGroup
	release := make(chan struct{})
	for i := 0; i < extra; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Lock to a dedicated OS thread so the runtime must materialise
			// a real task_struct, then hold it until released.
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			<-release
		}()
	}
	// Give the runtime a moment to actually create the threads.
	for i := 0; i < 100; i++ {
		if readCurrentUserNProc() >= before+extra/2 {
			break
		}
		runtime.Gosched()
	}
	during := readCurrentUserNProc()
	close(release)
	wg.Wait()

	if during <= before {
		t.Fatalf("counter did not rise after locking %d OS threads (before=%d during=%d); "+
			"a task-counting implementation must observe new threads", extra, before, during)
	}
}

// TestChildNProcCap_ExceedsCurrentTaskCount asserts the derived cap leaves
// real headroom above live usage. If this ever inverts, every sandboxed
// fork() fails — which is precisely the production outage this guards.
func TestChildNProcCap_ExceedsCurrentTaskCount(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	baseline := readCurrentUserNProc()
	if baseline == 0 {
		t.Skip("/proc unreadable in this environment")
	}
	nprocCap := baseline + childNProcSlack
	live := readCurrentUserNProc()
	if nprocCap <= live {
		t.Fatalf("computed RLIMIT_NPROC cap %d is not above live task count %d — "+
			"children would fail every fork() with EAGAIN", nprocCap, live)
	}
}

// TestReadCurrentUserNProc_MatchesProcfsWalk cross-checks the production
// counter against an independent walk of /proc, so the two can never drift
// the way production and the red-team test once did.
func TestReadCurrentUserNProc_MatchesProcfsWalk(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	got := readCurrentUserNProc()
	if got == 0 {
		t.Skip("/proc unreadable in this environment")
	}

	uid := os.Getuid()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Skipf("read /proc: %v", err)
	}
	var want uint64
	for _, e := range entries {
		name := e.Name()
		if _, convErr := strconv.Atoi(name); convErr != nil {
			continue
		}
		info, statErr := os.Stat("/proc/" + name)
		if statErr != nil {
			continue
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(st.Uid) != uid {
			continue
		}
		tasks, terr := os.ReadDir("/proc/" + name + "/task")
		if terr != nil {
			want++
			continue
		}
		want += uint64(len(tasks))
	}

	// /proc is a moving target: processes and threads come and go between the
	// two walks. Assert the same order of magnitude rather than equality.
	if want == 0 {
		t.Skip("independent walk found nothing; environment too restricted")
	}
	lo, hi := want/2, want*2
	if got < lo || got > hi {
		t.Fatalf("readCurrentUserNProc()=%d diverges from an independent task walk (%d); "+
			"expected within [%d,%d]", got, want, lo, hi)
	}
}
