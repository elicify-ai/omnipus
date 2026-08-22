// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// ExecProxy is a loopback HTTP proxy that enforces SSRF rules on child processes
// spawned by the exec tool (US-14 / SEC-28).
//
// Construction via NewExecProxy does NOT start the proxy. Call Start() to bind
// and listen, Stop() to shut down. The proxy auto-stops after IdleTimeout
// when no exec commands are active (FR-028).
type ExecProxy struct {
	ssrf        *SSRFChecker
	listener    net.Listener
	server      *http.Server
	addr        string
	mu          sync.Mutex
	running     atomic.Bool
	cmdCount    atomic.Int32
	idleTimeout time.Duration
	idleStop    chan struct{} // closed to cancel idle watcher
}

// DefaultIdleTimeout is the duration after which the proxy auto-stops when no
// exec commands are active (FR-028).
const DefaultIdleTimeout = 30 * time.Second

// NewExecProxy creates an ExecProxy. ssrfChecker is required; auditLogger is
// reserved for future use (pass nil).
func NewExecProxy(ssrfChecker *SSRFChecker, auditLogger any) *ExecProxy {
	return &ExecProxy{
		ssrf:        ssrfChecker,
		idleTimeout: DefaultIdleTimeout,
	}
}

// SetIdleTimeout configures the idle timeout for auto-stop. Zero disables auto-stop.
func (p *ExecProxy) SetIdleTimeout(d time.Duration) {
	p.idleTimeout = d
}

// Start binds the proxy to a random loopback port and begins serving.
func (p *ExecProxy) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("execproxy: bind loopback: %w", err)
	}

	p.mu.Lock()
	p.listener = ln
	p.addr = ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/", p.handleProxy)
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	p.server = srv
	p.running.Store(true)
	p.mu.Unlock()

	// Capture `srv` by value into the goroutine so a concurrent Stop()
	// that sets p.server = nil does not cause a nil-pointer dereference
	// inside http.Server.Serve. The Server.Close() call in Stop() will
	// unblock Serve() with http.ErrServerClosed regardless of whether
	// p.server is still set on the parent struct.
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Warn("execproxy: server exited", "error", err)
		}
	}()

	slog.Info("execproxy: started", "addr", p.addr)
	return nil
}

// Stop shuts down the proxy.
func (p *ExecProxy) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.server != nil {
		if closeErr := p.server.Close(); closeErr != nil {
			_ = closeErr
		}
		p.server = nil
	}
	p.running.Store(false)
	// Cancel any pending idle watcher
	if p.idleStop != nil {
		select {
		case <-p.idleStop:
		default:
			close(p.idleStop)
		}
	}
	slog.Info("execproxy: stopped")
}

// Addr returns the proxy address in "127.0.0.1:PORT" form.
func (p *ExecProxy) Addr() string {
	return p.addr
}

// PrepareCmd sets HTTP_PROXY and HTTPS_PROXY env vars on the command so child
// process traffic is routed through this proxy. Also increments the active
// command counter and cancels any pending idle shutdown.
//
// If the proxy was stopped by the idle watcher since the last PrepareCmd call,
// it is transparently restarted so subsequent exec commands do not silently
// bypass SSRF protection. Restart failures leave the counter un-incremented
// and the cmd env unchanged — the caller's nil-check on Addr() or on the
// returned cmd.Env should detect that the proxy is unavailable.
func (p *ExecProxy) PrepareCmd(cmd *exec.Cmd) {
	// Restart if the idle watcher stopped us since the last use. Without this,
	// once the 30s idle timeout fires, every subsequent exec command runs
	// without SSRF protection with no visible signal.
	if !p.running.Load() {
		if err := p.Start(); err != nil {
			slog.Warn("execproxy: restart after idle stop failed — exec command will run without proxy",
				"error", err)
			return
		}
	}

	// Cancel any pending idle watcher
	p.mu.Lock()
	if p.idleStop != nil {
		select {
		case <-p.idleStop:
			// already closed
		default:
			close(p.idleStop)
		}
		p.idleStop = make(chan struct{})
	}
	p.mu.Unlock()
	p.cmdCount.Add(1)
	proxyURL := "http://" + p.addr

	// Inherit current environment if not already set
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env,
		"HTTP_PROXY="+proxyURL,
		"HTTPS_PROXY="+proxyURL,
		"http_proxy="+proxyURL,
		"https_proxy="+proxyURL,
	)
}

// CmdDone decrements the active command counter. If the counter reaches 0 and
// an idle timeout is configured, a background goroutine will stop the proxy
// after the timeout unless a new command starts (FR-028).
func (p *ExecProxy) CmdDone() {
	count := p.cmdCount.Add(-1)
	if count <= 0 && p.idleTimeout > 0 && p.running.Load() {
		go p.watchIdle()
	}
}

// watchIdle waits for idleTimeout then stops the proxy if still idle.
func (p *ExecProxy) watchIdle() {
	timer := time.NewTimer(p.idleTimeout)
	defer timer.Stop()

	select {
	case <-timer.C:
		if p.cmdCount.Load() <= 0 && p.running.Load() {
			slog.Info("execproxy: idle timeout reached, auto-stopping")
			p.Stop()
		}
	case <-p.idleStopChan():
		// New command started or proxy manually stopped; cancel idle watch
	}
}

func (p *ExecProxy) idleStopChan() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idleStop == nil {
		p.idleStop = make(chan struct{})
	}
	return p.idleStop
}

// handleProxy dispatches HTTP or HTTPS CONNECT requests.
func (p *ExecProxy) handleProxy(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	hostOnly, _, err := net.SplitHostPort(host)
	if err != nil {
		hostOnly = host
	}

	// CheckHost handles both raw IPs and hostnames with DNS resolution
	if _, err := p.ssrf.CheckHost(r.Context(), hostOnly); err != nil {
		// #nosec G706 -- hostOnly/err.Error() are passed as discrete slog
		// key/value fields, never concatenated into the message string. Both
		// log sinks (pkg/logger's JSON file writer and zerolog ConsoleWriter's
		// needsQuote/strconv.Quote path for values containing control bytes)
		// escape embedded newlines, so a crafted Host header cannot forge a
		// fake log line in either the persisted audit trail or the terminal.
		slog.Warn("execproxy: SSRF blocked", "host", hostOnly, "reason", err.Error())
		http.Error(w, "SSRF: "+err.Error(), http.StatusForbidden)
		return
	}

	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

func (p *ExecProxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	r.RequestURI = ""
	// #nosec G704 -- constructing outReq here is not the dispatch point: the
	// host it targets is already gated by p.ssrf.CheckHost in handleProxy
	// (the sole caller of handleHTTP) before we ever reach this line, and the
	// actual network dispatch below goes through SafeClient(), an SSRF-safe
	// client that re-validates the resolved IP at dial time (defends against
	// DNS-rebinding between the check and the dial). That call, not this
	// one, is the real enforcement point; this constructor call is inert
	// until then.
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	outReq.Header = r.Header.Clone()

	// #nosec G704 -- this is the actual dispatch: outReq's host was gated by
	// p.ssrf.CheckHost in handleProxy (the sole caller of handleHTTP) before
	// we ever got here, and SafeClient() is itself an SSRF-safe client that
	// re-validates the resolved IP at dial time (defends against
	// DNS-rebinding between that check and this dial). This is the
	// enforcement point gosec's taint analysis is (correctly) flagging as
	// the sink; it is not a bypass of it.
	resp, err := p.ssrf.SafeClient().Do(outReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// Body is fully streamed to the client via io.Copy below; a Close
	// error on an already-drained response body is not actionable here.
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		_ = err
	}
}

func (p *ExecProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	// Split host and port from the CONNECT target.
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		http.Error(w, "bad CONNECT target: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Re-validate and resolve here (not just in handleProxy) so we can dial
	// using the pre-resolved IP. This prevents TOCTOU DNS rebinding: an attacker
	// cannot swap the DNS answer between our SSRF check and the actual dial.
	addrs, err := p.ssrf.CheckHost(r.Context(), host)
	if err != nil {
		// #nosec G706 -- see the identical justification on the handleProxy
		// slog.Warn above: structured k/v fields, escaped at both log sinks.
		slog.Warn("execproxy: SSRF blocked in CONNECT", "host", host, "reason", err.Error())
		http.Error(w, "SSRF: "+err.Error(), http.StatusForbidden)
		return
	}

	// Dial the first safe pre-resolved IP directly (bypass DNS entirely).
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	var targetConn net.Conn
	for _, addr := range addrs {
		conn, dialErr := dialer.DialContext(r.Context(), "tcp", net.JoinHostPort(addr.IP.String(), port))
		if dialErr == nil {
			targetConn = conn
			break
		}
	}
	if targetConn == nil {
		http.Error(w, "connect failed", http.StatusBadGateway)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		// Abort path, no data in flight yet — Close error not actionable.
		if closeErr := targetConn.Close(); closeErr != nil {
			_ = closeErr
		}
		return
	}

	w.WriteHeader(http.StatusOK)
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		// Abort path, no data in flight yet — Close error not actionable.
		if closeErr := targetConn.Close(); closeErr != nil {
			_ = closeErr
		}
		return
	}

	// Raw bidirectional TCP relay: io.Copy's own error is already dropped
	// above by design (connection resets from either peer are the normal
	// way a CONNECT tunnel ends, not a fault to surface), so the Close
	// errors that tear the relay down afterward carry no extra information.
	go func() {
		defer func() {
			if closeErr := targetConn.Close(); closeErr != nil {
				_ = closeErr
			}
		}()
		defer func() {
			if closeErr := clientConn.Close(); closeErr != nil {
				_ = closeErr
			}
		}()
		if _, err := io.Copy(targetConn, clientConn); err != nil {
			_ = err
		}
	}()
	go func() {
		defer func() {
			if closeErr := targetConn.Close(); closeErr != nil {
				_ = closeErr
			}
		}()
		defer func() {
			if closeErr := clientConn.Close(); closeErr != nil {
				_ = closeErr
			}
		}()
		if _, err := io.Copy(clientConn, targetConn); err != nil {
			_ = err
		}
	}()
}
