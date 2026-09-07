package cdppipe

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"testing"

	"github.com/gobwas/ws"
)

// TestInstallDialer_RoutesSyntheticAndFallsThroughForNormalAddr is the AR-N5
// regression/guard test. installDialer mutates the process-global
// ws.DefaultDialer.NetDial exactly once (dialerOnce), chaining to whatever
// NetDial was previously configured. That is correct today because chromedp
// is the only gobwas/ws consumer in this binary, but it is a process-wide
// side effect that would silently break any OTHER ws.Dial caller that
// appeared later — this test does not change that production behavior, it
// just pins the two guarantees installDialer's doc comment makes so a future
// regression is caught: (1) a synthetic, registry-backed address is served
// entirely from the in-memory bridge, never reaching a chained prior dialer;
// (2) any other address transparently falls through to whatever NetDial was
// previously installed, so a hypothetical second consumer of
// ws.DefaultDialer keeps working. It also proves the installed dispatcher
// survives being installed more than once (dialerOnce), which is the normal
// case in production — installDialer runs on every NewPipeAllocator call,
// one per browser launch.
//
// Run in a subprocess so installing/restoring the process-global dialer cannot
// consume sync.Once for other allocator tests, regardless of test order.
func TestInstallDialer_RoutesSyntheticAndFallsThroughForNormalAddr(t *testing.T) {
	if os.Getenv("OMNIPUS_DIALER_TEST_HELPER") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestInstallDialer_RoutesSyntheticAndFallsThroughForNormalAddr$")
		cmd.Env = append(os.Environ(), "OMNIPUS_DIALER_TEST_HELPER=1", "GORACE=atexit_sleep_ms=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated dialer assertions failed: %v\n%s", err, out)
		}
		return
	}
	origNetDial := ws.DefaultDialer.NetDial
	t.Cleanup(func() { ws.DefaultDialer.NetDial = origNetDial })

	// Simulate a prior consumer having already installed its own NetDial —
	// the "another gobwas/ws consumer" scenario AR-N5 is guarding against.
	// installDialer must chain to it, not replace it.
	var prevCalledWith string
	prevErr := errors.New("prev dialer sentinel")
	ws.DefaultDialer.NetDial = func(_ context.Context, _, addr string) (net.Conn, error) {
		prevCalledWith = addr
		return nil, prevErr
	}

	installDialer()

	// Case 1: a synthetic, registry-backed address must be served from the
	// in-memory bridge — never reaching the chained prev dialer.
	synthAddr := "deadbeef" + magicHostSuffix + ":1"
	client, server := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	registry.register(synthAddr, client)

	got, err := ws.DefaultDialer.NetDial(context.Background(), "tcp", synthAddr)
	if err != nil {
		t.Fatalf("NetDial(synthetic) error = %v, want nil", err)
	}
	if got != client {
		t.Fatalf(
			"NetDial(synthetic) returned %#v, want the registered bridge conn %#v",
			got,
			client,
		)
	}
	if prevCalledWith != "" {
		t.Fatalf(
			"prev NetDial was called with %q for a synthetic address — should not fall through",
			prevCalledWith,
		)
	}

	// Case 2: a normal, never-registered address must fall through to the
	// chained prev dialer, proving installDialer did not silently swallow
	// every dial.
	const normalAddr = "example.com:443"
	_, err = ws.DefaultDialer.NetDial(context.Background(), "tcp", normalAddr)
	if !errors.Is(err, prevErr) {
		t.Fatalf("NetDial(normal) error = %v, want fallthrough to prev sentinel %v", err, prevErr)
	}
	if prevCalledWith != normalAddr {
		t.Fatalf("prev NetDial called with %q, want %q", prevCalledWith, normalAddr)
	}

	// Case 3: the installed dispatcher survives a second installDialer call
	// (dialerOnce no-ops it) and keeps routing both address classes
	// correctly — the normal-launch case, since installDialer runs on every
	// NewPipeAllocator call.
	installDialer()

	synthAddr2 := "cafef00d" + magicHostSuffix + ":1"
	client2, server2 := net.Pipe()
	t.Cleanup(func() {
		client2.Close()
		server2.Close()
	})
	registry.register(synthAddr2, client2)

	got2, err := ws.DefaultDialer.NetDial(context.Background(), "tcp", synthAddr2)
	if err != nil {
		t.Fatalf("NetDial(synthetic) after second installDialer error = %v, want nil", err)
	}
	if got2 != client2 {
		t.Fatalf(
			"NetDial(synthetic) after second installDialer returned %#v, want %#v",
			got2,
			client2,
		)
	}

	prevCalledWith = ""
	_, err = ws.DefaultDialer.NetDial(context.Background(), "tcp", normalAddr)
	if !errors.Is(err, prevErr) {
		t.Fatalf(
			"NetDial(normal) after second installDialer error = %v, want fallthrough to prev sentinel %v",
			err,
			prevErr,
		)
	}
	if prevCalledWith != normalAddr {
		t.Fatalf(
			"prev NetDial after second installDialer called with %q, want %q",
			prevCalledWith,
			normalAddr,
		)
	}
}
