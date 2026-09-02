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

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
// "infra" (the discovery tool `tools` that drives the manifest mechanism
// itself). The infra tool's effective_policy is resolved through the SAME
// FilterToolsByPolicy call as every other tool — it is seeded "allow" as real
// policy data for every agent (pkg/coreagent/core.go), so it is present here
// when actually allowed and correctly absent when an operator has denied it.
// There used to be a second pass here that unconditionally force-included
// infra tools with a hardcoded "allow" regardless of what FilterToolsByPolicy
// resolved, mirroring a runtime force-allow that also existed in the agent
// loop and gateway approval gate. Both were CLAUDE.md hard-constraint-6
// violations (a hardcoded allow with no real policy data behind it) and have
// been removed — this panel now shows the tool's true effective policy.
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

	// Also include global tool policies from sandbox config. cfg.Sandbox.ToolPolicies
	// is raw config data (map[string]string) — its values are validated at the
	// write boundary (rest_tool_policies.go's putToolPolicies rejects anything
	// outside allow/ask/deny before persisting), so this is a trusting
	// conversion, matching this codebase's existing pattern of trusting
	// validated config rather than re-validating everywhere.
	if len(cfg.Sandbox.ToolPolicies) > 0 {
		gp := make(map[string]config.ToolPolicy, len(cfg.Sandbox.ToolPolicies))
		for k, v := range cfg.Sandbox.ToolPolicies {
			gp[k] = config.ToolPolicy(v)
		}
		policyCfg.GlobalPolicies = gp
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

		for _, t := range filtered {
			name := t.Name()
			// This fallback is provably dead today: FilterToolsByPolicy always
			// sets an entry in policyMap for every tool it returns. It is a
			// literal hardcoded string sitting in a REST handler though —
			// exactly the anti-pattern eliminated everywhere else in this
			// diff (CLAUDE.md hard constraint 6: no hardcoded allow/deny/ask
			// fallback anywhere in Go for tool-policy resolution) — so the
			// fallback value is "deny" (fail-closed), not "allow", as
			// defense-in-depth against a future FilterToolsByPolicy
			// refactor silently reintroducing an allow-default through this
			// now-vestigial guard.
			effectivePolicy, ok := policyMap[name]
			if !ok || effectivePolicy == "" {
				effectivePolicy = "deny"
			}

			// configured_policy: what the agent config says.
			configuredPolicy := resolveConfiguredPolicy(name, toolsCfg, cfg.Sandbox.ToolPolicies)

			// manifest_tier: map tools.ManifestTier to the wire enum.
			tier := manifestTierToWire(tools.ToolManifestTier(name))

			toolEntries = append(toolEntries, toolsEntry{
				Name:             name,
				ConfiguredPolicy: gen.AgentToolsResponseToolsConfiguredPolicy(configuredPolicy),
				EffectivePolicy:  gen.AgentToolsResponseToolsEffectivePolicy(effectivePolicy),
				ManifestTier:     tier,
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
	respPolicies := policyCfgForResp.Policies
	if respPolicies == nil {
		respPolicies = map[string]config.ToolPolicy{}
	}

	// Convert map[string]string to map[string]AgentToolsResponseConfigBuiltinPolicies.
	builtinPolicies := make(map[string]gen.AgentToolsResponseConfigBuiltinPolicies, len(respPolicies))
	for k, v := range respPolicies {
		builtinPolicies[k] = gen.AgentToolsResponseConfigBuiltinPolicies(v)
	}
	// CLAUDE.md hard constraint 6 — complete the reported map over the FULL
	// static builtin catalog, so this response is a valid request body for
	// PUT /api/v1/agents/{id}/tools.
	//
	// That PUT (and PUT /agents/{id} with tools_cfg, and POST /agents) now
	// rejects an incomplete builtin.policies map with 400, because the map it
	// receives REPLACES the agent's own map wholesale and a hole in it silently
	// drops that agent's tightening in favour of the global ceiling at
	// resolution time (pkg/tools/compositor.go's "one side is enough" merge).
	// The SPA builds its PUT body by editing exactly the map this handler
	// returns (src/components/agents/ToolsAndPermissions.tsx's cfgToValue →
	// valueToCfg round-trip, which spreads the hydrated map and overwrites one
	// key). So if this response reported only the agent's own, possibly-sparse
	// stored map, a single per-tool toggle on any agent whose stored map
	// predates a catalog addition — or whose map was emptied by the malformed-
	// body defect this change also fixes — would round-trip an incomplete map
	// and be rejected. Reporting the resolved CONFIGURED policy for every known
	// tool keeps the read/modify/write cycle closed without weakening the
	// write-side check.
	//
	// The fill value is the same resolveConfiguredPolicy the per-tool entries
	// above use (agent entry first, then the global ceiling), so nothing here
	// invents a policy: it reports what the agent already resolves to today. A
	// name with no entry on EITHER side resolves to "deny" — fail-closed,
	// never "allow", matching the compositor's own no-match disposition.
	for toolName := range buildKnownBuiltinToolNames() {
		if _, ok := builtinPolicies[toolName]; ok {
			continue
		}
		configured := resolveConfiguredPolicy(toolName, toolsCfg, cfg.Sandbox.ToolPolicies)
		if configured == "" {
			configured = string(config.ToolPolicyDeny)
		}
		builtinPolicies[toolName] = gen.AgentToolsResponseConfigBuiltinPolicies(configured)
	}
	agentTypeVal := gen.AgentToolsResponseAgentType(wireAgentType)

	// Build the AgentToolsResponse. The Tools field uses the same anonymous struct
	// as gen.AgentToolsResponse.Tools, aliased as toolsEntry above.
	resp := gen.AgentToolsResponse{
		AgentType: &agentTypeVal,
		Config: struct {
			Builtin *struct {
				Policies map[string]gen.AgentToolsResponseConfigBuiltinPolicies `json:"policies"`
			} `json:"builtin,omitempty"`
			Mcp *struct {
				Servers *[]struct {
					Id    string    `json:"id"`
					Tools *[]string `json:"tools,omitempty"`
				} `json:"servers,omitempty"`
			} `json:"mcp,omitempty"`
		}{
			Builtin: &struct {
				Policies map[string]gen.AgentToolsResponseConfigBuiltinPolicies `json:"policies"`
			}{
				// The default-policy fallback concept was removed (CLAUDE.md hard
				// constraint 6) — Policies is now the complete, explicit per-tool
				// source of truth, with no separate "default" value to report.
				Policies: builtinPolicies,
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

// resolveConfiguredPolicy returns the explicitly configured policy for
// toolName: the agent's own entry if present, else the global
// sandbox.tool_policies entry if present. There is no default-policy
// fallback (CLAUDE.md hard constraint 6) — coverage is always one of these
// two explicit, literal sources (enforced by config.ValidateToolPolicyCoverage
// at boot and at every agent write), never a hardcoded value. Returns "" only
// if genuinely uncovered, which should not occur on a validated config.
func resolveConfiguredPolicy(toolName string, cfg *config.AgentToolsCfg, globalPolicies map[string]string) string {
	if cfg != nil {
		if p, ok := cfg.Builtin.Policies[toolName]; ok {
			return string(p)
		}
	}
	if p, ok := globalPolicies[toolName]; ok {
		return p
	}
	return ""
}

// HandleToolApprovals handles POST /api/v1/tool-approvals/{approval_id}.
//
// Body: {"action": "approve"|"deny"|"cancel"|"always"}
//
//   - approve/deny/cancel — resolve this single pending call (FR-017, FR-018).
//   - always — resolve this call as approved AND record a session-scoped
//     "Always Allow" grant for the approval's (session, agent, tool, args) so future
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
	// grant. Identity (session_id, agent_id, tool, args) comes from the immutable
	// approval entry set at creation, never from client-supplied data, so a
	// caller cannot install a grant for an arbitrary (session, agent, tool, args).
	// ApprovalGrantStore.Record is nil-receiver-safe and no-ops (returns false)
	// on any empty key component (fail-safe: never records under an empty
	// session/agent/tool key). This is now the ONLY writer of "always" grants
	// (ADR-036 §3.4 retired the legacy WS-frame gate, wsApprovalHook, which
	// used to also record grants from its own decision:"always" handling).
	//
	// The underlying tool call is still approved either way (the state
	// transition above already succeeded) — a no-op grant only means the NEXT
	// matching call will prompt again, not that this call fails. We log at Warn
	// instead of Info in that case so an operator can see the grant did not
	// actually take effect, rather than silently believing it did.
	resp := gen.ToolApprovalResponse{
		ApprovalId: approvalID,
		Action:     gen.ToolApprovalResponseAction(body.Action),
		Status:     gen.ToolApprovalResponseStatusOk,
	}
	if recordGrant {
		recorded := a.agentLoop.ApprovalGrants().Record(entry.SessionID, entry.AgentID, entry.ToolName, entry.Args)
		if recorded {
			slog.Info("tool-approval: recorded session Always-Allow grant",
				"approval_id", approvalID,
				"session_id", entry.SessionID,
				"agent_id", entry.AgentID,
				"tool", entry.ToolName)
		} else {
			slog.Warn("tool-approval: 'always' action approved this call but the grant was NOT recorded "+
				"(missing session_id/agent_id/tool identity on the approval entry) — the next matching call will prompt again",
				"approval_id", approvalID,
				"session_id", entry.SessionID,
				"agent_id", entry.AgentID,
				"tool", entry.ToolName)
		}
		// Bug fix: also propagate the grant onto the durable identity the
		// NEXT delegation will inherit from, so "Always Allow" clicked
		// during a delegated child's own turn survives past that child's
		// session lifetime. See recordGrantOnDelegationParent's doc comment.
		// Honesty: if this IS a delegated child and the parent write did
		// not stick, grant_recorded must be false — the child session is
		// torn down at the end of the turn and the next same-identity use
		// will ask again.
		parentOK := a.recordGrantOnDelegationParent(entry, approvalID)
		resp.GrantRecorded = boolPtr(recorded && parentOK)
	}

	jsonOK(w, resp)
}

// recordGrantOnDelegationParent is the fix for the bug where an "Always
// Allow" grant clicked during a DELEGATED CHILD's own turn evaporates on the
// very next delegation.
//
// # The bug
//
// entry.SessionID is the ACTING session id (approvalEntry.SessionID's own
// doc comment, approvals.go) — for a tool-approval request raised from
// inside a delegated child's turn, that is the CHILD's OWN session id, never
// the delegating parent's. HandleToolApprovals records the "always" grant
// under exactly that key: (entry.SessionID, entry.AgentID). But
// pkg/agent/subturn.go's spawnSubTurn calls
// al.CloseSession(childID, "delegate_terminal") unconditionally in its
// cleanup defer, which calls ApprovalGrants().ClearSession(childID) — wiping
// every grant filed under the child's key the MOMENT this one delegation
// ends. Since every delegation mints a brand-new, effectively single-use
// child session id (subturn.go's childID), a grant recorded only under that
// key can never survive past this one delegation's own lifetime.
//
// # The fix — and the boundary it must not cross (2026-08 security review)
//
// In addition to the existing record under (entry.SessionID, entry.AgentID)
// — which keeps the grant immediately effective for any further tool calls
// within THIS SAME child turn — also record it under the acting session's
// OWN immediate parent's SESSION id (reconstructed via the session store's
// ParentSessionID, ADR-057 FR-008) paired with entry.AgentID: the EXACT
// (session, agent) identity the approval modal displayed to the user. The
// parent session outlives any one child's "delegate_terminal" teardown
// (ClearSession only ever wipes the CHILD's own key), so this durably
// survives that teardown while staying scoped to the agent identity the
// user actually reviewed.
//
// An earlier version of this fix keyed the second component by
// AgentForSession(meta.ParentSessionID) — the PARENT turn's OWN driving
// agent (e.g. "Mia", delegating to "Jim") — reasoning that this exactly
// matches the SOURCE key pkg/agent/subturn.go's spawnSubTurn reads via
// InheritFrom (parentTS.transcriptSessionID, parentTS.agentID) at the next
// delegation, so the grant would auto-propagate into every future sibling
// delegation regardless of target agent. That was a real security defect:
// the approval modal named "Jim," and a user clicking Always Allow for Jim
// never reviewed or consented to Mia's OWN future tool calls, nor to
// whatever DIFFERENT agent Mia might delegate to next — recording under
// Mia's identity silently escalated a Jim-scoped decision across an agent
// boundary the user never saw. Keying by entry.AgentID instead closes that
// leak: the grant only ever benefits the SAME agent identity again, e.g. if
// that agent is later switched in as the directly-active agent on this same
// parent session (pkg/agent/loop.go's per-turn
// `ApprovalGrants().IsAllowed(ts.transcriptSessionID, ts.agentID, ...)`
// check then matches (parentSessionID, entry.AgentID) exactly).
//
// Trade-off accepted deliberately: because subturn.go's InheritFrom always
// SOURCES from the PARENT's own agent id, not the previous child's, this
// narrower key is no longer picked up by that standard spawn-time
// inheritance call — a grant approved for Jim during one delegation does
// NOT automatically flow into the NEXT delegation (to Jim again, or anyone
// else) the way the wider, boundary-crossing key used to. Re-prompting on
// the next delegation is the accepted cost of not silently crossing an
// agent boundary the user never reviewed.
//
// A no-op (nothing extra recorded) when the acting session is not itself a
// delegated child (ParentSessionID == "", the common case for approvals
// raised directly in a real user chat turn) — that case already resolves
// correctly today, since entry.SessionID there already equals whatever a
// future delegation FROM this session will read as source. Also a no-op,
// logged at Warn, when the parent session's owning agent cannot be resolved
// (deleted agent, corrupt meta) — kept as a liveness gate on the delegation
// relationship itself, even though its value no longer supplies the grant
// key; the immediate grant above already succeeded, so the tool call itself
// is unaffected, only this extra durability is missed.
func (a *restAPI) recordGrantOnDelegationParent(entry *approvalEntry, approvalID string) bool {
	store := a.agentLoop.GetSessionStore()
	if store == nil {
		return true
	}
	meta, err := store.GetMeta(entry.SessionID)
	if err != nil || meta.ParentSessionID == "" {
		// Not a delegated child's session (or unresolvable) — nothing more
		// to propagate; the direct record above already covers this case.
		return true
	}
	// AgentForSession is a liveness/validity gate on the delegation
	// relationship (does the parent session still resolve to a real,
	// currently-registered agent?), not the grant key itself — the key's
	// second component is entry.AgentID (see doc comment above).
	parentAgent, err := a.agentLoop.AgentForSession(meta.ParentSessionID)
	if err != nil || parentAgent == nil {
		slog.Warn("tool-approval: could not resolve the delegating parent's agent for grant inheritance; "+
			"the grant recorded above will not survive this delegation's own teardown",
			"approval_id", approvalID,
			"child_session_id", entry.SessionID,
			"parent_session_id", meta.ParentSessionID,
			"error", err)
		return false
	}
	if a.agentLoop.ApprovalGrants().Record(meta.ParentSessionID, entry.AgentID, entry.ToolName, entry.Args) {
		slog.Info("tool-approval: also recorded Always-Allow grant on the delegating parent's session, "+
			"scoped to the SAME agent identity the approval modal named, so it survives this delegation's "+
			"own teardown without crossing into the parent's own agent identity",
			"approval_id", approvalID,
			"child_session_id", entry.SessionID,
			"parent_session_id", meta.ParentSessionID,
			"parent_agent_id", parentAgent.ID,
			"agent_id", entry.AgentID,
			"tool", entry.ToolName)
		return true
	}
	slog.Warn("tool-approval: parent Always-Allow grant was NOT recorded "+
		"(missing parent session, agent, or tool identity) — this delegation's teardown will drop the child grant",
		"approval_id", approvalID,
		"child_session_id", entry.SessionID,
		"parent_session_id", meta.ParentSessionID,
		"agent_id", entry.AgentID,
		"tool", entry.ToolName)
	return false
}

// toolsCfgToPolicy converts a config.AgentToolsCfg to ToolPolicyCfg.
// Used by HandleAgentToolsRegistry to build the effective tool view.
//
// This is a near-duplicate of pkg/agent's unexported agentToolsCfgToPolicy,
// but is not a clean candidate for reuse: that function is package-private
// (lowercase) to pkg/agent, and it additionally threads GodMode + the global
// sandbox floor — concerns this lighter read-path bridge deliberately leaves
// to its caller (HandleAgentToolsRegistry sets GlobalPolicies itself, see
// below). There is no default-policy fallback (CLAUDE.md hard constraint 6):
// a tool with no exact-match entry in cfg.Builtin.Policies is simply absent
// from the returned map, and resolves to "deny" downstream (fail-closed)
// unless the global sandbox.tool_policies map covers it instead.
func toolsCfgToPolicy(cfg *config.AgentToolsCfg) *tools.ToolPolicyCfg {
	if cfg == nil {
		return &tools.ToolPolicyCfg{}
	}
	// cfg.Builtin.Policies is already map[string]config.ToolPolicy (typed at the
	// config layer) — a direct copy, no string round-trip needed now that
	// tools.ToolPolicyCfg.Policies is typed too.
	policies := make(map[string]config.ToolPolicy, len(cfg.Builtin.Policies))
	for k, v := range cfg.Builtin.Policies {
		policies[k] = v
	}
	return &tools.ToolPolicyCfg{Policies: policies}
}
