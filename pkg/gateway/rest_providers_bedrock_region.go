// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// rest_providers_bedrock_region.go — issue #800's own follow-up
// (orchestrator-approved, founder decision): a live refresh of AWS's own
// ListInferenceProfiles answer, triggered when a Bedrock provider row is set
// up or its region changes, cached on the row (config.json's own
// `bedrock_inference_profiles` — not secret, so no credential-store
// involvement beyond resolving the key needed to CALL AWS).
//
// This is deliberately best-effort and never blocks the PUT it rides on:
// AWS's own control-plane endpoint may not even accept an API key for this
// call (unconfirmed — see bedrock.ListInferenceProfiles's own doc comment),
// so any failure here — 403, network, anything — degrades silently to the
// catalog's own inference_profiles approximation
// (bedrock.ResolveModelIDLive), logged once as a WARN and never surfaced to
// the caller.

package gateway

import (
	"log/slog"
	"net/http"
	"strings"

	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/bedrock"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// refreshBedrockInferenceProfilesIfNeeded runs AFTER providerPutPersist's
// own config write already succeeded. It does nothing (returns immediately)
// unless this PUT was for a Bedrock-protocol row and either changed the key
// or set a region — the two triggers issue #800 names ("set up or its
// region changes"). It never returns an error: every failure path logs and
// returns.
func (a *restAPI) refreshBedrockInferenceProfilesIfNeeded(p *providerPut) {
	if !p.keyChanged && p.reqRegion == "" {
		return
	}
	row, known := providers_pkg.CatalogProvider(p.providerID)
	if !known || row.Protocol != catalog.ProtocolBedrock {
		return
	}

	apiKey := a.resolveBedrockRefreshAPIKey(p)
	if apiKey == "" {
		return
	}
	region := p.reqRegion
	if region == "" {
		region = row.Region
	}

	profiles, err := bedrock.ListInferenceProfiles(
		p.r.Context(), a.bedrockControlPlaneHTTPClient(), apiKey, region, a.bedrockControlPlaneBaseOverride,
	)
	if err != nil {
		slog.Warn("rest: bedrock ListInferenceProfiles lookup failed; "+
			"falling back to the catalog's own inference_profiles rules",
			"provider", p.providerID, "region", region, "error", err)
		return
	}
	if len(profiles) == 0 {
		return
	}
	a.persistBedrockInferenceProfiles(p.providerID, profiles)
}

// resolveBedrockRefreshAPIKey returns the key to authenticate the refresh
// call with: the plaintext key this very PUT just supplied when it changed
// the key, otherwise the row's EXISTING stored key (a region-only change
// carries no key of its own) — resolved from the PRE-PUT config snapshot
// (p.cfg), which is still accurate for api_key_ref on a region-only change.
// "" means no usable key was found; the caller treats that as "nothing to
// refresh with" rather than an error.
func (a *restAPI) resolveBedrockRefreshAPIKey(p *providerPut) string {
	if p.keyChanged {
		return derefStr(p.req.ApiKey)
	}
	for _, m := range p.cfg.Providers {
		if m.IsVirtual() || strings.TrimSpace(m.Provider) != p.providerID {
			continue
		}
		key := m.APIKey()
		if key == "" && m.APIKeyRef != "" {
			if v, err := a.resolveCredentialRef(m.APIKeyRef); err == nil {
				key = v
			}
		}
		return key
	}
	return ""
}

// bedrockControlPlaneHTTPClient is a plain client: the control-plane host is
// validated by bedrock.ValidateRegion before any URL is built (SSRF guard —
// see region.go's own doc comment), so no additional SSRF checker is needed
// here the way providerPutValidateKey needs one for an operator-typed
// api_base.
func (a *restAPI) bedrockControlPlaneHTTPClient() *http.Client {
	return http.DefaultClient
}

// persistBedrockInferenceProfiles writes the refreshed cache onto the
// row — a second, narrow safeUpdateConfigJSON write so the (possibly slow)
// network call above never runs inside providerPutPersist's own critical
// section. Best-effort: a write failure is logged, never surfaced — the
// region/key change itself already saved successfully.
func (a *restAPI) persistBedrockInferenceProfiles(providerID string, profiles map[string]string) {
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		providerList, _ := m["providers"].([]any)
		for _, entry := range providerList {
			model, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if strings.TrimSpace(strVal(model, "provider")) != providerID {
				continue
			}
			asAny := make(map[string]any, len(profiles))
			for k, v := range profiles {
				asAny[k] = v
			}
			model["bedrock_inference_profiles"] = asAny
			break
		}
		return nil
	}); err != nil {
		slog.Warn("rest: could not persist the bedrock inference profiles cache",
			"provider", providerID, "error", err)
		return
	}
	// Best-effort: the next resolution should see the fresh cache. A
	// reload failure here is not user-facing (the PUT itself already
	// answered 200) — log and move on, mirroring the WARN-only pattern
	// providerPutPersist's own reload-timeout branch uses.
	if _, err := a.triggerReloadAndWaitOutcome(); err != nil {
		slog.Warn("rest: reload after bedrock inference profiles refresh failed",
			"provider", providerID, "error", err)
	}
}
