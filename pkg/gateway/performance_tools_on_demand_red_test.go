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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

// Order 48b — TestGetPerformance_DoesNotReportToolsOnDemand (S-44; ST-Q4 B; C-22
// row 2). Added in fix round 4, Part B row 7, when the three tests that pinned the
// old GET/PUT behaviour of the switch were retired from rest_performance_test.go.
// Oracle: C-22 row 2 — `tools_on_demand` leaves PerformanceSettings and
// `rest_performance.go` "stops reading, writing and reporting it". The PUT half is
// the test above; this is the GET half, which had no test in the redesign pack.
//
// The stored key is seeded with the non-default value (the shipped default is
// compressed=true), so a handler that still mirrored a stored value would have
// something to report. The control assertions make the decode prove it saw a real
// Performance body: a non-object, an error body or an empty object cannot pass.
//
// RED today: the handler always mirrors cfg.Tools.Manifest.Compressed into the
// response, so the key is present.
func TestGetPerformance_DoesNotReportToolsOnDemand(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	cfgJSON := []byte(`{"version":1,"agents":{"defaults":{}},"providers":[],"tools":{"manifest":{"compressed":false}}}`)
	require.NoError(t, os.WriteFile(api.configPath(), cfgJSON, 0o600))
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/performance", nil)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET must return 200; body: %s", w.Body.String())

	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "GET body must be a JSON object: %s", w.Body.String())
	require.Contains(t, body, "goal_max_rounds",
		"instrument: the decoded body must be the Performance settings, or an absent key proves nothing; body: %s", w.Body.String())
	require.Contains(t, body, "max_parallel_agents",
		"instrument: the decoded body must be the Performance settings; body: %s", w.Body.String())
	require.NotContains(t, body, "tools_on_demand",
		"C-22 row 2: GET /performance must not report tools_on_demand any more; body: %s", w.Body.String())
}
