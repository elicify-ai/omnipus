package gateway

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Q58 supersedes the old details.at oracle. Inspect every response string,
// including unknown fields and escaped Windows paths, not just error text.
func t8AssertNoProgramPath(t *testing.T, raw []byte, paths ...string) {
	t.Helper()
	t.Run("client_diagnostics_Q58", func(t *testing.T) {
		t11bAssertNoProgramDiagnostics(t, string(raw), paths...)
		var body any
		require.NoError(t, json.Unmarshal(raw, &body))
		var inspect func(any)
		inspect = func(value any) {
			switch value := value.(type) {
			case string:
				t11bAssertNoProgramDiagnostics(t, value, paths...)
			case map[string]any:
				for key, entry := range value {
					t11bAssertNoProgramDiagnostics(t, key, paths...)
					inspect(entry)
				}
			case []any:
				for _, entry := range value {
					inspect(entry)
				}
			}
		}
		inspect(body)
	})
}

// Real person session, registered Settings route, successful save control, then
// a refused writable program. Only server-log sinks are captured, not the check.
func TestR8c_SettingsSaveRefusalHasNoPathAndLogsIt_Q58(t *testing.T) {
	f := t8SettingsAPI(t)
	control := t11bWriteExe(t, "r8c-settings-control")
	body := t11bSettingsBody(t, gen.CliPreset{Path: control}, gen.CliPreset{}, gen.CliPreset{})
	w := f.call(t, http.MethodPut, t8SettingsRoute, body, "person")
	require.Equal(t, http.StatusOK, w.Code, "instrument: same real Settings route saves a trusted program: %s", w.Body.String())
	planted := t11bWorkspaceExe(t, f.api.homePath)
	before := f.disk(t)

	var diagnostic bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&diagnostic, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	file := filepath.Join(t.TempDir(), "r8c-settings-server.log")
	level := logger.GetLevel()
	logger.SetLevel(logger.DEBUG)
	require.NoError(t, logger.EnableFileLogging(file))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(level)
	})
	// Test the instrument at a permissive level; the requirement does not
	// prescribe which server logger or severity records the private path.
	slog.Debug("r8c-slog-capture-control")
	logger.Debug("r8c-file-capture-control")
	captured, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Contains(t, diagnostic.String(), "r8c-slog-capture-control")
	require.Contains(t, string(captured), "r8c-file-capture-control")

	body.Clis.ClaudeCode.Path = planted
	w = f.call(t, http.MethodPut, t8SettingsRoute, body, "person")
	require.Equal(t, http.StatusBadRequest, w.Code)
	out := t11bDecodeError(t, w)
	require.NotNil(t, out.Code)
	assert.Equal(t, "cli_path_invalid", *out.Code)
	require.NotNil(t, out.Field)
	assert.Equal(t, "clis.claude-code.path", *out.Field)
	require.NotNil(t, out.Details)
	assert.Equal(t, "claude-code", (*out.Details)["cli"])
	assert.Equal(t, "agent_writable_location", (*out.Details)["check"])
	assert.Equal(t, "The program path for Claude Code was not saved: it is inside a folder agents can write to.", out.Error)
	t8AssertNoProgramPath(t, w.Body.Bytes(), planted, control)
	assert.Equal(t, before, f.disk(t), "Q58 does not change atomic rejection")
	logged, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Contains(t, diagnostic.String()+string(logged), planted,
		"Q58: the refused program path remains in server diagnostics, never the response")
}
