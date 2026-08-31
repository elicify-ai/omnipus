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

// NOTE (ADR-054 D6.4): the F11 RepairMultipleDefaults test suite that used
// to live here (TestRepairMultipleDefaults_NoOp,
// TestRepairMultipleDefaults_SingleDefault_Unchanged,
// TestRepairMultipleDefaults_MultiDefault_KeepsFirst,
// TestRepairMultipleDefaults_NilConfig_NoPanic) is retired along with the
// function itself — see validate.go's NOTE at the same location for why the
// at-most-one-Default invariant no longer needs enforcing. Coverage for the
// replacement (settings-singleton) resolution ladder lives in
// pkg/agent/registry_test.go (TestAgentRegistry_GetDefaultAgent_*) and
// pkg/routing/route_test.go (TestResolveRoute_DefaultAgentSelection*).

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
	assert.Empty(
		t,
		gaps,
		"mere presence of an entry on either layer satisfies coverage; the two layers are not required to agree on a value",
	)
}

// --- RepairIncompleteToolPolicyCoverage tests ---

// TestRepairIncompleteToolPolicyCoverage_BackfillsMissingToDeny simulates a
// pre-existing installation whose on-disk agent config predates the removal
// of the DefaultPolicy/default_policy fallback: the agent's
// Tools.Builtin.Policies map is genuinely sparse (only a handful of entries,
// as if every unlisted tool used to fall through to a deleted default).
// After RepairIncompleteToolPolicyCoverage runs, every gap must be backfilled
// to "deny", pre-existing entries must survive untouched, the returned
// []CoverageGap must name exactly the backfilled (agent, tool) pairs, and a
// subsequent ValidateToolPolicyCoverage call must find zero gaps.
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
	require.Len(t, repaired, 3, "exactly three gaps should have been repaired")

	wantGaps := []CoverageGap{
		{AgentID: "legacy-agent", ToolName: "create_agent"},
		{AgentID: "legacy-agent", ToolName: "delete_agent"},
		{AgentID: "legacy-agent", ToolName: "send_message"},
	}
	assert.ElementsMatch(t, wantGaps, repaired, "repaired must name exactly the backfilled (agent, tool) pairs")

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

// TestRepairIncompleteToolPolicyCoverage_GlobalEntryCoversGap_NoRedundantPerAgentEntry
// verifies that a tool already covered globally (cfg.Sandbox.ToolPolicies)
// does NOT get a redundant per-agent entry added by the repair — the repair
// must respect the same "either layer counts as covered" rule
// ValidateToolPolicyCoverage enforces, not just backfill everything
// unconditionally onto the agent.
func TestRepairIncompleteToolPolicyCoverage_GlobalEntryCoversGap_NoRedundantPerAgentEntry(t *testing.T) {
	cfg := &Config{
		Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"send_message": "allow"},
		},
		Agents: AgentsConfig{
			List: []AgentConfig{
				{
					ID: "agent-j",
					Tools: &AgentToolsCfg{
						Builtin: AgentBuiltinToolsCfg{
							Policies: map[string]ToolPolicy{"read_file": ToolPolicyAllow},
						},
					},
				},
			},
		},
	}
	knownTools := map[string]struct{}{"read_file": {}, "send_message": {}}

	repaired := RepairIncompleteToolPolicyCoverage(cfg, knownTools)
	assert.Empty(t, repaired, "a tool already covered globally must not be reported as repaired")

	agentPolicies := cfg.Agents.List[0].Tools.Builtin.Policies
	_, hasPerAgentEntry := agentPolicies["send_message"]
	assert.False(t, hasPerAgentEntry, "globally-covered tool must not gain a redundant per-agent entry")
	assert.Equal(t, ToolPolicyAllow, agentPolicies["read_file"], "pre-existing entry must be untouched")
}

// TestRepairIncompleteToolPolicyCoverage_DelegatesToValidate is a
// characterization test locking in that the repair function derives its gap
// list by CALLING ValidateToolPolicyCoverage rather than re-implementing the
// coverage predicate a second time. It exercises the exact
// "both-sides-present-different-values-not-a-gap" scenario
// TestValidateToolPolicyCoverage_BothSidesPresentDifferentValues_NotAGap locks
// in for ValidateToolPolicyCoverage: if the repair ever grew its own,
// independent notion of "covered" that disagreed with ValidateToolPolicyCoverage
// (the exact class of bug this whole session's work fixed elsewhere), this
// test would catch the divergence by asserting repair finds nothing to do
// here even though a naive re-implementation might.
func TestRepairIncompleteToolPolicyCoverage_DelegatesToValidate(t *testing.T) {
	cfg := &Config{
		Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"read_file": "ask"},
		},
		Agents: AgentsConfig{
			List: []AgentConfig{
				{
					ID: "agent-k",
					Tools: &AgentToolsCfg{
						Builtin: AgentBuiltinToolsCfg{
							// Deliberately disagrees with the global "ask" value above —
							// mere presence on either side satisfies coverage, so this must
							// NOT be treated as a gap by the repair either.
							Policies: map[string]ToolPolicy{"read_file": ToolPolicyDeny},
						},
					},
				},
			},
		},
	}
	knownTools := map[string]struct{}{"read_file": {}}

	// Sanity check: ValidateToolPolicyCoverage (the authority) finds no gap.
	require.Empty(t, ValidateToolPolicyCoverage(cfg, knownTools))

	repaired := RepairIncompleteToolPolicyCoverage(cfg, knownTools)
	assert.Empty(
		t,
		repaired,
		"repair must agree with ValidateToolPolicyCoverage: value disagreement across layers is not a gap",
	)
	assert.Equal(
		t, ToolPolicyDeny, cfg.Agents.List[0].Tools.Builtin.Policies["read_file"],
		"pre-existing per-agent value must be left untouched, not overwritten to deny",
	)
}

// TestRepairIncompleteToolPolicyCoverage_Idempotent verifies that repairing
// an already-fully-covered config is a no-op: repaired is empty and no
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
	assert.Empty(t, repaired, "an already-fully-covered config must not be reported as repaired")
	assert.Equal(t, ToolPolicyAllow, cfg.Agents.List[0].Tools.Builtin.Policies["read_file"])
	assert.Equal(t, ToolPolicyDeny, cfg.Agents.List[0].Tools.Builtin.Policies["write_file"])

	// Calling it again is still a no-op.
	repairedAgain := RepairIncompleteToolPolicyCoverage(cfg, knownTools)
	assert.Empty(t, repairedAgain, "repeated repair calls on a covered config must stay a no-op")
}

// TestRepairIncompleteToolPolicyCoverage_NilOrEmptyInputs_NoOp verifies the
// "nothing to repair" guard mirrors ValidateToolPolicyCoverage's own guard.
func TestRepairIncompleteToolPolicyCoverage_NilOrEmptyInputs_NoOp(t *testing.T) {
	assert.Empty(t, RepairIncompleteToolPolicyCoverage(nil, map[string]struct{}{"read_file": {}}))

	cfg := &Config{Agents: AgentsConfig{List: []AgentConfig{{ID: "agent-i"}}}}
	assert.Empty(t, RepairIncompleteToolPolicyCoverage(cfg, nil))
	assert.Empty(t, RepairIncompleteToolPolicyCoverage(cfg, map[string]struct{}{}))
}

// --- MigrateLegacyToolPolicyKeys (ADR-071 §5.3.5, D1+D4 policy-key migration) ---
//
// Required tests per ADR-071 §5.3.5b, cases (i)-(iv), plus D1's load_tool
// equivalent and the nil/empty guard.

// TestMigrateLegacyToolPolicyKeys_HandOffAllowOnly_MigratesWithoutDenyBackfill
// is required test (i): a config with only "hand_off": "allow" must boot with
// "switch_agent": "allow" — NOT get "switch_agent": "deny" backfilled by
// RepairIncompleteToolPolicyCoverage. This is the ADR-071 §5.3.5a regression
// this whole migration exists to prevent: proves the full ordered sequence
// (migrate -> repair -> validate) rather than just the migration in isolation.
func TestMigrateLegacyToolPolicyKeys_HandOffAllowOnly_MigratesWithoutDenyBackfill(t *testing.T) {
	cfg := &Config{
		Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"hand_off": "allow"},
		},
	}
	knownTools := map[string]struct{}{"switch_agent": {}}

	changed := MigrateLegacyToolPolicyKeys(cfg)
	require.True(t, changed)
	assert.Equal(t, "allow", cfg.Sandbox.ToolPolicies["switch_agent"],
		"hand_off's value must carry forward to switch_agent")
	_, legacyStillPresent := cfg.Sandbox.ToolPolicies["hand_off"]
	assert.False(t, legacyStillPresent, "legacy hand_off key must be deleted")

	// Now run the exact sequence repairAndValidateToolPolicyCoverage uses:
	// migrate (already done above) -> repair -> validate. If the ordering
	// regression this migration exists to prevent were present, the repair
	// would instead see "switch_agent" as wholly uncovered and backfill it
	// to "deny" for the agent below.
	cfg.Agents.List = []AgentConfig{{ID: "some-agent"}}
	repaired := RepairIncompleteToolPolicyCoverage(cfg, knownTools)
	assert.Empty(t, repaired, "switch_agent must already be covered by the migrated global entry — nothing to repair")
	gaps := ValidateToolPolicyCoverage(cfg, knownTools)
	assert.Empty(t, gaps, "switch_agent must be fully covered after migration, with no deny backfill")
}

// TestMigrateLegacyToolPolicyKeys_Idempotent_SecondRunIsNoOp is required test
// (ii): running the migration twice over the same config produces identical
// output — the second run finds no legacy keys and changes nothing.
func TestMigrateLegacyToolPolicyKeys_Idempotent_SecondRunIsNoOp(t *testing.T) {
	cfg := &Config{
		Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{
				"hand_off":          "deny",
				"return_to_default": "allow",
				"load_tool":         "ask",
			},
		},
		Agents: AgentsConfig{
			List: []AgentConfig{{
				ID: "agent-a",
				Tools: &AgentToolsCfg{
					Builtin: AgentBuiltinToolsCfg{
						Policies: map[string]ToolPolicy{"hand_off": ToolPolicyAllow},
					},
				},
			}},
		},
	}

	first := MigrateLegacyToolPolicyKeys(cfg)
	require.True(t, first, "first run over a config with legacy keys must report a change")
	snapshot := map[string]string{
		"global_switch_agent": cfg.Sandbox.ToolPolicies["switch_agent"],
		"global_tool_search":  cfg.Sandbox.ToolPolicies["ToolSearch"],
		"agent_switch_agent":  string(cfg.Agents.List[0].Tools.Builtin.Policies["switch_agent"]),
	}
	assert.Len(t, cfg.Sandbox.ToolPolicies, 2, "both legacy destinations, no legacy keys left")
	assert.Len(t, cfg.Agents.List[0].Tools.Builtin.Policies, 1, "the one legacy key folded to its one destination")

	second := MigrateLegacyToolPolicyKeys(cfg)
	assert.False(t, second, "second run over an already-migrated config must be a no-op")
	assert.Equal(t, snapshot["global_switch_agent"], cfg.Sandbox.ToolPolicies["switch_agent"])
	assert.Equal(t, snapshot["global_tool_search"], cfg.Sandbox.ToolPolicies["ToolSearch"])
	assert.Equal(t, snapshot["agent_switch_agent"], string(cfg.Agents.List[0].Tools.Builtin.Policies["switch_agent"]))
}

// TestMigrateLegacyToolPolicyKeys_StrictestWins_DenyBeatsAllow is required
// test (iii): hand_off: deny + return_to_default: allow -> switch_agent:
// deny, and both legacy keys are gone.
func TestMigrateLegacyToolPolicyKeys_StrictestWins_DenyBeatsAllow(t *testing.T) {
	cfg := &Config{
		Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{
				"hand_off":          "deny",
				"return_to_default": "allow",
			},
		},
	}

	require.True(t, MigrateLegacyToolPolicyKeys(cfg))
	assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies["switch_agent"], "strictest of deny/allow must win")
	_, handOffPresent := cfg.Sandbox.ToolPolicies["hand_off"]
	_, returnPresent := cfg.Sandbox.ToolPolicies["return_to_default"]
	assert.False(t, handOffPresent, "hand_off must be deleted")
	assert.False(t, returnPresent, "return_to_default must be deleted")
}

// TestMigrateLegacyToolPolicyKeys_DestinationParticipatesInFold is required
// test (iv): switch_agent: deny + hand_off: allow -> switch_agent stays deny
// — the pre-existing destination value must not be weakened by a looser
// legacy value (ADR-071 §5.3.5b's "destination participates in the fold").
func TestMigrateLegacyToolPolicyKeys_DestinationParticipatesInFold(t *testing.T) {
	cfg := &Config{
		Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{
				"switch_agent": "deny",
				"hand_off":     "allow",
			},
		},
	}

	require.True(t, MigrateLegacyToolPolicyKeys(cfg))
	assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies["switch_agent"],
		"a pre-existing stricter switch_agent value must not be weakened by a looser legacy hand_off value")
	_, handOffPresent := cfg.Sandbox.ToolPolicies["hand_off"]
	assert.False(t, handOffPresent)
}

// TestMigrateLegacyToolPolicyKeys_LoadTool_MigratesToToolSearch covers D1's
// equivalent migration (load_tool -> ToolSearch), handled by the same pass
// as D4's — both renames must be migrated together at the same insertion
// point per the task's instruction.
func TestMigrateLegacyToolPolicyKeys_LoadTool_MigratesToToolSearch(t *testing.T) {
	cfg := &Config{
		Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"load_tool": "ask"},
		},
	}

	require.True(t, MigrateLegacyToolPolicyKeys(cfg))
	assert.Equal(t, "ask", cfg.Sandbox.ToolPolicies["ToolSearch"])
	_, loadToolPresent := cfg.Sandbox.ToolPolicies["load_tool"]
	assert.False(t, loadToolPresent)
}

// TestMigrateLegacyToolPolicyKeys_PerAgentMap verifies the per-agent
// AgentConfig.Tools.Builtin.Policies map (map[string]ToolPolicy, a distinct
// Go type from the global map[string]string) is migrated identically via the
// same generic fold.
func TestMigrateLegacyToolPolicyKeys_PerAgentMap(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{
					ID: "agent-with-legacy-keys",
					Tools: &AgentToolsCfg{
						Builtin: AgentBuiltinToolsCfg{
							Policies: map[string]ToolPolicy{
								"hand_off":          ToolPolicyAsk,
								"return_to_default": ToolPolicyDeny,
								"read_file":         ToolPolicyAllow, // untouched
							},
						},
					},
				},
				{
					ID:    "agent-without-tools-cfg",
					Tools: nil, // must not panic
				},
			},
		},
	}

	require.True(t, MigrateLegacyToolPolicyKeys(cfg))
	policies := cfg.Agents.List[0].Tools.Builtin.Policies
	assert.Equal(t, ToolPolicyDeny, policies["switch_agent"], "deny beats ask")
	assert.Equal(t, ToolPolicyAllow, policies["read_file"], "unrelated entries must survive untouched")
	_, handOffPresent := policies["hand_off"]
	_, returnPresent := policies["return_to_default"]
	assert.False(t, handOffPresent)
	assert.False(t, returnPresent)
	assert.Nil(t, cfg.Agents.List[1].Tools, "an agent with no Tools config must be left alone, not panic")
}

// TestMigrateLegacyToolPolicyKeys_NilOrEmpty_NoOp mirrors
// TestRepairIncompleteToolPolicyCoverage_NilOrEmptyInputs_NoOp's guard shape.
func TestMigrateLegacyToolPolicyKeys_NilOrEmpty_NoOp(t *testing.T) {
	assert.False(t, MigrateLegacyToolPolicyKeys(nil))

	cfg := &Config{Agents: AgentsConfig{List: []AgentConfig{{ID: "agent-i"}}}}
	assert.False(t, MigrateLegacyToolPolicyKeys(cfg), "a config with no legacy keys anywhere must be a no-op")
}
