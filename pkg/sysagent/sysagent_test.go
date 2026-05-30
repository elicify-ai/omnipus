// Omnipus — System Agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Wave 5b system agent unit tests.
//
// Covers spec tests 1-3, 7-9, 11, 17 (unit level).
// Integration tests 18-23 are blocked pending pkg/sysagent/tools/ completion.
// E2E tests 24-27 require a running server and frontend components.

package sysagent_test

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/sysagent"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// --------------------------------------------------------------------------
// Mock tool for schema tests
// --------------------------------------------------------------------------

type mockSystemTool struct {
	name        string
	description string
	params      map[string]any
	executed    atomic.Bool
}

func (m *mockSystemTool) Name() string                 { return m.name }
func (m *mockSystemTool) Description() string          { return m.description }
func (m *mockSystemTool) Parameters() map[string]any   { return m.params }
func (m *mockSystemTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (m *mockSystemTool) RequiresAdminAsk() bool       { return true }
func (m *mockSystemTool) Category() tools.ToolCategory { return tools.CategorySystem }
func (m *mockSystemTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	m.executed.Store(true)
	return tools.NewToolResult(`{"success":true}`)
}

// newMockTool creates a mock tool with a name, description, and named parameters.
func newMockTool(name, desc string, paramNames ...string) *mockSystemTool {
	props := make(map[string]any, len(paramNames))
	for _, p := range paramNames {
		props[p] = map[string]any{"type": "string"}
	}
	return &mockSystemTool{
		name:        name,
		description: desc + "\nExtra detail that only appears in full schemas.",
		params: map[string]any{
			"type":       "object",
			"properties": props,
		},
	}
}

// =====================================================================
// Test #1 — TestSystemToolErrorContract
// =====================================================================

// TestSystemToolErrorContract verifies that RBAC, rate-limit, and confirmation
// error responses follow the wave5b spec error contract.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: System tool error responses
// BDD: "When <tool> called with <params>, Then success:false AND error.code AND suggestion"
func TestSystemToolErrorContract(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 438 (Scenario Outline: System tool error responses)

	t.Run("RBAC denial message has required role and caller role", func(t *testing.T) {
		// Dataset row 4: viewer + system.agent.create → PERMISSION_DENIED
		err := sysagent.CheckRBAC(sysagent.RoleViewer, "system.agent.create")
		require.Error(t, err, "viewer must be denied create operations")

		var denied *sysagent.PermissionDeniedError
		require.ErrorAs(t, err, &denied,
			"denial must be of type *PermissionDeniedError for consistent error contract")

		msg := sysagent.FriendlyDenialMessage(denied)
		assert.Contains(t, msg, "operator",
			"friendly denial must name the required role")
		assert.Contains(t, msg, "viewer",
			"friendly denial must name the caller's role")
	})

	t.Run("rate-limit error has retry_after_seconds", func(t *testing.T) {
		rl := sysagent.NewSystemRateLimiter()

		// Exhaust the create category (30/min).
		for i := 0; i < 30; i++ {
			require.NoError(t, rl.Check("system.agent.create"),
				"first 30 create calls must succeed")
		}

		err := rl.Check("system.agent.create")
		require.Error(t, err, "31st create call must be rate-limited")

		var rlErr *sysagent.RateLimitedError
		require.ErrorAs(t, err, &rlErr,
			"rate-limit error must be of type *RateLimitedError")
		assert.Greater(t, rlErr.RetryAfterSeconds, 0.0,
			"rate-limit error must include retry_after_seconds > 0")
	})

	t.Run("confirmation-required response is not success", func(t *testing.T) {
		// Destructive ops without a confirmation handler and without confirm:true must
		// return CONFIRMATION_REQUIRED. In single-user mode, passing confirm:false
		// (or omitting confirm) triggers the denial path.
		registry := tools.NewToolRegistry()
		handler := sysagent.NewSystemToolHandler(sysagent.HandlerConfig{
			Registry: registry,
			Confirm:  nil, // no confirmation handler (headless)
		})

		result := handler.Handle(
			context.Background(),
			sysagent.RoleSingleUser,
			"test-device",
			"system.agent.delete",
			map[string]any{"id": "my-agent", "confirm": false}, // confirm:false → denied
		)
		require.NotNil(t, result)
		assert.True(t, result.IsError,
			"destructive op with nil confirm handler and confirm:false must return an error result")
		assert.Contains(t, result.ContentForLLM(), "CONFIRMATION_REQUIRED",
			"result must include CONFIRMATION_REQUIRED in content")
	})
}

// =====================================================================
// Test #2 — TestRBACEnforcement
// =====================================================================

// TestRBACEnforcement verifies the full RBAC dataset from the wave5b spec.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: RBAC enforcement on system tools
// BDD: "Given device with <role>, When agent attempts <tool>, Then <outcome>"
func TestRBACEnforcement(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 473, Dataset line 828

	tests := []struct {
		name        string
		role        sysagent.PrincipalRole
		tool        string
		expectDeny  bool
		description string
	}{
		// Dataset row 8: viewer + create → denied
		{"viewer cannot create agents", sysagent.RoleViewer, "system.agent.create", true, "viewers read-only"},
		// Dataset row 6: viewer + list → allowed
		{"viewer can list agents", sysagent.RoleViewer, "system.agent.list", false, "viewers can read"},
		// Dataset row 7: viewer + doctor → allowed
		{"viewer can run doctor", sysagent.RoleViewer, "system.doctor.run", false, "doctor is read-only"},
		// Dataset row 9: viewer + navigate → allowed
		{"viewer can navigate", sysagent.RoleViewer, "system.navigate", false, "navigation is safe"},
		// Dataset row 3: operator + create → allowed
		{"operator can create agents", sysagent.RoleOperator, "system.agent.create", false, "operators can create"},
		// Dataset row 4: operator + delete → denied
		{
			"operator cannot delete agents",
			sysagent.RoleOperator,
			"system.agent.delete",
			true,
			"operators cannot destroy",
		},
		// Dataset row 6: operator + config (security.*) → denied at operator level (admin required for delete)
		{
			"operator cannot delete projects",
			sysagent.RoleOperator,
			"system.project.delete",
			true,
			"operators cannot delete projects",
		},
		// Dataset row 1: admin + delete → allowed
		{"admin can delete agents", sysagent.RoleAdmin, "system.agent.delete", false, "admins have full access"},
		// Dataset row 2: admin + config security → allowed
		{"admin can set config", sysagent.RoleAdmin, "system.config.set", false, "admins can change config"},
		// Dataset row 10: agent + list → denied
		{
			"user agent has no system tool access",
			sysagent.RoleAgent,
			"system.agent.list",
			true,
			"agents never get system access",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := sysagent.CheckRBAC(tc.role, tc.tool)
			if tc.expectDeny {
				require.Error(t, err,
					"%s (%s) should be denied for %s — %s", tc.tool, tc.role, tc.name, tc.description)

				var denied *sysagent.PermissionDeniedError
				assert.ErrorAs(t, err, &denied,
					"denial must be *PermissionDeniedError, not a generic error")
			} else {
				assert.NoError(t, err,
					"%s (%s) should be allowed for %s — %s", tc.tool, tc.role, tc.name, tc.description)
			}
		})
	}
}

// =====================================================================
// Test #3 — TestRBACBypassSingleUser
// =====================================================================

// TestRBACBypassSingleUser verifies that RoleSingleUser bypasses all role checks.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: Single-user mode bypasses RBAC (US-3 AC4)
// BDD: "Given RBAC not configured (single-user mode), When system.agent.delete called,
//
//	Then no RBAC rejection — proceeds to UI confirmation"
func TestRBACBypassSingleUser(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 498

	destructiveTools := []string{
		"system.agent.delete",
		"system.project.delete",
		"system.task.delete",
		"system.skill.remove",
		"system.mcp.remove",
		"system.pin.delete",
	}

	for _, tool := range destructiveTools {
		t.Run("single-user can call "+tool, func(t *testing.T) {
			err := sysagent.CheckRBAC(sysagent.RoleSingleUser, tool)
			assert.NoError(t, err,
				"RoleSingleUser must bypass RBAC for %s (single-user mode has no restrictions)", tool)
		})
	}

	// Even tools that require admin for multi-user setups must be allowed.
	t.Run("single-user can change security config", func(t *testing.T) {
		err := sysagent.CheckRBAC(sysagent.RoleSingleUser, "system.config.set")
		assert.NoError(t, err, "RoleSingleUser must be able to change any config")
	})
}

// =====================================================================
// Test #7 — TestSchemaRedactionCloud
// =====================================================================

// TestSchemaRedactionCloud verifies cloud providers get summarized schemas.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: Cloud provider receives summarized schemas (US-5 AC1)
// Dataset rows 1-2: Cloud (Anthropic/OpenAI), full_schemas=false → Summarized
func TestSchemaRedactionCloud(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 549

	cloudProviders := []string{"anthropic", "openai", "deepseek", "groq", "openrouter", "bedrock", "gemini"}
	for _, provider := range cloudProviders {
		t.Run(provider+" is cloud provider", func(t *testing.T) {
			assert.True(t, sysagent.IsCloudProvider(provider),
				"%q must be identified as a cloud provider (receives summarized schemas)", provider)
		})
	}

	t.Run("local provider is not cloud", func(t *testing.T) {
		assert.False(t, sysagent.IsCloudProvider("ollama"),
			"ollama must NOT be identified as a cloud provider (receives full schemas)")
	})

	// Build mock tool list and verify summarized schema content.
	mockTools := []tools.Tool{
		newMockTool("system.agent.create",
			"Create a new custom agent",
			"name", "description", "model", "color", "icon"),
		newMockTool("system.agent.list",
			"List all registered agents",
			"filter", "status"),
	}

	summarized := sysagent.BuildToolSchemas(mockTools, true /* cloudProvider */)
	require.Len(t, summarized, 2, "schema count must match tool count")

	for i, schema := range summarized {
		toolName := mockTools[i].Name()
		t.Run("summarized schema for "+toolName, func(t *testing.T) {
			// Must have name field.
			assert.Equal(t, toolName, schema["name"],
				"summarized schema must include tool name")

			// Must have one-line description (no newlines).
			desc, _ := schema["description"].(string)
			assert.NotEmpty(t, desc, "summarized schema must include description")
			assert.NotContains(t, desc, "\n",
				"summarized description must be one-line only (no newlines)")

			// Must have parameter names (not full schema).
			params := schema["parameters"]
			assert.NotNil(t, params, "summarized schema must include parameters")

			// Must NOT include full JSON schema objects (only names or minimal).
			// The summarized form should be a []string of names or a minimal object.
			schemaJSON, _ := json.Marshal(schema)
			assert.NotContains(t, string(schemaJSON), "Extra detail that only appears in full schemas",
				"summarized schema must NOT include full tool description text")
		})
	}
}

// =====================================================================
// Test #8 — TestSchemaRedactionLocal
// =====================================================================

// TestSchemaRedactionLocal verifies local providers receive full schemas.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: Local provider receives full schemas (US-5 AC2)
// Dataset row 3: Local (Ollama), full_schemas=false → Full schemas
func TestSchemaRedactionLocal(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 562

	mockTools := []tools.Tool{
		newMockTool("system.agent.create",
			"Create a new custom agent",
			"name", "description", "model"),
	}

	fullSchemas := sysagent.BuildToolSchemas(mockTools, false /* localProvider */)
	require.Len(t, fullSchemas, 1)

	schema := fullSchemas[0]

	// Full schema must include all standard JSON Schema fields.
	t.Run("full schema has name", func(t *testing.T) {
		assert.Equal(t, "system.agent.create", schema["name"])
	})

	t.Run("full schema has full description", func(t *testing.T) {
		desc, _ := schema["description"].(string)
		// Full description includes the extra detail line (multiline).
		assert.Contains(t, desc, "Extra detail that only appears in full schemas",
			"local provider full schema must include complete description text")
	})

	t.Run("full schema has detailed parameters", func(t *testing.T) {
		// ToolToSchema wraps in function structure.
		schemaJSON, _ := json.Marshal(schema)
		assert.Contains(t, string(schemaJSON), "properties",
			"full schema must include properties field with parameter details")
	})
}

// =====================================================================
// Test #9 — TestSchemaRedactionOverride
// =====================================================================

// TestSchemaRedactionOverride verifies that full_schemas=true sends full
// schemas regardless of provider type.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: User override sends full schemas to cloud (US-5 AC3)
// Dataset row 4: Cloud (Anthropic), full_schemas=true → Full schemas sent
func TestSchemaRedactionOverride(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 574

	// With full_schemas override, BuildToolSchemas is called with cloudProvider=false
	// regardless of the actual provider type.
	mockTools := []tools.Tool{
		newMockTool("system.agent.create",
			"Create a new custom agent",
			"name", "model"),
	}

	// Simulating: full_schemas=true overrides cloud provider → use cloudProvider=false
	fullOverrideSchemas := sysagent.BuildToolSchemas(mockTools, false /* full_schemas:true overrides */)
	summarizedSchemas := sysagent.BuildToolSchemas(mockTools, true /* normal cloud */)

	t.Run("override produces full schema", func(t *testing.T) {
		fullJSON, _ := json.Marshal(fullOverrideSchemas[0])
		assert.Contains(t, string(fullJSON), "Extra detail that only appears in full schemas",
			"override path must use full description")
	})

	t.Run("summarized schema lacks full description", func(t *testing.T) {
		sumJSON, _ := json.Marshal(summarizedSchemas[0])
		assert.NotContains(t, string(sumJSON), "Extra detail that only appears in full schemas",
			"cloud path (no override) must not include full description")
	})

	t.Run("override schema is different from cloud summarized schema", func(t *testing.T) {
		fullJSON, _ := json.Marshal(fullOverrideSchemas)
		sumJSON, _ := json.Marshal(summarizedSchemas)
		assert.NotEqual(t, string(fullJSON), string(sumJSON),
			"full_schemas override must produce different output than cloud summarization")
	})
}

// =====================================================================
// Test #11 — TestSystemToolExclusivity
// =====================================================================

// TestSystemToolExclusivity verifies that user agents cannot invoke system.* tools.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: User agent cannot invoke system tools (US-2 AC6)
// BDD: "Given General Assistant session, When agent invokes system.agent.list, Then rejected"
func TestSystemToolExclusivity(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 427

	t.Run("IsSystemTool detects system namespace", func(t *testing.T) {
		systemTools := []string{
			"system.agent.create", "system.agent.list", "system.agent.delete",
			"system.provider.list", "system.config.get", "system.doctor.run",
			"system.navigate", "system.backup.create",
		}
		for _, name := range systemTools {
			assert.True(t, sysagent.IsSystemTool(name),
				"%q must be identified as a system tool", name)
		}
	})

	t.Run("IsSystemTool rejects user tools", func(t *testing.T) {
		userTools := []string{
			"web_search", "file.read", "shell", "browser.navigate",
			"spawn", "send_message", "edit",
		}
		for _, name := range userTools {
			assert.False(t, sysagent.IsSystemTool(name),
				"%q must NOT be identified as a system tool", name)
		}
	})

	t.Run("RoleAgent is denied all system tools", func(t *testing.T) {
		systemTools := []string{
			"system.agent.list",
			"system.agent.create",
			"system.agent.delete",
			"system.doctor.run",
			"system.navigate",
		}
		for _, tool := range systemTools {
			err := sysagent.CheckRBAC(sysagent.RoleAgent, tool)
			require.Error(t, err,
				"user agent must be denied %s — system tools exclusive to omnipus-system", tool)

			var denied *sysagent.PermissionDeniedError
			assert.ErrorAs(t, err, &denied,
				"rejection must use *PermissionDeniedError for consistent error format")
		}
	})
}

// =====================================================================
// Test #17 — TestConfirmationRequired
// =====================================================================

// TestConfirmationRequired verifies destructive tools require UI confirmation
// and non-destructive tools do not.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: Confirmation dialog for agent deletion (US-4 AC1-5)
// BDD: "Given destructive op, When gateway intercepts, Then ConfirmationUI required
//
//	AND LLM text NOT accepted as confirmation"
func TestConfirmationRequired(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 509

	destructiveTools := []struct {
		name string
		tool string
	}{
		{"agent delete", "system.agent.delete"},
		{"project delete", "system.project.delete"},
		{"task delete", "system.task.delete"},
		{"channel disable", "system.channel.disable"},
		{"skill remove", "system.skill.remove"},
		{"mcp remove", "system.mcp.remove"},
		{"pin delete", "system.pin.delete"},
	}

	for _, tc := range destructiveTools {
		t.Run(tc.name+" requires UI confirmation", func(t *testing.T) {
			level := sysagent.RequiresConfirmation(tc.tool)
			assert.Equal(t, sysagent.ConfirmationUI, level,
				"%s must require UI confirmation (ConfirmationUI)", tc.tool)
		})
	}

	additiveSafeTools := []struct {
		name string
		tool string
	}{
		{"agent create", "system.agent.create"},
		{"task create", "system.task.create"},
		{"agent list", "system.agent.list"},
		{"project list", "system.project.list"},
		{"provider list", "system.provider.list"},
		{"doctor run", "system.doctor.run"},
		{"navigate", "system.navigate"},
		{"backup create", "system.backup.create"},
	}

	for _, tc := range additiveSafeTools {
		t.Run(tc.name+" does NOT require confirmation", func(t *testing.T) {
			level := sysagent.RequiresConfirmation(tc.tool)
			assert.Equal(t, sysagent.ConfirmationNone, level,
				"%s must NOT require UI confirmation (additive/safe operation)", tc.tool)
		})
	}

	t.Run("LLM text confirmation is rejected — only UI button counts", func(t *testing.T) {
		// When confirm func returns (false, nil) — simulating LLM-generated "I confirm" text
		// that the gateway refuses to accept as a real confirmation.
		llmTextConfirm := func(_ context.Context, _ string, _ map[string]any) (bool, error) {
			// Gateway never accepts LLM text — returns false.
			return false, nil
		}

		registry := tools.NewToolRegistry()
		handler := sysagent.NewSystemToolHandler(sysagent.HandlerConfig{
			Registry: registry,
			Confirm:  llmTextConfirm,
		})

		result := handler.Handle(
			context.Background(),
			sysagent.RoleSingleUser,
			"test-device",
			"system.agent.delete",
			map[string]any{"id": "my-agent"},
		)
		require.NotNil(t, result)
		// Result should NOT be a success — LLM text confirmation must not proceed.
		content := result.ContentForLLM()
		assert.Contains(t, content, "CONFIRMATION_REQUIRED",
			"gateway refusing confirmation must result in CONFIRMATION_REQUIRED, not success")
	})

	t.Run("unknown tool falls back to ConfirmationUI (deny-by-default)", func(t *testing.T) {
		level := sysagent.RequiresConfirmation("system.unknown.operation")
		assert.Equal(t, sysagent.ConfirmationUI, level,
			"unknown tools must default to ConfirmationUI (deny-by-default security posture)")
	})
}

// =====================================================================
// Supplementary: SystemAgentName constant
// =====================================================================

// TestSystemAgentNameConstant verifies the system agent display name is set.
//
// Traces to: wave5b-system-agent-spec.md line 7 (FR-001).
// SystemAgentID was retired per FR-045; privileges now flow from agent type.
func TestSystemAgentNameConstant(t *testing.T) {
	assert.Equal(t, "Omnipus", sysagent.SystemAgentName,
		"system agent display name must be 'Omnipus'")
}

// =====================================================================
// Supplementary: IsCloudProvider edge cases
// =====================================================================

// TestIsCloudProvider_EdgeCases verifies IsCloudProvider handles unknown/local names.
//
// Traces to: wave5b-system-agent-spec.md Dataset: Schema Redaction rows 1-4
func TestIsCloudProvider_EdgeCases(t *testing.T) {
	localOrUnknown := []string{"ollama", "llamacpp", "local", "custom", "", "lm-studio"}
	for _, name := range localOrUnknown {
		assert.False(t, sysagent.IsCloudProvider(name),
			"%q must NOT be classified as a cloud provider — should receive full schemas", name)
	}
}

// =====================================================================
// Supplementary: Rate limit categories (sysagent layer)
// =====================================================================

// TestSystemRateLimiter_CreateCategory verifies create category enforces 30/min.
//
// Traces to: wave5b-system-agent-spec.md line 762 (US-11 AC1)
// BDD: "Given 30 system.agent.create calls in 60s, When 31st, Then RATE_LIMITED"
func TestSystemRateLimiter_CreateCategory(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 762 (Scenario: Rate limit hit on create operations)
	rl := sysagent.NewSystemRateLimiter()

	for i := 0; i < 30; i++ {
		require.NoError(t, rl.Check("system.agent.create"),
			"create call %d/30 must be allowed", i+1)
	}

	err := rl.Check("system.agent.create")
	require.Error(t, err, "31st create call must be RATE_LIMITED")

	var rlErr *sysagent.RateLimitedError
	require.ErrorAs(t, err, &rlErr,
		"rate limit error must be *RateLimitedError")
	assert.Greater(t, rlErr.RetryAfterSeconds, 0.0,
		"RATE_LIMITED must include retry_after_seconds")
}

// TestSystemRateLimiter_DeleteCategory verifies delete category enforces 10/min.
//
// Traces to: wave5b-system-agent-spec.md Dataset: Rate Limit Categories row 5
func TestSystemRateLimiter_DeleteCategory(t *testing.T) {
	rl := sysagent.NewSystemRateLimiter()

	for i := 0; i < 10; i++ {
		require.NoError(t, rl.Check("system.agent.delete"),
			"delete call %d/10 must be allowed", i+1)
	}
	require.Error(t, rl.Check("system.agent.delete"),
		"11th delete call must be RATE_LIMITED")
}

// TestSystemRateLimiter_BackupCategory verifies backup allows only 1 per 5 minutes.
//
// Traces to: wave5b-system-agent-spec.md Dataset: Rate Limit Categories row 9
func TestSystemRateLimiter_BackupCategory(t *testing.T) {
	rl := sysagent.NewSystemRateLimiter()

	require.NoError(t, rl.Check("system.backup.create"),
		"first backup call must be allowed")
	require.Error(t, rl.Check("system.backup.create"),
		"second backup call within 5 minutes must be RATE_LIMITED")
}

// TestSystemRateLimiter_IndependentCategories verifies that rate limits are per-category.
//
// Traces to: wave5b-system-agent-spec.md US-11 (category isolation)
func TestSystemRateLimiter_IndependentCategories(t *testing.T) {
	rl := sysagent.NewSystemRateLimiter()

	// Exhaust the delete category (10/min).
	for i := 0; i < 10; i++ {
		require.NoError(t, rl.Check("system.agent.delete"))
	}
	require.Error(t, rl.Check("system.agent.delete"), "delete exhausted")

	// Create category must still be available (independent window).
	assert.NoError(t, rl.Check("system.agent.create"),
		"exhausting delete category must not affect create category")
}

// =====================================================================
// Supplementary: System prompt is not empty / hardcoded
// =====================================================================

// TestSystemPromptHardcoded verifies the system prompt is a non-empty Go string
// constant (compiled into the binary, not a file path).
//
// Traces to: wave5b-system-agent-spec.md — FR-001 (hardcoded prompt in binary)
func TestSystemPromptHardcoded(t *testing.T) {
	assert.NotEmpty(t, sysagent.SystemPrompt,
		"system agent prompt must be a non-empty compiled-in constant")
	assert.NotContains(t, sysagent.SystemPrompt, "/home/",
		"system prompt must NOT be a file path — it must be the actual prompt text")
	assert.NotContains(t, sysagent.SystemPrompt, ".md",
		"system prompt must NOT reference an external file — it must be embedded in the binary")
}

// =====================================================================
// Supplementary: 35 tools coverage check (pending tools implementation)
// =====================================================================

// TestToolPermissionsMapCoversAll35Tools verifies that the RBAC permission map
// covers exactly the tools specified in BRD Appendix D §D.4.
//
// Traces to: wave5b-system-agent-spec.md — FR-002 (35 system tools)
func TestToolPermissionsMapCoversAll35Tools(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 900 (FR-002: 35 system tools)

	// The 35 tools defined in BRD Appendix D §D.4.1-D.4.10.
	expected35Tools := []string{
		// Agent management (6)
		"system.agent.create", "system.agent.update", "system.agent.delete",
		"system.agent.list", "system.agent.activate", "system.agent.deactivate",
		// Project management (4)
		"system.project.create", "system.project.update",
		"system.project.delete", "system.project.list",
		// Task management (4)
		"system.task.create", "system.task.update",
		"system.task.delete", "system.task.list",
		// Channel management (5)
		"system.channel.enable", "system.channel.configure",
		"system.channel.disable", "system.channel.list", "system.channel.test",
		// Skill management (4)
		"system.skill.install", "system.skill.remove",
		"system.skill.search", "system.skill.list",
		// MCP management (3)
		"system.mcp.add", "system.mcp.remove", "system.mcp.list",
		// Provider management (3)
		"system.provider.configure", "system.provider.list", "system.provider.test",
		// Pin management (3)
		"system.pin.list", "system.pin.create", "system.pin.delete",
		// Config (2)
		"system.config.get", "system.config.set",
		// Diagnostics / utility (4 — but backup, cost, navigate, doctor = 4)
		"system.doctor.run", "system.backup.create", "system.cost.query", "system.navigate",
	}

	// BRD Appendix D text says "35" but the actual tool table lists 38 tools.
	// The implementation matches the table (authoritative), not the introductory text.
	assert.Len(t, expected35Tools, 38,
		"BRD Appendix D tool table defines 38 system tools — test dataset must reflect this")

	// Every expected tool must have an RBAC permission entry.
	for _, tool := range expected35Tools {
		t.Run("tool "+tool+" has RBAC entry", func(t *testing.T) {
			// A tool without an entry is denied by default (deny-by-default posture).
			// We check that the tool IS correctly defined in the permission map by
			// verifying admin can call it (admin access implies it's in the map).
			err := sysagent.CheckRBAC(sysagent.RoleAdmin, tool)
			assert.NoError(t, err,
				"admin must have access to %s — tool must be in RBAC permission map", tool)
		})
	}

	// Verify all 35 tools have sort-stable ordering (no duplicates).
	sorted := make([]string, len(expected35Tools))
	copy(sorted, expected35Tools)
	sort.Strings(sorted)
	unique := make([]string, 0, len(sorted))
	prev := ""
	for _, s := range sorted {
		if s != prev {
			unique = append(unique, s)
			prev = s
		}
	}
	assert.Len(t, unique, 38, "tool list must not contain duplicates")
}

// =====================================================================
// Supplementary: RedirectMessage
// =====================================================================

// TestRedirectMessage verifies the system agent generates a navigation link
// when redirecting user tasks to appropriate agents.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: System agent redirects user tasks (US-1 AC4)
// BDD: "When user sends a user task, Then response includes navigation link [→ Switch to <agent>]"
func TestRedirectMessage(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 362

	msg := sysagent.RedirectMessage("General Assistant")
	assert.Contains(t, msg, "General Assistant",
		"redirect message must name the target agent")
	assert.Contains(t, msg, "[→ Switch to General Assistant]",
		"redirect message must include navigation link")
	assert.NotContains(t, msg, "I'll write the email",
		"system agent must NOT perform user tasks, only redirect")
}

// =====================================================================
// Supplementary: FriendlyDenialMessage
// =====================================================================

// TestFriendlyDenialMessage verifies denial messages name both caller and required role.
//
// Traces to: wave5b-system-agent-spec.md — US-3 AC1 (viewer denied message)
func TestFriendlyDenialMessage(t *testing.T) {
	tests := []struct {
		tool     string
		caller   sysagent.PrincipalRole
		required sysagent.PrincipalRole
	}{
		{"system.agent.create", sysagent.RoleViewer, sysagent.RoleOperator},
		{"system.agent.delete", sysagent.RoleOperator, sysagent.RoleAdmin},
	}
	for _, tc := range tests {
		err := &sysagent.PermissionDeniedError{Tool: tc.tool, Caller: tc.caller, Required: tc.required}
		msg := sysagent.FriendlyDenialMessage(err)

		assert.Contains(t, msg, string(tc.required),
			"denial message must name the required role %q", tc.required)
		assert.Contains(t, msg, string(tc.caller),
			"denial message must name the caller's role %q", tc.caller)
	}
}

// =====================================================================
// Integration Test stubs (pending pkg/sysagent/tools/ completion)
// =====================================================================

// TestAgentCreateIntegration is blocked pending pkg/sysagent/tools/ completion.
// Traces to: wave5b-system-agent-spec.md line 389 (Scenario: Create a custom agent via system tool)
func TestAgentCreateIntegration(t *testing.T) {
	t.Skip("Blocked: pkg/sysagent/tools/ (systools) not yet fully compiled into registry — pending task #2")
}

// TestAgentDeleteIntegration is blocked pending pkg/sysagent/tools/ completion.
// Traces to: wave5b-system-agent-spec.md line 401 (Scenario: Delete an agent with confirmation)
func TestAgentDeleteIntegration(t *testing.T) {
	t.Skip("Blocked: pkg/sysagent/tools/ (systools) not yet fully compiled into registry — pending task #2")
}

// TestProviderConfigureIntegration is blocked pending pkg/sysagent/tools/ completion.
// Traces to: wave5b-system-agent-spec.md line 600 (Scenario: Successful provider connection)
func TestProviderConfigureIntegration(t *testing.T) {
	t.Skip("Blocked: pkg/sysagent/tools/ (systools) not yet fully compiled into registry — pending task #2")
}

// TestConfirmationFlowIntegration is blocked pending full system tool handler wiring.
// Traces to: wave5b-system-agent-spec.md line 509 (Scenario: Confirmation flow)
func TestConfirmationFlowIntegration(t *testing.T) {
	t.Skip("Blocked: full SystemToolHandler integration with real tools pending task #2")
}

// TestDoctorRunIntegration is blocked pending pkg/sysagent/tools/ + state.json extension.
// Traces to: wave5b-system-agent-spec.md line 730 (Scenario: Run doctor from Settings UI)
func TestDoctorRunIntegration(t *testing.T) {
	t.Skip("Blocked: system.doctor.run tool and state.json last_doctor_run/last_doctor_score fields pending task #2-3")
}

// =====================================================================
// TestSysagentGuardedTool_DeleteRequiresConfirmation
// =====================================================================

// TestSysagentGuardedTool_ConfirmationPaths verifies confirmation gate behavior
// across three sub-cases per Blocker 3:
//
//  1. SingleUser + confirm=false → denied with CONFIRMATION_REQUIRED
//  2. SingleUser + confirm=true  → tool executes, executed flag set
//  3. Operator   + confirm=true, h.confirm=nil → denied (multi-user requires confirm func)
//
// Traces to: critical blocker — GuardedTool wraps raw tools with handler guards.
// BRD SEC-19, US-4 AC1 — destructive ops require UI-button confirmation.
func TestSysagentGuardedTool_ConfirmationPaths(t *testing.T) {
	newMockDelete := func() (*mockSystemTool, *tools.ToolRegistry) {
		mock := &mockSystemTool{
			name:        "system.agent.delete",
			description: "Delete an agent",
			params:      map[string]any{"type": "object"},
		}
		reg := tools.NewToolRegistry()
		reg.Register(mock)
		return mock, reg
	}

	t.Run("SingleUser_confirm_false_denied", func(t *testing.T) {
		mock, reg := newMockDelete()
		handler := sysagent.NewSystemToolHandler(sysagent.HandlerConfig{
			Registry: reg,
			Confirm:  nil,
		})
		guarded := sysagent.NewGuardedTool(mock, handler, sysagent.RoleSingleUser, "test-device")

		result := guarded.Execute(context.Background(), map[string]any{"id": "my-agent", "confirm": false})
		require.NotNil(t, result)
		assert.True(t, result.IsError, "confirm=false must return error")
		assert.Contains(t, result.ContentForLLM(), "CONFIRMATION_REQUIRED")
		assert.False(t, mock.executed.Load(), "inner tool must NOT have executed")
	})

	t.Run("SingleUser_confirm_true_executes", func(t *testing.T) {
		mock, reg := newMockDelete()
		handler := sysagent.NewSystemToolHandler(sysagent.HandlerConfig{
			Registry: reg,
			Confirm:  nil,
		})
		guarded := sysagent.NewGuardedTool(mock, handler, sysagent.RoleSingleUser, "test-device")

		result := guarded.Execute(context.Background(), map[string]any{"id": "my-agent", "confirm": true})
		require.NotNil(t, result)
		assert.False(t, result.IsError, "SingleUser+confirm=true must succeed, got: %s", result.ContentForLLM())
		assert.True(t, mock.executed.Load(), "inner tool must have executed")
	})

	t.Run("Admin_confirm_true_denied_without_confirm_func", func(t *testing.T) {
		mock, reg := newMockDelete()
		handler := sysagent.NewSystemToolHandler(sysagent.HandlerConfig{
			Registry: reg,
			Confirm:  nil, // multi-user mode: no confirm func wired
		})
		// Admin role passes RBAC for delete but is NOT single-user mode —
		// must still require confirm func regardless of confirm arg.
		guarded := sysagent.NewGuardedTool(mock, handler, sysagent.RoleAdmin, "test-device")

		result := guarded.Execute(context.Background(), map[string]any{"id": "my-agent", "confirm": true})
		require.NotNil(t, result)
		assert.True(t, result.IsError, "Admin without confirm func must be denied in multi-user mode")
		assert.Contains(t, result.ContentForLLM(), "CONFIRMATION_REQUIRED")
		assert.False(t, mock.executed.Load(), "inner tool must NOT have executed")
	})
}

// =====================================================================
// TestGuardedToolScope_DelegatesToInner
// =====================================================================

// TestGuardedToolScope_DelegatesToInner verifies that GuardedTool.Scope()
// delegates to the inner tool's Scope() rather than returning a hardcoded value.
// Two different inner scopes are tested to prove delegation, not hardcoding.
//
// BDD: Given a GuardedTool wrapping an inner tool with a specific Scope,
//
//	When GuardedTool.Scope() is called,
//	Then it returns the same scope as the inner tool (not a hardcoded value).
//
// Traces to: pkg/sysagent/guarded_tool.go — Scope() delegates to inner tool
// so ToolCompositor's scope gate fires correctly (PR #41 Per-Agent Tool Visibility).
func TestGuardedToolScope_DelegatesToInner(t *testing.T) {
	tests := []struct {
		name          string
		innerScope    tools.ToolScope
		expectedScope tools.ToolScope
	}{
		{
			// ScopeSystem was removed (FR-045). Former system tools now use ScopeCore.
			name:          "ScopeCore_inner_returns_ScopeCore_system_tools",
			innerScope:    tools.ScopeCore,
			expectedScope: tools.ScopeCore,
		},
		{
			// ScopeGeneral inner scope is also delegated correctly.
			name:          "ScopeGeneral_inner_returns_ScopeGeneral",
			innerScope:    tools.ScopeGeneral,
			expectedScope: tools.ScopeGeneral,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a mock inner tool with a configurable scope
			inner := &scopedInnerTool{
				mockSystemTool: mockSystemTool{
					name:        "test.tool",
					description: "test",
					params:      map[string]any{"type": "object"},
				},
				scope: tc.innerScope,
			}
			// And: a real tool registry with the inner tool registered
			reg := tools.NewToolRegistry()
			reg.Register(inner)
			handler := sysagent.NewSystemToolHandler(sysagent.HandlerConfig{
				Registry: reg,
			})

			// When: GuardedTool wraps the inner tool
			guarded := sysagent.NewGuardedTool(inner, handler, sysagent.RoleSingleUser, "")

			// Then: GuardedTool.Scope() returns the inner tool's scope
			assert.Equal(t, tc.expectedScope, guarded.Scope(),
				"GuardedTool must delegate Scope() to inner tool, not hardcode a value")
		})
	}

	// Differentiation check: ensure the two test cases actually produced different scopes,
	// proving the test is not trivially always-true.
	require.NotEqual(t, tests[0].expectedScope, tests[1].expectedScope,
		"test dataset must include two DIFFERENT scopes to prove delegation, not hardcoding")
}

// scopedInnerTool extends mockSystemTool with a configurable Scope() for GuardedTool delegation tests.
type scopedInnerTool struct {
	mockSystemTool
	scope tools.ToolScope
}

func (s *scopedInnerTool) Scope() tools.ToolScope { return s.scope }

// =====================================================================
// Helper: verify strings import used
// =====================================================================

var _ = strings.TrimSpace // ensure strings import is used in test helpers
