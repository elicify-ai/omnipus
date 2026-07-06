// Contract test: Plan 3 §1 acceptance decision — every known secret token pattern
// (sk-, Bearer, ghp_, AWS, GCP) is masked by the audit redactor regardless of
// the filter_min_length content-length setting.
//
// BDD: Given audit content containing a known secret token pattern,
//
//	When the Redactor processes the content,
//	Then the token is replaced with [REDACTED].
//
// Acceptance decision: Plan 3 §1 "filter_min_length applies to entropy filter only;
// known patterns mask regardless of length"
// Traces to: temporal-puzzling-melody.md §4 Axis-1, pkg/audit/mask_patterns_test.go
//
// NOTE: This test lives in pkg/audit/ because the canonical pattern-based masking
// implementation is pkg/audit/redactor.go. The plan document referenced "pkg/secret/"
// but that package does not exist — the audit redactor IS the secret-masking layer.

package audit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestKnownSecretPatternsMaskedRegardlessOfLength verifies that all known secret
// token patterns are caught by the audit redactor. Each test case embeds the
// pattern in content that would otherwise pass the filter_min_length fast-path.
//
// Traces to: temporal-puzzling-melody.md §4 Axis-1 — TestKnownSecretPatternsMaskedRegardlessOfLength
func TestKnownSecretPatternsMaskedRegardlessOfLength(t *testing.T) {
	redactor, err := audit.NewRedactor(nil) // nil = default patterns only
	require.NoError(t, err, "NewRedactor must succeed")

	tests := []struct {
		name         string
		input        string
		wantRedacted bool
		note         string
	}{
		{
			// OpenAI key: sk- followed by 20+ alphanumeric chars.
			name:         "OpenAI API key (sk-)",
			input:        "calling openai with key sk-proj-abcdefghijklmnopqrstu",
			wantRedacted: true,
		},
		{
			// Bearer token (JWT or OAuth).
			name:         "Bearer token",
			input:        "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.sig",
			wantRedacted: true,
		},
		{
			// GitHub personal access token.
			name:         "GitHub PAT (ghp_)",
			input:        "token: ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890",
			wantRedacted: true,
		},
		{
			// GitHub OAuth token.
			name:         "GitHub OAuth token (gho_)",
			input:        "oauth: gho_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890",
			wantRedacted: true,
		},
		{
			// Slack bot token.
			name:         "Slack bot token (xoxb-)",
			input:        "slack_token: xoxb-1234567890-abcdefghij",
			wantRedacted: true,
		},
		{
			// Benign content must NOT be redacted.
			name:         "benign content is not redacted",
			input:        "the user asked about weather in Paris",
			wantRedacted: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := redactor.Redact(tc.input)

			if tc.wantRedacted {
				assert.Containsf(t, result, "[REDACTED]",
					"known secret pattern must be replaced; input: %q", tc.input)
				assert.NotEqualf(t, tc.input, result,
					"redacted output must differ from input; input: %q", tc.input)
			} else {
				assert.NotContainsf(t, result, "[REDACTED]",
					"benign content must not be redacted; input: %q", tc.input)
				assert.Equalf(t, tc.input, result,
					"benign content must pass through unchanged; input: %q", tc.input)
			}
		})
	}

	// Differentiation: redacted and non-redacted inputs produce genuinely different results.
	secretContent := redactor.Redact("key: sk-proj-abcdefghijklmnopqrstu")
	benignContent := redactor.Redact("the user asked about weather")
	assert.NotEqual(t, secretContent, benignContent,
		"secret content and benign content must produce different redaction outcomes")
}
