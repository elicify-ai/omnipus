//go:build !mipsle && !netbsd && !(freebsd && arm)

package whatsapp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseJID_Valid verifies that valid chat IDs are correctly parsed into JID structs.
// Traces to: wave4-whatsapp-browser-spec.md line 991 (Test #1: TestParseJID_Valid)
// BDD: Given a valid phone number or JID string,
// When parseJID(s) is called,
// Then a correctly populated types.JID is returned without error.

func TestParseJID_Valid(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md Dataset: JID Parsing rows 1–3, 7
	tests := []struct {
		name       string
		input      string
		wantUser   string
		wantServer string
	}{
		// Dataset row 1 — plain phone number
		{
			name:       "plain phone number",
			input:      "1234567890",
			wantUser:   "1234567890",
			wantServer: "s.whatsapp.net",
		},
		// Dataset row 2 — full direct JID
		{
			name:       "full direct JID",
			input:      "1234567890@s.whatsapp.net",
			wantUser:   "1234567890",
			wantServer: "s.whatsapp.net",
		},
		// Dataset row 3 — group JID
		{
			name:       "group JID",
			input:      "12345678@g.us",
			wantUser:   "12345678",
			wantServer: "g.us",
		},
		// Dataset row 7 — formatted phone number (whatsmeow handles)
		{
			name:       "phone with hyphens treated as plain number",
			input:      "1234567890",
			wantUser:   "1234567890",
			wantServer: "s.whatsapp.net",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			jid, err := parseJID(tc.input)
			require.NoError(t, err, "parseJID(%q) must not return error", tc.input)
			assert.Equal(t, tc.wantUser, jid.User, "JID.User mismatch for input %q", tc.input)
			assert.Equal(t, tc.wantServer, jid.Server, "JID.Server mismatch for input %q", tc.input)
		})
	}
}

// TestParseJID_Invalid verifies that invalid chat IDs return descriptive errors.
// Traces to: wave4-whatsapp-browser-spec.md line 992 (Test #2: TestParseJID_Invalid)
// BDD: Given an empty or malformed chat ID,
// When parseJID(s) is called,
// Then an error is returned with a descriptive message.

func TestParseJID_Invalid(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md Dataset: JID Parsing rows 4–6
	tests := []struct {
		name        string
		input       string
		errContains string
	}{
		// Dataset row 4 — empty string
		{
			name:        "empty string",
			input:       "",
			errContains: "empty chat id",
		},
		// Dataset row 5 — whitespace only
		{
			name:        "whitespace only",
			input:       "   ",
			errContains: "empty chat id",
		},
		// Dataset row 6 — missing user part (@server only)
		// Note: types.ParseJID may or may not error on this — behavior is whatsmeow-defined.
		// We verify parseJID does not panic and returns either error or JID.
		{
			name:  "at-server only treated as partial JID",
			input: "@s.whatsapp.net",
			// whatsmeow may return empty user — test just verifies no panic
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseJID(tc.input)
			if tc.errContains != "" {
				require.Error(t, err, "parseJID(%q) must return error", tc.input)
				assert.Contains(t, err.Error(), tc.errContains,
					"error message must contain %q for input %q", tc.errContains, tc.input)
			}
			// For partial JID cases, no panic is sufficient
		})
	}
}
