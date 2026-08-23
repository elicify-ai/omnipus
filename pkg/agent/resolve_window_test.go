// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// ---------------------------------------------------------------------------
// Fixture: a 2.0.0 catalog document built in-test from real rows (ADR-067
// T067-02 is merged, so the catalog rung is driven by the real parser, not
// faked — spec X-27). Rows: openrouter / z-ai/glm-5.2 (1,048,576), ollama
// (local, no models), lmstudio (local by id), vllm (local by id, no window),
// codex-cli (protocol cli, cli_kind codex — the subprocess driver),
// openai-chatgpt (HTTP transport, cloud, a real window).
// ---------------------------------------------------------------------------

func windowTestProvider(id, protocol, api string, extra map[string]any, models ...map[string]any) map[string]any {
	p := map[string]any{
		"id":           id,
		"name":         id,
		"company":      id,
		"api":          api,
		"protocol":     protocol,
		"env":          []string{},
		"tier":         "standard",
		"auth_methods": []string{"api_key"},
		"aliases":      []string{},
		"resize_limits": map[string]any{
			"long_edge_px": 7680,
			"max_bytes":    10485760,
		},
		"models": models,
	}
	if models == nil {
		p["models"] = []map[string]any{}
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func windowTestModel(id string, window int) map[string]any {
	return map[string]any{
		"id":                id,
		"name":              id,
		"tool_call":         true,
		"context_window":    window,
		"max_output_tokens": 8192,
		"input_modalities":  []string{"text"},
		"status":            "active",
	}
}

// windowTestCatalogDoc returns the JSON document; glmWindow lets the
// re-clamp test lower the catalog value for openrouter / z-ai/glm-5.2.
func windowTestCatalogDoc(t *testing.T, glmWindow int) []byte {
	t.Helper()
	doc := map[string]any{
		"schema_version":        "2.0.0",
		"version":               "v2026.8.23",
		"updated_at":            "2026-08-23T00:00:00Z",
		"source":                "in-test fixture",
		"default_resize_limits": map[string]any{"long_edge_px": 7680, "max_bytes": 10485760},
		"providers": []map[string]any{
			windowTestProvider("openrouter", "openai-compatible", "https://openrouter.ai/api/v1", nil,
				windowTestModel("z-ai/glm-5.2", glmWindow)),
			windowTestProvider("ollama", "ollama", "http://localhost:11434/v1", nil),
			windowTestProvider("lmstudio", "openai-compatible", "http://127.0.0.1:1234/v1", nil,
				windowTestModel("qwen3-8b", 32768)),
			windowTestProvider("vllm", "openai-compatible", "http://localhost:8000/v1", nil),
			windowTestProvider("codex-cli", "cli", "", map[string]any{"cli_kind": "codex"},
				windowTestModel("gpt-5-codex", 400000)),
			windowTestProvider("openai-chatgpt", "openai-compatible", "https://chatgpt.com/backend-api/codex", map[string]any{"token_source": "codex-auth-json"},
				windowTestModel("gpt-5.4", 272000)),
		},
	}
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	return data
}

// installWindowTestCatalog swaps the package-level window catalog for the
// fixture document and restores the previous one on cleanup.
func installWindowTestCatalog(t *testing.T, glmWindow int) *catalog.Catalog {
	t.Helper()
	c, err := catalog.NewCatalog(windowTestCatalogDoc(t, glmWindow))
	require.NoError(t, err, "fixture document must parse under the real 2.0.0 rules")
	prev := windowCatalog()
	SetWindowCatalog(c)
	t.Cleanup(func() { SetWindowCatalog(prev) })
	return c
}

// installLiveWindowStub installs a fake live-limits rung for the ladder
// tests. The real on-demand, cached fetch is T066-10's; the ladder's ORDER
// around that rung is what these tests pin. A nil-returning stub is "rung
// skipped".
func installLiveWindowStub(t *testing.T, fn LiveWindowLookup) {
	t.Helper()
	prev := liveWindowLookup()
	SetLiveWindowLookup(fn)
	t.Cleanup(func() { SetLiveWindowLookup(prev) })
}

func windowTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Context = config.DefaultContextSettings()
	cfg.Agents.List = []config.AgentConfig{{ID: "mia"}, {ID: "jim"}}
	return cfg
}

// TestResolveContextWindow_Ladder — spec test 1 (B-01, B-02, B-02b, B-04,
// B-07, B-08; FR-001, FR-006, FR-007). DS-4 rows 1, 2, 3, 4, 6, 7, 8, 9.
func TestResolveContextWindow_Ladder(t *testing.T) {
	installWindowTestCatalog(t, 1_048_576)
	installLiveWindowStub(t, nil)

	t.Run("DS-4 #1 catalog wins with no overrides (B-01)", func(t *testing.T) {
		cfg := windowTestConfig()
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 1_048_576, got.Window)
		assert.Equal(t, WindowSourceCatalog, got.Source)
		assert.False(t, got.Clamped)
		assert.False(t, got.Exempt)
		assert.False(t, got.Unknown)
	})

	t.Run("DS-4 #2 per-agent override lowers (B-02)", func(t *testing.T) {
		cfg := windowTestConfig()
		cfg.Agents.List[0].ContextWindowOverride = intPtr(100_000)
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 100_000, got.Window)
		assert.Equal(t, WindowSourceOperator, got.Source)
		assert.False(t, got.Clamped)
		// The override belongs to mia only: jim still resolves from the catalog.
		other := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "jim")
		assert.Equal(t, 1_048_576, other.Window)
		assert.Equal(t, WindowSourceCatalog, other.Source)
	})

	t.Run("DS-4 #3 per-(provider, model) override, no per-agent (B-02b)", func(t *testing.T) {
		cfg := windowTestConfig()
		cfg.Context.ModelOverrides = []config.ContextModelOverride{{Provider: "openrouter", Model: "z-ai/glm-5.2", ContextWindow: 200_000}}
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 200_000, got.Window)
		assert.Equal(t, WindowSourceOperator, got.Source)
	})

	t.Run("DS-4 #4 per-agent beats per-(provider, model) (B-02b)", func(t *testing.T) {
		cfg := windowTestConfig()
		cfg.Agents.List[0].ContextWindowOverride = intPtr(100_000)
		cfg.Context.ModelOverrides = []config.ContextModelOverride{{Provider: "openrouter", Model: "z-ai/glm-5.2", ContextWindow: 200_000}}
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 100_000, got.Window)
		assert.Equal(t, WindowSourceOperator, got.Source)
	})

	t.Run("DS-4 #6 global default (B-02)", func(t *testing.T) {
		cfg := windowTestConfig()
		cfg.Context.DefaultContextWindow = intPtr(150_000)
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 150_000, got.Window)
		assert.Equal(t, WindowSourceOperator, got.Source)
	})

	t.Run("DS-4 #7 live beats catalog (B-04)", func(t *testing.T) {
		calls := 0
		installLiveWindowStub(t, func(provider, baseURL, model string) (int, bool) {
			calls++
			assert.Equal(t, "openrouter", provider)
			assert.Equal(t, "https://openrouter.ai/api/v1", baseURL, "live key carries the row's base URL")
			assert.Equal(t, "z-ai/glm-5.2", model)
			return 200_000, true
		})
		cfg := windowTestConfig()
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 200_000, got.Window)
		assert.Equal(t, WindowSourceLive, got.Source)
		assert.Equal(t, 1, calls)
	})

	t.Run("an operator override still reads the live capability, for the clamp only", func(t *testing.T) {
		// FR-002: capability is the live-or-catalog value, recomputed on
		// every resolution — so the (cached, never-on-the-turn-path) live
		// rung is consulted for the ceiling even when an override wins.
		installLiveWindowStub(t, func(string, string, string) (int, bool) { return 200_000, true })
		cfg := windowTestConfig()
		cfg.Context.DefaultContextWindow = intPtr(150_000)
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 150_000, got.Window, "the override is below the live capability and stands")
		assert.Equal(t, WindowSourceOperator, got.Source)
		assert.False(t, got.Clamped)
	})

	t.Run("DS-4 #8 cloud floor with one WARN naming the model (B-07)", func(t *testing.T) {
		readLog := captureLogFile(t, logger.WARN)
		cfg := windowTestConfig()
		got := ResolveWindow(cfg, "openrouter", "brand-new/model-not-in-catalog", "mia")
		assert.Equal(t, cloudWindowFloor, got.Window)
		assert.Equal(t, 128_000, got.Window)
		assert.Equal(t, WindowSourceFloor, got.Source)
		assert.False(t, got.Unknown)
		log := readLog()
		assert.Equal(t, 1, strings.Count(log, "brand-new/model-not-in-catalog"),
			"exactly one WARN naming the model; log=%s", log)
	})

	t.Run("DS-4 #9 ollama live, never floored (B-08)", func(t *testing.T) {
		installLiveWindowStub(t, func(provider, baseURL, model string) (int, bool) {
			if provider == "ollama" && model == "llama3:8b" {
				return 8192, true
			}
			return 0, false
		})
		cfg := windowTestConfig()
		got := ResolveWindow(cfg, "ollama", "llama3:8b", "mia")
		assert.Equal(t, 8192, got.Window)
		assert.Equal(t, WindowSourceLive, got.Source)
		assert.False(t, got.Unknown)
	})
}

// TestResolveContextWindow_ClampAllRungs — spec test 2 (B-03, B-03b; FR-002).
// DS-4 rows 5 and 15: every override rung is min(override, capability),
// recomputed on every resolution; a clamp logs exactly one WARN naming the
// agent, the override and the clamped value.
func TestResolveContextWindow_ClampAllRungs(t *testing.T) {
	installLiveWindowStub(t, nil)

	t.Run("DS-4 #5 per-agent override above capability is clamped + one WARN (B-03)", func(t *testing.T) {
		installWindowTestCatalog(t, 1_048_576)
		readLog := captureLogFile(t, logger.WARN)
		cfg := windowTestConfig()
		cfg.Agents.List[0].ContextWindowOverride = intPtr(2_000_000)
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 1_048_576, got.Window)
		assert.True(t, got.Clamped)
		assert.Equal(t, WindowSourceOperator, got.Source)
		log := readLog()
		assert.Equal(t, 1, len(strings.Split(strings.TrimSpace(log), "\n")),
			"exactly one WARN line; log=%s", log)
		assert.Contains(t, log, "mia")
		assert.Contains(t, log, "2000000")
		assert.Contains(t, log, "1048576")
	})

	t.Run("per-(provider, model) override is clamped too", func(t *testing.T) {
		installWindowTestCatalog(t, 1_048_576)
		cfg := windowTestConfig()
		cfg.Context.ModelOverrides = []config.ContextModelOverride{{Provider: "openrouter", Model: "z-ai/glm-5.2", ContextWindow: 5_000_000}}
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 1_048_576, got.Window)
		assert.True(t, got.Clamped)
		assert.Equal(t, WindowSourceOperator, got.Source)
	})

	t.Run("global default is clamped too", func(t *testing.T) {
		installWindowTestCatalog(t, 1_048_576)
		cfg := windowTestConfig()
		cfg.Context.DefaultContextWindow = intPtr(3_000_000)
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 1_048_576, got.Window)
		assert.True(t, got.Clamped)
	})

	t.Run("clamped against a live capability", func(t *testing.T) {
		installWindowTestCatalog(t, 1_048_576)
		installLiveWindowStub(t, func(string, string, string) (int, bool) { return 200_000, true })
		cfg := windowTestConfig()
		cfg.Agents.List[0].ContextWindowOverride = intPtr(500_000)
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 200_000, got.Window)
		assert.True(t, got.Clamped)
		assert.Equal(t, WindowSourceOperator, got.Source)
	})

	t.Run("an override at or below capability is not clamped", func(t *testing.T) {
		installWindowTestCatalog(t, 1_048_576)
		cfg := windowTestConfig()
		cfg.Agents.List[0].ContextWindowOverride = intPtr(1_048_576)
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 1_048_576, got.Window)
		assert.False(t, got.Clamped)
	})

	t.Run("DS-4 #15 re-clamp when the catalog is lowered; override persists (B-03b)", func(t *testing.T) {
		cfg := windowTestConfig()
		cfg.Agents.List[0].ContextWindowOverride = intPtr(1_048_576)

		installWindowTestCatalog(t, 1_048_576)
		first := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 1_048_576, first.Window)
		assert.False(t, first.Clamped)

		installWindowTestCatalog(t, 200_000)
		second := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 200_000, second.Window)
		assert.True(t, second.Clamped)
		assert.Equal(t, WindowSourceOperator, second.Source)
		require.NotNil(t, cfg.Agents.List[0].ContextWindowOverride)
		assert.Equal(t, 1_048_576, *cfg.Agents.List[0].ContextWindowOverride, "overrides never expire")
	})

	t.Run("cloud override above the floor is clamped to the floor when nothing knows the window", func(t *testing.T) {
		installWindowTestCatalog(t, 1_048_576)
		cfg := windowTestConfig()
		cfg.Context.ModelOverrides = []config.ContextModelOverride{{Provider: "openrouter", Model: "unknown/model", ContextWindow: 400_000}}
		got := ResolveWindow(cfg, "openrouter", "unknown/model", "mia")
		assert.Equal(t, cloudWindowFloor, got.Window)
		assert.True(t, got.Clamped)
		assert.Equal(t, WindowSourceOperator, got.Source)
	})
}

// TestResolveContextWindow_ExemptByCliDriver — spec test 3 (B-05; FR-005).
// DS-4 rows 14 and 14b: exempt by the row's cli_kind FIELD (a subprocess
// driver), never by id; openai-chatgpt is an HTTP transport and stays cloud.
func TestResolveContextWindow_ExemptByCliDriver(t *testing.T) {
	installWindowTestCatalog(t, 1_048_576)
	installLiveWindowStub(t, nil)

	t.Run("DS-4 #14 subprocess cli row → window 0, exempt, no source", func(t *testing.T) {
		cfg := windowTestConfig()
		// Overrides on an exempt row must not resurrect a window — the
		// provider manages its own context.
		cfg.Agents.List[0].ContextWindowOverride = intPtr(100_000)
		cfg.Context.DefaultContextWindow = intPtr(150_000)
		got := ResolveWindow(cfg, "codex-cli", "gpt-5-codex", "mia")
		assert.Equal(t, 0, got.Window)
		assert.True(t, got.Exempt)
		assert.Equal(t, WindowSource(""), got.Source)
		assert.False(t, got.Unknown)
	})

	t.Run("DS-4 #14b openai-chatgpt is cloud: catalog value", func(t *testing.T) {
		cfg := windowTestConfig()
		got := ResolveWindow(cfg, "openai-chatgpt", "gpt-5.4", "mia")
		assert.Equal(t, 272_000, got.Window)
		assert.Equal(t, WindowSourceCatalog, got.Source)
		assert.False(t, got.Exempt)
	})

	t.Run("exemption is by field: no provider-id literal in the resolver", func(t *testing.T) {
		src := readOwnedFileForTest(t, "resolve_window.go")
		for _, literal := range []string{`"codex-cli"`, `"codex"`, `"copilot"`, `"claude-cli"`, `"openai-chatgpt"`} {
			assert.NotContains(t, src, literal,
				"resolve_window.go must decide exemption by the catalog row's cli_kind field, never by id %s", literal)
		}
		assert.Contains(t, src, "CLIKind", "exemption must read the row's cli_kind field")
	})
}

// TestResolveWindow_NoAgent — spec test 3c (B-04b, B-04c; FR-001 / US-1.AC9,
// AC10). Without an agent id rungs 2–6 apply; an exempt provider returns 0
// with no source; an override for a provider that no longer exists is
// ignored.
func TestResolveWindow_NoAgent(t *testing.T) {
	installWindowTestCatalog(t, 1_048_576)
	installLiveWindowStub(t, nil)

	t.Run("B-04b catalog without an agent", func(t *testing.T) {
		cfg := windowTestConfig()
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "")
		assert.Equal(t, 1_048_576, got.Window)
		assert.Equal(t, WindowSourceCatalog, got.Source)
	})

	t.Run("B-04b per-agent override applies only with the agent id", func(t *testing.T) {
		cfg := windowTestConfig()
		cfg.Agents.List[0].ContextWindowOverride = intPtr(100_000)
		noAgent := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "")
		assert.Equal(t, 1_048_576, noAgent.Window, "rung 1 must not apply without an agent id")
		assert.Equal(t, WindowSourceCatalog, noAgent.Source)
		withAgent := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 100_000, withAgent.Window)
		assert.Equal(t, WindowSourceOperator, withAgent.Source)
	})

	t.Run("B-04b per-(provider, model) and global rungs apply without an agent", func(t *testing.T) {
		cfg := windowTestConfig()
		cfg.Context.ModelOverrides = []config.ContextModelOverride{{Provider: "openrouter", Model: "z-ai/glm-5.2", ContextWindow: 200_000}}
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "")
		assert.Equal(t, 200_000, got.Window)
		assert.Equal(t, WindowSourceOperator, got.Source)

		cfg = windowTestConfig()
		cfg.Context.DefaultContextWindow = intPtr(150_000)
		got = ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "")
		assert.Equal(t, 150_000, got.Window)
		assert.Equal(t, WindowSourceOperator, got.Source)
	})

	t.Run("B-04b exempt provider → 0, no source", func(t *testing.T) {
		cfg := windowTestConfig()
		got := ResolveWindow(cfg, "codex-cli", "gpt-5-codex", "")
		assert.Equal(t, 0, got.Window)
		assert.True(t, got.Exempt)
		assert.Equal(t, WindowSource(""), got.Source)
	})

	t.Run("B-04c dead override for a deleted provider is ignored", func(t *testing.T) {
		cfg := windowTestConfig()
		cfg.Context.ModelOverrides = []config.ContextModelOverride{
			{Provider: "deleted-provider", Model: "some-model", ContextWindow: 50_000},
			{Provider: "openrouter", Model: "z-ai/glm-5.2", ContextWindow: 200_000},
		}
		// The live entry still works.
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "")
		assert.Equal(t, 200_000, got.Window)
		assert.Equal(t, WindowSourceOperator, got.Source)
		// The dead entry is not honoured: the provider is neither in the
		// catalog nor configured, so the pair resolves as if the entry were
		// absent (an unknown-host provider is cloud → floor).
		dead := ResolveWindow(cfg, "deleted-provider", "some-model", "")
		assert.NotEqual(t, WindowSourceOperator, dead.Source, "a dead override must be ignored, got %+v", dead)
		assert.NotEqual(t, 50_000, dead.Window)
		// Pruning on the next write is T066-17's; the entry itself is left alone here.
		assert.Len(t, cfg.Context.ModelOverrides, 2, "the resolver never mutates settings")
	})

	t.Run("an unknown agent id behaves like no agent", func(t *testing.T) {
		cfg := windowTestConfig()
		got := ResolveWindow(cfg, "openrouter", "z-ai/glm-5.2", "nobody")
		assert.Equal(t, 1_048_576, got.Window)
		assert.Equal(t, WindowSourceCatalog, got.Source)
	})

	t.Run("nil config still resolves from the catalog", func(t *testing.T) {
		got := ResolveWindow(nil, "openrouter", "z-ai/glm-5.2", "mia")
		assert.Equal(t, 1_048_576, got.Window)
		assert.Equal(t, WindowSourceCatalog, got.Source)
	})
}

// TestResolveContextWindow_ByLocality — spec test 4 (B-08, B-09, B-10b;
// FR-007, FR-008 refusal half). Drives ADR-067's locality predicate through
// the fixture rows: ollama / lmstudio / custom loopback / custom public. A
// local endpoint with no live value is REFUSED (Unknown), never floored; a
// custom row at a public host is cloud and floored. The projection half
// (window_unknown on the catalog GET) is T066-17's; the pre-turn gate order
// is asserted by source below.
func TestResolveContextWindow_ByLocality(t *testing.T) {
	installWindowTestCatalog(t, 1_048_576)

	t.Run("DS-4 #10 vllm with no live value → refused, never floored (B-09)", func(t *testing.T) {
		installLiveWindowStub(t, nil)
		readLog := captureLogFile(t, logger.WARN)
		cfg := windowTestConfig()
		got := ResolveWindow(cfg, "vllm", "meta-llama/Llama-3-8B", "mia")
		assert.True(t, got.Unknown)
		assert.Equal(t, 0, got.Window)
		assert.Equal(t, WindowSource(""), got.Source)
		assert.False(t, got.Exempt)
		assert.NotContains(t, readLog(), "floor", "a local endpoint must never be floored")
	})

	t.Run("DS-4 #9 ollama live 8,192 (B-08)", func(t *testing.T) {
		installLiveWindowStub(t, func(provider, _, _ string) (int, bool) {
			if provider == "ollama" {
				return 8192, true
			}
			return 0, false
		})
		got := ResolveWindow(windowTestConfig(), "ollama", "llama3:8b", "mia")
		assert.Equal(t, 8192, got.Window)
		assert.Equal(t, WindowSourceLive, got.Source)
	})

	t.Run("lmstudio is local by id: catalog value, live failure → refused", func(t *testing.T) {
		installLiveWindowStub(t, nil)
		// A catalog window for a local row is still a real capability.
		got := ResolveWindow(windowTestConfig(), "lmstudio", "qwen3-8b", "mia")
		assert.Equal(t, 32768, got.Window)
		assert.Equal(t, WindowSourceCatalog, got.Source)
		// No catalog row, no live value → refused.
		miss := ResolveWindow(windowTestConfig(), "lmstudio", "some-other-model", "mia")
		assert.True(t, miss.Unknown)
		assert.Equal(t, 0, miss.Window)
	})

	t.Run("DS-4 #11 vllm with a per-(provider, model) override → usable (B-10)", func(t *testing.T) {
		installLiveWindowStub(t, nil)
		cfg := windowTestConfig()
		cfg.Context.ModelOverrides = []config.ContextModelOverride{{Provider: "vllm", Model: "meta-llama/Llama-3-8B", ContextWindow: 32768}}
		got := ResolveWindow(cfg, "vllm", "meta-llama/Llama-3-8B", "mia")
		assert.Equal(t, 32768, got.Window)
		assert.Equal(t, WindowSourceOperator, got.Source)
		assert.False(t, got.Unknown)
		assert.False(t, got.Clamped, "no capability is known, so nothing to clamp against")
	})

	t.Run("DS-4 #12 custom row at a public host is cloud → floor (B-10b)", func(t *testing.T) {
		installLiveWindowStub(t, nil)
		cfg := windowTestConfig()
		cfg.Providers = []*config.ModelConfig{{ModelName: "proxy-model", Model: "proxy-model", Provider: "my-proxy", APIBase: "https://llm.example.com/v1"}}
		got := ResolveWindow(cfg, "my-proxy", "proxy-model", "mia")
		assert.Equal(t, cloudWindowFloor, got.Window)
		assert.Equal(t, WindowSourceFloor, got.Source)
		assert.False(t, got.Unknown)
	})

	t.Run("DS-4 #13 custom row at 127.0.0.1 is local → refused (B-09)", func(t *testing.T) {
		installLiveWindowStub(t, nil)
		cfg := windowTestConfig()
		cfg.Providers = []*config.ModelConfig{{ModelName: "proxy-model", Model: "proxy-model", Provider: "my-proxy", APIBase: "http://127.0.0.1:8080/v1"}}
		got := ResolveWindow(cfg, "my-proxy", "proxy-model", "mia")
		assert.True(t, got.Unknown)
		assert.Equal(t, 0, got.Window)
		assert.Equal(t, WindowSource(""), got.Source)
	})

	t.Run("custom loopback row with a live value is usable", func(t *testing.T) {
		installLiveWindowStub(t, func(provider, baseURL, _ string) (int, bool) {
			if provider == "my-proxy" && baseURL == "http://127.0.0.1:8080/v1" {
				return 4096, true
			}
			return 0, false
		})
		cfg := windowTestConfig()
		cfg.Providers = []*config.ModelConfig{{ModelName: "proxy-model", Model: "proxy-model", Provider: "my-proxy", APIBase: "http://127.0.0.1:8080/v1"}}
		got := ResolveWindow(cfg, "my-proxy", "proxy-model", "mia")
		assert.Equal(t, 4096, got.Window)
		assert.Equal(t, WindowSourceLive, got.Source)
	})

	t.Run("the refusal code, attribution and copy are the contract's", func(t *testing.T) {
		assert.Equal(t, LLMErrorCode("context_window_unknown"), CodeContextWindowUnknown)
		assert.Equal(t, generated.LLMErrorAttributionConfig, AttributionForCode(CodeContextWindowUnknown))
		// The copy itself is the contract's (LLMError.yaml x-user-messages,
		// read through the generated catalogue); pin its shape, not a paste.
		msg := UserMessageForCode(CodeContextWindowUnknown)
		assert.Equal(t, generated.LLMErrorUserMessages["context_window_unknown"], msg)
		assert.Contains(t, msg, "Settings → Models → Model overrides", "the copy names the exact field to set")
		assert.NotContains(t, strings.ToLower(msg), "try again", "config-attributed copy must not tell the user to retry")
		llm := TranslateTurnError(ErrContextWindowUnknown)
		assert.Equal(t, CodeContextWindowUnknown, llm.Code)
		assert.False(t, llm.Retryable, "config-attributed copy must not invite a retry")
	})

	t.Run("gate order: the window gate sits after the workspace gate, before the first budget check", func(t *testing.T) {
		src := readOwnedFileForTest(t, "loop.go")
		runTurn := src[strings.Index(src, "func (al *AgentLoop) runTurn("):]
		wsGate := strings.Index(runTurn, "resolveTurnWorkDirOrRefuse(turnCtx")
		windowGate := strings.Index(runTurn, "ErrContextWindowUnknown")
		budget := strings.Index(runTurn, "isOverContextBudget(")
		require.Positive(t, wsGate)
		require.Positive(t, windowGate, "runTurn must refuse an unknown window with ErrContextWindowUnknown")
		require.Positive(t, budget)
		assert.Greater(t, windowGate, wsGate, "context_window_unknown is evaluated after the earlier pre-turn refusals")
		assert.Less(t, windowGate, budget, "the refusal must fire before any budget check reads the window")
	})
}

// TestNewAgentInstance_MaxTokensClampedWhenBudgetNonPositive — spec test 3b
// (B-05b; FR-005b / A-18). DS-4 row 16: W = 8,192, max_tokens = 8,192 →
// effective max_tokens = 2,048, one WARN naming the model, B > 0.
func TestNewAgentInstance_MaxTokensClampedWhenBudgetNonPositive(t *testing.T) {
	installWindowTestCatalog(t, 1_048_576)
	installLiveWindowStub(t, func(provider, _, _ string) (int, bool) {
		if provider == "ollama" {
			return 8192, true
		}
		return 0, false
	})

	newCfg := func(t *testing.T, maxTokens int) *config.Config {
		t.Helper()
		home := filepath.Join(t.TempDir(), "home")
		require.NoError(t, os.MkdirAll(home, 0o700))
		cfg := &config.Config{
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Home:              home,
					ModelName:         "llama3:8b",
					Provider:          "ollama",
					MaxTokens:         maxTokens,
					MaxToolIterations: 5,
				},
				List: []config.AgentConfig{{ID: "mia", Home: home}},
			},
			Providers: []*config.ModelConfig{{ModelName: "llama3:8b", Model: "ollama/llama3:8b", Provider: "ollama", APIBase: "http://localhost:11434/v1"}},
		}
		cfg.Context = config.DefaultContextSettings()
		return cfg
	}

	t.Run("DS-4 #16 8,192 / 8,192 → 2,048 + one WARN", func(t *testing.T) {
		readLog := captureLogFile(t, logger.WARN)
		cfg := newCfg(t, 8192)
		agent := NewAgentInstance(&cfg.Agents.List[0], &cfg.Agents.Defaults, cfg, &mockProvider{})
		assert.Equal(t, 8192, agent.ContextWindow)
		assert.Equal(t, WindowSourceLive, agent.WindowSource)
		assert.Equal(t, 2048, agent.MaxTokens)
		assert.Positive(t, agentContextBudget(agent), "B must be > 0 after the clamp")
		log := readLog()
		assert.Contains(t, log, "llama3:8b", "the WARN names the model")
		assert.Contains(t, log, "8192")
		assert.Contains(t, log, "2048")
	})

	t.Run("a max_tokens that leaves B > 0 is left alone", func(t *testing.T) {
		cfg := newCfg(t, 1024)
		agent := NewAgentInstance(&cfg.Agents.List[0], &cfg.Agents.Defaults, cfg, &mockProvider{})
		assert.Equal(t, 8192, agent.ContextWindow)
		assert.Equal(t, 1024, agent.MaxTokens)
		assert.Positive(t, agentContextBudget(agent))
	})
}

// TestWindowAgreement_OneBudgetAllSites — spec test 6 (B-06; FR-004, SC-009).
// One resolved window feeds one budget B at every site; the retired
// fallbacks are gone from the source; exactly one cloudWindowFloor exists.
func TestWindowAgreement_OneBudgetAllSites(t *testing.T) {
	t.Run("SC-009 grep over pkg/agent and pkg/config", func(t *testing.T) {
		forbidden := regexp.MustCompile(`maxTokens \* 4|contextWindow = 128000|newContextWindow = 128000|SummarizeTokenPercent`)
		for _, dir := range []string{".", filepath.Join("..", "config")} {
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
					continue
				}
				data, err := os.ReadFile(filepath.Join(dir, name))
				require.NoError(t, err)
				if m := forbidden.Find(data); m != nil {
					t.Errorf("%s/%s still contains the retired pattern %q (FR-004)", dir, name, m)
				}
			}
		}
	})

	t.Run("exactly one cloudWindowFloor definition, valued 128000, in pkg/agent", func(t *testing.T) {
		entries, err := os.ReadDir(".")
		require.NoError(t, err)
		defs := 0
		literal := 0
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			data, err := os.ReadFile(name)
			require.NoError(t, err)
			defs += len(regexp.MustCompile(`cloudWindowFloor\s*=\s*128000`).FindAll(data, -1))
			literal += strings.Count(string(data), "128000")
		}
		assert.Equal(t, 1, defs, "exactly one `cloudWindowFloor = 128000` constant")
		assert.Equal(t, 1, literal, "128000 must appear only as the cloudWindowFloor constant in pkg/agent")
		assert.Equal(t, 128000, cloudWindowFloor)
	})

	t.Run("AgentDefaults.ContextWindow no longer exists", func(t *testing.T) {
		src := readOwnedFileForTest(t, filepath.Join("..", "config", "config.go"))
		assert.NotContains(t, src, "OMNIPUS_AGENTS_DEFAULTS_CONTEXT_WINDOW")
		assert.NotContains(t, src, "Defaults.ContextWindow")
		loopSrc := readOwnedFileForTest(t, "loop.go")
		assert.NotContains(t, loopSrc, "Defaults.ContextWindow", "every consumer reads the resolved window, never a config default")
	})

	t.Run("every budget site reads the one resolved window through agentContextBudget", func(t *testing.T) {
		src := readOwnedFileForTest(t, "loop.go")
		calls := regexp.MustCompile(`isOverContextBudget\(\s*([^,]+),`).FindAllStringSubmatch(src, -1)
		require.GreaterOrEqual(t, len(calls), 2, "pre-turn and timeout-recovery sites")
		for _, c := range calls {
			assert.Equal(t, "agentContextBudget(ts.agent)", strings.TrimSpace(c[1]))
		}
		switchFn := src[strings.Index(src, "func (al *AgentLoop) handleModelSwitch("):]
		switchFn = switchFn[:strings.Index(switchFn, "\n}\n")]
		assert.Contains(t, switchFn, "ResolveWindow(", "model-switch re-window consolidates onto the resolver")
		assert.NotContains(t, switchFn, "128000")
	})

	t.Run("an exempt agent has no budget and every check is skipped", func(t *testing.T) {
		inst := &AgentInstance{ContextWindow: 0, MaxTokens: 4096, WindowExempt: true}
		assert.True(t, inst.budgetChecksExempt())
		assert.Equal(t, 0, agentContextBudget(inst))
		sized := &AgentInstance{ContextWindow: 100000, MaxTokens: 4096}
		assert.False(t, sized.budgetChecksExempt())
		assert.Equal(t, contextBudget(100000, 4096, breadcrumbTokenCap), agentContextBudget(sized))
	})
}
