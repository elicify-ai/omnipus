// Package sandbox — egress proxy for Tier 2 (build_static) and Tier 3
// (web_serve dev mode) child processes.
//
// Threat model:
//
// - Children from Tier 2 / Tier 3 are documented as trusted-prompt
// features. The proxy is one of several controls (alongside per-agent
// workspace + npm cache) that limit damage from a hostile build script.
// - The proxy enforces an operator-controlled allow-list (cfg.Sandbox.
// EgressAllowList) on the HTTP/HTTPS host. Hosts not on the list get
// a 403 with a structured audit log entry (path.network_denied).
// - Wildcard convention follows the prevailing one: "*.x"
// matches one-or-more leading labels (e.g. "*.npmjs.org" matches both
// "registry.npmjs.org" and "foo.bar.npmjs.org") but NOT the apex
// ("npmjs.org" — write that as a separate exact entry if needed).
// - IDN: hostnames are matched as-is. Punycode forms (xn--...) match
// by their literal labels; non-ASCII forms reach this layer post-
// normalisation by Go's URL parser. Bidi attacks are defeated by
// case-folding to ASCII before comparison and rejecting any non-
// printable bytes in the host.
//
// Limitations (acknowledged in spec):
// - HTTP/HTTPS only. Raw TCP connect bypasses the proxy entirely.
// Operators are warned in the env preamble.
// - The proxy CONNECT method blesses the upstream TLS connection. We do
// not MITM TLS — the client tunnels through us. Allow-list is checked
// on the CONNECT host; subsequent bytes are forwarded opaquely.

package sandbox

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// EgressAuditFunc is invoked for every denied egress request, and for any
// upstream-error condition that the proxy detects, so the caller (gateway)
// can write a structured audit entry. Passing nil disables audit (used in
// tests). Implementations should be non-blocking — the proxy's
// request-handling goroutine waits on this call.
//
// B1.2(c): the function now receives a fully-populated *audit.Entry rather
// than (host, allowList) so the proxy can emit canonical event names and
// decisions (e.g. event="egress_denied", decision="deny") without each
// call site reshaping the audit row. The host, allow-list, port, and
// reason all live in entry.Details. The agent_id is left empty here
// because the proxy does not know which agent originated a given child-
// process request — the caller's closure can fill that in if it has the
// information; B1.2(c) leaves it blank by design.
type EgressAuditFunc func(*audit.Entry)

// EgressProxy is a loopback HTTP/HTTPS forward proxy with host allow-listing.
// One instance is created per gateway; child processes target it via
// HTTP_PROXY/HTTPS_PROXY env vars (set by hardened_exec.Run).
type EgressProxy struct {
	allowList []string
	patterns  []hostPattern
	listener  net.Listener
	server    *http.Server
	audit     EgressAuditFunc
	addr      string

	// tunnels tracks active CONNECT-tunnel goroutines so Close can
	// wait for them to drain rather than leaking them past Shutdown.
	tunnels sync.WaitGroup

	closeOnce sync.Once
	closed    bool
	mu        sync.Mutex
}

// hostPattern is the precompiled form of an allow-list entry. Two kinds:
// exact match (no leading "*.") and suffix match (leading "*."). Exact
// patterns hit the literal hostname only; suffix patterns require at
// least one extra label before the suffix, matching the prevailing
// "*.x" convention.
type hostPattern struct {
	suffix bool   // true → "*.x" pattern; false → exact match
	host   string // exact host or the part after "*."
}

// NewEgressProxy creates and starts a loopback HTTP/HTTPS proxy. The
// returned proxy listens on 127.0.0.1:<random-port>; the address is
// available via Addr. Callers MUST call Close on shutdown to release
// the listener and stop the goroutine.
//
// allowList: cfg.Sandbox.EgressAllowList. Empty list = deny all (every
// request is rejected). audit may be nil.
func NewEgressProxy(allowList []string, audit EgressAuditFunc) (*EgressProxy, error) {
	patterns, err := compileEgressAllowList(allowList)
	if err != nil {
		return nil, err
	}
	// Bind to 127.0.0.1:0 — the kernel assigns a free port. Loopback-only
	// so the proxy is unreachable from outside the host.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("egress_proxy: listen: %w", err)
	}
	p := &EgressProxy{
		allowList: append([]string(nil), allowList...),
		patterns:  patterns,
		listener:  listener,
		audit:     audit,
		addr:      listener.Addr().String(),
	}
	p.server = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		if err := p.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Warn("egress_proxy: serve exited with error", "error", err)
		}
	}()

	return p, nil
}

// Addr returns the loopback address the proxy is bound to (e.g.
// "127.0.0.1:54321"). Suitable for assignment to Limits.EgressProxyAddr.
func (p *EgressProxy) Addr() string {
	return p.addr
}

// AllowList returns a copy of the operator-supplied allow-list. Used by
// callers (e.g. audit emission) so the slice is not aliased.
func (p *EgressProxy) AllowList() []string {
	out := make([]string, len(p.allowList))
	copy(out, p.allowList)
	return out
}

// Close shuts down the proxy. Safe to call multiple times. After Shutdown
// returns the HTTP server is no longer accepting requests, but in-flight
// CONNECT tunnels (which were hijacked out of HTTP) keep running until both
// directions close. We wait up to 5 s for those tunnels to drain naturally;
// if the deadline fires we log and return — there is no safe way to abort
// a mid-read io.Copy without race-prone connection closes.
func (p *EgressProxy) Close() error {
	var err error
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = p.server.Shutdown(ctx)

		done := make(chan struct{})
		go func() {
			p.tunnels.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			slog.Warn("egress_proxy: CONNECT tunnels still active after Close timeout")
		}
	})
	return err
}

// ServeHTTP dispatches between CONNECT (HTTPS tunneling) and plain HTTP
// proxy. The two have different bodies because plain HTTP carries the
// full URL on the request line whereas CONNECT carries only host:port.
func (p *EgressProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

// handleHTTP forwards a plain (non-TLS) HTTP request. The host is in
// r.URL.Host; we extract it, check the allow-list, and reverse-proxy the
// request via httputil.ReverseProxy.
func (p *EgressProxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	host := normaliseHost(r.URL.Host)
	if host == "" {
		http.Error(w, "egress_proxy: missing host", http.StatusBadRequest)
		return
	}
	if !p.hostAllowed(host) {
		p.deny(w, host)
		return
	}

	// Build the upstream URL. r.URL.Scheme may be empty when the proxy
	// receives a relative URL; default to http for plain proxy mode.
	scheme := r.URL.Scheme
	if scheme == "" {
		scheme = "http"
	}
	target := &url.URL{Scheme: scheme, Host: r.URL.Host}

	rp := httputil.NewSingleHostReverseProxy(target)
	// Default Director sets r.URL.Host = target.Host but preserves the
	// path; that's correct for proxy mode.
	//
	// HIGH-2 (silent-failure-hunter): the upstream error message can leak
	// resolved-IP information, TLS handshake details (e.g. certificate
	// expiration timestamps), and DNS-lookup metadata to the agent's
	// child process. Operators see the full error in the gateway log;
	// the child sees only a generic "upstream unavailable" with status
	// 502. We also surface the upstream-error event to audit so
	// operators can correlate with proxy denials and reachability issues.
	rp.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		slog.Warn("egress_proxy: upstream error",
			"host", host, "error", err)
		if p.audit != nil {
			// B1.2(c): emit a distinct event so audit consumers can
			// distinguish "denied by allow-list" from "allowed but
			// upstream unreachable". decision="error" matches the
			// audit.DecisionError convention. The underlying error string
			// stays in Details where it's redactable; we don't echo it
			// back to the child (HIGH-2 silent-failure-hunter — see the
			// comment above the rp.ErrorHandler assignment).
			p.audit(&audit.Entry{
				Event:    "egress_upstream_error",
				Decision: audit.DecisionError,
				Details: map[string]any{
					"host":   host,
					"error":  err.Error(),
					"phase":  "http_proxy",
					"method": req.Method,
				},
			})
		}
		http.Error(rw, "egress_proxy: upstream unavailable", http.StatusBadGateway)
	}
	rp.ServeHTTP(w, r)
}

// handleConnect implements HTTP CONNECT for HTTPS tunneling. The client
// (npm/node) sends "CONNECT host:port HTTP/1.1"; we check the host
// against the allow-list and, on success, hijack the connection and
// pipe bytes between client and upstream.
func (p *EgressProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	hostPort := r.URL.Host
	if hostPort == "" {
		hostPort = r.Host
	}
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort // No port — accept as-is.
	}
	host = normaliseHost(host)
	if host == "" {
		http.Error(w, "egress_proxy: missing host", http.StatusBadRequest)
		return
	}
	if !p.hostAllowed(host) {
		p.deny(w, host)
		return
	}

	// Dial upstream first, BEFORE hijacking, so failures can be
	// reported as a normal HTTP response.
	//
	// HIGH-2: as with handleHTTP, we do NOT echo the dial error back to
	// the child (it can leak DNS / IP / firewall topology info). Operators
	// see the full error in gateway logs. The audit hook is reused with a
	// distinguishing prefix so the gateway can emit a structured
	// upstream-error audit entry.
	upstream, err := net.DialTimeout("tcp", hostPort, 10*time.Second)
	if err != nil {
		slog.Warn("egress_proxy: connect dial failed",
			"host", host, "error", err)
		if p.audit != nil {
			// B1.2(c): structured upstream-error audit for the CONNECT
			// (HTTPS tunnel) path. Same shape as the plain-HTTP branch
			// above so audit consumers can union the two by event name.
			p.audit(&audit.Entry{
				Event:    "egress_upstream_error",
				Decision: audit.DecisionError,
				Details: map[string]any{
					"host":   host,
					"error":  err.Error(),
					"phase":  "connect_dial",
					"method": http.MethodConnect,
				},
			})
		}
		http.Error(w, "egress_proxy: upstream unavailable", http.StatusBadGateway)
		return
	}

	// Hijack the client's connection. From here on we're outside HTTP.
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "egress_proxy: hijacking unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		_ = upstream.Close()
		slog.Warn("egress_proxy: hijack failed", "error", err)
		return
	}

	// Register the tunnel goroutines BEFORE writing the 200 response so the
	// client never observes a connection-established without the WaitGroup
	// having been incremented. Without this, a Close() that races between
	// the 200 write and Add(2) reads tunnels==0, returns immediately, and
	// orphans the goroutines that get spawned just after — exactly the
	// regression TestClose_WaitsForTunnels guards against.
	p.tunnels.Add(2)

	// Tell the client the tunnel is open. From this point on bytes flow
	// in both directions until either side closes.
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		p.tunnels.Done()
		p.tunnels.Done()
		_ = clientConn.Close()
		_ = upstream.Close()
		return
	}

	// Pipe bytes in both directions. Each direction runs in its own
	// goroutine; either side closing terminates both. The WaitGroup lets
	// Close drain in-flight tunnels rather than leaking them.
	go func() {
		defer p.tunnels.Done()
		_, _ = io.Copy(upstream, clientConn)
		_ = upstream.Close()
		_ = clientConn.Close()
	}()
	go func() {
		defer p.tunnels.Done()
		_, _ = io.Copy(clientConn, upstream)
		_ = upstream.Close()
		_ = clientConn.Close()
	}()
}

// deny writes a 403 to the client and emits the audit entry.
//
// B1.2(c): the audit closure receives a fully-shaped audit.Entry with
// event="egress_denied", decision="deny", and Details = {host, allow_list,
// reason}. Falls back to slog.Warn when no closure is wired so denials are
// always visible in logs even in tests/development.
func (p *EgressProxy) deny(w http.ResponseWriter, host string) {
	slog.Info("egress_proxy: denied", "host", host, "allow_list", p.allowList)
	if p.audit != nil {
		p.audit(&audit.Entry{
			Event:    "egress_denied",
			Decision: audit.DecisionDeny,
			Details: map[string]any{
				"host":       host,
				"allow_list": p.AllowList(),
				"reason":     "host not in egress allow-list",
			},
		})
	} else {
		slog.Warn("egress_proxy: deny without audit hook (audit logger unavailable)",
			"host", host, "allow_list", p.allowList)
	}
	http.Error(w, fmt.Sprintf("egress_proxy: host %q not in allow-list", host), http.StatusForbidden)
}

// hostAllowed reports whether host matches any compiled pattern.
//
// Algorithm:
// - Reject empty hosts and hosts with non-printable bytes (defense-in-
// depth against bidi-style attacks; the URL parser usually catches
// these earlier, but we belt-and-brace at the policy edge).
// - For each pattern, exact patterns require host == pattern.host.
// Suffix patterns require host to END WITH "." + pattern.host AND
// to have at least one label before the suffix.
func (p *EgressProxy) hostAllowed(host string) bool {
	if host == "" {
		return false
	}
	// Reject any non-printable / non-ASCII byte. Allow-list entries are
	// ASCII (operators write them as text); a non-ASCII host can only
	// arrive via IDN normalisation, and Go's net/url parser converts
	// IDN to punycode before this layer. If we still see non-ASCII, it
	// is suspicious and we fail closed.
	for i := 0; i < len(host); i++ {
		b := host[i]
		if b < 0x21 || b > 0x7E {
			return false
		}
	}

	for _, pat := range p.patterns {
		if pat.suffix {
			// "*.x" matches "y.x", "y.z.x" but not "x" itself, nor
			// "yx.evil.com" (which has "x" only as a substring).
			if matchSuffixWildcard(host, pat.host) {
				return true
			}
			continue
		}
		// Exact match.
		if host == pat.host {
			return true
		}
	}
	return false
}

// matchSuffixWildcard reports whether host is "<one-or-more-labels>.suffix".
// host and suffix are already lower-cased.
//
// Examples (suffix = "npmjs.org"):
// - "registry.npmjs.org" → true (one leading label)
// - "foo.bar.npmjs.org" → true (multiple leading labels — )
// - "npmjs.org" → false (no leading label)
// - "attacker.npmjs.org.evil.com" → false (suffix not at end)
// - "fakenpmjs.org" → false (no dot before suffix)
func matchSuffixWildcard(host, suffix string) bool {
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	// The character immediately before the suffix must be a dot, and
	// there must be at least one byte before that dot.
	idx := len(host) - len(suffix)
	if idx < 2 { // need at least "x." before the suffix
		return false
	}
	if host[idx-1] != '.' {
		return false
	}
	return true
}

// normaliseHost trims any port, lower-cases the host, and trims a single
// trailing dot (FQDN form). Returns empty when input is empty.
func normaliseHost(host string) string {
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(host, ".")
	return host
}

// compileEgressAllowList validates and compiles the operator's allow-list.
// Errors are returned for entries that contain spaces or wildcards in the
// wrong position. An empty input list compiles to no patterns (deny all).
func compileEgressAllowList(allowList []string) ([]hostPattern, error) {
	patterns := make([]hostPattern, 0, len(allowList))
	for _, raw := range allowList {
		entry := strings.TrimSpace(strings.ToLower(raw))
		if entry == "" {
			continue
		}
		if strings.ContainsAny(entry, " \t\n\r") {
			return nil, fmt.Errorf("egress_proxy: allow-list entry %q contains whitespace", raw)
		}
		// "**" is no longer supported ( dropped it in favor of
		// the prevailing "*.x" convention). Reject any "**" early so
		// operators don't get surprising silent matches.
		if strings.Contains(entry, "**") {
			return nil, fmt.Errorf(
				"egress_proxy: allow-list entry %q uses unsupported '**' wildcard (use '*.x' for one-or-more leading labels)",
				raw,
			)
		}
		if strings.HasPrefix(entry, "*.") {
			suffix := strings.TrimPrefix(entry, "*.")
			if suffix == "" || strings.Contains(suffix, "*") {
				return nil, fmt.Errorf("egress_proxy: allow-list entry %q is malformed", raw)
			}
			patterns = append(patterns, hostPattern{suffix: true, host: suffix})
			continue
		}
		if strings.Contains(entry, "*") {
			return nil, fmt.Errorf(
				"egress_proxy: allow-list entry %q has '*' in non-leading position (only '*.x' is supported)",
				raw,
			)
		}
		patterns = append(patterns, hostPattern{suffix: false, host: entry})
	}
	return patterns, nil
}
