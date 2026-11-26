//go:build goolm && stdjson

package security_test

// Order 59 / CHECK F17 and F21: S-121, C-26, D17#1–9/#12–13.
// Plan: use actual grant methods and lookups for every narrow subject; use
// real loopback HTTP/proxy traffic with exactly the token minted by the store.
// A real, still-running shell/curl loop opens a new proxy connection on demand.
//
// Rows 1–8 and 13 record with the no-end Record* methods, which are still the
// production methods behind a no-end answer; the duration-bearing answers are
// pinned in approval_grants_agent_timed_red_test.go.
//
// Part H (2026-11-26) replaced the two placeholders that stood here:
//   - D17#9 records a timed address answer (RecordNetworkGrantFor), moves the
//     store's injected wall and monotonic clock to the end, and asks the
//     production sweep (SweepEndedNetwork) which tokens ended. The returned
//     tokens are handed to the proxy the way the production caller does it; the
//     refusal is observed through the real running sh/curl loop. The sweep
//     must return nothing one nanosecond before the end and exactly Mia's
//     token at it.
//   - D17#12 records 3 hours and then 1 hour through the real methods.
// The manual-revocation control (testNarrowLiveProxy) stays as the instrument
// check: it shows the rig refuses a revoked token and is NEVER presented as
// expiry. The call chain after the sweep (AgentLoop.SweepEndedAllowances, which
// AgentLoop.RunAllowanceExpirySweep runs every second) is pinned in pkg/agent
// (timed_allowance_sweep_red_test.go), not here. That the gateway STARTS
// RunAllowanceExpirySweep is pinned by no test; the header of
// pkg/gateway/timed_allowance_native_lifecycle_red_test.go says why. Native
// kernel command confinement is separate, UNVERIFIED here, and requires
// ST-29/ST-30 on each supported platform.
// Mutations (independent CHECK): widen path/host/family, share another agent's
// grants, fingerprint an ordinary call, revoke the wrong proxy token, kill the
// running command, omit the expiry sweep, shorten a running allowance with a
// later shorter answer. GREEN/mutation proof is deferred.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShellTimedAllowances_NarrowSubjects(t *testing.T) {
	const chat1, chat2, mia, jim = "chat-1-mia", "chat-2-mia", "mia", "jim"
	t.Run("D17#1 exact command follows agent but stays exact", func(t *testing.T) {
		s := security.NewApprovalGrantStore()
		args := map[string]any{"command": "npm run test"}
		require.True(t, s.Record(chat1, mia, "bash", args))
		require.True(t, s.IsAllowed(chat1, mia, "bash", args), "instrument: source grant exists")
		assert.True(t, s.IsAllowed(chat2, mia, "bash", args), "D17#1: same agent in another chat")
		assert.False(t, s.IsAllowed(chat1, mia, "bash", map[string]any{"command": "npm run build"}), "exact command is not another command")
		assert.False(t, s.IsAllowed(chat1, jim, "bash", args), "D17#8: another agent never inherits the answer")
	})
	t.Run("D17#2–3 family follows agent without crossing token or background boundary", func(t *testing.T) {
		s := security.NewApprovalGrantStore()
		g := security.ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test"}
		require.True(t, s.RecordPrefixGrant(chat1, mia, "bash", g))
		require.True(t, s.IsPrefixAllowed(chat1, mia, "bash", g.Binary, []string{"run", "test"}, false), "instrument: real source family covers its own words")
		assert.True(t, s.IsPrefixAllowed(chat2, mia, "bash", g.Binary, []string{"run", "test", "--", "--watch"}, false), "D17#2")
		for _, chat := range []string{chat1, chat2} {
			assert.False(t, s.IsPrefixAllowed(chat, mia, "bash", g.Binary, []string{"run", "testfoo"}, false), "D17#3: token boundary in %s", chat)
			assert.False(t, s.IsPrefixAllowed(chat, mia, "bash", g.Binary, []string{"run", "test"}, true), "foreground never covers background in %s", chat)
			assert.False(t, s.IsPrefixAllowed(chat, jim, "bash", g.Binary, []string{"run", "test"}, false), "D17#8")
		}
	})
	t.Run("D17#4–5 path and access follow only the same agent", func(t *testing.T) {
		s := security.NewApprovalGrantStore()
		want := []fspolicy.PathGrant{{Path: "/home/p/Desktop/out.pdf", Access: fspolicy.PathGrantAccessWrite}}
		require.True(t, s.RecordPathGrant(chat1, mia, want[0]))
		require.Equal(t, want, s.PathGrantsFor(chat1, mia), "instrument: exact source path/access")
		assert.Equal(t, want, s.PathGrantsFor(chat2, mia), "D17#4–5: the exact one-item list, no unrelated path or widened access")
		assert.Empty(t, s.PathGrantsFor(chat2, jim), "D17#8: no other-agent path grant")
	})
	t.Run("D17#6 named addresses follow only the same agent", func(t *testing.T) {
		s := security.NewApprovalGrantStore()
		require.True(t, s.RecordNetworkGrantWithHosts(chat1, mia, []string{"example.com"}))
		require.Equal(t, []string{"example.com"}, s.NetworkHostsFor(chat1, mia), "instrument: real source address")
		assert.Equal(t, []string{"example.com"}, s.NetworkHostsFor(chat2, mia), "D17#6: exactly the named host, never example.org or a wildcard")
		assert.Empty(t, s.NetworkHostsFor(chat2, jim), "D17#8")
	})
	t.Run("D17#7 no-address ports scope follows only the same agent", func(t *testing.T) {
		s := security.NewApprovalGrantStore()
		require.True(t, s.RecordNetworkGrant(chat1, mia))
		require.True(t, s.HasNetworkGrant(chat1, mia), "instrument: actual source network grant")
		assert.True(t, s.HasNetworkGrant(chat2, mia), "D17#7: port-grant lookup is agent scoped; this lookup alone does not certify kernel ports")
		assert.False(t, s.HasNetworkGrant(chat2, jim), "D17#8")
	})
	t.Run("instrument exact-token live proxy and original running command", testNarrowLiveProxy)
	t.Run("D17#9 timed address ends during a running command", testNarrowTimedAddressExpiry)
	t.Run("D17#12 a shorter second answer never shortens a running allowance", testNarrowTimedLaterEndWins)
	t.Run("D17#13 ordinary card covers other arguments for that agent", func(t *testing.T) {
		s := security.NewApprovalGrantStore()
		a, b := map[string]any{"task_id": "t1"}, map[string]any{"task_id": "t2"}
		require.True(t, s.Record(chat1, mia, "delete_task", a))
		require.True(t, s.IsAllowed(chat1, mia, "delete_task", a), "instrument: real source grant")
		assert.True(t, s.IsAllowed(chat1, mia, "delete_task", b), "whole tool, not just argument A")
		assert.True(t, s.IsAllowed(chat2, mia, "delete_task", b), "whole tool in the agent's other chat")
		assert.False(t, s.IsAllowed(chat1, jim, "delete_task", a), "D17#8")
	})
}

const narrowUpstreamBody = "S121-UPSTREAM-9Z"

// No internet, DNS, synthetic verdict or test proxy: requests go through the
// actual production EgressProxy to a real loopback HTTP server.
// narrowLiveRig is the instrument the manual-revocation control and the D17#9
// expiry test share: a real loopback HTTP upstream behind the real production
// EgressProxy. Sharing it is what lets the control vouch for the expiry test:
// the 403 the expiry test reads comes from the very rig that the control shows
// to refuse a revoked token and to serve a granted one.
type narrowLiveRig struct {
	upstreamURL string
	host        string
	proxy       *sandbox.EgressProxy
}

func newNarrowLiveRig(t *testing.T) *narrowLiveRig {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := io.WriteString(w, narrowUpstreamBody)
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
	return &narrowLiveRig{upstreamURL: upstream.URL, host: host, proxy: proxy}
}

// request opens one new connection through the proxy with token as the run
// token and returns the status and body the upstream path answered.
func (r *narrowLiveRig) request(t *testing.T, token string) (int, string) {
	t.Helper()
	proxyURL := &url.URL{Scheme: "http", Host: r.proxy.Addr()}
	if token != "" {
		proxyURL.User = url.User(token)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, requestErr := client.Get(r.upstreamURL + "/instrument")
	require.NoError(t, requestErr, "a transport failure is not a policy refusal")
	body, readErr := io.ReadAll(response.Body)
	require.NoError(t, readErr)
	require.NoError(t, response.Body.Close())
	return response.StatusCode, string(body)
}

func testNarrowLiveProxy(t *testing.T) {
	rig := newNarrowLiveRig(t)
	host, proxy := rig.host, rig.proxy
	store := security.NewApprovalGrantStore()
	require.True(t, store.RecordNetworkGrantWithHosts("live-command-session", "mia", []string{host}))
	token := store.SessionEgressToken("live-command-session", "mia")
	jimToken := store.SessionEgressToken("unrelated-session", "jim")
	require.NotEmpty(t, token, "real store minted the exact running-command token")
	require.NotEmpty(t, jimToken)
	require.NotEqual(t, token, jimToken)
	require.Equal(t, token, store.SessionEgressToken("live-command-session", "mia"), "the issued token is stable for this run")
	request := func(token string) (int, string) {
		t.Helper()
		return rig.request(t, token)
	}
	status, body := request(token)
	require.Equal(t, http.StatusForbidden, status, "instrument: an issued but ungranted token is denied")
	require.NotContains(t, body, narrowUpstreamBody)
	proxy.GrantRunHosts(token, store.NetworkHostsFor("live-command-session", "mia"))
	status, body = request(token)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, narrowUpstreamBody, body)
	status, body = request(jimToken)
	require.Equal(t, http.StatusForbidden, status, "another token never borrows Mia's dynamic grant")
	require.NotContains(t, body, narrowUpstreamBody)
	proxy.GrantRunHosts(jimToken, []string{host}) // Independent grant proves wrong-token revocation cannot pass.
	status, body = request(jimToken)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, narrowUpstreamBody, body)

	command := newNarrowCurlLoop(t, proxy.Addr(), token, rig.upstreamURL)
	status, body = command.request(t, "before")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, narrowUpstreamBody, body)
	proxy.RevokeRunToken(token) // Instrument only. Never presented as automatic expiry.
	status, body = command.request(t, "after-manual-control-revocation")
	require.Equal(t, http.StatusForbidden, status, "the SAME command and EXACT issued token must be refused on its NEW connection")
	require.NotContains(t, body, narrowUpstreamBody)
	command.assertAlive(t) // Same process, no restart or replacement after denial.
	status, body = request(jimToken)
	require.Equal(t, http.StatusOK, status, "the unrelated token is not revoked as a substitute")
	require.Equal(t, narrowUpstreamBody, body)
	t.Log("PROXY_INSTRUMENT: exact-issued-token new-connection=403, unrelated-token=200, original-command=alive")
}

// testNarrowTimedAddressExpiry is Dataset D17 row 9 (S-121; C-26 "Decision time" and "End"): "as row
// 6; the test clock passes the hour during a running `curl` loop that opens a new connection to
// `example.com`". The row expects: "the new connection is refused; the command is not killed", and
// the token is revoked. Row 6 is a network card naming one address; the one-hour length is the
// answer "Allow for 1 hour" this test records.
//
// The named address is the rig's real loopback host where the row says example.com: example.com is
// the row's stand-in for the named address. The clock is the store's own injected wall and
// monotonic input. The sweep is the production SweepEndedNetwork. Its caller half (hand each
// returned token to EgressProxy.RevokeRunToken) is written out below the way
// AgentLoop.SweepEndedAllowances does it; that production caller is pinned in pkg/agent
// (timed_allowance_sweep_red_test.go), and that the gateway starts the sweep is pinned by no test
// (see the header above). The test also asserts the sweep returns NOTHING before
// the end and EXACTLY Mia's token at it, so a sweep that revokes nothing, the wrong token, Jim's
// token as well, or one nanosecond early, fails here.
func testNarrowTimedAddressExpiry(t *testing.T) {
	rig := newNarrowLiveRig(t)
	clock := &allowanceStoreClock{wall: allowanceStoreStart()}
	store := newTimedStoreWithClock(t, clock)
	end, ok := store.RecordNetworkGrantFor("mia", []string{rig.host}, time.Hour)
	require.True(t, ok, "the store takes Mia's timed answer for the named address")
	require.Equal(t, time.Date(2026, time.November, 22, 13, 0, 0, 0, time.UTC), end,
		"C-26 Length: one hour on the server clock, reported in wall-clock time")
	miaToken := store.SessionEgressToken("running-curl-session", "mia")
	jimToken := store.SessionEgressToken("jim-session", "jim")
	require.NotEmpty(t, miaToken, "the store minted the running command's token")
	require.NotEmpty(t, jimToken)
	require.NotEqual(t, miaToken, jimToken)
	// What a bash call does when it starts: it pushes the agent's allowed addresses onto its token.
	rig.proxy.GrantRunHosts(miaToken, store.NetworkHostsFor("running-curl-session", "mia"))
	// Jim holds a grant of his own on his own token, with no end. Only a revocation of the wrong
	// token could end it.
	rig.proxy.GrantRunHosts(jimToken, []string{rig.host})

	command := newNarrowCurlLoop(t, rig.proxy.Addr(), miaToken, rig.upstreamURL)
	status, body := command.request(t, "before-the-end")
	require.Equal(t, http.StatusOK, status, "instrument: the running command reaches the upstream while the allowance runs")
	require.Equal(t, narrowUpstreamBody, body)

	clock.advance(time.Hour - time.Nanosecond)
	require.Empty(t, store.SweepEndedNetwork(), "one nanosecond before the end nothing has ended: no token is named for revocation")
	status, body = command.request(t, "last-nanosecond")
	require.Equal(t, http.StatusOK, status, "the allowance still runs one nanosecond before its end")
	require.Equal(t, narrowUpstreamBody, body)

	clock.advance(time.Nanosecond)
	ended := store.SweepEndedNetwork()
	require.Equal(t, []string{miaToken}, ended, "at the end the sweep names exactly Mia's live token, never Jim's")
	for _, token := range ended {
		rig.proxy.RevokeRunToken(token) // The caller's part, as AgentLoop.SweepEndedAllowances does it.
	}
	status, body = command.request(t, "after-the-end")
	require.Equal(t, http.StatusForbidden, status, "D17#9: the SAME running command's NEW connection with its EXACT token is refused")
	require.NotContains(t, body, narrowUpstreamBody)
	command.assertAlive(t) // Same process: the command was not killed.
	status, body = rig.request(t, jimToken)
	require.Equal(t, http.StatusOK, status, "Jim's token, with a grant of its own, is not revoked as a substitute")
	require.Equal(t, narrowUpstreamBody, body)

	assert.Empty(t, store.NetworkHostsFor("running-curl-session", "mia"), "the ended address is no longer allowed for Mia")
	assert.Empty(t, store.SweepEndedNetwork(), "the end is handled once; a later sweep names nothing, so an answer given after the end is not revoked again")
	// C-26 Decision time: "the agent's next command gets a fresh token with the addresses still
	// allowed". Mia's only address ended, so the next command's token carries no address.
	nextToken := store.SessionEgressToken("next-command-session", "mia")
	require.NotEqual(t, miaToken, nextToken, "another command session gets a token of its own")
	rig.proxy.GrantRunHosts(nextToken, store.NetworkHostsFor("next-command-session", "mia"))
	status, body = rig.request(t, nextToken)
	require.Equal(t, http.StatusForbidden, status, "the next command has no address left to reach")
	require.NotContains(t, body, narrowUpstreamBody)
	t.Log("PROXY_EXPIRY: store clock at the end → sweep named exactly Mia's token → new connection=403, original-command=alive, Jim's token=200")
}

// testNarrowTimedLaterEndWins is Dataset D17 row 12 (S-121): "`delete_task` card A answered 'Allow for 3
// hours', then a second card answered 'Allow for 1 hour'" → "end stays at 3 hours" (C-26 "A second
// answer for the same subject": the later end time wins; an answer never shortens a running
// allowance). The row names the whole-tool kind; the three narrow shell kinds follow the same
// rule and run here too. Expected ends are literals derived from C-26's lengths, never read from
// the store's reply to an earlier call.
func testNarrowTimedLaterEndWins(t *testing.T) {
	start := allowanceStoreStart()
	for _, kind := range []string{"tool", "shell_family", "path", "network_hosts"} {
		t.Run(kind, func(t *testing.T) {
			subject := allowanceStoreSubjectOfKind(t, kind)
			clock := &allowanceStoreClock{wall: start}
			store := newTimedStoreWithClock(t, clock)
			first := recordActualTimedStore(t, store, "mia", subject, "3h")
			require.Equal(t, time.Date(2026, time.November, 22, 15, 0, 0, 0, time.UTC), first, "instrument: the first answer ends at 15:00")
			clock.advance(30 * time.Minute)
			second := recordActualTimedStore(t, store, "mia", subject, "1h")
			assert.Equal(t, time.Date(2026, time.November, 22, 15, 0, 0, 0, time.UTC), second,
				"D17#12: the second answer reports the end that stays, not its own 13:30")
			clock.advance(time.Hour) // 13:30, the end the shorter second answer would have had.
			assertActualStoreSubject(t, store, "chat-2-mia", "mia", subject, true)
			assertActualStoreSubject(t, store, "chat-2-jim", "jim", subject, false) // D17#8: running allowance, still Mia's alone
			clock.advance(90*time.Minute - time.Nanosecond)                         // one nanosecond before 15:00
			assertActualStoreSubject(t, store, "chat-2-mia", "mia", subject, true)
			clock.advance(time.Nanosecond)
			assertActualStoreSubject(t, store, "chat-2-mia", "mia", subject, false)
		})
	}
}

// allowanceStoreSubjectOfKind returns the shared fixture subject of one kind.
func allowanceStoreSubjectOfKind(t *testing.T, kind string) allowanceStoreSubject {
	t.Helper()
	for _, subject := range allowanceStoreSubjects(t.TempDir()) {
		if subject.kind == kind {
			return subject
		}
	}
	t.Fatalf("fixture: no allowance subject of kind %q", kind)
	return allowanceStoreSubject{}
}

type narrowCurlLoop struct {
	input    io.WriteCloser
	output   *bufio.Scanner
	bodyPath string
}

func newNarrowCurlLoop(t *testing.T, proxyAddr, token, target string) *narrowCurlLoop {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Fatal("UNVERIFIED: this shell/curl loop cannot certify Windows; ST-29/ST-30 must exercise the actual supported-platform command path")
	}
	shell, err := exec.LookPath("sh")
	require.NoError(t, err, "fixture: a real shell is required")
	curl, err := exec.LookPath("curl")
	require.NoError(t, err, "fixture: actual curl is required, never a substitute that returns expected status")
	bodyPath := filepath.Join(t.TempDir(), "curl-body")
	proxyURL := &url.URL{Scheme: "http", Host: proxyAddr, User: url.User(token)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	cmd := exec.CommandContext(ctx, shell, "-c", `while IFS= read -r step; do
  if [ "$step" = alive ]; then printf 'ALIVE\n'; else
    "$S121_CURL" --disable --silent --show-error --noproxy '' --proxy "$S121_PROXY" --max-time 5 --output "$S121_BODY" --write-out '%{http_code}\n' "$S121_TARGET/$step" || exit 70
  fi
done`)
	cmd.Env = append(sandbox.ScrubGatewayEnv(), "S121_CURL="+curl, "S121_PROXY="+proxyURL.String(), "S121_BODY="+bodyPath, "S121_TARGET="+target)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	output, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		// Test cleanup: a Close error is inconsequential here. A stdin that did not close
		// leaves the command running, and the deadline branch below reports exactly that.
		if closeErr := input.Close(); closeErr != nil {
			_ = closeErr
		}
		select {
		case waitErr := <-done:
			assert.NoError(t, waitErr, "running command ended unexpectedly: %s", stderr.String())
		case <-ctx.Done():
			cancel()
			// The command is killed at the deadline, so its Wait error is expected; the
			// failure names it instead of dropping it.
			t.Errorf("fixture: running command did not exit after stdin closed (killed at the deadline: %v)", <-done)
		}
		cancel()
	})
	return &narrowCurlLoop{input: input, output: bufio.NewScanner(output), bodyPath: bodyPath}
}

func (c *narrowCurlLoop) request(t *testing.T, step string) (int, string) {
	t.Helper()
	_, err := fmt.Fprintln(c.input, step)
	require.NoError(t, err)
	require.True(t, c.output.Scan(), "real running command must answer; timeout/exit is not a denied connection")
	var status int
	count, err := fmt.Sscanf(c.output.Text(), "%d", &status)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	body, err := os.ReadFile(c.bodyPath)
	require.NoError(t, err)
	return status, string(body)
}

func (c *narrowCurlLoop) assertAlive(t *testing.T) {
	t.Helper()
	_, err := fmt.Fprintln(c.input, "alive")
	require.NoError(t, err)
	require.True(t, c.output.Scan(), "a killed command cannot answer an alive probe")
	require.Equal(t, "ALIVE", c.output.Text())
}
