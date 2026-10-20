package gateway

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// The listen/close/reuse idiom (bind :0, read the kernel's pick, close, hand
// the number to something that binds later) has a TOCTOU window: between our
// close and the later bind, another process — or another test binary in the
// same `go test` run — can be handed the same number. Train 3 PR CI hit it on
// 7be3eecbf: TestWU15Rooms_OutstandingRequestsKeepDistinctStatesAndRoomIsolation
// died in SETUP with "bind shared HTTP server at 127.0.0.1:44991: bind:
// address already in use". These helpers and the forcing test below pin the
// fix: every such site redraws (or reboots) on a fresh port, bounded, and
// fails loudly with the last error instead of a flaky SETUP crash.

// wu15BootPortDrawnForTest, when non-nil, is called by wu15Boot with every
// freshly drawn port BEFORE the gateway binds it — the forcing hook for
// TestWU15RoomsBoot_BindRaceLostOnFirstPort_BootsOnAFreshPort. Nil in every
// other test.
var wu15BootPortDrawnForTest func(port int)

// drawFreeTCPPort returns a port that was free when its listener was closed.
// Only EADDRINUSE retries; resource/permission/close/type errors remain loud.
func drawFreeTCPPort(t *testing.T, attempts int) int {
	t.Helper()
	port, err := drawFixturePort("TCP", attempts, func() (int, error) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return 0, err
		}
		return closeFixturePort("TCP", ln.Addr(), ln)
	})
	require.NoError(t, err, "drawFreeTCPPort failed")
	return port
}

// drawFreeUDPPort applies the same bounded, diagnostic-preserving rule to UDP.
func drawFreeUDPPort(t *testing.T, attempts int) int {
	t.Helper()
	port, err := drawFixturePort("UDP", attempts, func() (int, error) {
		pc, err := net.ListenPacket("udp", ":0")
		if err != nil {
			return 0, err
		}
		return closeFixturePort("UDP", pc.LocalAddr(), pc)
	})
	require.NoError(t, err, "drawFreeUDPPort failed")
	return port
}

// drawFixturePort keeps the OS cause, including when the retry budget expires.
// The injected draw is only the socket edge: tests can induce EMFILE without
// exhausting descriptors on this shared machine.
func drawFixturePort(network string, attempts int, draw func() (int, error)) (int, error) {
	if attempts < 1 {
		return 0, fmt.Errorf("%s fixture port draw requires at least one attempt", network)
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		port, err := draw()
		if err == nil {
			return port, nil
		}
		lastErr = fmt.Errorf("%s fixture port draw %d/%d failed: %w", network, attempt, attempts, err)
		if !errors.Is(err, syscall.EADDRINUSE) {
			return 0, lastErr
		}
	}
	return 0, lastErr
}

func closeFixturePort(network string, addr net.Addr, socket io.Closer) (int, error) {
	var port int
	var typeErr error
	switch a := addr.(type) {
	case *net.TCPAddr:
		if network == "TCP" {
			port = a.Port
		} else {
			typeErr = fmt.Errorf("%s fixture received unexpected address type %T", network, addr)
		}
	case *net.UDPAddr:
		if network == "UDP" {
			port = a.Port
		} else {
			typeErr = fmt.Errorf("%s fixture received unexpected address type %T", network, addr)
		}
	default:
		typeErr = fmt.Errorf("%s fixture received unexpected address type %T", network, addr)
	}
	if err := errors.Join(typeErr, socket.Close()); err != nil {
		return 0, err
	}
	return port, nil
}

// TestWU15RoomsBoot_BindRaceLostOnFirstPort_BootsOnAFreshPort FORCES the bind
// race the boot retry exists for: the first freshly drawn port is occupied
// before the gateway binds it — exactly what a concurrent test binary does in
// CI. Before the fix this was a SETUP crash ("bind: address already in use");
// with the bounded retry the boot redraws and lands on a port that is still
// free, and the harness's own SETUP route check proves the real gateway — not
// the squatter — is answering.
func TestWU15RoomsBoot_BindRaceLostOnFirstPort_BootsOnAFreshPort(t *testing.T) {
	var squattedPort atomic.Int32
	previous := wu15BootPortDrawnForTest
	t.Cleanup(func() { wu15BootPortDrawnForTest = previous })
	wu15BootPortDrawnForTest = func(port int) {
		if !squattedPort.CompareAndSwap(0, int32(port)) {
			return // squat only the FIRST drawn port — the losing attempt
		}
		squatter, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		require.NoError(t, err)
		t.Cleanup(func() { _ = squatter.Close() })
		t.Logf("forced the bind race: port %d is occupied before the gateway binds it", port)
	}

	h := wu15NewHarness(t)

	u, err := url.Parse(h.baseURL)
	require.NoError(t, err)
	bootedPort, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	require.NotEqual(t, squattedPort.Load(), int32(bootedPort),
		"the boot must have landed on a fresh port, never the squatted one")
	status, body := h.http(http.MethodGet, "/api/v1/workspaces/"+wu15Workspace, nil)
	require.Equal(t, http.StatusOK, status,
		"the real gateway, not the squatter, answers on the port it finally bound: %s", body)
	require.NotEqual(t, int32(0), squattedPort.Load(),
		"the forcing hook recorded the port it squatted")
}
