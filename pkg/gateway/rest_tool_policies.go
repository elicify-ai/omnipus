// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// HandleToolPolicies handles GET/PUT /api/v1/security/tool-policies.
//
// GET returns the current global tool policy configuration:
//
//	{
//	  "policies": {"exec": "ask", "browser_evaluate": "deny"}
//	}
//
// PUT accepts the same format, validates all policy values, and persists to
// config.json under sandbox.tool_policies via safeUpdateConfigJSON (preserves
// credential refs). There is no default_policy field (CLAUDE.md hard
// constraint 6): every static builtin tool must resolve from an explicit,
// literal entry either here (globally) or in an agent's
// tools.builtin.policies map — putToolPolicies rejects a PUT that would
// leave any agent with an uncovered tool (config.ValidateToolPolicyCoverage)
// before persisting anything. Changes are audit-logged per SEC-15. After the
// write, putToolPolicies calls TriggerReload to enforce the new policy on
// already-running agents by rebuilding every agent instance with the new
// global policy — SwapConfig alone does not (see the O7 hot-path note in
// putToolPolicies). A reload failure is logged loudly and never masks the
// successful persist (the policy still applies after the next restart).
//
// Valid policy values: "allow", "ask", "deny".
func (a *restAPI) HandleToolPolicies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := a.agentLoop.GetConfig()
		// Coerce policies to the generated map type — never null (Ava-chat bug class).
		policies := make(map[string]gen.GlobalToolPoliciesPolicies)
		for k, v := range cfg.Sandbox.ToolPolicies {
			policies[k] = gen.GlobalToolPoliciesPolicies(v)
		}
		jsonOK(w, gen.GlobalToolPolicies{
			Policies: policies,
		})

	case http.MethodPut:
		// PUT mutates tool policies. GET and PUT are both readable/writable by
		// the single authenticated account — withAuth (applied by the caller)
		// is sufficient, matching how cli-validate is handled (ADR-030
		// precedent).
		a.putToolPolicies(w, r)

	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// putToolPolicies is the body of PUT /api/v1/security/tool-policies.
// It is called only after withAuth has confirmed the caller holds a valid session.
func (a *restAPI) putToolPolicies(w http.ResponseWriter, r *http.Request) {
	// The step-up re-auth gate (requireReAuth) was INTENTIONALLY REMOVED here for
	// the global tool-policy PUT per UAT feedback (operator found re-typing the
	// password to change a tool permission to be unnecessary friction). This is a
	// deliberate, scoped loosening — do NOT restore it. Authorization is still
	// enforced: withAuth requires a valid session (single-account model — no
	// further role gate applies). The password re-prompt is the only control
	// removed. The same gate remains in force on
	// Integrations/Providers/Sandbox/Credentials/Performance PUTs.
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

	// Reject any submitted KEY that isn't a real, known static builtin tool
	// name (or the MCP-namespace carve-out). CLAUDE.md hard constraint 6:
	// wildcard/garbage keys must never be silently accepted anywhere in the
	// tool-policy write surface. This endpoint's per-value loop above only
	// checked that each VALUE was allow/ask/deny — a key like "*" or a
	// typo'd tool name has a perfectly valid value and slipped through
	// unvalidated, getting persisted verbatim into sandbox.tool_policies
	// (confirmed live: PUT {"*":"allow","not_a_real_tool_xyz":"ask"}
	// returned 200 and echoed both back). Unlike the agent-level tools-write
	// endpoints, this one deliberately does NOT require the submitted map to
	// be COMPLETE (ValidateSubmittedToolPolicyMap's Missing list is ignored
	// on purpose) — a sparse global ceiling is the intended shape; the
	// seeded "worker" agent, for one, is deliberately built to rely on it
	// for most tools (coreAgentSeed's tightenGlobalCeiling). Completeness of
	// the resulting (agent OR global) coverage is what
	// withToolPolicyCoverageGuard below already enforces; this check only
	// closes the separate "was every key real" gap.
	if defects := config.ValidateSubmittedToolPolicyMap(body.Policies, buildKnownBuiltinToolNames()); len(defects.Invalid) > 0 {
		jsonErr(w, http.StatusBadRequest,
			fmt.Sprintf("policies: unrecognized tool name(s): %s", strings.Join(defects.Invalid, ", ")))
		return
	}

	// Convert map[string]GlobalToolPoliciesPolicies → map[string]string for config persistence.
	policiesStr := make(map[string]string, len(body.Policies))
	for k, v := range body.Policies {
		policiesStr[k] = string(v)
	}

	// CLAUDE.md hard constraint 6 / config.ValidateToolPolicyCoverage: this
	// endpoint fully replaces the global sandbox.tool_policies map on
	// persist (below) — policiesStr IS the prospective new global map.
	// Reject the write 400 with the full gap list if it, together with
	// every agent's CURRENT per-agent tools.builtin.policies, would leave
	// any static builtin tool uncovered for any agent. This is the
	// mechanism that also structurally closes off a PUT here silently
	// resetting the global policy to fully-permissive for every agent —
	// incompleteness is rejected up front, before anything is persisted or
	// applied to the live in-memory config.
	//
	// The validate step and the persist step run inside ONE a.configMu-locked
	// critical section (closing a TOCTOU race two concurrent tool-policy
	// writes could otherwise open — see updateConfigJSONLocked's doc comment
	// in rest.go), via withToolPolicyCoverageGuard: it always fetches the
	// config FRESH, inside a.configMu, immediately before validating. Returns
	// false once it has already written the HTTP response (error case), so
	// the caller just returns.
	if ok := a.withToolPolicyCoverageGuard(
		w,
		func(c *config.Config) {
			c.Sandbox.ToolPolicies = policiesStr
		},
		func(gaps []config.CoverageGap) string {
			return fmt.Sprintf(
				"tool policy coverage incomplete (%d gap(s)): %s",
				len(gaps), joinCoverageGapMessages(gaps),
			)
		},
		// Persist to config.json under the sandbox key, preserving all other fields.
		func(m map[string]any) error {
			sandbox, _ := m["sandbox"].(map[string]any)
			if sandbox == nil {
				sandbox = map[string]any{}
				m["sandbox"] = sandbox
			}
			if body.Policies != nil {
				sandbox["tool_policies"] = policiesStr
			} else {
				// Explicit null from the client clears the map.
				sandbox["tool_policies"] = map[string]any{}
			}
			return nil
		},
		"rest: update tool policies",
	); !ok {
		return
	}

	// SEC-15: audit-log the policy change.
	if a.agentLoop != nil {
		if auditLogger := a.agentLoop.AuditLogger(); auditLogger != nil {
			if err := auditLogger.Log(&audit.Entry{
				Event:    audit.EventPolicyEval,
				Decision: audit.DecisionAllow,
				Details: map[string]any{
					"action":       "tool_policies_update",
					"policy_count": len(body.Policies),
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
	// reload is required.
	//
	// triggerReloadAndWait (not a bare TriggerReload) — mirrors
	// createAgent/updateAgent/updateAgentTools/setGodMode: a bare TriggerReload
	// only enqueues the reload onto runningServices.manualReloadChan and returns
	// immediately — the actual registry rebuild (TriggerReload → executeReload →
	// ReloadProviderAndConfig → NewAgentRegistry, which rebuilds every instance
	// with the new GlobalPolicies) happens on a separate goroutine. Without
	// waiting, a tool call dispatched the instant this handler responds 200
	// could still be evaluated under the PREVIOUS global policy for as long as
	// that goroutine takes to run — a tightening edit (e.g. exec: allow → deny)
	// must be enforced before the success response, not merely persisted and
	// queued. triggerReloadAndWait already treats ErrReloadNotConfigured (unit
	// tests / minimal embeddings without the full gateway reload pipeline
	// wired) as a no-op, so a non-nil error here is always a genuine reload
	// failure.
	//
	// Reload-failure semantics mirror updateAgent's soul path: the config IS
	// persisted, so we never 500 (that would wrongly signal the write failed).
	// A genuine reload error is logged at Error — it means running agents may
	// keep the previous global policy until the next restart, which an operator
	// must see in the logs.
	//
	// triggerReloadAndWait (not the bare TriggerReload): this is the fail-open
	// authorization-bypass path described above, so the response must not claim
	// the tightening is enforced while the rebuild is still queued. It also
	// absorbs ErrReloadNotConfigured (the no-reload-loop case in tests / minimal
	// embeddings) as a benign no-op, and — via services.beginReload's coalescing
	// — a request that arrives mid-reload is served by a follow-up reload
	// instead of being dropped, which used to leave the old policy live
	// indefinitely.
	if err := a.triggerReloadAndWait(); err != nil {
		slog.Error(
			"rest: reload after tool policies update failed; running agents may keep the previous global policy until restart",
			"error",
			err,
		)
	}

	slog.Info("rest: global tool policies updated",
		"policy_count", len(body.Policies),
	)

	// Return the persisted state. Changes take effect immediately because
	// triggerReloadAndWait above waited for every running agent to be rebuilt
	// with the new global policy.
	// body.Policies is already map[string]GlobalToolPoliciesPolicies — pass directly.
	respPolicies := make(map[string]gen.GlobalToolPoliciesPolicies)
	for k, v := range body.Policies {
		respPolicies[k] = v
	}
	jsonOK(w, gen.GlobalToolPolicies{
		Policies: respPolicies,
	})
}
