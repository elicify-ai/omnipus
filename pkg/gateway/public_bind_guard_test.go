//go:build !cgo

// Contract test: Plan 3 §1 acceptance decision — binding to a public IP with no
// users and no dev_mode_bypass must cause a fatal boot error.
//
// BDD: Given gateway config with host=0.0.0.0, no users, dev_mode_bypass=false,
//
//	When the gateway validates its boot config, Then it must return a fatal error.
//
// Acceptance decision: Plan 3 §1 "Public-IP binding with no users and no dev_mode_bypass: fatal at boot"
// Traces to: temporal-puzzling-melody.md §4 F33 / Plan 4 A5 scope
//
// Status: guard not yet implemented in RunContext.
// This file is intentionally kept as a non-fatal skip (not t.Fatal) per the
// Plan 4 A5 instructions: "Option (a) — skip with t.Skip + file an issue."
// GitHub issue: #115 — "Add public-bind guard: reject host=0.0.0.0 + no users + no dev_mode_bypass at boot"
//
// When the guard IS implemented, replace the t.Skip calls below with real
// assertions against RunContext returning a non-nil error with a message
// containing "unsafe public binding".

package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestPublicBindNoAuthFatal documents the expected fatal behavior when a
// gateway is configured to listen on a public interface with no authentication.
//
// The positive test cases (loopback bind, dev_mode_bypass=true, users present)
// are also listed to clarify the acceptance criteria for when the guard lands.
//
// Traces to: temporal-puzzling-melody.md §4 Axis-1 F33 — TestPublicBindGuard
func TestPublicBindNoAuthFatal(t *testing.T) {
	t.Skip(
		"BLOCKED: production guard not yet implemented — " +
			"RunContext must reject host=0.0.0.0 + no users + no dev_mode_bypass. " +
			"Track at GitHub issue #115.",
	)
}

// TestPublicBindGuard_Cases documents all acceptance criteria for the guard.
// Each sub-test either asserts the guard fires (dangerous config) or does not
// fire (safe config). All sub-tests skip until the guard is implemented.
//
// Traces to: temporal-puzzling-melody.md §4 F33
func TestPublicBindGuard_Cases(t *testing.T) {
	type testCase struct {
		name        string
		cfg         config.GatewayConfig
		expectFatal bool // true = guard must return an error
		skipReason  string
	}

	cases := []testCase{
		{
			name: "public_bind_no_users_no_bypass_must_fatal",
			cfg: config.GatewayConfig{
				Host:          "0.0.0.0",
				DevModeBypass: false,
				Users:         nil,
			},
			expectFatal: true,
			skipReason:  "BLOCKED: guard not implemented — see GitHub issue #115",
		},
		{
			name: "loopback_bind_no_users_no_bypass_safe",
			cfg: config.GatewayConfig{
				Host:          "127.0.0.1",
				DevModeBypass: false,
				Users:         nil,
			},
			expectFatal: false,
			skipReason:  "BLOCKED: guard not implemented — see GitHub issue #115",
		},
		{
			name: "loopback_ipv6_bind_no_users_no_bypass_safe",
			cfg: config.GatewayConfig{
				Host:          "::1",
				DevModeBypass: false,
				Users:         nil,
			},
			expectFatal: false,
			skipReason:  "BLOCKED: guard not implemented — see GitHub issue #115",
		},
		{
			name: "public_bind_dev_mode_bypass_safe",
			cfg: config.GatewayConfig{
				Host:          "0.0.0.0",
				DevModeBypass: true,
				Users:         nil,
			},
			expectFatal: false,
			skipReason:  "BLOCKED: guard not implemented — see GitHub issue #115",
		},
		{
			name: "public_bind_with_users_safe",
			cfg: config.GatewayConfig{
				Host:          "0.0.0.0",
				DevModeBypass: false,
				Users: []config.UserConfig{
					{Username: "admin"},
				},
			},
			expectFatal: false,
			skipReason:  "BLOCKED: guard not implemented — see GitHub issue #115",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Skip(tc.skipReason)

			// When the guard is implemented, call the boot-validation function here.
			// Example (adjust to match the actual function signature when it exists):
			//
			//   err := validateBootConfig(&tc.cfg)
			//   if tc.expectFatal {
			//       require.Error(t, err, "expected fatal error for dangerous public bind config")
			//       require.Contains(t, err.Error(), "unsafe public binding")
			//   } else {
			//       require.NoError(t, err, "expected no error for safe config")
			//   }
			_ = tc.cfg
			_ = tc.expectFatal
		})
	}
}
