// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package media

import (
	"testing"
)

// TestParseMediaRef verifies the ParseMediaRef validation rules.
// BDD: Given various candidate strings,
//
//	When ParseMediaRef is called,
//	Then only non-empty, correctly-prefixed strings with a non-empty ID are valid.
func TestParseMediaRef(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		wantStr string // expected String() output when valid
	}{
		{
			name:    "empty string is invalid",
			input:   "",
			wantErr: true,
		},
		{
			name:    "bare prefix with empty ID is invalid",
			input:   "media://",
			wantErr: true,
		},
		{
			name:    "valid ref with non-empty ID",
			input:   "media://x",
			wantErr: false,
			wantStr: "media://x",
		},
		{
			name:    "valid ref with UUID-style ID",
			input:   "media://abc123-def456",
			wantErr: false,
			wantStr: "media://abc123-def456",
		},
		{
			name:    "leading space makes it invalid (no media:// prefix)",
			input:   " media://x",
			wantErr: true,
		},
		{
			name:    "non-prefixed string is invalid",
			input:   "not-a-media-ref",
			wantErr: true,
		},
		{
			name:    "HTTP URL is invalid",
			input:   "http://example.com/file.jpg",
			wantErr: true,
		},
		{
			name:    "local path is invalid",
			input:   "/tmp/file.jpg",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := ParseMediaRef(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("ParseMediaRef(%q): expected error, got nil (ref=%q)", tc.input, ref.String())
				}
				if ref.IsValid() {
					t.Errorf("ParseMediaRef(%q): error case must produce invalid Ref, got IsValid()=true", tc.input)
				}
				if ref.raw != "" {
					t.Errorf("ParseMediaRef(%q): error case must produce zero-value Ref, got raw=%q", tc.input, ref.raw)
				}
			} else {
				if err != nil {
					t.Errorf("ParseMediaRef(%q): unexpected error: %v", tc.input, err)
				}
				if !ref.IsValid() {
					t.Errorf("ParseMediaRef(%q): expected IsValid()=true", tc.input)
				}
				if ref.String() != tc.wantStr {
					t.Errorf("ParseMediaRef(%q): String()=%q, want %q", tc.input, ref.String(), tc.wantStr)
				}
			}
		})
	}
}

// TestRef_IsValid verifies the IsValid method is consistent with the constructor.
func TestRef_IsValid(t *testing.T) {
	// Zero-value Ref must be invalid.
	var zero Ref
	if zero.IsValid() {
		t.Error("zero-value Ref must have IsValid()=false")
	}

	// ParseMediaRef success → IsValid must be true.
	ref, err := ParseMediaRef("media://some-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ref.IsValid() {
		t.Error("successfully parsed Ref must have IsValid()=true")
	}
}

// TestFilterValidRefs verifies that FilterValidRefs returns exactly the valid subset.
func TestFilterValidRefs(t *testing.T) {
	input := []string{
		"media://abc123",     // valid
		"",                   // invalid — empty
		"media://",           // invalid — empty ID
		"media://def456",     // valid
		"not-a-ref",          // invalid — no prefix
		" media://x",         // invalid — leading space
		"http://example.com", // invalid — wrong scheme
		"media://z",          // valid
	}

	got := FilterValidRefs(input)
	want := []string{"media://abc123", "media://def456", "media://z"}

	if len(got) != len(want) {
		t.Fatalf("FilterValidRefs: got %d items %v, want %d items %v", len(got), got, len(want), want)
	}
	for i, g := range got {
		if g != want[i] {
			t.Errorf("FilterValidRefs[%d]: got %q, want %q", i, g, want[i])
		}
	}
}

// TestFilterValidRefs_EmptyInput verifies nil input returns nil (not empty slice).
func TestFilterValidRefs_EmptyInput(t *testing.T) {
	got := FilterValidRefs(nil)
	if got != nil {
		t.Errorf("FilterValidRefs(nil): want nil, got %v", got)
	}
	got2 := FilterValidRefs([]string{})
	if got2 != nil {
		t.Errorf("FilterValidRefs([]): want nil, got %v", got2)
	}
}
