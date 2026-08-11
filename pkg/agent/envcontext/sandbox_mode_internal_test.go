// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Internal test: renderSandboxMode is unexported, and the external
// envcontext_test package cannot reach it.
package envcontext

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/assert"
)

// TestRenderSandboxMode pins every branch of the spec-mandated mapping
// (env-awareness-and-memory-spec.md, "renderSandboxMode mapping (pinned)").
//
// The seatbelt row is the reason this file exists. When ADR-052 Phase-3 AC-6
// made darwin select a real kernel backend, the mapping had no case for it and
// silently returned "unknown" — so every macOS agent was told its sandbox state
// was unknown while it was actually confined. Nothing failed; the string was
// simply wrong in the agent's own system prompt.
func TestRenderSandboxMode(t *testing.T) {
	tests := []struct {
		name   string
		status sandbox.Status
		want   string
	}{
		{
			name:   "no backend and unavailable is off",
			status: sandbox.Status{Backend: "none", Available: false},
			want:   "off",
		},
		{
			name:   "linux with kernel enforcement reports the landlock ABI",
			status: sandbox.Status{Backend: "linux", KernelLevel: true, ABIVersion: 4},
			want:   "landlock-abi-4",
		},
		{
			name:   "linux without kernel enforcement degrades to fallback",
			status: sandbox.Status{Backend: "linux", KernelLevel: false},
			want:   "fallback",
		},
		{
			name:   "fallback backend reports fallback",
			status: sandbox.Status{Backend: "fallback"},
			want:   "fallback",
		},
		{
			name:   "seatbelt backend reports seatbelt, not unknown",
			status: sandbox.Status{Backend: "seatbelt", Available: true},
			want:   "seatbelt",
		},
		{
			name: "seatbelt reports seatbelt even though it has no ABI version",
			// Seatbelt exposes no versioned ABI, so ABIVersion stays 0. That
			// must not push it down into the unknown branch.
			status: sandbox.Status{Backend: "seatbelt", Available: true, KernelLevel: true, ABIVersion: 0},
			want:   "seatbelt",
		},
		{
			name:   "an unrecognised backend is reported as unknown",
			status: sandbox.Status{Backend: "brand-new-backend"},
			want:   "unknown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, renderSandboxMode(tc.status))
		})
	}
}
