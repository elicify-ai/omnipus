// rest_performance_r4_logging_test.go — #904 gate round 3, items 1 and 3.
//
// Item 1: a config
// LOAD failure inside refreshConfigAndRewireServices during a Performance
// save was returned without being logged anywhere (PUT /performance itself
// deliberately logs only the fixed stage). The loader never resolves
// credentials, so its error is logged in full by
// refreshConfigAndRewireServices, like the neighbouring roster and
// credential failures.

package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// breakConfigLoad rewrites config.json with a schema version the loader
// rejects ("unsupported config version: 99"). The raw read-modify-write of
// PUT /performance keeps a version above the current one, so the write
// succeeds and the following in-memory refresh fails at the LOAD step.
func breakConfigLoad(t *testing.T, api *restAPI) {
	t.Helper()
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	m["version"] = 99
	out, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), out, 0o600))
}

func TestPerformancePut_RefreshLoadFailure_IsLogged(t *testing.T) {
	for _, tc := range []struct {
		name      string
		withStore bool
	}{
		{name: "no credential store", withStore: false},
		{name: "with credential store", withStore: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newMTIAPI(t, "200")
			if tc.withStore {
				api.credStore = credentials.NewStore(filepath.Join(api.homePath, "credentials.json"))
			}
			breakConfigLoad(t, api)
			logs := captureMTILogs(t)

			w := mtiPutPerf(t, api, `{"max_parallel_agents":3}`)
			require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
			got := decodeReloadFailed(t, w.Body.Bytes())
			require.Equal(t, "refresh", string(got.Details.Stage),
				"instrument: the load failure must be the refresh stage")

			logs.mu.Lock()
			all := logs.buf.String()
			logs.mu.Unlock()
			var loadLines []string
			for _, l := range strings.Split(all, "\n") {
				if strings.Contains(l, "config load failed") {
					loadLines = append(loadLines, l)
				}
			}
			require.Len(t, loadLines, 1, "the load failure must be logged exactly once; logs:\n%s", all)
			assert.Contains(t, loadLines[0], "level=ERROR", "logged at error level: %s", loadLines[0])
			assert.Contains(t, loadLines[0], "unsupported config version: 99",
				"the log line carries the loader's cause: %s", loadLines[0])
		})
	}
}

// ---------------------------------------------------------------------------
// Item 3: the underlying write failure is logged, the response stays fixed.
// ---------------------------------------------------------------------------

func TestPerformancePut_ConfigWriteFailure_LogsUnderlyingCause(t *testing.T) {
	api := newMTIAPI(t, "200")
	require.NoError(t, os.WriteFile(api.configPath(), []byte("{not json"), 0o600))
	logs := captureMTILogs(t)

	w := mtiPutPerf(t, api, `{"max_parallel_agents":3}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "config.json: invalid JSON", "the response keeps the fixed cause class")
	assert.NotContains(t, w.Body.String(), "invalid character", "the response must not carry the raw cause")

	logs.mu.Lock()
	all := logs.buf.String()
	logs.mu.Unlock()
	var lines []string
	for _, l := range strings.Split(all, "\n") {
		if strings.Contains(l, "could not write config.json") {
			lines = append(lines, l)
		}
	}
	require.Len(t, lines, 1, "the write failure must be logged once; logs:\n%s", all)
	assert.Contains(t, lines[0], "config.json: invalid JSON", "the log line keeps the cause class: %s", lines[0])
	assert.Contains(t, lines[0], "invalid character", "the log line carries the underlying error: %s", lines[0])
}
