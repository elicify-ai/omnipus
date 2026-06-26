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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
	"github.com/dapicom-ai/omnipus/pkg/sysagent"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
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
		// Dataset row 4: viewer + create_agent → PERMISSION_DENIED
		err := sysagent.CheckRBAC(sysagent.RoleViewer, "create_agent")
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
			require.NoError(t, rl.Check("create_agent"),
				"first 30 create calls must succeed")
		}

		err := rl.Check("create_agent")
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
			"delete_agent",
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
		{"viewer cannot create agents", sysagent.RoleViewer, "create_agent", true, "viewers read-only"},
		// Dataset row 6: viewer + list → allowed
		{"viewer can list agents", sysagent.RoleViewer, "list_workspaces", false, "viewers can read"},
		// Dataset row 7: viewer + doctor → allowed
		{"viewer can run doctor", sysagent.RoleViewer, "run_doctor", false, "doctor is read-only"},
		// Dataset row 9: viewer + navigate → allowed
		{"viewer can navigate", sysagent.RoleViewer, "navigate", false, "navigation is safe"},
		// Dataset row 3: operator + create → allowed
		{"operator can create agents", sysagent.RoleOperator, "create_agent", false, "operators can create"},
		// Dataset row 4: operator + delete → denied
		{
			"operator cannot delete agents",
			sysagent.RoleOperator,
			"delete_agent",
			true,
			"operators cannot destroy",
		},
		// Dataset row 6: operator + config (security.*) → denied at operator level (admin required for delete)
		{
			"operator cannot delete projects",
			sysagent.RoleOperator,
			"delete_workspace",
			true,
			"operators cannot delete projects",
		},
		// Dataset row 1: admin + delete → allowed
		{"admin can delete agents", sysagent.RoleAdmin, "delete_agent", false, "admins have full access"},
		// Dataset row 2: admin + config security → allowed
		{"admin can set config", sysagent.RoleAdmin, "set_config", false, "admins can change config"},
		// Dataset row 10: agent + list → denied
		{
			"user agent has no system tool access",
			sysagent.RoleAgent,
			"create_agent",
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
		"delete_agent",
		"delete_workspace",
		"delete_task_in_workspace",
		"remove_skill",
		"remove_mcp_server",
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
		err := sysagent.CheckRBAC(sysagent.RoleSingleUser, "set_config")
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
	// System-tool exclusivity is enforced by RBAC (CheckRBAC), not a name-prefix
	// helper: user (RoleAgent) callers must be denied every management tool.

	t.Run("RoleAgent is denied all system tools", func(t *testing.T) {
		systemTools := []string{
			"list_workspaces",
			"create_agent",
			"delete_agent",
			"run_doctor",
			"navigate",
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
		{"agent delete", "delete_agent"},
		{"project delete", "delete_workspace"},
		{"task delete", "delete_task_in_workspace"},
		{"channel disable", "disable_channel"},
		{"skill remove", "remove_skill"},
		{"mcp remove", "remove_mcp_server"},
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
		{"agent create", "create_agent"},
		{"task create", "create_task_in_workspace"},
		{"workspace list", "list_workspaces"},
		{"provider list", "list_providers"},
		{"doctor run", "run_doctor"},
		{"navigate", "navigate"},
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
			"delete_agent",
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
// BDD: "Given 30 create_agent calls in 60s, When 31st, Then RATE_LIMITED"
func TestSystemRateLimiter_CreateCategory(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 762 (Scenario: Rate limit hit on create operations)
	rl := sysagent.NewSystemRateLimiter()

	for i := 0; i < 30; i++ {
		require.NoError(t, rl.Check("create_agent"),
			"create call %d/30 must be allowed", i+1)
	}

	err := rl.Check("create_agent")
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
		require.NoError(t, rl.Check("delete_agent"),
			"delete call %d/10 must be allowed", i+1)
	}
	require.Error(t, rl.Check("delete_agent"),
		"11th delete call must be RATE_LIMITED")
}

// TestSystemRateLimiter_IndependentCategories verifies that rate limits are per-category.
//
// Traces to: wave5b-system-agent-spec.md US-11 (category isolation)
func TestSystemRateLimiter_IndependentCategories(t *testing.T) {
	rl := sysagent.NewSystemRateLimiter()

	// Exhaust the delete category (10/min).
	for i := 0; i < 10; i++ {
		require.NoError(t, rl.Check("delete_agent"))
	}
	require.Error(t, rl.Check("delete_agent"), "delete exhausted")

	// Create category must still be available (independent window).
	assert.NoError(t, rl.Check("create_agent"),
		"exhausting delete category must not affect create category")
}

// =====================================================================
// Supplementary: 35 tools coverage check
// =====================================================================

// TestToolPermissionsMapCoversAll35Tools verifies that the RBAC permission map
// covers exactly the 35 tools returned by AllTools() per BRD Appendix D §D.4.
//
// Traces to: wave5b-system-agent-spec.md — FR-002 (system tools)
func TestToolPermissionsMapCoversAll35Tools(t *testing.T) {
	// The 35 tools in AllTools() as of the §7 tool rename (system.* → verb-first;
	// system.agent.list, system.skill.install, system.skill.search, activate_agent,
	// deactivate_agent retired).
	// Count: 5 agent + 5 workspace + 4 task + 5 channel + 4 skill + 3 mcp +
	//        4 provider + 2 config + 3 diagnostics = 35.
	expectedTools := []string{
		// Agent management (5: list, activate, deactivate retired; 2 metadata accessors from issue #240)
		"create_agent", "update_agent", "delete_agent",
		"read_agent_metadata", "write_agent_metadata",
		// Workspace management (5)
		"create_workspace", "update_workspace",
		"delete_workspace", "list_workspaces", "get_workspace",
		// Task management (4)
		"create_task_in_workspace", "update_task_in_workspace",
		"delete_task_in_workspace", "list_tasks_in_workspace",
		// Channel management (5)
		"enable_channel", "configure_channel",
		"disable_channel", "list_channels", "test_channel",
		// Skill management (4: install/search retired)
		"remove_skill", "list_skills",
		"create_skill", "edit_skill",
		// MCP management (3)
		"add_mcp_server", "remove_mcp_server", "list_mcp_servers",
		// Provider management (4: configure + list + test + models.list)
		"configure_provider", "list_providers", "test_provider",
		"list_models",
		// Config (2)
		"get_config", "set_config",
		// Diagnostics / utility (3)
		"run_doctor", "get_usage", "navigate",
	}

	assert.Len(t, expectedTools, 35,
		"AllTools() returns 35 RBAC-mapped system tools — test dataset must reflect this")

	// Every expected tool must have an RBAC permission entry.
	for _, tool := range expectedTools {
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
	sorted := make([]string, len(expectedTools))
	copy(sorted, expectedTools)
	sort.Strings(sorted)
	unique := make([]string, 0, len(sorted))
	prev := ""
	for _, s := range sorted {
		if s != prev {
			unique = append(unique, s)
			prev = s
		}
	}
	assert.Len(t, unique, 35, "tool list must not contain duplicates")
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
		{"create_agent", sysagent.RoleViewer, sysagent.RoleOperator},
		{"delete_agent", sysagent.RoleOperator, sysagent.RoleAdmin},
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
// Integration test helpers (sysagent_test package scope)
// =====================================================================

// newIntegrationDeps creates a minimal Deps backed by a real temp-dir.
// It mirrors the pattern from pkg/sysagent/tools/agent_test.go::newTestDeps.
// CredStore is wired with a real unlocked store (OMNIPUS_MASTER_KEY env var).
func newIntegrationDeps(t *testing.T) (*systools.Deps, *config.Config, string) {
	t.Helper()
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.json")
	cfg := config.DefaultConfig()
	// Seed config.json on disk so doctor's os.Stat checks can find it.
	require.NoError(t, config.SaveConfig(cfgPath, cfg), "seed config.json")
	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }
	// Wire a real CredStore so configure_provider can set/get credentials.
	t.Setenv("OMNIPUS_MASTER_KEY", strings.Repeat("a", 64))
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(store), "unlock credential store")
	deps := &systools.Deps{
		Home:       home,
		ConfigPath: cfgPath,
		GetCfg:     getCfg,
		MutateConfig: func(fn func(*config.Config) error) error {
			mu.Lock()
			defer mu.Unlock()
			return fn(getCfg())
		},
		SaveConfigLocked: func(cfg *config.Config) error {
			return config.SaveConfig(cfgPath, cfg)
		},
		CredStore: store,
	}
	return deps, cfg, home
}

// newIntegrationRegistry builds a real ToolRegistry populated with all system
// tools from AllTools (the production registry) using the provided deps.
func newIntegrationRegistry(deps *systools.Deps) *tools.ToolRegistry {
	reg := systools.BuildRegistry(deps, nil /* navCb */)
	return reg
}

// =====================================================================
// Integration Tests (tools now implemented — un-skip these)
// =====================================================================

// TestAgentCreateIntegration exercises the create_agent tool through its real
// Execute path and verifies the agent persists in the in-memory config with the
// expected fields.
//
// Traces to: wave5b-system-agent-spec.md line 389 (Scenario: Create a custom agent via system tool)
// BDD: Given a system agent registry with create_agent,
//
//	When Execute is called with name/description/soul/model/color/icon,
//	Then the agent appears in cfg.Agents.List with those exact field values.
func TestAgentCreateIntegration(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 389

	deps, cfg, _ := newIntegrationDeps(t)
	tool := systools.NewAgentCreateTool(deps)

	// Happy path: create two distinct agents to prove the result is not hardcoded.
	agents := []struct {
		name        string
		description string
		color       string
		icon        string
		expectedID  string
	}{
		{"Alpha Bot", "First research agent", "#22C55E", "robot", "alpha-bot"},
		{"Beta Scout", "Second scout agent", "#3B82F6", "star", "beta-scout"},
	}

	for _, a := range agents {
		result := tool.Execute(context.Background(), map[string]any{
			"name":        a.name,
			"description": a.description,
			"soul":        "You are " + a.name + ".",
			"model":       "test/model",
			"color":       a.color,
			"icon":        a.icon,
		})
		require.False(t, result.IsError,
			"create_agent(%q) must succeed, got: %s", a.name, result.ForLLM)
	}

	// Differentiation test: two different names produce two distinct agents.
	require.Len(t, cfg.Agents.List, 2,
		"both agents must be persisted in cfg.Agents.List; hardcoded response would produce 1")

	// Content test: assert exact field values, not just presence.
	alpha := cfg.Agents.List[0]
	assert.Equal(t, "alpha-bot", alpha.ID, "agent ID must be slugified from name")
	assert.Equal(t, "Alpha Bot", alpha.Name)
	assert.Equal(t, "#22C55E", alpha.Color)
	assert.Equal(t, "robot", alpha.Icon)

	beta := cfg.Agents.List[1]
	assert.Equal(t, "beta-scout", beta.ID)
	assert.Equal(t, "#3B82F6", beta.Color)
	assert.Equal(t, "star", beta.Icon)

	// Rejection test: invalid color must be caught.
	badResult := tool.Execute(context.Background(), map[string]any{
		"name":        "Bad Bot",
		"description": "test",
		"soul":        "You are bad.",
		"model":       "test/model",
		"color":       "not-a-color",
	})
	require.True(t, badResult.IsError,
		"create_agent with invalid color must return an error")
	var errBody map[string]any
	require.NoError(t, json.Unmarshal([]byte(badResult.ForLLM), &errBody))
	errBlock, _ := errBody["error"].(map[string]any)
	assert.Equal(t, "INVALID_COLOR", errBlock["code"],
		"must return INVALID_COLOR error code, not a generic error")
}

// TestAgentDeleteIntegration exercises create_agent then delete_agent to prove
// the full lifecycle: agent created, agent removed, confirmation gate enforced.
//
// Traces to: wave5b-system-agent-spec.md line 401 (Scenario: Delete an agent with confirmation)
// BDD: Given an agent in cfg.Agents.List,
//
//	When delete_agent is called with confirm=false, Then it is denied (CONFIRMATION required).
//	When delete_agent is called with confirm=true,  Then agent is removed from the list.
func TestAgentDeleteIntegration(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 401

	deps, cfg, _ := newIntegrationDeps(t)

	// Pre-populate the config with an agent to delete.
	cfg.Agents.List = []config.AgentConfig{
		{ID: "to-delete", Name: "To Delete"},
	}

	deleteTool := systools.NewAgentDeleteTool(deps)

	// Rejection test: confirm=false must be denied by the tool.
	resultNoConfirm := deleteTool.Execute(context.Background(), map[string]any{
		"id":      "to-delete",
		"confirm": false,
	})
	require.True(t, resultNoConfirm.IsError,
		"delete_agent without confirm=true must return error (tool-level guard)")
	assert.Len(t, cfg.Agents.List, 1,
		"agent must NOT be deleted when confirm=false")

	// Happy path: confirm=true executes the deletion.
	resultConfirmed := deleteTool.Execute(context.Background(), map[string]any{
		"id":      "to-delete",
		"confirm": true,
	})
	require.False(t, resultConfirmed.IsError,
		"delete_agent with confirm=true must succeed, got: %s", resultConfirmed.ForLLM)

	// Persistence test: agent must be gone from the in-memory list.
	assert.Empty(t, cfg.Agents.List,
		"agent must be removed from cfg.Agents.List after confirmed delete")

	// Differentiation test: deleting a non-existent agent returns a distinct
	// error (not "success") to prove error handling is real, not hardcoded.
	resultMissing := deleteTool.Execute(context.Background(), map[string]any{
		"id":      "ghost-agent",
		"confirm": true,
	})
	require.True(t, resultMissing.IsError,
		"deleting a non-existent agent must return error, not silent success")
	var missingBody map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultMissing.ForLLM), &missingBody))
	errBlock, _ := missingBody["error"].(map[string]any)
	assert.NotEmpty(t, errBlock["code"],
		"missing-agent error must include an error code")
}

// TestProviderConfigureIntegration exercises configure_provider via its Execute
// path and asserts that the provider is persisted in the config's Providers
// slice with the correct APIKeyRef. This test does NOT require a network
// connection — it asserts config write and credential-store write only.
//
// Traces to: wave5b-system-agent-spec.md line 600 (Scenario: Successful provider connection)
// BDD: Given a credential store,
//
//	When configure_provider is called with name+api_key,
//	Then the key is stored under <name>_API_KEY AND cfg.Providers has the entry wired.
func TestProviderConfigureIntegration(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 600

	deps, cfg, _ := newIntegrationDeps(t)
	tool := systools.NewProviderConfigureTool(deps)

	// Happy path: configure two different providers.
	result1 := tool.Execute(context.Background(), map[string]any{
		"name":    "groq",
		"api_key": "gsk-test-key-1",
	})
	require.False(t, result1.IsError,
		"configure_provider(groq) must succeed, got: %s", result1.ForLLM)

	result2 := tool.Execute(context.Background(), map[string]any{
		"name":    "openai",
		"api_key": "sk-openai-test-2",
	})
	require.False(t, result2.IsError,
		"configure_provider(openai) must succeed, got: %s", result2.ForLLM)

	// providerName extracts the canonical name from a ModelConfig entry, mirroring
	// the tool's providerNameOf logic: explicit Provider field, or the prefix of Model.
	providerName := func(p *config.ModelConfig) string {
		if p == nil {
			return ""
		}
		if p.Provider != "" {
			return p.Provider
		}
		// Extract prefix from "provider/model" format.
		for i, ch := range p.Model {
			if ch == '/' {
				return p.Model[:i]
			}
		}
		return p.Model
	}

	// Differentiation test: both providers must appear in cfg.Providers after
	// configure calls. Groq and openai already have seed entries in DefaultConfig
	// so configure_provider UPDATES their APIKeyRef rather than appending new ones.
	// We assert via providerName(p) to match how the tool identifies entries.
	var providerNames []string
	for _, p := range cfg.Providers {
		if p != nil {
			providerNames = append(providerNames, providerName(p))
		}
	}
	assert.Contains(t, providerNames, "groq",
		"groq must appear in cfg.Providers (as existing seed or appended entry)")
	assert.Contains(t, providerNames, "openai",
		"openai must appear in cfg.Providers (as existing seed or appended entry)")

	// Content test: APIKeyRef is wired on the persisted entry for groq.
	var groqEntry *config.ModelConfig
	for _, p := range cfg.Providers {
		if p != nil && providerName(p) == "groq" {
			if p.APIKeyRef != "" {
				groqEntry = p
				break
			}
		}
	}
	require.NotNil(t, groqEntry, "a groq provider entry with APIKeyRef must exist in config")
	assert.Equal(t, "groq_API_KEY", groqEntry.APIKeyRef,
		"APIKeyRef must follow <provider>_API_KEY convention")

	// Persistence test: credential store holds the actual key.
	storedKey, err := deps.CredStore.Get("groq_API_KEY")
	require.NoError(t, err, "groq_API_KEY must be in credential store")
	assert.Equal(t, "gsk-test-key-1", storedKey,
		"stored credential must match what was passed to configure_provider")

	// Rejection test: cloud provider without api_key must fail.
	resultNoKey := tool.Execute(context.Background(), map[string]any{
		"name": "deepseek",
		// api_key deliberately omitted
	})
	require.True(t, resultNoKey.IsError,
		"configure_provider of cloud provider without api_key must return error")
	var errBody map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultNoKey.ForLLM), &errBody))
	errBlock, _ := errBody["error"].(map[string]any)
	assert.Equal(t, "INVALID_INPUT", errBlock["code"],
		"missing api_key must return INVALID_INPUT, not a generic error")
}

// TestConfirmationFlowIntegration exercises the GuardedTool→SystemToolHandler
// confirmation gate end-to-end with a real delete_agent tool registered on a
// real registry. This tests the full guard sequence that runs in production.
//
// Traces to: wave5b-system-agent-spec.md line 509 (Scenario: Confirmation dialog for agent deletion)
// BDD: Given a real registry with delete_agent registered,
//
//	When GuardedTool.Execute is called with confirm=false (SingleUser), Then CONFIRMATION_REQUIRED.
//	When GuardedTool.Execute is called with confirm=true  (SingleUser), Then tool executes.
func TestConfirmationFlowIntegration(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 509

	deps, cfg, _ := newIntegrationDeps(t)
	// Pre-populate an agent to delete.
	cfg.Agents.List = []config.AgentConfig{
		{ID: "confirm-target", Name: "Confirm Target"},
	}

	// Wire a real registry with all system tools — this is the production path.
	reg := newIntegrationRegistry(deps)
	handler := sysagent.NewSystemToolHandler(sysagent.HandlerConfig{
		Registry: reg,
		Confirm:  nil, // no external confirm func; single-user relies on args["confirm"]
	})

	// Retrieve the real delete_agent tool from the registry for GuardedTool wrapping.
	rawTool, ok := reg.Get("delete_agent")
	require.True(t, ok, "delete_agent must be registered in the production registry")
	require.NotNil(t, rawTool, "delete_agent tool must not be nil")

	guarded := sysagent.NewGuardedTool(rawTool, handler, sysagent.RoleSingleUser, "test-device-integ")

	// Test 1: confirm=false → CONFIRMATION_REQUIRED (no execution).
	resultDenied := guarded.Execute(context.Background(), map[string]any{
		"id":      "confirm-target",
		"confirm": false,
	})
	require.NotNil(t, resultDenied)
	assert.True(t, resultDenied.IsError,
		"confirm=false must produce an error result")
	assert.Contains(t, resultDenied.ContentForLLM(), "CONFIRMATION_REQUIRED",
		"denied result must contain CONFIRMATION_REQUIRED code")
	assert.Len(t, cfg.Agents.List, 1,
		"agent must not be deleted when confirmation is denied")

	// Test 2: confirm=true → executes (agent is removed).
	resultApproved := guarded.Execute(context.Background(), map[string]any{
		"id":      "confirm-target",
		"confirm": true,
	})
	require.NotNil(t, resultApproved)
	assert.False(t, resultApproved.IsError,
		"confirm=true in SingleUser mode must succeed, got: %s", resultApproved.ContentForLLM())
	assert.Empty(t, cfg.Agents.List,
		"agent must be deleted after confirmed execution")

	// Test 3: Differentiation — confirm=true on a DIFFERENT tool (create_agent) must
	// NOT trigger the confirmation gate (create_agent is safe, not destructive).
	createTool, ok2 := reg.Get("create_agent")
	require.True(t, ok2, "create_agent must be registered")
	require.NotNil(t, createTool, "create_agent tool must not be nil")
	guardedCreate := sysagent.NewGuardedTool(createTool, handler, sysagent.RoleSingleUser, "test-device-integ")
	resultCreate := guardedCreate.Execute(context.Background(), map[string]any{
		"name":        "Fresh Agent",
		"description": "a new agent",
		"soul":        "You are fresh.",
		"model":       "test/model",
		"color":       "#22C55E",
		"icon":        "robot",
	})
	assert.False(t, resultCreate.IsError,
		"create_agent (non-destructive) must not be blocked by confirmation gate")
}

// TestDoctorRunIntegration exercises the run_doctor tool through its real
// Execute path and asserts it returns a structured diagnostic result with all
// required fields. The test also verifies that two runs with different
// filesystem states produce different risk scores.
//
// NOTE: state.json last_doctor_run/last_doctor_score is NOT wired by the
// run_doctor tool Execute path — the tool returns the data but does not
// write state.json. That wiring is a UI/gateway concern (the Settings panel
// calls the REST endpoint which persists state.json). The test asserts what
// the tool DOES produce and notes this gap.
//
// Traces to: wave5b-system-agent-spec.md line 730 (Scenario: Run doctor from Settings UI)
// BDD: Given the doctor tool is registered and deps.Home exists,
//
//	When Execute is called, Then result contains risk_score, checks_passed,
//	checks_failed, issues[], run_at — no network required.
func TestDoctorRunIntegration(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 730

	deps, _, home := newIntegrationDeps(t)
	tool := systools.NewDoctorRunTool(deps)

	// Happy path: fresh temp-dir, config.json seeded by newIntegrationDeps.
	result := tool.Execute(context.Background(), map[string]any{})
	require.False(t, result.IsError,
		"run_doctor must succeed on a fresh home, got: %s", result.ForLLM)

	// Content test: result must be valid JSON with required top-level keys.
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &body),
		"run_doctor result must be valid JSON")

	riskScore, hasRiskScore := body["risk_score"]
	assert.True(t, hasRiskScore, "result must include risk_score")
	riskFloat, _ := riskScore.(float64)
	assert.GreaterOrEqual(t, riskFloat, 0.0,
		"risk_score must be >= 0")
	assert.LessOrEqual(t, riskFloat, 100.0,
		"risk_score must be <= 100")

	_, hasIssues := body["issues"]
	assert.True(t, hasIssues, "result must include issues array")

	_, hasChecksPassed := body["checks_passed"]
	assert.True(t, hasChecksPassed, "result must include checks_passed")

	_, hasChecksFailed := body["checks_failed"]
	assert.True(t, hasChecksFailed, "result must include checks_failed")

	runAt, hasRunAt := body["run_at"]
	assert.True(t, hasRunAt, "result must include run_at timestamp")
	assert.NotEmpty(t, runAt, "run_at must not be empty")

	// Differentiation test: create a credentials.json with open permissions to
	// trigger a high-severity issue, then re-run. The risk score must be higher
	// than the baseline (proving the tool actually checks the filesystem).
	credPath := filepath.Join(home, "credentials.json")
	require.NoError(t, os.WriteFile(credPath, []byte(`{}`), 0o644),
		"seed credentials.json with world-readable permissions")

	result2 := tool.Execute(context.Background(), map[string]any{})
	require.False(t, result2.IsError,
		"run_doctor must succeed even with issues present, got: %s", result2.ForLLM)

	var body2 map[string]any
	require.NoError(t, json.Unmarshal([]byte(result2.ForLLM), &body2))

	riskScore2, _ := body2["risk_score"].(float64)
	checksFailed2, _ := body2["checks_failed"].(float64)

	assert.Greater(t, riskScore2, riskFloat,
		"risk_score must increase when credentials.json has open permissions (hardcoded response would not change)")
	assert.Greater(t, checksFailed2, 0.0,
		"checks_failed must be > 0 when a security issue is detected")

	// Gap documented: state.json last_doctor_run/last_doctor_score NOT written by
	// run_doctor.Execute — that persistence is the gateway's responsibility (it
	// reads the JSON result and writes state.json after calling the tool via the
	// REST endpoint). No production code is changed here.
	// TODO: when the gateway wires last_doctor_run/last_doctor_score into state.json,
	// add a test here that reads state.json and asserts the fields.
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
			name:        "delete_agent",
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
