package testutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// TestStartTestGateway_ForeignHealthResponderIsNotAcceptedAsReady reproduces
// the CI failure on run 37943454247 (Go Race Tests group-2,
// TestSinceCursor_FullReplayWithoutSince):
//
//	since_cursor_reconnect_test.go:239: createSession: response did not include session id
//	gateway_harness.go:672: testutil.TestGateway.Close: gateway exited with error:
//	    error starting channels: channels: bind shared HTTP server at 127.0.0.1:44819:
//	    bind: address already in use
//
// MECHANISM UNDER TEST. The gateway binds its sole HTTP listener synchronously
// inside StartAll (pkg/channels/manager.go) BEFORE it can serve /health, so a
// 200 /health answer can never come from a gateway whose bind failed. In that
// CI run the harness's own gateway had LOST the bind race, yet /health still
// answered 200 — a foreign listener (another test binary's server; race
// group-2 runs 39 package binaries concurrently on one runner, all drawing
// kernel-ephemeral ports through the same listen/close/reuse idiom) was holding
// the port. pollUntilReady accepted that stranger's 200 as proof of OUR
// readiness, the test then ran against the foreign server, and the real boot
// error only surfaced later in Close.
//
// This test forces exactly that shape: the first port draw returns a port
// already held by a squatter serving 200 /health, and the gateway runner loses
// the bind race against it exactly the way production loses (same bind, same
// error wrap chain). The harness must NOT declare readiness from the squatter:
// it must retry on a fresh port and return a gateway whose /health responder
// is provably the one it booted.
//
// Before the fix this test fails with the same signature as CI: StartTestGateway
// returns a TestGateway whose URL points at the squatter.
func TestStartTestGateway_ForeignHealthResponderIsNotAcceptedAsReady(t *testing.T) {
	// ── The squatter: a foreign HTTP server already listening on the port
	// our first draw will return, answering /health with a bare 200 —
	// indistinguishable from a healthy gateway as far as the OLD probe went.
	squatterMux := http.NewServeMux()
	squatterMux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	squatterLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("squatter listen: %v", err)
	}
	squatterAddr, ok := squatterLn.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("squatter listener address type %T (want *net.TCPAddr)", squatterLn.Addr())
	}
	squatterPort := squatterAddr.Port
	squatterSrv := &http.Server{Handler: squatterMux}
	go func() { _ = squatterSrv.Serve(squatterLn) }()
	t.Cleanup(func() { _ = squatterSrv.Close() })

	// ── Force the draws: first attempt gets the squatter's port (the CI
	// collision), later attempts get honest fresh ports so the retry can win.
	realAllocator := portAllocator
	firstDraw := true
	portAllocator = func(tb *testing.T) int {
		if firstDraw {
			firstDraw = false
			return squatterPort
		}
		return realAllocator(tb)
	}
	t.Cleanup(func() { portAllocator = realAllocator })

	// ── The runner under boot: behaves like gateway.RunContext — reads the
	// port from config.json, binds it, serves /health (with an identity marker
	// the squatter does NOT send), and writes homeDir/gateway.port the way
	// gateway_boot.go::registerProcess does after a successful bind. On the
	// squatter's port it loses the bind race for real (kernel EADDRINUSE) and
	// returns it through production's exact wrap chain.
	RegisterGatewayRunner(fakeGatewayRunner(t, squatterPort))
	t.Cleanup(func() { RegisterGatewayRunner(nil) })

	gw := StartTestGateway(t)

	if got := portOfTestGatewayURL(t, gw.URL); got == squatterPort {
		t.Fatalf("StartTestGateway declared readiness while pointing at the foreign server: "+
			"URL %s is the port the squatter holds — the 200 /health was not our gateway's "+
			"(the exact CI 37943454247 failure shape)", gw.URL)
	}

	resp, err := gw.HTTPClient.Get(gw.URL + "/health")
	if err != nil {
		t.Fatalf("health check against the booted gateway: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("booted gateway /health: status %d, want 200", resp.StatusCode)
	}
	if marker := resp.Header.Get(fakeGatewayMarkerHeader); marker != fakeGatewayMarkerValue {
		t.Fatalf("the /health responder is NOT the gateway this harness booted "+
			"(marker header %q=%q missing) — readiness was accepted from a foreign server",
			fakeGatewayMarkerHeader, marker)
	}
}

const (
	fakeGatewayMarkerHeader = "X-Testutil-Fake-Gateway"
	fakeGatewayMarkerValue  = "booted-by-fake-runner"
)

// fakeGatewayRunner mimics the real gateway boot for the identity test: it
// resolves its port from config.json (gateway.port, exactly as the harness
// writes it), and either loses the bind race on the squatted port through
// production's error chain or binds a fresh port and serves a marked /health.
func fakeGatewayRunner(tb *testing.T, squatterPort int) func(context.Context, bool, string, string, bool) error {
	tb.Helper()
	return func(ctx context.Context, _ bool, homePath, configPath string, _ bool) error {
		raw, err := os.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("fake runner: read config: %w", err)
		}
		var cfg struct {
			Gateway struct {
				Port int `json:"port"`
			} `json:"gateway"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("fake runner: decode config: %w", err)
		}
		addr := fmt.Sprintf("127.0.0.1:%d", cfg.Gateway.Port)

		if cfg.Gateway.Port == squatterPort {
			// Lose the bind race for real: the squatter still holds the port,
			// so the kernel hands back a genuine EADDRINUSE, wrapped exactly
			// as production wraps it (gateway_boot.go → manager.go). The short
			// delay mirrors the manager's bind-retry window, during which the
			// real harness previously accepted the squatter's 200.
			time.Sleep(200 * time.Millisecond)
			ln, bindErr := net.Listen("tcp", addr)
			if bindErr == nil {
				_ = ln.Close()
				return fmt.Errorf("fake runner: expected %s to be held by the squatter, but the bind succeeded", addr)
			}
			return fmt.Errorf("error starting channels: %w",
				fmt.Errorf("channels: bind shared HTTP server at %s: %w", addr, bindErr))
		}

		// Fresh port: full fake boot — bind, serve a marked /health, and write
		// homeDir/gateway.port the way gateway_boot.go::registerProcess does.
		ln, bindErr := net.Listen("tcp", addr)
		if bindErr != nil {
			if errors.Is(bindErr, syscall.EADDRINUSE) {
				return fmt.Errorf("error starting channels: %w",
					fmt.Errorf("channels: bind shared HTTP server at %s: %w", addr, bindErr))
			}
			return fmt.Errorf("fake runner: bind %s: %w", addr, bindErr)
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(fakeGatewayMarkerHeader, fakeGatewayMarkerValue)
			w.WriteHeader(http.StatusOK)
		})
		srv := &http.Server{Handler: mux}
		serveErr := make(chan error, 1)
		go func() { serveErr <- srv.Serve(ln) }()
		if writeErr := os.WriteFile(
			filepath.Join(homePath, "gateway.port"),
			[]byte(strconv.Itoa(cfg.Gateway.Port)+"\n"),
			0o600,
		); writeErr != nil {
			_ = srv.Close()
			return fmt.Errorf("fake runner: write gateway.port: %w", writeErr)
		}

		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
			return nil
		case err := <-serveErr:
			return fmt.Errorf("fake runner: serve: %w", err)
		}
	}
}

// portOfTestGatewayURL extracts the port from a TestGateway URL ("http://127.0.0.1:N").
func portOfTestGatewayURL(tb *testing.T, rawURL string) int {
	tb.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		tb.Fatalf("parse gateway URL %q: %v", rawURL, err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		tb.Fatalf("gateway URL %q has no numeric port", rawURL)
	}
	return port
}
