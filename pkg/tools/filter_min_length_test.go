// Contract test: Plan 3 §1 — documenting actual redaction behavior for the
// audit.Redactor pattern-matching layer.
//
// The plan §1 statement "known patterns mask regardless of length" refers to
// the regex-length threshold built into each pattern (e.g., sk-[a-zA-Z0-9-]{20,}
// requires the prefix PLUS 20 characters). Short fragments below that threshold
// (e.g. "sk-abc") are NOT redacted by the regex, which is the correct behavior —
// the regex is intentionally conservative to avoid false positives.
//
// filter_min_length is a separate content-length fast-path in config.FilterSensitiveData
// that skips the entropy scan for very short strings; it does NOT affect the audit
// redactor's pattern matching, which always runs regardless of content length.
//
// Acceptance decision: Plan 3 §1 — filter_min_length applies to entropy filter only;
// known pattern redaction is governed by each pattern's own length constraint.
// Traces to: temporal-puzzling-melody.md §4 Axis-1, pkg/tools/filter_min_length_test.go

package tools_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestRedactorPatternLengthThresholds verifies the audit.Redactor's actual behavior:
// full-length known patterns are redacted; short fragments below the pattern's
// own length threshold are not. This documents the real contract, not an
// aspirational one.
//
// The plan §1 phrase "known patterns mask regardless of length" means the filter_min_length
// content-length fast-path does NOT bypass pattern matching — it does NOT mean that
// arbitrarily short fragments matching a key prefix are masked.
//
// Traces to: temporal-puzzling-melody.md §4 Axis-1 — TestRedactorPatternLengthThresholds
func TestRedactorPatternLengthThresholds(t *testing.T) {
	// The audit redactor is the canonical pattern-matching layer.
	redactor, err := audit.NewRedactor(nil) // nil = use default patterns
	require.NoError(t, err, "NewRedactor must succeed with nil custom patterns")

	tests := []struct {
		name         string
		input        string
		wantRedacted bool
		note         string
	}{
		{
			// A full-length OpenAI key — matches sk-[a-zA-Z0-9-]{20,}
			name:         "full openai key is redacted",
			input:        "api_key: sk-abcdefghijklmnopqrstu",
			wantRedacted: true,
		},
		{
			// A GitHub token — matches ghp_[a-zA-Z0-9]{36} (requires exactly 36 chars)
			name:         "full github token is redacted",
			input:        "token: ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890",
			wantRedacted: true,
		},
		{
			// Bearer token — matches Bearer\s+[...]+
			name:         "bearer token is redacted",
			input:        "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
			wantRedacted: true,
		},
		{
			// Short "sk-abc" does NOT match sk-[a-zA-Z0-9-]{20,} because the
			// pattern requires 20+ chars after the prefix. The regex length
			// threshold intentionally prevents false positives on short fragments.
			// This is the ACTUAL behavior and the correct contract.
			name:         "short sk-abc below pattern length threshold is not redacted",
			input:        "key: sk-abc",
			wantRedacted: false,
			note: "sk-abc has only 3 chars after 'sk-'; the pattern requires 20+. " +
				"Plan §1 'known patterns mask regardless of length' refers to " +
				"filter_min_length not gating the pattern check, not to the " +
				"pattern's own length constraint being ignored.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := redactor.Redact(tc.input)

			if tc.wantRedacted {
				assert.Contains(t, result, "[REDACTED]",
					"known secret pattern must be replaced with [REDACTED]; input: %q", tc.input)
				assert.NotEqual(t, tc.input, result,
					"redacted output must differ from input")
			} else {
				if tc.note != "" {
					t.Logf("CONTRACT NOTE: %s", tc.note)
				}
				assert.NotContains(t, result, "[REDACTED]",
					"short fragment below pattern length threshold must not be redacted; input: %q", tc.input)
			}
		})
	}

	// Differentiation: a full key is redacted, a short fragment is not.
	fullKey := redactor.Redact("sk-abcdefghijklmnopqrstu")
	shortKey := redactor.Redact("sk-abc")
	assert.NotEqual(t, fullKey, shortKey,
		"full key and short fragment must produce different redaction outcomes")
}
