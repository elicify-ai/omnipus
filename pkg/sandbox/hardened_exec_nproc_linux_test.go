//go:build linux

// RLIMIT_NPROC coverage for hardened-exec children (v0.2 #155 item 5).
//
// Two distinct failure modes are guarded here, and they pull in opposite
// directions — which is why both need tests:
//
//	OUTAGE   — the cap lands at or below the live per-UID TASK count, so every
//	           fork() by the child returns EAGAIN ("sh: Cannot fork") and the
//	           `bash` tool is 100% dead. This shipped once, caused by
//	           readCurrentUserNProc counting PROCESSES while the kernel
//	           enforces against TASKS.
//
//	ESCAPE   — the cap is present but removable: leaving the child's HARD
//	           RLIMIT_NPROC at the inherited value lets an unprivileged child
//	           run `ulimit -u unlimited` and fork freely. A soft-only limit
//	           contains a COOPERATING child; this control exists for the
//	           adversarial one.
//
// Root note (matters for CI): the kernel skips the RLIMIT_NPROC check entirely
// for uid 0 — copy_process() only enforces it when
// `p->real_cred->user != INIT_USER && !capable(CAP_SYS_RESOURCE)`. So any test
// whose signal is "the child's fork() failed" is blind as root and must skip.
// The tests that assert on the APPLIED LIMIT VALUES (read back with prlimit)
// are uid-independent and carry the regression load on a root CI worker.

package sandbox

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// ReadCurrentUserNProcForTest exposes the production per-UID task counter to
// the external sandbox_test package. Red-team tests MUST measure exactly the
// quantity production enforces against — keeping a private copy is how the
// fork-limit outage stayed green in CI while production was broken.
func ReadCurrentUserNProcForTest() uint64 { return readCurrentUserNProc() }

// nprocTestThreads is the number of extra OS threads a test parks on itself
// before exercising the cap derivation.
//
// It must exceed childNProcSlack (128). That is the whole point: with N extra
// threads held by this process alone, the UID's live TASK count is at least
// (process count + N - 1), so a PROCESS-counting implementation derives a cap
// of (process count + 128) which is provably below live usage. The gap becomes
// arithmetic rather than environmental — the test discriminates on a quiet
// laptop and on a busy CI runner alike.
const nprocTestThreads = 192

// nprocChildThreads is how many OS threads the re-exec'd helper child parks,
// for the "counts OTHER processes' threads" invariant.
const nprocChildThreads = 64

// nprocChildEnv marks the re-exec'd helper child.
const nprocChildEnv = "OMNIPUS_NPROC_THREAD_CHILD"

// nprocRaceSlack absorbs /proc churn between two walks taken at slightly
// different instants. It is deliberately tiny relative to nprocTestThreads
// (192) so it cannot rescue a process-counting implementation.
const nprocRaceSlack uint64 = 16

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

// countUIDProcesses counts the /proc/<pid> entries owned by our UID — i.e.
// thread-group leaders, i.e. PROCESSES.
//
// This is deliberately the WRONG quantity for RLIMIT_NPROC. It is used only as
// a term in a lower bound on the RIGHT quantity: every UID-owned process
// contributes at least one task, so
//
//	tasks >= processes + (this process's own threads) - 1
//
// A process-counting implementation returns exactly `processes`, which cannot
// satisfy that bound once this process holds more than one thread. Note this
// is not a mirror of the production walk — it is a mirror of the BUG, used to
// prove the bug is absent.
func countUIDProcesses(t *testing.T) uint64 {
	t.Helper()
	uid := os.Getuid()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Skipf("read /proc: %v", err)
	}
	var n uint64
	for _, e := range entries {
		if _, convErr := strconv.Atoi(e.Name()); convErr != nil {
			continue
		}
		var st unix.Stat_t
		if statErr := unix.Stat("/proc/"+e.Name(), &st); statErr != nil {
			continue
		}
		if uint64(st.Uid) != uint64(uid) {
			continue
		}
		n++
	}
	return n
}

// liveTaskLowerBound returns a lower bound on the UID's live task count that is
// derived from the kernel rather than from production's own walk.
func liveTaskLowerBound(t *testing.T) uint64 {
	t.Helper()
	procs := countUIDProcesses(t)
	if procs == 0 {
		t.Skip("no UID-owned /proc entries visible; environment too restricted")
	}
	return procs + selfTaskCount(t) - 1
}

// requireNProcHeadroom skips the test unless the UID's RLIMIT_NPROC soft limit
// leaves room for `want` additional tasks plus two slack allowances.
//
// Two reasons this guard exists:
//   - Go turns a failed thread creation into a FATAL runtime throw, not an
//     error. Parking 192 threads on a UID that is near its nproc limit would
//     kill the whole test binary.
//   - With this much headroom, production's downward clamp against the
//     inherited limits provably does not fire, so tests that assert on the
//     derived cap are measuring the derivation and not the clamp.
func requireNProcHeadroom(t *testing.T, want uint64) {
	t.Helper()
	var lim unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NPROC, &lim); err != nil {
		t.Skipf("getrlimit RLIMIT_NPROC: %v", err)
	}
	if lim.Cur == unix.RLIM_INFINITY {
		return
	}
	live := readCurrentUserNProc()
	if live == 0 {
		t.Skip("/proc unreadable in this environment")
	}
	need := live + want + 2*childNProcSlack
	if lim.Cur < need {
		t.Skipf("insufficient RLIMIT_NPROC headroom: soft=%d live=%d need>=%d "+
			"(creating %d test threads could fatally abort the test binary)",
			lim.Cur, live, need, want)
	}
}

// holdLockedOSThreads parks n goroutines on dedicated OS threads for the
// remainder of the test, so the kernel really materialises n task_structs.
// It blocks until /proc/self/task reflects them.
func holdLockedOSThreads(t *testing.T, n int) {
	t.Helper()
	requireNProcHeadroom(t, uint64(n))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			<-stop
		}()
	}
	var once sync.Once
	release := func() { once.Do(func() { close(stop); wg.Wait() }) }
	t.Cleanup(release)

	deadline := time.Now().Add(30 * time.Second)
	for {
		if selfTaskCount(t) >= uint64(n) {
			return
		}
		if time.Now().After(deadline) {
			got := selfTaskCount(t)
			t.Skipf("runtime materialised only %d OS threads within 30s (wanted >= %d)", got, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// gatedChild is a started `sh` child parked on a read from stdin, so a test can
// apply post-start hardening BEFORE the child executes anything. Production has
// an inherent race here (Start, then prlimit); the gate removes it from the
// test so the assertion is about the limit, not about timing.
type gatedChild struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *bytes.Buffer
	waited bool
}

// startGatedChild spawns `sh -c "read <gate>; <script>"` through the production
// pre-start hardening path and returns once it is running and blocked on stdin.
func startGatedChild(t *testing.T, script string) *gatedChild {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("sh not in PATH: %v", err)
	}

	// applyPlatformHardening sets Pdeathsig, which the kernel delivers when the
	// CREATING THREAD exits — not when the process exits. Go can retire an M at
	// any time, so pin this goroutine to its thread for the test's lifetime or
	// the child may be SIGTERMed out from under the assertions.
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)

	c := &gatedChild{stderr: &bytes.Buffer{}}
	c.cmd = exec.Command("sh", "-c", "read __gate; "+script)
	c.cmd.Stderr = c.stderr

	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	c.stdin = stdin

	if err := applyPlatformHardening(c.cmd, Limits{}); err != nil {
		t.Fatalf("applyPlatformHardening: %v", err)
	}
	if err := c.cmd.Start(); err != nil {
		t.Fatalf("start gated child: %v", err)
	}
	t.Cleanup(func() {
		if c.waited {
			return
		}
		_ = c.stdin.Close()
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	})
	return c
}

func (c *gatedChild) pid() int { return c.cmd.Process.Pid }

// release lets the gated child proceed past its stdin read.
func (c *gatedChild) release(t *testing.T) {
	t.Helper()
	if _, err := io.WriteString(c.stdin, "go\n"); err != nil {
		t.Fatalf("release gated child: %v", err)
	}
	_ = c.stdin.Close()
}

// wait reaps the child exactly once, so the cleanup hook does not Kill a
// recycled PID.
func (c *gatedChild) wait() error {
	c.waited = true
	return c.cmd.Wait()
}

// childNProcLimit reads a child's RLIMIT_NPROC back from the kernel.
func childNProcLimit(t *testing.T, pid int) unix.Rlimit {
	t.Helper()
	var got unix.Rlimit
	if err := unix.Prlimit(pid, unix.RLIMIT_NPROC, nil, &got); err != nil {
		t.Fatalf("prlimit read-back on pid %d: %v", pid, err)
	}
	return got
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
// The earlier version of this test asserted only
// `readCurrentUserNProc() >= selfTaskCount()`. That is satisfiable by a
// process-counter whenever the UID owns more processes than this binary has
// threads — which is the normal case for uid 0 on a CI runner, so the test
// could false-green exactly where it runs.
//
// The invariant used now is arithmetic, not environmental. Every UID-owned
// process contributes at least one task and this process contributes
// selfTaskCount(), so any task counter must report at least
// `processes + selfTaskCount() - 1`. We first park nprocTestThreads (192)
// threads on ourselves, which makes that bound exceed `processes + 128` — so a
// process counter fails no matter how many processes the UID owns.
func TestReadCurrentUserNProc_CountsThreadsNotProcesses(t *testing.T) {
	holdLockedOSThreads(t, nprocTestThreads)

	got := readCurrentUserNProc()
	if got == 0 {
		t.Skip("/proc unreadable in this environment")
	}
	procs := countUIDProcesses(t)
	if procs == 0 {
		t.Skip("no UID-owned /proc entries visible; environment too restricted")
	}
	self := selfTaskCount(t)
	want := procs + self - 1

	if got+nprocRaceSlack < want {
		t.Fatalf("readCurrentUserNProc()=%d is below the kernel-derived lower bound on live tasks "+
			"(%d UID processes + %d threads in this process - 1 = %d, race slack %d) — "+
			"it is counting processes, not tasks. RLIMIT_NPROC is enforced against tasks, so a cap "+
			"derived from this value lands below live usage and EAGAINs every child fork()",
			got, procs, self, want, nprocRaceSlack)
	}
}

// TestReadCurrentUserNProc_TracksNewThreads asserts the counter actually
// responds to thread creation — a stronger form of the invariant above that
// would also catch a hard-coded or cached value.
func TestReadCurrentUserNProc_TracksNewThreads(t *testing.T) {
	before := readCurrentUserNProc()
	if before == 0 {
		t.Skip("/proc unreadable in this environment")
	}

	const extra = 24
	requireNProcHeadroom(t, extra)
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

// TestReadCurrentUserNProc_CountsOtherProcessesThreads closes the one invariant
// the deleted mirror-implementation test (TestReadCurrentUserNProc_MatchesProcfsWalk)
// covered that nothing else did: the counter must sum the threads of OTHER
// UID-owned processes, not just its own.
//
// Instead of re-walking /proc the way production does — a mirror that has to be
// edited in lockstep and is blind to any shared misconception — this re-execs
// the test binary as a helper child that parks nprocChildThreads OS threads,
// then takes the child's ACTUAL thread count from the kernel
// (/proc/<childpid>/task) and requires the counter to have risen by
// approximately that much. An implementation that counts the child as a single
// process, or that only counts its own threads, rises by 1 and fails.
func TestReadCurrentUserNProc_CountsOtherProcessesThreads(t *testing.T) {
	if os.Getenv(nprocChildEnv) == "1" {
		runNProcThreadChild()
		return // unreachable
	}
	requireNProcHeadroom(t, 2*nprocChildThreads)

	before := readCurrentUserNProc()
	if before == 0 {
		t.Skip("/proc unreadable in this environment")
	}

	//nolint:gosec // intentional test-binary self-exec, same pattern as backend_linux_subprocess_test.go
	child := exec.Command(os.Args[0],
		"-test.run=^TestReadCurrentUserNProc_CountsOtherProcessesThreads$",
		"-test.count=1",
	)
	child.Env = append(os.Environ(), nprocChildEnv+"=1")
	if err := child.Start(); err != nil {
		t.Fatalf("start helper child: %v", err)
	}
	defer func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	}()

	taskDir := "/proc/" + strconv.Itoa(child.Process.Pid) + "/task"
	var childTasks uint64
	deadline := time.Now().Add(30 * time.Second)
	for {
		entries, err := os.ReadDir(taskDir)
		if err == nil {
			childTasks = uint64(len(entries))
			if childTasks >= nprocChildThreads {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Skipf("helper child materialised only %d threads within 30s (wanted >= %d)",
				childTasks, nprocChildThreads)
		}
		time.Sleep(5 * time.Millisecond)
	}

	during := readCurrentUserNProc()
	// The child contributes childTasks tasks but only ONE /proc entry, so the
	// counter must rise by ~childTasks. Allow race slack for unrelated churn.
	want := before + childTasks - 1
	if during+nprocRaceSlack < want {
		t.Fatalf("readCurrentUserNProc rose from %d to %d when a child process holding %d "+
			"kernel-visible threads appeared; a task counter must observe the child's THREADS, "+
			"not just its PID (wanted >= %d, race slack %d)",
			before, during, childTasks, want, nprocRaceSlack)
	}
}

// runNProcThreadChild is the helper-child half of
// TestReadCurrentUserNProc_CountsOtherProcessesThreads: park a known number of
// OS threads and idle until the parent kills us. It never returns.
func runNProcThreadChild() {
	stop := make(chan struct{})
	for i := 0; i < nprocChildThreads; i++ {
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			<-stop
		}()
	}
	// Sleep rather than block forever: a process whose every goroutine is
	// parked on a channel trips Go's deadlock detector and dies. A pending
	// timer keeps the runtime happy. The parent kills us long before this
	// elapses; the exit is a backstop against a leaked helper.
	time.Sleep(5 * time.Minute)
	close(stop)
	os.Exit(0)
}

// TestApplyPostStartHardening_AppliedCapExceedsLiveTasks is the root-safe
// replacement for the deleted TestChildNProcCap_ExceedsCurrentTaskCount.
//
// The deleted test computed `baseline := readCurrentUserNProc()`,
// `cap := baseline + childNProcSlack`, `live := readCurrentUserNProc()`, and
// asserted `cap > live`. Both sides came from the same function, so it reduced
// to `P + 128 > P` and passed identically on the broken code. It also never
// touched applyPostStartHardening, where the cap is actually derived, clamped
// and applied.
//
// This test drives the real production function against a real child, reads the
// applied limit back out of the kernel, and compares it to a lower bound on
// live tasks derived independently (UID process count + this process's own
// thread count - 1). With nprocTestThreads (192) parked on ourselves, a
// process-counting derivation yields `processes + 128`, which is below that
// bound by construction — so this fails on the pre-fix code on any host, root
// or not.
func TestApplyPostStartHardening_AppliedCapExceedsLiveTasks(t *testing.T) {
	holdLockedOSThreads(t, nprocTestThreads)

	c := startGatedChild(t, "true")
	if err := applyPostStartHardening(c.cmd, Limits{}); err != nil {
		t.Fatalf("applyPostStartHardening: %v", err)
	}

	got := childNProcLimit(t, c.pid())
	lower := liveTaskLowerBound(t)
	if got.Cur <= lower {
		t.Fatalf("applied RLIMIT_NPROC soft cap %d is not above the live per-UID task count "+
			"(>= %d) — the child is born unable to fork and every command it runs fails with "+
			"EAGAIN. This is the shape of the production `bash` outage: a cap derived from a "+
			"PROCESS count instead of a TASK count", got.Cur, lower)
	}
}

// TestApplyPostStartHardening_ChildCanFork is the end-to-end outage guard: a
// real child, spawned through the production hardening path, must actually be
// able to fork.
//
// Nothing else in the suite asserts the observable symptom of the outage —
// "sh: Cannot fork" for an innocuous command. The 192 parked threads are what
// make it deterministic: they push the live TASK count more than
// childNProcSlack (128) above the PROCESS count, so a process-derived cap is
// already exceeded when applied and the pipeline below cannot fork.
//
// Non-root only: copy_process() skips the RLIMIT_NPROC check for uid 0
// (INIT_USER / CAP_SYS_RESOURCE bypass), so as root the child forks fine even
// with a mis-sized cap and the test proves nothing. The value-level assertion
// in TestApplyPostStartHardening_AppliedCapExceedsLiveTasks is the root-safe
// guard for the same regression.
func TestApplyPostStartHardening_ChildCanFork(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("must run as non-root: the kernel exempts uid 0 from RLIMIT_NPROC " +
			"(copy_process checks `user != INIT_USER && !capable(CAP_SYS_RESOURCE)`), so a " +
			"mis-sized cap produces no EAGAIN and this test cannot discriminate")
	}
	holdLockedOSThreads(t, nprocTestThreads)

	// A pipeline forces the shell to fork, unlike a single simple command
	// which sh may exec in place.
	c := startGatedChild(t, "true | true")
	if err := applyPostStartHardening(c.cmd, Limits{}); err != nil {
		t.Fatalf("applyPostStartHardening: %v", err)
	}
	applied := childNProcLimit(t, c.pid())

	c.release(t)
	if err := c.wait(); err != nil {
		t.Fatalf("hardened child could not run `true | true`: %v\n"+
			"applied RLIMIT_NPROC soft=%d hard=%d, live per-UID tasks >= %d, stderr=%q\n"+
			"This is a total outage of the sandboxed shell: the cap was applied already "+
			"exceeded, so every fork() returns EAGAIN",
			err, applied.Cur, applied.Max, liveTaskLowerBound(t), c.stderr.String())
	}
}

// TestApplyPostStartHardening_PinsHardLimitToSoftCap pins the security half of
// the fix: the child's HARD limit must be pinned to the applied cap, never left
// at the inherited value.
//
// setrlimit(2) lets an unprivileged process raise its own SOFT limit up to its
// HARD limit at will, and neither setrlimit nor prlimit64 is in the seccomp
// denylist (pkg/sandbox/seccomp_linux.go blocks ptrace, mount, module, kexec,
// bpf, perf_event_open — not resource limits). So an inherited hard limit makes
// the entire fork-bomb cap removable with `ulimit -u unlimited`.
//
// requireNProcHeadroom guarantees the inherited hard limit is far above the
// derived cap, so "Max == Cur" cannot be satisfied by accident: an
// implementation that writes `Max: existing.Max` produces Max >> Cur here and
// fails.
func TestApplyPostStartHardening_PinsHardLimitToSoftCap(t *testing.T) {
	requireNProcHeadroom(t, 0)

	c := startGatedChild(t, "true")
	inherited := childNProcLimit(t, c.pid())

	if err := applyPostStartHardening(c.cmd, Limits{}); err != nil {
		t.Fatalf("applyPostStartHardening: %v", err)
	}
	got := childNProcLimit(t, c.pid())

	if got.Max != got.Cur {
		t.Fatalf("hardening left the child's HARD RLIMIT_NPROC at %d while the soft cap is %d "+
			"(inherited soft=%d hard=%d). An unprivileged child raises its soft limit to the "+
			"hard limit in one line (`ulimit -u unlimited`), so the fork-bomb cap is removable "+
			"and the control is decorative",
			got.Max, got.Cur, inherited.Cur, inherited.Max)
	}
}

// TestApplyPostStartHardening_ClampsDownToInheritedLimit asserts hardening
// never WIDENS an operator-configured limit. A unit file specifying
// LimitNPROC=softLimit must not be silently raised to baseline+128 for every
// sandboxed child — that is a policy inversion, and a silent one.
//
// The simulated operator limit is deliberately soft < hard (live+64 soft,
// live+384 hard), which is the shape systemd's `LimitNPROC=soft:hard` produces
// and the shape that separates the three candidate implementations:
//
//   - clamp against BOTH inherited limits (correct): applies live+64. Passes.
//   - clamp against the HARD limit only: baseline+128 is under the hard limit
//     so nothing bites; it applies live+128 and silently doubles the
//     operator's soft limit. Fails on the widening assertion.
//   - no clamp at all: applies live+128 outright, same widening. Fails. As
//     non-root against a hard limit BELOW the request it would additionally
//     EPERM, surfacing as a hardening error and failing earlier.
//
// The soft margin (64) is under childNProcSlack (128) so the clamp must bite,
// and above live usage so production's fail-closed guard does not fire — that
// guard has its own test below.
func TestApplyPostStartHardening_ClampsDownToInheritedLimit(t *testing.T) {
	// Headroom for the simulated hard limit: we can only LOWER the child's
	// limits, so live+384 must be within what we inherited.
	requireNProcHeadroom(t, 512)

	c := startGatedChild(t, "true")
	live := readCurrentUserNProc()
	if live == 0 {
		t.Skip("/proc unreadable in this environment")
	}

	const (
		operatorSoftMargin = 64  // 0 < soft margin < childNProcSlack (must clamp)
		operatorHardMargin = 384 // > childNProcSlack, so a hard-only clamp does NOT bite
	)
	operatorSoft := live + operatorSoftMargin
	operatorHard := live + operatorHardMargin
	lower := unix.Rlimit{Cur: operatorSoft, Max: operatorHard}
	if err := unix.Prlimit(c.pid(), unix.RLIMIT_NPROC, &lower, nil); err != nil {
		t.Skipf("cannot lower the child's RLIMIT_NPROC to simulate an operator limit: %v", err)
	}

	if err := applyPostStartHardening(c.cmd, Limits{}); err != nil {
		t.Fatalf("applyPostStartHardening errored with an operator RLIMIT_NPROC of "+
			"{soft=%d hard=%d}, both comfortably above live usage %d: %v",
			operatorSoft, operatorHard, live, err)
	}

	got := childNProcLimit(t, c.pid())
	if got.Cur > operatorSoft || got.Max > operatorHard {
		t.Fatalf("hardening WIDENED an operator-configured RLIMIT_NPROC: applied "+
			"{soft=%d hard=%d} against an inherited {soft=%d hard=%d}. A hardening routine "+
			"must only ever tighten",
			got.Cur, got.Max, operatorSoft, operatorHard)
	}
}

// TestApplyPostStartHardening_FailsClosedWhenCapWouldBlockAllForks pins the
// fail-closed contract: when the downward clamp produces a cap at or below live
// usage, hardening must return an ERROR (the caller kills the child and reports
// it), not log a warning and hand back a child that EAGAINs every fork.
//
// A log-only implementation passes every other test in this file — the limit is
// applied, the hard limit is pinned, nothing is widened — and still ships the
// original silent outage through a different door.
func TestApplyPostStartHardening_FailsClosedWhenCapWouldBlockAllForks(t *testing.T) {
	c := startGatedChild(t, "true")
	live := readCurrentUserNProc()
	if live == 0 {
		t.Skip("/proc unreadable in this environment")
	}

	// An operator limit of 1 is unreachably below live usage: any cap derived
	// from it leaves the child unable to fork at all.
	unusable := unix.Rlimit{Cur: 1, Max: 1}
	if err := unix.Prlimit(c.pid(), unix.RLIMIT_NPROC, &unusable, nil); err != nil {
		t.Skipf("cannot lower the child's RLIMIT_NPROC: %v", err)
	}

	err := applyPostStartHardening(c.cmd, Limits{})
	if err == nil {
		got := childNProcLimit(t, c.pid())
		t.Fatalf("applyPostStartHardening accepted an RLIMIT_NPROC cap of {soft=%d hard=%d} "+
			"against a live per-UID task count of %d — the child is born unable to fork and "+
			"nothing surfaces it to the caller", got.Cur, got.Max, live)
	}
	if !strings.Contains(err.Error(), "RLIMIT_NPROC") {
		t.Fatalf("fail-closed error does not name the limit it is about, so an operator cannot "+
			"act on it: %v", err)
	}
}
