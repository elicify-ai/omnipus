package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-088 / chief 18:40Z ruling: named missing entries return absent inside the
// existing list carrier. Unfiltered enumeration still lists configured only.
func TestWU3SharedReadToolsExposeAbsentTokenWithoutInventingEntry(t *testing.T) {
	for _, kind := range []string{"provider", "mcp"} {
		t.Run(kind, func(t *testing.T) {
			d, _ := wu3Deps(t)
			var tool tools.Tool = systools.NewProviderListTool(d)
			array, name := "providers", "ollama"
			if kind == "mcp" {
				tool = systools.NewMCPListTool(d)
				array, name = "servers", "missing-mcp"
			}
			body := wu3Success(t, tool.Execute(context.Background(), map[string]any{"name": name}))
			assert.Equal(t, []any{map[string]any{"name": name, "revision": "absent"}}, body[array], "exact missing resource projection")
			unfiltered := wu3Success(t, tool.Execute(context.Background(), map[string]any{}))
			assert.Equal(t, []any{}, unfiltered[array], "a missing read must not configure a phantom")
		})
	}
}

func TestWU3SharedReadNameSelectsExactlyOneExistingEntry(t *testing.T) {
	for _, kind := range []string{"provider", "mcp"} {
		t.Run(kind, func(t *testing.T) {
			d, cfg := wu3Deps(t)
			cfg.Providers = []*config.ModelConfig{{Provider: "ollama", Model: "one"}, {Provider: "openai", Model: "two"}}
			cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{"one": {Type: "stdio", Command: "inert-one"}, "two": {Type: "stdio", Command: "inert-two"}}
			var tool tools.Tool = systools.NewProviderListTool(d)
			array, name := "providers", "ollama"
			if kind == "mcp" {
				tool = systools.NewMCPListTool(d)
				array, name = "servers", "one"
			}
			body := wu3Success(t, tool.Execute(context.Background(), map[string]any{"name": name}))
			items, ok := body[array].([]any)
			require.True(t, ok)
			require.Len(t, items, 1)
			row := wu3Object(t, items[0])
			assert.Equal(t, name, row["name"])
			token, ok := row["revision"].(string)
			require.True(t, ok)
			require.NotEmpty(t, token)
			assert.NotEqual(t, "absent", token)
		})
	}
}
