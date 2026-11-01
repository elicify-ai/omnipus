package systools_test

// CHECK-11 / S-64/S-68/S-76/S-94: production registry boundary matrix.
// Generic config writes accept unoffered strings; typed agent writes do not.
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT_TL94_GenericCompositeAllValidStringClassesSurviveReopen(t *testing.T) {
	for _, shape := range []string{"default_model", "recap_fallback_models", "section"} {
		for _, value := range []string{"", "high", strings.Repeat("x", 32)} {
			t.Run(fmt.Sprintf("%s/length=%d", shape, len(value)), func(t *testing.T) {
				deps, _ := tlPersistentDeps(t)
				reg := tlRegistry(t, deps)
				key := "agents.defaults." + shape
				var input any
				switch shape {
				case "default_model":
					input = map[string]any{"provider": "openrouter", "model": "unoffered-generic-model", "reasoning_effort": value}
				case "recap_fallback_models":
					input = []any{map[string]any{"provider": "openrouter", "model": "unoffered-generic-model", "reasoning_effort": value}}
				case "section":
					key = "agents.defaults"
					// An existing writable sibling isolates effort validation from new recap-limit support.
					input = map[string]any{"max_tokens": 4096, "recap_reasoning_effort": value}
				}
				res := tlSetConfig(t, reg, key, input)
				require.False(t, res.IsError, "valid actual JSON composite with a <=32 string is accepted without a catalog check: %s", res.ForLLM)
				got := tlDurableValue(t, deps, key)
				var effort any
				switch shape {
				case "default_model":
					m := got.(map[string]any)
					assert.Equal(t, "unoffered-generic-model", m["model"])
					effort = m["reasoning_effort"]
				case "recap_fallback_models":
					list := got.([]any)
					require.Len(t, list, 1)
					m := list[0].(map[string]any)
					assert.Equal(t, "unoffered-generic-model", m["model"])
					effort = m["reasoning_effort"]
				case "section":
					m := got.(map[string]any)
					assert.EqualValues(t, 4096, m["max_tokens"], "valid existing sibling persists together")
					effort = m["recap_reasoning_effort"]
				}
				if value == "" {
					assert.True(t, effort == nil || effort == "", "empty clears the stored override")
				} else {
					assert.Equal(t, value, effort, "reopened codec must not drop or remap a generic-config value")
				}
			})
		}
	}
}

func TestT_TL64_TypedPrimaryAndFallbackValidationIsAtomic(t *testing.T) {
	for _, slot := range []string{"primary", "fallback"} {
		for _, value := range []string{"xhigh", strings.Repeat("x", 32), strings.Repeat("x", 33)} {
			t.Run(fmt.Sprintf("%s/length=%d", slot, len(value)), func(t *testing.T) {
				deps, _ := tlPersistentDeps(t)
				reg := tlRegistry(t, deps)
				id := tlCreateMainAgent(t, reg, "Typed Matrix", tlPrimaryModel, tlPrimaryProvider, nil)
				before, err := os.ReadFile(filepath.Join(deps.Home, "entities", "agents", id+".json"))
				require.NoError(t, err)
				args := map[string]any{"id": id, "revision": currentAgentRevision(t, deps, id), "description": "valid sibling must not save"}
				if slot == "primary" {
					args["reasoning_effort"] = value
				} else {
					args["fallback_models"] = []any{map[string]any{"provider": tlPrimaryProvider, "model": tlPrimaryModel, "reasoning_effort": value}}
				}
				res := tlExec(t, reg, "update_agent", args)
				require.True(t, res.IsError)
				text := tlRefusalText(t, res)
				if len(value) == 33 {
					assert.Contains(t, text, tlLenEffortMsg)
				} else {
					for _, option := range []string{"provider_default", "none", "low", "high", "max"} {
						assert.Contains(t, text, option, "unoffered refusal lists each offered option")
					}
				}
				after, err := os.ReadFile(filepath.Join(deps.Home, "entities", "agents", id+".json"))
				require.NoError(t, err)
				assert.Equal(t, before, after, "nothing saved, including a valid sibling")
			})
		}
	}
}

func TestT_TL76_SchemaEnumAndRuntimeFieldNamesAgree(t *testing.T) {
	deps, _ := tlPersistentDeps(t)
	reg := tlRegistry(t, deps)
	tool, ok := reg.Get("get_agent")
	require.True(t, ok)
	raw, err := json.Marshal(tool.Parameters())
	require.NoError(t, err)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(raw, &schema))
	properties := schema["properties"].(map[string]any)
	field, ok := properties["fields"].(map[string]any)
	if !ok {
		t.Fatal("BLOCKED: get_agent fields enum/schema not implemented — required by S-76 and T8 dependency TD-1")
	}
	items, ok := field["items"].(map[string]any)
	require.True(t, ok)
	enum, ok := items["enum"].([]any)
	require.True(t, ok)
	require.Contains(t, enum, "reasoning_effort", "schema must advertise the new read field")
	id := tlCreateMainAgent(t, reg, "Schema Matrix", tlPrimaryModel, tlPrimaryProvider, nil)
	for _, name := range enum {
		res := tlExec(t, reg, "get_agent", map[string]any{"id": id, "fields": []any{name}})
		assert.False(t, res.IsError, "every advertised field is accepted by runtime: %v: %s", name, res.ForLLM)
	}
	bad := tlExec(t, reg, "get_agent", map[string]any{"id": id, "fields": []any{"effort"}})
	require.True(t, bad.IsError)
	text := tlRefusalText(t, bad)
	require.Contains(t, text, `unknown field "effort" for agent. Valid: `)
	_, names, found := strings.Cut(text, "Valid: ")
	require.True(t, found)
	listed := strings.Split(strings.TrimSuffix(strings.TrimSpace(names), "."), ",")
	for i := range listed {
		listed[i] = strings.TrimSpace(listed[i])
	}
	want := make([]string, len(enum))
	for i, name := range enum {
		want[i] = name.(string)
	}
	assert.ElementsMatch(t, want, listed, "runtime enum and tool schema cannot drift")
}
