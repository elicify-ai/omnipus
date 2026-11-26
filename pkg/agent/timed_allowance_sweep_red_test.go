//go:build goolm && stdjson

package agent

// Part H (2026-11-26): C-26 "Decision time" and "End", Dataset D17 row 9 (S-121), the
// production caller half. pkg/security's TestShellTimedAllowances_NarrowSubjects pins what the
// STORE reports when a timed address allowance ends (SweepEndedNetwork names exactly the ended
// agent's live egress tokens) and watches a real running curl loop be refused once those tokens
// are revoked. This file pins what the LOOP does with that report, and that it does it by itself:
//
//   - AgentLoop.SweepEndedAllowances hands each token the store names to the real production
//     EgressProxy.RevokeRunToken: Mia's token is refused at the end, Jim's, which holds a grant
//     of its own, is not, and nothing is revoked one nanosecond early.
//   - AgentLoop.RunAllowanceExpirySweep calls it on its own: no test code calls the sweep or
//     revokes a token in the automatic row.
//
// The clock is the loop's own input (AgentLoop.SetAllowanceClock); the proxy and the upstream are
// real loopback servers; no verdict, token or refusal is supplied by the test. The expected
// statuses come from C-26 (an ended allowance revokes the token; the proxy refuses an ungranted or
// revoked token with 403, the status the pkg/security control shows for the same proxy).
// That the gateway STARTS RunAllowanceExpirySweep (the launch in
// pkg/gateway/gateway.go::runContextWithOptions.startBackgroundServices) is pinned in pkg/gateway
// (Q4, 2026-11-26): timed_allowance_sweep_launch_red_test.go reads that the start launches it, and
// timed_allowance_sweep_seam_red_test.go runs the launch through the seam the backend added,
// because calling startBackgroundServices leaves running the retention goroutines that read the
// package-level retention*SweepFn variables other tests write (a data race under -race).
// Mutations (independent CHECK): never revoke, revoke every agent's tokens, revoke one tick
// early, never start the ticker, ignore ctx cancel. GREEN and mutation proof are deferred.

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sweepUpstreamBody = "D17-9-SWEEP-UPSTREAM-9Z"

type sweepRig struct {
	al          *AgentLoop
	proxy       *sandbox.EgressProxy
	clock       *allowanceTestClock
	upstreamURL string
	host        string
	miaToken    string
	jimToken    string
	end         time.Time
}

// newSweepRig builds a real loop wired with a real egress proxy and holds, for Mia, a timed address
// allowance of one hour on the real loopback upstream's address, pushed onto her running command's
// token the way a bash call pushes the agent's allowed addresses; Jim's token holds an allowance of
// its own for the same address.
func newSweepRig(t *testing.T) *sweepRig {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := io.WriteString(w, sweepUpstreamBody)
		assert.NoError(t, err)
	}))
	t.Cleanup(upstream.Close)
	u, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	host, _, err := net.SplitHostPort(u.Host)
	require.NoError(t, err)
	proxy, err := sandbox.NewEgressProxy(nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, proxy.Close()) })

	tmp := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmp,
				DefaultModel:      config.DefaultModel{Model: "allowance-sweep-test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 2,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmp}, {ID: "jim", Home: tmp}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &capturingProvider{})
	t.Cleanup(al.Close)
	al.WireTier13Deps(Tier13Deps{EgressProxy: proxy})
	clock := &allowanceTestClock{start: time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)}
	al.SetAllowanceClock(clock.wallNow, clock.elapsedNow)

	store := al.ApprovalGrants()
	end, recorded := store.RecordNetworkGrantFor("mia", []string{host}, time.Hour)
	require.True(t, recorded, "the loop's store takes Mia's timed answer for the named address")
	require.Equal(t, time.Date(2026, time.November, 22, 13, 0, 0, 0, time.UTC), end, "instrument: one hour on the injected clock")
	rig := &sweepRig{
		al: al, proxy: proxy, clock: clock, upstreamURL: upstream.URL, host: host, end: end,
		miaToken: store.SessionEgressToken("mia-running-command", "mia"),
		jimToken: store.SessionEgressToken("jim-running-command", "jim"),
	}
	require.NotEmpty(t, rig.miaToken)
	require.NotEmpty(t, rig.jimToken)
	require.NotEqual(t, rig.miaToken, rig.jimToken)
	proxy.GrantRunHosts(rig.miaToken, store.NetworkHostsFor("mia-running-command", "mia"))
	require.True(t, store.RecordNetworkGrantWithHosts("jim-running-command", "jim", []string{host}), "Jim's own grant, with no end")
	proxy.GrantRunHosts(rig.jimToken, store.NetworkHostsFor("jim-running-command", "jim"))
	status, body := rig.request(t, rig.miaToken)
	require.Equal(t, http.StatusOK, status, "instrument: Mia's token reaches the upstream while her allowance runs")
	require.Equal(t, sweepUpstreamBody, body)
	status, body = rig.request(t, rig.jimToken)
	require.Equal(t, http.StatusOK, status, "instrument: Jim's token reaches the upstream")
	require.Equal(t, sweepUpstreamBody, body)
	return rig
}

// request opens one new connection through the real proxy with token as the run token.
func (r *sweepRig) request(t *testing.T, token string) (int, string) {
	t.Helper()
	proxyURL := &url.URL{Scheme: "http", Host: r.proxy.Addr(), User: url.User(token)}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, requestErr := client.Get(r.upstreamURL + "/sweep")
	require.NoError(t, requestErr, "a transport failure is not a policy refusal")
	body, readErr := io.ReadAll(response.Body)
	require.NoError(t, readErr)
	require.NoError(t, response.Body.Close())
	return response.StatusCode, string(body)
}

func TestTimedAllowance_SweepRevokesLiveEgressToken(t *testing.T) {
	t.Run("D17#9 SweepEndedAllowances revokes exactly the ended agent's token at the end", func(t *testing.T) {
		rig := newSweepRig(t)
		rig.clock.advance(time.Hour - time.Nanosecond)
		rig.al.SweepEndedAllowances()
		status, body := rig.request(t, rig.miaToken)
		require.Equal(t, http.StatusOK, status, "one nanosecond before the end the sweep revokes nothing")
		require.Equal(t, sweepUpstreamBody, body)

		rig.clock.advance(time.Nanosecond)
		rig.al.SweepEndedAllowances()
		status, body = rig.request(t, rig.miaToken)
		assert.Equal(t, http.StatusForbidden, status, "at the end Mia's running command's NEW connection with its exact token is refused")
		assert.NotContains(t, body, sweepUpstreamBody)
		status, body = rig.request(t, rig.jimToken)
		assert.Equal(t, http.StatusOK, status, "Jim's token holds a grant of its own and is not revoked as a substitute")
		assert.Equal(t, sweepUpstreamBody, body)
	})

	t.Run("D17#9 the running expiry sweep ends the token with no explicit call", func(t *testing.T) {
		rig := newSweepRig(t)
		ctx, cancel := context.WithCancel(context.Background())
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			rig.al.RunAllowanceExpirySweep(ctx)
		}()
		t.Cleanup(func() {
			cancel()
			<-stopped
		})
		rig.clock.advance(time.Hour) // the hour passes; nothing below calls the sweep or revokes a token

		// The sweep runs on its own ticker, so the refusal is awaited, polling a real new connection.
		// The deadline is a failure, never a threshold to widen: it is ten times the sweep's
		// one-second tick.
		deadline := time.Now().Add(10 * time.Second)
		var status int
		var body string
		for {
			status, body = rig.request(t, rig.miaToken)
			if status == http.StatusForbidden || time.Now().After(deadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		require.Equal(t, http.StatusForbidden, status,
			"C-26 End: with no one calling the sweep, Mia's token is refused after the hour (the running sweep missed the end)")
		assert.NotContains(t, body, sweepUpstreamBody)
		status, body = rig.request(t, rig.jimToken)
		assert.Equal(t, http.StatusOK, status, "Jim's token is untouched")
		assert.Equal(t, sweepUpstreamBody, body)

		cancel()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Fatal("RunAllowanceExpirySweep did not return after its context ended")
		}
	})
}
