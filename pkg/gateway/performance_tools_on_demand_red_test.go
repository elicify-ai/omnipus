//go:build goolm && stdjson

package gateway

// Order 48 — TestPerformancePut_RejectsToolsOnDemand (S-44; ST-Q4 B; C-22
// row 2). Oracle: skills-tools-redesign-spec.md — C-22 row 2 removes
// `tools_on_demand` from PerformanceSettings, PerformanceSettingsUpdate and
// both changed_fields enums, and `rest_performance.go` stops reading,
// writing and reporting it: "A request still carrying `tools_on_demand`
// gets the schema's 400 (`additionalProperties: false`). Saving the other
// Limits values keeps working." S-44: Settings → Limits shows no "Load
// tools on demand" switch, and saving Max parallel agents there still
// succeeds.
//
// Harness: the real PUT /api/v1/performance handler
// (rest_performance_ceiling_test.go's mechanics — newTestRestAPIWithHome,
// withReAuthAdmin).
//
// RED today: the Settings base still carries the switch — the generated
// PerformanceSettingsUpdate accepts tools_on_demand and the handler mirrors
// it (rest_performance.go), so leg 1 answers 200 where the landed schema
// must answer 400.
import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPerformancePut_RejectsToolsOnDemand(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Leg 1 — a request still carrying tools_on_demand is refused by the
	// schema (additionalProperties: false once the field is removed).
	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"tools_on_demand":false}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"C-22 row 2: tools_on_demand must be refused with the schema's 400, got %d, body: %s",
		w.Code, w.Body.String())

	// Leg 2 — saving the other Limits values keeps working.
	r = httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":6}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w = httptest.NewRecorder()
	api.HandlePerformance(w, r)
	require.Equal(t, http.StatusOK, w.Code,
		"C-22 row 2: saving the other Limits values keeps working, got %d, body: %s",
		w.Code, w.Body.String())
}
