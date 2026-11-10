package agent

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Fix round 8 (issue #1340, founder rule 2026-10-18: "provider and model come
// always in pairs, never alone"). A fallback is used only when it names BOTH
// a provider and a model. An unpaired entry is never given a guessed
// provider, never routed through the default provider, and never silently
// disappears: the load reports it, naming the agent and the entry.

// routing1340PairedConfig has a configured row that an unpaired entry COULD
// be matched against (and an OpenRouter passthrough), so any guessing would
// turn the bare entry into a routed candidate.
func routing1340PairedConfig(t *testing.T, fallbacks config.FallbackModelSlice) (*config.Config, *config.AgentConfig) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	agentCfg := config.AgentConfig{
		ID:             "paired",
		Home:           home,
		Model:          &config.AgentModelConfig{Primary: "deepseek/deepseek-v4.1-flash", Provider: "openrouter"},
		FallbackModels: fallbacks,
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash"}},
			List:     []config.AgentConfig{agentCfg},
		},
		Providers: []*config.ModelConfig{
			{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash", APIBase: "https://openrouter.invalid/api/v1"},
			{Provider: "anthropic", Model: "claude-haiku-4-5", APIBase: "https://anthropic.invalid/v1"},
		},
	}
	// The gateway runs this right after loading the roster; it must not
	// supply a guessed provider either.
	config.NormalizeAgentRoster(cfg)
	return cfg, &cfg.Agents.List[0]
}

func TestRouting1340_UnpairedFallbackIsNotRoutedAndIsReported(t *testing.T) {
	readLog := captureLogFile(t, logger.WARN)
	cfg, agentCfg := routing1340PairedConfig(t, config.FallbackModelSlice{{Model: "claude-haiku-4-5"}})
	agent := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})

	want := []providers.FallbackCandidate{{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash"}}
	if !reflect.DeepEqual(agent.Candidates, want) {
		t.Errorf("candidates = %+v, want only the pinned primary %+v (an unpaired fallback is never routed)", agent.Candidates, want)
	}
	logged := readLog()
	if !strings.Contains(logged, "paired") || !strings.Contains(logged, "claude-haiku-4-5") || !strings.Contains(logged, "fallback") {
		t.Errorf("the unpaired fallback was not reported with the agent and the entry; log:\n%s", logged)
	}
}

func TestRouting1340_PairedFallbackStillRoutes(t *testing.T) {
	cfg, agentCfg := routing1340PairedConfig(t, config.FallbackModelSlice{{Provider: "anthropic", Model: "claude-haiku-4-5"}})
	agent := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})

	want := []providers.FallbackCandidate{
		{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash"},
		{Provider: "anthropic", Model: "claude-haiku-4-5"},
	}
	if !reflect.DeepEqual(agent.Candidates, want) {
		t.Errorf("candidates = %+v, want the pinned primary then the paired fallback %+v", agent.Candidates, want)
	}
}
