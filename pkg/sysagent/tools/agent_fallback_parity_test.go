package systools_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// The expected object chain comes from AgentUpdateRequest/FallbackModel, not
// the retired model_fallbacks string-array implementation.
func TestAgentToolsFallbackModelsPersistAndClear(t *testing.T) {
	deps, _ := newTestDeps()
	chain := []any{map[string]any{"model": "first", "provider": "provider-a"}, map[string]any{"model": "second", "provider": "provider-b"}}
	args := map[string]any{"name": "Fallback Bot", "description": "Fallback parity", "soul": "Test instructions", "model": "primary", "color": "#22C55E", "icon": "Robot", "fallback_models": chain}
	created := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
	if created.IsError {
		t.Fatalf("create: %s", created.ForLLM)
	}
	id := extractID(t, created.ForLLM)
	store := agentstore.New(deps.Home)
	state, err := store.ReadState(id)
	if err != nil {
		t.Fatal(err)
	}
	want := config.FallbackModelSlice{{Model: "first", Provider: "provider-a"}, {Model: "second", Provider: "provider-b"}}
	if !reflect.DeepEqual(state.Agent.FallbackModels, want) {
		t.Fatalf("create chain = %#v, want %#v", state.Agent.FallbackModels, want)
	}
	updater := systools.NewAgentUpdateTool(deps)
	for _, step := range []struct {
		raw  []any
		want config.FallbackModelSlice
	}{
		{[]any{map[string]any{"model": "replacement", "provider": "provider-c"}}, config.FallbackModelSlice{{Model: "replacement", Provider: "provider-c"}}},
		{[]any{}, config.FallbackModelSlice{}},
	} {
		result := updater.Execute(context.Background(), map[string]any{"id": id, "revision": state.Revision, "fallback_models": step.raw})
		if result.IsError {
			t.Fatalf("update: %s", result.ForLLM)
		}
		state, err = store.ReadState(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(state.Agent.FallbackModels) != len(step.want) || (len(step.want) > 0 && !reflect.DeepEqual(state.Agent.FallbackModels, step.want)) {
			t.Fatalf("updated chain=%#v, want %#v", state.Agent.FallbackModels, step.want)
		}
		if len(state.Agent.Model.Fallbacks) != 0 {
			t.Fatalf("legacy fallback field was written: %#v", state.Agent.Model.Fallbacks)
		}
	}
}

func TestAgentUpdateFallbackModelsRejectsInvalidWithoutWrites(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"null", nil}, {"scalar", "bad"}, {"legacy entry", []any{"legacy"}},
		{"missing model", []any{map[string]any{"provider": "a"}}},
		{"unknown key", []any{map[string]any{"model": "a", "secret": "do not persist"}}},
		{"too many", []any{map[string]any{"model": "a"}, map[string]any{"model": "b"}, map[string]any{"model": "c"}}},
		{"long model", []any{map[string]any{"model": strings.Repeat("a", 257)}}},
		{"long provider", []any{map[string]any{"model": "a", "provider": strings.Repeat("b", 65)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _ := newTestDeps()
			store := agentstore.New(deps.Home)
			_, err := store.CreateState("fallback-test", &config.AgentConfig{Name: "Fallback", Model: &config.AgentModelConfig{Primary: "primary"}, FallbackModels: config.FallbackModelSlice{{Model: "original", Provider: "original-provider"}}}, "original soul")
			if err != nil {
				t.Fatal(err)
			}
			before, err := store.ReadState("fallback-test")
			if err != nil {
				t.Fatal(err)
			}
			result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{"id": "fallback-test", "revision": before.Revision, "fallback_models": tc.value, "description": "must not persist"})
			if !result.IsError || !strings.Contains(result.ForLLM, "INVALID_INPUT") {
				t.Fatalf("want INVALID_INPUT, got %s", result.ForLLM)
			}
			after, err := store.ReadState("fallback-test")
			if err != nil {
				t.Fatal(err)
			}
			if before.Revision != after.Revision {
				t.Fatalf("invalid input changed revision %s -> %s", before.Revision, after.Revision)
			}
		})
	}
}

func TestAgentUpdateFallbackModelsBoundariesAndOmission(t *testing.T) {
	deps, _ := newTestDeps()
	store := agentstore.New(deps.Home)
	_, err := store.CreateState("fallback-bounds", &config.AgentConfig{Name: "Bounds", Model: &config.AgentModelConfig{Primary: "primary"}}, "instructions")
	if err != nil {
		t.Fatal(err)
	}
	for _, length := range []int{0, 1, 255, 256} {
		state, err := store.ReadState("fallback-bounds")
		if err != nil {
			t.Fatal(err)
		}
		model := strings.Repeat("界", length)
		provider := strings.Repeat("p", 64)
		result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{"id": "fallback-bounds", "revision": state.Revision, "fallback_models": []any{map[string]any{"model": model, "provider": provider}}})
		if result.IsError {
			t.Fatalf("length %d: %s", length, result.ForLLM)
		}
		state, err = store.ReadState("fallback-bounds")
		if err != nil {
			t.Fatal(err)
		}
		want := config.FallbackModelSlice{{Model: model, Provider: provider}}
		if !reflect.DeepEqual(state.Agent.FallbackModels, want) {
			t.Fatalf("length %d chain=%#v want %#v", length, state.Agent.FallbackModels, want)
		}
		result = systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{"id": "fallback-bounds", "revision": state.Revision, "description": "preserve fallback chain"})
		if result.IsError {
			t.Fatalf("omitted chain: %s", result.ForLLM)
		}
		state, err = store.ReadState("fallback-bounds")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(state.Agent.FallbackModels, want) {
			t.Fatalf("omission changed chain: %#v", state.Agent.FallbackModels)
		}
	}
}

func TestAgentUpdateFallbackModelsExternalWorkerRefusesWithoutWrites(t *testing.T) {
	deps, _ := newTestDeps()
	store := agentstore.New(deps.Home)
	_, err := store.CreateState("fallback-external", &config.AgentConfig{Name: "CLI", Type: config.AgentTypeWorker, Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "codex"}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.ReadState("fallback-external")
	if err != nil {
		t.Fatal(err)
	}
	result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{"id": "fallback-external", "revision": before.Revision, "fallback_models": []any{map[string]any{"model": "never"}}})
	if !result.IsError || !strings.Contains(result.ForLLM, "INVALID_INPUT") {
		t.Fatalf("external worker must refuse: %s", result.ForLLM)
	}
	after, err := store.ReadState("fallback-external")
	if err != nil {
		t.Fatal(err)
	}
	if before.Revision != after.Revision {
		t.Fatal("external rejection changed state")
	}
}
