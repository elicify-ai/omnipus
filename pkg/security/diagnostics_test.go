// Omnipus — exec-egress diagnostic tests.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// TestCheckExecEgress_WarningMatrix pins both outcomes of every input the
// check consults: the disabled-tool short circuit, the proxy-off warning
// trigger, and the fully healthy configuration.
func TestCheckExecEgress_WarningMatrix(t *testing.T) {
	tests := []struct {
		name      string
		cfg       security.DiagnosticConfig
		wantCodes []string
	}{
		{
			name:      "disabled tool short-circuits even with proxy off",
			cfg:       security.DiagnosticConfig{ExecToolEnabled: false},
			wantCodes: nil,
		},
		{
			name:      "proxy off yields SEC-29",
			cfg:       security.DiagnosticConfig{ExecToolEnabled: true, ExecProxyEnabled: false},
			wantCodes: []string{"SEC-29"},
		},
		{
			name:      "proxy on is healthy",
			cfg:       security.DiagnosticConfig{ExecToolEnabled: true, ExecProxyEnabled: true},
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
