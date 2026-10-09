// workspace_seed_defaults_wire_test.go — RED pack, session-core U5a condition
// 1: `workspace_seed_defaults` is config-file-only and must never cross the
// wire, and the generic gateway write must refuse it.
//
// Expected values derive from the seed-owner-decision (architect c4b574b9c),
// ::No gateway exposure is commissioned: the GET path (rest_config.go's
// getConfig -> sanitizeConfigForWire + wireExcludedConfigFields) must exclude
// the new root table, and updateConfig must "refuse generic gateway writes
// before mutation".
//
// Current code: "workspace_seed_defaults" is in neither the exclusion list nor
// the blocked-path walker, so it leaks on GET and is accepted on PUT. Every
// test below fails on the pre-change code for that reason.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const workspaceSeedDefaultsKey = "workspace_seed_defaults"

// TestSanitizeConfigForWire_StripsWorkspaceSeedDefaults pins the GET sanitizer:
// the shared wire-sanitizer must delete the config-file-only key, and must not
// touch unrelated keys.
func TestSanitizeConfigForWire_StripsWorkspaceSeedDefaults(t *testing.T) {
	m := map[string]any{
		"gateway": map[string]any{"port": float64(5000)},
		workspaceSeedDefaultsKey: map[string]any{
			"self_edge": map[string]any{"exclude_agent_ids": []any{"judge", "plansupervisor"}},
		},
	}
	sanitizeConfigForWire(m)

	if _, present := m[workspaceSeedDefaultsKey]; present {
		t.Fatalf("config-file-only %q survived sanitizeConfigForWire — it must never cross the wire "+
			"(seed-owner-decision ::No gateway exposure; U5a condition-1 GET sanitizer)", workspaceSeedDefaultsKey)
	}
	// Control: the sanitizer must leave unrelated keys alone — a blanket-wipe
	// implementation is not a pass.
	if _, present := m["gateway"]; !present {
		t.Fatal("control: sanitizeConfigForWire must not touch keys outside the exclusion list")
	}
}

// TestWireExcludedConfigFields_IncludesWorkspaceSeedDefaults pins the single
// place the exclusion lives, so a future endpoint that serves the raw config
// map cannot re-introduce the leak.
func TestWireExcludedConfigFields_IncludesWorkspaceSeedDefaults(t *testing.T) {
	for _, k := range wireExcludedConfigFields {
		if k == workspaceSeedDefaultsKey {
			return
		}
	}
	t.Fatalf("BLOCKED: %q is not listed in wireExcludedConfigFields — the config-file-only key "+
		"would leak to GET /api/v1/config (seed-owner-decision ::No gateway exposure)",
		workspaceSeedDefaultsKey)
}

// TestUpdateConfig_RefusesWorkspaceSeedDefaultsWrite pins the raw-write refusal:
// a generic PUT /api/v1/config carrying the config-file-only key must be
// refused BEFORE mutation (403, naming the key) and must persist nothing.
func TestUpdateConfig_RefusesWorkspaceSeedDefaultsWrite(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	before := readConfigOnDisk(t, api)

	body := `{"` + workspaceSeedDefaultsKey + `":{"self_edge":{"exclude_agent_ids":[]}}}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.updateConfig(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("a generic gateway write of %q must be refused (403), got %d body=%s — "+
			"seed-owner-decision ::refuse generic gateway writes before mutation",
			workspaceSeedDefaultsKey, w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil {
		assert.Contains(t, resp["error"], workspaceSeedDefaultsKey,
			"the refusal must name the key so the operator knows it is config-file-only")
	}
	after := readConfigOnDisk(t, api)
	assert.Equal(t, before, after, "a refused PUT must persist nothing (no partial mutation)")
}
