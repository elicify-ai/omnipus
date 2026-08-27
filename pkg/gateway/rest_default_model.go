// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_default_model.go — GET/PUT /api/v1/providers/default-model (T068-11,
// ADR-068 FR-018/FR-019/FR-042, MAJ-002/MAJ-007).
//
// The route is registered as its OWN handler with adminWrap (withAuth →
// RequireNotBypass) in rest.go, ahead of the /api/v1/providers/ prefix
// dispatcher — "default-model" is a reserved path segment, never a provider
// id. The dynamic mux matches exact paths before subtree prefixes, so the
// registration order is belt-and-braces; the reserved-literal validator
// (isReservedProviderPathSegment) closes the remaining holes everywhere a
// provider id is accepted (PUT /providers/{id}, DELETE, the onboarding probe
// and completion).
package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// Wire-format limits from contracts/components/schemas/DefaultModelUpdateRequest.yaml.
const (
	defaultModelProviderMaxLen = 64
	defaultModelModelMaxLen    = 256
)

// EventProviderDefaultModelChanged is the audit event emitted on every
// successful default-model PUT (FR-018; registered in
// pkg/audit.IsValidEventName).
const EventProviderDefaultModelChanged = "provider.default_model.changed"

// isReservedProviderPathSegment reports whether id is one of the reserved
// /api/v1/providers/ path segments that are never provider ids (MAJ-002):
// "catalog" (the served catalog GET), "default-model" (this file's route),
// and "model-capabilities" (retired by ADR-067; reserved until S67 removes
// the last consumer). Every handler that accepts a provider id — PUT
// /providers/{id}, DELETE /providers/{id}, POST /onboarding/probe-provider,
// POST /onboarding/complete — must reject these before doing anything else.
func isReservedProviderPathSegment(id string) bool {
	switch id {
	case "catalog", "default-model", "model-capabilities":
		return true
	}
	return false
}

// jsonErrField writes an ErrorResponse carrying the `field` the error is
// about ({"error": ..., "field": ...} — the ADR-068 validation body shape).
func jsonErrField(w http.ResponseWriter, status int, msg, field string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(gen.ErrorResponse{Error: msg, Field: &field}); err != nil {
		slog.Debug("rest: write error response failed", "error", err)
	}
}

// HandleDefaultModel handles GET/PUT /api/v1/providers/default-model.
// Registered with adminWrap: 401 unauthenticated and 503 under dev-mode
// bypass are enforced by the middleware chain, not here.
func (a *restAPI) HandleDefaultModel(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getDefaultModel(w)
	case http.MethodPut:
		a.putDefaultModel(w, r)
	default:
		// The route accepts GET and PUT only; a DELETE here is a reserved-
		// literal probe, not a provider removal (MAJ-002 scenario row).
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// getDefaultModel returns the persisted pair with the resolved window, or
// 404 {"error":"no default model"} when no pair is set (fresh install before
// onboarding's explicit pick — FR-018).
func (a *restAPI) getDefaultModel(w http.ResponseWriter) {
	cfg := a.agentLoop.GetConfig()
	pair := cfg.Agents.Defaults.DefaultModel
	if pair.IsZero() {
		jsonErr(w, http.StatusNotFound, "no default model")
		return
	}
	jsonOK(w, defaultModelResponse(cfg, pair))
}

// putDefaultModel validates and persists the (provider, model) pair
// (FR-018): provider configured and connected|signed_in, model in the served
// catalog unless the row is custom or local (S67's single predicate,
// X-13/X-17 — any non-empty model, no live call, X-22), write under the
// config lock, audit provider.default_model.changed, reload and wait.
func (a *restAPI) putDefaultModel(w http.ResponseWriter, r *http.Request) {
	// Strict decode: the contract is additionalProperties:false, so an
	// unknown key (e.g. a ProviderUpdateRequest body aimed at a provider
	// named "default-model") is a 400 on shape unconditionally — never a
	// provider create (MAJ-002).
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return
	}
	var req gen.DefaultModelUpdateRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		jsonErr(w, http.StatusBadRequest,
			fmt.Sprintf("request body does not match DefaultModelUpdateRequest: %v", err))
		return
	}

	providerID := strings.TrimSpace(req.Provider)
	model := strings.TrimSpace(req.Model)
	switch {
	case providerID == "":
		jsonErrField(w, http.StatusBadRequest, "provider is required", "provider")
		return
	case len(providerID) > defaultModelProviderMaxLen:
		jsonErrField(w, http.StatusBadRequest,
			fmt.Sprintf("provider must be at most %d characters", defaultModelProviderMaxLen), "provider")
		return
	case model == "":
		jsonErrField(w, http.StatusBadRequest, "model is required", "model")
		return
	case len(model) > defaultModelModelMaxLen:
		jsonErrField(w, http.StatusBadRequest,
			fmt.Sprintf("model must be at most %d characters", defaultModelModelMaxLen), "model")
		return
	}

	cfg := a.agentLoop.GetConfig()

	// Provider configured and connected|signed_in (FR-018).
	if !a.providerConfiguredAndUsable(cfg, providerID) {
		jsonErrField(w, http.StatusBadRequest, "provider not configured", "provider")
		return
	}

	// Model must be one the configured row can actually SERVE — the same
	// question providers.CreateProvider (the boot/reload path) asks via
	// ResolveDefaultModelRow: an exact match on the row's legacy Model
	// field, the row's own Models[] list, the X-13/X-17/X-22 custom/local
	// bypass (any non-empty model, no live call), or — for a known,
	// non-custom, non-local catalog row — the served catalog. Calling the
	// SAME function CreateProvider calls (rather than a separately
	// hand-rolled catalog check) is load-bearing: it is what guarantees a
	// pair this PUT accepts with 200 is guaranteed to boot, and a pair it
	// cannot apply is rejected here instead of corrupting config.json (see
	// ResolveDefaultModelRow's doc for the incident this closes).
	if _, ok := providers.ResolveDefaultModelRow(cfg, a.providerCatalog, providerID, model); !ok {
		msg := "model not offered by provider"
		if catRow, known := providerCatalogRow(a.providerCatalog, providerID); known &&
			!catRow.Custom && catRow.Locality != catalog.LocalityLocal {
			// Preserve the more specific, existing wording for the common
			// case: a known cloud provider whose served catalog simply does
			// not carry this model id.
			msg = "model not in catalog for provider"
		}
		jsonErrField(w, http.StatusBadRequest, msg, "model")
		return
	}

	oldPair := cfg.Agents.Defaults.DefaultModel

	// Persist under the config lock (safeUpdateConfigJSON takes configMu and
	// refreshes the in-memory config on success).
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		defaults := ensureMap(m, "agents", "defaults")
		defaults["default_model"] = map[string]any{"provider": providerID, "model": model}
		return nil
	}); err != nil {
		slog.Error("rest: default-model PUT: config write failed", "error", err)
		jsonErr(w, http.StatusInternalServerError,
			fmt.Sprintf("could not persist default model: %v", err))
		return
	}

	// Audit provider.default_model.changed with the old and new pairs
	// (FR-018/MAJ-016). Best-effort: a log write failure never fails the PUT.
	if a.auditor != nil {
		if err := a.auditor.Log(&audit.Entry{
			Event:    EventProviderDefaultModelChanged,
			Decision: audit.DecisionAllow,
			Details: map[string]any{
				"old_provider": oldPair.Provider,
				"old_model":    oldPair.Model,
				"new_provider": providerID,
				"new_model":    model,
			},
		}); err != nil {
			slog.Warn("audit write failed", "event", EventProviderDefaultModelChanged, "error", err)
		}
	}

	// TriggerReload and wait; the change must be live for the next turn.
	confirmed, reloadErr := a.triggerReloadAndWaitOutcome()
	if reloadErr != nil || !confirmed {
		reason := "reload did not confirm within the wait deadline"
		if reloadErr != nil {
			reason = reloadErr.Error()
		}
		jsonErr(w, http.StatusInternalServerError,
			fmt.Sprintf("default model saved but config reload failed: %s", reason))
		return
	}

	fresh := a.agentLoop.GetConfig()
	jsonOK(w, defaultModelResponse(fresh, fresh.Agents.Defaults.DefaultModel))
}

// providerCatalogRow looks up id in cat, tolerating a nil catalog (no
// document loaded at all — E7) by reporting "not known" rather than
// panicking. Used only to choose the more specific error message on a
// rejected default-model PUT; the accept/reject decision itself is
// providers.ResolveDefaultModelRow's alone.
func providerCatalogRow(cat *catalog.Catalog, id string) (catalog.Provider, bool) {
	if cat == nil {
		return catalog.Provider{}, false
	}
	return cat.Provider(id)
}

// providerConfiguredAndUsable reports whether id names a configured
// providers[] row that is connected or signed_in: a row exists with this
// provider identity AND either it needs no vault credential (local /
// api_base-only rows never had one), its credential resolves to a non-empty
// value, or it authenticates by vendor sign-in (auth_method sign_in; the
// live signed-in/expired distinction arrives with T068-14's status
// machinery).
func (a *restAPI) providerConfiguredAndUsable(cfg *config.Config, id string) bool {
	for _, m := range cfg.Providers {
		if m == nil || strings.TrimSpace(m.Provider) != id {
			continue
		}
		if m.AuthMethod == config.AuthMethodSignIn {
			return true
		}
		if strings.TrimSpace(m.APIKeyRef) == "" {
			// No credential requirement at all (custom endpoint, local
			// model) — mirrors config.modelConfigCredentialUsable.
			return true
		}
		if m.APIKey() != "" {
			return true
		}
		if v, err := a.resolveCredentialRef(m.APIKeyRef); err == nil && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// defaultModelResponse projects the persisted pair onto the DefaultModel
// wire shape, with the window fields produced by ADR-066's exported
// ResolveWindow(provider, model) — the rungs WITHOUT the per-agent override
// (agentID "", cross-spec X-07). Exempt subprocess-CLI rows return
// context_window 0 with window_source absent; a local row nobody can size
// sets window_unknown true (X-08).
func defaultModelResponse(cfg *config.Config, pair config.DefaultModel) gen.DefaultModel {
	out := gen.DefaultModel{Provider: pair.Provider, Model: pair.Model}
	res := agent.ResolveWindow(cfg, pair.Provider, pair.Model, "")
	switch {
	case res.Exempt:
		zero := 0
		out.ContextWindow = &zero
	case res.Unknown:
		unknown := true
		out.WindowUnknown = &unknown
	case res.Window > 0:
		window := res.Window
		out.ContextWindow = &window
		src := gen.DefaultModelWindowSource(res.Source)
		if src.Valid() {
			out.WindowSource = &src
		}
	}
	return out
}
