package config

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R2: strictness is local to pdf_limits; the rest of config stays lenient.
func TestPDFLimitsConfig_LoadRejectsInvalid(t *testing.T) {
	for _, tc := range []struct {
		name, section, path, reason string
	}{
		{"unknown section field", `{"defaults":{}}`, "pdf_limits", `unknown field "defaults"`},
		{"unknown default field", `{"default":{"max_page":2}}`, "pdf_limits.default", `unknown field "max_page"`},
		{"unknown override field", `{"overrides":[{"provider":"p","max_page":2}]}`, "pdf_limits.overrides[0]", `unknown field "max_page"`},
		{"empty provider", `{"overrides":[{"provider":""}]}`, "pdf_limits.overrides[0]", "provider must be non-empty"},
		{"missing provider", `{"overrides":[{"model":"m"}]}`, "pdf_limits.overrides[0]", "provider must be non-empty"},
		{"blank provider", `{"overrides":[{"provider":"  "}]}`, "pdf_limits.overrides[0]", "provider must be non-empty"},
		{"duplicate provider", `{"overrides":[{"provider":"p"},{"provider":"p","model":""}]}`, "pdf_limits.overrides[1]", "duplicate"},
		{"duplicate model", `{"overrides":[{"provider":"p","model":"m"},{"provider":"p","model":"m"}]}`, "pdf_limits.overrides[1]", "duplicate"},
		{"duplicate pattern", `{"overrides":[{"provider":"p","model":"a/*"},{"provider":"p","model":"a/*"}]}`, "pdf_limits.overrides[1]", "duplicate"},
		{"integer type", `{"default":{"max_file_bytes":"10"}}`, "pdf_limits.default", "cannot unmarshal"},
		{"fraction", `{"overrides":[{"provider":"p","max_file_bytes":0.5}]}`, "pdf_limits.overrides[0]", "cannot unmarshal"},
		{"overflow", `{"default":{"max_file_bytes":9223372036854775808}}`, "pdf_limits.default", "cannot unmarshal"},
		{"bool type", `{"overrides":[{"provider":"p","inline_pdf":1}]}`, "pdf_limits.overrides[0]", "cannot unmarshal"},
		{"overrides type", `{"overrides":{}}`, "pdf_limits", "cannot unmarshal"},
		{"null row", `{"overrides":[null]}`, "pdf_limits.overrides[0]", "provider must be non-empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadConfig([]byte(`{"version":1,"pdf_limits":` + tc.section + `}`))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.path)
			assert.Contains(t, err.Error(), tc.reason)
		})
	}
	for _, field := range pdfLimitIntegerNames() {
		for _, level := range []string{"default", "override"} {
			t.Run(level+" negative "+field, func(t *testing.T) {
				section := fmt.Sprintf(`{"default":{%q:-1}}`, field)
				path := "pdf_limits.default"
				if level == "override" {
					section = fmt.Sprintf(`{"overrides":[{"provider":"p",%q:-1}]}`, field)
					path = "pdf_limits.overrides[0]"
				}
				_, err := loadConfig([]byte(`{"version":1,"pdf_limits":` + section + `}`))
				require.Error(t, err)
				assert.Contains(t, err.Error(), path)
				assert.Contains(t, err.Error(), field)
				assert.Contains(t, err.Error(), "must be >= 0")
			})
		}
	}
	for _, pattern := range []string{"*", "*suffix", "a*b", "a**", "a?", "a[bc]", "a{b,c}"} {
		t.Run("bad pattern "+pattern, func(t *testing.T) {
			row, err := json.Marshal(map[string]any{"provider": "p", "model": pattern})
			require.NoError(t, err)
			_, err = loadConfig([]byte(`{"version":1,"pdf_limits":{"overrides":[` + string(row) + `]}}`))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "pdf_limits.overrides[0]")
			assert.Contains(t, err.Error(), "model")
			assert.Contains(t, err.Error(), "trailing *")
		})
	}
}

// Field list is from R2's eight-field contract, not reflection over production.
func pdfLimitIntegerNames() []string {
	return []string{"max_file_bytes", "max_total_file_bytes", "max_request_bytes", "max_pages_per_file", "max_pages_per_request", "max_documents_per_message", "max_documents_per_request"}
}
