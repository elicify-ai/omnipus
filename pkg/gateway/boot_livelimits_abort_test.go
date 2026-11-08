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
// channel delivering the fetch's request context once it started, and a
// releaseNow function that unblocks the held answer (safe to call any
// number of times; the cleanup calls it too, on every path).
func bootPrimedLocalFetch(t *testing.T) (*runContextWithOptions, <-chan context.Context, func()) {
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
	return rc, entered, func() { releaseOnce.Do(func() { close(release) }) }
}

// TestExplicitLiveLimitsCloseCancelsPrimedFetch is the positive control for
// the abort regression below: the same held fetch, closed EXPLICITLY, comes
// back cancelled with no cache file. It proves the instrument would see
// the defect-1 write if an abort left the writer alive — without it, a
// silent pass below could only mean the fetch never started.
func TestExplicitLiveLimitsCloseCancelsPrimedFetch(t *testing.T) {
	rc, entered, releaseNow := bootPrimedLocalFetch(t)
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
	rc, entered, releaseNow := bootPrimedLocalFetch(t)
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
