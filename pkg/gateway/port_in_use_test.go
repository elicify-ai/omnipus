// Contract test: Plan 3 §1 acceptance decision — binding to an in-use port must
// cause a fatal exit with an informative error message.
//
// BDD: Given port P is already in use, When the gateway tries to listen on port P,
//
//	Then it fails with an error that identifies the port and suggests a fix.
//
// Acceptance decision: Plan 3 §1 "Port-in-use: fatal at boot with fix hint"
// Traces to: temporal-puzzling-melody.md §4 Axis-1, pkg/gateway/port_in_use_test.go

package gateway

import (
	"testing"
)

// TestPortInUseFatalExit verifies that starting a gateway on an already-occupied
// port causes a boot error.
//
// The testutil harness does not yet support WithPort(p) to force a specific port,
// which is required to orchestrate a "port already in use" scenario via
// StartTestGateway. Until that option lands the test is honest-skipped.
//
// When WithPort is available:
//  1. Start a raw net.Listener on port P to occupy it.
//  2. Call testutil.StartTestGateway(t, testutil.WithPort(P)).
//  3. Assert the call fails (or the gateway exits) with an error containing "address already in use".
//  4. Assert that starting on a different free port succeeds.
//
// Traces to: temporal-puzzling-melody.md §4 Axis-1 — TestPortInUseFatalExit
func TestPortInUseFatalExit(t *testing.T) {
	t.Skip(
		"testutil.StartTestGateway does not yet support WithPort(p) — " +
			"add WithPort option to pkg/agent/testutil/options.go, then " +
			"implement this test against the real gateway boot path",
	)
}
