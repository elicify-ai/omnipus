//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// REST endpoints for the Central Tool Registry redesign (A3 lane).
//
// Implements:
//   - GET  /api/v1/tools                        (FR-027) — registry snapshot
//   - GET  /api/v1/agents/{id}/tools            (FR-028, FR-086) — per-agent policy view
//   - GET  /api/v1/tools/builtin               (FR-029) — returns HTTP 404
//   - POST /api/v1/tool-approvals/{approval_id} (FR-011, FR-014, FR-015, FR-017, FR-018, FR-064)
//
// Coordination with A1/A2:
//   - RequiresAdminAsk() is part of the Tool interface via BaseTool (default false).
//     System tools override it to return true. toolRequiresAdminAsk() uses a direct
//     interface call rather than a local optional interface.
//   - isAdminRole checks the role placed in context by withAuth — same as existing usage
//     in rest_rate_limits.go. No new RBAC infrastructure required (FR-015, resolves H-09).

package gateway

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// toolRequiresAdminAsk returns true when the tool's RequiresAdminAsk() returns true.
// RequiresAdminAsk() is part of the Tool interface (pkg/tools/base.go); BaseTool
// provides a default-false implementation. System tools override to return true.
func toolRequiresAdminAsk(t tools.Tool) bool {
	return t.RequiresAdminAsk()
}

// isAdminRole returns true when the authenticated caller has the admin role.
// Reads from the context written by withAuth; fails closed (returns false) if absent.
func isAdminRole(r *http.Request) bool {
	role, _ := r.Context().Value(RoleContextKey{}).(config.UserRole)
	return role == config.UserRoleAdmin
}

// toolSource is the discriminator for GET /api/v1/tools (FR-027).
type toolSource string

const (
	toolSourceBuiltin toolSource = "builtin"
	toolSourceMCP     toolSource = "mcp"
)

// toolCategoryFromTool derives the category string for the REST response.
// Prefers the Category() method if available (A1 FR-067); falls back to name-prefix.
func toolCategoryFromTool(t tools.Tool) string {
	// Preferred path: A1 adds Category() to the Tool interface via BaseTool mixin.
	type categorizer interface {
		Category() tools.ToolCategory
	}
	if c, ok := t.(categorizer); ok {
		return string(c.Category())
	}
	// Fallback: derive from name prefix (e.g. "system.config.set" → "system").
	name := t.Name()
	if idx := strings.Index(name, "."); idx > 0 {
		return name[:idx]
	}
	return "general"
}

// HandleBuiltinToolsDeprecated handles GET /api/v1/tools/builtin — returns HTTP 404.
// The legacy catalog endpoint is replaced by GET /api/v1/tools (FR-029).
func (a *restAPI) HandleBuiltinToolsDeprecated(w http.ResponseWriter, r *http.Request) {
	// FR-029: /tools/builtin returns 404 post-redesign.
	jsonErr(w, http.StatusNotFound, "endpoint removed: use GET /api/v1/tools instead")
}

// HandleToolsRegistry handles GET /api/v1/tools.
//
// Returns the snapshot of registered tools with per-entry:
//
//	{name, description, scope, category, source}
//
// FR-027: source discriminator ("builtin" | "mcp").
// M16: when the central BuiltinRegistry is populated (wired at boot), it is
// the authoritative supply-side source for builtin tools. MCPRegistry entries
// are appended after builtins (deduplication by name, first-wins). When the
// central registries are not wired (test setups), the handler falls back to
// the default agent's tool set as a proxy.
func (a *restAPI) HandleToolsRegistry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var entries []gen.ToolRegistryEntry
	seen := make(map[string]struct{})

	addTool := func(t tools.Tool, source string) {
		name := t.Name()
		if _, dup := seen[name]; dup {
			return // dedup: first registration wins
		}
		seen[name] = struct{}{}
		entries = append(entries, gen.ToolRegistryEntry{
			Name:        name,
			Description: t.Description(),
			Scope:       gen.ToolRegistryEntryScope(t.Scope()),
			Category:    toolCategoryFromTool(t),
			Source:      gen.ToolRegistryEntrySource(source),
		})
	}

	// M16: central BuiltinRegistry is the authoritative supply-side source when wired.
	// Comment: per-agent ToolRegistry remains the demand-side filter at LLM-call time.
	// Full migration to central-registry-only at LLM call assembly is tracked separately.
	if a.builtinRegistry != nil {
		for _, t := range a.builtinRegistry.All() {
			addTool(t, string(toolSourceBuiltin))
		}
	}

	// M16: central MCPRegistry provides MCP tools when wired.
	if a.mcpRegistry != nil {
		for _, t := range a.mcpRegistry.All() {
			addTool(t, "mcp")
		}
	}

	// Fallback: when central registries are not wired (test setups or pre-boot),
	// read from the default agent's tool set as before.
	if a.builtinRegistry == nil {
		registry := a.agentLoop.GetRegistry()
		defaultAgent := registry.GetDefaultAgent()
		if defaultAgent != nil {
			for _, t := range defaultAgent.Tools.GetAll() {
				// FR-027: source discriminator.
				source := string(toolSourceBuiltin)
				if t.Category() == tools.CategoryMCP {
					source = "mcp"
				}
				addTool(t, source)
			}
		}
	}

	// Return an empty array — never null.
	if entries == nil {
		entries = []gen.ToolRegistryEntry{}
	}

	jsonOK(w, entries)
}

// HandleAgentToolsRegistry handles GET /api/v1/agents/{id}/tools.
//
// Returns per-tool:
//
//	{name, configured_policy, effective_policy, fence_applied, requires_admin_ask}
//
// FR-028, FR-086: effective_policy and fence_applied for SPA badge rendering.
// fence_applied=true means the admin-ask structural fence downgraded allow→ask on a
// custom agent for a RequiresAdminAsk tool (FR-061).
func (a *restAPI) HandleAgentToolsRegistry(w http.ResponseWriter, r *http.Request, agentID string) {
	cfg := a.agentLoop.GetConfig()

	// Determine agent type.
	agentType := "custom"
	var toolsCfg *config.AgentToolsCfg
	for _, ac := range cfg.Agents.List {
		if ac.ID == agentID {
			at := ac.ResolveType(coreagent.IsCoreAgent)
			agentType = string(at)
			toolsCfg = ac.Tools
			break
		}
	}
	if agentType == "custom" && coreagent.IsCoreAgent(agentID) {
		agentType = "core"
	}

	// Retrieve the effective tool set via FilterToolsByPolicy.
	registry := a.agentLoop.GetRegistry()
	agentInstance, ok := registry.GetAgent(agentID)
	if !ok {
		slog.Warn("rest: agent not found in registry for tool view", "agent_id", agentID)
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentID))
		return
	}

	policyCfg := toolsCfgToPolicy(toolsCfg)

	// Also include global tool policies from sandbox config.
	sandboxPolicies := cfg.Sandbox.ToolPolicies
	sandboxDefault := cfg.Sandbox.DefaultToolPolicy
	if len(sandboxPolicies) > 0 || sandboxDefault != "" {
		policyCfg.GlobalPolicies = sandboxPolicies
		policyCfg.GlobalDefaultPolicy = sandboxDefault
	}

	// Build tool entries as gen.AgentToolsResponse.Tools anonymous struct slice.
	type toolsEntry = struct {
		ConfiguredPolicy gen.AgentToolsResponseToolsConfiguredPolicy `json:"configured_policy"`
		EffectivePolicy  gen.AgentToolsResponseToolsEffectivePolicy  `json:"effective_policy"`
		FenceApplied     bool                                        `json:"fence_applied"`
		Name             string                                      `json:"name"`
		RequiresAdminAsk bool                                        `json:"requires_admin_ask"`
	}

	var toolEntries []toolsEntry

	if agentInstance != nil {
		allTools := agentInstance.Tools.GetAll()
		filtered, policyMap := tools.FilterToolsByPolicy(allTools, agentType, policyCfg)

		for _, t := range filtered {
			name := t.Name()
			effectivePolicy, _ := policyMap[name]
			if effectivePolicy == "" {
				effectivePolicy = "allow"
			}

			// configured_policy: what the agent config says (before fence).
			configuredPolicy := resolveConfiguredPolicy(name, toolsCfg)

			// requires_admin_ask: check optional interface (A1 wires this).
			rak := toolRequiresAdminAsk(t)

			// fence_applied (FR-061): true when:
			//   - tool.RequiresAdminAsk() == true
			//   - agent is NOT a core agent
			//   - configured_policy (or effective before fence) was "allow"
			//   - effective_policy after fence is "ask"
			fenceApplied := rak && agentType == "custom" && configuredPolicy == "allow" && effectivePolicy == "ask"

			toolEntries = append(toolEntries, toolsEntry{
				Name:             name,
				ConfiguredPolicy: gen.AgentToolsResponseToolsConfiguredPolicy(configuredPolicy),
				EffectivePolicy:  gen.AgentToolsResponseToolsEffectivePolicy(effectivePolicy),
				FenceApplied:     fenceApplied,
				RequiresAdminAsk: rak,
			})
		}
	}

	if toolEntries == nil {
		toolEntries = []toolsEntry{}
	}

	// Build config section to match existing SPA contract.
	// Use toolsCfgToPolicy so legacy mode:"explicit"+visible:[...] is converted to
	// policy format consistently with the old getAgentTools handler.
	policyCfgForResp := toolsCfgToPolicy(toolsCfg)
	respDefaultPolicy := policyCfgForResp.DefaultPolicy
	if respDefaultPolicy == "" {
		respDefaultPolicy = "allow"
	}
	respPolicies := policyCfgForResp.Policies
	if respPolicies == nil {
		respPolicies = map[string]string{}
	}

	// Convert map[string]string to map[string]AgentToolsResponseConfigBuiltinPolicies.
	builtinPolicies := make(map[string]gen.AgentToolsResponseConfigBuiltinPolicies, len(respPolicies))
	for k, v := range respPolicies {
		builtinPolicies[k] = gen.AgentToolsResponseConfigBuiltinPolicies(v)
	}
	respBuiltinDefaultPolicy := gen.AgentToolsResponseConfigBuiltinDefaultPolicy(respDefaultPolicy)
	agentTypeVal := gen.AgentToolsResponseAgentType(agentType)

	// Build the AgentToolsResponse. The Tools field uses the same anonymous struct
	// as gen.AgentToolsResponse.Tools, aliased as toolsEntry above.
	resp := gen.AgentToolsResponse{
		AgentType: &agentTypeVal,
		Config: struct {
			Builtin *struct {
				DefaultPolicy *gen.AgentToolsResponseConfigBuiltinDefaultPolicy       `json:"default_policy,omitempty"`
				Policies      *map[string]gen.AgentToolsResponseConfigBuiltinPolicies `json:"policies,omitempty"`
			} `json:"builtin,omitempty"`
			Mcp *struct {
				Servers *[]struct {
					Id    string    `json:"id"`
					Tools *[]string `json:"tools,omitempty"`
				} `json:"servers,omitempty"`
			} `json:"mcp,omitempty"`
		}{
			Builtin: &struct {
				DefaultPolicy *gen.AgentToolsResponseConfigBuiltinDefaultPolicy       `json:"default_policy,omitempty"`
				Policies      *map[string]gen.AgentToolsResponseConfigBuiltinPolicies `json:"policies,omitempty"`
			}{
				DefaultPolicy: &respBuiltinDefaultPolicy,
				Policies:      &builtinPolicies,
			},
		},
		Tools: toolEntries,
	}
	jsonOK(w, resp)
}

// resolveConfiguredPolicy returns the agent-configured policy for toolName
// (ignoring global and fence overrides).
func resolveConfiguredPolicy(toolName string, cfg *config.AgentToolsCfg) string {
	if cfg == nil {
		return "allow"
	}
	if p, ok := cfg.Builtin.Policies[toolName]; ok {
		return string(p)
	}
	dp := string(cfg.Builtin.DefaultPolicy)
	if dp == "" {
		return "allow"
	}
	return dp
}

// HandleToolApprovals handles POST /api/v1/tool-approvals/{approval_id}.
//
// Body: {"action": "approve"|"deny"|"cancel"}
//
// Auth:
//   - Requires valid bearer token (withAuth, FR-014). Unauthenticated → 401.
//   - For tools with RequiresAdminAsk=true, non-admin caller → 403 (FR-015).
//
// Outcomes:
//   - 200 OK        action processed
//   - 400 Bad Request  malformed body or unknown action
//   - 401 Unauthorized  missing/invalid token (enforced by withAuth)
//   - 403 Forbidden  non-admin on RequiresAdminAsk tool
//   - 404 Not Found    approval_id not found
//   - 410 Gone       approval already resolved (FR-018)
func (a *restAPI) HandleToolApprovals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract approval_id from URL path: /api/v1/tool-approvals/{approval_id}
	approvalID := strings.TrimPrefix(r.URL.Path, "/api/v1/tool-approvals/")
	approvalID = strings.TrimSuffix(approvalID, "/")
	if err := validateEntityID(approvalID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid approval_id")
		return
	}

	// Parse body.
	var body gen.ToolApprovalActionRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "ToolApprovalActionRequest", &body, validateEnabled) {
		return
	}
	var action ApprovalAction
	switch body.Action {
	case gen.ToolApprovalActionRequestActionApprove:
		action = ApprovalActionApprove
	case gen.ToolApprovalActionRequestActionDeny:
		action = ApprovalActionDeny
	case gen.ToolApprovalActionRequestActionCancel:
		action = ApprovalActionCancel
	default:
		jsonErr(
			w,
			http.StatusBadRequest,
			fmt.Sprintf("unknown action %q: must be approve, deny, or cancel", string(body.Action)),
		)
		return
	}

	// Guard: registry is nil in pre-registry test harnesses.
	if a.approvalReg == nil {
		jsonErr(w, http.StatusServiceUnavailable, "approval registry not initialized")
		return
	}

	// Look up the approval entry.
	entry := a.approvalReg.get(approvalID)
	if entry == nil {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("approval %q not found", approvalID))
		return
	}

	// Admin check for RequiresAdminAsk tools (FR-015).
	// Any action (approve, deny, cancel) on a RequiresAdminAsk tool requires admin.
	if entry.RequiresAdmin && !isAdminRole(r) {
		jsonErr(w, http.StatusForbidden, "admin role required to act on this approval")
		return
	}

	// Attempt the state transition.
	resolved, gone := a.approvalReg.resolve(approvalID, action)
	if gone {
		// Entry already in terminal state — FR-018.
		slog.Warn("tool-approval: late action on resolved approval",
			"approval_id", approvalID, "action", string(body.Action))
		jsonErr(w, http.StatusGone, "approval already resolved")
		return
	}
	if !resolved {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("approval %q not found", approvalID))
		return
	}

	jsonOK(w, gen.ToolApprovalResponse{
		ApprovalId: approvalID,
		Action:     gen.ToolApprovalResponseAction(body.Action),
		Status:     gen.Ok,
	})
}

// toolsCfgToPolicy converts a config.AgentToolsCfg to ToolPolicyCfg.
// Used by HandleAgentToolsRegistry to build the effective tool view.
func toolsCfgToPolicy(cfg *config.AgentToolsCfg) *tools.ToolPolicyCfg {
	if cfg == nil {
		return &tools.ToolPolicyCfg{DefaultPolicy: "allow"}
	}
	policies := make(map[string]string, len(cfg.Builtin.Policies))
	for k, v := range cfg.Builtin.Policies {
		policies[k] = string(v)
	}
	dp := string(cfg.Builtin.DefaultPolicy)
	if dp == "" {
		dp = "allow"
	}
	return &tools.ToolPolicyCfg{DefaultPolicy: dp, Policies: policies}
}
