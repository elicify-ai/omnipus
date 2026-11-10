//go:build darwin || linux

package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder Q49 A: the same per-user runtime temp rule reaches both real person
// routes. The program works before its existing directory becomes runtime temp.
func TestR8_RuntimeTempProgramsRefusedByPersonRoutes_Q49(t *testing.T) {
	for _, spelling := range []string{"ordinary", "symlink"} {
		t.Run(spelling, func(t *testing.T) {
			f, program, marker := r8PersonProgram(t)
			control := f.call(t, http.MethodPost, "/api/v1/system/cli-validate",
				gen.CliValidateRequest{Cli: gen.ExternalCliToolClaudeCode, CliPath: program}, "person")
			require.Equal(t, http.StatusOK, control.Code)
			var initial gen.CliValidateResponse
			require.NoError(t, json.Unmarshal(control.Body.Bytes(), &initial))
			require.Equal(t, gen.CliValidateResponseReasonOk, initial.Reason)
			require.Equal(t, "8.1.0", deref(initial.Version))
			beforeStarts, err := os.ReadFile(marker)
			require.NoError(t, err)
			require.Equal(t, "--version\n", string(beforeStarts), "instrument: real allowed handshake ran once")
			temp, err := filepath.EvalSymlinks(filepath.Dir(program))
			require.NoError(t, err)
			if spelling == "symlink" {
				link := filepath.Join(t.TempDir(), "r8-runtime-temp-alias")
				require.NoError(t, os.Symlink(temp, link))
				require.NotEqual(t, temp, link)
				temp = link
			}
			t.Setenv("TMPDIR", temp)
			require.Equal(t, temp, os.TempDir())
			t.Run("settings_save", func(t *testing.T) {
				beforeDisk := f.disk(t)
				w := f.call(t, http.MethodPut, t8SettingsRoute,
					t11bSettingsBody(t, gen.CliPreset{Path: program, PresetArgs: "--model r8-unsaved"}, gen.CliPreset{}, gen.CliPreset{}), "person")
				require.Equal(t, http.StatusBadRequest, w.Code, "Q49: person cannot save a runtime-temp program: %s", w.Body.String())
				result := t11bDecodeError(t, w)
				require.NotNil(t, result.Code)
				assert.Equal(t, "cli_path_invalid", *result.Code)
				require.NotNil(t, result.Details)
				assert.Equal(t, "agent_writable_location", (*result.Details)["check"])
				assert.Equal(t, beforeDisk, f.disk(t), "refusal saves no settings")
				assert.Equal(t, "", f.api.agentLoop.GetConfig().Sandbox.ThirdPartyCLI("claude-code").PresetArgs)
			})
			t.Run("check_cli", func(t *testing.T) {
				w := f.call(t, http.MethodPost, "/api/v1/system/cli-validate",
					gen.CliValidateRequest{Cli: gen.ExternalCliToolClaudeCode, CliPath: program}, "person")
				require.Equal(t, http.StatusOK, w.Code)
				result := t11DecodeValidate(t, w)
				assert.False(t, result.Ok)
				assert.Equal(t, gen.CliValidateResponseReasonPathNotAllowed, result.Reason,
					"Q49: runtime temp is agent-writable, not a runnable program location")
				assert.Nil(t, result.Version)
				assert.Nil(t, result.ResolvedPath)
				assert.Contains(t, deref(result.Detail), "agent_writable_location")
				afterStarts, err := os.ReadFile(marker)
				require.NoError(t, err)
				assert.Equal(t, beforeStarts, afterStarts, "Q49: refuse before even the version process starts")
			})
		})
	}
}
