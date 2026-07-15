// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package config — surviving Track E test for ApplyWarmupTimeoutDefault.
//
// ADR-044 (preview-on-main-listener) deleted the separate preview listener
// entirely — PreviewPort/PreviewHost/PreviewOrigin/PreviewListenerEnabled and
// the ValidateAndApplyPreviewDefaults validator that this file used to test
// are gone (no back-compat). ApplyWarmupTimeoutDefault is unrelated to the
// preview listener (it defaults tools.run_in_workspace.warmup_timeout_seconds)
// and survives untouched, so its test remains here.
//
// The new gateway.preview_enabled semantics (default true, read live via
// GatewayConfig.IsPreviewEnabled) are covered by TestGatewayConfig_IsPreviewEnabled
// and TestLoadConfig_LegacyPreviewFields_Ignored in config_test.go, and
// TestRestartGatedKeys_KeepsPublicURL_DropsPreview in
// pkg/gateway/rest_pending_restart_test.go.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// TestRunInWorkspaceConfig_DefaultWarmupTimeout
// ---------------------------------------------------------------------------

// TestRunInWorkspaceConfig_DefaultWarmupTimeout verifies FR-013 / CR-04:
// ApplyWarmupTimeoutDefault sets 60 s when the field is <= 0.
// Traces to: chat-served-iframe-preview-spec.md FR-013
func TestRunInWorkspaceConfig_DefaultWarmupTimeout(t *testing.T) {
	tests := []struct {
		name    string
		initial int32
		want    int32
	}{
		{
			name:    "zero → 60 (default)",
			initial: 0,
			want:    60,
		},
		{
			name:    "negative → 60 (default)",
			initial: -1,
			want:    60,
		},
		{
			name:    "explicit value preserved",
			initial: 120,
			want:    120,
		},
		{
			name:    "custom low value preserved",
			initial: 30,
			want:    30,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tools := &ToolsConfig{}
			tools.RunInWorkspace.WarmupTimeoutSeconds = tc.initial
			tools.ApplyWarmupTimeoutDefault()
			assert.Equal(t, tc.want, tools.RunInWorkspace.WarmupTimeoutSeconds,
				"FR-013: warmup_timeout_seconds must be %d for initial=%d",
				tc.want, tc.initial)
		})
	}

	// Differentiation: two different explicit values produce two different outputs.
	t1 := &ToolsConfig{}
	t1.RunInWorkspace.WarmupTimeoutSeconds = 30
	t1.ApplyWarmupTimeoutDefault()

	t2 := &ToolsConfig{}
	t2.RunInWorkspace.WarmupTimeoutSeconds = 90
	t2.ApplyWarmupTimeoutDefault()

	assert.NotEqual(t, t1.RunInWorkspace.WarmupTimeoutSeconds,
		t2.RunInWorkspace.WarmupTimeoutSeconds,
		"Different explicit values must produce different outputs (differentiation test)")
}
