package systools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// Fix round 8 (issue #1340, founder rule 2026-10-18): a fallback is a
// (provider, model) pair. The agent tools refuse an entry without a provider,
// naming the entry, and write nothing.
func TestAgentUpdateFallbackModelsRefusesUnpairedEntry(t *testing.T) {
	for _, tc := range []struct {
		name, names, missing string
		entry                map[string]any
	}{
		{"provider_missing", "unpaired-model", "provider", map[string]any{"model": "unpaired-model"}},
		{"provider_empty", "unpaired-model", "provider", map[string]any{"model": "unpaired-model", "provider": ""}},
		{"model_empty", "provider-b", "model", map[string]any{"model": "", "provider": "provider-b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _ := newTestDeps(t)
			store := agentstore.New(deps.Home)
			if _, err := store.CreateState("paired-test", &config.AgentConfig{Name: "Paired", Model: &config.AgentModelConfig{Primary: "primary"}}, "instructions"); err != nil {
				t.Fatal(err)
			}
			before, err := store.ReadState("paired-test")
			if err != nil {
				t.Fatal(err)
			}
			chain := []any{map[string]any{"model": "paired-model", "provider": "provider-a"}, tc.entry}
			result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{"id": "paired-test", "revision": before.Revision, "fallback_models": chain})
			if !result.IsError || !strings.Contains(result.ForLLM, "INVALID_INPUT") || !strings.Contains(result.ForLLM, tc.names) || !strings.Contains(result.ForLLM, tc.missing) {
				t.Fatalf("want INVALID_INPUT naming the unpaired entry (%s) and the missing %s, got %s", tc.names, tc.missing, result.ForLLM)
			}
			after, err := store.ReadState("paired-test")
			if err != nil {
				t.Fatal(err)
			}
			if before.Revision != after.Revision {
				t.Fatalf("refused input changed revision %s -> %s", before.Revision, after.Revision)
			}
		})
	}
}
