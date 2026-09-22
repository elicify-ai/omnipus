// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// rest_providers_bedrock_probe.go — orchestrator review round 2, D1: the
// region a Bedrock probe (key-validation) call resolves against. Shared by
// the onboarding probe (HandleOnboardingProbeProvider, rest_onboarding.go)
// and the PUT-triggered save-time key check (providerPutValidateKey,
// rest_providers.go) so the two probe paths cannot drift from each other —
// the same failure mode this file's own bug started as: the onboarding
// probe never read a region at all, so the key check silently ran against
// the catalog's us-east-1 default however far east or west the operator's
// own AWS region picker said otherwise.
//
// This mirrors — for the PROBE, not a real turn — exactly what
// factory_provider.go's ProtocolBedrock case already does for a real turn:
// bedrock.ResolveRegion's precedence (row/request -> AWS_REGION -> catalog
// default), bedrock.ValidateRegion before any URL is built (the SSRF guard
// for a region-derived URL), and bedrock.ResolveModelID's cross-region
// inference profile group prefix — read from catalog DATA only, never a
// hand-typed model list.

package gateway

import (
	"os"
	"strings"

	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/bedrock"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// resolveBedrockPersistedRegion returns a Bedrock row's EXISTING persisted
// region from the PRE-PUT config snapshot (p.cfg) — same lookup style as
// resolveBedrockRefreshAPIKey (rest_providers_bedrock_region.go), which this
// mirrors on purpose: a request that carries no region field of its own (a
// key-only edit) still resolves the row's ACTUAL configured region, never
// silently falling straight through to AWS_REGION or the catalog default.
//
// Shared by providerPutValidateKey's save-time key check (D1, round 2) and
// refreshBedrockInferenceProfilesIfNeeded's live-profile-cache refresh (D1
// gap, round 3) so this lookup is written once, not duplicated per caller.
// Returns "" when the row is not found (a brand-new row this same PUT is
// creating has nothing persisted yet).
func resolveBedrockPersistedRegion(p *providerPut) string {
	for _, m := range p.cfg.Providers {
		if m.IsVirtual() || strings.TrimSpace(m.Provider) != p.providerID {
			continue
		}
		return m.Region
	}
	return ""
}

// bedrockRegionsRow reports whether row is a Bedrock-protocol catalog row
// with its own region picker — the ONE gate every Bedrock-probe branch
// below checks first, matching the Bedrock region contract's "ignored for a
// provider whose catalog entry carries no regions" rule.
func bedrockRegionsRow(row catalog.Provider, isCatalogRow bool) bool {
	return isCatalogRow && row.Protocol == catalog.ProtocolBedrock && len(row.Regions) > 0
}

// bedrockProbeRegionResolution resolves the Bedrock-specific pieces of a
// key-check probe: the region-derived runtime endpoint (unless an explicit
// endpoint override wins outright — reqAPIBase, matching
// ProviderUpdateRequest.api_base's own "wins over the region-derived
// endpoint" contract) and each candidate model id rewritten with its
// cross-region inference profile group prefix.
//
// row MUST already be confirmed via bedrockRegionsRow — this function does
// not re-check it.
//
// reqRegion is the caller's own region setting for THIS call: the
// onboarding probe's request body region field, or (on a PUT that changes
// only the key) the row's persisted region. Falls through
// bedrock.ResolveRegion's row -> AWS_REGION -> catalog-default precedence
// exactly like a real turn's factory construction does.
//
// runtimeBaseOverride is bedrockRuntimeBaseOverride (rest.go) — "" in every
// production path, in which case the real bedrock.RegionalEndpoint(region)
// host is used.
//
// resolvedModels is candidateModels rewritten 1:1 (same order, same
// length). originalByResolved maps a rewritten id back to the caller's own
// candidate id — ADR-068 FR-036's probed_model must always echo the
// operator's OWN pick, never the AWS-facing rewritten one: the SPA never
// offered a group-prefixed id as a choice, and onboarding.tsx's canFinish
// gate compares probed_model to the raw selection verbatim.
func bedrockProbeRegionResolution(
	row catalog.Provider, reqRegion, reqAPIBase, runtimeBaseOverride string, candidateModels []string,
) (baseURL string, resolvedModels []string, originalByResolved map[string]string, err error) {
	region := bedrock.ResolveRegion(reqRegion, os.Getenv("AWS_REGION"), row.Region)

	baseURL = reqAPIBase
	if baseURL == "" {
		if verr := bedrock.ValidateRegion(region); verr != nil {
			return "", nil, nil, verr
		}
		if runtimeBaseOverride != "" {
			baseURL = strings.TrimRight(runtimeBaseOverride, "/") + "/" + region
		} else {
			baseURL = bedrock.RegionalEndpoint(region)
		}
	}

	group := providers_pkg.BedrockGroupForRegion(row.Regions, region)
	profilesByID := make(map[string][]string, len(row.Models))
	for _, m := range row.Models {
		profilesByID[m.ID] = m.InferenceProfiles
	}

	resolvedModels = make([]string, len(candidateModels))
	originalByResolved = make(map[string]string, len(candidateModels))
	for i, id := range candidateModels {
		resolved := id
		if group != "" {
			resolved = bedrock.ResolveModelID(id, group, profilesByID[id])
		}
		resolvedModels[i] = resolved
		originalByResolved[resolved] = id
	}
	return baseURL, resolvedModels, originalByResolved, nil
}
