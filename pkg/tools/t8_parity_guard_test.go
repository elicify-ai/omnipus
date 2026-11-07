package tools_test

// Oracle: tool-parity r17 §7.1 and scenarios 13.1–13.6. The recursive
// instrument is test-owned; its adversarial fixtures do not mutate production.
// Its fixture PASS is NOT proof that production parity/rule tables exist.
import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type t8ParityRow struct{ side, path, operation, reason string }

func t8JSONLeaves(typ reflect.Type, prefix string) []string {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	// A time value marshals to one JSON string: it is a leaf, not a struct with
	// no exported fields (which would make REST due, started_at and
	// completed_at invisible to the instrument).
	if typ == reflect.TypeFor[time.Time]() {
		return []string{prefix}
	}
	switch typ.Kind() {
	case reflect.Struct:
		var leaves []string
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag == "-" {
				continue
			}
			if tag == "" {
				tag = f.Name
			}
			path := tag
			if prefix != "" {
				path = prefix + "." + tag
			}
			leaves = append(leaves, t8JSONLeaves(f.Type, path)...)
		}
		return leaves
	case reflect.Slice, reflect.Array:
		return t8JSONLeaves(typ.Elem(), prefix+"[]")
	case reflect.Map:
		return t8JSONLeaves(typ.Elem(), prefix+"{}")
	default:
		return []string{prefix}
	}
}
func t8SchemaLeaves(schema map[string]any, prefix string) []string {
	if props, ok := schema["properties"].(map[string]any); ok {
		var leaves []string
		for key, value := range props {
			child, ok := value.(map[string]any)
			if !ok {
				panic("malformed schema fixture: " + key)
			}
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			leaves = append(leaves, t8SchemaLeaves(child, path)...)
		}
		if extra, ok := schema["additionalProperties"].(map[string]any); ok {
			leaves = append(leaves, t8SchemaLeaves(extra, prefix+"{}")...)
		}
		return leaves
	}
	if items, ok := schema["items"].(map[string]any); ok {
		return t8SchemaLeaves(items, prefix+"[]")
	}
	if extra, ok := schema["additionalProperties"].(map[string]any); ok {
		return t8SchemaLeaves(extra, prefix+"{}")
	}
	return []string{prefix}
}
func t8Unmapped(side, operation string, paths []string, mappings []t8ParityRow, excluded []t8ParityRow) []string {
	var errors []string
	for _, path := range paths {
		mapped := false
		for _, row := range mappings {
			if row.side == side && row.path == path && (row.operation == operation || row.operation == "both") {
				mapped = true
			}
		}
		for _, row := range excluded {
			if row.side == side && row.path == path && (row.operation == operation || row.operation == "both") {
				if strings.TrimSpace(row.reason) == "" {
					errors = append(errors, fmt.Sprintf("%s %s %s: exclusion requires a reason", side, operation, path))
				} else {
					mapped = true
				}
			}
		}
		if !mapped {
			errors = append(errors, fmt.Sprintf("unmapped %s %s %s", side, operation, path))
		}
	}
	sort.Strings(errors)
	return errors
}

func TestParityGuard_RecursiveMutationFixtures(t *testing.T) {
	// These are intentionally synthetic fixture types for the guard's own
	// self-test, NOT additional cross-boundary request types.
	type executor struct {
		Env map[string]string `json:"env_overrides"`
	}
	type fallback struct {
		Model string `json:"model"`
	}
	type base struct {
		Title     string     `json:"title"`
		Executor  *executor  `json:"executor"`
		Fallbacks []fallback `json:"fallback_models"`
	}
	want := []string{"executor.env_overrides{}", "fallback_models[].model", "title"}
	// Time-typed fields are single leaves, plain or optional, at any depth.
	type stamped struct {
		Due       *time.Time `json:"due,omitempty"`
		StartedAt time.Time  `json:"started_at"`
		Nested    *struct {
			CompletedAt *time.Time `json:"completed_at"`
		} `json:"nested"`
		Marks []time.Time `json:"marks"`
	}
	stampedPaths := t8JSONLeaves(reflect.TypeFor[stamped](), "")
	sort.Strings(stampedPaths)
	require.Equal(t, []string{"due", "marks[]", "nested.completed_at", "started_at"}, stampedPaths, "time fields must be counted as leaves")
	paths := t8JSONLeaves(reflect.TypeFor[base](), "")
	sort.Strings(paths)
	require.Equal(t, want, paths)
	schema := map[string]any{"properties": map[string]any{
		"title":           map[string]any{"type": "string"},
		"executor":        map[string]any{"properties": map[string]any{"env_overrides": map[string]any{"additionalProperties": map[string]any{"type": "string"}}}},
		"fallback_models": map[string]any{"items": map[string]any{"properties": map[string]any{"model": map[string]any{"type": "string"}}}},
	}}
	toolPaths := t8SchemaLeaves(schema, "")
	sort.Strings(toolPaths)
	require.Equal(t, want, toolPaths)
	rows := make([]t8ParityRow, 0, len(want)*2)
	for _, side := range []string{"REST", "tool"} {
		for _, path := range want {
			rows = append(rows, t8ParityRow{side: side, path: path, operation: "both"})
		}
	}
	require.Empty(t, t8Unmapped("REST", "create", paths, rows, nil), "known complete baseline")
	require.Empty(t, t8Unmapped("tool", "update", toolPaths, rows, nil), "schema baseline")
	mutations := []struct {
		name, side, path string
		typ              reflect.Type
	}{
		{"13.1 new top-level", "REST", "zz_probe", reflect.TypeFor[struct {
			New string `json:"zz_probe"`
		}]()},
		{"13.2 new nested executor", "REST", "executor.zz_probe", reflect.TypeFor[struct {
			Executor *struct {
				New string `json:"zz_probe"`
			} `json:"executor"`
		}]()},
		{"13.3 new fallback member", "REST", "fallback_models[].zz_probe", reflect.TypeFor[struct {
			Fallbacks []struct {
				New string `json:"zz_probe"`
			} `json:"fallback_models"`
		}]()},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			// The nested fixture intentionally checks JSON tags; it is not a string
			// inserted directly into the path comparison.
			got := t8JSONLeaves(m.typ, "")
			require.Equal(t, []string{m.path}, got)
			assert.Equal(t, []string{"unmapped REST update " + m.path}, t8Unmapped(m.side, "update", got, rows, nil))
		})
	}
	t.Run("13.4 tool-only parameter", func(t *testing.T) {
		mutant := map[string]any{"properties": map[string]any{"tool_only": map[string]any{"type": "boolean"}}}
		assert.Equal(t, []string{"unmapped tool create tool_only"}, t8Unmapped("tool", "create", t8SchemaLeaves(mutant, ""), rows, nil))
	})
	t.Run("exclusions require reason and operation", func(t *testing.T) {
		excluded := []t8ParityRow{{side: "REST", path: "server_only", operation: "create", reason: "server provenance"}}
		assert.Empty(t, t8Unmapped("REST", "create", []string{"server_only"}, nil, excluded))
		assert.Equal(t, []string{"unmapped REST update server_only"}, t8Unmapped("REST", "update", []string{"server_only"}, nil, excluded))
		excluded[0].reason = " "
		assert.Equal(t, []string{"REST create server_only: exclusion requires a reason", "unmapped REST create server_only"}, t8Unmapped("REST", "create", []string{"server_only"}, nil, excluded))
	})
}
