package systools_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type wu25Catalog struct {
	field  string
	values []string
}

// Oracles are parsed from the authoritative YAML catalogs, not the tool's
// validator, coreagent.Canonical*, or a copy of successful runtime output.
func wu25Catalogs(t *testing.T) []wu25Catalog {
	t.Helper()
	catalogs := make([]wu25Catalog, 0, 3)
	for _, spec := range []struct{ field, file string }{
		{"figure", "AgentFigure.yaml"}, {"role", "AgentRole.yaml"}, {"color", "AgentColor.yaml"},
	} {
		path, err := filepath.Abs(filepath.Join("..", "..", "..", "contracts", "components", "schemas", spec.file))
		require.NoError(t, err)
		data, err := os.ReadFile(path)
		require.NoError(t, err, "read the real %s catalog", spec.field)
		var schema struct {
			Type     string   `yaml:"type"`
			Nullable bool     `yaml:"nullable"`
			Enum     []string `yaml:"enum"`
		}
		require.NoError(t, yaml.Unmarshal(data, &schema))
		require.Equal(t, "string", schema.Type)
		require.False(t, schema.Nullable)
		require.NotEmpty(t, schema.Enum, "a missing catalog cannot silently make a zero-case green")
		seen := map[string]bool{}
		for _, value := range schema.Enum {
			require.NotEmpty(t, value)
			require.False(t, seen[value], "catalog contains duplicate %q", value)
			seen[value] = true
		}
		catalogs = append(catalogs, wu25Catalog{field: spec.field, values: schema.Enum})
	}
	return catalogs
}

func wu25CatalogFor(t *testing.T, field string) wu25Catalog {
	t.Helper()
	for _, catalog := range wu25Catalogs(t) {
		if catalog.field == field {
			return catalog
		}
	}
	t.Fatalf("missing test oracle for %s", field)
	return wu25Catalog{}
}

func wu25Function(t *testing.T, registry *tools.ToolRegistry, name string) map[string]any {
	t.Helper()
	for _, definition := range registry.GetDefinitions() {
		fn, ok := definition["function"].(map[string]any)
		require.True(t, ok, "model-facing definition must contain function object")
		if fn["name"] == name {
			// Normalize schema containers as the provider's JSON transport does:
			// []string and []any both become ordinary JSON arrays.
			data, err := json.Marshal(fn)
			require.NoError(t, err)
			var normalized map[string]any
			require.NoError(t, json.Unmarshal(data, &normalized))
			return normalized
		}
	}
	t.Fatalf("BLOCKED: %s absent from model-facing registry contract — required by WU-25/#1303", name)
	return nil
}

func wu25Properties(t *testing.T, fn map[string]any) map[string]any {
	t.Helper()
	parameters, ok := fn["parameters"].(map[string]any)
	require.True(t, ok, "model-facing parameters must be an object")
	props, ok := parameters["properties"].(map[string]any)
	require.True(t, ok, "model-facing properties must be an object")
	// Instrument check: both mutation tools have a name field, update also id.
	_, hasName := props["name"]
	require.True(t, hasName, "property search must see a known-present control")
	return props
}

func wu25Descriptors(t *testing.T, body map[string]any) map[string]map[string]any {
	t.Helper()
	items, ok := body["editable_fields"].([]any)
	require.True(t, ok, "editable_fields must be a real, nonempty descriptor array")
	require.NotEmpty(t, items)
	out := map[string]map[string]any{}
	for _, item := range items {
		descriptor, object := item.(map[string]any)
		require.True(t, object)
		name, isString := descriptor["name"].(string)
		require.True(t, isString)
		require.NotEmpty(t, name)
		_, duplicate := out[name]
		require.False(t, duplicate, "duplicate descriptor for %s", name)
		_, isBool := descriptor["editable"].(bool)
		require.True(t, isBool, "descriptor %s must state a boolean editability", name)
		out[name] = descriptor
	}
	_, hasModel := out["model"]
	require.True(t, hasModel, "descriptor search must see its known-present control")
	return out
}

// A token match cannot confuse role "data" with prose "metadata", nor figure
// "Man" with "Woman". Presence is pinned, not ordering or prose wording.
func wu25ChoiceTokens(text string) map[string]bool {
	pattern := regexp.MustCompile(`#[0-9A-Fa-f]{6}|[A-Za-z][A-Za-z0-9_-]*`)
	tokens := map[string]bool{}
	for _, token := range pattern.FindAllString(text, -1) {
		tokens[token] = true
	}
	return tokens
}
