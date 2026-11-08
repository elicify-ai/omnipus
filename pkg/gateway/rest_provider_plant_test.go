// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// rest_provider_plant_test.go — the hosted "provider plant" hole and the SSRF
// gaps around it.
//
// The hole: on a hosted (platform-mode) tenant the FR-050 pre-auth window
// stayed open until the owner's first sign-in, so any caller could
// PUT /api/v1/providers/<catalog id> with their own api_base. The owner's
// later key save then went to that base, and the engine could be steered to
// any reachable address, loopback included. Even with sandbox.ssrf on, a
// write with no key skipped the address check and GET /providers listed a
// "local" row with no checker at all.
//
// Every test drives the PRODUCTION route table (serveViaRealMux) and counts
// requests on a loopback canary: the canary's hit count is the assertion that
// matters — zero means the engine never reached the planted address.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	providercommon "github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/security"
)

// plantCanary is a loopback listener that answers like an OpenAI-compatible
// endpoint and counts every request it receives.
type plantCanary struct {
	*httptest.Server
	hits atomic.Int64
}

func startPlantCanary(t *testing.T) *plantCanary {
	t.Helper()
	c := &plantCanary{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			_, _ = w.Write([]byte(`{"data":[{"id":"canary-model"}]}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
		}
	}))
	t.Cleanup(c.Close)
	return c
}

// plantTestCatalog serves two catalog rows: `openrouter` (a cloud row on a
// public base) and `plantco`, whose own catalog base is on the reserved
// `.invalid` TLD (RFC 2606), so a key check against the CATALOG base can never
// reach anything and the onboarding probe only reports it unverified.
func plantTestCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	doc := `{
		"schema_version": "2.0.0",
		"version": "v2026.11.8",
		"updated_at": "2026-11-08T06:00:00Z",
		"source": "models.dev@0123456789abcdef0123456789abcdef01234567",
		"default_resize_limits": { "long_edge_px": 7680, "max_bytes": 10485760 },
		"providers": [
			{
				"id": "openrouter", "name": "OpenRouter", "company": "OpenRouter",
				"api": "https://openrouter.ai/api/v1", "protocol": "openai-compatible",
				"env": ["OPENROUTER_API_KEY"], "tier": "standard",
				"auth_methods": ["api_key"],
				"models": [
					{"id": "rec-newest", "name": "Rec Newest", "tool_call": true,
					 "context_window": 200000, "max_output_tokens": 8192,
					 "release_date": "2026-08-01",
					 "input_modalities": ["text"], "status": "active"}
				]
			},
			{
				"id": "plantco", "name": "PlantCo", "company": "PlantCo",
				"api": "https://plantco.invalid/v1", "protocol": "openai-compatible",
				"env": ["PLANTCO_API_KEY"], "tier": "standard",
				"auth_methods": ["api_key"],
				"models": [
					{"id": "plant-model", "name": "Plant Model", "tool_call": true,
					 "context_window": 200000, "max_output_tokens": 8192,
					 "release_date": "2026-08-01",
					 "input_modalities": ["text"], "status": "active"}
				]
			}
		]
	}`
	c, err := catalog.NewCatalog([]byte(doc))
	require.NoError(t, err)
	return c
}

// newPlantEnv builds an onboarding-incomplete instance in edition e serving
// plantTestCatalog. signedIn seeds the platform account with a live session
// (the shape a platform sign-in leaves); otherwise no account exists at all —
// a genuine fresh install.
func newPlantEnv(t *testing.T, e string, signedIn bool) *onboardAuthEnv {
	t.Helper()
	withEdition(t, e)
	var env *onboardAuthEnv
	if signedIn {
		env = newSignedInOnboardEnv(t)
	} else {
		env = newOnboardAuthEnv(t)
	}
	cat := plantTestCatalog(t)
	env.api.providerCatalog = cat
	installProbeCatalog(t, cat)
	// The shared onboarding fixture names loopback in the endpoint allow list
	// (allowLoopbackProviderEndpoints); these tests need the strict default an
	// instance with no operator allow list runs with.
	providercommon.SetEndpointAllowlist(nil)
	return env
}

// seedProviderRow writes row into config.json's providers[] and the live
// in-memory config, as a planted (or earlier) save would have left it.
func seedProviderRow(t *testing.T, env *onboardAuthEnv, row map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(env.home + "/config.json")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	list, _ := m["providers"].([]any)
	m["providers"] = append(list, row)
	out, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(env.home+"/config.json", out, 0o600))

	mc := &config.ModelConfig{}
	b, err := json.Marshal(row)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, mc))
	live := env.api.agentLoop.GetConfig()
	live.Providers = append(live.Providers, mc)
}

// diskProviderRows reads providers[] straight off config.json.
func diskProviderRows(t *testing.T, env *onboardAuthEnv) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(env.home + "/config.json")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	list, _ := m["providers"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		if row, ok := e.(map[string]any); ok {
			out = append(out, row)
		}
	}
	return out
}

func plantRequest(method, path, body string, signedIn bool) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if signedIn {
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: platformSessionToken})
	}
	return req
}

// ── Requirement 1: no pre-auth window in platform mode ──────────────────────

// TestProviderPlant_PlatformMode_AnonymousProviderRoutesRefusedOnFreshInstall:
// a hosted or desktop instance with NO account yet (the state before the
// owner's first sign-in) must refuse an anonymous caller on every route the
// FR-050 window used to open — onboarding runs after sign-in in platform mode,
// so the window has nothing to serve there.
func TestProviderPlant_PlatformMode_AnonymousProviderRoutesRefusedOnFreshInstall(t *testing.T) {
	for _, edition := range []string{config.EditionHosted, config.EditionDesktop} {
		t.Run(edition, func(t *testing.T) {
			env := newPlantEnv(t, edition, false)
			canary := startPlantCanary(t)
			putBody := fmt.Sprintf(`{"api_key":"sk-stranger","model":"rec-newest","api_base":%q}`, canary.URL+"/v1")

			routes := []struct {
				name, method, path, body string
			}{
				{"PUT provider", http.MethodPut, "/api/v1/providers/openrouter", putBody},
				{"POST provider test", http.MethodPost, "/api/v1/providers/openrouter/test", ""},
				{"GET providers", http.MethodGet, "/api/v1/providers", ""},
				{"GET catalog", http.MethodGet, "/api/v1/providers/catalog", ""},
			}
			for i, rt := range routes {
				ip := fmt.Sprintf("198.51.100.%d", 140+i+len(edition))
				w := serveViaRealMux(t, env.api, env.cfg, plantRequest(rt.method, rt.path, rt.body, false), ip)
				assert.Equal(t, http.StatusUnauthorized, w.Code,
					"%s: an anonymous caller on a platform-mode fresh install must get 401; body=%s",
					rt.name, w.Body.String())
			}
			assert.Equal(t, int64(0), canary.hits.Load(), "nothing may reach the planted address")
			assert.Empty(t, diskProviderRows(t, env), "no provider row may be written by an anonymous caller")
		})
	}
}

// ── Requirement 2: api_base on a catalog id is refused on hosted ────────────

// TestProviderPlant_Hosted_CatalogAPIBaseRefused: even a signed-in owner may
// not point a catalog id at another base on a hosted tenant: 400 naming
// api_base, nothing written, nothing called.
func TestProviderPlant_Hosted_CatalogAPIBaseRefused(t *testing.T) {
	env := newPlantEnv(t, config.EditionHosted, true)
	canary := startPlantCanary(t)
	body := fmt.Sprintf(`{"api_key":"sk-owner","model":"rec-newest","api_base":%q}`, canary.URL+"/v1")

	w := serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPut, "/api/v1/providers/openrouter", body, true), "198.51.100.150")

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "api_base", resp["field"], "the refusal must name the api_base field")
	assert.Equal(t, providerCatalogEndpointRefusedMsg, resp["error"])
	assert.Equal(t, int64(0), canary.hits.Load())
	assert.Empty(t, diskProviderRows(t, env))
}

// TestProviderPlant_Local_CatalogAPIBaseOverrideKept is the local-mode
// control: the documented override (ProviderUpdateRequest.api_base on a
// catalog id) keeps working outside the hosted edition.
func TestProviderPlant_Local_CatalogAPIBaseOverrideKept(t *testing.T) {
	env := newPlantEnv(t, config.EditionCore, false)
	canary := startPlantCanary(t)
	body := fmt.Sprintf(`{"api_key":"sk-local","model":"rec-newest","api_base":%q}`, canary.URL+"/v1")

	w := serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPut, "/api/v1/providers/openrouter", body, false), "198.51.100.151")

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	rows := diskProviderRows(t, env)
	require.Len(t, rows, 1)
	assert.Equal(t, canary.URL+"/v1", rows[0]["api_base"])
	assert.Positive(t, canary.hits.Load(), "local mode probes the override as before")
}

// ── Requirement 3: the address check runs on every api_base write ──────────

// TestProviderPlant_SSRFOn_KeylessCustomRowWriteChecked: with sandbox.ssrf on
// (local mode), repointing an existing custom row at loopback WITHOUT a key
// used to skip the check (it only ran when the key changed). It must be
// refused 422 and leave the stored row alone.
func TestProviderPlant_SSRFOn_KeylessCustomRowWriteChecked(t *testing.T) {
	env := newPlantEnv(t, config.EditionCore, false)
	env.api.ssrfChecker = security.NewSSRFChecker(nil)
	canary := startPlantCanary(t)
	seedProviderRow(t, env, map[string]any{
		"provider": "mybox", "model": "m1", "custom": true,
		"protocol": "openai-compatible", "api_base": "https://mybox.example.com/v1",
	})
	body := fmt.Sprintf(`{"api_base":%q,"protocol":"openai-compatible"}`, canary.URL+"/v1")

	w := serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPut, "/api/v1/providers/mybox", body, false), "198.51.100.152")

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body=%s", w.Body.String())
	rows := diskProviderRows(t, env)
	require.Len(t, rows, 1)
	assert.Equal(t, "https://mybox.example.com/v1", rows[0]["api_base"], "the stored base must be unchanged")
	assert.Equal(t, int64(0), canary.hits.Load())
}

// TestProviderPlant_Hosted_GuardOnWithSSRFSettingOff: on hosted the address
// check is on even though sandbox.ssrf is off (restAPI.ssrfChecker nil): a
// signed-in owner's custom row at loopback is refused before any probe.
func TestProviderPlant_Hosted_GuardOnWithSSRFSettingOff(t *testing.T) {
	env := newPlantEnv(t, config.EditionHosted, true)
	require.Nil(t, env.api.ssrfChecker, "fixture: sandbox.ssrf is off")
	canary := startPlantCanary(t)

	for i, base := range []string{canary.URL + "/v1", strings.Replace(canary.URL, "127.0.0.1", "localhost", 1) + "/v1"} {
		body := fmt.Sprintf(`{"api_key":"sk-owner","model":"m1","protocol":"openai-compatible","api_base":%q}`, base)
		w := serveViaRealMux(t, env.api, env.cfg,
			plantRequest(http.MethodPut, "/api/v1/providers/mybox", body, true), fmt.Sprintf("198.51.100.%d", 153+i))
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code, "base %s: body=%s", base, w.Body.String())
	}
	assert.Equal(t, int64(0), canary.hits.Load())
	assert.Empty(t, diskProviderRows(t, env))
}

// TestProviderPlant_ListDoesNotCallPlantedPrivateEndpoint: a custom row on a
// private host counts as "local", and GET /providers lists its models live.
// That listing used a no-op checker; with the guard on (sandbox.ssrf in local
// mode, always on hosted) it must not reach the planted address.
func TestProviderPlant_ListDoesNotCallPlantedPrivateEndpoint(t *testing.T) {
	cases := []struct {
		name     string
		edition  string
		signedIn bool
		ssrfOn   bool
	}{
		{"local with sandbox.ssrf on", config.EditionCore, false, true},
		{"hosted with sandbox.ssrf off", config.EditionHosted, true, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newPlantEnv(t, tc.edition, tc.signedIn)
			if tc.ssrfOn {
				env.api.ssrfChecker = security.NewSSRFChecker(nil)
			}
			canary := startPlantCanary(t)
			seedProviderRow(t, env, map[string]any{
				"provider": "planted", "model": "m1", "custom": true,
				"protocol": "openai-compatible", "api_base": canary.URL + "/v1",
			})

			w := serveViaRealMux(t, env.api, env.cfg,
				plantRequest(http.MethodGet, "/api/v1/providers", "", tc.signedIn), fmt.Sprintf("198.51.100.%d", 160+i))

			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
			assert.Equal(t, int64(0), canary.hits.Load(), "the listing must not call the planted private address")
		})
	}
}

// TestProviderPlant_ListStillCallsAllowedLocalEndpoint is the control for the
// test above: with no guard (local mode, sandbox.ssrf off) a local endpoint's
// live model list is still fetched (FR-020).
func TestProviderPlant_ListStillCallsAllowedLocalEndpoint(t *testing.T) {
	env := newPlantEnv(t, config.EditionCore, false)
	canary := startPlantCanary(t)
	seedProviderRow(t, env, map[string]any{
		"provider": "localbox", "model": "m1", "custom": true,
		"protocol": "openai-compatible", "api_base": canary.URL + "/v1",
	})

	w := serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodGet, "/api/v1/providers", "", false), "198.51.100.165")

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Positive(t, canary.hits.Load())
}

// ── Requirement 4: onboarding does not keep or accept a planted base ───────

func postPlantComplete(t *testing.T, env *onboardAuthEnv, provider, ip string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"provider":` + provider + `,"preferences":{"name":"Daniel","tone":"direct","detail":"brief"}}`
	return serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPost, "/api/v1/onboarding/complete", body, true), ip)
}

// TestProviderPlant_Hosted_OnboardingDropsPlantedBaseOnMatchingRow: a planted
// row for the same (provider, model) pair used to keep its api_base when the
// owner completed onboarding without an endpoint, so every later call went to
// the planter. On hosted the base must not survive.
func TestProviderPlant_Hosted_OnboardingDropsPlantedBaseOnMatchingRow(t *testing.T) {
	env := newPlantEnv(t, config.EditionHosted, true)
	seedProviderRow(t, env, map[string]any{
		"provider": "plantco", "model": "plant-model", "api_base": "https://planter.example.net/v1",
	})

	w := postPlantComplete(t, env,
		`{"auth_method":"api_key","id":"plantco","api_key":"sk-owner","model":"plant-model"}`, "198.51.100.170")

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var found bool
	for _, row := range diskProviderRows(t, env) {
		if row["provider"] == "plantco" && row["model"] == "plant-model" {
			found = true
			assert.NotContains(t, row, "api_base", "the planted base must be gone after the owner's onboarding")
		}
	}
	require.True(t, found, "onboarding must have written the plantco row")
}

// TestProviderPlant_Hosted_OnboardingRefusesEndpointOnCatalogID: the
// onboarding write path refuses an endpoint override on a catalog id on
// hosted, like PUT — and the probe-provider check does too.
func TestProviderPlant_Hosted_OnboardingRefusesEndpointOnCatalogID(t *testing.T) {
	env := newPlantEnv(t, config.EditionHosted, true)
	canary := startPlantCanary(t)

	// The probe first: a successful complete would close onboarding and the
	// probe would then answer 409 for an unrelated reason.
	probe := fmt.Sprintf(`{"id":"plantco","auth":"api_key","api_key":"sk-owner","model":"plant-model","api_base":%q}`,
		canary.URL+"/v1")
	w := serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPost, "/api/v1/onboarding/probe-provider", probe, true), "198.51.100.172")
	assert.Equal(t, http.StatusBadRequest, w.Code, "probe: body=%s", w.Body.String())
	assert.JSONEq(t, fmt.Sprintf(`{"error":%q,"field":"api_base"}`, onboardingCatalogEndpointRefusedMsg), w.Body.String())

	w = postPlantComplete(t, env, fmt.Sprintf(
		`{"auth_method":"api_key","id":"plantco","api_key":"sk-owner","model":"plant-model","endpoint":%q}`,
		canary.URL+"/v1"), "198.51.100.171")
	assert.Equal(t, http.StatusBadRequest, w.Code, "complete: body=%s", w.Body.String())
	assert.JSONEq(t, fmt.Sprintf(`{"error":%q,"field":"endpoint"}`, onboardingCatalogEndpointRefusedMsg), w.Body.String())

	assert.Equal(t, int64(0), canary.hits.Load())
	assert.Empty(t, diskProviderRows(t, env))
}

// ── Requirement 5: a DNS failure is reported as one ────────────────────────

type failingResolver struct{}

func (failingResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

// TestProviderPlant_DNSFailureIsNotReportedAsSSRFBlock: a host that does not
// resolve used to come back as "provider endpoint not allowed (SSRF guard)",
// sending the operator after an allow-list problem that does not exist.
func TestProviderPlant_DNSFailureIsNotReportedAsSSRFBlock(t *testing.T) {
	env := newPlantEnv(t, config.EditionCore, false)
	checker := security.NewSSRFChecker(nil)
	checker.SetResolver(failingResolver{})
	env.api.ssrfChecker = checker
	body := `{"api_key":"sk-x","model":"m1","protocol":"openai-compatible","api_base":"https://no-such-host.example.invalid/v1"}`

	w := serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPut, "/api/v1/providers/mybox", body, false), "198.51.100.175")

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	msg, _ := resp["error"].(string)
	assert.Contains(t, msg, "host name did not resolve")
	assert.NotContains(t, msg, "not allowed")
	assert.NotContains(t, msg, "SSRF")
	assert.NotContains(t, msg, "internal network")
}

// ── Planted catalog base: never contacted, cleared by the owner's save ─────

// recordingResolver answers every lookup with ip (or a DNS failure when ip
// is nil) and records the host names asked for, so a test can prove which
// endpoint a check was about without any connection being made.
type recordingResolver struct {
	ip    net.IP
	hosts []string
}

func (r *recordingResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.hosts = append(r.hosts, host)
	if r.ip == nil {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return []net.IPAddr{{IP: r.ip}}, nil
}

const plantedHost = "planter.example.net"

func seedPlantedCatalogRow(t *testing.T, env *onboardAuthEnv) {
	t.Helper()
	t.Setenv("PLANTCO_TEST_KEY", "sk-owner-stored")
	seedProviderRow(t, env, map[string]any{
		"provider": "plantco", "model": "plant-model", "api_key_ref": "PLANTCO_TEST_KEY",
		"api_base": "https://" + plantedHost + "/v1",
	})
}

// TestProviderPlant_Hosted_PlantedCatalogBaseNeverChecked: with a planted
// api_base on a catalog row, the owner's key test and key save must aim at
// the catalog's own endpoint, never at the planted host. The resolver
// records every host a check was about (and fails it, so nothing dials).
func TestProviderPlant_Hosted_PlantedCatalogBaseNeverChecked(t *testing.T) {
	env := newPlantEnv(t, config.EditionHosted, true)
	seedPlantedCatalogRow(t, env)
	res := &recordingResolver{}
	checker := security.NewSSRFChecker(nil)
	checker.SetResolver(res)
	env.api.ssrfChecker = checker

	w := serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPost, "/api/v1/providers/plantco/test", "", true), "198.51.100.180")
	require.Equal(t, http.StatusOK, w.Code, "test: body=%s", w.Body.String())

	w = serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPut, "/api/v1/providers/plantco", `{"api_key":"sk-owner-new"}`, true), "198.51.100.181")
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "put: body=%s", w.Body.String())

	assert.Contains(t, res.hosts, "plantco.invalid", "the checks must be about the catalog endpoint")
	assert.NotContains(t, res.hosts, plantedHost, "no check may be about the planted host")
}

// TestProviderPlant_Hosted_OwnerSaveClearsPlantedCatalogBase: the owner's
// next save of the row removes the planted base from config.json.
func TestProviderPlant_Hosted_OwnerSaveClearsPlantedCatalogBase(t *testing.T) {
	env := newPlantEnv(t, config.EditionHosted, true)
	seedPlantedCatalogRow(t, env)
	checker := security.NewSSRFChecker(nil)
	checker.SetResolver(&recordingResolver{ip: net.ParseIP("93.184.216.34")})
	env.api.ssrfChecker = checker

	w := serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPut, "/api/v1/providers/plantco", `{"model":"plant-model"}`, true), "198.51.100.182")

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	rows := diskProviderRows(t, env)
	require.Len(t, rows, 1)
	assert.NotContains(t, rows[0], "api_base", "the owner's save must clear the planted base")
}

// TestProviderPlant_Hosted_AllowlistedEndpointAccepted: the operator's SSRF
// allow list is the one way to point a hosted catalog id at a private base
// (CI's end-to-end mock provider does exactly this). With the host on the
// list, onboarding and PUT accept the override and the key check reaches it.
func TestProviderPlant_Hosted_AllowlistedEndpointAccepted(t *testing.T) {
	env := newPlantEnv(t, config.EditionHosted, true)
	providercommon.SetEndpointAllowlist([]string{"127.0.0.1"})
	t.Cleanup(func() { providercommon.SetEndpointAllowlist(nil) })
	canary := startPlantCanary(t)

	w := postPlantComplete(t, env, fmt.Sprintf(
		`{"auth_method":"api_key","id":"plantco","api_key":"sk-owner","model":"plant-model","endpoint":%q}`,
		canary.URL+"/v1"), "198.51.100.183")
	require.Equal(t, http.StatusOK, w.Code, "complete: body=%s", w.Body.String())
	afterComplete := canary.hits.Load()
	assert.Positive(t, afterComplete, "the onboarding key check reaches the allow-listed endpoint")

	body := fmt.Sprintf(`{"api_key":"sk-owner","model":"rec-newest","api_base":%q}`, canary.URL+"/v1")
	w = serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPut, "/api/v1/providers/openrouter", body, true), "198.51.100.184")
	require.Equal(t, http.StatusOK, w.Code, "put: body=%s", w.Body.String())
	assert.Greater(t, canary.hits.Load(), afterComplete, "the PUT key check reaches it too")
}

// TestCreateStartupProvider_InstallsEndpointAllowlist: boot and every reload
// install sandbox.ssrf.allow_internal + sandbox.egress_allow_cidrs as the
// provider endpoint guard's allow list.
func TestCreateStartupProvider_InstallsEndpointAllowlist(t *testing.T) {
	withEdition(t, config.EditionHosted)
	t.Cleanup(func() { providercommon.SetEndpointAllowlist(nil) })
	cfg := &config.Config{}
	cfg.Sandbox.SSRF.AllowInternal = []string{"mock.internal"}
	cfg.Sandbox.EgressAllowCIDRs = []string{"10.9.0.0/16"}

	_, _, err := createStartupProvider(cfg, true)
	require.NoError(t, err)

	assert.True(t, providercommon.EndpointExplicitlyAllowed("http://mock.internal:9000/v1"))
	assert.True(t, providercommon.EndpointExplicitlyAllowed("http://10.9.3.4/v1"))
	assert.False(t, providercommon.EndpointExplicitlyAllowed("http://10.8.3.4/v1"))
}

// TestProviderPlant_EntitlementLocalRow: POST /providers/{id}/entitlement
// lists a row's models live. A local row (here the ollama protocol on a
// loopback base) was listed with a no-op checker. On hosted the endpoint
// guard has no local exception, so the row is refused (422) with 0 calls; in
// core (no guard, sandbox.ssrf off) the same row is still listed, the control
// that shows the canary would see the call.
func TestProviderPlant_EntitlementLocalRow(t *testing.T) {
	cases := []struct {
		edition  string
		wantHits bool
	}{
		{config.EditionHosted, false},
		{config.EditionCore, true},
	}
	for i, tc := range cases {
		t.Run(tc.edition, func(t *testing.T) {
			env := newPlantEnv(t, tc.edition, true)
			canary := startPlantCanary(t)
			seedProviderRow(t, env, map[string]any{
				"provider": "localbox", "model": "m1",
				"protocol": "ollama", "api_base": canary.URL,
			})

			w := serveViaRealMux(t, env.api, env.cfg,
				plantRequest(http.MethodPost, "/api/v1/providers/localbox/entitlement", "", true),
				fmt.Sprintf("198.51.100.%d", 190+i))

			if tc.wantHits {
				assert.Positive(t, canary.hits.Load(), "control: the local row is listed live (body=%s)", w.Body.String())
				return
			}
			require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body=%s", w.Body.String())
			assert.Equal(t, int64(0), canary.hits.Load(), "hosted: the local row must not be called")
			assert.JSONEq(t, fmt.Sprintf(`{"error":%q}`, "The provider address points at an internal network, which is not reachable from here. Use the provider's public address."), w.Body.String())
		})
	}
}

// TestProviderPlant_Hosted_PlantedCatalogBaseNotProbed: the owner's key test
// and key save must send the stored key to the catalog's own endpoint, never
// to a planted api_base. Unlike the test above, the stored key resolves and
// the checks pass, so both requests reach their key probe. Every host
// resolves to loopback port 443, which nothing serves, so a probe fails to
// connect and nothing leaves the machine; the resolver records which host
// each check and dial was about.
func TestProviderPlant_Hosted_PlantedCatalogBaseNotProbed(t *testing.T) {
	env := newPlantEnv(t, config.EditionHosted, true)
	seedPlantedCatalogRow(t, env)
	require.NoError(t, env.api.credStore.Set("PLANTCO_TEST_KEY", "sk-owner-stored"))
	res := &recordingResolver{ip: net.ParseIP("127.0.0.1")}
	checker := security.NewSSRFChecker([]string{"127.0.0.1"})
	checker.SetResolver(res)
	env.api.ssrfChecker = checker

	w := serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPost, "/api/v1/providers/plantco/test", "", true), "198.51.100.186")
	require.Equal(t, http.StatusOK, w.Code, "test: body=%s", w.Body.String())
	assert.Contains(t, res.hosts, "plantco.invalid", "the key test must probe the catalog endpoint (body=%s)", w.Body.String())
	assert.NotContains(t, res.hosts, plantedHost, "the key test must never probe the planted host")

	res.hosts = nil
	w = serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPut, "/api/v1/providers/plantco", `{"api_key":"sk-owner-new"}`, true), "198.51.100.187")
	assert.Contains(t, res.hosts, "plantco.invalid", "the key save must probe the catalog endpoint (body=%s)", w.Body.String())
	assert.NotContains(t, res.hosts, plantedHost, "the key save must never probe the planted host")
}

// TestProviderPlant_Hosted_OnboardingKeyProbeUsesGuard: on hosted with
// sandbox.ssrf off, the onboarding key check still asks the endpoint guard
// first. plantco's catalog host does not resolve, so the guard refuses it and
// the check is skipped with its "not reachable for an automatic check" note;
// without the guard the probe would run and fail as an ordinary unreachable
// endpoint instead.
func TestProviderPlant_Hosted_OnboardingKeyProbeUsesGuard(t *testing.T) {
	env := newPlantEnv(t, config.EditionHosted, true)
	require.Nil(t, env.api.ssrfChecker, "precondition: sandbox.ssrf is off")

	w := postPlantComplete(t, env,
		`{"auth_method":"api_key","id":"plantco","api_key":"sk-owner","model":"plant-model"}`, "198.51.100.188")

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "reachable for an automatic check",
		"the guard must decide before the key probe runs")
}

// plantedBootConfig is an instance whose rows were planted before the fix:
// the default model's row and one other catalog row both carry an api_base
// at a public host the operator never allowed.
func plantedBootConfig(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv("PLANT_BOOT_KEY", "sk-owner")
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "openrouter", Model: "x"}},
		},
		Providers: []*config.ModelConfig{
			{Name: "or", Provider: "openrouter", Model: "x", APIBase: "https://attacker.example/v1", APIKeyRef: "PLANT_BOOT_KEY"},
			{Name: "ds", Provider: "deepseek", Model: "deepseek-chat", APIBase: "https://attacker2.example/v1", APIKeyRef: "PLANT_BOOT_KEY"},
		},
	}
}

// TestCreateStartupProvider_Hosted_PlantedDefaultRowStartsLimited: on hosted
// the run-time builder refuses a planted catalog base, and when that row backs
// the default model the gateway must still boot (a boot error would
// crash-loop the instance) in limited mode, answering every turn with the
// refusal the person can act on. Each planted row is logged by provider id.
func TestCreateStartupProvider_Hosted_PlantedDefaultRowStartsLimited(t *testing.T) {
	withEdition(t, config.EditionHosted)
	logs := captureSlog(t)

	p, _, err := createStartupProvider(plantedBootConfig(t), false)

	require.NoError(t, err, "a planted row must not stop the gateway booting")
	require.IsType(t, &startupBlockedProvider{}, p)
	_, chatErr := p.Chat(context.Background(), nil, nil, "", nil)
	require.Error(t, chatErr)
	assert.Equal(t, (&providers.PlantedEndpointError{ProviderID: "openrouter"}).Error(), chatErr.Error())
	assert.Contains(t, logs.String(), "provider=openrouter")
	assert.Contains(t, logs.String(), "provider=deepseek")
	assert.NotContains(t, logs.String(), "attacker", "the log names the provider, never the planted address")
}

// TestCreateStartupProvider_Hosted_PlantedRowServingDefaultByListStartsLimited:
// the default pair need not equal a row's own model. The run-time resolver
// also serves it from a row whose model list carries it, or (for a catalog
// provider) from any row once the catalog lists the model, and keeps that
// row's saved address. A planted row reached that way must start limited
// mode too, not fail the boot. A planted row the default does not resolve to
// leaves a clean sibling in charge.
func TestCreateStartupProvider_Hosted_PlantedRowServingDefaultByListStartsLimited(t *testing.T) {
	withEdition(t, config.EditionHosted)
	t.Setenv("PLANT_BOOT_KEY", "sk-owner")
	const planted = "https://attacker.example/v1"
	boot := func(model string, rows ...*config.ModelConfig) *config.Config {
		return &config.Config{
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "openrouter", Model: model}},
			},
			Providers: rows,
		}
	}
	refusal := (&providers.PlantedEndpointError{ProviderID: "openrouter"}).Error()

	t.Run("model list carries the default", func(t *testing.T) {
		cfg := boot("x", &config.ModelConfig{
			Name: "decoy", Provider: "openrouter", Model: "decoy", Models: []string{"x"},
			APIBase: planted, APIKeyRef: "PLANT_BOOT_KEY",
		})
		p, _, err := createStartupProvider(cfg, false)
		require.NoError(t, err, "a planted row must not stop the gateway booting")
		require.IsType(t, &startupBlockedProvider{}, p)
		_, chatErr := p.Chat(context.Background(), nil, nil, "", nil)
		require.Error(t, chatErr)
		assert.Equal(t, refusal, chatErr.Error())
	})

	t.Run("catalog lists the default", func(t *testing.T) {
		require.True(t, providers.ProviderCatalog().Resolve("openrouter", "z-ai/glm-5.2").Found(),
			"fixture: the served catalog must list this openrouter model")
		cfg := boot("z-ai/glm-5.2", &config.ModelConfig{
			Name: "decoy", Provider: "openrouter", Model: "decoy",
			APIBase: planted, APIKeyRef: "PLANT_BOOT_KEY",
		})
		p, _, err := createStartupProvider(cfg, false)
		require.NoError(t, err, "a planted row must not stop the gateway booting")
		require.IsType(t, &startupBlockedProvider{}, p)
		_, chatErr := p.Chat(context.Background(), nil, nil, "", nil)
		require.Error(t, chatErr)
		assert.Equal(t, refusal, chatErr.Error())
	})

	t.Run("round-robin sibling of a clean exact row", func(t *testing.T) {
		// The resolver round-robins over rows carrying the exact pair, so
		// the planted second row can be the one built: limited mode.
		cfg := boot("x",
			&config.ModelConfig{Name: "clean", Provider: "openrouter", Model: "x", APIKeyRef: "PLANT_BOOT_KEY"},
			&config.ModelConfig{Name: "decoy", Provider: "openrouter", Model: "x", APIBase: planted, APIKeyRef: "PLANT_BOOT_KEY"},
		)
		p, _, err := createStartupProvider(cfg, false)
		require.NoError(t, err)
		require.IsType(t, &startupBlockedProvider{}, p)
	})

	t.Run("clean row serves the default", func(t *testing.T) {
		cfg := boot("x",
			&config.ModelConfig{
				Name: "decoy", Provider: "openrouter", Model: "decoy", Models: []string{"y"},
				APIBase: planted, APIKeyRef: "PLANT_BOOT_KEY",
			},
			&config.ModelConfig{
				Name: "clean", Provider: "openrouter", Model: "clean", Models: []string{"x"},
				APIKeyRef: "PLANT_BOOT_KEY",
			},
		)
		p, _, err := createStartupProvider(cfg, false)
		require.NoError(t, err)
		_, blocked := p.(*startupBlockedProvider)
		assert.False(t, blocked, "the default resolves to the clean row, so the real provider is built")
	})
}

// TestCreateStartupProvider_Core_PlantedShapeIsAnOverride is the control: in
// core the same rows are a supported api_base override, so the real provider
// is built and nothing is logged as planted.
func TestCreateStartupProvider_Core_PlantedShapeIsAnOverride(t *testing.T) {
	withEdition(t, config.EditionCore)
	logs := captureSlog(t)

	p, _, err := createStartupProvider(plantedBootConfig(t), false)

	require.NoError(t, err)
	_, blocked := p.(*startupBlockedProvider)
	assert.False(t, blocked)
	assert.NotContains(t, logs.String(), "provider=openrouter")
}

// ── sandbox.ssrf.allow_internal is the operator's list on hosted ───────────

// TestSandboxConfig_Hosted_AllowInternalChangeRefused: on hosted the SSRF
// allow list is also the provider endpoint guard's exemption list, so the
// tenant cannot widen it through PUT /security/sandbox-config (platform mode
// has no password step-up there). A body that resends the current list
// unchanged, as the settings screen does with every paths save, is accepted;
// core keeps the operator-owned setting writable.
func TestSandboxConfig_Hosted_AllowInternalChangeRefused(t *testing.T) {
	t.Cleanup(func() { providercommon.SetEndpointAllowlist(nil) })

	withEdition(t, config.EditionHosted)
	api := newTestRestAPIWithHome(t)
	w := sandboxConfigPUT(t, api, `{"ssrf":{"allow_internal":["127.0.0.1"]}}`)
	require.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
	assert.JSONEq(t, fmt.Sprintf(`{"error":%q,"field":"ssrf.allow_internal"}`, sandboxAllowInternalManagedMsg), w.Body.String())
	assert.Empty(t, api.agentLoop.GetConfig().Sandbox.SSRF.AllowInternal, "nothing may be written")
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "127.0.0.1", "nothing may be persisted")

	w = sandboxConfigPUT(t, api, `{"ssrf_allow_internal":[]}`)
	require.Equal(t, http.StatusOK, w.Code, "resending the unchanged (empty) list: body: %s", w.Body.String())

	withEdition(t, config.EditionCore)
	api = newTestRestAPIWithHome(t)
	w = sandboxConfigPUT(t, api, `{"ssrf":{"allow_internal":["127.0.0.1"]}}`)
	require.Equal(t, http.StatusOK, w.Code, "core: body: %s", w.Body.String())
}

// ── Azure OpenAI on hosted: the Custom endpoint route ──────────────────────

// TestProviderPlant_Hosted_CustomAzureEndpoint: the catalog id azure is
// unsupported in every edition, so Azure OpenAI is added as a custom
// OpenAI-compatible provider pointing at the resource's v1 base. On hosted
// the owner's save that points the row at the Azure base is accepted when the
// host resolves to a public address, and the saved row builds a client that
// posts to that base. The same host resolving to a loopback or private
// address is refused and the stored row is left unchanged. The save keeps the
// stored key, so no key probe runs, and the resolver answers every lookup:
// nothing reaches the network.
func TestProviderPlant_Hosted_CustomAzureEndpoint(t *testing.T) {
	const (
		azureHost = "myres.openai.azure.com"
		azureBase = "https://" + azureHost + "/openai/v1"
		blocked   = "The provider address points at an internal network, which is not reachable from here. Use the provider's public address."
	)
	const seededBase = "https://seeded.example.com/v1"
	body := fmt.Sprintf(`{"model":"gpt-4o","protocol":"openai-compatible","api_base":%q}`, azureBase)
	seed := func(t *testing.T, env *onboardAuthEnv) {
		t.Helper()
		t.Setenv("MY_AZURE_TEST_KEY", "sk-owner-stored")
		seedProviderRow(t, env, map[string]any{
			"provider": "my-azure", "model": "gpt-4o", "custom": true, "protocol": "openai-compatible",
			"api_base": seededBase, "api_key_ref": "MY_AZURE_TEST_KEY",
		})
	}

	t.Run("public address accepted and used at run time", func(t *testing.T) {
		env := newPlantEnv(t, config.EditionHosted, true)
		seed(t, env)
		res := &recordingResolver{ip: net.ParseIP("93.184.216.34")}
		checker := security.NewSSRFChecker(nil)
		checker.SetResolver(res)
		env.api.ssrfChecker = checker

		w := serveViaRealMux(t, env.api, env.cfg,
			plantRequest(http.MethodPut, "/api/v1/providers/my-azure", body, true), "198.51.100.200")

		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		assert.Contains(t, res.hosts, azureHost, "the save must check the Azure host")
		rows := diskProviderRows(t, env)
		require.Len(t, rows, 1)
		assert.Equal(t, "my-azure", rows[0]["provider"])
		assert.Equal(t, true, rows[0]["custom"])
		assert.Equal(t, "openai-compatible", rows[0]["protocol"])
		assert.Equal(t, azureBase, rows[0]["api_base"])

		raw, err := json.Marshal(rows[0])
		require.NoError(t, err)
		var mc config.ModelConfig
		require.NoError(t, json.Unmarshal(raw, &mc))
		p, _, err := providers.CreateProviderFromConfig(&mc)
		require.NoError(t, err, "the saved custom Azure row must build a client on hosted")
		withBase, ok := p.(interface{ APIBase() string })
		require.True(t, ok, "provider %T exposes no base", p)
		assert.Equal(t, azureBase, withBase.APIBase())
	})

	for i, ip := range []string{"127.0.0.1", "10.1.2.3"} {
		t.Run("host resolving to "+ip+" refused", func(t *testing.T) {
			env := newPlantEnv(t, config.EditionHosted, true)
			seed(t, env)
			checker := security.NewSSRFChecker(nil)
			checker.SetResolver(&recordingResolver{ip: net.ParseIP(ip)})
			env.api.ssrfChecker = checker

			w := serveViaRealMux(t, env.api, env.cfg,
				plantRequest(http.MethodPut, "/api/v1/providers/my-azure", body, true), fmt.Sprintf("198.51.100.%d", 201+i))

			require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body=%s", w.Body.String())
			assert.JSONEq(t, fmt.Sprintf(`{"error":%q}`, blocked), w.Body.String())
			rows := diskProviderRows(t, env)
			require.Len(t, rows, 1)
			assert.Equal(t, seededBase, rows[0]["api_base"], "the stored base must be unchanged")
		})
	}
}
