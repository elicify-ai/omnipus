package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// normalizeAgentIdentityForSchemaValidation changes a validation-only copy.
// ARCH-DECISIONS §1.4 permits letter-case variants of the closed colour enum.
// The 2026-10-09 founder create ruling additionally treats null/empty identity
// as omission on POST only. The named decoder and raw presence guards keep the
// original bytes; shared schemas and global validation are never relaxed.
func normalizeAgentIdentityForSchemaValidation(raw []byte, create bool) []byte {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		// The unchanged schema check rejects malformed input. A normalization
		// failure must not skip that check or turn an invalid body into a write.
		return raw
	}
	changed := false
	if create {
		for _, name := range []string{"figure", "role", "color"} {
			value := bytes.TrimSpace(fields[name])
			if bytes.Equal(value, []byte("null")) || bytes.Equal(value, []byte(`""`)) {
				delete(fields, name)
				changed = true
			}
		}
	}
	if value, present := fields["color"]; present {
		var color string
		if json.Unmarshal(value, &color) == nil {
			if canonical, ok := coreagent.CanonicalColor(color); ok && canonical != color {
				encoded, err := json.Marshal(canonical)
				if err != nil {
					return raw // Validate the original, never bypass the enum.
				}
				fields["color"] = encoded
				changed = true
			}
		}
	}
	if !changed {
		return raw
	}
	normalized, err := json.Marshal(fields)
	if err != nil {
		return raw // Validate the original, never bypass the schema.
	}
	return normalized
}

func validateAgentIdentitySchema(w http.ResponseWriter, schemaName string, raw []byte, create bool) bool {
	schemaBody := normalizeAgentIdentityForSchemaValidation(raw, create)
	if errMsg, serverErr := validateBodyAgainstSchema(schemaName, schemaBody); errMsg != "" {
		if serverErr {
			jsonErr(w, http.StatusInternalServerError, "inbound schema unavailable")
		} else {
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("request body does not match schema %s: %s", schemaName, errMsg))
		}
		return false
	}
	return true
}
