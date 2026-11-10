package gateway

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is catalog INPUT, not a mock of the formatter. The real get_agent
// reader and policy resolver run. Parameters/Execute must never be consulted.
type defaultToolsMetadata struct {
	tools.BaseTool
	name, description string
}

func (m defaultToolsMetadata) Name() string           { return m.name }
func (m defaultToolsMetadata) Description() string    { return m.description }
func (m defaultToolsMetadata) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (m defaultToolsMetadata) Parameters() map[string]any {
	panic("compact inventory must not read parameter schemas")
}
func (m defaultToolsMetadata) Execute(context.Context, map[string]any) *tools.ToolResult {
	panic("compact inventory must not execute a catalog tool")
}

// defaultToolsInstallCatalog supplies the read-only tool metadata callback of
// the real get_agent reader (spec r17 7.12.1: tool_permissions is built from
// this catalog).
func defaultToolsInstallCatalog(t *testing.T, deps *systools.Deps, catalog []tools.Tool) {
	t.Helper()
	require.NotNil(t, deps)
	deps.ToolCatalog = func() []tools.Tool { return catalog }
}

// defaultToolsInventory runs the real get_agent reader for fields
// ["tool_permissions"] (r17 7.12: it replaced get_agent_tools) and returns its
// JSON result. args carries the selector: id, name or new_agent_type.
func defaultToolsInventory(t *testing.T, home string, cfg *config.Config, catalog []tools.Tool, args map[string]any) string {
	t.Helper()
	deps := &systools.Deps{Home: home, GetCfg: func() *config.Config { return cfg }}
	defaultToolsInstallCatalog(t, deps, catalog)
	call := map[string]any{"fields": []any{"tool_permissions"}}
	for k, v := range args {
		call[k] = v
	}
	result := systools.NewAgentGetTool(deps).Execute(context.Background(), call)
	require.NotNil(t, result)
	require.False(t, result.IsError, "get_agent must return the required inventory: %s", result.ForLLM)
	characters := utf8.RuneCountInString(result.ForLLM)
	// Approximate tokens use the explicit characters/4 heuristic, not a model tokenizer.
	t.Logf("INVENTORY_SIZE bytes=%d characters=%d approx_tokens_char4=%d requested=%v", len(result.ForLLM), characters, (characters+3)/4, args)
	return result.ForLLM
}

// defaultToolsEntry is one tool_permissions entry: {tool, effective, override, description?}.
type defaultToolsEntry struct {
	Tool        string  `json:"tool"`
	Effective   string  `json:"effective"`
	Override    *string `json:"override"`
	Description *string `json:"description"`
}

// defaultToolsDecode parses a get_agent tool_permissions result: the identity
// keys (an existing agent: id and revision; a preview: preview_of) plus the
// inventory entries. It also proves the result carries nothing else.
func defaultToolsDecode(t *testing.T, raw string) (map[string]any, []defaultToolsEntry) {
	t.Helper()
	require.True(t, json.Valid([]byte(raw)), "r17 7.12.1: get_agent returns one JSON object, never the retired compact text")
	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &object))
	identity := map[string]any{}
	for _, key := range []string{"id", "revision", "preview_of"} {
		if value, ok := object[key]; ok {
			var decoded any
			require.NoError(t, json.Unmarshal(value, &decoded))
			identity[key] = decoded
		}
	}
	var entries []defaultToolsEntry
	require.Contains(t, object, "tool_permissions", "the requested property must be present")
	require.NoError(t, json.Unmarshal(object["tool_permissions"], &entries))
	for key := range object {
		assert.Contains(t, []string{"id", "revision", "preview_of", "tool_permissions", "connectors"}, key,
			"r17 7.12.1: the result carries the identity keys and the requested properties, nothing else")
	}
	return identity, entries
}

// defaultToolsParseInventory checks the inventory contract of r17 7.12.1 and
// returns tool -> effective policy. identity is the expected identity keys:
// {"id","revision"} for an existing agent or {"preview_of"} for a preview.
func defaultToolsParseInventory(t *testing.T, raw string, identity map[string]any, catalog []tools.Tool) map[string]string {
	t.Helper()
	gotIdentity, entries := defaultToolsDecode(t, raw)
	assert.Equal(t, identity, gotIdentity, "r17 7.12.1 identity keys")
	require.Len(t, entries, len(catalog), "exactly one entry per catalog tool; denied tools are kept")
	policies := make(map[string]string, len(entries))
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		require.NotEmpty(t, entry.Tool, "tool names cannot disappear")
		_, duplicate := policies[entry.Tool]
		assert.False(t, duplicate, "each tool appears once: %s", entry.Tool)
		assert.Contains(t, []string{"allow", "ask", "deny"}, entry.Effective, "policy is an exact enum value")
		if entry.Effective == "deny" {
			// r17 7.12.1: description is omitted on deny.
			assert.Nil(t, entry.Description, "denied tool %s must not include a description", entry.Tool)
		} else {
			// r17 7.12.1: Allow/Ask keep a normalized first-sentence description of at most 40 characters.
			require.NotNil(t, entry.Description, "permitted tool %s must include its short description", entry.Tool)
			assert.Equal(t, strings.Join(strings.Fields(*entry.Description), " "), *entry.Description, "description whitespace must collapse")
			assert.LessOrEqual(t, utf8.RuneCountInString(*entry.Description), 40, "descriptions are capped at 40 characters")
		}
		policies[entry.Tool] = entry.Effective
		names = append(names, entry.Tool)
	}
	expectedNames := make([]string, 0, len(catalog))
	for _, tool := range catalog {
		expectedNames = append(expectedNames, tool.Name())
	}
	slices.Sort(expectedNames)
	assert.Equal(t, expectedNames, names, "all catalog tools, including deny entries, appear sorted by name")
	return policies
}
