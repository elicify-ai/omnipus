package systools_test

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type defaultToolsPreviewMetadata struct {
	tools.BaseTool
	name string
}

func (m defaultToolsPreviewMetadata) Name() string { return m.name }
func (m defaultToolsPreviewMetadata) Description() string {
	return "Short description. Omit this second sentence."
}
func (m defaultToolsPreviewMetadata) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (m defaultToolsPreviewMetadata) Parameters() map[string]any {
	panic("tool inventory must not return parameter schemas")
}
func (m defaultToolsPreviewMetadata) Execute(context.Context, map[string]any) *tools.ToolResult {
	panic("tool inventory must not execute listed tools")
}

func defaultToolsWirePreviewCatalog(t *testing.T, deps *systools.Deps) []string {
	t.Helper()
	names := []string{"read_file", "bash", "write_file", "ToolSearch", "browser_handover"}
	metadata := make([]tools.Tool, 0, len(names))
	for _, name := range names {
		metadata = append(metadata, defaultToolsPreviewMetadata{name: name})
	}
	deps.ToolCatalog = func() []tools.Tool { return metadata }
	sort.Strings(names)
	return names
}

// defaultToolsEntries decodes a get_agent tool_permissions result (r17
// 7.12.1): the identity keys and the {tool, effective, override,
// description?} entries. Any other top-level key is an error: the result
// carries the identity and the requested properties, nothing else.
func defaultToolsEntries(t *testing.T, raw string) (map[string]any, []map[string]any) {
	t.Helper()
	require.True(t, json.Valid([]byte(raw)), "get_agent returns one JSON object, never the retired compact text")
	var object map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &object))
	identity := map[string]any{}
	var entries []map[string]any
	for key, value := range object {
		switch key {
		case "id", "revision", "preview_of":
			identity[key] = value
		case "tool_permissions":
			list, ok := value.([]any)
			require.True(t, ok, "tool_permissions is an array")
			for _, item := range list {
				entry, ok := item.(map[string]any)
				require.True(t, ok)
				entries = append(entries, entry)
			}
		default:
			t.Errorf("unexpected top-level key %q: only identity and the requested property may be returned", key)
		}
	}
	return identity, entries
}

func defaultToolsParsePreview(t *testing.T, raw, kind string, names []string) map[string]string {
	t.Helper()
	identity, entries := defaultToolsEntries(t, raw)
	assert.Equal(t, map[string]any{"preview_of": kind}, identity, "r17 7.12.1: a preview has preview_of and no id or revision")
	require.Len(t, entries, len(names), "every catalog entry appears once")
	policies := map[string]string{}
	actualNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		name, _ := entry["tool"].(string)
		effective, _ := entry["effective"].(string)
		if effective == "deny" {
			assert.NotContains(t, entry, "description", "deny entries carry no description")
		} else {
			assert.Equal(t, "Short description.", entry["description"], "first sentence only; no schemas or long descriptions")
		}
		policies[name] = effective
		actualNames = append(actualNames, name)
	}
	assert.Equal(t, names, actualNames, "every catalog entry, name-sorted, including deny")
	return policies
}

func defaultToolsWireCatalog(t *testing.T, deps *systools.Deps, metadata []tools.Tool) {
	t.Helper()
	deps.ToolCatalog = func() []tools.Tool { return metadata }
}

// defaultToolsExistingBody reads an existing agent's tool_permissions through
// the real get_agent. Identity, exact-name lookup, permissions and mutation
// error checks stay real; args carries the selector (id or name).
func defaultToolsExistingBody(t *testing.T, deps *systools.Deps, id string, args map[string]any, extra ...tools.Tool) (string, map[string]string) {
	t.Helper()
	metadata := append(systools.AllTools(deps), tools.GeneralBuiltinMetadata()...)
	metadata = append(metadata, extra...)
	defaultToolsWireCatalog(t, deps, metadata)
	call := map[string]any{"fields": []any{"tool_permissions"}}
	for k, v := range args {
		call[k] = v
	}
	result := systools.NewAgentGetTool(deps).Execute(context.Background(), call)
	require.False(t, result.IsError, "get_agent read failed: %s", result.ForLLM)
	state, err := agentstore.New(deps.Home).ReadState(id)
	require.NoError(t, err)
	policies := defaultToolsCompactPolicies(t, result.ForLLM, map[string]any{"id": id, "revision": state.Revision})
	return result.ForLLM, policies
}

// defaultToolsCompactPolicies checks the inventory contract and returns tool -> effective.
func defaultToolsCompactPolicies(t *testing.T, raw string, identity map[string]any) map[string]string {
	t.Helper()
	gotIdentity, entries := defaultToolsEntries(t, raw)
	require.Equal(t, identity, gotIdentity, "exact identity keys")
	result := map[string]string{}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name, _ := entry["tool"].(string)
		effective, _ := entry["effective"].(string)
		assert.Contains(t, []string{"allow", "ask", "deny"}, effective)
		description, hasDescription := entry["description"].(string)
		if effective == "deny" {
			assert.False(t, hasDescription, "deny entries carry no description")
		} else {
			require.True(t, hasDescription, "allow/ask entries include a description")
			assert.LessOrEqual(t, utf8.RuneCountInString(description), 40)
			assert.Equal(t, strings.Join(strings.Fields(description), " "), description)
		}
		_, duplicate := result[name]
		assert.False(t, duplicate, "one entry per tool")
		result[name] = effective
		names = append(names, name)
	}
	assert.True(t, slices.IsSorted(names), "name-sorted inventory")
	return result
}
