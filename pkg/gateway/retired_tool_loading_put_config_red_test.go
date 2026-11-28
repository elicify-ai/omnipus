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
// caller there is a dead end.
//
// What the spec leaves open, ruled 2026-11-28 (team-lead; the spec-sync note
// for the PR is team-lead's):
//   - the HTTP status of the refusal (the neighbouring guards answer 400 for
//     an effect refusal and 403 for a blocked path): any 4xx is accepted, so
//     these tests accept any 4xx and pin the text and the stored state;
//   - "a change to them": by name only. The refusal is of a body that names a
//     retired key. A body that merely drops a stored retired value is not
//     pinned: the upgrade is expected to remove stored retired values, so
//     refusing that would contradict it (the by-effect reading, a separate
//     function in the earlier version of this file, was taken out; it can
//     return if the Chief Architect's redesign of the upgrade rules otherwise).
//
// The refusal must also store nothing: config.json is compared before and
// after, as the neighbouring blocked-path tests do. updateConfig is called
// directly (the admin middleware is orthogonal), like blocked_paths_test.go.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// rtlRefusalText is C-22 row 3's refusal text, character for character.
const rtlRefusalText = "This setting was replaced by levels in Skills & Tools."

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
