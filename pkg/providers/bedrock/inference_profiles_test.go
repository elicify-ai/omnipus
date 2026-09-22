package bedrock

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Orchestrator-approved scope addition (issue #800 follow-up): which
// cross-region inference profile a source region may invoke is decided PER
// MODEL by AWS, not per region — CONTRACT.md's single `group` per region is
// only an approximation. The exact answer is AWS's own ListInferenceProfiles
// control-plane call (GET /inference-profiles?type=SYSTEM_DEFINED on
// https://bedrock.<region>.amazonaws.com — the CONTROL plane, distinct from
// bedrock-runtime.<region>.amazonaws.com the Converse API uses), called with
// the operator's own API key as a Bearer token.
//
// IMPORTANT — honest limitation: it is NOT confirmed whether an AWS Bedrock
// API key (as opposed to full IAM SigV4 credentials) is authorized to call
// this control-plane endpoint at all. No live AWS account or key was used to
// verify this; the shape below is this implementation's best-effort model of
// the documented ListInferenceProfiles response, exercised only against a
// fake HTTP server. A 403 (or any non-2xx) must degrade to the catalog rules
// silently — see TestListInferenceProfiles_403FallsBack and
// TestResolveModelIDLive_DegradesToCatalogRulesWhenLiveLookupUnavailable.

func TestControlPlaneEndpoint_DerivedFromRegion(t *testing.T) {
	cases := map[string]string{
		"us-east-1":    "https://bedrock.us-east-1.amazonaws.com",
		"eu-central-1": "https://bedrock.eu-central-1.amazonaws.com",
	}
	for region, want := range cases {
		if got := ControlPlaneEndpoint(region); got != want {
			t.Errorf("ControlPlaneEndpoint(%q) = %q, want %q", region, got, want)
		}
	}
}

func TestListInferenceProfiles_SuccessParsesBaseModelIDToProfileID(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"inferenceProfileSummaries": [
				{
					"inferenceProfileId": "eu.anthropic.claude-sonnet-4-5-20250929-v1:0",
					"models": [
						{"modelArn": "arn:aws:bedrock:eu-central-1::foundation-model/anthropic.claude-sonnet-4-5-20250929-v1:0"},
						{"modelArn": "arn:aws:bedrock:eu-west-1::foundation-model/anthropic.claude-sonnet-4-5-20250929-v1:0"}
					],
					"status": "ACTIVE",
					"type": "SYSTEM_DEFINED"
				},
				{
					"inferenceProfileId": "eu.amazon.nova-pro-v1:0",
					"models": [
						{"modelArn": "arn:aws:bedrock:eu-central-1::foundation-model/amazon.nova-pro-v1:0"}
					],
					"status": "ACTIVE",
					"type": "SYSTEM_DEFINED"
				}
			]
		}`))
	}))
	defer srv.Close()

	got, err := ListInferenceProfiles(context.Background(), srv.Client(), "test-api-key", "eu-central-1", srv.URL)
	if err != nil {
		t.Fatalf("ListInferenceProfiles: %v", err)
	}
	if !strings.Contains(gotPath, "/inference-profiles") || !strings.Contains(gotPath, "type=SYSTEM_DEFINED") {
		t.Fatalf("request path = %q, want /inference-profiles?type=SYSTEM_DEFINED", gotPath)
	}
	if gotAuth != "Bearer test-api-key" {
		t.Fatalf("Authorization header = %q, want %q", gotAuth, "Bearer test-api-key")
	}
	want := map[string]string{
		"anthropic.claude-sonnet-4-5-20250929-v1:0": "eu.anthropic.claude-sonnet-4-5-20250929-v1:0",
		"amazon.nova-pro-v1:0":                      "eu.amazon.nova-pro-v1:0",
	}
	if len(got) != len(want) {
		t.Fatalf("ListInferenceProfiles = %#v, want %#v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("ListInferenceProfiles[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestListInferenceProfiles_403FallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"not authorized"}`))
	}))
	defer srv.Close()

	got, err := ListInferenceProfiles(context.Background(), srv.Client(), "test-api-key", "eu-central-1", srv.URL)
	if err == nil {
		t.Fatal("expected an error on 403, got nil")
	}
	if got != nil {
		t.Fatalf("got = %#v, want nil on error", got)
	}
	var lookupErr *ProfileLookupError
	if !AsProfileLookupError(err, &lookupErr) {
		t.Fatalf("error = %v, want *ProfileLookupError", err)
	}
	if lookupErr.StatusCode != http.StatusForbidden {
		t.Fatalf("StatusCode = %d, want %d", lookupErr.StatusCode, http.StatusForbidden)
	}
	// SEC-16 / never log the key: the error string must not carry it.
	if strings.Contains(err.Error(), "test-api-key") {
		t.Fatalf("error string leaks the API key: %q", err.Error())
	}
}

// TestListInferenceProfiles_RejectsMalformedRegionWithoutABaseEndpointOverride
// mirrors TestNewProvider_RejectsMalformedRegion's SSRF guard: when no test
// baseEndpoint override is supplied (the production path), a malformed
// region must never reach ControlPlaneEndpoint's naive string concatenation.
func TestListInferenceProfiles_RejectsMalformedRegionWithoutABaseEndpointOverride(t *testing.T) {
	_, err := ListInferenceProfiles(context.Background(), http.DefaultClient, "test-api-key", "us-east-1.evil.com/x", "")
	if err == nil {
		t.Fatal("expected a rejection for a malformed region, got nil")
	}
	// Must be an input-validation rejection, never merely "the network call
	// to the attacker host happened to fail" (e.g. DNS NXDOMAIN in this
	// sandbox) — the latter would still leak the request (and the Bearer
	// key) to a resolvable attacker host in a real deployment.
	if !strings.Contains(err.Error(), "invalid") && !strings.Contains(err.Error(), "region") {
		t.Fatalf("error = %q, want an explicit region-validation rejection, not a transport failure", err.Error())
	}
	var lookupErr *ProfileLookupError
	if AsProfileLookupError(err, &lookupErr) {
		t.Fatalf("a malformed region must be rejected before any request is made, got *ProfileLookupError %v", lookupErr)
	}
}

func TestListInferenceProfiles_NetworkErrorReturnsError(t *testing.T) {
	// An address nothing listens on — a transport-level failure, not a
	// non-2xx response.
	_, err := ListInferenceProfiles(context.Background(), http.DefaultClient, "test-api-key", "eu-central-1", "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected a transport error, got nil")
	}
	var lookupErr *ProfileLookupError
	if AsProfileLookupError(err, &lookupErr) {
		t.Fatalf("a transport error must not be classified as *ProfileLookupError, got %v", lookupErr)
	}
}

func TestResolveModelIDLive_UsesLiveProfileWhenPresent(t *testing.T) {
	live := map[string]string{"anthropic.claude-sonnet-4-5-20250929-v1:0": "eu.anthropic.claude-sonnet-4-5-20250929-v1:0"}
	got := ResolveModelIDLive("anthropic.claude-sonnet-4-5-20250929-v1:0", "us", nil, live)
	if want := "eu.anthropic.claude-sonnet-4-5-20250929-v1:0"; got != want {
		t.Fatalf("ResolveModelIDLive = %q, want the live profile id %q", got, want)
	}
}

func TestResolveModelIDLive_DegradesToCatalogRulesWhenLiveLookupUnavailable(t *testing.T) {
	// live is nil — the lookup failed (403/network/etc) and degraded
	// silently; resolution falls back to CONTRACT.md's catalog rules.
	got := ResolveModelIDLive("anthropic.claude-sonnet-4-5-20250929-v1:0", "us", []string{"us"}, nil)
	if want := "us.anthropic.claude-sonnet-4-5-20250929-v1:0"; got != want {
		t.Fatalf("ResolveModelIDLive = %q, want the catalog-rule result %q", got, want)
	}
}

func TestResolveModelIDLive_NeverAutoSelectsGlobalEvenFromLiveResult(t *testing.T) {
	// AWS itself would never plausibly return this, but the guard must hold
	// regardless of what the live lookup reports.
	live := map[string]string{"anthropic.claude-sonnet-4-5-20250929-v1:0": "global.anthropic.claude-sonnet-4-5-20250929-v1:0"}
	got := ResolveModelIDLive("anthropic.claude-sonnet-4-5-20250929-v1:0", "us", []string{"us"}, live)
	if want := "us.anthropic.claude-sonnet-4-5-20250929-v1:0"; got != want {
		t.Fatalf("ResolveModelIDLive = %q, must not use a global. live profile — want the catalog-rule result %q", got, want)
	}
}

func TestResolveModelIDLive_ArnAndPrefixedPassThroughBeforeConsultingLiveProfiles(t *testing.T) {
	live := map[string]string{"anthropic.claude-sonnet-4-5-20250929-v1:0": "eu.anthropic.claude-sonnet-4-5-20250929-v1:0"}
	arn := "arn:aws:bedrock:us-east-1:111122223333:inference-profile/my-profile"
	if got := ResolveModelIDLive(arn, "us", nil, live); got != arn {
		t.Fatalf("ResolveModelIDLive(arn) = %q, want unchanged", got)
	}
	prefixed := "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
	if got := ResolveModelIDLive(prefixed, "us", nil, live); got != prefixed {
		t.Fatalf("ResolveModelIDLive(prefixed) = %q, want unchanged", got)
	}
}
