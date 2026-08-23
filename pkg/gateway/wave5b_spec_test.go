// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Wave 5b system-agent spec tests — surviving live coverage.
//
// History: this file originally held 23 stubs from wave5b-system-agent-spec.md,
// most BLOCKED/skipped pending the standalone "Omnipus" system agent
// (pkg/sysagent.SystemToolHandler / RBACChecker / SchemaRedactor /
// ConfirmationGateway). That system agent was retired — its tools became ordinary
// policy-governed builtins on the core agents (see pkg/sysagent/tools), and its
// enforcement moved to the main agent loop. The 18 skipped stubs were removed
// because their behaviors are now covered by live tests on the new path:
//   - error contract / RBAC deny / exclusivity → pkg/tools/compositor_*_test.go,
//     pkg/coreagent/*_seed_test.go, pkg/sysagent/tools/*_test.go
//   - confirmation / approval gating          → pkg/gateway/{approvals,reauth_gate,
//     rest_tool_policies}_test.go
//   - single-user bypass                       → pkg/gateway/{rest_auth,routes_admin}_test.go
//   - create/delete-agent, configure-provider  → pkg/sysagent/tools/{agent,provider}_test.go
//   - core-agent-cannot-delete                 → pkg/sysagent/tools/agent_test.go::TestAgentDelete_RefusesLockedAgent
//   - onboarding / doctor / system-agent E2E   → the Playwright e2e suite (e2e gate)
//   - schema redaction (cloud/local/override)  → RETIRED feature (superseded by the
//     compressed tool manifest); the system-agent chat E2E → RETIRED.
//
// What remains here is the live, runnable subset: credential write-only behavior,
// onboarding-state detection/resume/never-reshow, and the seeded-agent roster.

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

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
)

// --------------------------------------------------------------------------
// Helpers (local to this file — avoids redeclaring newTestRestAPI)
// --------------------------------------------------------------------------

// newWave5bTestAPI creates a restAPI using the existing restMockProvider declared
// in rest_test.go (same package). Both files compile together in the test binary.
// It seeds the PRODUCTION roster via coreagent.SeedConfig — Mia·Assistant,
// Jim·Orchestrator, Ray·Scout, Ava·Builder, plus the planner/explorer/researcher
// specialists (Spec-3; Max retired). It deliberately does NOT seed the retired
// "omnipus-system" agent (the shared seedTestAgents helper injects that synthetic
// fixture for the dedicated system/locked-agent tests; this roster check must
// mirror what production actually seeds).
func newWave5bTestAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	// Production-faithful seeding: the 4 base core agents + 3 specialists, no
	// synthetic system agent.
	coreagent.SeedConfig(cfg)
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
	}
}

// --------------------------------------------------------------------------
// TestProviderCredentialsWriteOnly
// --------------------------------------------------------------------------

// TestProviderCredentialsWriteOnly verifies that credential values never appear
// in API responses (the /config endpoint redacts sensitive fields).
//
// Traces to: wave5b-system-agent-spec.md line 460 (Scenario: Provider credentials are write-only)
func TestProviderCredentialsWriteOnly(t *testing.T) {
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
}

// --------------------------------------------------------------------------
// TestOnboardingStateDetection
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
// TestOnboardingStateResume
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
// TestOnboardingNeverReshow
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
// TestCoreAgentDefaults
// --------------------------------------------------------------------------

// TestCoreAgentDefaults verifies the fresh-install agent roster as production
// actually seeds it (Spec-3 roster re-cast + S3 specialist seeding):
//   - 4 base core agents: mia, jim, ava, ray (wire type 'core'). Max was retired.
//   - 3 seeded specialist subagents: planner, explorer, researcher (wire type
//     'Subagent' — subagent-tier, native executor).
//
// The retired "omnipus-system" agent is NOT seeded by production SeedConfig and is
// therefore not asserted here (system/locked-agent handling is covered separately
// in rest_test.go using a synthetic fixture).
//
// Traces to: Spec-3 (v0.1.0 roster re-cast) + S3 specialist seeding.
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

	// The retired system agent must NOT be in the production-seeded roster.
	_, sysFound := agentsByID["omnipus-system"]
	assert.False(t, sysFound,
		"omnipus-system (retired system agent) must NOT be seeded by production SeedConfig")

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
	// S3 seeded specialist subagents must all be present with wire type "Subagent"
	// (subagent-tier, native executor).
	specialists := []string{"planner", "explorer", "researcher"}
	for _, id := range specialists {
		t.Run(id+" specialist is present with type Subagent", func(t *testing.T) {
			agType, found := agentsByID[id]
			assert.True(t, found,
				"specialist subagent %q must be present in agent list after SeedConfig (S3)", id)
			if found {
				assert.Equal(t, "Subagent", agType,
					"specialist subagent %q must have wire type 'Subagent'", id)
			}
		})
	}

	// Max must NOT be present as a seeded base agent.
	t.Run("max is not a seeded base agent (Spec-3)", func(t *testing.T) {
		_, found := agentsByID["max"]
		assert.False(t, found, "max must not be in the seeded roster after Spec-3 re-cast")
	})

	// Old ad-hoc roster agents must NOT be present. NOTE: 'researcher' is a seeded
	// S3 specialist now (asserted present above), so it is NOT in this removed set.
	for _, oldID := range []string{"general-assistant", "content-creator"} {
		_, found := agentsByID[oldID]
		assert.False(t, found,
			"old agent %q must NOT be present — removed in issue #45", oldID)
	}
}
