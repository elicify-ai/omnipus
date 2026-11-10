package systools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const selectionRegistryPublic = "WU4-SELECTION-REGISTRY-PUBLIC"
const selectionRegistryProtected = "WU4-SELECTION-REGISTRY-REJECTED-PLAINTEXT"

type selectionRegistryEcho struct {
	tools.BaseTool
	t     *testing.T
	calls int
}

func (*selectionRegistryEcho) Name() string           { return "selection_registry_public_echo" }
func (*selectionRegistryEcho) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (*selectionRegistryEcho) Description() string    { return "Public non-secret registry log control" }
func (*selectionRegistryEcho) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"label": map[string]any{"type": "string"}}, "required": []string{"label"}}
}
func (tool *selectionRegistryEcho) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	tool.calls++
	label, ok := args["label"].(string)
	require.True(tool.t, ok, "public registry control label must be a string")
	return tools.NewToolResult(label)
}

// T2/Q2-A: invoke the real registry DIRECTLY, not through native turn
// normalization. The logger's own argument event must show a public exact
// argument control and no protected input, independently of the return value.
func TestWU4Selection_DirectRegistryRefusesBeforeItsOwnArgumentLog(t *testing.T) {
	for _, name := range []string{"configure_provider", "add_mcp_server", "configure_channel"} {
		t.Run(name, func(t *testing.T) {
			f := newWU4BFixture(t)
			tool, args, _ := wu4bMatchingSelection(t, f, name)
			switch name {
			case "configure_provider":
				args["api_key"] = selectionRegistryProtected
			case "add_mcp_server":
				args["env"] = map[string]any{"TOKEN": selectionRegistryProtected}
			case "configure_channel":
				args["token"] = selectionRegistryProtected
			}
			registry := tools.NewToolRegistry()
			registry.Register(tool)
			registered, exists := registry.Get(name)
			require.True(t, exists)
			require.Same(t, tool, registered, "instrument executes the intended real integration tool")
			public := &selectionRegistryEcho{t: t}
			registry.Register(public)
			path := filepath.Join(t.TempDir(), "direct-registry.jsonl")
			level := logger.GetLevel()
			logger.SetLevel(logger.INFO)
			t.Cleanup(func() { logger.SetLevel(level) })
			require.NoError(t, logger.EnableFileLogging(path))
			t.Cleanup(logger.DisableFileLogging)
			before := f.snapshot(t)
			result := registry.ExecuteWithContext(context.Background(), name, args, "webchat", "selection-direct", nil)
			wu4bRefusal(t, result, "request a secret")
			assert.NotContains(t, result.ForLLM, selectionRegistryProtected)
			f.unchanged(t, before)

			publicResult := registry.ExecuteWithContext(context.Background(), public.Name(), map[string]any{"label": selectionRegistryPublic}, "webchat", "selection-direct", nil)
			require.False(t, publicResult.IsError)
			require.Equal(t, selectionRegistryPublic, publicResult.ForLLM)
			require.Equal(t, 1, public.calls)
			logger.DisableFileLogging() // close/flush before inspecting the real sink
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Zero(t, bytes.Count(raw, []byte(selectionRegistryProtected)), "direct registry must refuse BEFORE its first args log")
			decoder := json.NewDecoder(bytes.NewReader(raw))
			publicStarts, protectedStarts := 0, 0
			for {
				var event map[string]any
				err := decoder.Decode(&event)
				if err == io.EOF {
					break
				}
				require.NoError(t, err, "capture contains actual structured file-logger events")
				if event["message"] != "Tool execution started" {
					continue
				}
				switch event["tool"] {
				case public.Name():
					publicStarts++
					assert.Equal(t, map[string]any{"label": selectionRegistryPublic}, event["args"], "instrument captures exact ordinary args, not an artificial log marker")
				case name:
					protectedStarts++
				}
			}
			assert.Equal(t, 1, publicStarts, "empty/disabled/global-argument-scrubbed log cannot pass")
			assert.Zero(t, protectedStarts, "protected inputs must never enter the registry's args-bearing log event")
		})
	}
}
