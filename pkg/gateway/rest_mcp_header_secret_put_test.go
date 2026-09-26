// Issue #638 fix-round regression: PUT /api/v1/config must reject — with 403
// and nothing persisted — any body carrying a literal MCP header value under
// tools.mcp.servers.<name>.headers, in EVERY body shape the JSON decoder can
// produce: the nested shape, a top-level dot-path literal key, and a
// partially-flattened dot-path under "tools".
//
// Specification source: the PUT guard's own contract
// (rest_config.go::findLiteralMCPServerHeaderValue doc comment): "header
// values are credentials; set them via POST/PATCH /api/v1/mcp-servers, which
// store them encrypted (only ref names land in config.json)" — and issue
// #638's intent: no literal secret ever lands in config.json. Expected values
// derive from that contract, never from the current implementation's output.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfigPUT_MCPLiteralHeader_RejectedInEveryBodyShape drives updateConfig
// with the nested shape (guarded today — control) and the two flattened
// dot-path shapes (F2, round-2 review, Medium). Every case must come back 403
// with config.json unchanged — the same atomic-reject semantics the
// blockedPaths guard already enforces.
func TestConfigPUT_MCPLiteralHeader_RejectedInEveryBodyShape(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			// The shape findLiteralMCPServerHeaderValue walks today: guarded,
			// and this subtest proves the instrument can see a 403 happen.
			name: "nested_shape_control",
			body: `{"tools":{"mcp":{"servers":{"remote":{"headers":{"Authorization":"Bearer ` + mcpAuthSecret + `"}}}}}}`,
		},
		{
			// F2: the path arrives as one flattened top-level key. The guard
			// only walks tools→mcp→servers, so this must be rejected by name.
			name: "top_level_dot_path_key",
			body: `{"tools.mcp.servers.remote.headers.Authorization":"Bearer ` + mcpAuthSecret + `"}`,
		},
		{
			// F2, second shape the round-2 review names: the flattening
			// starts one level down, inside the "tools" object.
			name: "partial_dot_path_under_tools",
			body: `{"tools":{"mcp.servers.remote.headers.Authorization":"Bearer ` + mcpAuthSecret + `"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			before := readConfigOnDisk(t, api)

			r := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			api.updateConfig(w, r)

			require.Equal(t, http.StatusForbidden, w.Code,
				"issue #638 F2: a literal MCP header value must be rejected in body shape %q; body=%s",
				tc.name, w.Body.String())

			after := readConfigOnDisk(t, api)
			assert.Equal(t, before, after,
				"rejected PUT must not mutate config.json (atomic reject, shape %q)", tc.name)
		})
	}
}
