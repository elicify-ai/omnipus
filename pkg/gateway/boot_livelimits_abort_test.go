package gateway

// CHECK 2 (2026-11-08, defect 1): a boot abort that returns from
// validateAndApplySandbox used to leave the unknown-window primer's rung-4
// fetch running. That fetch's writer lives under the LiveLimits instance's
// own lifetime (agent.NewLiveLimits derives it from context.Background(),
// deliberately independent of every gateway context and of the boot
// WaitGroup the abort drains), so the join did not cover it: when the held
// HTTP answer was released afterwards, the fetch wrote
// cache/model_limits.json into the home the caller had already started
// removing. The fix closes rc.liveLimits inside stopBootBackgroundWriters,
// after the WaitGroup join. These tests boot the REAL primer against a
// held, in-memory local endpoint and assert on the actual cache file, not
// on a cancel flag.
//
// Only the EXTERNAL HTTP edge is replaced: one localhost-shaped endpoint
// answered fully in memory; the default transport is restored in cleanup,
// so no request can reach the network.
import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// bootPrimedLocalFetch boots the real loadConfigAndProvider +
// initializeAgentLoop with every agent on a `locality: local` model the
// catalog cannot size, so the real unknown-window primer
// (live_limits_boot.go::primeUnknownWindows) starts a real rung-4 fetch,
// and holds that fetch's HTTP answer open. Returns the run context, a
// channel delivering the fetch's request context once it started, a
// releaseNow function that unblocks the held answer (safe to call any
// number of times; the cleanup calls it too, on every path), and the
// fetch's exit witness: closed when the held fetch's HTTP edge has returned,
// on either path (answer released, or request context cancelled). A drain
// that closed the rung but did not JOIN its writer is back while this
// witness is still open — CHECK 3 (2026-11-08) finding R1's probe shape.
func bootPrimedLocalFetch(t *testing.T) (*runContextWithOptions, <-chan context.Context, func(), <-chan struct{}) {
	t.Helper()
	home, _ := bootFixture(t)
	bootEditedPlan(t, home)
	// The live-window lookup is a package-global the boot sets; reset it on
	// both ends so this test cannot poison later ones with a stale rung.
	agent.SetLiveWindowLookup(nil)
	ctx, cancel := context.WithCancel(context.Background())
	releaseOnce := new(sync.Once)
	release := make(chan struct{})
	entered := make(chan context.Context, 1)
	fetchExited := make(chan struct{})
	exitOnce := new(sync.Once)
	old := http.DefaultTransport
	http.DefaultTransport = bootDownloadTransport(func(r *http.Request) (*http.Response, error) {
		// Ollama's shipped catalog row can supply localhost rather than the
		// explicit APIBase below; both shapes stay fully in memory.
		host := r.URL.Hostname()
		if host != "bootll-local.invalid" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return nil, errors.New("boot live-limits instrument forbids external request: " + r.URL.Host)
		}
		if r.URL.Path != "/api/ps" {
			return nil, errors.New("boot live-limits instrument refuses unexpected endpoint: " + r.URL.Path)
		}
		// The exit witness sits AFTER the guards: a refused request (the boot's
		// browser preprovision also rides this transport) is not the held
		// fetch, and must not close the witness.
		defer exitOnce.Do(func() { close(fetchExited) })
		select {
		case entered <- r.Context():
		default:
		}
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-release:
		}
		data := `{"models":[{"name":"bootll-unknown-local","context_length":131072}]}`
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(data)), Request: r}, nil
	})
	rc := &runContextWithOptions{ctx: ctx, homePath: home, configPath: filepath.Join(home, "config.json"), allowEmptyStartup: true}
	// Instrument safety net for a FAILED assertion: unblock the held fetch
	// before the teardown closes the rung, so the test binary never hangs on
	// a writer this test was proving something about.
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		if rc.liveLimits != nil {
			rc.liveLimits.Close() // once-guarded in agent.LiveLimits: idempotent
		}
		stopCtx, stopCancel := context.WithTimeout(context.Background(), bootAbortDrainTimeout)
		defer stopCancel()
		rc.stopBootBackgroundWriters(stopCtx)
		cancel()
		if rc.agentLoop != nil {
			rc.agentLoop.Close()
		}
		if rc.credStore != nil {
			rc.credStore.Close()
		}
		agent.SetLiveWindowLookup(nil)
		http.DefaultTransport = old
	})
	err, stop := rc.loadConfigAndProvider()
	require.NoError(t, err)
	require.False(t, stop)
	rc.cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: "ollama", Model: "bootll-unknown-local"}
	for i := range rc.cfg.Agents.List {
		rc.cfg.Agents.List[i].Model = &config.AgentModelConfig{Provider: "ollama", Primary: "bootll-unknown-local"}
	}
	rc.cfg.Providers = []*config.ModelConfig{{Provider: "ollama", Model: "bootll-unknown-local", APIBase: "http://bootll-local.invalid/v1"}}
	rc.provider = &restMockProvider{}
	err, stop = rc.initializeAgentLoop()
	require.NoError(t, err)
	require.False(t, stop)
	return rc, entered, func() { releaseOnce.Do(func() { close(release) }) }, fetchExited
}

// TestExplicitLiveLimitsCloseCancelsPrimedFetch is the positive control for
// the abort regression below: the same held fetch, closed EXPLICITLY, comes
// back cancelled with no cache file. It proves the instrument would see
// the defect-1 write if an abort left the writer alive — without it, a
// silent pass below could only mean the fetch never started.
func TestExplicitLiveLimitsCloseCancelsPrimedFetch(t *testing.T) {
	rc, entered, releaseNow, _ := bootPrimedLocalFetch(t)
	var query context.Context
	select {
	case query = <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: the real unknown-window primer did not start the local live-limit fetch")
	}
	rc.liveLimits.Close()
	select {
	case <-query.Done():
	default:
		t.Fatal("explicit LiveLimits.Close did not cancel the held live-limit query")
	}
	require.True(t, rc.liveLimits.Closed())
	releaseNow()
	rc.liveLimits.Wait()
	_, readErr := os.ReadFile(rc.liveLimits.CachePath())
	require.ErrorIs(t, readErr, os.ErrNotExist, "explicit close must prevent the cache write")
}

// TestSandboxAbortClosesLiveLimitsBeforeReturn is defect 1's regression:
// every stop=true return of validateAndApplySandbox happens after the real
// unknown-window primer started a rung-4 fetch, and the abort must close
// the rung with the boot writers it drains — cancelling the held fetch and
// waiting for its writer — before returning. The caller treats this return
// as "the gateway is gone" and removes homePath right after it, so a fetch
// released afterwards must not be able to write cache/model_limits.json
// into it. The oracle is the actual cache file: the held answer is released
// only AFTER the abort returned, so any writer the abort failed to join
// lands its write exactly then.
func TestSandboxAbortClosesLiveLimitsBeforeReturn(t *testing.T) {
	rc, entered, releaseNow, _ := bootPrimedLocalFetch(t)
	var query context.Context
	select {
	case query = <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: the real unknown-window primer did not start the local live-limit fetch")
	}

	rc.opts.SandboxMode = "bootll-invalid"
	err, stop := rc.validateAndApplySandbox()
	require.True(t, stop, "instrument: the real sandbox-validation abort branch was reached")
	require.Error(t, err)
	var bootErr *SandboxBootError
	require.ErrorAs(t, err, &bootErr, "expected a sandbox boot error, got %v", err)

	// The abort drained the boot writers; the rung must be closed with them.
	if !rc.liveLimits.Closed() {
		t.Error("sandbox abort returned with the live-limits instance still open: the LiveLimits.Close boundary was not reached")
	}
	select {
	case <-query.Done():
	default:
		t.Error("sandbox abort returned with the primer-started live-limit query still active")
	}

	// Release the held answer only after the abort returned.
	releaseNow()
	rc.liveLimits.Wait()
	data, readErr := os.ReadFile(rc.liveLimits.CachePath())
	if readErr == nil {
		t.Errorf("real boot writer persisted cache after the sandbox abort returned: %s", data)
	} else {
		require.ErrorIs(t, readErr, os.ErrNotExist)
	}

	// Exactly-once: the abort path already closed the rung; the later
	// teardown closes must stay idempotent — never a double-close panic.
	require.NotPanics(t, func() {
		rc.liveLimits.Close()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), bootAbortDrainTimeout)
		defer stopCancel()
		rc.stopBootBackgroundWriters(stopCtx)
	})
}

// TestSharedCleanupClosesLiveLimitsBeforeReturn is CHECK 3 (2026-11-08)
// finding R1's regression: the shared non-reload teardown drained the boot
// WaitGroup and the catalog/orphan-GC handles but never closed the RETAINED
// LiveLimits instance, so a primer fetch held mid-flight stayed live past
// the return and wrote cache/model_limits.json the moment its answer was
// released — into a home the caller already treats as removed. The services
// value carries the exact three attachments startServices makes
// (gateway.go::startServices: LiveLimits + the boot cancel/done handles),
// and the oracle is the actual cache file, exactly as in the sandbox-abort
// regression above; the fetch-exit witness additionally pins the JOIN (a
// close that fired but was not awaited would cancel the answer away while
// leaving the writer unjoined at the boundary). Reload is excluded by the
// same !isReload guard that excludes every boot writer — the process-lifetime
// instance survives reload (gateway_reload.go r17 TK-30) — and the later
// teardown closes stay idempotent (agent.LiveLimits.Close is once-guarded).
func TestSharedCleanupClosesLiveLimitsBeforeReturn(t *testing.T) {
	rc, entered, releaseNow, fetchExited := bootPrimedLocalFetch(t)
	var query context.Context
	select {
	case query = <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: the real unknown-window primer did not start the local live-limit fetch")
	}

	// The exact production attachment startServices makes.
	rs := &services{
		LiveLimits:           rc.liveLimits,
		bootBackgroundCancel: rc.bootBackgroundCancel,
		bootBackgroundDone:   rc.bootBackgroundDone,
	}
	stopAndCleanupServices(rs, 5*time.Second, false)

	if !rc.liveLimits.Closed() {
		t.Error("shared non-reload cleanup returned with the retained live-limits instance still open")
	}
	select {
	case <-query.Done():
	default:
		t.Error("shared non-reload cleanup returned with the primer-started live-limit query still active")
	}
	select {
	case <-fetchExited:
	default:
		t.Error("shared non-reload cleanup returned before the held fetch's HTTP edge exited: the live-limits writer was not joined")
	}

	// Release the held answer only after the cleanup returned.
	releaseNow()
	rc.liveLimits.Wait()
	data, readErr := os.ReadFile(rc.liveLimits.CachePath())
	if readErr == nil {
		t.Errorf("real boot writer persisted cache after the shared cleanup returned: %s", data)
	} else {
		require.ErrorIs(t, readErr, os.ErrNotExist)
	}

	// Double close stays safe: the cleanup already closed the rung; the later
	// teardown close must stay idempotent — never a double-close panic.
	require.NotPanics(t, func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), bootAbortDrainTimeout)
		defer stopCancel()
		rc.stopBootBackgroundWriters(stopCtx)
	})
}
