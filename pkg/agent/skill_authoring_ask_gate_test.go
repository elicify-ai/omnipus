// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// UAT batch3 S67 (docs/internal/qa/uat-report-full-tool-catalog-batch3-2026-09-02.md,
// finding #4) reported that create_skill's documented "ask"-policy consent
// gate did not fire against a live gateway: two independent raw-WS attempts
// with the calling agent's create_skill policy explicitly "ask" both executed
// immediately with no tool_approval_required frame, contradicting
// pkg/sysagent/tools/skill_authoring.go's own doc comment ("the tool
// implementation deliberately does NOT bypass that gate").
//
// This file proves, via two independent close-to-production integration
// tests, that the runTurn ask-gate (pkg/agent/loop.go's toctouPolicy ==
// "ask" branch → CheckGrantOrRequestApproval → the wired PolicyApprover) DOES
// correctly consult the approver for the REAL sysagent create_skill tool
// (systools.SkillCreateTool, ScopeCore) under an explicit "ask" policy —
// across every agentType passesScopeGate distinguishes (core/custom/empty)
// and using the exact production wiring path (coreagent.SeedConfig's real
// Ava seed plus AgentLoop.WireSysagentDeps — not a synthetic stub tool and
// not a hand-built policy map). Under the pre-ADR-090 seed that seed shipped
// create_skill:"ask" out of the box; ADR-090 §5 changed Ava's seeded value
// to allow, so the real-seed leg of that historical claim holds only for the
// pre-ADR-090 contract this file was written against (see
// TestRunTurn_AvaCreateSkillSeededAllow_RealSeed below). Neither test
// reproduces the UAT-observed bypass: the approver is consulted exactly once
// in every case where the policy under test is "ask".
//
// Given this, the fix landed for the S67 finding is NOT a change to the
// ask-gate mechanism itself (there is no reproducible defect in it) — it is
// pkg/gateway/rest.go's updateAgentTools, hardened to stop returning 200 when
// triggerReloadAndWaitOutcome reports the reload did not confirm within the
// poll window (see rest_tool_policy_reload_confirm_test.go). An operator
// tightening a tool from allow/deny to "ask" via that endpoint could
// previously receive a 200 while the in-memory policy snapshot was still the
// OLD one for as long as the reload took — the exact "runtime executed under
// a stale, more permissive policy" shape the UAT symptom describes. These
// two tests stay as permanent regression coverage for the ask-gate mechanism
// itself, which independent investigation confirmed is sound.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// TestRunTurn_CreateSkillAskPolicy_ApproverConsulted drives the REAL sysagent
// create_skill tool (ScopeCore) under an explicit "ask" policy for every
// agentType passesScopeGate (pkg/tools/compositor.go) distinguishes, and
// asserts the wired PolicyApprover is consulted exactly once before
// execution — never silently skipped.
func TestRunTurn_CreateSkillAskPolicy_ApproverConsulted(t *testing.T) {
	for _, agentType := range []config.AgentType{"", config.AgentTypeCore, config.AgentTypeCustom} {
		t.Run(string(agentType)+"_agentType", func(t *testing.T) {
			cfg, _ := baseLoopDenialTestConfig(t)
			cfg.Agents.List[0].Type = agentType
			if agentType == config.AgentTypeCore {
				cfg.Agents.List[0].Locked = true
			}

			provider := testutil.NewScenario().
				WithToolCall("create_skill", `{"name":"uat-s67","content":"---\nname: uat-s67\ndescription: debug\n---\nbody"}`).
				WithText("done")

			msgBus := bus.NewMessageBus()
			al := mustNewAgentLoop(t, cfg, msgBus, provider)
			defer al.Close()

			// Register the REAL sysagent create_skill tool — not a synthetic
			// ScopeGeneral stub — since the UAT finding is specifically about
			// this tool's Scope() == ScopeCore.
			al.RegisterTool(systools.NewSkillCreateTool(&systools.Deps{}))
			setAskPolicyForAllAgents(t, al, "create_skill", config.ToolPolicyAsk)

			approver := &countingDenyApprover{reason: "user"}
			al.SetToolApprover(approver)

			_, err := al.ProcessDirect(context.Background(), "please create the uat-s67 skill", "test-session-uat-s67")
			require.NoError(t, err)

			assert.Equal(t, 1, approver.callCount(),
				"create_skill is policied ask — the approver MUST be consulted before it executes")
		})
	}
}

// TestRunTurn_AvaCreateSkillSeededAllow_RealSeed drives a real Ava agent
// built via the production coreagent.SeedConfig seed — as close to the real
// gateway boot path as pkg/agent's own test harness allows — and pins the
// ADR-090 §5 role-policy flip: Ava's seeded create_skill policy is ALLOW
// (she is the builder; authoring skills is her job, not an ask-gated
// operation), so the wired PolicyApprover must NEVER be consulted for her
// create_skill call.
//
// Historical note: this test was originally
// TestRunTurn_AvaCreateSkillAskPolicy_RealSeed and asserted the inverse —
// that Ava's then-seeded create_skill:"ask" routed through the approver
// exactly once. ADR-090 §5 changed the seeded value to allow; the ask-gate
// MECHANISM itself (toctouPolicy == "ask" → CheckGrantOrRequestApproval)
// remains covered by TestRunTurn_CreateSkillAskPolicy_ApproverConsulted
// above, which drives the same real sysagent tool under an explicit "ask"
// policy across every agentType.
func TestRunTurn_AvaCreateSkillSeededAllow_RealSeed(t *testing.T) {
	cfg := config.DefaultConfig()
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	cfg.Agents.Defaults.Home = workspaceDir
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10
	cfg.Sandbox.AuditLog = true

	coreagent.SeedConfig(cfg)
	// Route directly to Ava so this turn does not depend on the default-agent
	// resolution ladder.
	cfg.Agents.Defaults.DefaultAgentID = string(coreagent.IDAva)

	// create_skill is a search-only lazy tool under ADR-090 (the upfront set
	// names `Skill`, not `create_skill`): on a compressed request it is
	// offered to the model only after ToolSearch loads it. The offered-tool
	// gate (tool_offer_gate.go) refuses a direct call to a never-loaded lazy
	// tool before any approval prompt. Script the real production sequence:
	// ToolSearch (upfront infrastructure, seeded allow for every agent) loads
	// create_skill, then create_skill is called — and with the seeded allow
	// it must execute WITHOUT consulting the approver. (The harness wires no
	// SkillWriter, so the tool itself returns NOT_AVAILABLE after the policy
	// decision — the claim under test is the decision, not the write.)
	provider := testutil.NewScenario().
		WithToolCall("ToolSearch", `{"names":["create_skill"]}`).
		WithToolCall("create_skill", `{"name":"uat-s67","content":"---\nname: uat-s67\ndescription: debug\n---\nbody"}`).
		WithText("done")

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	al.WireSysagentDeps(&systools.Deps{})

	approver := &countingDenyApprover{reason: "user"}
	al.SetToolApprover(approver)

	_, err := al.ProcessDirect(context.Background(), "please create the uat-s67 skill", "test-session-uat-s67-ava")
	require.NoError(t, err)

	assert.Zero(t, approver.callCount(),
		"Ava's ADR-090 seeded create_skill policy is ALLOW — the approver must never be consulted (the ask path is covered by TestRunTurn_CreateSkillAskPolicy_ApproverConsulted)")
}
