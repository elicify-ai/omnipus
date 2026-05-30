// Tests for the egress proxy host allow-list and proxy lifecycle.
// Covers (wildcard convention) plus the IDN/punycode
// dataset extension.

package sandbox

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// listenLoopback opens a TCP listener on 127.0.0.1:0 for use in tests
// that need a real upstream server.
func listenLoopback(t *testing.T) (net.Listener, error) {
	t.Helper()
	return net.Listen("tcp", "127.0.0.1:0")
}

// TestEgressWildcard_PrevailingConvention exercises the spec dataset
// "Egress Wildcard" (eight rows including IDN/punycode)
// Each row asserts hostAllowed returns the documented decision.
func TestEgressWildcard_PrevailingConvention(t *testing.T) {
	cases := []struct {
		name      string
		allowList []string
		host      string
		want      bool
	}{
		{"row1 wildcard one leading label", []string{"*.npmjs.org"}, "registry.npmjs.org", true},
		{"row2 wildcard multiple leading labels", []string{"*.npmjs.org"}, "foo.bar.npmjs.org", true},
		{"row3 wildcard rejects apex", []string{"*.npmjs.org"}, "npmjs.org", false},
		{"row4 exact apex", []string{"npmjs.org"}, "npmjs.org", true},
		{"row5 exact does not match subdomain", []string{"npmjs.org"}, "registry.npmjs.org", false},
		{"row6 suffix attack defeated", []string{"*.npmjs.org"}, "attacker.npmjs.org.evil.com", false},
		{"row7 punycode label allowed", []string{"*.npmjs.org"}, "xn--foo-9na.npmjs.org", true},
		{"row8 raw cyrillic rejected (non-ASCII)", []string{"*.npmjs.org"}, "е" + "xample.npmjs.org", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			patterns, err := compileEgressAllowList(tc.allowList)
			if err != nil {
				t.Fatalf("compileEgressAllowList: %v", err)
			}
			p := &EgressProxy{patterns: patterns, allowList: tc.allowList}
			got := p.hostAllowed(tc.host)
			if got != tc.want {
				t.Errorf("hostAllowed(%q) with allowList=%v = %v; want %v",
					tc.host, tc.allowList, got, tc.want)
			}
		})
	}
}

// TestCompileEgressAllowList_Errors covers malformed entries — the
// compiler rejects them at boot rather than producing surprising
// allow/deny behavior.
func TestCompileEgressAllowList_Errors(t *testing.T) {
	cases := []struct {
		name      string
		allowList []string
		wantErr   string
	}{
		{"whitespace", []string{"foo bar"}, "whitespace"},
		{"double-star unsupported", []string{"**.npmjs.org"}, "unsupported '**'"},
		{"bare star", []string{"*"}, "non-leading position"},
		{"non-leading wildcard", []string{"foo.*.org"}, "non-leading"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compileEgressAllowList(tc.allowList)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestEgressProxy_DenyEmitsAudit verifies that a denied request triggers
// the EgressAuditFunc callback with a fully-shaped audit.Entry.
//
// B1.2(c): the proxy now emits a structured *audit.Entry rather than the
// legacy (host, allowList) tuple. event="egress_denied", decision="deny",
// host + allow_list + reason in Details.
func TestEgressProxy_DenyEmitsAudit(t *testing.T) {
	var captured atomic.Value
	auditFn := func(entry *audit.Entry) {
		captured.Store(entry)
	}
	p, err := NewEgressProxy([]string{"registry.npmjs.org"}, auditFn)
	if err != nil {
		t.Fatalf("NewEgressProxy: %v", err)
	}
	defer p.Close()

	// Make an HTTP request through the proxy targeting a denied host.
	proxyURL, _ := url.Parse("http://" + p.Addr())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
		Timeout: 5 * time.Second,
	}
	resp, err := client.Get("http://evil.example.com/")
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 403; body=%s", resp.StatusCode, body)
	}

	// Audit callback should have fired with a structured entry.
	entry, ok := captured.Load().(*audit.Entry)
	if !ok || entry == nil {
		t.Fatalf("captured audit entry not stored; got %v", captured.Load())
	}
	if entry.Event != "egress_denied" {
		t.Errorf("entry.Event = %q, want egress_denied", entry.Event)
	}
	if entry.Decision != audit.DecisionDeny {
		t.Errorf("entry.Decision = %q, want deny", entry.Decision)
	}
	if got, _ := entry.Details["host"].(string); got != "evil.example.com" {
		t.Errorf("entry.Details[host] = %v, want evil.example.com", entry.Details["host"])
	}
	listV, ok := entry.Details["allow_list"].([]string)
	if !ok || len(listV) != 1 || listV[0] != "registry.npmjs.org" {
		t.Errorf("entry.Details[allow_list] = %v, want [registry.npmjs.org]", entry.Details["allow_list"])
	}
	if reason, _ := entry.Details["reason"].(string); reason == "" {
		t.Errorf("entry.Details[reason] should be populated; got empty")
	}
}

// TestEgressProxy_AllowedRequestForwards verifies that an allow-listed
// host gets proxied through to the upstream. We point the proxy at a
// loopback test server (which is what dev environments would do) by
// allow-listing the test host.
func TestEgressProxy_AllowedRequestForwards(t *testing.T) {
	// Build a tiny upstream that returns a known body.
	upstream := &http.Server{
		Addr: "127.0.0.1:0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("upstream-ok"))
		}),
	}
	// We need to start the upstream on an actual port to get its addr.
	listener, err := listenLoopback(t)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = upstream.Serve(listener) }()
	defer upstream.Close()
	upstreamHost := listener.Addr().String() // 127.0.0.1:NNNNN

	// Allow-list "127.0.0.1" exactly.
	p, err := NewEgressProxy([]string{"127.0.0.1"}, nil)
	if err != nil {
		t.Fatalf("NewEgressProxy: %v", err)
	}
	defer p.Close()

	proxyURL, _ := url.Parse("http://" + p.Addr())
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}
	resp, err := client.Get("http://" + upstreamHost + "/")
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "upstream-ok" {
		t.Errorf("body = %q, want upstream-ok", body)
	}
}

// TestEgressProxy_EmptyAllowListDeniesAll confirms the deny-by-default
// posture when the operator has not configured any entries.
func TestEgressProxy_EmptyAllowListDeniesAll(t *testing.T) {
	p, err := NewEgressProxy(nil, nil)
	if err != nil {
		t.Fatalf("NewEgressProxy: %v", err)
	}
	defer p.Close()
	if p.hostAllowed("registry.npmjs.org") {
		t.Errorf("empty allow-list permitted registry.npmjs.org; should deny")
	}
}

// TestClose_WaitsForTunnels verifies that Close blocks until in-flight
// CONNECT-tunnel goroutines drain rather than leaking them past Shutdown.
// Regression test for the WaitGroup tracking added in — without it
// the io.Copy goroutines could outlive Close indefinitely.
func TestClose_WaitsForTunnels(t *testing.T) {
	upstream, err := listenLoopback(t)
	if err != nil {
		t.Fatalf("listenLoopback: %v", err)
	}
	defer upstream.Close()

	releaseUpstream := make(chan struct{})
	upstreamReady := make(chan struct{})
	go func() {
		conn, acceptErr := upstream.Accept()
		if acceptErr != nil {
			return
		}
		close(upstreamReady)
		defer conn.Close()
		<-releaseUpstream
	}()

	host, port, _ := net.SplitHostPort(upstream.Addr().String())
	p, err := NewEgressProxy([]string{host}, nil)
	if err != nil {
		t.Fatalf("NewEgressProxy: %v", err)
	}

	dial, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer dial.Close()
	if _, err := dial.Write(
		[]byte("CONNECT " + host + ":" + port + " HTTP/1.1\r\nHost: " + host + ":" + port + "\r\n\r\n"),
	); err != nil {
		t.Fatalf("CONNECT write: %v", err)
	}
	buf := make([]byte, 64)
	if _, err := dial.Read(buf); err != nil {
		t.Fatalf("CONNECT read: %v", err)
	}
	if !strings.Contains(string(buf), "200") {
		t.Fatalf("CONNECT response %q does not contain 200", string(buf))
	}

	// Wait for upstream to accept so the tunnel goroutines are definitely
	// registered with the WaitGroup before we call Close.
	select {
	case <-upstreamReady:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream never accepted")
	}

	closeReturned := make(chan error, 1)
	go func() { closeReturned <- p.Close() }()

	// Close must NOT return while tunnel goroutines are still running.
	select {
	case <-closeReturned:
		t.Fatal("Close returned before tunnels drained")
	case <-time.After(50 * time.Millisecond):
		// Expected: still waiting.
	}

	// Release upstream so io.Copy goroutines complete and Close returns.
	close(releaseUpstream)
	_ = dial.Close()

	select {
	case err := <-closeReturned:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Close did not return within timeout after tunnels released")
	}
}

// TestNormaliseHost covers port stripping, lower-casing, and trailing-dot
// trimming so the matcher sees a canonical form.
func TestNormaliseHost(t *testing.T) {
	cases := map[string]string{
		"Foo.NPMJS.org":       "foo.npmjs.org",
		"foo.npmjs.org.":      "foo.npmjs.org",
		"foo.npmjs.org:443":   "foo.npmjs.org",
		"  trim.example.com ": "trim.example.com",
		"":                    "",
	}
	for in, want := range cases {
		got := normaliseHost(in)
		if got != want {
			t.Errorf("normaliseHost(%q) = %q, want %q", in, got, want)
		}
	}
}
