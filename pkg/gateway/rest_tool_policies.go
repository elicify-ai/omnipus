//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"log/slog"
	"net/http"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/gateway/middleware"
)

// HandleToolPolicies handles GET/PUT /api/v1/security/tool-policies.
//
// GET returns the current global tool policy configuration:
//
//	{
//	  "default_policy": "allow",
//	  "policies": {"exec": "ask", "browser.evaluate": "deny"}
//	}
//
// PUT accepts the same format, validates all policy values, and persists to
// config.json under sandbox.tool_policies and sandbox.default_tool_policy via
// safeUpdateConfigJSON (preserves credential refs). Changes are audit-logged
// per SEC-15. The new policy is reflected immediately after the config reload
// triggered by safeUpdateConfigJSON.
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
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit
	var body gen.GlobalToolPolicies
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
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
	if body.DefaultPolicy == "" {
		body.DefaultPolicy = gen.GlobalToolPoliciesDefaultPolicyAllow
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

	// Persist to config.json under the sandbox key, preserving all other fields.
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		sandbox, _ := m["sandbox"].(map[string]any)
		if sandbox == nil {
			sandbox = map[string]any{}
			m["sandbox"] = sandbox
		}
		sandbox["default_tool_policy"] = string(body.DefaultPolicy)
		if body.Policies != nil {
			// Convert typed map to plain map[string]string for JSON serialization.
			plain := make(map[string]string, len(body.Policies))
			for k, v := range body.Policies {
				plain[k] = string(v)
			}
			sandbox["tool_policies"] = plain
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
					"default_policy": string(body.DefaultPolicy),
					"policy_count":   len(body.Policies),
				},
			}); err != nil {
				slog.Warn("rest: audit log tool policies update", "error", err)
			}
		}
	}

	slog.Info("rest: global tool policies updated",
		"default_policy", string(body.DefaultPolicy),
		"policy_count", len(body.Policies),
	)

	// Return the persisted state — use the already-typed body directly.
	// Coerce nil Policies to empty map — never null (Ava-chat bug class).
	if body.Policies == nil {
		body.Policies = map[string]gen.GlobalToolPoliciesPolicies{}
	}
	jsonOK(w, body)
}
