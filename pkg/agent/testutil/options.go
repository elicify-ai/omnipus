package testutil

import "github.com/dapicom-ai/omnipus/pkg/config"

// Option is a functional option applied to harnessConfig before StartTestGateway fires.
type Option func(*harnessConfig)

// harnessConfig holds the pre-boot settings assembled by Option functions.
type harnessConfig struct {
	scenario   *ScenarioProvider
	agents     []config.AgentConfig
	sandbox    *config.OmnipusSandboxConfig
	bearerAuth bool
	allowEmpty bool
	apiBase    string // optional override for Providers[0].APIBase
}

// WithScenario uses the provided ScenarioProvider instead of a fresh empty one.
func WithScenario(s *ScenarioProvider) Option {
	return func(hc *harnessConfig) {
		hc.scenario = s
	}
}

// WithAgents injects a pre-seeded agents list (useful for handoff tests needing Ray+Max).
func WithAgents(agents []config.AgentConfig) Option {
	return func(hc *harnessConfig) {
		hc.agents = agents
	}
}

// WithSandboxConfig lets tests override the gateway's sandbox settings.
func WithSandboxConfig(sandbox config.OmnipusSandboxConfig) Option {
	return func(hc *harnessConfig) {
		hc.sandbox = &sandbox
	}
}

// WithBearerAuth seeds gateway.users with one admin/admin123 so all requests are authenticated.
// The token is stored on TestGateway and added to requests made via NewRequest automatically.
func WithBearerAuth() Option {
	return func(hc *harnessConfig) {
		hc.bearerAuth = true
	}
}

// WithAllowEmpty passes the allow-empty flag so boot succeeds without a default model.
func WithAllowEmpty() Option {
	return func(hc *harnessConfig) {
		hc.allowEmpty = true
	}
}

// WithAPIBase overrides Providers[0].APIBase. Used by perf tests to redirect
// LLM traffic to a local httptest.Server (see tests/perf/mock_openrouter_test.go)
// so the load test exercises the gateway pipeline without depending on a real
// OpenRouter endpoint, network latency, or rate limits.
func WithAPIBase(apiBase string) Option {
	return func(hc *harnessConfig) {
		hc.apiBase = apiBase
	}
}
