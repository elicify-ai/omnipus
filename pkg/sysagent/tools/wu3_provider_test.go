package systools_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wu3RoundTrip func(*http.Request) (*http.Response, error)

func (fn wu3RoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func wu3HTTPResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d", status), Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

// Only the network process edge is replaced; no real third-party traffic can
// escape. An unexpected call is a failing assertion, not a silent success.
func wu3Network(t *testing.T, respond wu3RoundTrip) {
	t.Helper()
	before := http.DefaultTransport
	http.DefaultTransport = respond
	t.Cleanup(func() { http.DefaultTransport = before })
}

// FR-031 / BDD-9.1 negative control: catalogue works, saved endpoint fails.
func TestWU3ConfigureProviderChecksSavedEndpointNotCatalogue(t *testing.T) {
	d, cfg := wu3Deps(t)
	cfg.Providers = []*config.ModelConfig{{Provider: "openai", Model: "chosen-model", APIBase: "https://saved.wu3.invalid/v1", APIKeyRef: "fixture-ref"}}
	require.NoError(t, d.CredStore.Set("fixture-ref", "wu3-old-key"))
	savedCalls, catalogueCalls := 0, 0
	wu3Network(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "saved.wu3.invalid" {
			savedCalls++
			assert.Equal(t, "/v1/models", r.URL.Path)
			assert.Equal(t, "Bearer wu3-new-key", r.Header.Get("Authorization"), "probe uses newly saved effective credential")
			return wu3HTTPResponse(r, 503, `{"error":"not ready"}`), nil
		}
		catalogueCalls++
		return wu3HTTPResponse(r, 200, `{"data":[{"id":"catalogue-only-model"}]}`), nil
	})
	ref := systools.IssueProviderReferenceForTest(t, d, "openai", "", "wu3-new-key")
	result := systools.NewProviderConfigureTool(d).Execute(context.Background(), map[string]any{"name": "openai", "api_key_ref": ref, "expected_revision": wu3SharedRow(t, d, "provider", "openai")["revision"]})
	assert.Equal(t, 1, savedCalls, "FR-031: saved effective custom endpoint must actually be probed")
	assert.Zero(t, catalogueCalls, "successful catalogue is not evidence about custom endpoint")
	assert.Equal(t, "https://saved.wu3.invalid/v1", cfg.Providers[0].APIBase)
	key, err := d.CredStore.Get(ref)
	require.NoError(t, err)
	assert.Equal(t, "wu3-new-key", key)
	old, err := d.CredStore.Get("fixture-ref")
	require.NoError(t, err)
	assert.Equal(t, "wu3-old-key", old, "D2-A selection must not overwrite the old version")
	assert.Equal(t, ref, cfg.Providers[0].APIKeyRef)
	wu3Outcome(t, result, "saved but not working")
}

func TestWU3ConfigureProviderKeylessLocalRunsNativeListing(t *testing.T) {
	d, cfg := wu3Deps(t)
	calls := 0
	wu3Network(t, func(r *http.Request) (*http.Response, error) {
		calls++
		assert.Equal(t, "local.wu3.invalid", r.URL.Host)
		assert.Equal(t, "/api/tags", r.URL.Path, "protocol-aware Ollama listing")
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Empty(t, r.Header.Get("X-Api-Key"))
		return wu3HTTPResponse(r, 200, `{"models":[{"name":"fixture-model"}]}`), nil
	})
	result := systools.NewProviderConfigureTool(d).Execute(context.Background(), map[string]any{"name": "ollama", "api_base": "http://local.wu3.invalid/v1", "expected_revision": "absent"})
	assert.Equal(t, 1, calls, "keyless must not mean check skipped")
	require.Len(t, cfg.Providers, 1)
	assert.Equal(t, "http://local.wu3.invalid/v1", cfg.Providers[0].APIBase)
	wu3Outcome(t, result, "working")
}

func TestWU3ConfigureProviderUsesSavedNonOpenAIProtocol(t *testing.T) {
	d, cfg := wu3Deps(t)
	cfg.Providers = []*config.ModelConfig{{Provider: "anthropic", Model: "fixture-model", APIBase: "https://protocol.wu3.invalid", Protocol: "anthropic", APIKeyRef: "protocol-ref"}}
	require.NoError(t, d.CredStore.Set("protocol-ref", "wu3-old-key"))
	calls := 0
	wu3Network(t, func(r *http.Request) (*http.Response, error) {
		calls++
		assert.Equal(t, "protocol.wu3.invalid", r.URL.Host)
		assert.Equal(t, "/v1/models", r.URL.Path)
		assert.Equal(t, "wu3-new-key", r.Header.Get("X-Api-Key"))
		assert.Empty(t, r.Header.Get("Authorization"), "Anthropic protocol is not Bearer protocol")
		assert.NotEmpty(t, r.Header.Get("Anthropic-Version"), "protocol requires version header")
		return wu3HTTPResponse(r, 200, `{"data":[{"id":"fixture-model"}]}`), nil
	})
	ref := systools.IssueProviderReferenceForTest(t, d, "anthropic", "", "wu3-new-key")
	result := systools.NewProviderConfigureTool(d).Execute(context.Background(), map[string]any{"name": "anthropic", "api_key_ref": ref, "expected_revision": wu3SharedRow(t, d, "provider", "anthropic")["revision"]})
	assert.Equal(t, 1, calls, "saved non-OpenAI protocol must drive the live listing")
	wu3Outcome(t, result, "working")
}

// BDD-9.7: the chosen model being absent must not be called tested or executed.
func TestWU3ProviderWorkingOnlyProvesModelListing(t *testing.T) {
	d, _ := wu3Deps(t)
	listCalls, completionCalls := 0, 0
	wu3Network(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			listCalls++
			return wu3HTTPResponse(r, 200, `{"data":[{"id":"different-from-chosen-model"}]}`), nil
		}
		completionCalls++
		return wu3HTTPResponse(r, 404, `{"error":"chosen model unavailable"}`), nil
	})
	ref := systools.IssueProviderReferenceForTest(t, d, "openai", "https://listing.wu3.invalid/v1", "wu3-test-key")
	result := systools.NewProviderConfigureTool(d).Execute(context.Background(), map[string]any{"name": "openai", "api_key_ref": ref, "api_base": "https://listing.wu3.invalid/v1", "expected_revision": "absent"})
	assert.Equal(t, 1, listCalls)
	assert.Zero(t, completionCalls, "configure_provider must not pretend to test model execution")
	body := wu3Outcome(t, result, "working")
	assert.Contains(t, body["check_scope"], "model listing")
	assert.Contains(t, strings.ToLower(fmt.Sprint(body["not_tested"])), "model", "execution not tested must be stated")
}

// #1060 F1/F3: invalid filters are not genuinely empty catalogs.
func TestWU3ModelsBadProviderFilterFailsLoudly(t *testing.T) {
	for _, name := range []string{"OpenRouter", "nonexistent-provider-xyz"} {
		t.Run(name, func(t *testing.T) {
			d, _ := wu3Deps(t)
			body := wu3Error(t, systools.NewModelsListTool(d).Execute(context.Background(), map[string]any{"provider": name}), "UNKNOWN_PROVIDER")
			assert.Contains(t, body["message"], name)
		})
	}
}

func TestWU3ModelsEmptyResultIsArrayNotNull(t *testing.T) {
	d, cfg := wu3Deps(t)
	cfg.Providers = []*config.ModelConfig{{Provider: "openai", Model: "fixture-model"}}
	body := wu3Success(t, systools.NewModelsListTool(d).Execute(context.Background(), map[string]any{"provider": "openai"}))
	assert.Equal(t, []any{}, body["models"], "#1060 empty model list is []")
	assert.Equal(t, float64(0), body["total"])
}

// #1060 F2: every configured provider ID exposed by list_providers can be
// checked by test_provider. It may say unusable, but never UNKNOWN_PROVIDER.
func TestWU3ProviderReadToolsAgreeOnConfiguredIdentity(t *testing.T) {
	d, cfg := wu3Deps(t)
	cfg.Providers = []*config.ModelConfig{{Provider: "grok-local", Model: "fixture-model", APIBase: "", APIKeyRef: "custom-ref"}}
	require.NoError(t, d.CredStore.Set("custom-ref", "wu3-test-key"))
	listed := wu3Success(t, systools.NewProviderListTool(d).Execute(context.Background(), nil))
	entries, ok := listed["providers"].([]any)
	require.True(t, ok)
	require.Len(t, entries, 1)
	assert.Equal(t, "grok-local", wu3Object(t, entries[0])["name"])
	result := systools.NewProviderTestTool(d).Execute(context.Background(), map[string]any{"name": "grok-local"})
	body := wu3Success(t, result)
	assert.Equal(t, "grok-local", body["name"])
	assert.Equal(t, "error", body["status"], "missing effective endpoint is unusable despite credential presence")
	assert.NotEmpty(t, body["error_message"], "unusable provider needs a visible explanation")
}

// #1060 F4 / Q4: dry_run is shape-only and never writes or calls a provider.
func TestWU3ConfigureProviderDryRunHasNoSideEffects(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprintf("valid_%t", valid), func(t *testing.T) {
			d, cfg := wu3Deps(t)
			base := "https://dry-run.wu3.invalid/v1"
			if !valid {
				base = "file:///etc/passwd"
			}
			ref := systools.IssueProviderReferenceForTest(t, d, "openai", base, "wu3-test-key")
			configBefore := wu3ReadFile(t, d.ConfigPath)
			credentialPath := d.Home + "/credentials.json"
			require.NoError(t, d.CredStore.Set("fixture-control", "wu3-control-value"))
			credsBefore := wu3ReadFile(t, credentialPath)
			calls := 0
			wu3Network(t, func(r *http.Request) (*http.Response, error) { calls++; return wu3HTTPResponse(r, 500, `{}`), nil })
			result := systools.NewProviderConfigureTool(d).Execute(context.Background(), map[string]any{"name": "openai", "api_key_ref": ref, "api_base": base, "dry_run": true, "expected_revision": "absent"})
			assert.Zero(t, calls, "dry_run never probes the upstream")
			assert.Equal(t, configBefore, wu3ReadFile(t, d.ConfigPath), "dry_run never persists config")
			assert.Equal(t, credsBefore, wu3ReadFile(t, credentialPath), "dry_run never persists credentials")
			assert.Empty(t, cfg.Providers)
			if valid {
				body := wu3Success(t, result)
				assert.Equal(t, "shape only; no network", body["check_scope"])
				assert.NotEmpty(t, body["not_tested"], "live key is explicitly untested")
			} else {
				wu3Error(t, result, "INVALID_INPUT")
				wu3Outcome(t, result, "failed: nothing saved")
			}
		})
	}
}
