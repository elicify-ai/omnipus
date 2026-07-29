// Package channels — manager_http_boot_test.go
//
// Regression coverage for the swallowed-boot-failure defect on the shared HTTP
// server. Historically StartAll launched httpServer.ListenAndServe() in a
// goroutine and only LOGGED the error on a startup-time bind failure (e.g.
// "bind: address already in use"), so StartAll returned nil and the gateway
// stayed alive serving nothing — a silent running-dead state. The fix binds
// the listener synchronously before StartAll returns, turning a port collision
// into a hard, clear boot abort.

package channels

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// TestStartAll_HTTPBindFailureIsFatal proves a startup-time bind collision on
// the shared HTTP server is RETURNED from StartAll (fatal boot error) rather
// than swallowed into a log line. Mechanism: occupy a free port with a
// throwaway listener, point the manager's HTTP server at the same address, and
// assert StartAll surfaces a non-nil error identifying the bind failure.
func TestStartAll_HTTPBindFailureIsFatal(t *testing.T) {
	t.Parallel()

	// Grab a free port and KEEP it occupied so the manager's net.Listen collides.
	occupier, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("open occupier listener: %v", err)
	}
	t.Cleanup(func() { _ = occupier.Close() })
	addr := occupier.Addr().String()

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	m, err := NewManager(&config.Config{}, credentials.SecretBundle{}, msgBus, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// A started channel makes StartAll exercise the full path (workers +
	// dispatchers) before reaching the HTTP bind, mirroring real boot.
	m.channels["test-http-boot"] = &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error { return nil },
	}

	m.SetupHTTPServer(addr, nil)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	startErr := m.StartAll(ctx)
	// Whether or not StartAll returned an error, tear down anything it DID
	// start (workers/dispatchers) so the test leaks nothing.
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })

	if startErr == nil {
		t.Fatalf("StartAll returned nil for a port-in-use bind; expected a fatal boot error (running-dead bug regressed)")
	}

	// The error should reference the bind address and the underlying cause so
	// the operator sees WHY boot aborted, not just THAT it aborted.
	msg := startErr.Error()
	if !strings.Contains(msg, addr) {
		t.Errorf("error %q does not mention the bind address %q", msg, addr)
	}
	// "address already in use" is the OS-level bind-collision text on
	// Linux/macOS/Windows; the wrapped err comes straight from net.Listen.
	if !strings.Contains(msg, "address already in use") {
		t.Errorf("error %q does not surface the underlying bind failure (want %q)", msg, "address already in use")
	}
}

// TestStartAll_HTTPBindSuccess_BootsAndServes is the GREEN counterpart: with a
// free port, StartAll must return nil AND the server must actually accept a
// connection and route it through m.mux. This proves the synchronous-bind +
// goroutine-Serve wiring did not regress the happy path (and that Serve — not
// ListenAndServe — is what now drives the bound listener).
func TestStartAll_HTTPBindSuccess_BootsAndServes(t *testing.T) {
	t.Parallel()

	// Reserve a free port by opening then closing a listener. There is a small
	// race window where another process could grab the port; the manager's
	// net.Listen will surface a clear error if that happens, failing the test
	// rather than masking it.
	reserve, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("open reserve listener: %v", err)
	}
	addr := reserve.Addr().String()
	_ = reserve.Close()

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	m, err := NewManager(&config.Config{}, credentials.SecretBundle{}, msgBus, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	m.SetupHTTPServer(addr, nil)
	// Register a trivial handler on the mux the server will serve, so we can
	// prove the bound listener actually routes through m.mux.
	m.mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := m.StartAll(ctx); err != nil {
		t.Fatalf("StartAll on free port: %v", err)
	}
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })

	// Dial the server to prove it is actually accepting connections (bind +
	// Serve both happened). Retry briefly because Serve starts in a goroutine
	// and may not be accept-ready the instant StartAll returns.
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, gErr := client.Get("http://" + addr + "/healthz")
		if gErr == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				lastErr = nil
				break
			}
			lastErr = fmt.Errorf("unexpected status %d", resp.StatusCode)
		} else {
			lastErr = gErr
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("server did not serve /healthz on the bound port: %v", lastErr)
	}
}
