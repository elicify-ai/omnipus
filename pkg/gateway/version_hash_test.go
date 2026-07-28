// Contract test: Plan 3 §1 acceptance decision — /api/v1/version endpoint returns
// a build SHA that clients use to detect SPA version drift.
//
// BDD: Given the gateway is running, When GET /api/v1/version is called,
//
//	Then 200 is returned with a non-empty build hash.
//
// Acceptance decision: Plan 3 §1 "SPA cache drift: /api/v1/version build hash poll"
// Traces to: temporal-puzzling-melody.md §4 Axis-1, pkg/gateway/version_hash_test.go

package gateway

import (
	"testing"
)

// TestVersionEndpointReturnsBuildSHA verifies the /api/v1/version endpoint contract.
//
// This endpoint is required by Plan 3 §1 to detect SPA cache drift.
// It must return the build SHA so the frontend can compare against its bundled hash.
//
// Traces to: temporal-puzzling-melody.md §4 Axis-1 — TestVersionEndpointReturnsBuildSHA
func TestVersionEndpointReturnsBuildSHA(t *testing.T) {
	t.Skip(
		"gated on /api/v1/version endpoint implementation — tracked in Plan 3 §4 Axis-1; implement after the endpoint lands in rest.go",
	)
	// When /api/v1/version exists:
	//   api, cleanup := newTestRestAPI(t)
	//   defer cleanup()
	//   req := httptest.NewRequest("GET", "/api/v1/version", nil)
	//   w := httptest.NewRecorder()
	//   api.handleVersion(w, req)
	//   assert.Equal(t, 200, w.Code)
	//   var resp map[string]any
	//   json.NewDecoder(w.Body).Decode(&resp)
	//   buildHash, ok := resp["build_hash"].(string)
	//   assert.True(t, ok, "response must contain build_hash string field")
	//   assert.NotEmpty(t, buildHash, "build hash must not be empty")
	//   // Differentiation: the hash must not be identical to a hardcoded constant.
	//   assert.NotEqual(t, "unknown", buildHash, "build hash must not be the 'unknown' fallback")
}
