package config

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Traces to: schedules-t10-spec-r6.md lines 724–729; TDD 10/11, D-2.1/2, FR-07.
func TestT10_SchedulesCapacityKeyIsIgnoredAndNotSeeded(t *testing.T) {
	for _, tc := range []struct{ name, schedules string }{
		{"unset", `{"retry_backoff_ms":[1700,2300]}`},
		{"zero", `{"max_concurrent_runs":0,"retry_backoff_ms":[1700,2300]}`},
		{"negative", `{"max_concurrent_runs":-3,"retry_backoff_ms":[1700,2300]}`},
		{"eight", `{"max_concurrent_runs":8,"retry_backoff_ms":[1700,2300]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			path := filepath.Join(home, "config.json")
			// A valid current-version config reaches the retired-key oracle instead
			// of failing the unrelated required-version check during fixture load.
			require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"schedules":`+tc.schedules+`}`), 0600))
			cfg, err := LoadConfig(path)
			require.NoError(t, err)
			data, err := json.Marshal(cfg)
			require.NoError(t, err)
			var wire map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(data, &wire))
			assert.NotContains(t, string(wire["schedules"]), "max_concurrent_runs", "old unknown key must not be retained or defaulted")
			assert.Equal(t, []int64{1700, 2300}, cfg.Schedules.RetryBackoffMs, "removal must preserve the independent boot retry setting")
			_, found := reflect.TypeOf(cfg.Schedules).FieldByName("MaxConcurrentRuns")
			assert.False(t, found, "capacity field is removed, not merely ignored by one caller")
		})
	}
	seeded, err := json.Marshal(DefaultConfig())
	require.NoError(t, err)
	assert.NotContains(t, string(seeded), `"max_concurrent_runs"`, "fresh install must seed no capacity")
}

// Traces to: schedules-t10-spec-r6.md lines 724, 1662; FR-07 explicitly removes the constant.
func TestT10_NoSchedulesCapacityDefaultConstant(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	found := []string{}
	for _, name := range files {
		if filepath.Ext(name) != ".go" || len(name) >= 8 && name[len(name)-8:] == "_test.go" {
			continue
		}
		f, parseErr := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		require.NoError(t, parseErr)
		ast.Inspect(f, func(n ast.Node) bool {
			if v, ok := n.(*ast.ValueSpec); ok {
				for _, id := range v.Names {
					if id.Name == "DefaultSchedulesMaxConcurrentRuns" {
						found = append(found, name)
					}
				}
			}
			return true
		})
	}
	assert.Empty(t, found, "deleted fixed-capacity default remains in production declarations")
}
