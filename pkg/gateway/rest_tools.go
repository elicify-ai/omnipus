// rest_tools.go: Tool registry and per-agent tool visibility

package gateway

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
)

// --- Tools ---

// HandleTools handles GET /api/v1/tools — returns the list of tools available to the agent.
func (a *restAPI) HandleTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	registry := a.agentLoop.GetRegistry()
	agent := registry.GetDefaultAgent()
	if agent == nil {
		jsonOK(w, []map[string]any{})
		return
	}
	allTools := agent.Tools.GetAll()
	tools := make([]map[string]any, 0, len(allTools))
	for _, t := range allTools {
		name := t.Name()
		category := "general"
		if idx := strings.Index(name, "."); idx > 0 {
			category = name[:idx]
		}
		tools = append(tools, map[string]any{
			"name":        name,
			"category":    category,
			"description": t.Description(),
		})
	}
	jsonOK(w, tools)
}

// --- Tool Visibility (Issue #41) ---

// HandleMCPTools handles GET /api/v1/tools/mcp — returns all configured MCP
// servers with their status and tool lists for the agent tool picker UI.
func (a *restAPI) HandleMCPTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	cfg := a.agentLoop.GetConfig()
	servers := make([]map[string]any, 0, len(cfg.Tools.MCP.Servers))
	for name, srv := range cfg.Tools.MCP.Servers {
		entry := map[string]any{
			"id":      name,
			"name":    name,
			"enabled": srv.Enabled,
			"command": srv.Command,
		}
		if len(srv.Args) > 0 {
			entry["args"] = srv.Args
		}
		servers = append(servers, entry)
	}
	jsonOK(w, servers)
}

// restAPIUpdateAgentTools carries the shared state of updateAgentTools across its stages.
type restAPIUpdateAgentTools struct {
	a               *restAPI
	w               http.ResponseWriter
	r               *http.Request
	agentID         string
	cfg             *config.Config
	req             gen.AgentToolsUpdateRequest
	roundTrip       bool
	builtinPolicies map[string]string
	mcpServers      []struct {
		ID             string
		Tools          []string
		ToolsSpecified bool
	}
	mcpSpecified bool
}

// updateAgentTools handles PUT /api/v1/agents/{id}/tools — replaces the
// agent's tool visibility config.
func (a *restAPI) updateAgentTools(w http.ResponseWriter, r *http.Request, agentID string) {
	rau := &restAPIUpdateAgentTools{a: a, w: w, r: r, agentID: agentID}

	if rau.validateRequest() {
		return
	}

	if rau.normalizeBuiltinPolicies() {
		return
	}

	if rau.validateMCPBindings() {
		return
	}

	if rau.persistPolicy() {
		return
	}

	rau.reloadAndRespond()
}

// validateRequest checks the target agent, authorization, and request body before any policy changes.
func (rau *restAPIUpdateAgentTools) validateRequest() bool {
	rau.cfg = rau.a.agentLoop.GetConfig()
	found := false
	var foundAgent config.AgentConfig
	for _, ac := range rau.cfg.Agents.List {
		if ac.ID == rau.agentID {
			found = true
			foundAgent = ac
			break
		}
	}
	if !found {
		jsonErr(rau.w, http.StatusNotFound, fmt.Sprintf("agent %q not found", rau.agentID))
		return true
	}
	// Hidden system agents keep fixed capabilities. Ordinary built-ins have
	// editable capability assignments under ADR-090.
	if foundAgent.IsSystem() {
		jsonErr(rau.w, http.StatusForbidden, fmt.Sprintf("agent %q is locked and cannot be modified", rau.agentID))
		return true
	}
	// subagent_3p (External CLI) agents have no tools_cfg on the wire at all
	// (W1 discriminated union — AgentCreateRequestSubagent3p has no tools_cfg
	// property) — the runner manages its own tool loop. This endpoint is a
	// separate write path from updateAgent's firstForbiddenSubagent3pField
	// guard, so it needs its own check: closes the tools_cfg-for-3p leak
	// endpoint (a caller could otherwise bypass the PUT /agents/{id} guard by
	// hitting PUT /agents/{id}/tools directly).
	if isExternalSubagent(foundAgent) {
		jsonErr(rau.w, http.StatusBadRequest, "external subagents run their own tools — tools_cfg is not configurable")
		return true
	}

	// The step-up re-auth gate (requireReAuth) was INTENTIONALLY REMOVED here for
	// the per-agent tool-grant PUT per UAT feedback (operator found re-typing the
	// password to change a tool permission to be unnecessary friction). This is a
	// deliberate, scoped loosening — do NOT restore it. Authorization is still
	// enforced: this handler runs behind withAuth, so the caller must hold a valid
	// session; only the password re-prompt is removed. The same gate remains in
	// force on Integrations/Providers/Sandbox/Credentials/Performance PUTs.
	if user, ok := rau.r.Context().Value(UserContextKey{}).(*config.UserConfig); !ok || user == nil {
		jsonErr(rau.w, http.StatusUnauthorized, "not authenticated")
		return true
	}

	validateEnabled := rau.a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(rau.w, rau.r, "AgentToolsUpdateRequest", &rau.req, validateEnabled) {
		return true
	}
	return false
}

// normalizeBuiltinPolicies normalizes and validates the complete built-in tool policy map.
func (rau *restAPIUpdateAgentTools) normalizeBuiltinPolicies() bool {
	// CLAUDE.md hard constraint 6 — a body carrying no `builtin` object at all
	// is REJECTED, never treated as "replace the agent's policy map with
	// nothing".
	//
	// This endpoint fully replaces the agent's builtin tools config on persist
	// (see the updateConfigJSONLocked closure below). Before this guard, a body
	// missing the required `builtin` wrapper — e.g. {"policies": {...}}, the
	// shape a client that assumed a PATCH-style partial update would send —
	// left builtinPolicies nil, and the persist closure then wrote
	// `"tools": {"builtin": {}, "mcp": {}}`: a completely empty policy map,
	// with a 200 OK and no indication anything was wrong.
	//
	// That empty state does NOT fail closed. Runtime resolution is a
	// strictest-wins merge where one side is enough
	// (pkg/tools/compositor.go's resolveEffectivePolicyWith), so every tool
	// then resolved to the GLOBAL ceiling value alone — mostly "allow". The
	// coverage guard further down could not catch it either, because
	// config.ValidateToolPolicyCoverage counts a tool as covered when either
	// the global map or the agent map has an entry, and the seeded global map
	// covers the entire static catalog on a default install. Reproduced live
	// (UAT 2026-09-02, batch 2): an agent explicitly policied `bash: deny` and
	// `list_providers: deny` executed BOTH successfully after one such
	// malformed request. This is the one defect of the three that failed in
	// the ALLOW direction, so it is rejected here at the earliest possible
	// point, before any normalization can make a partial body look valid.
	//
	// UAT 2026-09-13 D-86: the body of a GET /agents/{id}/tools response
	// (AgentToolsResponse: config + tools + agent_type) is accepted as-is,
	// so a client can read, modify and write back the same shape. When the
	// top-level `builtin` is absent and `config.builtin` is present, the
	// policy map (and MCP bindings, from `config.mcp`) are read from there;
	// `tools` and `agent_type` are read-only echoes and are ignored. A body
	// carrying NEITHER `builtin` nor `config.builtin` is still rejected.
	rau.roundTrip = rau.req.Builtin == nil && rau.req.Config != nil && rau.req.Config.Builtin != nil
	if rau.req.Builtin == nil && !rau.roundTrip {
		jsonErr(rau.w, http.StatusBadRequest,
			"builtin.policies is required: this endpoint replaces the agent's complete tool-policy map, "+
				"so a body with no \"builtin\" object (and no \"config.builtin\" object, the GET response shape) "+
				"is rejected rather than persisted as an empty policy")
		return true
	}
	// Extract builtin fields. There is no default_policy field on the wire
	// any more (CLAUDE.md hard constraint 6).

	switch {
	case rau.roundTrip:
		if rau.req.Config.Builtin.Policies != nil {
			rau.builtinPolicies = make(map[string]string, len(rau.req.Config.Builtin.Policies))
			for k, v := range rau.req.Config.Builtin.Policies {
				rau.builtinPolicies[k] = string(v)
			}
		}
	case rau.req.Builtin.Policies != nil:
		rau.builtinPolicies = make(map[string]string, len(rau.req.Builtin.Policies))
		for k, v := range rau.req.Builtin.Policies {
			rau.builtinPolicies[k] = string(v)
		}
	}
	// Legacy mode/visible compatibility (one release): only mode="explicit"
	// with `visible` has any effect (populates builtinPolicies from visible
	// names as "allow"), and ONLY when the caller did NOT also send a real
	// `policies` map — policies always wins outright when present, matching
	// the documented wire contract ("mode/visible are... ignored when
	// policies is present"). Before this fix, the guard checked a now-inert
	// `builtinDefaultPolicy` bookkeeping variable (always "" at this point —
	// nothing set it earlier) instead of req.Builtin.Policies == nil, so a
	// caller sending BOTH a real policies map AND mode="explicit"+visible had
	// their real per-tool values silently discarded and replaced by the
	// deny-all-except-visible legacy conversion a few lines below — found
	// live, two independent reviewers, 2026-07-06. mode="inherit" alone is a
	// no-op now that there is no default-policy fallback to inherit from
	// (CLAUDE.md hard constraint 6) — an incomplete map is rejected by the
	// coverage check below regardless.
	if rau.req.Builtin != nil && rau.req.Builtin.Policies == nil && rau.req.Builtin.Mode != nil &&
		string(*rau.req.Builtin.Mode) == "explicit" && rau.req.Builtin.Visible != nil {
		rau.builtinPolicies = make(map[string]string, len(*rau.req.Builtin.Visible))
		for _, name := range *rau.req.Builtin.Visible {
			rau.builtinPolicies[name] = "allow"
		}
	}

	// Validate per-tool policy values sent in the request.
	validPolicies := map[string]bool{"allow": true, "ask": true, "deny": true}
	for name, p := range rau.builtinPolicies {
		if !validPolicies[p] {
			jsonErr(rau.w, http.StatusUnprocessableEntity, fmt.Sprintf("invalid policy %q for tool %q", p, name))
			return true
		}
	}

	// CLAUDE.md hard constraint 6 — the resolved map (after the legacy
	// mode/visible conversion above) IS this agent's prospective complete
	// per-agent policy map, because the persist closure below replaces
	// Tools.Builtin wholesale. Reject an incomplete map, or one carrying a
	// wildcard/unrecognized key, right here — the coverage guard further down
	// cannot see either defect (the seeded global ceiling covers the whole
	// catalog, so it reports zero gaps for any agent-side map, empty included).
	// This also gives the documented 400 for the legacy mode="explicit" +
	// visible[] shape sent alone, exactly as
	// contracts/components/schemas/AgentToolsUpdateRequest.yaml describes.
	if defects := config.ValidateSubmittedToolPolicyMap(
		rau.builtinPolicies, buildKnownBuiltinToolNames(),
	); !defects.Empty() {
		jsonErr(rau.w, http.StatusBadRequest, "builtin.policies "+defects.String())
		return true
	}
	return false
}

// validateMCPBindings normalizes MCP bindings and rejects references to unconfigured servers.
func (rau *restAPIUpdateAgentTools) validateMCPBindings() bool {
	// Validate MCP server IDs reference configured servers. Computed before
	// the locked validate+persist IIFE below (it does not feed
	// config.ValidateToolPolicyCoverage, only the MCP section of the persist
	// payload), then captured by that IIFE's persist closure.

	// The MCP binding list comes from the top-level `mcp` when present, or
	// from `config.mcp` on a D-86 round-trip body; the two are the same
	// shape on the wire but distinct generated types, so they are normalised
	// into one list before validation.
	type mcpBindingWire struct {
		Id    string
		Tools *[]string
	}
	var mcpBindings []mcpBindingWire
	switch {
	case rau.req.Mcp != nil && rau.req.Mcp.Servers != nil:
		rau.mcpSpecified = true
		for _, s := range *rau.req.Mcp.Servers {
			mcpBindings = append(mcpBindings, mcpBindingWire{Id: s.Id, Tools: s.Tools})
		}
	case rau.roundTrip && rau.req.Config.Mcp != nil && rau.req.Config.Mcp.Servers != nil:
		rau.mcpSpecified = true
		for _, s := range *rau.req.Config.Mcp.Servers {
			mcpBindings = append(mcpBindings, mcpBindingWire{Id: s.Id, Tools: s.Tools})
		}
	}
	if mcpBindings != nil {
		configuredServers := rau.cfg.Tools.MCP.Servers
		for _, s := range mcpBindings {
			if s.Id == "" {
				jsonErr(rau.w, http.StatusUnprocessableEntity, "mcp.servers[].id must not be empty")
				return true
			}
			if _, exists := configuredServers[s.Id]; !exists {
				jsonErr(rau.w, http.StatusUnprocessableEntity, fmt.Sprintf("MCP server %q is not configured", s.Id))
				return true
			}
			var toolList []string
			if s.Tools != nil {
				toolList = *s.Tools
			}
			binding := config.AgentMCPServerBinding{ID: s.Id, Tools: toolList, ToolsSpecified: s.Tools != nil}
			if err := config.ValidateAgentMCPServerBinding(binding); err != nil {
				jsonErr(rau.w, http.StatusUnprocessableEntity, err.Error())
				return true
			}
			rau.mcpServers = append(rau.mcpServers, struct {
				ID             string
				Tools          []string
				ToolsSpecified bool
			}{ID: s.Id, Tools: toolList, ToolsSpecified: s.Tools != nil})
		}
	}
	return false
}

// persistPolicy validates policy coverage and persists the agent tool configuration atomically.
func (rau *restAPIUpdateAgentTools) persistPolicy() bool {
	complete := make(map[string]config.ToolPolicy, len(rau.builtinPolicies))
	for name, policy := range rau.builtinPolicies {
		complete[name] = config.ToolPolicy(policy)
	}
	known := buildKnownBuiltinToolNames()
	ceiling := make(map[string]config.ToolPolicy, len(known))
	for name := range known {
		policy := config.ToolPolicy(rau.cfg.Sandbox.ToolPolicies[name])
		if policy == "" {
			policy = config.ToolPolicyDeny
		}
		ceiling[name] = policy
	}
	overrides, selectErr := agentmutation.SelectOverrides(complete, rau.req.OverrideNames, ceiling)
	if selectErr != nil {
		if errors.Is(selectErr, agentmutation.ErrCeilingChanged) {
			jsonErr(rau.w, http.StatusConflict, selectErr.Error())
		} else {
			jsonErr(rau.w, http.StatusBadRequest, selectErr.Error())
		}
		return true
	}
	mutation, updateErr := agentstore.New(rau.a.homePath).MutateState(rau.agentID, rau.req.Revision, func(agentRec *config.AgentConfig) error {
		if err := agentmutation.ValidateFields(*agentRec, []string{"tools_cfg"}); err != nil {
			return err
		}
		if agentRec.Tools == nil {
			agentRec.Tools = &config.AgentToolsCfg{}
		}
		agentRec.Tools.Builtin.Policies = overrides
		if rau.mcpSpecified {
			servers := make([]config.AgentMCPServerBinding, 0, len(rau.mcpServers))
			for _, s := range rau.mcpServers {
				servers = append(servers, config.AgentMCPServerBinding{ID: s.ID, Tools: s.Tools, ToolsSpecified: s.ToolsSpecified})
			}
			agentRec.Tools.MCP.Servers = servers
		}
		return nil
	}, nil)
	if updateErr != nil {
		var fieldErr *agentmutation.FieldError
		switch {
		case errors.Is(updateErr, agentstore.ErrInvalidRevision):
			jsonErr(rau.w, http.StatusBadRequest, updateErr.Error())
		case errors.Is(updateErr, agentstore.ErrRevisionConflict):
			jsonErr(rau.w, http.StatusConflict, updateErr.Error())
		case errors.As(updateErr, &fieldErr):
			jsonErr(rau.w, http.StatusForbidden, fieldErr.Error())
		case errors.Is(updateErr, entity.ErrNotFound):
			jsonErr(rau.w, http.StatusNotFound, updateErr.Error())
		default:
			writeConfigurationMutationFailure(rau.w, mutation)
		}
		return true
	}
	return false
}

// reloadAndRespond waits for the runtime reload and returns the updated tool registry response.
func (rau *restAPIUpdateAgentTools) reloadAndRespond() {
	// Trigger a reload AND WAIT for it (triggerReloadAndWaitOutcome, not a bare
	// TriggerReload — mirrors createAgent/updateAgent/deleteAgent) so the
	// agent's atomic toolPolicy pointer (pkg/agent/instance.go:290 —
	// populated by ReloadProviderAndConfig) is actually swapped to the new
	// policy before this handler responds. A bare TriggerReload only
	// enqueues the reload and returns before the registry swap happens —
	// without waiting, the next turn's resolveToolPolicyAtExec /
	// FilterToolsByPolicy could still see the previous snapshot for as long
	// as that swap takes to land, and (e.g.) an exec call freshly bumped to
	// "ask" would run as "allow" because LoadToolPolicy returns the stale
	// pointer. triggerReloadAndWaitOutcome already treats
	// ErrReloadNotConfigured (unit tests without the full gateway reload
	// pipeline wired) as a no-op, so a non-nil error here is always a genuine
	// reload failure. This is a fail-open authorization path — returning 200
	// while the rebuild is still queued means a tool freshly bumped to
	// "deny"/"ask" keeps executing as "allow" for the duration.
	//
	// UAT batch3 S67 (docs/internal/qa/uat-report-full-tool-catalog-batch3-2026-09-02.md,
	// finding #4): before this fix, an unconfirmed-but-error-free reload
	// (confirmed=false, err=nil) fell through to a 200 with only a
	// server-side Warn log — a caller had no way to know the tool-policy
	// tightening they just requested (e.g. create_skill allow/deny -> ask)
	// might not be enforced yet, and a tool call dispatched immediately
	// after could still run under the STALE, more permissive snapshot. This
	// mirrors waitForReload's OWN documented incident ("Persisted-but-not-
	// live is a real, caller-visible state and it has to surface as one") —
	// putToolPolicies (the global tool-policy PUT) already treats an
	// unconfirmed reload as a hard failure via triggerReloadAndWait; this
	// per-agent endpoint used the richer Outcome variant specifically to
	// distinguish the two cases, then silently discarded the distinction.
	// Now both variants agree: an unconfirmed reload is never reported as a
	// plain, unqualified success.
	if confirmed, err := rau.a.triggerReloadAndWaitOutcome(); err != nil {
		slog.Error("agent tools update: reload failed — in-memory policy not updated",
			"agent_id", rau.agentID, "error", err)
		if auditLogger := rau.a.agentLoop.AuditLogger(); auditLogger != nil {
			if auditErr := audit.EmitSecuritySettingChange(
				rau.r.Context(), auditLogger, "agent.tools_policy",
				map[string]any{"agent_id": rau.agentID, "saved": true},
				map[string]any{"agent_id": rau.agentID, "reload_error": err.Error()},
			); auditErr != nil {
				slog.Error("rest: audit emit agent tools reload failure", "error", auditErr)
			}
		}
		rau.r.Header.Set("X-Omnipus-Activation-Failed", "config saved but in-memory reload failed; restart the gateway or retry")
		rau.a.HandleAgentToolsRegistry(rau.w, rau.r, rau.agentID)
		return
	} else if !confirmed {
		slog.Error("rest: agent tools update: reload did not confirm within the poll window; "+
			"in-memory tool policy may not yet reflect the new config", "agent_id", rau.agentID)
		if auditLogger := rau.a.agentLoop.AuditLogger(); auditLogger != nil {
			if auditErr := audit.EmitSecuritySettingChange(
				rau.r.Context(), auditLogger, "agent.tools_policy",
				map[string]any{"agent_id": rau.agentID, "saved": true},
				map[string]any{"agent_id": rau.agentID, "reload_error": "reload did not confirm within the poll window"},
			); auditErr != nil {
				slog.Error("rest: audit emit agent tools reload unconfirmed", "error", auditErr)
			}
		}
		rau.r.Header.Set("X-Omnipus-Activation-Failed", "config saved but the in-memory reload did not confirm; retry or restart the gateway")
		rau.a.HandleAgentToolsRegistry(rau.w, rau.r, rau.agentID)
		return
	}
	// Use HandleAgentToolsRegistry so the PUT response emits `tools` (not
	// `effective_tools`) — both paths must share the same wire shape to match
	// the AgentToolsResponse spec and the SPA Zod schema (_agentToolsSchema).
	rau.a.HandleAgentToolsRegistry(rau.w, rau.r, rau.agentID)
}
