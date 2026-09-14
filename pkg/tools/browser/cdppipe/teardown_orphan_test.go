//go:build !windows

package cdppipe

// teardown_orphan_test.go — teardown must return, and must not leave Chrome's
// helper processes behind, when a helper outlives the browser process.
//
// Measured on a heavily loaded macOS host (2026-09-14): a managed Chrome's
// renderer was re-parented to launchd when the browser process exited, kept
// running, and kept Chrome's stderr pipe open. cdppipe routes that stderr
// through a Go writer (PipeOptions.Errf), so exec.Cmd.Wait also waits for the
// stderr copy goroutine — which cannot finish while ANY process still holds
// the pipe's write end. A goroutine dump of the stuck test binary showed
// launch.teardown parked at `<-waited`, behind exec.(*Cmd).awaitGoroutines,
// behind exec.(*Cmd).writerDescriptor's copy loop, more than 10 minutes after
// teardown began. Every caller of the CancelFunc (coordinator
// Shutdown, crash recovery, the LostConnection watcher) blocks with it, and the
// orphaned renderer keeps consuming memory on a host that was already out of it.
//
// The fakes below stand in for Chrome in exactly the respects that matter:
// each exits when its CDP command pipe (fd 3) reaches EOF, as Chrome does, and
// each leaves a child running that inherited stderr.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// helperInGroupFakeChrome leaves an ordinary helper behind: it stays in
// Chrome's process group, like a renderer.
const helperInGroupFakeChrome = `#!/bin/sh
sleep 300 &
echo $! > "$ORPHAN_PIDFILE"
cat <&3 >/dev/null
exit 0
`

// helperOutsideGroupFakeChrome leaves a helper that moved itself into a new
// session — out of Chrome's process group, where no group kill reaches it,
// like a daemonizing crash handler. perl provides setsid(2) portably (macOS has
// no setsid(1)); exec keeps the pid that $! reported.
const helperOutsideGroupFakeChrome = `#!/bin/sh
perl -e 'use POSIX (); POSIX::setsid(); exec "sleep", "300"' &
echo $! > "$ORPHAN_PIDFILE"
cat <&3 >/dev/null
exit 0
`

// teardownOrphanCeiling is far above what a bounded teardown needs here (each
// fake exits the moment fd 3 closes, so nothing waits on the 5s kill grace) and
// far below the 300s a helper would otherwise hold stderr open — so an
// unbounded teardown trips it on any machine, loaded or not.
const teardownOrphanCeiling = 20 * time.Second

func TestTeardown_ReturnsAndReapsHelperThatOutlivesChrome(t *testing.T) {
	l, helper := startFakeChrome(t, helperInGroupFakeChrome)

	teardownWithinCeiling(t, l, helper)

	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(helper, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("helper process %d is still alive 5s after teardown returned — a helper that outlives "+
				"the browser must be reaped with it, not left running", helper)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestTeardown_ReturnsWhenAHelperEscapedTheProcessGroup pins the second half of
// the bound: a helper the group kill cannot reach still must not hold teardown
// hostage through the stderr pipe.
func TestTeardown_ReturnsWhenAHelperEscapedTheProcessGroup(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl is not on PATH; it is only used to call setsid(2) from the fake Chrome")
	}
	l, helper := startFakeChrome(t, helperOutsideGroupFakeChrome)

	teardownWithinCeiling(t, l, helper)
}

// startFakeChrome launches script through launch.start with Errf set, exactly
// as the managed launcher does (that is what makes Chrome's stderr a pipe
// exec.Cmd.Wait drains), and returns the pid of the helper it left running.
// The helper is SIGKILLed at cleanup if the behaviour under test left it alive.
func startFakeChrome(t *testing.T, script string) (*launch, int) {
	t.Helper()
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-chrome.sh")
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake chrome: %v", err)
	}
	pidFile := filepath.Join(dir, "helper.pid")

	l := &launch{
		execPath: fake,
		opts: PipeOptions{
			Env:         []string{"ORPHAN_PIDFILE=" + pidFile},
			UserDataDir: dir,
			Errf:        func(string, ...any) {},
		},
	}
	if err := l.start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	helper := readHelperPID(t, pidFile)
	t.Cleanup(func() {
		if err := syscall.Kill(helper, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Logf("cleanup kill of helper %d: %v", helper, err)
		}
	})
	return l, helper
}

func teardownWithinCeiling(t *testing.T, l *launch, helper int) {
	t.Helper()
	done := make(chan struct{})
	started := time.Now()
	go func() {
		l.teardown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(teardownOrphanCeiling):
		t.Fatalf("teardown did not return within %s while a helper process (pid %d) that outlived the "+
			"browser still held its stderr pipe — every caller of the allocator's CancelFunc blocks with it",
			teardownOrphanCeiling, helper)
	}
	t.Logf("teardown returned in %s", time.Since(started).Round(time.Millisecond))
}

func readHelperPID(t *testing.T, path string) int {
	t.Helper()
	// Setup readiness, not the property under test: how long the fake gets to
	// start and report its helper. A mutation run on a host at load ~480 with
	// swap exhausted once spent more than 15s just getting sh running, and the
	// test then failed on setup — a failure that says nothing about teardown.
	// The teardown bound (teardownOrphanCeiling) and the reap check are
	// unaffected by this wait.
	deadline := time.Now().Add(60 * time.Second)
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake chrome never wrote its helper pid to %s (last read error: %v)", path, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
