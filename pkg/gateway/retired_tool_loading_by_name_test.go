// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// C-22 row 3 by name (team-lead ruling 2026-11-28, N83): PUT /config refuses a
// body that names a retired tool-loading key with a value other than the
// stored one. A body that only drops a stored retired value, or repeats it, is
// accepted.
func TestUpdateConfig_RetiredToolLoadingKeys_ByNameOnly(t *testing.T) {
	seed := func(t *testing.T, api *restAPI, tools map[string]any) {
		t.Helper()
		path := filepath.Join(api.homePath, "config.json")
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var stored map[string]any
		require.NoError(t, json.Unmarshal(raw, &stored))
		stored["tools"] = tools
		out, err := json.Marshal(stored)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, out, 0o600))
	}
	storedTools := map[string]any{
		"manifest": map[string]any{"compressed": false, "preview_all_lazy": true},
		"mcp": map[string]any{"servers": map[string]any{
			"github": map[string]any{"enabled": false, "command": "gh-mcp", "deferred": true},
		}},
	}

	t.Run("dropping stored retired values without naming them is accepted", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		seed(t, api, storedTools)
		w := rtlPut(api, `{"tools":{"manifest":{},"mcp":{"servers":{}}}}`)
		require.Equal(t, http.StatusOK, w.Code, rtlShort(w.Body.String()))
	})

	t.Run("a round trip that names the stored values unchanged is accepted", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		seed(t, api, storedTools)
		w := rtlPut(api, `{"tools":{"manifest":{"compressed":false,"preview_all_lazy":true},`+
			`"mcp":{"servers":{"github":{"enabled":false,"command":"gh-mcp","deferred":true}}},`+
			`"read_file":{"max_read_file_size":4096}}}`)
		require.Equal(t, http.StatusOK, w.Code, rtlShort(w.Body.String()))
	})

	for _, tc := range []struct{ name, body string }{
		{"naming compressed with another value", `{"tools":{"manifest":{"compressed":true}}}`},
		{"naming preview_all_lazy with another value, case-folded", `{"Tools":{"MANIFEST":{"Preview_All_Lazy":false}}}`},
		{"naming a stored server's deferred with another value",
			`{"tools":{"mcp":{"servers":{"github":{"enabled":false,"command":"gh-mcp","deferred":false}}}}}`},
	} {
		t.Run(tc.name+" is refused and stores nothing", func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			seed(t, api, storedTools)
			before := readConfigOnDisk(t, api)
			msg := rtlRefusalMessage(t, rtlPut(api, tc.body), tc.name)
			require.Equal(t, rtlRefusalText, msg)
			require.Equal(t, before, readConfigOnDisk(t, api))
		})
	}
}
