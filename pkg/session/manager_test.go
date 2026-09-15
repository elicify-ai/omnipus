package session

import (
	"encoding/hex"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input string
	}{
		{"simple"},
		{"telegram:123456"},
		{"discord:987654321"},
		{"slack:C01234"},
		{"no-colons-here"},
		{"multiple:colons:here"},
		{"agent:main:telegram:group:-1003822706455/12"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeFilename(tt.input)
			want := hex.EncodeToString([]byte(tt.input))
			if got != want {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, want)
			}
		})
	}
}
