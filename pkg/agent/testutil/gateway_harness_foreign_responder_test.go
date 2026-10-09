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
	"strings"
	"sync"
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
// This test pins the SQUATTER-CHECK layer: the forced first draw returns a
// port that is ALREADY LISTENING when the harness draws it, so
// portAlreadyListening discards it before any boot happens and the gateway
// boots on a fresh port. (It is deliberately insensitive to the identity
// check — see TestStartTestGateway_ThiefBetweenCheckAndBindIsRejectedByIdentityCheck
// for the test that pins that layer with a thief appearing AFTER the check.)
//
// Red-before-fix evidence: on the pre-fix tree this test failed with the CI
// signature (StartTestGateway returned while pointing at the squatter's port;
// Close then reported the same "address already in use" chain) — see the
// dispatch log red-before-fix.log. It did not compile there either (the
// portAllocator seam is part of the fix); the red run was taken with the seam
// added and everything else unpatched.
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

// TestStartTestGateway_ThiefBetweenCheckAndBindIsRejectedByIdentityCheck pins
// the IDENTITY-CHECK layer — the layer that actually stopped CI 37943454247.
//
// The real CI shape was NOT "the port was already listening when drawn" (that
// shape is pinned by TestStartTestGateway_ForeignHealthResponderIsNotAcceptedAsReady
// and handled by the squatter check). It was worse: the port was FREE when the
// harness drew it and verified it free, and a foreign server took it in the
// window between the squatter check and the gateway's bind. This test
// reproduces exactly that window: the first draw is an honest free port (the
// squatter check passes it), and the THIEF starts inside the gateway runner —
// after the check, before the runner's bind — so the bind fails for real and
// the thief answers /health 200 on the port the harness believes it is booting.
//
// Only the gateway.port identity check (plus the existing EADDRINUSE retry)
// stands between that thief and false readiness. MUTATION RED EVIDENCE: with
// the identity check disabled in the probe (the check this test exists for),
// this test FAILS — StartTestGateway accepts the thief's 200 as readiness and
// the assertions below catch the returned gateway pointing at the thief's
// port. Against the pre-fix tree this test does not compile (the portAllocator
// seam and both defense layers landed in the same fix commit), so a
// red-before-fix run in the usual sense does not exist; the mutation run is
// the red evidence for this layer.
func TestStartTestGateway_ThiefBetweenCheckAndBindIsRejectedByIdentityCheck(t *testing.T) {
	run, closeThieves := thiefAfterCheckRunner(t)
	RegisterGatewayRunner(run)
	t.Cleanup(func() { RegisterGatewayRunner(nil) })
	// closeThieves is registered BEFORE StartTestGateway's own Close cleanup,
	// so LIFO runs Close first: Close waits for the runner goroutine, which is
	// the only writer of the thief list, before the closer reads it.
	t.Cleanup(closeThieves)

	gw := StartTestGateway(t)

	if got := portOfTestGatewayURL(t, gw.URL); got == stolenPortUnderTest(t) {
		t.Fatalf("StartTestGateway declared readiness while pointing at the thief: URL %s is the "+
			"port stolen between the squatter check and our bind — the identity check did not stop it", gw.URL)
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
			"(marker header %q=%q missing) — readiness was accepted from the thief",
			fakeGatewayMarkerHeader, marker)
	}
}

// stolenPortUnderTest reports the port the first runner invocation saw (the
// one whose bind the thief stole), failing the test if the scenario never ran.
func stolenPortUnderTest(t *testing.T) int {
	t.Helper()
	stolenMu.Lock()
	defer stolenMu.Unlock()
	if stolenPort == 0 {
		t.Fatalf("the runner never recorded a stolen port — the thief scenario did not run")
	}
	return stolenPort
}

// stolenPort/stolenMu record the port whose bind attempt 1 lost.
var (
	stolenMu   sync.Mutex
	stolenPort int
)

// thiefAfterCheckRunner builds a gateway runner whose FIRST invocation is
// racing a thief: it starts a foreign HTTP server (serving bare-200 /health)
// on the port its config names, only AFTER that port has passed the harness's
// squatter check (the check ran before the runner was even started), then
// loses the bind against it for real and returns production's error chain
// after a delay that mirrors the manager's bind-retry window — leaving the
// thief's 200 reachable while bootErr has not landed yet, which is the
// exact stolen-readiness race. Later invocations boot normally on the fresh
// port the harness drew for the retry.
func thiefAfterCheckRunner(tb *testing.T) (func(context.Context, bool, string, string, bool) error, func()) {
	tb.Helper()
	var thieves []*http.Server
	attempts := 0
	run := func(ctx context.Context, _ bool, homePath, configPath string, _ bool) error {
		raw, err := os.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("thief runner: read config: %w", err)
		}
		var cfg struct {
			Gateway struct {
				Port int `json:"port"`
			} `json:"gateway"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("thief runner: decode config: %w", err)
		}
		port := cfg.Gateway.Port
		addr := fmt.Sprintf("127.0.0.1:%d", port)

		stolenMu.Lock()
		isFirst := attempts == 0
		attempts++
		stolenMu.Unlock()

		if isFirst {
			// The thief takes the port HERE — after the harness's squatter
			// check has already passed this port as free.
			thiefMux := http.NewServeMux()
			thiefMux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			thiefLn, listenErr := net.Listen("tcp", addr)
			if listenErr != nil {
				return fmt.Errorf("thief runner: thief listen %s: %w", addr, listenErr)
			}
			thief := &http.Server{Handler: thiefMux}
			go func() { _ = thief.Serve(thiefLn) }()
			stolenMu.Lock()
			stolenPort = port
			stolenMu.Unlock()
			thieves = append(thieves, thief)

			// Lose the bind for real, then hold the error back briefly so the
			// thief's 200 is answerable before bootErr lands — the manager's
			// bind-retry window compressed.
			time.Sleep(300 * time.Millisecond)
			ln, bindErr := net.Listen("tcp", addr)
			if bindErr == nil {
				_ = ln.Close()
				return fmt.Errorf("thief runner: expected %s to be held by the thief, but the bind succeeded", addr)
			}
			return fmt.Errorf("error starting channels: %w",
				fmt.Errorf("channels: bind shared HTTP server at %s: %w", addr, bindErr))
		}

		// Retry attempt: boot normally — bind, serve a marked /health, write
		// gateway.port exactly like gateway_boot.go::registerProcess.
		ln, bindErr := net.Listen("tcp", addr)
		if bindErr != nil {
			if errors.Is(bindErr, syscall.EADDRINUSE) {
				return fmt.Errorf("error starting channels: %w",
					fmt.Errorf("channels: bind shared HTTP server at %s: %w", addr, bindErr))
			}
			return fmt.Errorf("thief runner: bind %s: %w", addr, bindErr)
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
			[]byte(strconv.Itoa(port)+"\n"),
			0o600,
		); writeErr != nil {
			_ = srv.Close()
			return fmt.Errorf("thief runner: write gateway.port: %w", writeErr)
		}
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
			return nil
		case err := <-serveErr:
			return fmt.Errorf("thief runner: serve: %w", err)
		}
	}
	closeThieves := func() {
		for _, s := range thieves {
			_ = s.Close()
		}
	}
	return run, closeThieves
}

// TestGatewayPortIdentityError_RejectsStaleFile unit-tests the identity
// helper directly: a gateway.port that names a DIFFERENT port than the one
// being booted (a stale file from an earlier attempt, or one written by
// something else) must never satisfy the readiness probe, even though the
// file exists — mere existence is not identity.
func TestGatewayPortIdentityError_RejectsStaleFile(t *testing.T) {
	home := t.TempDir()
	portFile := filepath.Join(home, gatewayPortFileName)

	// Missing file: not ready.
	if err := gatewayPortIdentityError(home, 31001); err == nil {
		t.Fatal("missing gateway.port: gatewayPortIdentityError = nil, want an error")
	}

	// Stale file naming a different port: must be rejected even though the
	// file exists — this is the stale-retry case the existence check missed.
	if err := os.WriteFile(portFile, []byte("29999\n"), 0o600); err != nil {
		t.Fatalf("write stale gateway.port: %v", err)
	}
	err := gatewayPortIdentityError(home, 31001)
	if err == nil {
		t.Fatal("stale gateway.port naming 29999 while booting 31001: gatewayPortIdentityError = nil, want an error")
	}
	if !strings.Contains(err.Error(), "29999") || !strings.Contains(err.Error(), "31001") {
		t.Fatalf("stale-file error must name both the observed and the expected port, got: %v", err)
	}

	// Correct port (production writes a trailing newline): accepted.
	if err := os.WriteFile(portFile, []byte("31001\n"), 0o600); err != nil {
		t.Fatalf("write gateway.port: %v", err)
	}
	if err := gatewayPortIdentityError(home, 31001); err != nil {
		t.Fatalf("gateway.port naming the booted port: gatewayPortIdentityError = %v, want nil", err)
	}
}
