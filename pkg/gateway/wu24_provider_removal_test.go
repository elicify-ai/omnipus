package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WU-24, founder 2026-10-14 / L1 plan section 10. Assembled names let the
// removal guard scan tests without a blanket test-file exemption.
func wu24RemovedProviderIDs() []string {
	return []string{"codex" + "-cli", "github" + "-copilot"}
}

func wu24FreshProviderAPI(t *testing.T) *restAPI {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	// A restored vendor-status branch must not run an installed, paid CLI.
	t.Setenv("PATH", t.TempDir())
	withEdition(t, config.EditionHosted)
	api, _ := newAuthMethodOnboardingAPI(t)
	t.Setenv("OMNIPUS_HOME", api.homePath)
	api.providerCatalog = bootProviderCatalog(api.homePath)
	previous := providers.ProviderCatalog()
	providers.SetCatalog(api.providerCatalog)
	t.Cleanup(func() { providers.SetCatalog(previous) })
	return api
}

// Use the real route dispatcher and catalog handler. Only the middleware's
// trusted config snapshot is injected, as it is in existing REST tests.
func wu24ProviderRequest(t *testing.T, api *restAPI, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		require.NoError(t, err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), configContextKey{}, api.agentLoop.GetConfig()))
	w := httptest.NewRecorder()
	if path == "/api/v1/providers/catalog" {
		api.HandleProvidersCatalog(w, r)
	} else {
		api.HandleProviders(w, isolateRateLimit(t, r))
	}
	return w
}

func TestWU24ServedProviderCatalog(t *testing.T) {
	api := wu24FreshProviderAPI(t)
	w := wu24ProviderRequest(t, api, http.MethodGet, "/api/v1/providers/catalog", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var got gen.ProvidersCatalog
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.NotEmpty(t, got.Providers, "an empty response is not proof of removal")

	removed := wu24RemovedProviderIDs()
	var kept []gen.CatalogProvider
	for _, row := range got.Providers {
		for _, id := range removed {
			assert.NotEqual(t, id, row.Id, "retired model provider is still offered by the real handler")
		}
		if row.Protocol != nil {
			assert.NotEqual(t, "cli", string(*row.Protocol), "provider %q still advertises a subprocess model protocol", row.Id)
		}
		for _, method := range row.AuthMethods {
			assert.NotEqual(t, "cli_"+"login", string(method), "provider %q offers the retired login method", row.Id)
		}
		if row.Id == "openai-chatgpt" {
			kept = append(kept, row)
		}
	}
	assert.NotContains(t, w.Body.String(), `"cli_`+`kind"`, "served projection must not carry a retired sign-in mechanism")
	assert.NotContains(t, w.Body.String(), `"cli_`+`login"`, "no retired login method anywhere in the served JSON")
	require.Len(t, kept, 1, "KEEP control: HTTP ChatGPT remains in the real served catalog exactly once")
	require.NotNil(t, kept[0].Protocol, "KEEP control: HTTP ChatGPT must declare its protocol")
	assert.Equal(t, "openai-compatible", string(*kept[0].Protocol))
	assert.Equal(t, []gen.CatalogProviderAuthMethods{gen.CatalogProviderAuthMethodsSignIn}, kept[0].AuthMethods)
}

func TestWU24RemovedProviderSignIn(t *testing.T) {
	for _, loaded := range []bool{true, false} {
		t.Run(fmt.Sprintf("catalog_loaded_%t", loaded), func(t *testing.T) {
			api := wu24FreshProviderAPI(t)
			if !loaded {
				api.providerCatalog = nil
			}
			ids := append([]string{"wu24-never-registered"}, wu24RemovedProviderIDs()...)
			for _, id := range ids {
				for _, route := range []struct{ method, suffix string }{
					{http.MethodPost, "/sign-in"},
					{http.MethodGet, "/sign-in/status"},
				} {
					t.Run(id+"_"+route.method, func(t *testing.T) {
						w := wu24ProviderRequest(t, api, route.method, "/api/v1/providers/"+id+route.suffix, nil)
						// Characterization explicitly required by WU-24: today's
						// generic unknown-kind sign-in refusal is 400, with this
						// exact body, not a new replacement-specific message.
						assert.Equal(t, http.StatusBadRequest, w.Code, "removed kind %q was accepted: %s", id, w.Body.String())
						assert.JSONEq(t, `{"error":"provider does not support sign-in"}`, w.Body.String())
					})
				}
			}
		})
	}
}

func TestWU24RemovedProviderOnboardingProbe(t *testing.T) {
	api := wu24FreshProviderAPI(t)
	ids := append([]string{"wu24-never-registered"}, wu24RemovedProviderIDs()...)
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			body, err := json.Marshal(gen.ProbeProviderRequest{Id: id, Auth: gen.ProbeProviderRequestAuthSignIn})
			require.NoError(t, err)
			w := postProbe(t, api, string(body))
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.JSONEq(t, fmt.Sprintf(`{"error":%q,"field":"id"}`, fmt.Sprintf("unknown provider %q", id)), w.Body.String())
		})
	}
}

func TestWU24RemovedProviderConfigProjection(t *testing.T) {
	api := wu24FreshProviderAPI(t)
	ids := append([]string{"wu24-never-registered"}, wu24RemovedProviderIDs()...)
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			// Characterization of the existing unknown-row projection, required
			// by the greenfield/no-silent-fallback decision. Model slugs and a
			// sign-in auth choice must not turn an unknown id into a usable row.
			api.agentLoop.GetConfig().Providers = []*config.ModelConfig{{
				Provider: id, Model: "requested-model", Models: []string{"requested-model"},
				AuthMethod: config.AuthMethodSignIn,
			}}
			rows := getProviders(t, api)
			require.Len(t, rows, 1, "unknown configured row must remain visible, never silently dropped")
			row := rows[0]
			assert.Equal(t, id, row.Id, "never replace the operator's requested kind")
			assert.Equal(t, gen.ProviderStatusUnknownProvider, row.Status)
			require.NotNil(t, row.Error, "unknown kind must have a visible error")
			assert.Equal(t, fmt.Sprintf("unknown provider %q", id), *row.Error)
			assert.Equal(t, []string{}, row.Models, "no invented or retained usable model list")
			assert.Nil(t, row.Protocol, "no inferred fallback protocol")
		})
	}
}
