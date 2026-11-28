// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// RED tests — Skills & Tools redesign (#1963, epic #1519), C-22 row 3.
// PUT /api/v1/config refuses a change to any of the retired tool-loading
// keys, however the write reaches them.
//
// The retired keys are exactly three (skills-tools-redesign-spec.md, C-22
// row 3 "Moves the guard", C-21 row "retired manifest switches", the last
// bullet of C-6's "Starting levels", BDD S-44):
//
//   - tools.manifest.compressed
//   - tools.manifest.preview_all_lazy
//   - tools.mcp.servers.<id>.deferred
//
// C-22 row 3: "`set_config` and `PUT /config` refuse `tools.manifest.compressed`,
// `tools.manifest.preview_all_lazy` and `tools.mcp.servers.<id>.deferred` with
// 'This setting was replaced by levels in Skills & Tools.'; the Settings
// entries for `tools.manifest.compressed` ... are removed in the same change,
// so the key is never unguarded." C-21: the refusal is of "a change to them",
// and it replaces the Settings guard "which name[s] Settings → Limits"; C-22
// also drops `tools_on_demand` from `PUT /performance`, so that endpoint is no
// longer a place a retired key can be changed — a refusal that sends the
// caller there is a dead end. C-6: "stored values stay in place, ignored".
//
// What the spec leaves open, named rather than guessed:
//   - the HTTP status of the refusal (the neighbouring guards answer 400 for
//     an effect refusal and 403 for a blocked path) — these tests accept any
//     4xx and pin the text and the stored state;
//   - "a change": the by-effect function below reads it as a change to the
//     stored value, so a body that repeats the stored values is NOT a change
//     (the whole-config round trip of C-6's "stored values stay in place"
//     keeps working) and a body that replaces a section and thereby drops a
//     stored retired value IS one. That is the strict reading; it is a
//     separate function so it can be dropped without touching the first.
//
// The refusal must also store nothing: config.json is compared before and
// after, as the neighbouring blocked-path tests do. updateConfig is called
// directly (the admin middleware is orthogonal), like blocked_paths_test.go.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// rtlRefusalText is C-22 row 3's refusal text, character for character.
const rtlRefusalText = "This setting was replaced by levels in Skills & Tools."

// rtlSeedTools rewrites the fixture's config.json so its "tools" section is
// toolsJSON — the stored state an upgraded install carries (C-6: stored values
// stay in place).
func rtlSeedTools(t *testing.T, api *restAPI, toolsJSON string) {
	t.Helper()
	path := filepath.Join(api.homePath, "config.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "fixture: config.json must be readable")
	var stored map[string]any
	require.NoError(t, json.Unmarshal(raw, &stored), "fixture: config.json must be valid JSON")
	var tools map[string]any
	require.NoError(t, json.Unmarshal([]byte(toolsJSON), &tools), "fixture: the seeded tools section must be a JSON object")
	stored["tools"] = tools
	out, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, out, 0o600))
}

// rtlPut sends body to PUT /api/v1/config through updateConfig.
func rtlPut(api *restAPI, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.updateConfig(w, r)
	return w
}

// rtlShort cuts a response body to its first bytes for a failure message: an
// accepted write answers with the whole config, which would bury the message.
func rtlShort(body string) string {
	const limit = 160
	if len(body) <= limit {
		return body
	}
	return body[:limit] + "... (" + strconv.Itoa(len(body)) + " bytes in all)"
}

// rtlRefusalMessage requires the response to be a refusal (any 4xx — the spec
// names no status) and returns its error message.
func rtlRefusalMessage(t *testing.T, w *httptest.ResponseRecorder, what string) string {
	t.Helper()
	require.True(t, w.Code >= http.StatusBadRequest && w.Code < http.StatusInternalServerError,
		"C-22 row 3: PUT /config must refuse %s with a client error (4xx), but it answered %d: %s",
		what, w.Code, rtlShort(w.Body.String()))
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "the refusal body must be JSON: %s", rtlShort(w.Body.String()))
	msg, _ := resp["error"].(string)
	require.NotEmpty(t, msg, "the refusal must carry an error message: %s", w.Body.String())
	return msg
}

// rtlToolsOnDisk returns the stored "tools" object.
func rtlToolsOnDisk(t *testing.T, api *restAPI) map[string]any {
	t.Helper()
	tools, ok := readConfigOnDisk(t, api)["tools"].(map[string]any)
	require.True(t, ok, "config.json must hold a tools object")
	return tools
}

// TestUpdateConfig_RefusesRetiredToolLoadingKeys — a section write whose body
// carries a retired key is refused with C-22's text and stores nothing.
// Traces to: C-22 row 3, C-21, BDD S-44.
func TestUpdateConfig_RefusesRetiredToolLoadingKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		// compressed marks the row for the retired Settings guard's key: its
		// refusal must not send the caller to PUT /api/v1/performance (C-21,
		// C-22: that guard is replaced, and that endpoint drops the switch).
		compressed bool
	}{
		{"a tools section write turns tools.manifest.compressed off",
			`{"tools":{"manifest":{"compressed":false}}}`, true},
		{"a tools section write turns tools.manifest.preview_all_lazy on",
			`{"tools":{"manifest":{"preview_all_lazy":true}}}`, false},
		{"a tools section write sets a server's deferred",
			`{"tools":{"mcp":{"servers":{"github":{"deferred":true}}}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			before := readConfigOnDisk(t, api)

			w := rtlPut(api, tc.body)
			msg := rtlRefusalMessage(t, w, tc.name)
			require.Contains(t, msg, rtlRefusalText,
				"C-22 row 3: the refusal carries the spec's text, got: %s", msg)
			if tc.compressed {
				require.NotContains(t, msg, "/api/v1/performance",
					"C-21/C-22: the retired Settings guard names Settings/Performance; the refusal must not send the caller to an endpoint that no longer takes the key")
			}
			require.Equal(t, before, readConfigOnDisk(t, api),
				"a refused write stores nothing: config.json must be unchanged")
		})
	}

	// Control (S-44: only the retired keys leave): a tools write that carries
	// no retired key is accepted and lands, so the refusals above are the
	// retired keys' and not a blanket refusal of tools writes — and the
	// harness can see an accepted write.
	t.Run("control: a tools write that carries no retired key is accepted and lands", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)

		w := rtlPut(api, `{"tools":{"read_file":{"max_read_file_size":4096}}}`)
		require.Equal(t, http.StatusOK, w.Code,
			"S-44: a non-retired tools setting must stay writable; got %d: %s", w.Code, w.Body.String())

		readFile, ok := rtlToolsOnDisk(t, api)["read_file"].(map[string]any)
		require.True(t, ok, "the accepted write must have stored tools.read_file")
		require.Equal(t, float64(4096), readFile["max_read_file_size"],
			"the accepted write must have landed in config.json")
	})
}

// TestUpdateConfig_RetiredToolLoadingKeys_RefusedByEffectNotByName — the
// refusal follows what the write does to the stored retired value, not which
// names the body spells (the strict reading of C-21's "a change to them").
// A whole-section replace that names no retired key but drops a stored one is
// refused; a round trip that repeats the stored values is not.
// Traces to: C-21 ("refuse a change to them"), C-6 ("stored values stay in
// place, ignored"), BDD S-44 (an install that had the switch off).
func TestUpdateConfig_RetiredToolLoadingKeys_RefusedByEffectNotByName(t *testing.T) {
	for _, tc := range []struct {
		name       string
		seed       string
		body       string
		compressed bool
	}{
		{"replacing tools.manifest drops a stored compressed=false",
			`{"manifest":{"compressed":false}}`, `{"tools":{"manifest":{}}}`, true},
		{"replacing tools.manifest drops a stored preview_all_lazy=true",
			`{"manifest":{"preview_all_lazy":true}}`, `{"tools":{"manifest":{}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			rtlSeedTools(t, api, tc.seed)
			before := readConfigOnDisk(t, api)

			w := rtlPut(api, tc.body)
			msg := rtlRefusalMessage(t, w, tc.name)
			require.Contains(t, msg, rtlRefusalText,
				"C-21/C-22: a write that changes a stored retired value is refused with the spec's text, got: %s", msg)
			if tc.compressed {
				require.NotContains(t, msg, "/api/v1/performance",
					"C-21/C-22: the refusal must not send the caller to an endpoint that no longer takes the key")
			}
			require.Equal(t, before, readConfigOnDisk(t, api),
				"a refused write stores nothing: config.json must be unchanged")
		})
	}

	// Control: "a change to them" — repeating the stored values is not one.
	// A whole-config round trip (read, edit one unrelated setting, write back)
	// carries every stored retired value unchanged and must keep working
	// (C-6: stored values stay in place, so every upgraded install has them).
	t.Run("control: a round trip that repeats the stored retired values is accepted", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		rtlSeedTools(t, api, `{"manifest":{"compressed":false,"preview_all_lazy":true}}`)

		w := rtlPut(api, `{"tools":{"manifest":{"compressed":false,"preview_all_lazy":true},`+
			`"read_file":{"max_read_file_size":4096}}}`)
		require.Equal(t, http.StatusOK, w.Code,
			"repeating the stored values is not a change to them; got %d: %s", w.Code, w.Body.String())

		tools := rtlToolsOnDisk(t, api)
		require.Equal(t, map[string]any{"compressed": false, "preview_all_lazy": true}, tools["manifest"],
			"the stored retired values must be exactly as they were")
		readFile, ok := tools["read_file"].(map[string]any)
		require.True(t, ok, "the unrelated setting in the same write must have landed")
		require.Equal(t, float64(4096), readFile["max_read_file_size"])
	})
}
