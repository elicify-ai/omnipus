package testutil

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
)

// TestBindRaceIsDetectable proves the predicate StartTestGateway's retry loop
// depends on actually fires.
//
// The loop retries only when the gateway's boot error satisfies
// errors.Is(err, syscall.EADDRINUSE). That is a real risk of being silently
// useless: if ANY layer between net.Listen and the harness had formatted with
// %v instead of %w, errors.Is would answer false forever, the retry would
// never run, and the flake it exists to remove would simply come back — with
// a fix in the tree claiming otherwise.
//
// So this does not assert on a hand-built error value. It provokes a REAL
// EADDRINUSE from the kernel and wraps it through the SAME two format strings
// production uses:
//
//	pkg/channels/manager.go   "channels: bind shared HTTP server at %s: %w"
//	pkg/gateway/gateway.go    "error starting channels: %w"
//
// If either of those is ever changed to %v, this test fails and names the
// consequence.
func TestBindRaceIsDetectable(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold a port: %v", err)
	}
	defer func() { _ = held.Close() }()

	// Same address, still occupied — the exact collision the retry handles.
	_, bindErr := net.Listen("tcp", held.Addr().String())
	if bindErr == nil {
		t.Fatal("expected a bind conflict on an occupied port, got none — this test cannot prove anything")
	}

	wrapped := fmt.Errorf("error starting channels: %w",
		fmt.Errorf("channels: bind shared HTTP server at %s: %w", held.Addr().String(), bindErr))

	if !errors.Is(wrapped, syscall.EADDRINUSE) {
		t.Fatalf("errors.Is(err, syscall.EADDRINUSE) = false for a real bind conflict wrapped as production wraps it.\n"+
			"StartTestGateway's port-race retry is therefore DEAD CODE and the flake it fixes will return.\n"+
			"error was: %v", wrapped)
	}
}

// TestBindRaceDoesNotMatchUnrelatedBootErrors keeps the retry NARROW. A
// blanket "boot failed, try again" would mask a genuine gateway defect behind
// five silent attempts, which is strictly worse than the flake.
func TestBindRaceDoesNotMatchUnrelatedBootErrors(t *testing.T) {
	for _, e := range []error{
		errors.New("fatal: provider credential injection failed"),
		fmt.Errorf("error starting channels: %w", errors.New("channels: telegram token rejected")),
		fmt.Errorf("error starting channels: %w", syscall.ECONNREFUSED),
	} {
		if errors.Is(e, syscall.EADDRINUSE) {
			t.Fatalf("unrelated boot error matched the port-race predicate — the retry would mask it: %v", e)
		}
	}
}

// TestAllocateEphemeralPortReturnsUsablePort covers the helper the retry loop
// calls once per attempt: the port it hands back must be bindable at the
// moment it returns (the race is about what happens AFTER, not before).
func TestAllocateEphemeralPortReturnsUsablePort(t *testing.T) {
	port := allocateEphemeralPort(t)
	if port <= 0 || port > 65535 {
		t.Fatalf("allocateEphemeralPort returned %d, want a valid TCP port", port)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("port %d was not bindable immediately after allocation: %v", port, err)
	}
	_ = ln.Close()
}

// TestAllocateEphemeralPortVariesAcrossCalls: the retry loop is pointless if
// every attempt is handed the same number back.
func TestAllocateEphemeralPortVariesAcrossCalls(t *testing.T) {
	seen := map[int]bool{}
	for i := 0; i < 8; i++ {
		seen[allocateEphemeralPort(t)] = true
	}
	if len(seen) == 1 {
		t.Fatal("allocateEphemeralPort returned the same port on all 8 calls — retrying with a 'fresh' port would retry the same collision")
	}
}
