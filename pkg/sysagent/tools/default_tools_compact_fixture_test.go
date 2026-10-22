package systools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

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
	panic("compact inventory must not return parameter schemas")
}
func (m defaultToolsPreviewMetadata) Execute(context.Context, map[string]any) *tools.ToolResult {
	panic("compact inventory must not execute listed tools")
}

func defaultToolsWirePreviewCatalog(t *testing.T, deps *systools.Deps) []string {
	t.Helper()
	names := []string{"read_file", "bash", "write_file", "ToolSearch", "browser_handover"}
	var metadata []tools.Tool
	for _, name := range names {
		metadata = append(metadata, defaultToolsPreviewMetadata{name: name})
	}
	// Keep the baseline compiling so RED is a real output assertion, not a missing-field error.
	field := reflect.ValueOf(deps).Elem().FieldByName("ToolCatalog")
	if field.IsValid() {
		callback := reflect.ValueOf(func() []tools.Tool { return metadata })
		require.True(t, callback.Type().AssignableTo(field.Type()))
		field.Set(callback)
	}
	sort.Strings(names)
	return names
}

func defaultToolsParsePreview(t *testing.T, raw, kind string, names []string) map[string]string {
	t.Helper()
	assert.False(t, json.Valid([]byte(raw)), "SPEC.md compact output replaces the verbose JSON blocks")
	column := "worker"
	if kind == "Main" {
		column = "main"
	}
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	require.Equal(t, "preview: "+kind+" | kind: "+column, lines[0], "SPEC.md compact preview identity")
	require.Len(t, lines, len(names)+1, "SPEC.md no id/revision or extra metadata sections in preview")
	policies := map[string]string{}
	var actualNames []string
	for _, line := range lines[1:] {
		parts := strings.SplitN(line, " | ", 3)
		require.Len(t, parts, 3)
		assert.Equal(t, "Short description.", parts[2], "SPEC.md first sentence only; no schemas or long descriptions")
		policies[parts[0]] = parts[1]
		actualNames = append(actualNames, parts[0])
	}
	assert.Equal(t, names, actualNames, "SPEC.md every catalog entry, name-sorted, including deny")
	return policies
}

func defaultToolsWireCatalog(t *testing.T, deps *systools.Deps, metadata []tools.Tool) {
	t.Helper()
	field := reflect.ValueOf(deps).Elem().FieldByName("ToolCatalog")
	if field.IsValid() {
		callback := reflect.ValueOf(func() []tools.Tool { return metadata })
		require.True(t, callback.Type().AssignableTo(field.Type()))
		field.Set(callback)
	}
}

// SPEC.md compact output replaces only the read-back carrier. Existing
// identity, exact-name lookup, permissions and mutation error checks stay real.
func defaultToolsExistingBody(t *testing.T, deps *systools.Deps, id string, args map[string]any, extra ...tools.Tool) (string, map[string]string) {
	t.Helper()
	metadata := append(systools.AllTools(deps), tools.GeneralBuiltinMetadata()...)
	metadata = append(metadata, extra...)
	defaultToolsWireCatalog(t, deps, metadata)
	result := systools.NewAgentGetToolsTool(deps).Execute(context.Background(), args)
	require.False(t, result.IsError, "compact read failed: %s", result.ForLLM)
	state, err := agentstore.New(deps.Home).ReadState(id)
	require.NoError(t, err)
	header := fmt.Sprintf("agent: %s | name: %s | kind: main | revision: %s", id, state.Agent.Name, state.Revision)
	policies := defaultToolsCompactPolicies(t, result.ForLLM, header)
	return result.ForLLM, policies
}

func defaultToolsCompactPolicies(t *testing.T, raw, header string) map[string]string {
	t.Helper()
	require.False(t, json.Valid([]byte(raw)), "SPEC.md compact output must not retain JSON blocks")
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	require.Equal(t, header, lines[0], "SPEC.md exact identity header")
	result := map[string]string{}
	var names []string
	for _, line := range lines[1:] {
		parts := strings.SplitN(line, " | ", 3)
		require.Len(t, parts, 3, "SPEC.md tool row has exactly three fields")
		assert.Contains(t, []string{"allow", "ask", "deny"}, parts[1])
		_, duplicate := result[parts[0]]
		assert.False(t, duplicate, "one row per tool")
		result[parts[0]] = parts[1]
		names = append(names, parts[0])
	}
	expected := append([]string{}, names...)
	sort.Strings(expected)
	assert.Equal(t, expected, names, "SPEC.md name-sorted inventory")
	return result
}
