// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import "testing"

// T27 (ADR-067 A-14, FR-025, FR-030) — TestDisplayName_FromCatalog pins
// DisplayName to the catalog as its only source: a known id returns the
// row's own `name`, and an id the catalog does not carry is echoed back
// VERBATIM — never title-cased, never looked up in a retired
// `knownDisplayNames` map (that map, and its brand-guessing fallback, is
// exactly the Go-side provider knowledge ADR-067 deleted).
func TestDisplayName_FromCatalog(t *testing.T) {
	tests := []struct {
		name       string
		providerID string
		want       string
	}{
		{
			name:       "a catalog id returns the row's own name",
			providerID: "zai",
			want:       "Z.AI",
		},
		{
			name:       "an unknown id is echoed back verbatim, not title-cased",
			providerID: "z-ai",
			want:       "z-ai",
		},
		{
			name:       "surrounding whitespace is trimmed before the lookup",
			providerID: "  zai  ",
			want:       "Z.AI",
		},
		{
			name:       "an empty id returns the input unchanged",
			providerID: "",
			want:       "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DisplayName(tt.providerID); got != tt.want {
				t.Errorf("DisplayName(%q) = %q, want %q", tt.providerID, got, tt.want)
			}
		})
	}
}
