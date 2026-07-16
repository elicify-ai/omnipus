// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package config — tests for the gateway.browser_video_enabled FR-020
// kill-switch resolver (live-browser-video-streaming-spec.md FR-020).

package config

import "testing"

// TestGatewayConfig_IsBrowserVideoEnabled verifies FR-020's default-true
// posture: an unset field resolves to enabled, and an explicit true/false
// value always wins over the default.
func TestGatewayConfig_IsBrowserVideoEnabled(t *testing.T) {
	tests := []struct {
		name string
		val  *bool
		want bool
	}{
		{name: "unset defaults to enabled", val: nil, want: true},
		{name: "explicit true stays enabled", val: boolPtr(true), want: true},
		{name: "explicit false is the kill-switch off", val: boolPtr(false), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := &GatewayConfig{BrowserVideoEnabled: tc.val}
			if got := g.IsBrowserVideoEnabled(); got != tc.want {
				t.Fatalf("IsBrowserVideoEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}
