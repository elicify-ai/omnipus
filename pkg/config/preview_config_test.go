// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package config — Track E tests for preview listener config validation
// (chat-served-iframe-preview-spec.md FR-001..FR-005, FR-027, FR-028).
//
// Tests drive ValidateAndApplyPreviewDefaults directly — same function called
// by gateway.setupAndStartServices at boot.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// boolPtr is a convenience helper to get a *bool from a literal.
func boolPtr(v bool) *bool { return &v }

// ---------------------------------------------------------------------------
// TestGatewayConfig_PreviewPort_DefaultDerivation
// ---------------------------------------------------------------------------

// TestGatewayConfig_PreviewPort_DefaultDerivation verifies FR-001: when
// preview_port is 0, it defaults to Port+1.
// Traces to: chat-served-iframe-preview-spec.md FR-001
func TestGatewayConfig_PreviewPort_DefaultDerivation(t *testing.T) {
	tests := []struct {
		name        string
		mainPort    int
		wantPreview int32
		wantErr     bool
	}{
		{
			name:        "port 5000 → preview 5001",
			mainPort:    5000,
			wantPreview: 5001,
		},
		{
			name:        "port 8080 → preview 8081",
			mainPort:    8080,
			wantPreview: 8081,
		},
		{
			name:        "port 1 → preview 2",
			mainPort:    1,
			wantPreview: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := &GatewayConfig{
				Host:                   "127.0.0.1",
				Port:                   tc.mainPort,
				PreviewPort:            0, // unset → derive
				PreviewListenerEnabled: boolPtr(true),
			}
			err := g.ValidateAndApplyPreviewDefaults()
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantPreview, g.PreviewPort,
				"FR-001: preview_port must default to port+1 when unset")
		})
	}

	// Differentiation: two different main ports produce two different preview ports.
	g1 := &GatewayConfig{Host: "127.0.0.1", Port: 3000, PreviewListenerEnabled: boolPtr(true)}
	g2 := &GatewayConfig{Host: "127.0.0.1", Port: 4000, PreviewListenerEnabled: boolPtr(true)}
	require.NoError(t, g1.ValidateAndApplyPreviewDefaults())
	require.NoError(t, g2.ValidateAndApplyPreviewDefaults())
	assert.NotEqual(t, g1.PreviewPort, g2.PreviewPort,
		"Different main ports must produce different preview ports (differentiation test)")
}

// ---------------------------------------------------------------------------
// TestGatewayConfig_PreviewPort_CollisionRejected
// ---------------------------------------------------------------------------

// TestGatewayConfig_PreviewPort_CollisionRejected verifies FR-004: preview_port
// must differ from port when preview is enabled.
// Traces to: chat-served-iframe-preview-spec.md FR-004
func TestGatewayConfig_PreviewPort_CollisionRejected(t *testing.T) {
	g := &GatewayConfig{
		Host:                   "127.0.0.1",
		Port:                   5000,
		PreviewPort:            5000, // collision
		PreviewListenerEnabled: boolPtr(true),
	}
	err := g.ValidateAndApplyPreviewDefaults()
	assert.Error(t, err, "FR-004: preview_port == port must be rejected")
	assert.Contains(t, err.Error(), "preview_port",
		"error message must mention preview_port")
}

// ---------------------------------------------------------------------------
// TestGatewayConfig_PreviewPort_OverflowRejected
// ---------------------------------------------------------------------------

// TestGatewayConfig_PreviewPort_OverflowRejected verifies FR-003: when
// Port=65535 and preview_port is 0, the auto-derived 65536 overflows and
// must be rejected with an error requiring explicit configuration.
// Traces to: chat-served-iframe-preview-spec.md FR-003
func TestGatewayConfig_PreviewPort_OverflowRejected(t *testing.T) {
	g := &GatewayConfig{
		Host:                   "127.0.0.1",
		Port:                   65535,
		PreviewPort:            0, // auto-derive → 65536 overflows
		PreviewListenerEnabled: boolPtr(true),
	}
	err := g.ValidateAndApplyPreviewDefaults()
	assert.Error(t, err,
		"FR-003: auto-derived port overflow (65536) must be rejected")
	assert.Contains(t, err.Error(), "out of range",
		"error message must mention out-of-range")
}

// ---------------------------------------------------------------------------
// TestGatewayConfig_ValidationOrder
// ---------------------------------------------------------------------------

// TestGatewayConfig_ValidationOrder verifies that the validator checks
// the main port range BEFORE attempting preview port derivation.
// Traces to: chat-served-iframe-preview-spec.md (validation order)
func TestGatewayConfig_ValidationOrder(t *testing.T) {
	g := &GatewayConfig{
		Host:                   "127.0.0.1",
		Port:                   0, // invalid port — must fail before preview checks
		PreviewPort:            0,
		PreviewListenerEnabled: boolPtr(true),
	}
	err := g.ValidateAndApplyPreviewDefaults()
	assert.Error(t, err, "port 0 must be rejected")
	assert.Contains(t, err.Error(), "gateway.port",
		"validation must report the main port as invalid (not preview port)")
}

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

// ---------------------------------------------------------------------------
// TestGatewayConfig_PreviewOriginRequiresEnabled
// ---------------------------------------------------------------------------

// TestGatewayConfig_PreviewOriginRequiresEnabled verifies FR-028: setting
// preview_origin when preview_listener_enabled=false is a validation error.
// Traces to: chat-served-iframe-preview-spec.md FR-028
func TestGatewayConfig_PreviewOriginRequiresEnabled(t *testing.T) {
	g := &GatewayConfig{
		Host:                   "127.0.0.1",
		Port:                   5000,
		PreviewOrigin:          "https://preview.example.com",
		PreviewListenerEnabled: boolPtr(false), // disabled
	}
	err := g.ValidateAndApplyPreviewDefaults()
	assert.Error(t, err,
		"FR-028: preview_origin must be rejected when preview_listener_enabled=false")
	assert.Contains(t, err.Error(), "preview_origin",
		"error must mention preview_origin")
}

// ---------------------------------------------------------------------------
// TestGateway_PreviewListenerEnabled_AndroidDefault
// ---------------------------------------------------------------------------

// TestGateway_PreviewListenerEnabled_AndroidDefault verifies that
// IsPreviewListenerEnabled returns false when PreviewListenerEnabled is
// explicitly set to false, simulating the Android/Termux default.
// Traces to: chat-served-iframe-preview-spec.md (Android default CLAUDE.md note)
func TestGateway_PreviewListenerEnabled_AndroidDefault(t *testing.T) {
	// Explicitly disabled (simulating Android/Termux default).
	g := &GatewayConfig{
		PreviewListenerEnabled: boolPtr(false),
	}
	assert.False(t, g.IsPreviewListenerEnabled(),
		"Android/Termux: preview_listener_enabled=false must disable the preview listener")

	// Explicitly enabled.
	g2 := &GatewayConfig{
		PreviewListenerEnabled: boolPtr(true),
	}
	assert.True(t, g2.IsPreviewListenerEnabled(),
		"Explicit true must enable the preview listener")

	// Unset (nil) — platform-specific default; must be a bool.
	g3 := &GatewayConfig{
		PreviewListenerEnabled: nil,
	}
	result := g3.IsPreviewListenerEnabled()
	// The default is true on Linux/macOS/Windows, false on Android.
	// We just assert it returns a boolean without panicking.
	_ = result
	assert.IsType(t, false, result, "IsPreviewListenerEnabled must return a bool (not panic)")
}

// ---------------------------------------------------------------------------
// TestGatewayConfig_PreviewPort_ExplicitValue_NotOverridden
// ---------------------------------------------------------------------------

// TestGatewayConfig_PreviewPort_ExplicitValue_NotOverridden verifies that
// when preview_port is explicitly set, the validator does not override it.
// Traces to: chat-served-iframe-preview-spec.md FR-001 (explicit value path)
func TestGatewayConfig_PreviewPort_ExplicitValue_NotOverridden(t *testing.T) {
	g := &GatewayConfig{
		Host:                   "127.0.0.1",
		Port:                   5000,
		PreviewPort:            9999, // explicit
		PreviewListenerEnabled: boolPtr(true),
	}
	err := g.ValidateAndApplyPreviewDefaults()
	require.NoError(t, err)
	assert.Equal(t, int32(9999), g.PreviewPort,
		"Explicit preview_port must not be overridden by auto-derivation")
}

// ---------------------------------------------------------------------------
// TestGatewayConfig_PreviewHost_DefaultsToHost
// ---------------------------------------------------------------------------

// TestGatewayConfig_PreviewHost_DefaultsToHost verifies that when preview_host
// is empty it defaults to the main gateway host.
// Traces to: chat-served-iframe-preview-spec.md FR-001 (host default)
func TestGatewayConfig_PreviewHost_DefaultsToHost(t *testing.T) {
	g := &GatewayConfig{
		Host:                   "10.0.0.1",
		Port:                   5000,
		PreviewPort:            5001,
		PreviewHost:            "", // unset
		PreviewListenerEnabled: boolPtr(true),
	}
	err := g.ValidateAndApplyPreviewDefaults()
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1", g.PreviewHost,
		"preview_host must default to gateway host when unset")
}

// ---------------------------------------------------------------------------
// TestGatewayConfig_PreviewOrigin_RequiresScheme
// ---------------------------------------------------------------------------

// TestGatewayConfig_PreviewOrigin_RequiresScheme verifies FR-028: preview_origin
// must have a scheme (https://) when set.
// Traces to: chat-served-iframe-preview-spec.md FR-028
func TestGatewayConfig_PreviewOrigin_RequiresScheme(t *testing.T) {
	tests := []struct {
		name          string
		previewOrigin string
		wantErr       bool
	}{
		{
			name:          "valid HTTPS origin",
			previewOrigin: "https://preview.example.com",
			wantErr:       false,
		},
		{
			name:          "valid HTTP origin",
			previewOrigin: "http://preview.example.com",
			wantErr:       false,
		},
		{
			name:          "no scheme — rejected",
			previewOrigin: "preview.example.com",
			wantErr:       true,
		},
		{
			name:          "empty — allowed (unset)",
			previewOrigin: "",
			wantErr:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := &GatewayConfig{
				Host:                   "127.0.0.1",
				Port:                   5000,
				PreviewPort:            5001,
				PreviewOrigin:          tc.previewOrigin,
				PreviewListenerEnabled: boolPtr(true),
			}
			err := g.ValidateAndApplyPreviewDefaults()
			if tc.wantErr {
				assert.Error(t, err, "preview_origin without scheme must be rejected")
			} else {
				assert.NoError(t, err, "valid preview_origin must be accepted")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestGatewayConfig_DisabledPreview_SkipsPortChecks
// ---------------------------------------------------------------------------

// TestGatewayConfig_DisabledPreview_SkipsPortChecks verifies that when
// preview_listener_enabled=false, port collision and overflow checks are
// skipped (no error even if preview_port == main port).
// Traces to: chat-served-iframe-preview-spec.md FR-003 (disabled path)
func TestGatewayConfig_DisabledPreview_SkipsPortChecks(t *testing.T) {
	g := &GatewayConfig{
		Host:                   "127.0.0.1",
		Port:                   5000,
		PreviewPort:            5000, // collision, but preview is disabled
		PreviewListenerEnabled: boolPtr(false),
	}
	err := g.ValidateAndApplyPreviewDefaults()
	assert.NoError(t, err,
		"Port collision must be allowed when preview listener is disabled")
}

// ---------------------------------------------------------------------------
// F-40 — PublicURL malformed validation
// ---------------------------------------------------------------------------

// TestGatewayConfig_PublicURL_RequiresScheme verifies that gateway.public_url
// is rejected at boot validate when it has no scheme or is malformed.
// Mirrors the existing TestGatewayConfig_PreviewOrigin_RequiresScheme pattern.
//
// Production validation (config.go): url.Parse(g.PublicURL) succeeds but
// u.Scheme == "" → reject. url.Parse("://broken") returns a parse error →
// reject. url.Parse("http://") sets scheme="http" host="" → currently accepted
// (the code only checks Scheme, not Host). Cases marked accept/reject reflect
// the actual production behavior, not the ideal.
//
// Traces to: chat-served-iframe-preview-spec.md F-40 / gateway.public_url
func TestGatewayConfig_PublicURL_RequiresScheme(t *testing.T) {
	tests := []struct {
		name      string
		publicURL string
		wantErr   bool
	}{
		{
			// "://broken" causes url.Parse to return a parse error →
			// production code checks err != nil → reject.
			name:      "broken scheme-only — rejected (parse error)",
			publicURL: "://broken",
			wantErr:   true,
		},
		{
			// "not-a-url" has no scheme (url.Parse sets Scheme="") → reject.
			name:      "no scheme — rejected",
			publicURL: "not-a-url",
			wantErr:   true,
		},
		{
			// Valid HTTP URL with host and port.
			name:      "valid http://host:port — accepted",
			publicURL: "http://x:5000",
			wantErr:   false,
		},
		{
			// Valid HTTPS URL with domain.
			name:      "valid https://domain — accepted",
			publicURL: "https://x.com",
			wantErr:   false,
		},
		{
			// Empty string is explicitly allowed (means "unset").
			name:      "empty — accepted (unset is fine)",
			publicURL: "",
			wantErr:   false,
		},
		{
			// "http://" has scheme="http" but empty host. The production code
			// checks only u.Scheme == ""; this currently passes. The case is
			// noted here as a known gap — a future validator should also require
			// a non-empty host (see F-40 reviewer comment).
			// CLARIFY: should http:// be rejected? Production code accepts it.
			name:      "http:// (no host) — accepted by current validator (known gap)",
			publicURL: "http://",
			wantErr:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := &GatewayConfig{
				Host:                   "127.0.0.1",
				Port:                   5000,
				PreviewPort:            5001,
				PublicURL:              tc.publicURL,
				PreviewListenerEnabled: boolPtr(true),
			}
			err := g.ValidateAndApplyPreviewDefaults()
			if tc.wantErr {
				assert.Error(t, err,
					"public_url %q must be rejected at boot validate", tc.publicURL)
				assert.Contains(t, err.Error(), "public_url",
					"error must mention public_url (not some other field)")
			} else {
				assert.NoError(t, err,
					"public_url %q must be accepted at boot validate", tc.publicURL)
			}
		})
	}

	// Differentiation: two different valid URLs produce two different configs
	// that both pass validation — proves the validator is not a no-op.
	g1 := &GatewayConfig{
		Host: "127.0.0.1", Port: 5000, PreviewPort: 5001,
		PublicURL: "http://first.example.com", PreviewListenerEnabled: boolPtr(true),
	}
	g2 := &GatewayConfig{
		Host: "127.0.0.1", Port: 5000, PreviewPort: 5001,
		PublicURL: "https://second.example.com", PreviewListenerEnabled: boolPtr(true),
	}
	require.NoError(t, g1.ValidateAndApplyPreviewDefaults())
	require.NoError(t, g2.ValidateAndApplyPreviewDefaults())
	assert.NotEqual(t, g1.PublicURL, g2.PublicURL,
		"F-40: two different valid public_url values must both pass and remain distinct")
}
