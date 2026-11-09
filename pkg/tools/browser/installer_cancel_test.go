package browser

// #1730 (the remainder of the TempDir-cleanup family #1644): the gateway's
// boot drain cancels the managed-Chromium preprovision and then waits for it,
// bounded by a shutdown budget. Every installer step that can outlast that
// budget must observe the cancellation itself; otherwise the drain gives up,
// the caller removes or reuses the home, and the installer keeps writing into
// it. These tests pin each such step: waiting for the process-wide install
// lock, extracting the archive, and (trim_cancel_test.go) the cache sweep.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

// installCancelAckBound is a liveness bound, not a tolerance: a waiter that
// observes cancellation returns within microseconds, while a waiter that
// ignores it stays blocked until the lock holder is released — which this
// test does only AFTER the bound, so a cancellation-blind waiter can never
// pass by being lucky with timing.
const installCancelAckBound = 5 * time.Second

// TestEnsureChromiumBuild_CancelledWhileWaitingForInstallLock holds the
// install lock with a real first install whose manifest fetch is parked on a
// local server, then starts and cancels a second install. The second must
// return the cancellation while the first still holds the lock, and must not
// write anything into its install root.
func TestEnsureChromiumBuild_CancelledWhileWaitingForInstallLock(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("no managed Chrome build for this platform, so there is no install to hold the lock: %v", err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	releaseHolder := func() { releaseOnce.Do(func() { close(release) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enteredOnce.Do(func() { close(entered) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
		http.Error(w, "held by the install-lock test", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(releaseHolder) // runs before srv.Close (LIFO), so the handler never pins Close
	oldURL := globalManifestURLForTesting
	globalManifestURLForTesting = srv.URL
	t.Cleanup(func() { globalManifestURLForTesting = oldURL })

	holderRoot := t.TempDir()
	holderDone := make(chan error, 1)
	go func() {
		_, err := EnsureChromiumBuild(context.Background(), holderRoot, headlessShellBuild())
		holderDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(installCancelAckBound):
		t.Fatal("instrument: the lock-holding install never reached its manifest fetch")
	}

	waiterRoot := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() {
		_, err := EnsureChromiumBuild(ctx, waiterRoot, headlessShellBuild())
		waiterDone <- err
	}()
	cancel()

	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled install waiting for the lock returned %v, want context.Canceled", err)
		}
	case <-time.After(installCancelAckBound):
		releaseHolder()
		<-waiterDone
		<-holderDone
		t.Fatal("EnsureChromiumBuild ignored cancellation while waiting for the install lock (#1730): a cancelled preprovision blocks the boot drain until another install finishes")
	}
	releaseHolder()
	<-holderDone

	entries, err := os.ReadDir(waiterRoot)
	if err != nil {
		t.Fatalf("read waiter install root: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("cancelled install wrote %d entries into its install root, want none", len(entries))
	}
}
