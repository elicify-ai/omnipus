// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- helpers ---

// writeAgentConfig creates a valid agent config JSON at agentsDir/<id>/agent.json.
func writeAgentConfig(t *testing.T, agentsDir, agentID string, ac AgentConfig) {
	t.Helper()
	dir := filepath.Join(agentsDir, agentID)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	data, err := json.Marshal(ac)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.json"), data, 0o644))
}

// noopAudit implements AuditEmitter as a no-op for tests that don't care about audit output.
type noopAudit struct{}

func (n *noopAudit) EmitRaw(event, severity string, fields map[string]any) error { return nil }

// captureAudit records emitted boot events for assertion.
type captureAudit struct {
	events []struct {
		event    string
		severity string
		fields   map[string]any
	}
}

func (c *captureAudit) EmitRaw(event, severity string, fields map[string]any) error {
	c.events = append(c.events, struct {
		event    string
		severity string
		fields   map[string]any
	}{event: event, severity: severity, fields: fields})
	return nil
}

// --- TestValidPolicy ---

// TestValidPolicy verifies that ValidPolicy accepts only "allow", "ask", "deny"
// and rejects empty string (FR-085).
//
// BDD: Given each policy value,
//
//	When ValidPolicy is called,
//	Then "allow"/"ask"/"deny" return true; empty and others return false.
//
// Traces to: pkg/config/validate.go — ValidPolicy (FR-085).
func TestValidPolicy_AcceptsAllowAskDeny(t *testing.T) {
	assert.True(t, ValidPolicy(ToolPolicyAllow), "allow must be valid")
	assert.True(t, ValidPolicy(ToolPolicyAsk), "ask must be valid")
	assert.True(t, ValidPolicy(ToolPolicyDeny), "deny must be valid")
}

func TestValidPolicy_RejectsEmptyAndUnknown(t *testing.T) {
	assert.False(t, ValidPolicy(""), "empty string must be invalid (FR-085)")
	assert.False(t, ValidPolicy("maybe"), "unknown string must be invalid")
	assert.False(t, ValidPolicy("ALLOW"), "case-sensitive: uppercase must be invalid")
}

// --- TestConfigValidator_ValidAgentConfig ---

// TestConfigValidator_ValidAgentConfig verifies that a well-formed agent config
// passes validation with no abort.
//
// BDD: Given a valid agent config file on disk,
//
//	When ValidateAgentConfigs is called,
//	Then it returns results with OK=true for that agent;
//	And abortBoot is false.
//
// Traces to: pkg/config/validate.go — ValidateAgentConfigs (FR-023).
func TestConfigValidator_ValidAgentConfig(t *testing.T) {
	agentsDir := t.TempDir()
	writeAgentConfig(t, agentsDir, "my-agent", AgentConfig{
		ID: "my-agent",
		Tools: &AgentToolsCfg{
			Builtin: AgentBuiltinToolsCfg{
				DefaultPolicy: ToolPolicyAllow,
				Policies: map[string]ToolPolicy{
					"system.*": ToolPolicyDeny,
				},
			},
		},
	})

	results, abort := ValidateAgentConfigs(agentsDir, func(id string) bool { return false }, nil, &noopAudit{})

	assert.False(t, abort, "valid config must not abort boot")
	require.Len(t, results, 1)
	assert.True(t, results[0].Valid, "valid agent must have OK=true")
	assert.Equal(t, "my-agent", results[0].AgentID)
}

// TestConfigValidator_EmptyPolicyValue_RejectsConfig verifies that a policy map
// with an empty string value fails validation (FR-085).
//
// BDD: Given an agent config with policies: {"web_search": ""},
//
//	When ValidateAgentConfigs is called,
//	Then the agent's result has OK=false and Error is non-empty.
//
// Traces to: pkg/config/validate.go — empty policy value check (FR-085).
func TestConfigValidator_EmptyPolicyValue_RejectsConfig(t *testing.T) {
	agentsDir := t.TempDir()
	writeAgentConfig(t, agentsDir, "bad-agent", AgentConfig{
		ID: "bad-agent",
		Tools: &AgentToolsCfg{
			Builtin: AgentBuiltinToolsCfg{
				DefaultPolicy: ToolPolicyAllow,
				Policies: map[string]ToolPolicy{
					"web_search": "", // empty policy value: invalid per FR-085
				},
			},
		},
	})

	results, _ := ValidateAgentConfigs(agentsDir, func(id string) bool { return false }, nil, &noopAudit{})

	require.Len(t, results, 1)
	assert.False(t, results[0].Valid, "empty policy value must produce Valid=false (FR-085)")
	assert.NotEmpty(t, results[0].PolicyErrors, "PolicyErrors must describe the problem")
}

// TestConfigValidator_CorruptCustom_SkipsAndAudits verifies that an unparseable
// custom agent config is skipped (boot continues) with a HIGH audit event (FR-049).
//
// BDD: Given an agent config file containing invalid JSON,
//
//	When ValidateAgentConfigs is called and hasSystemAllows returns false,
//	Then abortBoot is false (boot continues);
//	And the audit emitter receives a HIGH-severity boot event.
//
// Traces to: pkg/config/validate.go — corrupt custom agent disposition (FR-049).
func TestConfigValidator_CorruptCustom_SkipsAndAudits(t *testing.T) {
	agentsDir := t.TempDir()
	dir := filepath.Join(agentsDir, "corrupt-agent")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.json"), []byte("{invalid json}"), 0o644))

	audit := &captureAudit{}
	results, abort := ValidateAgentConfigs(agentsDir, func(id string) bool { return false }, nil, audit)

	assert.False(t, abort, "corrupt custom agent must NOT abort boot (FR-049)")
	require.Len(t, results, 1)
	assert.False(t, results[0].Valid)
	assert.NotEmpty(t, audit.events, "audit emitter must receive at least one event")
}

// TestConfigValidator_CorruptCore_AbortsBoot verifies that a corrupt config for
// an agent with HasSystemAllowsInConstructorSeed == true aborts boot (FR-049).
//
// BDD: Given a corrupt config for agent "ava" (which has system allows),
//
//	When ValidateAgentConfigs is called with hasSystemAllows("ava") == true,
//	Then abortBoot is true.
//
// Traces to: pkg/config/validate.go — corrupt core agent disposition (FR-049, FR-062).
func TestConfigValidator_CorruptCore_AbortsBoot(t *testing.T) {
	agentsDir := t.TempDir()
	dir := filepath.Join(agentsDir, "ava")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.json"), []byte("{invalid}"), 0o644))

	_, abort := ValidateAgentConfigs(agentsDir,
		func(id string) bool { return id == "ava" }, // ava has system allows
		nil,
		&noopAudit{},
	)

	assert.True(t, abort, "corrupt config for agent with system allows must abort boot (FR-049, FR-062)")
}

// TestConfigValidator_EmptyDir_NoResults verifies that an empty agents directory
// produces zero results and does not abort.
//
// BDD: Given an empty agents directory,
//
//	When ValidateAgentConfigs is called,
//	Then results is empty and abortBoot is false.
//
// Traces to: pkg/config/validate.go — empty directory guard.
func TestConfigValidator_EmptyDir_NoResults(t *testing.T) {
	agentsDir := t.TempDir()

	results, abort := ValidateAgentConfigs(agentsDir, func(string) bool { return false }, nil, &noopAudit{})

	assert.False(t, abort)
	assert.Empty(t, results)
}

// TestConfigValidator_MissingDir_NoResults verifies that a non-existent agents
// directory is treated gracefully (no error, no abort).
//
// BDD: Given a path that does not exist on disk,
//
//	When ValidateAgentConfigs is called,
//	Then results is empty and abortBoot is false.
//
// Traces to: pkg/config/validate.go — missing directory guard.
func TestConfigValidator_MissingDir_NoResults(t *testing.T) {
	results, abort := ValidateAgentConfigs(
		"/nonexistent/path/to/agents",
		func(string) bool { return false },
		nil,
		&noopAudit{},
	)

	assert.False(t, abort)
	assert.Empty(t, results)
}
