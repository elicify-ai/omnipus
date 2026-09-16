// Omnipus — exec-egress diagnostic tests.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// TestCheckExecEgress_WarningMatrix pins both outcomes of every input the
// check consults: the disabled-tool short circuit, each warning trigger on
// its own, both together, and the fully healthy configuration. The warning
// ORDER matters too — run_doctor renders findings in the order returned, so
// SEC-29 preceding SEC-05 is asserted, not just membership.
func TestCheckExecEgress_WarningMatrix(t *testing.T) {
	tests := []struct {
		name      string
		cfg       security.DiagnosticConfig
		wantCodes []string
	}{
		{
			name:      "disabled tool short-circuits even with proxy off and no allowlist",
			cfg:       security.DiagnosticConfig{ExecToolEnabled: false},
			wantCodes: nil,
		},
		{
			name:      "proxy off and no allowlist yields both warnings",
			cfg:       security.DiagnosticConfig{ExecToolEnabled: true, ExecProxyEnabled: false},
			wantCodes: []string{"SEC-29", "SEC-05"},
		},
		{
			name:      "proxy on but no allowlist yields only SEC-05",
			cfg:       security.DiagnosticConfig{ExecToolEnabled: true, ExecProxyEnabled: true},
			wantCodes: []string{"SEC-05"},
		},
		{
			name: "proxy off with allowlist yields only SEC-29",
			cfg: security.DiagnosticConfig{
				ExecToolEnabled:     true,
				ExecProxyEnabled:    false,
				ExecAllowedBinaries: []string{"/usr/bin/git"},
			},
			wantCodes: []string{"SEC-29"},
		},
		{
			name: "proxy on with allowlist is healthy",
			cfg: security.DiagnosticConfig{
				ExecToolEnabled:     true,
				ExecProxyEnabled:    true,
				ExecAllowedBinaries: []string{"/usr/bin/git"},
			},
			wantCodes: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := security.CheckExecEgress(tt.cfg)
			if len(got) != len(tt.wantCodes) {
				t.Fatalf("CheckExecEgress returned %d warning(s) %v, want %d %v",
					len(got), warningCodes(got), len(tt.wantCodes), tt.wantCodes)
			}
			for i, w := range got {
				if w.Code != tt.wantCodes[i] {
					t.Errorf("warning[%d].Code = %q, want %q", i, w.Code, tt.wantCodes[i])
				}
				if w.Message == "" {
					t.Errorf("warning[%d] (%s) carries an empty message; run_doctor's finding text comes from it",
						i, w.Code)
				}
			}
		})
	}
}

// warningCodes extracts the codes for failure messages only; it is not an
// assertion helper.
func warningCodes(ws []security.DiagnosticWarning) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.Code)
	}
	return out
}
