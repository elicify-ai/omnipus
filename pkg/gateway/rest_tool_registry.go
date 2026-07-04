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
//   - POST /api/v1/tool-approvals/{approval_id} (FR-011, FR-014, FR-017, FR-018)

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

	addTool := func(t tools.Tool, source string, serverID string) {
		name := t.Name()
		if _, dup := seen[name]; dup {
			return // dedup: first registration wins
		}
		seen[name] = struct{}{}
		entry := gen.ToolRegistryEntry{
			Name:        name,
			Description: t.Description(),
			Scope:       gen.ToolRegistryEntryScope(t.Scope()),
			Category:    toolCategoryFromTool(t),
			Source:      gen.ToolRegistryEntrySource(source),
		}
		// G11: populate ServerId for MCP tools so the SPA can group by server
		// unambiguously without parsing the tool name (which is lossy when a server
		// name contains underscores). Builtin tools leave ServerId nil.
		if serverID != "" {
			entry.ServerId = &serverID
		}
		entries = append(entries, entry)
	}

	// M16: central BuiltinRegistry is the authoritative supply-side source when wired.
	// Comment: per-agent ToolRegistry remains the demand-side filter at LLM-call time.
	// Full migration to central-registry-only at LLM call assembly is tracked separately.
	if a.builtinRegistry != nil {
		for _, t := range a.builtinRegistry.All() {
			addTool(t, string(toolSourceBuiltin), "")
		}
	}

	// M16: central MCPRegistry provides MCP tools when wired.
	// G11: look up the per-tool server ID from the registry so the response
	// carries server_id for each MCP tool entry.
	if a.mcpRegistry != nil {
		for _, t := range a.mcpRegistry.All() {
			sid, _ := a.mcpRegistry.GetServerID(t.Name())
			addTool(t, "mcp", sid)
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
				var serverID string
				if t.Category() == tools.CategoryMCP {
					source = "mcp"
					// G11: attempt server ID lookup via mcpRegistry if wired.
					if a.mcpRegistry != nil {
						serverID, _ = a.mcpRegistry.GetServerID(t.Name())
					}
				}
				addTool(t, source, serverID)
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
//	{name, configured_policy, effective_policy, manifest_tier}
//
// FR-028, FR-086: effective_policy for SPA badge rendering.
// Gap 3: manifest_tier tags each tool as "full" (always-callable), "compressed"
// (lazy — listed in manifest, loaded on demand via the `tools` infra tool), or
// "infra" (always-callable discovery tool `tools` that drives the manifest
// mechanism itself). Infra tools are force-included even when FilterToolsByPolicy
// would drop them for a deny-default agent, mirroring the loop's runtime force-include.
func (a *restAPI) HandleAgentToolsRegistry(w http.ResponseWriter, r *http.Request, agentID string) {
	cfg := a.agentLoop.GetConfig()

	// Determine agent type.
	agentType := "custom"
	var toolsCfg *config.AgentToolsCfg
	var wireAgentType gen.AgentType
	for _, ac := range cfg.Agents.List {
		if ac.ID == agentID {
			at := ac.ResolveType(coreagent.IsCoreAgent)
			agentType = string(at)
			wireAgentType = coreagent.ToWireType(ac)
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
		ManifestTier     gen.AgentToolsResponseToolsManifestTier     `json:"manifest_tier"`
		Name             string                                      `json:"name"`
	}

	var toolEntries []toolsEntry

	if agentInstance != nil {
		allTools := agentInstance.Tools.GetAll()
		filtered, policyMap := tools.FilterToolsByPolicy(allTools, agentType, policyCfg)

		// Track which tools are already included so infra force-include can dedup.
		included := make(map[string]struct{}, len(filtered))

		for _, t := range filtered {
			name := t.Name()
			effectivePolicy, _ := policyMap[name]
			if effectivePolicy == "" {
				effectivePolicy = "allow"
			}

			// configured_policy: what the agent config says.
			configuredPolicy := resolveConfiguredPolicy(name, toolsCfg)

			// manifest_tier: map tools.ManifestTier to the wire enum.
			tier := manifestTierToWire(tools.ToolManifestTier(name))

			toolEntries = append(toolEntries, toolsEntry{
				Name:             name,
				ConfiguredPolicy: gen.AgentToolsResponseToolsConfiguredPolicy(configuredPolicy),
				EffectivePolicy:  gen.AgentToolsResponseToolsEffectivePolicy(effectivePolicy),
				ManifestTier:     tier,
			})
			included[name] = struct{}{}
		}

		// Force-include infra tools that FilterToolsByPolicy may have dropped for a
		// deny-default agent. At runtime the agent loop always makes the `tools`
		// infra tool callable when registered, regardless of policy. The panel must
		// reflect this so operators can see the complete callable surface.
		for _, t := range allTools {
			name := t.Name()
			if _, alreadyIncluded := included[name]; alreadyIncluded {
				continue
			}
			if tools.ToolManifestTier(name) != tools.ManifestInfra {
				continue
			}
			configuredPolicy := resolveConfiguredPolicy(name, toolsCfg)
			toolEntries = append(toolEntries, toolsEntry{
				Name:             name,
				ConfiguredPolicy: gen.AgentToolsResponseToolsConfiguredPolicy(configuredPolicy),
				EffectivePolicy:  gen.AgentToolsResponseToolsEffectivePolicy("allow"),
				ManifestTier:     gen.AgentToolsResponseToolsManifestTierInfra,
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
	agentTypeVal := gen.AgentToolsResponseAgentType(wireAgentType)

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

// manifestTierToWire converts a tools.ManifestTier to the wire enum string used
// by the AgentToolsResponse.Tools.ManifestTier field.
func manifestTierToWire(tier tools.ManifestTier) gen.AgentToolsResponseToolsManifestTier {
	switch tier {
	case tools.ManifestFull:
		return gen.AgentToolsResponseToolsManifestTierFull
	case tools.ManifestInfra:
		return gen.AgentToolsResponseToolsManifestTierInfra
	case tools.ManifestLazy:
		return gen.AgentToolsResponseToolsManifestTierCompressed
	}
	// Unreachable: ToolManifestTier returns only the three tiers above.
	return gen.AgentToolsResponseToolsManifestTierCompressed
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
// Body: {"action": "approve"|"deny"|"cancel"|"always"}
//
//   - approve/deny/cancel — resolve this single pending call (FR-017, FR-018).
//   - always — resolve this call as approved AND record a session-scoped
//     "Always Allow" grant for the approval's (session, agent, tool) so future
//     matching calls in the same session auto-approve without re-prompting.
//     This is the generic REST replacement for the retired
//     exec_approval_response{decision:"always"} WS-frame path (ADR-036 §3.4):
//     it is the writer of ApprovalGrantStore.Record on the generic approval
//     path, restoring the "Always Allow" grant that agent-delegation-spec.md's
//     FR-D8 grant-inheritance depends on.
//
// Auth:
//   - Requires valid bearer token (withAuth, FR-014). Unauthenticated → 401.
//
// Outcomes:
//   - 200 OK        action processed
//   - 400 Bad Request  malformed body or unknown action
//   - 401 Unauthorized  missing/invalid token (enforced by withAuth)
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
	// recordGrant is set only for the "always" action. "always" resolves THIS
	// call exactly like "approve" (identical terminal transition, so the pending
	// tool call proceeds) and, once that resolve succeeds, records a
	// session-scoped "Always Allow" grant so future matching calls auto-approve.
	// The grant's scoping identity is read from the immutable approval entry
	// below — never from client-supplied fields.
	var action ApprovalAction
	var recordGrant bool
	switch body.Action {
	case gen.ToolApprovalActionRequestActionApprove:
		action = ApprovalActionApprove
	case gen.ToolApprovalActionRequestActionAlways:
		action = ApprovalActionApprove
		recordGrant = true
	case gen.ToolApprovalActionRequestActionDeny:
		action = ApprovalActionDeny
	case gen.ToolApprovalActionRequestActionCancel:
		action = ApprovalActionCancel
	default:
		jsonErr(
			w,
			http.StatusBadRequest,
			fmt.Sprintf("unknown action %q: must be approve, deny, cancel, or always", string(body.Action)),
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

	// "always": the pending call is now approved — record the session-scoped
	// grant. Identity (session_id, agent_id, tool) comes from the immutable
	// approval entry set at creation, never from client-supplied data, so a
	// caller cannot install a grant for an arbitrary (session, agent, tool).
	// ApprovalGrantStore.Record is nil-receiver-safe and no-ops on any empty key
	// component (fail-safe: never records under an empty session/agent/tool key).
	// This mirrors the outcome of ws_approval.go's legacy decision:"always"
	// handling, issued from the generic REST site instead (ADR-036 §3.4).
	if recordGrant {
		a.agentLoop.ApprovalGrants().Record(entry.SessionID, entry.AgentID, entry.ToolName)
		slog.Info("tool-approval: recorded session Always-Allow grant",
			"approval_id", approvalID,
			"session_id", entry.SessionID,
			"agent_id", entry.AgentID,
			"tool", entry.ToolName)
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
