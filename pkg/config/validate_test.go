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
// BDD: Given an agent config with policies: {"search_web": ""},
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
				Policies: map[string]ToolPolicy{
					"search_web": "", // empty policy value: invalid per FR-085
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

// --- F11: RepairMultipleDefaults ---

// TestRepairMultipleDefaults_NoOp verifies that a config with at most one
// Default=true is untouched.
//
// BDD: Given a config with zero Default=true agents,
//
//	When RepairMultipleDefaults is called,
//	Then no agent's Default flag changes.
//
// Traces to: pkg/config/validate.go — RepairMultipleDefaults (F11).
func TestRepairMultipleDefaults_NoOp(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{ID: "alpha", Default: false},
				{ID: "beta", Default: false},
			},
		},
	}
	RepairMultipleDefaults(cfg)

	for _, a := range cfg.Agents.List {
		if a.Default {
			t.Errorf("agent %q: Default changed to true unexpectedly", a.ID)
		}
	}
}

// TestRepairMultipleDefaults_SingleDefault_Unchanged verifies that a config
// with exactly one Default=true is unchanged.
//
// BDD: Given a config with exactly one Default=true agent ("mia"),
//
//	When RepairMultipleDefaults is called,
//	Then "mia".Default remains true and no other agent changes.
//
// Traces to: pkg/config/validate.go — RepairMultipleDefaults (F11).
func TestRepairMultipleDefaults_SingleDefault_Unchanged(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{ID: "alpha", Default: false},
				{ID: "mia", Default: true},
			},
		},
	}
	RepairMultipleDefaults(cfg)

	var defaults []string
	for _, a := range cfg.Agents.List {
		if a.Default {
			defaults = append(defaults, a.ID)
		}
	}
	require.Len(t, defaults, 1, "exactly one Default=true must remain after repair")
	assert.Equal(t, "mia", defaults[0])
}

// TestRepairMultipleDefaults_MultiDefault_KeepsFirst verifies that when
// multiple agents are marked Default=true, only the first (in list order) is
// retained and the others are cleared.
//
// BDD: Given a config with three Default=true agents ("alpha", "mia", "gamma"),
//
//	When RepairMultipleDefaults is called,
//	Then "alpha".Default remains true;
//	And "mia".Default and "gamma".Default are false;
//	And total Default=true count is 1.
//
// Traces to: pkg/config/validate.go — RepairMultipleDefaults (F11).
func TestRepairMultipleDefaults_MultiDefault_KeepsFirst(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{ID: "alpha", Default: true},
				{ID: "mia", Default: true},
				{ID: "gamma", Default: true},
			},
		},
	}
	RepairMultipleDefaults(cfg)

	var kept []string
	var cleared []string
	for _, a := range cfg.Agents.List {
		if a.Default {
			kept = append(kept, a.ID)
		} else {
			cleared = append(cleared, a.ID)
		}
	}

	require.Len(t, kept, 1, "exactly one Default=true must remain after repair")
	assert.Equal(t, "alpha", kept[0], "first agent in list order must be kept as default")
	assert.ElementsMatch(t, []string{"mia", "gamma"}, cleared, "remaining agents must have Default cleared")
}

// TestRepairMultipleDefaults_NilConfig_NoPanic verifies that a nil config
// is handled safely.
//
// Traces to: pkg/config/validate.go — RepairMultipleDefaults nil guard (F11).
func TestRepairMultipleDefaults_NilConfig_NoPanic(t *testing.T) {
	// Must not panic.
	RepairMultipleDefaults(nil)
}

// --- ValidateToolPolicyCoverage tests ---

// TestValidateToolPolicyCoverage_FullyCovered_NoGaps verifies that when every
// known tool has an explicit entry (global or per-agent) for every agent, the
// function returns no gaps.
func TestValidateToolPolicyCoverage_FullyCovered_NoGaps(t *testing.T) {
	cfg := &Config{
		Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"read_file": "allow"},
		},
		Agents: AgentsConfig{
			List: []AgentConfig{
				{
					ID: "agent-a",
					Tools: &AgentToolsCfg{
						Builtin: AgentBuiltinToolsCfg{
							Policies: map[string]ToolPolicy{"write_file": ToolPolicyAsk},
						},
					},
				},
			},
		},
	}
	knownTools := map[string]struct{}{"read_file": {}, "write_file": {}}

	gaps := ValidateToolPolicyCoverage(cfg, knownTools)
	assert.Empty(t, gaps, "every known tool is covered globally or per-agent")
}

// TestValidateToolPolicyCoverage_AgentMissingOneTool_ReportsGap verifies that a
// tool covered by neither the global map nor the agent's own map is reported.
func TestValidateToolPolicyCoverage_AgentMissingOneTool_ReportsGap(t *testing.T) {
	cfg := &Config{
		Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"read_file": "allow"},
		},
		Agents: AgentsConfig{
			List: []AgentConfig{
				{
					ID: "agent-b",
					Tools: &AgentToolsCfg{
						Builtin: AgentBuiltinToolsCfg{
							Policies: map[string]ToolPolicy{}, // no entry for write_file
						},
					},
				},
			},
		},
	}
	knownTools := map[string]struct{}{"read_file": {}, "write_file": {}}

	gaps := ValidateToolPolicyCoverage(cfg, knownTools)
	require.Len(t, gaps, 1, "write_file has no global or per-agent entry")
	assert.Equal(t, "agent-b", gaps[0].AgentID)
	assert.Equal(t, "write_file", gaps[0].ToolName)
}

// TestValidateToolPolicyCoverage_GlobalEntryCoversGap verifies that a global
// sandbox.tool_policies entry is sufficient coverage even when the agent has
// no Tools config at all (nil).
func TestValidateToolPolicyCoverage_GlobalEntryCoversGap(t *testing.T) {
	cfg := &Config{
		Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{
				"read_file":  "allow",
				"write_file": "ask",
			},
		},
		Agents: AgentsConfig{
			List: []AgentConfig{
				{ID: "agent-c", Tools: nil},
			},
		},
	}
	knownTools := map[string]struct{}{"read_file": {}, "write_file": {}}

	gaps := ValidateToolPolicyCoverage(cfg, knownTools)
	assert.Empty(t, gaps, "global entries alone are sufficient coverage")
}

// TestValidateToolPolicyCoverage_CaseSensitive_NoFalseMatch verifies that tool
// name matching is exact and case-sensitive: an entry for "Read_File" does not
// cover "read_file".
func TestValidateToolPolicyCoverage_CaseSensitive_NoFalseMatch(t *testing.T) {
	cfg := &Config{
		Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"Read_File": "allow"}, // wrong case
		},
		Agents: AgentsConfig{
			List: []AgentConfig{
				{ID: "agent-d", Tools: nil},
			},
		},
	}
	knownTools := map[string]struct{}{"read_file": {}}

	gaps := ValidateToolPolicyCoverage(cfg, knownTools)
	require.Len(t, gaps, 1, "case-mismatched key must not count as coverage")
	assert.Equal(t, "read_file", gaps[0].ToolName)
}

// TestValidateToolPolicyCoverage_WildcardDoesNotCountAsCoverage verifies that a
// wildcard entry (e.g. "system.*") does NOT satisfy coverage for an exact tool
// name — CLAUDE.md hard constraint 6 requires a literal, wildcard-free entry
// for the static builtin catalog.
func TestValidateToolPolicyCoverage_WildcardDoesNotCountAsCoverage(t *testing.T) {
	cfg := &Config{
		Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"system.*": "deny"},
		},
		Agents: AgentsConfig{
			List: []AgentConfig{
				{ID: "agent-e", Tools: nil},
			},
		},
	}
	knownTools := map[string]struct{}{"system.agent.list": {}}

	gaps := ValidateToolPolicyCoverage(cfg, knownTools)
	require.Len(t, gaps, 1, "a wildcard entry must not count as literal coverage")
	assert.Equal(t, "system.agent.list", gaps[0].ToolName)
}

// TestValidateToolPolicyCoverage_NilOrEmptyInputs_NoGaps verifies the
// "nothing to check" guard: a nil cfg or an empty/nil knownTools set returns
// no gaps rather than treating it as a universal gap.
func TestValidateToolPolicyCoverage_NilOrEmptyInputs_NoGaps(t *testing.T) {
	assert.Empty(t, ValidateToolPolicyCoverage(nil, map[string]struct{}{"read_file": {}}))

	cfg := &Config{Agents: AgentsConfig{List: []AgentConfig{{ID: "agent-f"}}}}
	assert.Empty(t, ValidateToolPolicyCoverage(cfg, nil))
	assert.Empty(t, ValidateToolPolicyCoverage(cfg, map[string]struct{}{}))
}

// TestValidateToolPolicyCoverage_MultipleAgentsIndependentGaps verifies that
// coverage is checked independently per agent: one agent's coverage does not
// satisfy another agent's requirement for the same tool.
func TestValidateToolPolicyCoverage_MultipleAgentsIndependentGaps(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{
					ID: "covered-agent",
					Tools: &AgentToolsCfg{
						Builtin: AgentBuiltinToolsCfg{
							Policies: map[string]ToolPolicy{"read_file": ToolPolicyAllow},
						},
					},
				},
				{ID: "uncovered-agent", Tools: nil},
			},
		},
	}
	knownTools := map[string]struct{}{"read_file": {}}

	gaps := ValidateToolPolicyCoverage(cfg, knownTools)
	require.Len(t, gaps, 1, "only the uncovered agent should be reported")
	assert.Equal(t, "uncovered-agent", gaps[0].AgentID)
}

// TestValidateToolPolicyCoverage_BothSidesPresentDifferentValues_NotAGap
// verifies the documented, intentional semantic: coverage checks only the
// PRESENCE of an explicit entry on either side (global or per-agent) — never
// agreement between the two values. A tool with a global "ask" entry and a
// conflicting per-agent "deny" entry for the same agent is fully covered,
// NOT a gap. This is deliberate: per-agent overrides exist precisely to let
// an agent diverge from the global default, so a future "fix" that started
// requiring value agreement between the two layers would silently break
// that override mechanism. This test locks the current (correct) behavior
// in so such a regression fails loudly.
func TestValidateToolPolicyCoverage_BothSidesPresentDifferentValues_NotAGap(t *testing.T) {
	cfg := &Config{
		Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"read_file": "ask"},
		},
		Agents: AgentsConfig{
			List: []AgentConfig{
				{
					ID: "agent-h",
					Tools: &AgentToolsCfg{
						Builtin: AgentBuiltinToolsCfg{
							// Deliberately disagrees with the global "ask" value above.
							Policies: map[string]ToolPolicy{"read_file": ToolPolicyDeny},
						},
					},
				},
			},
		},
	}
	knownTools := map[string]struct{}{"read_file": {}}

	gaps := ValidateToolPolicyCoverage(cfg, knownTools)
	assert.Empty(t, gaps, "mere presence of an entry on either layer satisfies coverage; the two layers are not required to agree on a value")
}

// --- RepairIncompleteToolPolicyCoverage tests ---

// TestRepairIncompleteToolPolicyCoverage_BackfillsMissingToDeny simulates a
// pre-existing installation whose on-disk agent config predates the removal
// of the DefaultPolicy/default_policy fallback: the agent's
// Tools.Builtin.Policies map is genuinely sparse (only a handful of entries,
// as if every unlisted tool used to fall through to a deleted default).
// After RepairIncompleteToolPolicyCoverage runs, every gap must be backfilled
// to "deny", pre-existing entries must survive untouched, and a subsequent
// ValidateToolPolicyCoverage call must find zero gaps.
func TestRepairIncompleteToolPolicyCoverage_BackfillsMissingToDeny(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{
					ID: "legacy-agent",
					Tools: &AgentToolsCfg{
						Builtin: AgentBuiltinToolsCfg{
							// Sparse: only 3 of the 6 known tools have an entry, as a
							// pre-DefaultPolicy-removal config would have.
							Policies: map[string]ToolPolicy{
								"read_file":  ToolPolicyAllow,
								"write_file": ToolPolicyAsk,
								"bash":       ToolPolicyAllow,
							},
						},
					},
				},
			},
		},
	}
	knownTools := map[string]struct{}{
		"read_file": {}, "write_file": {}, "bash": {},
		"delete_agent": {}, "create_agent": {}, "send_message": {},
	}

	repaired := RepairIncompleteToolPolicyCoverage(cfg, knownTools)
	assert.Equal(t, 1, repaired, "exactly one agent should have been repaired")

	agentPolicies := cfg.Agents.List[0].Tools.Builtin.Policies

	// Pre-existing entries survive untouched.
	assert.Equal(t, ToolPolicyAllow, agentPolicies["read_file"], "pre-existing entry must be untouched")
	assert.Equal(t, ToolPolicyAsk, agentPolicies["write_file"], "pre-existing entry must be untouched")
	assert.Equal(t, ToolPolicyAllow, agentPolicies["bash"], "pre-existing entry must be untouched")

	// Every gap is backfilled to "deny" (the safe, fail-closed direction).
	for _, name := range []string{"delete_agent", "create_agent", "send_message"} {
		assert.Equal(t, ToolPolicyDeny, agentPolicies[name], "gap for %q must be backfilled to deny", name)
	}

	// The subsequent hard-validation pass must now find zero gaps.
	gaps := ValidateToolPolicyCoverage(cfg, knownTools)
	assert.Empty(t, gaps, "after repair, coverage validation must find no remaining gaps")
}

// TestRepairIncompleteToolPolicyCoverage_Idempotent verifies that repairing
// an already-fully-covered config is a no-op: repairedAgentCount is 0 and no
// existing policy values change.
func TestRepairIncompleteToolPolicyCoverage_Idempotent(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{
					ID: "fully-covered-agent",
					Tools: &AgentToolsCfg{
						Builtin: AgentBuiltinToolsCfg{
							Policies: map[string]ToolPolicy{
								"read_file":  ToolPolicyAllow,
								"write_file": ToolPolicyDeny,
							},
						},
					},
				},
			},
		},
	}
	knownTools := map[string]struct{}{"read_file": {}, "write_file": {}}

	// First call repairs nothing (already covered).
	repaired := RepairIncompleteToolPolicyCoverage(cfg, knownTools)
	assert.Equal(t, 0, repaired, "an already-fully-covered config must not be reported as repaired")
	assert.Equal(t, ToolPolicyAllow, cfg.Agents.List[0].Tools.Builtin.Policies["read_file"])
	assert.Equal(t, ToolPolicyDeny, cfg.Agents.List[0].Tools.Builtin.Policies["write_file"])

	// Calling it again is still a no-op.
	repairedAgain := RepairIncompleteToolPolicyCoverage(cfg, knownTools)
	assert.Equal(t, 0, repairedAgain, "repeated repair calls on a covered config must stay a no-op")
}

// TestRepairIncompleteToolPolicyCoverage_NilOrEmptyInputs_NoOp verifies the
// "nothing to repair" guard mirrors ValidateToolPolicyCoverage's own guard.
func TestRepairIncompleteToolPolicyCoverage_NilOrEmptyInputs_NoOp(t *testing.T) {
	assert.Equal(t, 0, RepairIncompleteToolPolicyCoverage(nil, map[string]struct{}{"read_file": {}}))

	cfg := &Config{Agents: AgentsConfig{List: []AgentConfig{{ID: "agent-i"}}}}
	assert.Equal(t, 0, RepairIncompleteToolPolicyCoverage(cfg, nil))
	assert.Equal(t, 0, RepairIncompleteToolPolicyCoverage(cfg, map[string]struct{}{}))
}
