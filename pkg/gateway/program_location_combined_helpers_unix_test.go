//go:build !windows

package gateway

// T11 combine plan §3: real person routes, the real shared checker, and real
// version subprocesses. Expected checks/messages come from that plan/spec/Q37.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/fixtures/t11combined"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const t11cRunCheckPattern = `\(([a-z_]+)(?:\)| at )`

func t11cCanonicalDir(t *testing.T) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(t11combined.TrustedProgramDir(t))
	require.NoError(t, err)
	return p
}

func t11cProgramAt(t *testing.T, source, dir string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	p := filepath.Join(dir, "combined-cli")
	t11CopyExe(t, source, p, 0o755)
	return p
}

func t11cSetConfig(t *testing.T, f *t8SettingsFixture, change func(*config.Config)) {
	t.Helper()
	require.NoError(t, f.api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		change(cfg)
		return config.SaveConfig(f.api.configPath(), cfg)
	}))
}

func t11cSaveAllowed(t *testing.T, f *t8SettingsFixture, program string) {
	t.Helper()
	body := t11bSettingsBody(t, gen.CliPreset{Path: program}, gen.CliPreset{}, gen.CliPreset{})
	w := f.call(t, http.MethodPut, t8SettingsRoute, body, "person")
	require.Equal(t, http.StatusOK, w.Code, "control: real signed-in Settings save must work: %s", w.Body.String())
	var disk config.Config
	require.NoError(t, json.Unmarshal(f.disk(t), &disk))
	require.Equal(t, program, disk.Sandbox.ThirdPartyCLI("claude-code").Path)
	require.Equal(t, program, f.api.agentLoop.GetConfig().Sandbox.ThirdPartyCLI("claude-code").Path)
}

func t11cSaveRefused(t *testing.T, f *t8SettingsFixture, program, check, at string) string {
	t.Helper()
	before := f.disk(t)
	beforeLive := f.api.agentLoop.GetConfig().Sandbox.ThirdPartyCLI("claude-code")
	body := t11bSettingsBody(t, gen.CliPreset{Path: program}, gen.CliPreset{}, gen.CliPreset{})
	w := f.call(t, http.MethodPut, t8SettingsRoute, body, "person")
	assert.Equal(t, http.StatusBadRequest, w.Code, "combined spec: Save refuses %s: %s", check, w.Body.String())
	assert.Equal(t, before, f.disk(t), "FR-7: rejected save writes no bytes")
	assert.Equal(t, beforeLive, f.api.agentLoop.GetConfig().Sandbox.ThirdPartyCLI("claude-code"), "FR-7: no live-config change")
	if w.Code != http.StatusBadRequest {
		return "accepted"
	}
	var out gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.NotNil(t, out.Code)
	require.NotNil(t, out.Field)
	require.NotNil(t, out.Details)
	assert.Equal(t, "cli_path_invalid", *out.Code, "location_unverifiable remains a failed check, not an envelope code")
	assert.Equal(t, "clis.claude-code.path", *out.Field)
	assert.Equal(t, "claude-code", (*out.Details)["cli"])
	assert.Equal(t, check, (*out.Details)["check"])
	if check == "agent_writable_location" {
		at = t11CanonicalFailureElement(t, at)
	}
	assert.Equal(t, at, (*out.Details)["at"])
	reasons := map[string]string{
		"agent_writable_location": "it is inside a folder agents can write to.",
		"location_unverifiable":   t11combined.LocationUnverifiableReason,
		"not_absolute":            "it must be an absolute path.",
		"not_found":               "nothing exists at that path (or a link in it is broken).",
	}
	reason, specified := reasons[check]
	require.True(t, specified, "test fixture must name an independently specified check")
	assert.Equal(t, "The program path for Claude Code was not saved: "+reason, out.Error)
	assert.NotContains(t, out.Error, "Omnipus could not read the list of mounted folders", "Q37: no old mount-only sentence")
	got, ok := (*out.Details)["check"].(string)
	require.True(t, ok)
	return got
}

func t11cCLIResponse(t *testing.T, f *t8SettingsFixture, program string) gen.CliValidateResponse {
	t.Helper()
	w := f.call(t, http.MethodPost, "/api/v1/system/cli-validate",
		gen.CliValidateRequest{Cli: gen.ExternalCliToolClaudeCode, CliPath: program}, "person")
	require.Equal(t, http.StatusOK, w.Code, "real person CLI route: %s", w.Body.String())
	return t11DecodeValidate(t, w)
}

func t11cCLIRefused(t *testing.T, f *t8SettingsFixture, program, marker, check string, spawns *int) string {
	t.Helper()
	t11ClearMarker(t, marker)
	*spawns = 0
	resp := t11cCLIResponse(t, f, program)
	assert.Equal(t, gen.CliValidateResponseReasonPathNotAllowed, resp.Reason, "full-union CLI refusal: %+v", resp)
	assert.False(t, resp.Ok)
	assert.Nil(t, resp.Version)
	assert.Nil(t, resp.ResolvedPath)
	t11AssertNotSpawned(t, marker, *spawns)
	require.NotNil(t, resp.Detail, "C-A2/R10: person sees the failed check")
	assert.Contains(t, *resp.Detail, "check "+check+" failed", "R10: exact failed check id reaches CLI detail")
	if check == "location_unverifiable" {
		assert.NotContains(t, *resp.Detail, "Omnipus could not read the list of mounted folders")
	}
	if resp.Reason != gen.CliValidateResponseReasonPathNotAllowed {
		return "accepted"
	}
	return t11cReadCheck(t, *resp.Detail, `\bcheck ([a-z_]+) failed\b`)
}

func t11cUnsafeWorkspace(t *testing.T, home string) string {
	t.Helper()
	const id = "unsafe..workspace"
	_, err := workspace.SafeWorkspaceDir(home, id)
	require.ErrorIs(t, err, workspace.ErrInvalidWorkspaceID, "instrument: actual unsafe-id rule")
	folder := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(folder, 0o700))
	file := filepath.Join(folder, id+".json")
	require.NoError(t, os.WriteFile(file, []byte(`{"id":"unsafe..workspace","name":"Unsafe fixture"}`), 0o600))
	ids, err := workspace.ListWorkspaceIDs(home)
	require.NoError(t, err)
	require.Contains(t, ids, id, "instrument: actual listing must expose this unsafe record")
	return file
}

func t11cRunDecision(t *testing.T, f *t8SettingsFixture, program, wantCheck string) string {
	t.Helper()
	roots, rootsErr := thirdpartycli.AgentWritableRoots(f.api.homePath, f.api.agentLoop.GetConfig())
	resolved, refusal := thirdpartycli.RunProgram("claude-code", program, roots, rootsErr)
	if wantCheck == "" {
		final, err := filepath.EvalSymlinks(program)
		require.NoError(t, err)
		assert.Empty(t, refusal)
		assert.Equal(t, final, resolved)
		return ""
	}
	assert.Empty(t, resolved, "refusal never returns a file to launch")
	assert.Regexp(t, `(?i)claude[ -]code`, refusal, "Q48: keep the program identity")
	assert.Regexp(t, `\b`+wantCheck+`\b`, refusal, "R7: run must name the same independently expected check")
	if refusal == "" {
		return "accepted"
	}
	t.Run("client_diagnostics_Q48", func(t *testing.T) {
		t11bAssertNoProgramDiagnostics(t, refusal, program)
	})
	return t11cReadCheck(t, refusal, t11cRunCheckPattern)
}

func t11cReadCheck(t *testing.T, text, pattern string) string {
	t.Helper()
	matched := regexp.MustCompile(pattern).FindStringSubmatch(text)
	require.Len(t, matched, 2, "R7: extract the ACTUAL exposed check from %q", text)
	return matched[1]
}
