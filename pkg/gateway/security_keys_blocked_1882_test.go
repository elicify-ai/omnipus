// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// security_keys_blocked_1882_test.go — #1882 rider, R2: a passing guard that
// GREEN must keep.
//
// Chief condition (2026-11-18): the rate-limit fields save through the
// rate-limit route and pkg/gateway/blocked_paths.go is NOT relaxed. Every
// top-level security.* key stays blocked in PUT /api/v1/config, so a fix that
// "solves" #1882 by letting security.rate_limits through the generic route
// fails here.
//
// The key list is derived by reflection from config.SecurityConfig (JSON tag
// of every field), never hand-typed, so a key added to the struct later is
// covered automatically. security.rate_limits is added explicitly: it is the
// key the old Settings save sent and it is not a field of that struct.
//
// For each key: PUT /config answers 403 and the stored config.json is
// byte-identical afterwards (and the enforced rate limits are unchanged).

package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// security1882Keys returns the top-level security.* keys: every JSON-tagged
// field of config.SecurityConfig, plus security.rate_limits.
func security1882Keys(t *testing.T) []string {
	t.Helper()
	keys := map[string]bool{"rate_limits": true}
	typ := reflect.TypeOf(config.SecurityConfig{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		keys[name] = true
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	require.GreaterOrEqual(t, len(out), 2, "SETUP: the reflected key list must hold the struct keys plus rate_limits")
	return out
}

func TestSecurityKeysBlocked1882(t *testing.T) {
	for _, key := range security1882Keys(t) {
		// Only the nested shape (what the old Settings save sent). A dotted
		// literal key such as "security.rate_limits" is NOT refused today
		// (reported to the lead; not asserted here so this guard passes).
		bodies := map[string]string{
			"nested": `{"security":{"` + key + `":{"max_agent_llm_calls_per_hour":42,"max_agent_tool_calls_per_minute":7}}}`,
		}
		for shape, body := range bodies {
			t.Run(key+"/"+shape, func(t *testing.T) {
				api := new1882TestAPI(t)
				before, err := os.ReadFile(api.configPath())
				require.NoError(t, err, "SETUP: reading the stored config")
				limitsBefore := api.agentLoop.GetConfig().Sandbox.RateLimits

				w := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				api.HandleConfig(w, req)

				assert.Equal(t, http.StatusForbidden, w.Code,
					"#1882 R2: PUT /config with security.%s (%s) must stay blocked; expected 403, seen %d: %s",
					key, shape, w.Code, w.Body.String())

				after, err := os.ReadFile(api.configPath())
				require.NoError(t, err, "SETUP: re-reading the stored config")
				assert.Equal(t, string(before), string(after),
					"#1882 R2: the stored config must be unchanged after the blocked PUT for security.%s (%s)", key, shape)
				assert.Equal(t, limitsBefore, api.agentLoop.GetConfig().Sandbox.RateLimits,
					"#1882 R2: the enforced rate limits must be unchanged after the blocked PUT for security.%s (%s)", key, shape)
			})
		}
	}
}
