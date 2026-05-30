//go:build !cgo && test_harness

// Sanity tests for the pkg/agent/testutil gateway harness.
// Requires -tags=test_harness because SetTestProviderOverride is a no-op stub
// in non-test_harness builds; without the tag the gateway cannot boot using an
// injected ScenarioProvider and will fail with "no providers configured".
//
// These tests live in pkg/gateway (not pkg/agent/testutil) because the harness
// requires RegisterGatewayRunner + RegisterProviderOverrideFuncs to be called
// first — which happens in this package's TestMain. Placing them here avoids
// an import cycle while still exercising the harness against the real gateway.
//
// Traces to: temporal-puzzling-melody.md §1 acceptance contracts — harness sanity

package gateway

import (
	"net/http"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
)

// TestHarnessBootsAndShutsDown verifies that StartTestGateway boots a real
// gateway, serves /health with 200, and shuts down cleanly via t.Cleanup.
//
// Goroutine count before and after must be within a small tolerance to catch
// obvious goroutine leaks introduced by the harness itself.
//
// Traces to: temporal-puzzling-melody.md §1 acceptance contracts — harness boots
func TestHarnessBootsAndShutsDown(t *testing.T) {
	before := runtime.NumGoroutine()

	gw := testutil.StartTestGateway(t, testutil.WithAllowEmpty())

	// Gateway must be reachable.
	req, err := gw.NewRequest(http.MethodGet, "/health", nil)
	require.NoError(t, err)

	resp, err := gw.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"/health must return 200 while gateway is running")
	resp.Body.Close()

	// Close is called by t.Cleanup; call it explicitly here so we can check
	// goroutines immediately after shutdown in this test function.
	gw.Close()

	// Allow a small tolerance for background goroutines (GC, finalizers, etc.).
	after := runtime.NumGoroutine()
	const tolerance = 10
	assert.LessOrEqualf(t, after, before+tolerance,
		"goroutine count after shutdown (%d) must not exceed before+%d (%d+%d); possible goroutine leak",
		after, tolerance, before, tolerance)
}

// TestHarnessBearerAuth verifies that WithBearerAuth() enforces authentication:
// requests without the token receive 401, requests with the token receive 200.
//
// Traces to: temporal-puzzling-melody.md §1 acceptance contracts — harness auth
func TestHarnessBearerAuth(t *testing.T) {
	gw := testutil.StartTestGateway(t, testutil.WithBearerAuth())

	// Unauthenticated request to a protected endpoint must return 401.
	unauthReq, err := http.NewRequest(http.MethodGet, gw.URL+"/api/v1/sessions", nil)
	require.NoError(t, err)
	unauthReq.Header.Set("Origin", gw.URL)
	// No Authorization header.

	unauthResp, err := gw.HTTPClient.Do(unauthReq)
	require.NoError(t, err)
	unauthResp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, unauthResp.StatusCode,
		"unauthenticated request to /api/v1/sessions must return 401")

	// Authenticated request via gw.NewRequest (sets Authorization header automatically).
	authReq, err := gw.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	require.NoError(t, err)

	authResp, err := gw.Do(authReq)
	require.NoError(t, err)
	authResp.Body.Close()
	assert.Equal(t, http.StatusOK, authResp.StatusCode,
		"authenticated request to /api/v1/sessions must return 200")
}
