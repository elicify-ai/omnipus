//go:build goolm && stdjson

package security_test

// Order 59 / CHECK F17 and F21: S-121, C-26, D17#1–9/#12–13.
// Plan: use actual grant methods and lookups for every narrow subject; use
// real loopback HTTP/proxy traffic with exactly the token minted by the store.
// A real, still-running shell/curl loop opens a new proxy connection on demand.
// Its manual-revocation control must deny the exact token while an unrelated
// token still works and the original command answers an alive probe.
//
// The control is NOT automatic clock expiry. That missing C-26 sweep/timed
// recording remains a loud BLOCKED obligation, not a returned revoked-token
// list or a fabricated success. Native kernel command confinement is separate,
// UNVERIFIED here, and requires ST-29/ST-30 on each supported platform.
// The current Record* shapes are legacy scope probes, not HTTP timed answers;
// GREEN must reconcile signatures without removing subject/scope assertions.
// Mutations (independent CHECK): widen path/host/family, share another agent's
// grants, fingerprint an ordinary call, revoke the wrong proxy token, kill the
// running command, or omit expiry sweep. GREEN/mutation proof is deferred.

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
	t.Run("D17#9 automatic timed-address expiry", func(t *testing.T) {
		t.Fatal("BLOCKED: C-26 automatic timed-address expiry sweep/clock wiring is not implemented — required by D17#9; manual revocation in the separate control proves the live instrument, not expiry or native kernel acceptance (ST-29/ST-30)")
	})
	t.Run("D17#12 actual timed later-end-wins", func(t *testing.T) {
		t.Fatal("BLOCKED: duration-bearing shell-family recording and controlled monotonic clock are not implemented — required by C-26/D17#12; no callback may return the expected coverage boolean")
	})
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
func testNarrowLiveProxy(t *testing.T) {
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
		proxyURL := &url.URL{Scheme: "http", Host: proxy.Addr()}
		if token != "" {
			proxyURL.User = url.User(token)
		}
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		response, requestErr := client.Get(upstream.URL + "/instrument")
		require.NoError(t, requestErr, "a transport failure is not a policy refusal")
		body, readErr := io.ReadAll(response.Body)
		require.NoError(t, readErr)
		require.NoError(t, response.Body.Close())
		return response.StatusCode, string(body)
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

	command := newNarrowCurlLoop(t, proxy.Addr(), token, upstream.URL)
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
