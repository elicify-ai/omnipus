//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/middleware"
)

// HandleToolPolicies handles GET/PUT /api/v1/security/tool-policies.
//
// GET returns the current global tool policy configuration:
//
//	{
//	  "default_policy": "allow",
//	  "policies": {"exec": "ask", "browser_evaluate": "deny"}
//	}
//
// PUT accepts the same format, validates all policy values, and persists to
// config.json under sandbox.tool_policies and sandbox.default_tool_policy via
// safeUpdateConfigJSON (preserves credential refs). Changes are audit-logged
// per SEC-15. After the write, putToolPolicies calls TriggerReload to enforce
// the new policy on already-running agents by rebuilding every agent instance
// with the new global policy — SwapConfig alone does not (see the O7 hot-path
// note in putToolPolicies). A reload failure is logged loudly and never masks
// the successful persist (the policy still applies after the next restart).
//
// Valid policy values: "allow", "ask", "deny".
func (a *restAPI) HandleToolPolicies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := a.agentLoop.GetConfig()
		defaultPolicy := cfg.Sandbox.DefaultToolPolicy
		if defaultPolicy == "" {
			defaultPolicy = "allow"
		}
		// Coerce policies to the generated map type — never null (Ava-chat bug class).
		policies := make(map[string]gen.GlobalToolPoliciesPolicies)
		for k, v := range cfg.Sandbox.ToolPolicies {
			policies[k] = gen.GlobalToolPoliciesPolicies(v)
		}
		jsonOK(w, gen.GlobalToolPolicies{
			DefaultPolicy: gen.GlobalToolPoliciesDefaultPolicy(defaultPolicy),
			Policies:      policies,
		})

	case http.MethodPut:
		// PUT mutates tool policies — admin-only (Issue #98). GET remains
		// readable by all authenticated users. Wrapper is built once
		// (sync.Once) so each PUT doesn't allocate a new middleware chain.
		a.adminPutPoliciesOnce.Do(func() {
			a.adminPutPoliciesHandler = middleware.RequireAdmin(
				http.HandlerFunc(a.putToolPolicies),
			)
		})
		a.adminPutPoliciesHandler.ServeHTTP(w, r)

	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// putToolPolicies is the admin-only body of PUT /api/v1/security/tool-policies.
// It is called only after RequireAdmin has confirmed the caller holds admin role.
func (a *restAPI) putToolPolicies(w http.ResponseWriter, r *http.Request) {
	// The step-up re-auth gate (requireReAuth) was INTENTIONALLY REMOVED here for
	// the global tool-policy PUT per UAT feedback (operator found re-typing the
	// password to change a tool permission to be unnecessary friction). This is a
	// deliberate, scoped loosening — do NOT restore it. Authorization is still
	// enforced: RequireAdmin (applied in HandleToolPolicies above) confirms the
	// caller holds the admin role, and withAuth requires a valid session. The
	// password re-prompt is the only control removed. The same gate remains in
	// force on Integrations/Providers/Sandbox/Credentials/Performance PUTs.
	if user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig); !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit
	var body gen.GlobalToolPolicies
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "GlobalToolPolicies", &body, validateEnabled) {
		return
	}

	// Validate default_policy.
	switch string(body.DefaultPolicy) {
	case "", "allow", "ask", "deny":
		// valid; empty string is treated as "allow"
	default:
		jsonErr(w, http.StatusBadRequest, "default_policy must be 'allow', 'ask', or 'deny'")
		return
	}
	defaultPolicy := string(body.DefaultPolicy)
	if defaultPolicy == "" {
		defaultPolicy = "allow"
	}

	// Validate per-tool policies.
	for toolName, p := range body.Policies {
		switch string(p) {
		case "allow", "ask", "deny":
			// valid
		default:
			jsonErr(w, http.StatusBadRequest,
				"policies["+toolName+"]: value must be 'allow', 'ask', or 'deny'")
			return
		}
	}

	// Convert map[string]GlobalToolPoliciesPolicies → map[string]string for config persistence.
	policiesStr := make(map[string]string, len(body.Policies))
	for k, v := range body.Policies {
		policiesStr[k] = string(v)
	}

	// Persist to config.json under the sandbox key, preserving all other fields.
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		sandbox, _ := m["sandbox"].(map[string]any)
		if sandbox == nil {
			sandbox = map[string]any{}
			m["sandbox"] = sandbox
		}
		sandbox["default_tool_policy"] = defaultPolicy
		if body.Policies != nil {
			sandbox["tool_policies"] = policiesStr
		} else {
			// Explicit null from the client clears the map.
			sandbox["tool_policies"] = map[string]any{}
		}
		return nil
	}); err != nil {
		slog.Error("rest: update tool policies", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not save config")
		return
	}

	// SEC-15: audit-log the policy change.
	if a.agentLoop != nil {
		if auditLogger := a.agentLoop.AuditLogger(); auditLogger != nil {
			if err := auditLogger.Log(&audit.Entry{
				Event:    audit.EventPolicyEval,
				Decision: audit.DecisionAllow,
				Details: map[string]any{
					"action":         "tool_policies_update",
					"default_policy": defaultPolicy,
					"policy_count":   len(body.Policies),
				},
			}); err != nil {
				slog.Warn("rest: audit log tool policies update", "error", err)
			}
		}
	}

	// O7 hot-path (BLOCKER): persisting + SwapConfig alone does NOT rebuild the
	// already-constructed agent instances. Each instance carries a boot-time
	// toolPolicy snapshot (agentToolsCfgToPolicy → ToolPolicyCfg.GlobalPolicies)
	// that exec-time resolution (resolveSingleToolPolicy → FilterToolsByPolicy ←
	// LoadToolPolicy) reads. Without a rebuild a global "deny" set here would be
	// persisted + shown enforced by GET, while running agents keep executing the
	// "denied" tool until a restart — a fail-open authorization bypass on a
	// tightening edit. safeUpdateConfigJSON additionally registers the written
	// hash with selfWriteReg to suppress the file-watcher reload, so an explicit
	// TriggerReload is required. TriggerReload → executeReload →
	// ReloadProviderAndConfig → NewAgentRegistry rebuilds every instance with the
	// new GlobalPolicies, mirroring the delegation_policy path in updateAgent.
	//
	// Reload-failure semantics mirror updateAgent's delegation_policy path: the
	// config IS persisted, so we never 500 (that would wrongly signal the write
	// failed). ErrReloadNotConfigured is the no-reload-loop case (tests / minimal
	// embeddings) and is benign. A genuine reload error is logged at Error — it
	// means running agents may keep the previous global policy until the next
	// restart, which an operator must see in the logs.
	if err := a.agentLoop.TriggerReload(); err != nil {
		if errors.Is(err, agent.ErrReloadNotConfigured) {
			slog.Debug("rest: tool policies persisted; live reload not configured on this loop",
				"error", err)
		} else {
			slog.Error(
				"rest: reload after tool policies update failed; running agents may keep the previous global policy until restart",
				"error",
				err,
			)
		}
	}

	slog.Info("rest: global tool policies updated",
		"default_policy", defaultPolicy,
		"policy_count", len(body.Policies),
	)

	// Return the persisted state. Changes take effect immediately because the
	// TriggerReload above rebuilt every running agent with the new global policy.
	// body.Policies is already map[string]GlobalToolPoliciesPolicies — pass directly.
	respPolicies := make(map[string]gen.GlobalToolPoliciesPolicies)
	for k, v := range body.Policies {
		respPolicies[k] = v
	}
	jsonOK(w, gen.GlobalToolPolicies{
		DefaultPolicy: gen.GlobalToolPoliciesDefaultPolicy(defaultPolicy),
		Policies:      respPolicies,
	})
}
