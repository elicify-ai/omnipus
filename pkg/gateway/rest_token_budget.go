//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Token-budget settings endpoint (ADR-053 D12/R§8.3, FE-6 / US-13, G-14).
//
// GET  /api/v1/settings/token-budget — read the live OVERALL token-budget status
// PUT  /api/v1/settings/token-budget — persist the operator-set ceiling
//
// Both routes are wrapped with withAuth (any authenticated user per A2/G-02 —
// the same posture as /settings/memory: a non-sensitive operational knob).
//
// GET reads the LIVE pool (pkg/agent.TokenBudget via agentLoop.TokenBudget()):
// budget/consumed/remaining/exhausted reflect the restart-gated live ceiling
// and spend reconciled at boot and debited since. PUT persists
// cfg.Planning.TokenBudget to config.json via safeUpdateConfigJSON; the ceiling
// is restart-gated (R§8.3e/FR-177 — a live change would straddle two budgets,
// the N-15 hazard), so PUT writes disk ONLY and does NOT touch the live cap.
// The PUT response therefore reports the just-persisted ceiling (the operator's
// intent, so the setting is confirmed saved) alongside the live consumed total,
// with an advisory that calls out the restart-required + token≠dollar-cap
// (R§8.3b) caveats. The live lever for runaway spend remains the existing
// Stop/cancel cascade — no live token cut is added here.
//
// The response is the generated gen.TokenBudgetStatus (Constraint #8). The PUT
// request body is the single operator-set field (`{"budget": int}`, 0 = the
// unbounded sentinel R§8.3a) — there is no generated request type and no
// hand-written package-level wire struct; the body is decoded inline (the
// contract-first lint flags package-level structs with ≥2 json tags, not a
// function-local single-field decoder).

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// HandleTokenBudgetSettings dispatches GET and PUT /api/v1/settings/token-budget.
func (a *restAPI) HandleTokenBudgetSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getTokenBudgetSettings(w, r)
	case http.MethodPut:
		a.putTokenBudgetSettings(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// buildTokenBudgetStatus assembles the wire status from a ceiling and a live
// consumed total. remaining/exhausted are derived (not read separately) so the
// result is internally consistent: remaining = budget-consumed (floored at 0,
// -1 when budget==0 i.e. unbounded), exhausted = budget>0 && consumed>=budget.
//
// by_scope is NOT yet tracked — the pool debits one aggregate consumed counter
// (R§8.3d), so the per-scope breakdown is returned as zeros (display-only per
// the schema; per-scope accounting is a future direction, INV-8 notes the
// breakdown is display-only, not a separate budget).
func buildTokenBudgetStatus(budget, consumed int64, advisory string) gen.TokenBudgetStatus {
	var remaining int64
	exhausted := false
	if budget == 0 {
		remaining = -1 // unbounded sentinel (matches TokenBudget.Remaining)
	} else {
		exhausted = consumed >= budget
		remaining = budget - consumed
		if remaining < 0 {
			remaining = 0
		}
	}
	resp := gen.TokenBudgetStatus{
		Budget:    int(budget),
		Consumed:  int(consumed),
		Remaining: int(remaining),
		Exhausted: exhausted,
		// by_scope: all zero — per-scope spend is not yet tracked (see doc comment).
	}
	if advisory != "" {
		a := advisory // copy to take address
		resp.Advisory = &a
	}
	return resp
}

// getTokenBudgetSettings returns the live OVERALL token-budget status. When the
// ceiling is the 0 sentinel (unbounded, FR-175) the response carries the
// persistent R§8.3a advisory.
func (a *restAPI) getTokenBudgetSettings(w http.ResponseWriter, _ *http.Request) {
	tb := a.agentLoop.TokenBudget()
	var budget, consumed int64
	advisory := ""
	if tb != nil {
		budget = tb.Cap()
		consumed = tb.Consumed()
		if tb.IsUnbounded() {
			advisory = agent.UnboundedAdvisory()
		}
	} else {
		advisory = agent.UnboundedAdvisory()
	}
	jsonOK(w, buildTokenBudgetStatus(budget, consumed, advisory))
}

// putTokenBudgetSettings persists the operator-set ceiling to config.json. The
// ceiling is restart-gated (R§8.3e/FR-177): this writes disk only — the live
// pool's cap is NOT touched here (a live change would straddle two budgets).
// The response reports the just-persisted ceiling (operator's intent) with the
// live consumed total, and an advisory carrying the restart-required +
// token≠dollar-cap (R§8.3b) caveats.
func (a *restAPI) putTokenBudgetSettings(w http.ResponseWriter, r *http.Request) {
	// Inline single-field decoder — no hand-written package-level wire struct
	// (Constraint #8: the only cross-boundary type is gen.TokenBudgetStatus).
	var body struct { // not-wire-format: inline PUT body, no package-level wire struct
		Budget *int `json:"budget,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %s", err))
		return
	}
	if body.Budget == nil {
		jsonErr(w, http.StatusBadRequest, `"budget" is required`)
		return
	}
	if *body.Budget < 0 {
		jsonErr(w, http.StatusBadRequest, "budget must be ≥ 0 (0 = unbounded)")
		return
	}

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error { //nolint:gocritic
		planning, _ := m["planning"].(map[string]any)
		if planning == nil {
			planning = make(map[string]any)
			m["planning"] = planning
		}
		planning["token_budget"] = *body.Budget
		return nil
	}); err != nil {
		slog.Error("rest: PUT /settings/token-budget: failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to update token-budget setting")
		return
	}

	// Respond with the persisted ceiling (operator's intent) + the live consumed
	// total. The live cap is NOT reloaded (restart-gated) — consumed reflects
	// actual spend, while budget reflects what was just saved. The advisory
	// carries the restart-required + R§8.3b token≠dollar-cap caveats (or the
	// R§8.3a unbounded advisory when budget was set back to 0).
	persisted := int64(*body.Budget)
	var consumed int64
	if tb := a.agentLoop.TokenBudget(); tb != nil {
		consumed = tb.Consumed()
	}
	advisory := agent.SetTimeWarning() + " Restart required for the new ceiling to take effect (R§8.3e)."
	if persisted == 0 {
		advisory = agent.UnboundedAdvisory()
	}
	jsonOK(w, buildTokenBudgetStatus(persisted, consumed, advisory))
}
