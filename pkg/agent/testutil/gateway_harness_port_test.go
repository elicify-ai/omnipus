package testutil

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file tests the harness's gateway-port allocation (gateway_harness.go,
// the block above allocatePort) and the foreign-gateway detection that guards
// a "ready" verdict.
//
// Background — what these tests exist to prevent. StartTestGateway has to
// commit to a port ~0.65s BEFORE the gateway binds it, because the port goes
// into config.json and RunContext accepts no listener. The old code picked it
// with net.Listen("127.0.0.1:0") → read → close → reuse, under a comment
// claiming "the OS will not reuse the port immediately". That claim is false,
// and the port was measured being lost on ~0.5% of boots (~1-in-8 per
// tests/integration run) on Linux, three different ways:
//
//	A. a rival test binary's gateway binds it first;
//	B. any outbound connection on the host is assigned it as a SOURCE port;
//	C. the harness's OWN /health probe — polling every 50ms during exactly the
//	   pre-bind window — is assigned the port it is waiting for. C needs no
//	   competing process at all, which is why "it passed when run isolated"
//	   was never evidence that contention was the cause.
//
// B and C are both "the kernel auto-assigned our port to a connect()", so both
// die the moment the window sits below the kernel's ephemeral floor. That is
// what TestAllocatePort_ReturnsPortBelowEphemeralFloor and
// TestAllocatePort_HarnessWindowIsDisjointFromEphemeralRange assert, and it is
// what fails if the old `:0` picking is ever restored.

// ephemeralPortFloor returns the lowest port this kernel will auto-assign as
// the source port of an outbound connect().
//
// On Linux it is read from procfs, so the assertion is made against the value
// the CI worker's kernel actually uses rather than a number baked into a test.
// Elsewhere it falls back to the LOWEST default floor of any platform this
// project builds for (Linux 32768; macOS/BSD and Windows both start at 49152),
// which keeps the assertion conservative rather than vacuous.
func ephemeralPortFloor(t *testing.T) int {
	t.Helper()

	const lowestDefaultFloorAcrossPlatforms = 32768

	raw, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
	if err != nil {
		t.Logf("no procfs ip_local_port_range (%v) — using the lowest default floor across supported platforms: %d",
			err, lowestDefaultFloorAcrossPlatforms)
		return lowestDefaultFloorAcrossPlatforms
	}

	fields := strings.Fields(string(raw))
	require.Len(t, fields, 2, "ip_local_port_range must be two whitespace-separated ports, got %q", string(raw))
	low, err := strconv.Atoi(fields[0])
	require.NoError(t, err, "parse ip_local_port_range low bound from %q", string(raw))
	return low
}

// observedOutboundSourcePort opens a real loopback TCP connection and reports
// the source port the kernel picked for it.
//
// This is the instrument check for the two floor assertions below: it proves
// the floor value they compare against is the one the kernel genuinely draws
// source ports from, rather than a number this test read from a file (or
// guessed) that no longer reflects reality.
func observedOutboundSourcePort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "open a throwaway listener to connect to")
	defer func() { require.NoError(t, ln.Close()) }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err, "dial the throwaway listener")
	defer func() { require.NoError(t, conn.Close()) }()

	local, ok := conn.LocalAddr().(*net.TCPAddr)
	require.True(t, ok, "unexpected local address type %T", conn.LocalAddr())
	return local.Port
}

// TestAllocatePort_ReturnsPortBelowEphemeralFloor is the behavioural assertion
// that kills mechanisms B and C.
//
// Every port the harness hands a gateway must be one the kernel can NEVER
// auto-assign to an outbound connect(). If it can, then any connection on the
// host — including the harness's own /health poll — can take the port out from
// under the gateway during the ~0.65s it takes to boot.
//
// This is the test that fails if the old net.Listen("127.0.0.1:0") picking is
// restored: `:0` returns a port from the ephemeral range by definition, so
// every allocation would sit at or above the floor.
func TestAllocatePort_ReturnsPortBelowEphemeralFloor(t *testing.T) {
	floor := ephemeralPortFloor(t)

	// Instrument check first: confirm the floor we are about to assert against
	// is the one the kernel actually uses for outbound source ports.
	src := observedOutboundSourcePort(t)
	require.GreaterOrEqual(t, src, floor,
		"instrument check failed: an outbound connection got source port %d, below the floor %d this test "+
			"is asserting against — the floor is wrong, so the assertions below would prove nothing", src, floor)

	const allocations = 8
	for i := range allocations {
		p, err := allocatePort()
		require.NoError(t, err, "allocation %d", i)
		require.Less(t, p, floor,
			"allocation %d returned port %d, which is at or above the ephemeral floor %d — the kernel can "+
				"assign this port as the source port of any outbound connect(), including the harness's own "+
				"/health probe, so the gateway can lose it before it binds (mechanisms B and C)", i, p, floor)
		require.GreaterOrEqual(t, p, portRangeLo, "allocation %d returned %d, below the harness window", i, p)
		require.Less(t, p, portRangeHi, "allocation %d returned %d, above the harness window", i, p)
	}
}

// TestAllocatePort_HarnessWindowIsDisjointFromEphemeralRange states mechanism
// B/C's fix as the invariant it is, independent of any particular allocation:
// the harness's window must not overlap the kernel's ephemeral range AT ALL.
//
// Kept separate from the behavioural test above because it is the constraint a
// future edit to portRangeLo/portRangeHi would violate — widening the window
// "a bit" up to 40000 looks harmless and silently reopens the whole defect.
func TestAllocatePort_HarnessWindowIsDisjointFromEphemeralRange(t *testing.T) {
	floor := ephemeralPortFloor(t)

	assert.LessOrEqual(t, portRangeHi, floor,
		"the harness port window [%d,%d) overlaps this kernel's ephemeral range (starts at %d). Ports in the "+
			"overlap can be auto-assigned to an outbound connect(), which is exactly how the gateway used to "+
			"lose its port to the harness's own /health probe. Lower portRangeHi to at most %d",
		portRangeLo, portRangeHi, floor, floor)
	assert.Positive(t, portRangeLo, "window must start above port 0")
	assert.Less(t, portRangeLo, portRangeHi, "window must be non-empty")
	assert.Positive(t, portRegionCount,
		"the window must divide into at least one region of %d ports", portRegionSize)
}

// TestAllocatePort_SkipsPortAlreadyHeld is mechanism A, made deterministic: a
// rival process is holding the exact port the allocator is about to offer.
//
// peekCandidatePort reports that port WITHOUT consuming it, so the test can
// occupy it first rather than racing the allocator for it. allocatePort must
// then skip it and return a different, still in-window port — not hand out a
// port it cannot have, and not fail.
func TestAllocatePort_SkipsPortAlreadyHeld(t *testing.T) {
	// The peeked port may already be held by something unrelated on this
	// machine, in which case we cannot stage the scenario on it; advance and
	// try the next candidate. Bounded so a hostile machine fails loudly.
	var occupied int
	var rival net.Listener
	for attempt := range 16 {
		candidate := peekCandidatePort()
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", candidate))
		if err != nil {
			t.Logf("attempt %d: candidate %d already held by something else (%v) — advancing", attempt, candidate, err)
			nextCandidatePort() // consume it so the next peek moves on
			continue
		}
		occupied, rival = candidate, ln
		break
	}
	require.NotNil(t, rival, "could not occupy any candidate port to stage the rival-binder scenario")
	defer func() { require.NoError(t, rival.Close()) }()

	require.Equal(t, occupied, peekCandidatePort(),
		"precondition: the allocator must be about to offer the very port the rival now holds")

	got, err := allocatePort()
	require.NoError(t, err,
		"a single occupied candidate must be skipped, not turned into an allocation failure")

	assert.NotEqual(t, occupied, got,
		"allocatePort handed out port %d while another process was listening on it — the gateway would "+
			"fail to bind (mechanism A)", occupied)
	assert.GreaterOrEqual(t, got, portRangeLo,
		"port %d is below the harness window [%d,%d) — a port outside the window can be auto-assigned to an "+
			"outbound connect()", got, portRangeLo, portRangeHi)
	assert.Less(t, got, portRangeHi,
		"port %d is at or above the harness window [%d,%d) — a port outside the window can be auto-assigned "+
			"to an outbound connect()", got, portRangeLo, portRangeHi)
}

// TestAllocatePort_DoesNotRepeatWithinAProcess covers the cursor's other job:
// a gateway that is still shutting down can hold its port briefly, so handing
// the same port to the next StartTestGateway in the same binary is its own
// flake source. The cursor must advance, not restart.
func TestAllocatePort_DoesNotRepeatWithinAProcess(t *testing.T) {
	const allocations = 12
	seen := make(map[int]int, allocations)
	for i := range allocations {
		p, err := allocatePort()
		require.NoError(t, err, "allocation %d", i)
		if prev, dup := seen[p]; dup {
			t.Fatalf("allocation %d returned port %d, already handed out by allocation %d — the cursor is "+
				"not advancing, so consecutive gateways in one binary can collide with each other", i, p, prev)
		}
		seen[p] = i
	}
}

// TestPortRegionForPID_SpreadsAdjacentPIDsAcrossTheWindow covers mechanism A's
// structural half: two test binaries running concurrently must draw from
// DISJOINT regions of the window.
//
// The reason a hash is used rather than `pid % portRegionCount` is that
// concurrently-running test binaries are started back to back and therefore
// hold ADJACENT pids — and `%` maps adjacent pids to adjacent regions, i.e. it
// puts precisely the processes most likely to run together next door to each
// other, where a wrap or an off-by-one overruns into the neighbour. This test
// measures that, and checks its own instrument by running the naive `%` as a
// control: the control must show the clustering, or the assertion on the real
// function is proving nothing.
func TestPortRegionForPID_SpreadsAdjacentPIDsAcrossTheWindow(t *testing.T) {
	const (
		samples = 512
		basePID = 4096
	)

	nearMisses := func(region func(int) int) int {
		n := 0
		for pid := basePID; pid < basePID+samples; pid++ {
			a, b := region(pid), region(pid+1)
			diff := a - b
			if diff < 0 {
				diff = -diff
			}
			if diff <= 1 {
				n++
			}
		}
		return n
	}

	naive := func(pid int) int { return pid % portRegionCount }
	control := nearMisses(naive)
	// `pid % portRegionCount` walks the regions in lockstep with the pid, so
	// EVERY adjacent pair is one region apart — except at a wrap, where the
	// region jumps portRegionCount-1 back. There is one wrap per
	// portRegionCount consecutive pids, hence the small allowance.
	controlFloor := samples - (samples/portRegionCount + 1)
	require.GreaterOrEqual(t, control, controlFloor,
		"instrument check failed: the naive `pid %% portRegionCount` control was expected to put nearly every "+
			"pair of adjacent pids in the same or a neighbouring region (that clustering is the whole reason "+
			"the hash exists), but only %d of %d pairs did (floor %d, allowing for modulus wraps). The "+
			"measurement below cannot be trusted", control, samples, controlFloor)

	actual := nearMisses(portRegionForPID)
	// With portRegionCount regions, chance alone puts an adjacent pair in the
	// same or a neighbouring region ~3/portRegionCount of the time (~2.5% at
	// 120 regions). 10% leaves ~4x headroom over that while still being far
	// below the control's 100%.
	maxAllowed := samples / 10
	assert.LessOrEqual(t, actual, maxAllowed,
		"portRegionForPID put %d of %d adjacent-pid pairs into the same or a neighbouring region (limit %d; "+
			"the naive control puts %d there). Concurrently-started test binaries hold adjacent pids, so "+
			"clustering them is clustering exactly the processes that run at the same time",
		actual, samples, maxAllowed, control)

	for pid := basePID; pid < basePID+samples; pid++ {
		r := portRegionForPID(pid)
		require.GreaterOrEqual(t, r, 0, "pid %d mapped to region %d", pid, r)
		require.Less(t, r, portRegionCount, "pid %d mapped to region %d, outside [0,%d)", pid, r, portRegionCount)

		base := portRangeLo + r*portRegionSize
		require.GreaterOrEqual(t, base, portRangeLo, "pid %d region base %d below the window", pid, base)
		require.LessOrEqual(t, base+portRegionSize, portRangeHi,
			"pid %d region [%d,%d) runs past the end of the window [%d,%d)",
			pid, base, base+portRegionSize, portRangeLo, portRangeHi)
	}
}

// TestForeignGatewayError_ReadyWithOurBootFailed_IsReported covers the second
// harness defect: the readiness probe checks only `StatusCode == 200` on an
// unauthenticated /health, which carries no instance identity. A 200 therefore
// proves only that SOMETHING is listening on that port.
//
// When our own RunContext has already failed, our gateway is serving nothing —
// so a 200 came from a different process, and the test body would have run
// against a stranger's gateway with every assertion silently mis-attributed.
// That must be a loud failure naming the cause.
func TestForeignGatewayError_ReadyWithOurBootFailed_IsReported(t *testing.T) {
	bootErr := errors.New("listen tcp 127.0.0.1:20042: bind: address already in use")

	err := foreignGatewayError("http://127.0.0.1:20042", pollResult{kind: pollReady, attempts: 3}, bootErr)

	require.Error(t, err, "a ready verdict while our own gateway is dead must not be accepted")
	require.ErrorIs(t, err, bootErr, "the real boot error must stay reachable, not be flattened into a string")
	msg := err.Error()
	assert.Contains(t, msg, "DIFFERENT process",
		"the message must name the foreign-gateway diagnosis, or it gets read as an ordinary boot failure")
	assert.Contains(t, msg, "http://127.0.0.1:20042/health",
		"the message must name the endpoint that answered")
}

// TestForeignGatewayError_ReadyAndOurGatewayHealthy_IsAccepted is the happy
// path: our gateway booted and answered. Nothing to report.
func TestForeignGatewayError_ReadyAndOurGatewayHealthy_IsAccepted(t *testing.T) {
	assert.NoError(t, foreignGatewayError("http://127.0.0.1:20042", pollResult{kind: pollReady, attempts: 1}, nil))
}

// TestForeignGatewayError_NotReady_DefersToTheExistingFailurePaths: when the
// poll did not end in `ready`, StartTestGateway's own switch already reports
// the boot error with full diagnostics. Reporting it here too would replace a
// precise message with a wrong one (nothing answered 200, so nothing foreign
// was observed).
func TestForeignGatewayError_NotReady_DefersToTheExistingFailurePaths(t *testing.T) {
	bootErr := errors.New("fatal: provider credential injection failed")

	for _, kind := range []pollOutcomeKind{pollFatalBootError, pollConsecutiveFailures, pollHardBackstop} {
		assert.NoError(t,
			foreignGatewayError("http://127.0.0.1:20042", pollResult{kind: kind, bootErr: bootErr}, bootErr),
			"poll outcome %d is already handled by StartTestGateway's switch", kind)
	}
}

// TestPortRaceHint_BindFailure_NamesTheRace: a lost-port boot failure must not
// read as a generic "gateway failed to boot". That indistinguishability is how
// this defect survived as "flakiness" — the message has to say which of the
// two very different things happened.
func TestPortRaceHint_BindFailure_NamesTheRace(t *testing.T) {
	for _, bootErr := range []error{
		errors.New("listen tcp 127.0.0.1:20042: bind: address already in use"),
		errors.New("Only one usage of each socket address is normally permitted."),
	} {
		hint := portRaceHint(20042, bootErr)
		require.NotEmpty(t, hint, "a bind failure must be named as the port race: %v", bootErr)
		assert.Contains(t, hint, "PORT RACE")
		assert.Contains(t, hint, "20042", "the hint must name the port that was lost")
	}
}

// TestPortRaceHint_NonBindFailure_StaysSilent: the hint must not be pasted onto
// unrelated boot failures, or it becomes noise that trains readers to skip it.
func TestPortRaceHint_NonBindFailure_StaysSilent(t *testing.T) {
	assert.Empty(t, portRaceHint(20042, nil))
	assert.Empty(t, portRaceHint(20042, errors.New("fatal: provider credential injection failed")))
	assert.Empty(t, portRaceHint(20042, errors.New("credentials: decryption failed — wrong master key?")))
}
