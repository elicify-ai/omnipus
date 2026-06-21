// runner_egress_test.go — tests for the external-runner egress proxy injection
// into the dispatch path (Spec-4 FR-5.3). Verifies HTTP_PROXY/HTTPS_PROXY are
// injected into the child env when the egress proxy is available, pre-existing
// proxy vars are stripped, and graceful degradation when the proxy is absent.

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent/runner"
)

// TestInjectRunnerEgressProxy covers the env-injection helper directly.
func TestInjectRunnerEgressProxy(t *testing.T) {
	t.Run("empty addr is a no-op (graceful degradation)", func(t *testing.T) {
		in := []string{"PATH=/usr/bin", "HOME=/root", "HTTP_PROXY=http://evil:3128"}
		out := injectRunnerEgressProxy(in, "")
		assert.Equal(t, in, out, "empty proxyAddr must return env unchanged")
	})

	t.Run("injects both cases + NO_PROXY", func(t *testing.T) {
		out := injectRunnerEgressProxy([]string{"PATH=/usr/bin"}, "127.0.0.1:54321")
		require.Contains(t, out, "HTTP_PROXY=http://127.0.0.1:54321")
		require.Contains(t, out, "HTTPS_PROXY=http://127.0.0.1:54321")
		require.Contains(t, out, "http_proxy=http://127.0.0.1:54321")
		require.Contains(t, out, "https_proxy=http://127.0.0.1:54321")
		require.Contains(t, out, "NO_PROXY=127.0.0.1,localhost,::1")
		require.Contains(t, out, "no_proxy=127.0.0.1,localhost,::1")
	})

	t.Run("strips pre-existing proxy vars", func(t *testing.T) {
		in := []string{
			"PATH=/usr/bin",
			"HTTP_PROXY=http://operator-proxy:3128",
			"https_proxy=http://leaked:8080",
			"NO_PROXY=10.0.0.0/8",
		}
		out := injectRunnerEgressProxy(in, "127.0.0.1:9999")
		for _, kv := range out {
			if strings.HasPrefix(kv, "HTTP_PROXY=") {
				assert.Equal(t, "HTTP_PROXY=http://127.0.0.1:9999", kv, "injected HTTP_PROXY must win")
			}
			if strings.HasPrefix(kv, "https_proxy=") {
				assert.NotContains(t, kv, "leaked", "pre-existing https_proxy must be stripped")
			}
		}
		// Non-proxy vars preserved.
		require.Contains(t, out, "PATH=/usr/bin")
	})
}

// TestExternalDispatch_InjectsEgressProxyEnv verifies that when the AgentLoop
// has a runner egress proxy available, the dispatched RunOptions.Env carries
// HTTP_PROXY/HTTPS_PROXY pointing at it.
func TestExternalDispatch_InjectsEgressProxyEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	al, ts := newExternalTestLoop(t, "claude-code", "")
	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "ok"}})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	if _, err := runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second); err != nil {
		t.Fatalf("runExternalCLISubTurn: %v", err)
	}

	opts := fr.RecordedRunOpts()
	require.Len(t, opts, 1)
	env := opts[0].Env

	// The proxy must have been started and its address injected.
	var proxyAddr string
	for _, kv := range env {
		if strings.HasPrefix(kv, "HTTP_PROXY=") {
			proxyAddr = strings.TrimPrefix(kv, "HTTP_PROXY=")
			break
		}
	}
	require.NotEmpty(t, proxyAddr, "HTTP_PROXY must be injected into child env")
	assert.True(t, strings.HasPrefix(proxyAddr, "http://127.0.0.1:"),
		"proxy URL must point at loopback, got %q", proxyAddr)
	require.Contains(t, env, "HTTPS_PROXY="+proxyAddr)
	require.Contains(t, env, "http_proxy="+proxyAddr)
	require.Contains(t, env, "https_proxy="+proxyAddr)
}

// TestExternalDispatch_GracefulDegradationNoProxy verifies the run still
// succeeds (and no HTTP_PROXY is injected) when the runner egress proxy is
// unavailable. We force the unavailable state by pre-seeding a failed
// once.Do (simulating NewRunnerEgressProxy failure) so runnerEgressProxyAddr
// returns "".
func TestExternalDispatch_GracefulDegradationNoProxy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	al, ts := newExternalTestLoop(t, "claude-code", "")
	// Force the lazy-init to have already run with a nil proxy (degraded).
	al.runnerEgressProxyOnce.Do(func() { al.runnerEgressProxy = nil })

	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "degraded-ok"}})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	res, err := runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "degraded-ok", res.ForLLM)

	opts := fr.RecordedRunOpts()
	require.Len(t, opts, 1)
	for _, kv := range opts[0].Env {
		assert.NotContains(t, kv, "HTTP_PROXY=",
			"no HTTP_PROXY should be injected in degraded mode, got %q", kv)
	}
}

// TestRunnerEgressProxyAddr_LazilyStartsAndCaches verifies the proxy is started
// on first call and reused on subsequent calls (same address).
func TestRunnerEgressProxyAddr_LazilyStartsAndCaches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	al, _ := newExternalTestLoop(t, "claude-code", "")

	addr1 := al.runnerEgressProxyAddr()
	require.NotEmpty(t, addr1, "first call must start the proxy")
	addr2 := al.runnerEgressProxyAddr()
	assert.Equal(t, addr1, addr2, "second call must reuse the cached proxy")
}
