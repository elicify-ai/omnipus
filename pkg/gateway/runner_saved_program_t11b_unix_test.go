//go:build !windows

package gateway

// Founder Q12 A / S4, T11 FR-9: the connection check must select and check
// the same saved program as a real run. Only a harmless POSIX CLI is launched;
// credentials are synthetic and the stub cannot make network requests.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func t11bConnectionProgram(t *testing.T, dir, name, version string) (string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	program := filepath.Join(root, name)
	marker := program + ".executed"
	// Paths come from our fixture, not from user input. Quote each shell token.
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %s\nprintf '%%s\\n' %s\n", quote(marker), quote(version))
	require.NoError(t, os.WriteFile(program, []byte(script), 0o755))
	return program, marker
}

func t11bConnectionAPI(t *testing.T, legacy string) *restAPI {
	t.Helper()
	api := runnerTestAPI(t)
	api.homePath = t.TempDir()
	t.Setenv(config.EnvHome, api.homePath)
	t.Setenv("ANTHROPIC_API_KEY", "t11b-synthetic-auth-not-a-real-key")
	// Exercise persisted legacy input without making a removed config field a
	// compile-time prerequisite of this test. FR-9 says the field is ignored.
	raw, err := json.Marshal(map[string]any{"kind": "external-cli", "cli": "claude-code", "cli_path": legacy})
	require.NoError(t, err)
	var executor config.ExecutorConfig
	require.NoError(t, json.Unmarshal(raw, &executor))
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == "ext-claude" {
				cfg.Agents.List[i].Subagents.Executor = &executor
			}
		}
		return nil
	}))
	return api
}

func TestRunnerTestEndpoint_SavedProgramActuallyRuns_S4(t *testing.T) {
	legacy, legacyMarker := t11bConnectionProgram(t, t.TempDir(), "legacy-claude", "legacy version")
	saved, savedMarker := t11bConnectionProgram(t, t.TempDir(), "saved-claude", "claude-code saved-test-version")
	api := t11bConnectionAPI(t, legacy)
	t11bSaveProgram(t, api, "claude-code", saved)
	code, resp := postRunnerTest(t, api, "ext-claude")
	require.Equal(t, http.StatusOK, code)
	require.True(t, resp.Ok, "instrument: harmless saved program and synthetic auth satisfy the real check: %+v", resp)
	assert.Equal(t, "claude-code saved-test-version", deref(resp.CliVersion))
	got, err := os.ReadFile(savedMarker)
	require.NoError(t, err, "S4: the actual saved file, not merely its check, must run")
	assert.Equal(t, "--version\n", string(got))
	assert.NoFileExists(t, legacyMarker, "FR-9: legacy cli_path must never be executed")
}

func TestRunnerTestEndpoint_NoSavedProgramNeverFallsBack_S4(t *testing.T) {
	for _, source := range []string{"legacy", "PATH"} {
		t.Run(source, func(t *testing.T) {
			program, marker := t11bConnectionProgram(t, t.TempDir(), "claude", "claude-code fallback-test-version")
			legacy := program
			if source == "PATH" {
				legacy = ""
				t.Setenv("PATH", filepath.Dir(program))
			}
			api := t11bConnectionAPI(t, legacy)
			code, resp := postRunnerTest(t, api, "ext-claude")
			require.Equal(t, http.StatusOK, code)
			assert.False(t, resp.Ok, "a present executable is not a person-saved Settings program")
			assert.Equal(t, gen.RunnerTestResponseReasonMissingBinary, resp.Reason)
			assert.Equal(t, "This agent cannot run: no program path is set for Claude Code. A person can set it in Settings → Third-party agents → Claude Code.", resp.Message)
			assert.NoFileExists(t, marker, "S4: refusal must happen before the connection subprocess")
		})
	}
}

func TestRunnerTestEndpoint_ForbiddenSavedProgramNeverRuns_S4(t *testing.T) {
	api := t11bConnectionAPI(t, "")
	program, marker := t11bConnectionProgram(t, api.homePath, "claude-in-agent-home", "claude-code forbidden-test-version")
	// A legacy value naming the same file would previously execute it unchecked.
	raw, err := json.Marshal(map[string]any{"kind": "external-cli", "cli": "claude-code", "cli_path": program})
	require.NoError(t, err)
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		return json.Unmarshal(raw, cfg.Agents.List[2].Subagents.Executor)
	}))
	t11bSaveProgram(t, api, "claude-code", program)
	code, resp := postRunnerTest(t, api, "ext-claude")
	require.Equal(t, http.StatusOK, code)
	assert.False(t, resp.Ok)
	assert.Equal(t, gen.RunnerTestResponseReasonMissingBinary, resp.Reason)
	want := fmt.Sprintf("This agent cannot run: the program set for Claude Code in Settings → Third-party agents does not pass its checks (agent_writable_location at %s). A person can fix it there.", program)
	assert.Equal(t, want, resp.Message, "S4 uses the same actionable run refusal")
	assert.NoFileExists(t, marker, "an agent-writable executable cannot be used for the diagnostic either")
}
