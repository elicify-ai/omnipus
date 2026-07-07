// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package policy_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/policy"
)

// TestIsSystemAgent_PrivilegedAgentTypeDetection verifies privileges flow from
// agent type, not from a hardcoded ID (FR-045).
// Traces to: wave2-security-layer-spec.md line 183 (IsSystemAgent exemption)
func TestIsSystemAgent_PrivilegedAgentTypeDetection(t *testing.T) {
	assert.True(t, policy.IsSystemAgent("core"))
	assert.True(t, policy.IsSystemAgent("system"))
	assert.False(t, policy.IsSystemAgent("custom"))
	assert.False(t, policy.IsSystemAgent(""))
}
