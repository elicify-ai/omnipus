//go:build goolm && stdjson

package gateway

// Q4 (fix round 4, 2026-11-26): C-26 "Decision time" and "End", Dataset D17 row 9 (S-121), run at the
// gateway's launch. pkg/agent pins the loop's sweep and pkg/security pins the real running curl loop
// (the command is not killed, its new connection is refused); timed_allowance_sweep_launch_red_test.go
// pins that the gateway's start calls the launch. This file runs the launch itself:
// runContextWithOptions.startAllowanceExpirySweep, the seam the backend added so a test can start
// the sweep without startBackgroundServices (whose retention goroutines race other tests). The seam's
// name and its result, a channel that closes when the sweep has stopped, are the contract team-lead
// confirmed for this row; the backend's own test of it was not read and is not the oracle.
//
// The rig is the real thing end to end except the clock: a real AgentLoop with its approval grant
// store, the production EgressProxy, a real loopback upstream, and the loop's allowance clock injected
// through its own production input (AgentLoop.SetAllowanceClock, bound through nativebinding as in the
// sibling files). Nothing in the rig names a verdict, a token to revoke or an end: Mia's allowance is
// recorded with the store's duration-bearing call, her token is the one the store issues, and the
// hour passes only by advancing the clock. The status codes are the proxy's: 200 for a granted token,
// 403 for an ungranted or revoked one (the same statuses the pkg/security control shows). The hour,
// the three hours and the 12:00 start come from C-26's lengths and the harness clock.
//
// What this file does not pin: that the command is not killed (pkg/security, real running loop);
// that the kernel enforces anything (ST-29 and ST-30 stay UNVERIFIED); that startBackgroundServices
// calls the seam (the launch file).
//
// RED: the seam does not exist on the RED baseline 04ea85098. This file still builds there, because the
// seam is reached through an interface assertion (an unexported method name declared in this package is
// the same name as runContextWithOptions's own method), not through a direct call that would stop every
// pkg/gateway test from compiling. The two rows that need the seam then stop with t.Fatal("BLOCKED: ...")
// naming it, as the RED rules require for a missing element. That is the missing-element RED, not a
// behaviour RED: the behaviour the file checks was already present at 04ea85098 as a one-line launch in
// startBackgroundServices, so past the BLOCKED line this is a pin of existing behaviour, not a
// failing-first test of new behaviour; the launch file shows the baseline passes. Failability evidence
// is the removal run in the Q4 report.
//
// Mutations for the independent CHECK: the seam hands the sweep rc.ctx instead of rc.agentLoopCtx;
// returns a closed channel without starting it; runs the sweep synchronously; the sweep revokes every
// agent's tokens, or none, or one tick late. GREEN and mutation proof are deferred to CHECK.

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	sweepLaunchUpstreamBody = "Q4-LAUNCHED-SWEEP-UPSTREAM-9Z"
	sweepLaunchSession      = "running-curl-session"
	// The harness's two real custom agents stand in for the dataset's Mia and Jim.
	sweepLaunchMia = "test-agent"
	sweepLaunchJim = "peer-agent"
)

func TestTimedAllowance_LaunchedSweepEndsTheToken(t *testing.T) {
	t.Run("D17#9 the sweep the gateway launches refuses Mia's new connection once the hour has passed and spares Jim's", testLaunchedSweepRefusesTheEndedToken)
	t.Run("instrument: with no sweep launched nothing else revokes the ended allowance's token", testNothingRevokesWithoutALaunchedSweep)
	t.Run("characterization test: the launched sweep returns when the agent loop's context ends and not before", testLaunchedSweepStopsWithTheLoopContext)
}

func testLaunchedSweepRefusesTheEndedToken(t *testing.T) {
	rig := newSweepLaunchRig(t)
	launchGatewaySweep(t, rig.h.api.agentLoop)
	rig.clock.advance(time.Hour) // The hour passes. Nothing below calls the sweep or revokes a token.

	status, body := rig.awaitRefusal(t, rig.miaToken)
	require.Equal(t, http.StatusForbidden, status,
		"C-26 Decision time, D17 row 9: after the hour the running command's NEW connection with its exact token is refused (the launched sweep missed the end)")
	assert.NotContains(t, body, sweepLaunchUpstreamBody)

	status, body, err := rig.request(rig.jimToken)
	require.NoError(t, err, "a transport failure is not a policy refusal")
	assert.Equal(t, http.StatusOK, status, "Jim's allowance runs for three hours, so his token is not revoked as a substitute")
	assert.Equal(t, sweepLaunchUpstreamBody, body)

	store := rig.h.api.agentLoop.ApprovalGrants()
	assert.Empty(t, store.NetworkHostsFor(sweepLaunchSession, sweepLaunchMia), "the ended address is no longer allowed for Mia")
	assert.Equal(t, []string{rig.host}, store.NetworkHostsFor(sweepLaunchSession, sweepLaunchJim), "Jim's address is still allowed")
}

func testNothingRevokesWithoutALaunchedSweep(t *testing.T) {
	rig := newSweepLaunchRig(t)
	rig.clock.advance(time.Hour)
	// The sweep ticks once a second. Two ticks and slack: had anything but a launched sweep revoked
	// the token, it would be refused by now.
	time.Sleep(2500 * time.Millisecond)

	status, body, err := rig.request(rig.miaToken)
	require.NoError(t, err, "a transport failure is not a policy refusal")
	require.Equal(t, http.StatusOK, status,
		"instrument: with no sweep launched the ended allowance's token still reaches the upstream, so the refusal in the launch row comes from the launched sweep")
	require.Equal(t, sweepLaunchUpstreamBody, body)
	assert.Empty(t, rig.h.api.agentLoop.ApprovalGrants().NetworkHostsFor(sweepLaunchSession, sweepLaunchMia),
		"instrument: the allowance has ended in the store")
}

func testLaunchedSweepStopsWithTheLoopContext(t *testing.T) {
	h := newTimedCardHarness(t)
	loopCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The run context's own context stays live: only the agent loop's context ends below.
	rc := &runContextWithOptions{ctx: context.Background(), agentLoop: h.api.agentLoop, agentLoopCtx: loopCtx}
	stopped := startSweepLaunchSeam(t, rc)

	select {
	case <-stopped:
		t.Fatal("the launched sweep returned while the agent loop's context was still live")
	case <-time.After(300 * time.Millisecond):
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the launched sweep did not return after the agent loop's context ended")
	}
}

// sweepLaunchSeam is the seam team-lead confirmed for this row: runContextWithOptions.
// startAllowanceExpirySweep starts the allowance expiry sweep on the agent loop's context and returns a
// channel that closes when the sweep has stopped. Declared as an interface so this file builds on a head
// where the method does not exist yet (see the RED paragraph in the header).
type sweepLaunchSeam interface {
	startAllowanceExpirySweep() <-chan struct{}
}

// startSweepLaunchSeam starts the sweep through the run context's seam, or stops the test loudly when the
// run context has no such method.
func startSweepLaunchSeam(t *testing.T, rc *runContextWithOptions) <-chan struct{} {
	t.Helper()
	var candidate any = rc
	seam, ok := candidate.(sweepLaunchSeam)
	if !ok {
		t.Fatal("BLOCKED: runContextWithOptions.startAllowanceExpirySweep() <-chan struct{} not implemented — " +
			"required by C-26 \"Decision time\" and Dataset D17 row 9 (S-121): the seam a test starts the " +
			"allowance expiry sweep through without startBackgroundServices")
	}
	return seam.startAllowanceExpirySweep()
}

// launchGatewaySweep starts the sweep the way the gateway's start does, through the run context's
// seam, on an agent loop context the test owns. The cleanup ends it and waits for it, so no goroutine
// outlives the test, and the proxy it revokes on is closed after it.
func launchGatewaySweep(t *testing.T, al *agent.AgentLoop) <-chan struct{} {
	t.Helper()
	loopCtx, cancel := context.WithCancel(context.Background())
	rc := &runContextWithOptions{ctx: context.Background(), agentLoop: al, agentLoopCtx: loopCtx}
	stopped := startSweepLaunchSeam(t, rc)
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Error("the launched sweep did not return after its agent loop context ended")
		}
	})
	return stopped
}

type sweepLaunchRig struct {
	h        *timedCardHarness
	clock    *scheduledAllowanceClock
	proxy    *sandbox.EgressProxy
	upstream string
	host     string
	miaToken string
	jimToken string
}

// newSweepLaunchRig builds a real loop wired with a real egress proxy and holds a one-hour address
// allowance for Mia and a three-hour one for Jim on the real loopback upstream's address, each
// pushed onto the agent's running-command token the way a bash call pushes the agent's allowed
// addresses.
func newSweepLaunchRig(t *testing.T) *sweepLaunchRig {
	t.Helper()
	h := newTimedCardHarness(t)
	clock := controlledTimedCardClock(t, h)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := io.WriteString(w, sweepLaunchUpstreamBody)
		assert.NoError(t, err)
	}))
	t.Cleanup(upstream.Close)
	upstreamURL, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	host, _, err := net.SplitHostPort(upstreamURL.Host)
	require.NoError(t, err)
	proxy, err := sandbox.NewEgressProxy(nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, proxy.Close()) })
	h.api.agentLoop.WireTier13Deps(agent.Tier13Deps{EgressProxy: proxy})

	store := h.api.agentLoop.ApprovalGrants()
	end, ok := store.RecordNetworkGrantFor(sweepLaunchMia, []string{host}, time.Hour)
	require.True(t, ok, "the loop's store takes Mia's timed answer for the named address")
	require.Equal(t, time.Date(2026, time.November, 22, 13, 0, 0, 0, time.UTC), end,
		"instrument: one hour after 12:00 on the injected clock")
	_, ok = store.RecordNetworkGrantFor(sweepLaunchJim, []string{host}, 3*time.Hour)
	require.True(t, ok, "Jim holds an allowance of his own, three hours long, for the same address")

	rig := &sweepLaunchRig{
		h: h, clock: clock, proxy: proxy, upstream: upstream.URL, host: host,
		miaToken: store.SessionEgressToken(sweepLaunchSession, sweepLaunchMia),
		jimToken: store.SessionEgressToken(sweepLaunchSession, sweepLaunchJim),
	}
	require.NotEmpty(t, rig.miaToken, "the store minted Mia's running-command token")
	require.NotEmpty(t, rig.jimToken, "the store minted Jim's running-command token")
	require.NotEqual(t, rig.miaToken, rig.jimToken)
	proxy.GrantRunHosts(rig.miaToken, store.NetworkHostsFor(sweepLaunchSession, sweepLaunchMia))
	proxy.GrantRunHosts(rig.jimToken, store.NetworkHostsFor(sweepLaunchSession, sweepLaunchJim))
	for _, token := range []string{rig.miaToken, rig.jimToken} {
		status, body, reqErr := rig.request(token)
		require.NoError(t, reqErr, "instrument: a granted token's request reaches the proxy")
		require.Equal(t, http.StatusOK, status, "instrument: a granted token reaches the upstream while its allowance runs")
		require.Equal(t, sweepLaunchUpstreamBody, body)
	}
	return rig
}

// request opens one NEW connection through the real proxy with token as the run token. It never
// asserts: the callers decide what a status means.
func (r *sweepLaunchRig) request(token string) (int, string, error) {
	proxyURL := &url.URL{Scheme: "http", Host: r.proxy.Addr(), User: url.User(token)}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get(r.upstream + "/launched-sweep")
	if err != nil {
		return 0, "", err
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	return response.StatusCode, string(body), errors.Join(readErr, closeErr)
}

// awaitRefusal opens a new connection with token every 100 ms until the proxy refuses one with 403
// or ten seconds have passed. The sweep ticks once a second, so the deadline is a failure, never a
// threshold to widen.
func (r *sweepLaunchRig) awaitRefusal(t *testing.T, token string) (int, string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, body, err := r.request(token)
		require.NoError(t, err, "a transport failure is not a policy refusal")
		if status == http.StatusForbidden || time.Now().After(deadline) {
			return status, body
		}
		time.Sleep(100 * time.Millisecond)
	}
}
