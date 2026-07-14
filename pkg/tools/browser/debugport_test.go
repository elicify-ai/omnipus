package browser

// debugport_test.go — coverage for checkDebugPortAvailable, the CDP
// debug-port preflight added to ensureStarted's managed-mode launch path.
// The motivating live incident: an orphaned Chrome process from an
// unrelated prior run was squatting the fixed CDP debug port, and every
// subsequent launch failed with an opaque "websocket url timeout reached"
// deep inside chromedp — nothing named the real cause. checkDebugPortAvailable
// takes the port as a parameter specifically so it can be tested directly
// against an OS-assigned ephemeral port, without touching the pinned
// package constant (DebugPort) or spinning up a real Chromium.

import (
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckDebugPortAvailable_PortInUse_ReturnsClearError occupies a port
// with a real TCP listener (simulating the orphaned-Chrome-process
// incident) and verifies checkDebugPortAvailable reports a clear,
// diagnosable error naming the port — not chromedp's opaque downstream
// timeout.
func TestCheckDebugPortAvailable_PortInUse_ReturnsClearError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	err = checkDebugPortAvailable(uint16(port))
	require.Error(t, err)
	assert.Contains(t, err.Error(), strconv.Itoa(port))
	assert.Contains(t, err.Error(), "already in use")
}

// TestCheckDebugPortAvailable_PortFree_NoError verifies a genuinely free
// port passes the preflight cleanly and is left free afterward (the probe
// listener must release the port, not hold it).
func TestCheckDebugPortAvailable_PortFree_NoError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	require.NoError(t, checkDebugPortAvailable(uint16(port)))

	// The preflight must not leak the probe listener — re-binding the same
	// port immediately afterward must still succeed.
	ln2, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	require.NoError(t, err, "checkDebugPortAvailable should release the port it probed")
	require.NoError(t, ln2.Close())
}
