package gateway

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is catalog INPUT, not a mock of the formatter. The real get_agent_tools
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

// The new typed metadata seam is absent at c90310f1f. Reflection keeps the RED
// pack compiling on that base, so it can assert on the real old JSON output.
// Once implemented, the SAME tests supply real catalog inputs through the seam.
func defaultToolsInstallCatalog(t *testing.T, deps *systools.Deps, catalog []tools.Tool) {
	t.Helper()
	field := reflect.ValueOf(deps).Elem().FieldByName("ToolCatalog")
	if field.IsValid() {
		callback := reflect.ValueOf(func() []tools.Tool { return catalog })
		require.True(t, callback.Type().AssignableTo(field.Type()), "ToolCatalog must accept the agreed read-only metadata callback")
		field.Set(callback)
	} else {
		t.Log("BASELINE: ToolCatalog seam absent; asserting compact contract against the real current reader")
	}
}

func defaultToolsInventory(t *testing.T, home string, cfg *config.Config, catalog []tools.Tool, args map[string]any) string {
	t.Helper()
	deps := &systools.Deps{Home: home, GetCfg: func() *config.Config { return cfg }}
	defaultToolsInstallCatalog(t, deps, catalog)
	result := systools.NewAgentGetToolsTool(deps).Execute(context.Background(), args)
	require.NotNil(t, result)
	require.False(t, result.IsError, "get_agent_tools must return the required inventory: %s", result.ForLLM)
	characters := utf8.RuneCountInString(result.ForLLM)
	// Approximate tokens use the explicit characters/4 heuristic, not a model tokenizer.
	t.Logf("INVENTORY_SIZE bytes=%d characters=%d approx_tokens_char4=%d lines=%d requested=%v", len(result.ForLLM), characters, (characters+3)/4, strings.Count(strings.TrimSuffix(result.ForLLM, "\n"), "\n")+1, args)
	return result.ForLLM
}

func defaultToolsParseCompact(t *testing.T, raw, header string, catalog []tools.Tool) map[string]string {
	t.Helper()
	assert.False(t, json.Valid([]byte(raw)), "SPEC.md compact output is plain text, never a JSON metadata object")
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	require.Equal(t, header, lines[0], "SPEC.md compact identity header")
	require.Len(t, lines, len(catalog)+1, "exactly one header and one row per catalog tool; no extra sections")
	policies := make(map[string]string, len(catalog))
	var names []string
	for _, line := range lines[1:] {
		parts := strings.SplitN(line, " | ", 3)
		require.Len(t, parts, 3, "SPEC.md tool row is name | policy | short: %q", line)
		require.NotEmpty(t, parts[0], "tool names cannot disappear")
		_, duplicate := policies[parts[0]]
		assert.False(t, duplicate, "each tool appears once: %s", parts[0])
		assert.Contains(t, []string{"allow", "ask", "deny"}, parts[1], "policy is an exact enum value")
		assert.Equal(t, strings.Join(strings.Fields(parts[2]), " "), parts[2], "description whitespace must collapse")
		assert.LessOrEqual(t, utf8.RuneCountInString(parts[2]), 80, "SPEC.md caps descriptions at 80 characters")
		policies[parts[0]] = parts[1]
		names = append(names, parts[0])
	}
	var expectedNames []string
	maxRunes := utf8.RuneCountInString(header) + 1
	for _, tool := range catalog {
		expectedNames = append(expectedNames, tool.Name())
		// Two ' | ' separators, longest policy 'allow', 80 description characters, newline.
		maxRunes += utf8.RuneCountInString(tool.Name()) + len(" | ")*2 + len("allow") + 80 + 1
	}
	sort.Strings(expectedNames)
	assert.Equal(t, expectedNames, names, "all catalog tools, including Deny entries, appear in byte-sorted order")
	assert.LessOrEqual(t, utf8.RuneCountInString(raw), maxRunes, "compact output must stay within its per-row size budget")
	return policies
}
