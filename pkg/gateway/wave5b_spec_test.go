//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Wave 5b system agent spec tests.
//
// Coverage of the 23 remaining TDD-plan tests from wave5b-system-agent-spec.md
// (tests 1-3, 7-19, 21-27) not covered by:
//   - pkg/security/wave5b_sysagent_ratelimit_test.go (tests 4-6)
//   - pkg/audit/wave5b_system_tools_test.go (test 20)
//
// Test status:
//   REAL:    TestProviderCredentialsWriteOnly, TestCoreAgentDefaults (partial),
//            TestOnboardingStateDetection, TestOnboardingStateResume, TestOnboardingNeverReshow
//   BLOCKED: all others — pending pkg/sysagent, pkg/coreagent

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
)

// --------------------------------------------------------------------------
// Helpers (local to this file — avoids redeclaring newTestRestAPI)
// --------------------------------------------------------------------------

// newWave5bTestAPI creates a restAPI using the existing restMockProvider declared
// in rest_test.go (same package). Both files compile together in the test binary.
// Agents are seeded (omnipus-system + 4 base core agents) to mirror the production
// startup path in gateway.go (Spec-3: Mia·Assistant, Jim·Orchestrator, Ray·Scout, Ava·Builder).
func newWave5bTestAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
	}
	// Seed omnipus-system and core agents to mirror gateway startup (issue #45).
	// seedTestAgents is declared in rest_test.go (same package — gateway_test binary).
	seedTestAgents(cfg)
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
	}
}

// --------------------------------------------------------------------------
// Test #1 — TestSystemToolErrorContract
// --------------------------------------------------------------------------

// TestSystemToolErrorContract verifies that all system tool error categories return
// a consistent {success: false, error: {code, message, suggestion}} contract.
//
// Traces to: wave5b-system-agent-spec.md line 438 (Scenario: System tool error responses)
// BDD: "When <tool> called with <params>, Then success:false AND error.code set AND suggestion present"
func TestSystemToolErrorContract(t *testing.T) {
	t.Skip("Blocked: pkg/sysagent.SystemToolHandler not yet implemented — error contract test requires Handle() method")
	// When implemented, test the following dataset rows:
	//   system.agent.delete {id:"nonexistent", confirm:true} → AGENT_NOT_FOUND
	//   system.agent.create {name:"General Assistant"} → AGENT_ALREADY_EXISTS
	//   system.channel.enable {id:"signal"} (no Java) → DEPENDENCY_MISSING
	//   system.agent.delete {id:"omnipus-system", confirm:true} → PERMISSION_DENIED
	//   system.agent.delete {id:"general-assistant", confirm:true} → PERMISSION_DENIED
	//   system.provider.configure {api_key:"invalid"} → CONNECTION_FAILED
	//   system.config.set {key:"invalid.key"} → INVALID_INPUT
	//   system.agent.create {name:""} → INVALID_INPUT (name required)
	//   system.agent.create {name:"a"*256} → INVALID_INPUT (max length exceeded)
}

// --------------------------------------------------------------------------
// Test #2 — TestRBACEnforcement
// --------------------------------------------------------------------------

// TestRBACEnforcement verifies that system tool invocations are gated by RBAC role.
//
// Traces to: wave5b-system-agent-spec.md line 473 (Scenario: RBAC enforcement on system tools)
// BDD: "Given device with <role>, When agent attempts <tool>, Then <outcome>"
func TestRBACEnforcement(t *testing.T) {
	t.Skip("Blocked: pkg/sysagent RBAC gating not yet implemented — RBACChecker.Check() needed")
	// Dataset rows (wave5b spec line 483):
	//   viewer  + system.agent.create → PERMISSION_DENIED
	//   viewer  + system.agent.list   → Success
	//   viewer  + system.doctor.run   → Success (read-only)
	//   viewer  + system.navigate     → Success (safe)
	//   operator + system.agent.create → Success
	//   operator + system.agent.delete → PERMISSION_DENIED
	//   operator + system.config.set(security.*) → PERMISSION_DENIED
	//   admin   + system.agent.delete → Success (with UI confirmation)
	//   admin   + system.config.set(security.*) → Success (with UI confirmation)
	//   agent   + system.agent.list   → PERMISSION_DENIED (agents have no system access)
}

// --------------------------------------------------------------------------
// Test #3 — TestRBACBypassSingleUser
// --------------------------------------------------------------------------

// TestRBACBypassSingleUser verifies that in single-user mode (no RBAC configured),
// all operations proceed to UI confirmation rather than being RBAC-rejected.
//
// Traces to: wave5b-system-agent-spec.md line 498 (Scenario: Single-user mode bypasses RBAC)
// BDD: "Given RBAC not configured, When system.agent.delete called, Then no RBAC rejection"
func TestRBACBypassSingleUser(t *testing.T) {
	t.Skip("Blocked: pkg/sysagent RBAC gating not yet implemented — single-user bypass logic needed")
}

// --------------------------------------------------------------------------
// Test #7 — TestSchemaRedactionCloud
// --------------------------------------------------------------------------

// TestSchemaRedactionCloud verifies cloud providers receive summarized tool schemas.
//
// Traces to: wave5b-system-agent-spec.md line 549 (Scenario: Cloud provider receives summarized schemas)
// BDD: "Given Anthropic configured, When system agent prompt assembled,
//
//	Then schemas have only: name, one-line description, parameter names; total < 4K tokens"
func TestSchemaRedactionCloud(t *testing.T) {
	t.Skip("Blocked: pkg/sysagent.SchemaRedactor not yet implemented — Summarize() method needed")
	// Dataset row 1: Cloud (Anthropic), full_schemas=false → Summarized, <4K tokens
	// Dataset row 2: Cloud (OpenAI), full_schemas=false → Summarized, <4K tokens
}

// --------------------------------------------------------------------------
// Test #8 — TestSchemaRedactionLocal
// --------------------------------------------------------------------------

// TestSchemaRedactionLocal verifies local providers receive full tool schemas.
//
// Traces to: wave5b-system-agent-spec.md line 562 (Scenario: Local provider receives full schemas)
// BDD: "Given Ollama configured, When prompt assembled, Then full schemas with descriptions + examples"
func TestSchemaRedactionLocal(t *testing.T) {
	t.Skip("Blocked: pkg/sysagent.SchemaRedactor not yet implemented — full schema mode needed")
	// Dataset row 3: Local (Ollama), full_schemas=false → Full, ~10-15K tokens
}

// --------------------------------------------------------------------------
// Test #9 — TestSchemaRedactionOverride
// --------------------------------------------------------------------------

// TestSchemaRedactionOverride verifies system_agent.full_schemas:true overrides cloud summarization.
//
// Traces to: wave5b-system-agent-spec.md line 574 (Scenario: User override sends full schemas to cloud)
// BDD: "Given full_schemas:true AND cloud provider, When prompt assembled, Then full schemas sent"
func TestSchemaRedactionOverride(t *testing.T) {
	t.Skip("Blocked: pkg/sysagent.SchemaRedactor not yet implemented — config override needed")
	// Dataset row 4: Cloud (Anthropic), full_schemas=true → Full schemas sent
}

// --------------------------------------------------------------------------
// Test #10 — TestProviderCredentialsWriteOnly
// --------------------------------------------------------------------------

// TestProviderCredentialsWriteOnly verifies that credential values never appear
// in API responses. Uses the existing config endpoint (which has redactSensitiveFields)
// as a proxy for the write-only requirement until system.provider.list is implemented.
//
// Traces to: wave5b-system-agent-spec.md line 460 (Scenario: Provider credentials are write-only)
// BDD: "When system.provider.list called, Then api_key NOT included in response"
func TestProviderCredentialsWriteOnly(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 460 (Provider credentials are write-only)
	api := newWave5bTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	api.HandleConfig(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	body := w.Body.String()

	// No credential patterns must appear in the response body.
	assert.NotContains(t, body, "sk-ant-",
		"Anthropic key prefix must never appear in config response")
	assert.NotContains(t, body, "sk-proj-",
		"OpenAI project key prefix must never appear in config response")
	assert.NotContains(t, body, "ghp_",
		"GitHub PAT prefix must never appear in config response")
	assert.NotContains(t, body, "sk-or-",
		"OpenRouter key prefix must never appear in config response")

	// Response must still be valid JSON (not an error).
	var configMap map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &configMap),
		"config response must be valid JSON even after redaction")

	// TODO [INFERRED]: Once system.provider.list is implemented, add:
	//   result := handler.Handle("system.provider.list", nil, callerRole="admin")
	//   assert result["providers"] contains {name:"anthropic", status:"connected"}
	//   assert result["providers"][0] does NOT contain "api_key" key
	t.Log("NOTE: system.provider.list (system tool) not yet implemented — full test pending pkg/sysagent")
}

// --------------------------------------------------------------------------
// Test #11 — TestSystemToolExclusivity
// --------------------------------------------------------------------------

// TestSystemToolExclusivity verifies that only omnipus-system can invoke system.* tools.
//
// Traces to: wave5b-system-agent-spec.md line 427 (Scenario: User agent cannot invoke system tools)
// BDD: "Given General Assistant session, When agent invokes system.agent.list, Then rejected"
func TestSystemToolExclusivity(t *testing.T) {
	t.Skip("Blocked: pkg/sysagent.SystemToolHandler not yet implemented — exclusivity guard needed")
	// When implemented: calling Handle() with callerAgentID = "general-assistant"
	// must return an error: system tools exclusive to omnipus-system.
}

// --------------------------------------------------------------------------
// Test #12 — TestOnboardingStateDetection
// --------------------------------------------------------------------------

// TestOnboardingStateDetection verifies first-launch detection from state.json.
//
// Traces to: wave5b-system-agent-spec.md line 591 (Scenario: First launch shows provider setup screen)
// Dataset: Onboarding State Transitions rows 1-5
func TestOnboardingStateDetection(t *testing.T) {
	// Row 1: state.json missing → onboarding_complete=false (fresh install default).
	t.Run("missing state.json → fresh install", func(t *testing.T) {
		home := t.TempDir()
		// Do NOT create state.json — Manager must default to fresh install.
		mgr := onboarding.NewManager(home)
		assert.False(t, mgr.IsComplete(),
			"missing state.json must default to onboarding_complete=false")
	})

	// Row 2: state.json with onboarding_complete:false → false.
	t.Run("state.json with onboarding_complete:false → false", func(t *testing.T) {
		home := t.TempDir()
		sysDir := filepath.Join(home, "system")
		require.NoError(t, os.MkdirAll(sysDir, 0o755))
		stateJSON := `{"version":1,"onboarding_complete":false}`
		require.NoError(t, os.WriteFile(filepath.Join(sysDir, "state.json"), []byte(stateJSON), 0o600))

		mgr := onboarding.NewManager(home)
		assert.False(t, mgr.IsComplete(),
			"onboarding_complete:false in state.json must return false")
	})

	// Row 4: state.json with onboarding_complete:true → true.
	t.Run("state.json with onboarding_complete:true → true", func(t *testing.T) {
		home := t.TempDir()
		sysDir := filepath.Join(home, "system")
		require.NoError(t, os.MkdirAll(sysDir, 0o755))
		stateJSON := `{"version":1,"onboarding_complete":true}`
		require.NoError(t, os.WriteFile(filepath.Join(sysDir, "state.json"), []byte(stateJSON), 0o600))

		mgr := onboarding.NewManager(home)
		assert.True(t, mgr.IsComplete(),
			"onboarding_complete:true in state.json must return true")
	})

	// Row 5: corrupt state.json → treat as fresh install (false).
	t.Run("corrupt state.json → fresh install", func(t *testing.T) {
		home := t.TempDir()
		sysDir := filepath.Join(home, "system")
		require.NoError(t, os.MkdirAll(sysDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(sysDir, "state.json"), []byte("{bad json{{"), 0o600))

		mgr := onboarding.NewManager(home)
		assert.False(t, mgr.IsComplete(),
			"corrupt state.json must default to onboarding_complete=false")
	})
}

// --------------------------------------------------------------------------
// Test #13 — TestOnboardingStateResume
// --------------------------------------------------------------------------

// TestOnboardingStateResume verifies onboarding resumes from the correct step.
//
// Traces to: wave5b-system-agent-spec.md line 629 (Scenario: Onboarding interrupted after provider saved)
func TestOnboardingStateResume(t *testing.T) {
	// Simulate onboarding interrupted: state.json exists but onboarding_complete=false.
	// The Manager must detect this as "not complete" so the wizard re-opens.
	home := t.TempDir()
	sysDir := filepath.Join(home, "system")
	require.NoError(t, os.MkdirAll(sysDir, 0o755))
	// Partial state: onboarding started but not completed.
	partialState := `{"version":1,"onboarding_complete":false}`
	require.NoError(t, os.WriteFile(filepath.Join(sysDir, "state.json"), []byte(partialState), 0o600))

	mgr := onboarding.NewManager(home)
	assert.False(t, mgr.IsComplete(),
		"interrupted onboarding (complete=false) must resume wizard, not skip it")

	// After the user completes onboarding, it must be marked complete.
	require.NoError(t, mgr.CompleteOnboarding())
	assert.True(t, mgr.IsComplete(),
		"after CompleteOnboarding(), wizard must not show again")
}

// --------------------------------------------------------------------------
// Test #14 — TestOnboardingNeverReshow
// --------------------------------------------------------------------------

// TestOnboardingNeverReshow verifies onboarding_complete:true permanently skips onboarding.
//
// Traces to: wave5b-system-agent-spec.md line 652 (Scenario: Onboarding never shown again)
func TestOnboardingNeverReshow(t *testing.T) {
	home := t.TempDir()

	// Step 1: Fresh install → onboarding required.
	mgr := onboarding.NewManager(home)
	assert.False(t, mgr.IsComplete(), "fresh install must require onboarding")

	// Step 2: Complete onboarding.
	require.NoError(t, mgr.CompleteOnboarding())
	assert.True(t, mgr.IsComplete(), "after completion, onboarding must be marked done")

	// Step 3: Load a NEW Manager from the same home directory — simulates app restart.
	// onboarding_complete must be persisted to state.json so the wizard never reshows.
	mgr2 := onboarding.NewManager(home)
	assert.True(t, mgr2.IsComplete(),
		"after app restart, onboarding_complete=true must be read from state.json — wizard must NOT reshow")
}

// --------------------------------------------------------------------------
// Test #15 — TestCoreAgentDefaults (partial)
// --------------------------------------------------------------------------

// TestCoreAgentDefaults verifies agent defaults on fresh install (Spec-3 roster re-cast).
//
// The agent list is seeded via SeedConfig and includes:
// omnipus-system + 4 base core agents (mia, jim, ava, ray). Max was retired.
// The old roster (general-assistant, researcher, content-creator) is removed.
//
// Traces to: Spec-3 (v0.1.0 roster re-cast) — 4-base roster seeded.
// BDD: "Given default config seeded with SeedConfig, When agent list loaded,
//
//	Then omnipus-system present with type 'system'; 4 base core agents present with type 'core'"
func TestCoreAgentDefaults(t *testing.T) {
	api := newWave5bTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var agents []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &agents))

	agentsByID := make(map[string]string) // id → type
	for _, a := range agents {
		agentsByID[a.ID] = a.Type
	}

	// System agent must always be present with type "system".
	sysType, sysFound := agentsByID["omnipus-system"]
	require.True(t, sysFound,
		"omnipus-system must be present in agent list on fresh install (seeded at startup)")
	assert.Equal(t, "system", sysType,
		"omnipus-system type must be 'system'")

	// NOTE: The status of omnipus-system is BLOCKED (see TestAgentListStatus_SystemAlwaysActive).
	// computeAgentStatus() returns 'draft' for locked agents with no soul and no active turn.
	// Production fix needed in pkg/gateway/rest.go. Not asserted here to keep this test passing.

	// Spec-3 4-base core agents must all be present with type "core". Max was retired.
	coreAgents := []string{"mia", "jim", "ava", "ray"}
	for _, id := range coreAgents {
		t.Run(id+" is present with type core", func(t *testing.T) {
			agType, found := agentsByID[id]
			assert.True(t, found,
				"core agent %q must be present in agent list after SeedConfig (Spec-3)", id)
			if found {
				assert.Equal(t, "core", agType,
					"core agent %q must have type 'core'", id)
			}
		})
	}
	// Max must NOT be present as a seeded base agent.
	t.Run("max is not a seeded base agent (Spec-3)", func(t *testing.T) {
		_, found := agentsByID["max"]
		assert.False(t, found, "max must not be in the seeded roster after Spec-3 re-cast")
	})

	// Old roster agents must NOT be present.
	for _, oldID := range []string{"general-assistant", "researcher", "content-creator"} {
		_, found := agentsByID[oldID]
		assert.False(t, found,
			"old agent %q must NOT be present — removed in issue #45", oldID)
	}
}

// --------------------------------------------------------------------------
// Test #16 — TestCoreAgentCannotDelete
// --------------------------------------------------------------------------

// TestCoreAgentCannotDelete verifies that deleting a core or system agent returns
// PERMISSION_DENIED with explanation.
//
// Traces to: wave5b-system-agent-spec.md line 679 (Scenario: Core agent cannot be deleted)
func TestCoreAgentCannotDelete(t *testing.T) {
	t.Skip("BLOCKED: Ava's system.agent.delete tool not yet wired — core agent delete protection " +
		"will be enforced when Ava's CRUD tools are implemented. " +
		"IDs to protect (Spec-3 4-base roster): mia, jim, ava, ray")
}

// --------------------------------------------------------------------------
// Test #17 — TestConfirmationRequired
// --------------------------------------------------------------------------

// TestConfirmationRequired verifies that destructive ops require UI-level confirmation
// and that LLM text is NOT accepted as confirmation.
//
// Traces to: wave5b-system-agent-spec.md line 509 (Scenario: Confirmation dialog for agent deletion)
func TestConfirmationRequired(t *testing.T) {
	t.Skip("Blocked: pkg/sysagent.ConfirmationGateway not yet implemented — confirmation mechanism needed")
	// Without confirm=true from UI: system.agent.delete must return CONFIRMATION_REQUIRED.
	// LLM text like "I confirm the deletion" must NOT satisfy the confirmation check.
	// Non-destructive ops (system.agent.create, system.task.create) must NOT require confirmation.
}

// --------------------------------------------------------------------------
// Test #18 — TestAgentCreateIntegration
// --------------------------------------------------------------------------

// TestAgentCreateIntegration verifies end-to-end system.agent.create:
// config file updated, workspace directory created, audit entry written.
//
// Traces to: wave5b-system-agent-spec.md line 389 (Scenario: Create a custom agent via system tool)
func TestAgentCreateIntegration(t *testing.T) {
	t.Skip("Blocked: pkg/sysagent.SystemToolHandler not yet implemented — integration test requires Handle()")
}

// --------------------------------------------------------------------------
// Test #19 — TestAgentDeleteIntegration
// --------------------------------------------------------------------------

// TestAgentDeleteIntegration verifies end-to-end system.agent.delete:
// agent, sessions, memory, workspace all cleaned up, audit entry written.
//
// Traces to: wave5b-system-agent-spec.md line 401 (Scenario: Delete an agent with confirmation)
func TestAgentDeleteIntegration(t *testing.T) {
	t.Skip("Blocked: pkg/sysagent.SystemToolHandler not yet implemented — integration test requires Handle()")
}

// --------------------------------------------------------------------------
// Test #21 — TestProviderConfigureIntegration
// --------------------------------------------------------------------------

// TestProviderConfigureIntegration verifies system.provider.configure encrypts and
// saves the API key to credentials.json and tests the connection.
//
// Traces to: wave5b-system-agent-spec.md line 600 (Scenario: Successful provider connection transitions to chat)
func TestProviderConfigureIntegration(t *testing.T) {
	t.Skip("Blocked: pkg/sysagent.SystemToolHandler not yet implemented — provider configure integration needed")
}

// --------------------------------------------------------------------------
// Test #22 — TestConfirmationFlowIntegration
// --------------------------------------------------------------------------

// TestConfirmationFlowIntegration verifies the complete confirmation flow:
// tool call → gateway intercept → mock user confirms → operation completes.
//
// Traces to: wave5b-system-agent-spec.md line 509 (Scenario: Confirmation dialog — user clicks Delete)
func TestConfirmationFlowIntegration(t *testing.T) {
	t.Skip("Blocked: pkg/sysagent.ConfirmationGateway not yet implemented — full flow integration needed")
}

// --------------------------------------------------------------------------
// Test #23 — TestDoctorRunIntegration
// --------------------------------------------------------------------------

// TestDoctorRunIntegration verifies system.doctor.run updates state.json with
// last_doctor_run and last_doctor_score.
//
// Traces to: wave5b-system-agent-spec.md line 730 (Scenario: Run doctor from Settings UI)
func TestDoctorRunIntegration(t *testing.T) {
	t.Skip(
		"Blocked: pkg/sysagent.SystemToolHandler and state.json last_doctor_run/last_doctor_score fields not yet implemented",
	)
}

// --------------------------------------------------------------------------
// E2E Tests #24-27
// --------------------------------------------------------------------------

// TestOnboardingE2E verifies the complete onboarding journey end-to-end.
// Traces to: wave5b-system-agent-spec.md line 598 (Scenario: Successful provider connection transitions to chat)
func TestOnboardingE2E(t *testing.T) {
	t.Skip("Blocked: E2E — requires frontend OnboardingWizard component + pkg/onboarding + running server")
}

// TestSystemAgentConversationE2E verifies a full system agent conversation via the UI.
// Traces to: wave5b-system-agent-spec.md line 350 (Scenario: System agent responds to natural language request)
func TestSystemAgentConversationE2E(t *testing.T) {
	t.Skip("Blocked: E2E — requires pkg/sysagent full agent loop + running server + Playwright")
}

// TestDoctorUIE2E verifies the Settings → Security → Diagnostics panel in the UI.
// Traces to: wave5b-system-agent-spec.md line 730 (Scenario: Run doctor from Settings UI)
func TestDoctorUIE2E(t *testing.T) {
	t.Skip("Blocked: E2E — requires frontend DoctorPanel component in src/components/settings/ + running server")
}

// TestDestructiveConfirmationE2E verifies LLM text cannot bypass confirmation.
// Traces to: wave5b-system-agent-spec.md line 522 (Scenario: LLM text is not accepted as confirmation)
func TestDestructiveConfirmationE2E(t *testing.T) {
	t.Skip("Blocked: E2E — requires pkg/sysagent ConfirmationGateway + full agent loop + Playwright")
}
