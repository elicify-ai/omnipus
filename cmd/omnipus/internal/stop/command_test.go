//go:build goolm && stdjson

package stop_test

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/daemon"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// TestStop_NothingRunning verifies that daemon.Stop returns (false, nil) when
// no PID file exists — the stop command must not error in that case.
func TestStop_NothingRunning(t *testing.T) {
	home := t.TempDir()
	stopped, err := daemon.Stop(home)
	if err != nil {
		t.Fatalf("daemon.Stop returned error on empty home: %v", err)
	}
	if stopped {
		t.Errorf("daemon.Stop returned stopped=true but no gateway was running")
	}
}

// TestStop_StalePID verifies that daemon.Stop cleans up a stale PID file
// (a PID that is no longer alive) without returning an error.
func TestStop_StalePID(t *testing.T) {
	home := t.TempDir()

	// Use the maximum int32 value as a PID — it is never alive in a normal
	// test environment, so checkProcess reports (false, false) → stale path.
	// Avoid PID 1 (init/systemd): it is alive but not named "omnipus", which
	// causes daemon.Stop to return an error (safety guard), not (false, nil).
	const stalePID = 2147483647
	if err := fileutil.WriteFileAtomic(
		filepath.Join(home, "gateway.pid"),
		[]byte(strconv.Itoa(stalePID)),
		0o600,
	); err != nil {
		t.Fatalf("write stale PID file: %v", err)
	}

	stopped, err := daemon.Stop(home)
	if err != nil {
		// If PID 2147483647 somehow exists, skip rather than fail.
		if stopped {
			t.Skip("PID 2147483647 appears to be alive; skipping test")
		}
		t.Fatalf("daemon.Stop returned error on stale PID: %v", err)
	}
	if stopped {
		t.Errorf("daemon.Stop returned stopped=true for a stale PID")
	}

	// The stale PID file must have been removed.
	if _, statErr := os.Stat(filepath.Join(home, "gateway.pid")); !os.IsNotExist(statErr) {
		t.Errorf("stale PID file was not removed after daemon.Stop")
	}
}
