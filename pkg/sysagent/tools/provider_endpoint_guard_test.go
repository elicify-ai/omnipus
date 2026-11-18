// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// The system agent's provider checks (configure_provider's saved-entry check,
// list_models) list a configured row's models live. A row on a private host is
// "local", and local rows were listed with a no-op checker — on a hosted
// tenant that is a loopback call steered by whatever row was saved. On hosted
// the provider endpoint guard applies to local rows as well; elsewhere a local
// endpoint (Ollama, LM Studio) is still listed.

func withSysagentEdition(t *testing.T, e string) {
	t.Helper()
	t.Cleanup(config.SetEdition(e))
}

func startSysagentCanary(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"canary-model"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func plantedLocalRow(base string) *config.ModelConfig {
	return &config.ModelConfig{
		Provider: "planted", Model: "m1", Custom: true,
		Protocol: "openai-compatible", APIBase: base,
	}
}

// guardDeps is newProviderTestDeps with the REAL listing (no stub) and no
// configured SSRF checker (sandbox.ssrf off), so only the edition decides.
func guardDeps(t *testing.T, row *config.ModelConfig) *Deps {
	t.Helper()
	cfg := &config.Config{Providers: []*config.ModelConfig{row}}
	deps, _, _ := newProviderTestDeps(t, cfg)
	deps.ListProviderModels = nil
	deps.URLChecker = nil
	return deps
}

func TestSysagentProviderChecks_Hosted_DoNotCallLocalRow(t *testing.T) {
	withSysagentEdition(t, config.EditionHosted)
	srv, hits := startSysagentCanary(t)
	row := plantedLocalRow(srv.URL + "/v1")
	deps := guardDeps(t, row)

	_ = NewProviderConfigureTool(deps).checkSavedProvider(context.Background(), row)
	res := NewModelsListTool(deps).Execute(context.Background(), nil)
	require.NotNil(t, res)

	assert.Equal(t, int64(0), hits.Load(), "hosted: neither check may reach the loopback row")
}

func TestSysagentProviderChecks_Core_StillListLocalRow(t *testing.T) {
	withSysagentEdition(t, config.EditionCore)
	srv, hits := startSysagentCanary(t)
	row := plantedLocalRow(srv.URL + "/v1")
	deps := guardDeps(t, row)

	_ = NewProviderConfigureTool(deps).checkSavedProvider(context.Background(), row)
	assert.Equal(t, int64(1), hits.Load(), "configure_provider's check lists the local row")
	_ = NewModelsListTool(deps).Execute(context.Background(), nil)
	assert.Equal(t, int64(2), hits.Load(), "list_models lists the local row")
}
