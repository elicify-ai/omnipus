// egress_runner_test.go — tests for the external-runner egress proxy
// (NewRunnerEgressProxy) and its SSRF internal-CIDR blocking (Spec-4 FR-5.3).

package sandbox

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestRunnerEgressProxy_AllowAllExternalEmptyList verifies that a runner proxy
// with no host allow-list permits any external host at the host-allow layer
// (the "allow all external when no list is configured" branch). SSRF remains
// the gate and is exercised by the loopback-block tests below.
func TestRunnerEgressProxy_AllowAllExternalEmptyList(t *testing.T) {
	p, err := NewRunnerEgressProxy(nil, nil)
	if err != nil {
		t.Fatalf("NewRunnerEgressProxy: %v", err)
	}
	// Test cleanup: Close errors on proxies/servers/conns/response
	// bodies are inconsequential once assertions have run.
	defer func() {
		if err := p.Close(); err != nil {
			_ = err
		}
	}()

	if !p.SSRFEnabled() {
		t.Error("runner proxy should have SSRF enabled")
	}
	if !p.allowAll {
		t.Error("runner proxy with empty allow-list should set allowAll=true")
	}
	if !p.hostAllowed("api.anthropic.com") {
		t.Error("allowAll runner proxy denied api.anthropic.com at host layer")
	}
	if !p.hostAllowed("registry.npmjs.org") {
		t.Error("allowAll runner proxy denied registry.npmjs.org at host layer")
	}
}

// TestRunnerEgressProxy_NonEmptyAllowListEnforced verifies that a runner proxy
// WITH a host allow-list enforces it (allowAll=false), on top of SSRF.
func TestRunnerEgressProxy_NonEmptyAllowListEnforced(t *testing.T) {
	p, err := NewRunnerEgressProxy([]string{"api.anthropic.com"}, nil)
	if err != nil {
		t.Fatalf("NewRunnerEgressProxy: %v", err)
	}
	defer func() {
		if err := p.Close(); err != nil {
			_ = err
		}
	}()
	if p.allowAll {
		t.Error("runner proxy with non-empty allow-list should set allowAll=false")
	}
	if !p.hostAllowed("api.anthropic.com") {
		t.Error("allow-listed host should be permitted")
	}
	if p.hostAllowed("evil.example.com") {
		t.Error("non-allow-listed host should be denied")
	}
}

// TestRunnerEgressProxy_SSRFBlocksLoopbackHTTP verifies that the SSRF filter
// blocks a plain-HTTP request to a loopback (internal) address even though the
// host layer allows it (allowAll). The request must NOT reach the upstream.
func TestRunnerEgressProxy_SSRFBlocksLoopbackHTTP(t *testing.T) {
	reached := make(chan struct{}, 1)
	upstream := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			select {
			case reached <- struct{}{}:
			default:
			}
			if _, err := w.Write([]byte("should-not-reach")); err != nil {
				_ = err
			}
		}),
	}
	listener, err := listenLoopback(t)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = upstream.Serve(listener) }()
	defer func() {
		if err := upstream.Close(); err != nil {
			_ = err
		}
	}()
	upstreamHost := listener.Addr().String() // 127.0.0.1:NNNNN

	p, err := NewRunnerEgressProxy(nil, nil) // allowAll + SSRF
	if err != nil {
		t.Fatalf("NewRunnerEgressProxy: %v", err)
	}
	defer func() {
		if err := p.Close(); err != nil {
			_ = err
		}
	}()

	proxyURL, _ := url.Parse("http://" + p.Addr())
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}
	resp, err := client.Get("http://" + upstreamHost + "/")
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			_ = err
		}
	}()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (SSRF block of loopback)", resp.StatusCode)
	}
	select {
	case <-reached:
		t.Fatal("upstream was reached despite SSRF block on loopback")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestRunnerEgressProxy_SSRFBlocksLoopbackCONNECT verifies the HTTPS (CONNECT)
// path is also SSRF-filtered: a CONNECT to a loopback host:port is denied with
// 403 and the upstream is never dialed.
func TestRunnerEgressProxy_SSRFBlocksLoopbackCONNECT(t *testing.T) {
	upstream, err := listenLoopback(t)
	if err != nil {
		t.Fatalf("listenLoopback: %v", err)
	}
	defer func() {
		if err := upstream.Close(); err != nil {
			_ = err
		}
	}()
	reached := make(chan struct{}, 1)
	go func() {
		conn, acceptErr := upstream.Accept()
		if acceptErr != nil {
			return
		}
		select {
		case reached <- struct{}{}:
		default:
		}
		// Test goroutine cleanup: Close error not actionable here.
		if err := conn.Close(); err != nil {
			_ = err
		}
	}()

	p, err := NewRunnerEgressProxy(nil, nil)
	if err != nil {
		t.Fatalf("NewRunnerEgressProxy: %v", err)
	}
	defer func() {
		if err := p.Close(); err != nil {
			_ = err
		}
	}()

	dial, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer func() {
		if err := dial.Close(); err != nil {
			_ = err
		}
	}()
	req := "CONNECT " + upstream.Addr().String() + " HTTP/1.1\r\nHost: " + upstream.Addr().String() + "\r\n\r\n"
	if _, err := dial.Write([]byte(req)); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	buf := make([]byte, 1024)
	n, _ := dial.Read(buf)
	statusLine := string(buf[:n])
	if !strings.Contains(statusLine, "403") {
		t.Errorf("CONNECT response = %q, want 403 (SSRF block)", firstLine(statusLine))
	}
	select {
	case <-reached:
		t.Fatal("upstream was dialed despite SSRF block on CONNECT")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestRunnerEgressProxy_SSRFAuditHook verifies the SSRF deny emits an audit
// entry via the wired hook (event="egress_ssrf_blocked").
func TestRunnerEgressProxy_SSRFAuditHook(t *testing.T) {
	auditCalls := make(chan string, 4)
	p, err := NewRunnerEgressProxy(nil, func(e *audit.Entry) {
		if e != nil {
			auditCalls <- e.Event
		}
	})
	if err != nil {
		t.Fatalf("NewRunnerEgressProxy: %v", err)
	}
	defer func() {
		if err := p.Close(); err != nil {
			_ = err
		}
	}()

	listener, err := listenLoopback(t)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			_ = err
		}
	}()
	proxyURL, _ := url.Parse("http://" + p.Addr())
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   3 * time.Second,
	}
	resp, err := client.Get("http://" + listener.Addr().String() + "/")
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			_ = err
		}
	}()
	if _, err := io.ReadAll(resp.Body); err != nil {
		_ = err
	}
	select {
	case ev := <-auditCalls:
		if ev != "egress_ssrf_blocked" {
			t.Errorf("audit event = %q, want egress_ssrf_blocked", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no audit entry emitted for SSRF block")
	}
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
