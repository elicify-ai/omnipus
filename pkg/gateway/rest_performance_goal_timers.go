package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// decodePerformanceUpdate retains the existing schema-toggle behavior and
// generated request type. Explicit null is not omission for these non-nullable
// timer fields, including when inbound schema validation is disabled.
func decodePerformanceUpdate(w http.ResponseWriter, r *http.Request, req *gen.PerformanceSettingsUpdate, validateEnabled bool) bool {
	raw, err := io.ReadAll(r.Body) // The caller already applied the 1MB limit.
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if !decodeAndValidate(w, r, "PerformanceSettingsUpdate", req, validateEnabled) {
		return false
	}
	var present map[string]json.RawMessage
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&present); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	for _, key := range []string{"goal_reminder_quiet_seconds", "goal_stalled_checkin_enabled", "goal_stalled_checkin_minutes"} {
		if value, exists := present[key]; exists && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			jsonErr(w, http.StatusBadRequest, key+" must not be null")
			return false
		}
	}
	return true
}
