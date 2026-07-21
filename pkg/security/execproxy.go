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
	"strings"
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
	srv := &http.Server{Handler: mux}
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
		_ = p.server.Close()
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
		// hostOnly is user-controlled (r.URL.Host, r.Host). Sanitize for log
		// injection (CodeQL go/log-injection): the inline strings.ReplaceAll
		// on "\n"/"\r" is the recognized sanitizer.
		slog.Warn("execproxy: SSRF blocked", "host", strings.ReplaceAll(strings.ReplaceAll(hostOnly, "\n", ""), "\r", ""), "reason", err.Error())
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
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	outReq.Header = r.Header.Clone()

	resp, err := p.ssrf.SafeClient().Do(outReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
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
		// host is user-controlled (CONNECT r.Host). Sanitize for log
		// injection (CodeQL go/log-injection).
		slog.Warn("execproxy: SSRF blocked in CONNECT", "host", strings.ReplaceAll(strings.ReplaceAll(host, "\n", ""), "\r", ""), "reason", err.Error())
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
		targetConn.Close()
		return
	}

	w.WriteHeader(http.StatusOK)
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		targetConn.Close()
		return
	}

	go func() {
		defer targetConn.Close()
		defer clientConn.Close()
		_, _ = io.Copy(targetConn, clientConn)
	}()
	go func() {
		defer targetConn.Close()
		defer clientConn.Close()
		_, _ = io.Copy(clientConn, targetConn)
	}()
}
