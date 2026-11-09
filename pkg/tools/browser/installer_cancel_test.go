package browser

// #1730 (the remainder of the TempDir-cleanup family #1644): the gateway's
// boot drain cancels the managed-Chromium preprovision and then waits for it,
// bounded by a shutdown budget. Every installer step that can outlast that
// budget must observe the cancellation itself; otherwise the drain gives up,
// the caller removes or reuses the home, and the installer keeps writing into
// it. These tests pin each such step: waiting for the process-wide install
// lock, extracting the archive (installer_extract_cancel_test.go), and the
// cache sweep (trim_cancel_test.go).

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// installCancelAckBound is a liveness bound, not a tolerance: a waiter that
// observes cancellation returns within microseconds, while a waiter that
// ignores it stays blocked until the slot holder releases — which this test
// does only AFTER the bound, so a cancellation-blind waiter can never pass by
// being lucky with timing.
const installCancelAckBound = 5 * time.Second

// installWaiterCreator is the frame Go's goroutine dump prints under
// "created by" for the waiter goroutine this test starts, so the queued
// witness below can only ever match THIS test's waiter.
const installWaiterCreator = "created by github.com/elicify-ai/omnipus/pkg/tools/browser.TestEnsureChromiumBuild_CancelledWhileWaitingForInstallLock"

// installWaiterQueued reports whether this test's waiter goroutine is parked
// inside acquireInstallSlot on a channel operation — that is, it is really
// queued behind the held slot, not merely started.
func installWaiterQueued() bool {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	for _, g := range strings.Split(string(buf), "\n\n") {
		header, _, _ := strings.Cut(g, "\n")
		parked := strings.Contains(header, "[select") || strings.Contains(header, "[chan send")
		if parked && strings.Contains(g, "browser.acquireInstallSlot(") && strings.Contains(g, installWaiterCreator) {
			return true
		}
	}
	return false
}

// TestEnsureChromiumBuild_CancelledWhileWaitingForInstallLock holds the
// process-wide install slot itself (the slot is taken before any platform,
// manifest or network step, so this needs no managed-Chrome platform), starts
// an install, waits until that install is provably queued on the slot, and
// only THEN cancels it. The install must return the cancellation while the
// slot is still held, and must write nothing into its install root.
func TestEnsureChromiumBuild_CancelledWhileWaitingForInstallLock(t *testing.T) {
	installSlot <- struct{}{} // the "other install" holding the lock
	held := true
	releaseHolder := func() {
		if held {
			held = false
			<-installSlot
		}
	}
	t.Cleanup(releaseHolder)

	waiterRoot := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiterDone := make(chan error, 1)
	go func() {
		_, err := EnsureChromiumBuild(ctx, waiterRoot, headlessShellBuild())
		waiterDone <- err
	}()

	// In-flight witness: the cancel must arrive AFTER the waiter is parked on
	// the slot. The loop ends on that observed state; the deadline only fails.
	queuedBy := time.Now().Add(installCancelAckBound)
	for !installWaiterQueued() {
		select {
		case err := <-waiterDone:
			t.Fatalf("instrument: the install returned (%v) before it ever queued on the held slot", err)
		default:
		}
		if time.Now().After(queuedBy) {
			t.Fatal("instrument: the install never queued on the held install slot")
		}
		runtime.Gosched()
	}
	cancel()

	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled install waiting for the lock returned %v, want context.Canceled", err)
		}
	case <-time.After(installCancelAckBound):
		releaseHolder()
		<-waiterDone
		t.Fatal("EnsureChromiumBuild ignored cancellation while waiting for the install lock (#1730): a cancelled preprovision blocks the boot drain until another install finishes")
	}
	releaseHolder()

	entries, err := os.ReadDir(waiterRoot)
	if err != nil {
		t.Fatalf("read waiter install root: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("cancelled install wrote %d entries into its install root, want none", len(entries))
	}
}
