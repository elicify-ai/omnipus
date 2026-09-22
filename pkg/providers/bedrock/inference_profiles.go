// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Orchestrator-approved scope addition (issue #800 follow-up, founder
// decision): which cross-region inference profile a source region may
// invoke is decided PER MODEL by AWS, not per region — the catalog's single
// `group` per region (region.go) is only an approximation of that. The
// precise answer is AWS's own ListInferenceProfiles control-plane call.
//
// IMPORTANT — honest limitation, stated up front because it could not be
// verified: it is NOT confirmed that a Bedrock API key (as opposed to full
// IAM SigV4 credentials) is authorized to call this control-plane endpoint
// at all. No live AWS account or key was used here — every claim below is
// this implementation's best-effort model of AWS's documented response
// shape, exercised only against a fake HTTP server (inference_profiles_test.go).
// A non-2xx response (403 in particular) MUST degrade silently to the
// catalog rules (ResolveModelIDLive) — see that function's own doc comment.

const inferenceProfileMaxResponseBody = 4 * 1024 * 1024

// ControlPlaneEndpoint derives the Bedrock CONTROL-plane host for a region —
// distinct from the bedrock-runtime host provider_bedrock.go uses for the
// Converse API. ListInferenceProfiles lives on the control plane only.
func ControlPlaneEndpoint(region string) string {
	return "https://bedrock." + strings.TrimSpace(region) + ".amazonaws.com"
}

// ProfileLookupError reports a non-2xx response from the control-plane
// ListInferenceProfiles call — most notably a 403, which is exactly the
// "API key may not be permitted for this call" case the caller must degrade
// on rather than treat as fatal.
type ProfileLookupError struct {
	StatusCode int
}

func (e *ProfileLookupError) Error() string {
	return fmt.Sprintf("bedrock control plane returned HTTP %d for ListInferenceProfiles", e.StatusCode)
}

// AsProfileLookupError is errors.As's exact shape, exported so callers (and
// tests) never need to import bedrock's error internals beyond this one
// classification helper.
func AsProfileLookupError(err error, target **ProfileLookupError) bool {
	return errors.As(err, target)
}

// inferenceProfilesResponse is this implementation's model of AWS's
// documented ListInferenceProfiles response shape — see the file header's
// honest-limitation note. Only the fields the base-model-id mapping needs
// are decoded; everything else on the real response is ignored.
type inferenceProfilesResponse struct {
	InferenceProfileSummaries []inferenceProfileSummary `json:"inferenceProfileSummaries"`
}

type inferenceProfileSummary struct {
	InferenceProfileID string                  `json:"inferenceProfileId"`
	Models             []inferenceProfileModel `json:"models"`
}

type inferenceProfileModel struct {
	ModelArn string `json:"modelArn"`
}

// ListInferenceProfiles calls AWS Bedrock's control-plane
// ListInferenceProfiles (GET /inference-profiles?type=SYSTEM_DEFINED) from
// the given region, authenticating with the operator's own API key as a
// Bearer token — the same credential Chat/Converse already uses, never a
// second secret. It returns a map of BASE model id (as it appears in the
// catalog, e.g. "anthropic.claude-sonnet-4-5-20250929-v1:0") to the
// region-specific inference profile id AWS reports as usable from this
// region for that model.
//
// baseEndpoint overrides ControlPlaneEndpoint(region) — the test seam
// (httptest.Server) production code leaves empty.
//
// A non-2xx response yields a *ProfileLookupError the caller can detect via
// AsProfileLookupError and degrade on; a transport-level failure (DNS,
// connection refused, timeout) yields a different, unclassified error so the
// two are never confused. Neither error string, nor any log this function
// emits, ever includes apiKey (SEC-16).
func ListInferenceProfiles(ctx context.Context, client *http.Client, apiKey, region, baseEndpoint string) (map[string]string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := strings.TrimRight(strings.TrimSpace(baseEndpoint), "/")
	if endpoint == "" {
		if err := ValidateRegion(region); err != nil {
			return nil, err
		}
		endpoint = ControlPlaneEndpoint(region)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		endpoint+"/inference-profiles?type=SYSTEM_DEFINED", nil)
	if err != nil {
		return nil, fmt.Errorf("bedrock list inference profiles request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", bedrockContentTypeJSON)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bedrock list inference profiles: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, inferenceProfileMaxResponseBody+1))
	if err != nil {
		return nil, fmt.Errorf("bedrock list inference profiles response: %w", err)
	}
	if len(body) > inferenceProfileMaxResponseBody {
		return nil, errors.New("bedrock list inference profiles response exceeds 4 MiB")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &ProfileLookupError{StatusCode: resp.StatusCode}
	}

	var parsed inferenceProfilesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("bedrock list inference profiles response: %w", err)
	}
	return indexProfilesByBaseModelID(parsed), nil
}

// indexProfilesByBaseModelID flattens the response into base-model-id ->
// profile-id: each summary's models[].modelArn ends with
// "/<baseModelId>" (a foundation-model ARN); the first profile a base model
// id is seen under wins on the rare case two profiles both claim it.
func indexProfilesByBaseModelID(resp inferenceProfilesResponse) map[string]string {
	out := make(map[string]string)
	for _, summary := range resp.InferenceProfileSummaries {
		if summary.InferenceProfileID == "" {
			continue
		}
		for _, m := range summary.Models {
			baseID := baseModelIDFromArn(m.ModelArn)
			if baseID == "" {
				continue
			}
			if _, seen := out[baseID]; !seen {
				out[baseID] = summary.InferenceProfileID
			}
		}
	}
	return out
}

// baseModelIDFromArn extracts the trailing "<publisher>.<model>" segment of
// a foundation-model ARN (everything after the last "/").
func baseModelIDFromArn(arn string) string {
	idx := strings.LastIndex(arn, "/")
	if idx == -1 || idx == len(arn)-1 {
		return ""
	}
	return arn[idx+1:]
}

// ResolveModelIDLive layers the live ListInferenceProfiles result on top of
// the catalog-only rules ResolveModelID implements (region.go): an arn or an
// already-prefixed id still passes through unchanged (rules 1/2 — checked
// BEFORE either profile source is consulted); otherwise, when liveProfiles
// carries an entry for id, that exact profile id wins — UNLESS it is itself
// `global.`-prefixed, in which case it is refused for the same data-
// residency reason ResolveModelID never invents one: a global route may
// leave the chosen geography. Any other case (no live entry, live lookup
// unavailable — liveProfiles nil, the caller's degrade-on-failure path)
// falls through to ResolveModelID's own catalog-data rules unchanged.
func ResolveModelIDLive(id, group string, catalogProfiles []string, liveProfiles map[string]string) string {
	if strings.HasPrefix(id, "arn:") {
		return id
	}
	for _, prefix := range knownGroupPrefixes {
		if strings.HasPrefix(id, prefix) {
			return id
		}
	}
	if live, ok := liveProfiles[id]; ok && live != "" && !strings.HasPrefix(live, "global.") {
		return live
	}
	return ResolveModelID(id, group, catalogProfiles)
}
