// Tests for DevServerRegistry.ReservePort / ConfirmReservation — the #255 fix.
// The old web_serve dev mode always auto-picked PortRange[0], so every
// concurrent dev server collided on one port. ReservePort must hand out
// distinct, free ports under the registry lock.

package sandbox

import (
	"errors"
	"sync"
	"testing"
)

const (
	testPortLo int32 = 18000
	testPortHi int32 = 18999
)

// TestReservePort_AutoPicksDistinctPortsAcrossAgents is the core #255
// regression: two agents that both auto-select (wantPort=0) must receive
// different ports, not both PortRange[0].
func TestReservePort_AutoPicksDistinctPortsAcrossAgents(t *testing.T) {
	r := NewDevServerRegistry()
	defer r.Close()

	_, p1, err := r.ReservePort("agentA", 0, testPortLo, testPortHi, 10)
	if err != nil {
		t.Fatalf("ReservePort agentA: %v", err)
	}
	_, p2, err := r.ReservePort("agentB", 0, testPortLo, testPortHi, 10)
	if err != nil {
		t.Fatalf("ReservePort agentB: %v", err)
	}
	if p1 == p2 {
		t.Fatalf("both agents got the same port %d (the #255 bug)", p1)
	}
	for _, p := range []int32{p1, p2} {
		if p < testPortLo || p > testPortHi {
			t.Errorf("port %d out of range [%d,%d]", p, testPortLo, testPortHi)
		}
	}
}

// TestReservePort_SkipsRegisteredPort confirms auto-select skips a port already
// held by a Register-path entry (explicit-port dev server).
func TestReservePort_SkipsRegisteredPort(t *testing.T) {
	r := NewDevServerRegistry()
	defer r.Close()

	if _, err := r.Register("explicit", testPortLo, 1, "next dev", 10); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, port, err := r.ReservePort("auto", 0, testPortLo, testPortHi, 10)
	if err != nil {
		t.Fatalf("ReservePort: %v", err)
	}
	if port == testPortLo {
		t.Fatalf("ReservePort handed out the already-registered port %d", port)
	}
}

// TestReservePort_ExplicitPortInUse rejects an explicit port already reserved.
func TestReservePort_ExplicitPortInUse(t *testing.T) {
	r := NewDevServerRegistry()
	defer r.Close()

	if _, _, err := r.ReservePort("a", testPortLo, testPortLo, testPortHi, 10); err != nil {
		t.Fatalf("first explicit ReservePort: %v", err)
	}
	if _, _, err := r.ReservePort("b", testPortLo, testPortLo, testPortHi, 10); err == nil {
		t.Fatalf("second ReservePort on the same explicit port should fail")
	}
}

// TestReservePort_ConfirmReservationSetsPID confirms the reservation is a real
// registry entry that ConfirmReservation fills in.
func TestReservePort_ConfirmReservationSetsPID(t *testing.T) {
	r := NewDevServerRegistry()
	defer r.Close()

	token, _, err := r.ReservePort("a", 0, testPortLo, testPortHi, 10)
	if err != nil {
		t.Fatalf("ReservePort: %v", err)
	}
	// Before confirm: entry exists with PID 0.
	if got := r.Lookup(token); got == nil || got.PID != 0 {
		t.Fatalf("reservation entry = %+v; want PID 0", got)
	}
	r.ConfirmReservation(token, 4242, "next dev")
	got := r.Lookup(token)
	if got == nil || got.PID != 4242 || got.Command != "next dev" {
		t.Fatalf("after confirm = %+v; want PID 4242, command 'next dev'", got)
	}
}

// TestReservePort_EnforcesCaps confirms the per-agent and gateway caps apply on
// the reservation path just as they do on Register.
func TestReservePort_EnforcesCaps(t *testing.T) {
	r := NewDevServerRegistry()
	defer r.Close()

	if _, _, err := r.ReservePort("a", 0, testPortLo, testPortHi, 1); err != nil {
		t.Fatalf("first ReservePort: %v", err)
	}
	if _, _, err := r.ReservePort("a", 0, testPortLo, testPortHi, 10); !errors.Is(err, ErrPerAgentCap) {
		t.Errorf("same-agent reservation err = %v; want ErrPerAgentCap", err)
	}
	_, _, err := r.ReservePort("b", 0, testPortLo, testPortHi, 1)
	if _, ok := err.(GatewayCapError); !ok {
		t.Errorf("over-cap reservation err = %T: %v; want exactly GatewayCapError (ReservePort returns it unwrapped)", err, err)
	}
}

// TestReservePort_ConcurrentDistinct stresses the atomicity: N concurrent
// auto-reservations across distinct agents must all get distinct ports.
func TestReservePort_ConcurrentDistinct(t *testing.T) {
	r := NewDevServerRegistry()
	defer r.Close()

	const n = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[int32]bool, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			agent := string(rune('A' + i))
			_, port, err := r.ReservePort(agent, 0, testPortLo, testPortHi, n)
			if err != nil {
				t.Errorf("agent %s ReservePort: %v", agent, err)
				return
			}
			mu.Lock()
			if seen[port] {
				t.Errorf("port %d handed out twice", port)
			}
			seen[port] = true
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if len(seen) != n {
		t.Errorf("got %d distinct ports; want %d", len(seen), n)
	}
}
