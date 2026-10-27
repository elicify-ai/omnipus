package systools_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/security"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// code-reviewer CR-03 / silent-failure-hunter SF-03 / architect F02 /
// comment-analyzer C1: the saved-entry check must use the credential just
// selected, not an older value still injected into the process environment
// under that reference (the reload that re-injects it is only queued).
// Both rotation directions are covered: a good old key must not certify a bad
// new one, and a bad old key must not fail a good new one.
func TestWU3ConfigureProviderChecksTheSavedKeyNotTheInjectedOldOne(t *testing.T) {
	const ref = "WU3_FIX_ROTATION_REF"
	for _, tc := range []struct {
		name, oldKey, newKey, accepted, outcome string
	}{
		{"good old key, bad new key", "wu3-old-good", "wu3-new-bad", "wu3-old-good", "saved but not working"},
		{"bad old key, good new key", "wu3-old-bad", "wu3-new-good", "wu3-new-good", "working"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, cfg := wu3Deps(t)
			cfg.Providers = []*config.ModelConfig{{Provider: "openai", Model: "chosen-model",
				APIBase: "https://rotation.wu3.invalid/v1", APIKeyRef: ref}}
			require.NoError(t, d.CredStore.Set(ref, tc.oldKey))
			t.Setenv(ref, tc.oldKey) // the boot-injected old value
			newRef := systools.IssueProviderReferenceForTest(t, d, "openai", "", tc.newKey)
			t.Setenv(newRef, tc.oldKey) // stale process value under the newly selected name
			var seen []string
			wu3Network(t, func(r *http.Request) (*http.Response, error) {
				require.Equal(t, "rotation.wu3.invalid", r.URL.Host)
				seen = append(seen, r.Header.Get("Authorization"))
				if r.Header.Get("Authorization") == "Bearer "+tc.accepted {
					return wu3HTTPResponse(r, 200, `{"data":[{"id":"chosen-model"}]}`), nil
				}
				return wu3HTTPResponse(r, 401, `{"error":"bad key"}`), nil
			})
			result := systools.NewProviderConfigureTool(d).Execute(context.Background(), map[string]any{
				"name": "openai", "api_key_ref": newRef,
				"expected_revision": wu3SharedRow(t, d, "provider", "openai")["revision"],
			})
			assert.Equal(t, []string{"Bearer " + tc.newKey}, seen, "exactly one probe, with the newly saved key")
			wu3Outcome(t, result, tc.outcome)
			assert.NotContains(t, result.ForLLM, tc.newKey)
			assert.NotContains(t, result.ForLLM, tc.oldKey)
		})
	}
}

// A saved reference whose credential cannot be read is reported, not
// silently replaced by another source.
func TestWU3ConfigureProviderUnreadableSavedKeyIsReported(t *testing.T) {
	d, cfg := wu3Deps(t)
	cfg.Providers = []*config.ModelConfig{{Provider: "openai", Model: "m",
		APIBase: "https://unreadable.wu3.invalid/v1", APIKeyRef: "WU3_FIX_MISSING_REF"}}
	t.Setenv("WU3_FIX_MISSING_REF", "wu3-env-only")
	calls := 0
	wu3Network(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return wu3HTTPResponse(r, 200, `{"data":[]}`), nil
	})
	listed := 0
	d.ListProviderModels = func(context.Context, catalog.Protocol, string, string, providers.URLChecker) ([]string, error) {
		listed++
		return []string{"m"}, nil
	}
	ref := systools.IssueProviderReferenceForTest(t, d, "openai", "", "wu3-saved-then-lost")
	t.Setenv(ref, "wu3-env-only")
	// The store loses the selected entry at config publication (a concurrent
	// removal); the check must say so instead of using the env value.
	d.SaveConfigLocked = func(c *config.Config) error {
		if err := d.CredStore.Delete(c.Providers[0].APIKeyRef); err != nil {
			return err
		}
		return config.SaveConfig(d.ConfigPath, c)
	}
	result := systools.NewProviderConfigureTool(d).Execute(context.Background(), map[string]any{
		"name": "openai", "api_key_ref": ref,
		"expected_revision": wu3SharedRow(t, d, "provider", "openai")["revision"],
	})
	body := wu3Outcome(t, result, "saved but not working")
	assert.Contains(t, body["reason"], "saved credential")
	assert.Zero(t, listed, "no probe with a substitute key")
	assert.Zero(t, calls)
}

// pr-test-analyzer TC-03 / security-lead W2: with the outbound-request guard
// wired, a cloud provider pointed at a forbidden address gets no request at
// all and is reported saved but not working; the key is never echoed.
func TestWU3ConfigureProviderGuardRefusesForbiddenCloudEndpoint(t *testing.T) {
	d, cfg := wu3Deps(t)
	d.URLChecker = security.NewSSRFChecker(nil)
	calls := 0
	wu3Network(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return wu3HTTPResponse(r, 200, `{"data":[]}`), nil
	})
	listed := 0
	d.ListProviderModels = func(context.Context, catalog.Protocol, string, string, providers.URLChecker) ([]string, error) {
		listed++
		return nil, nil
	}
	ref := systools.IssueProviderReferenceForTest(t, d, "openai", "https://10.20.30.40/v1", "sk-wu3fixguardsecret000000000000")
	result := systools.NewProviderConfigureTool(d).Execute(context.Background(), map[string]any{
		"name": "openai", "api_key_ref": ref, "api_base": "https://10.20.30.40/v1",
		"expected_revision": "absent",
	})
	body := wu3Outcome(t, result, "saved but not working")
	assert.Contains(t, body["reason"], "outbound-request guard")
	assert.Zero(t, calls, "no outbound request")
	assert.Zero(t, listed, "the listing never ran")
	assert.NotContains(t, result.ForLLM, "wu3fixguardsecret")
	require.Len(t, cfg.Providers, 1, "the entry is still saved")
	assert.Equal(t, "https://10.20.30.40/v1", cfg.Providers[0].APIBase)
}

// TC-03 controls: an allowed cloud endpoint is listed through the wired guard
// (the guard itself is handed to the listing), and a local provider keeps its
// deliberate exception (no guard).
func TestWU3ConfigureProviderGuardControls(t *testing.T) {
	t.Run("allowed cloud endpoint uses the wired guard", func(t *testing.T) {
		d, _ := wu3Deps(t)
		guard := security.NewSSRFChecker(nil)
		d.URLChecker = guard
		var got providers.URLChecker
		d.ListProviderModels = func(_ context.Context, _ catalog.Protocol, api, _ string, c providers.URLChecker) ([]string, error) {
			got = c
			assert.Equal(t, "https://93.184.216.34/v1", api)
			return []string{"m"}, nil
		}
		ref := systools.IssueProviderReferenceForTest(t, d, "openai", "https://93.184.216.34/v1", "sk-wu3fixallowed0000000000000000")
		result := systools.NewProviderConfigureTool(d).Execute(context.Background(), map[string]any{
			"name": "openai", "api_key_ref": ref, "api_base": "https://93.184.216.34/v1",
			"expected_revision": "absent",
		})
		wu3Outcome(t, result, "working")
		assert.Same(t, guard, got, "the listing runs through the wired guard")
	})
	t.Run("local provider keeps its exception", func(t *testing.T) {
		d, _ := wu3Deps(t)
		d.URLChecker = security.NewSSRFChecker(nil)
		var got providers.URLChecker
		d.ListProviderModels = func(_ context.Context, _ catalog.Protocol, _, _ string, c providers.URLChecker) ([]string, error) {
			got = c
			return []string{"m"}, nil
		}
		result := systools.NewProviderConfigureTool(d).Execute(context.Background(), map[string]any{
			"name": "ollama", "api_base": "http://127.0.0.1:11434/v1", "expected_revision": "absent",
		})
		wu3Outcome(t, result, "working")
		assert.IsType(t, providers.NoopChecker{}, got, "a local endpoint is not guarded")
	})
}
