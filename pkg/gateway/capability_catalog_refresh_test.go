// capability_catalog_refresh_test.go — FIX-2 regression test.
//
// gateway.go's capability-catalog refresh goroutine (FR-024/FR-025/FR-026)
// used to build a time.Ticker and immediately enter its select loop with no
// call to capCatalog.Refresh beforehand. Go's time.Ticker does not fire on
// creation, so any gateway restarted more often than the 7-day interval
// (dev pods, containers, k8s rolling deploys, systemd restarts) never
// invoked the puller and ran indefinitely on the build-time embedded seed —
// contradicting catalog.go's own "on gateway startup and every 7 days"
// promise. This test proves runCapabilityCatalogRefreshLoop (the extracted
// function gateway.go's setupAndStartServices now calls) invokes Refresh
// immediately, without waiting for the first tick.

package gateway

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers/capabilities"
)

// errCountingPullerStub is the fixed error countingPuller.Pull always
// returns — no network access, no valid catalog JSON needed. Refresh
// treats any Pull error as "log and retain last-known-good", which is
// exactly what this test wants: a fast, deterministic, network-free signal
// that Refresh was actually invoked.
var errCountingPullerStub = errors.New("countingPuller: stub pull failure (test double)")

// countingPuller is a test-double capabilities.Puller that signals pullCh
// on every Pull call.
type countingPuller struct {
	pullCh chan struct{}
}

func (p *countingPuller) Pull(ctx context.Context) ([]byte, error) {
	select {
	case p.pullCh <- struct{}{}:
	default:
	}
	return nil, errCountingPullerStub
}

// TestRunCapabilityCatalogRefreshLoop_RefreshesImmediately is the FIX-2
// regression test. It uses a 1-hour refresh interval — long enough that the
// ticker cannot plausibly fire during this test — and asserts the fake
// Puller's Pull method is still invoked almost immediately. Before the fix,
// this would time out waiting on pullCh: nothing calls Pull until the (here,
// 1-hour) ticker fires.
func TestRunCapabilityCatalogRefreshLoop_RefreshesImmediately(t *testing.T) {
	puller := &countingPuller{pullCh: make(chan struct{}, 4)}
	cat, err := capabilities.NewCatalog(capabilities.EmbeddedSeed(), puller, nil, slog.Default())
	require.NoError(t, err, "catalog must construct from the embedded seed with a nil store")

	go runCapabilityCatalogRefreshLoop(cat, time.Hour, 2*time.Second)

	select {
	case <-puller.pullCh:
		// Pass: Refresh (and therefore Pull) ran before the 1-hour ticker
		// could possibly have fired.
	case <-time.After(5 * time.Second):
		t.Fatal("capability catalog was not refreshed on startup — Pull was never called " +
			"within 5s of starting a loop with a 1-hour ticker interval")
	}
}

// TestRunCapabilityCatalogRefreshLoop_NilPuller_NoOp guards the
// puller==nil short-circuit documented on capabilities.Catalog.Refresh:
// the loop must not panic or block forever when the catalog has no puller
// configured (e.g. catalog construction succeeded but no Puller was
// wired). Bounded via a short interval + short test timeout rather than
// asserting anything about Refresh's return value directly (Refresh
// returning nil for puller==nil is capabilities package behavior, not
// gateway's to re-test) — this only proves the extracted loop itself
// doesn't misbehave in that configuration.
func TestRunCapabilityCatalogRefreshLoop_NilPuller_NoOp(t *testing.T) {
	cat, err := capabilities.NewCatalog(capabilities.EmbeddedSeed(), nil, nil, slog.Default())
	require.NoError(t, err)

	// runCapabilityCatalogRefreshLoop has no exit path (matches the
	// original inline goroutine's forever-loop shape), so this goroutine is
	// intentionally leaked past test completion — same as production
	// behavior, and harmless for a single short-lived test binary run.
	go runCapabilityCatalogRefreshLoop(cat, 10*time.Millisecond, time.Second)

	// If the immediate refresh call panicked (e.g. a nil-puller dereference
	// bug), the test process would have already crashed by the time this
	// sleep returns.
	time.Sleep(50 * time.Millisecond)
	assert.True(t, true, "no panic on puller==nil")
}

// TestRunCapabilityCatalogRefreshLoop_FailureLoggedToFileSink is a
// re-review FIX 1 regression test: the refresh-failure log line inside
// runCapabilityCatalogRefreshLoop used to be a bare slog.Warn, which never
// reaches $OMNIPUS_HOME/logs/gateway.log on a real (backgrounded) gateway
// since nothing in this repo ever calls slog.SetDefault. It must now be
// discoverable through pkg/logger's file sink (the same one
// logger.EnableFileLogging wires to gateway.log at gateway.go's own
// EnableFileLogging call).
func TestRunCapabilityCatalogRefreshLoop_FailureLoggedToFileSink(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "capability-catalog-refresh.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	puller := &countingPuller{pullCh: make(chan struct{}, 4)}
	cat, err := capabilities.NewCatalog(capabilities.EmbeddedSeed(), puller, nil, capabilityCatalogLogAdapter{})
	require.NoError(t, err)

	go runCapabilityCatalogRefreshLoop(cat, time.Hour, 2*time.Second)

	select {
	case <-puller.pullCh:
	case <-time.After(5 * time.Second):
		t.Fatal("capability catalog was not refreshed on startup")
	}

	// The Warn call happens synchronously inside Refresh, which the
	// pullCh signal above only confirms was ENTERED (Pull was called) —
	// give the rest of refreshLocked a moment to return and the Warn call
	// to land before reading the file back.
	require.Eventually(t, func() bool {
		data, readErr := os.ReadFile(logFile)
		return readErr == nil && len(data) > 0
	}, 2*time.Second, 10*time.Millisecond, "refresh failure was never logged to the file sink")

	data, err := os.ReadFile(logFile)
	require.NoError(t, err)
	logged := string(data)
	assert.Contains(t, logged, "gateway: capability catalog initial refresh failed; embedded seed retained")
	assert.Contains(t, logged, errCountingPullerStub.Error())
}

// TestCapabilityCatalogLogAdapter_RoutesToLoggerFileSink is the FIX 1b unit
// test: capabilityCatalogLogAdapter (passed to capabilities.NewCatalog in
// place of slog.Default(), which catalog.go — another agent's file, out of
// scope to edit here — accepts via a structurally-matched, unexported
// Warn/Info interface) must forward both Warn and Info calls, together
// with their key/value args, into pkg/logger's file sink.
func TestCapabilityCatalogLogAdapter_RoutesToLoggerFileSink(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "capability-catalog-log-adapter.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.DEBUG)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	adapter := capabilityCatalogLogAdapter{}
	adapter.Warn("capabilities: pull failed; retaining last-known-good", "error", errors.New("boom-warn-9f21"))
	adapter.Info("capabilities: pulled catalog applied", "version", "2026-07-25")

	data, err := os.ReadFile(logFile)
	require.NoError(t, err)
	logged := string(data)
	assert.Contains(t, logged, "capabilities: pull failed; retaining last-known-good")
	assert.Contains(t, logged, "boom-warn-9f21")
	assert.Contains(t, logged, "capabilities: pulled catalog applied")
	assert.Contains(t, logged, "2026-07-25")
}

// TestSlogArgsToFields covers the key/value-pair conversion helper
// directly, including the malformed odd-length call site slog itself
// documents a "!BADKEY" convention for.
func TestSlogArgsToFields(t *testing.T) {
	fields := slogArgsToFields([]any{"a", 1, "b", "two"})
	assert.Equal(t, map[string]any{"a": 1, "b": "two"}, fields)

	fields = slogArgsToFields(nil)
	assert.Empty(t, fields)

	fields = slogArgsToFields([]any{"a", 1, "orphan"})
	assert.Equal(t, 1, fields["a"])
	assert.Equal(t, "orphan", fields["!BADKEY"])
}
