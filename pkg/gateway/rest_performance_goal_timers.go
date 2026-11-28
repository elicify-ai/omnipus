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
	if names, ok := topLevelJSONKeys(raw); ok && namesRetiredPerformanceField(names) {
		jsonErr(w, http.StatusBadRequest, retiredToolLoadingRefusal)
		return false
	}
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

// retiredPerformanceField is the Performance field the levels in Skills &
// Tools replaced (C-22 row 2). It is refused by name, with the PUT /config
// refusal of the retired tool-loading keys (C-22 row 3), whether or not
// inbound validation is on: a caller who sends it believes something changed.
const retiredPerformanceField = "tools_on_demand"

// topLevelJSONKeys returns the keys of a JSON object body; ok is false when
// the body is not an object (the decoder answers that case).
func topLevelJSONKeys(raw []byte) ([]string, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	return keys, true
}

// namesRetiredPerformanceField reports whether a key folds, the way
// encoding/json matches field names, to retiredPerformanceField.
func namesRetiredPerformanceField(keys []string) bool {
	for _, k := range keys {
		if jsonFoldKey(k) == jsonFoldKey(retiredPerformanceField) {
			return true
		}
	}
	return false
}
