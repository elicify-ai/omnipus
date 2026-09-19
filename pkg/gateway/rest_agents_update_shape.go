package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Mandatory field validation is independent of optional full-schema checks.
// Policy and other semantic validation still retain their existing 422 errors.
func validateAgentUpdateShape(w http.ResponseWriter, raw []byte) bool {
	if !json.Valid(raw) {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	var request gen.AgentUpdateRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, gen.ErrorResponse{
			Error: "invalid agent update: " + err.Error(),
			Code:  strPtr("invalid_input"),
		})
		return false
	}
	return true
}
