// T11-B RED: spec §4, §8 FR-6/FR-7 and the T8 generated settings contract.
// Real boundary: registered HTTP mux, authentication, workspace store and config
// persistence. No handler or validator is mocked. Missing T8 routes fail loudly.
// Q2b removes owner/permission checks; only executable and location rules apply.
package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

var (
	t11bPersonToken = "omnipus_" + strings.Repeat("p", 64)
	t11bCLIToken    = "omnipus_" + strings.Repeat("e", 64)
)

const t11bSettingsURL = "/api/v1/settings/third-party-agents"

func t11bSettingsAPI(t *testing.T) (*restAPI, *t8SettingsFixture, string) {
	t.Helper()
	f := t8SettingsAPI(t) // real login, issued cookies and production middleware
	// Keep a real machine credential for the negative probe. The selector below
	// chooses this credential; it never injects a person or CLI role into context.
	hash := mustBcryptHash(t, t11bCLIToken)
	require.NoError(t, f.api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		cfg.Gateway.CLIToken = &config.TokenEntry{Hash: hash}
		return config.SaveConfig(f.api.configPath(), cfg)
	}))
	require.NoError(t, f.api.agentLoop.GetConfig().Gateway.VerifyCLIToken(t11bCLIToken),
		"instrument: the negative probe presents a valid configured CLI token")
	f.token = t11bCLIToken
	return f.api, f, f.api.homePath
}

func t11bRequest(t *testing.T, f *t8SettingsFixture, token, method, url, body string) *httptest.ResponseRecorder {
	t.Helper()
	actor := "signed-out"
	switch token {
	case t11bPersonToken:
		actor = "person"
	case t11bCLIToken:
		actor = "token"
	case "":
	default:
		t.Fatalf("SETUP: unknown T11-B actor selector %q", token)
	}
	var request any
	if body != "" {
		request = json.RawMessage(body) // retain the object, not a JSON-encoded string
	}
	// f.call keeps the issued CSRF pair for every probe, but includes the real
	// session cookie only for the person. No authority boundary is bypassed.
	w := f.call(t, method, url, request, actor)
	if w.Code == http.StatusNotFound {
		t.Fatalf("BLOCKED: %s %s handler not implemented — required by T11 §4 / FR-6, FR-7; body=%s", method, url, w.Body.String())
	}
	return w
}

// The file is never launched by the save tests. Windows accepts .cmd; Unix
// accepts a regular executable script. Paths are constructed by the host OS.
func t11bWriteExe(t *testing.T, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	return p
}

func t11bWorkspaceExe(t *testing.T, home string) string {
	t.Helper()
	const id = "t11bws"
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: id, Name: "T11B"}))
	work, err := workspace.SafeWorkDir(home, id)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(work, 0o700))
	name := "plantedcli"
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	p := filepath.Join(work, name)
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	return p
}

// Unmarshal the fixed empty fixture into the GENERATED request. This avoids
// redeclaring oapi-codegen's anonymous nested wire structs. All variable input
// is assigned to generated fields and then JSON-encoded, never interpolated.
func t11bSettingsBody(t *testing.T, claude, codex, opencode gen.CliPreset) gen.ThirdPartyAgentSettings {
	t.Helper()
	var req gen.ThirdPartyAgentSettings
	require.NoError(t, json.Unmarshal([]byte(`{"clis":{"claude-code":{"path":"","preset_args":""},"codex":{"path":"","preset_args":""},"opencode":{"path":"","preset_args":""}}}`), &req))
	req.Clis.ClaudeCode.Path, req.Clis.ClaudeCode.PresetArgs = claude.Path, claude.PresetArgs
	req.Clis.Codex.Path, req.Clis.Codex.PresetArgs = codex.Path, codex.PresetArgs
	req.Clis.Opencode.Path, req.Clis.Opencode.PresetArgs = opencode.Path, opencode.PresetArgs
	return req
}

func t11bPut(t *testing.T, mux *t8SettingsFixture, req gen.ThirdPartyAgentSettings) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(req)
	require.NoError(t, err)
	return t11bRequest(t, mux, t11bPersonToken, http.MethodPut, t11bSettingsURL, string(raw))
}

func t11bDecodeView(t *testing.T, w *httptest.ResponseRecorder) gen.ThirdPartyAgentSettingsView {
	t.Helper()
	var view gen.ThirdPartyAgentSettingsView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &view), "body=%s", w.Body.String())
	return view
}

func t11bDecodeError(t *testing.T, w *httptest.ResponseRecorder) gen.ErrorResponse {
	t.Helper()
	var resp gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "body=%s", w.Body.String())
	return resp
}

func TestSettingsThirdPartyAgents_PersonRouteRegisteredAndCLITokenRefused(t *testing.T) {
	api, mux, home := t11bSettingsAPI(t)
	cfg := api.agentLoop.GetConfig()
	w := t11bRequest(t, mux, t11bPersonToken, http.MethodGet, t11bSettingsURL, "")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	before, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		for _, tc := range []struct {
			name, token string
			bypass      bool
			status      int
		}{
			{"CLI token", t11bCLIToken, false, http.StatusForbidden},
			{"signed out", "", false, http.StatusUnauthorized},
			{"development bypass", "", true, http.StatusForbidden},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				cfg.Gateway.DevModeBypass = tc.bypass
				defer func() { cfg.Gateway.DevModeBypass = false }()
				w := t11bRequest(t, mux, tc.token, method, t11bSettingsURL, `{"clis":{}}`)
				require.Equal(t, tc.status, w.Code, "person-only route; body=%s", w.Body.String())
				after, err := os.ReadFile(filepath.Join(home, "config.json"))
				require.NoError(t, err)
				require.Equal(t, before, after, "non-person request must not write config")
			})
		}
	}
}

func TestSettingsThirdPartyAgents_GetViewAlwaysCarriesThreeRows(t *testing.T) {
	_, mux, home := t11bSettingsAPI(t)
	before, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	w := t11bRequest(t, mux, t11bPersonToken, http.MethodGet, t11bSettingsURL, "")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	view := t11bDecodeView(t, w)
	require.Equal(t, "", view.Clis.ClaudeCode.Path)
	require.Equal(t, "", view.Clis.ClaudeCode.PresetArgs)
	require.Equal(t, "", view.Clis.Codex.Path)
	require.Equal(t, "", view.Clis.Codex.PresetArgs)
	require.Equal(t, "", view.Clis.Opencode.Path)
	require.Equal(t, "", view.Clis.Opencode.PresetArgs)
	// A generic JSON object is used only to verify required-key PRESENCE;
	// unmarshalling into a generated struct alone cannot detect missing keys.
	var wire map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &wire))
	var rows map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wire["clis"], &rows))
	require.Len(t, rows, 3)
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		require.Contains(t, rows, cli)
		for _, key := range []string{"path", "preset_args", "detected"} {
			require.Contains(t, rows[cli], key, "required %s.%s", cli, key)
		}
		require.NotEqual(t, "null", string(rows[cli]["detected"]))
	}
	after, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	require.Equal(t, before, after, "detection is live read data, never auto-saved")
}

func TestSettingsThirdPartyAgents_DetectionNeverExecutesFoundProgram(t *testing.T) {
	_, mux, _ := t11bSettingsAPI(t)
	binDir := t.TempDir()
	marker := filepath.Join(binDir, "started")
	name := "codex"
	script := fmt.Sprintf("#!/bin/sh\nprintf started > %q\n", marker)
	if runtime.GOOS == "windows" {
		name += ".cmd"
		script = "@echo started > \"" + marker + "\"\r\n"
	}
	program := filepath.Join(binDir, name)
	require.NoError(t, os.WriteFile(program, []byte(script), 0o755))
	t.Setenv("PATH", binDir)
	w := t11bRequest(t, mux, t11bPersonToken, http.MethodGet, t11bSettingsURL, "")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	view := t11bDecodeView(t, w)
	require.True(t, view.Clis.Codex.Detected.Installed, "instrument: the fake program must actually be detected")
	want, err := filepath.EvalSymlinks(program)
	require.NoError(t, err)
	require.NotNil(t, view.Clis.Codex.Detected.Path)
	require.Equal(t, want, *view.Clis.Codex.Detected.Path)
	_, err = os.Stat(marker)
	require.True(t, os.IsNotExist(err), "FR-6: merely opening Settings must not execute a detected CLI; stat=%v", err)
	// Positive control AFTER the product assertion: execute only our harmless
	// fixture explicitly, proving that the marker instrument would see a spawn.
	cmd := exec.CommandContext(t.Context(), program)
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(t.Context(), filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/c", program)
	}
	require.NoError(t, cmd.Run())
	bytes, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "started", strings.TrimSpace(string(bytes)))
}

func TestSettingsThirdPartyAgents_PutStoresTypedValuesAndReturnsView(t *testing.T) {
	_, mux, home := t11bSettingsAPI(t)
	claudePath := t11bWriteExe(t, "claude-valid")
	codexPath := t11bWriteExe(t, "codex-valid")
	// Preserve harmless lexical spelling: storage is as typed, not a cleaned
	// or symlink-resolved path (CliPreset.path). Native separator on each OS.
	codexTyped := filepath.Dir(codexPath) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(codexPath)
	const preset = "--dangerously-bypass-approvals-and-sandbox --model gpt-5-codex"
	req := t11bSettingsBody(t, gen.CliPreset{Path: claudePath, PresetArgs: "--max-turns 40"}, gen.CliPreset{Path: codexTyped, PresetArgs: preset}, gen.CliPreset{})
	w := t11bPut(t, mux, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	view := t11bDecodeView(t, w)
	require.Equal(t, claudePath, view.Clis.ClaudeCode.Path)
	require.Equal(t, "--max-turns 40", view.Clis.ClaudeCode.PresetArgs)
	require.Equal(t, codexTyped, view.Clis.Codex.Path)
	require.Equal(t, preset, view.Clis.Codex.PresetArgs)
	w = t11bRequest(t, mux, t11bPersonToken, http.MethodGet, t11bSettingsURL, "")
	require.Equal(t, http.StatusOK, w.Code)
	stored := t11bDecodeView(t, w)
	require.Equal(t, view.Clis.ClaudeCode.Path, stored.Clis.ClaudeCode.Path)
	require.Equal(t, codexTyped, stored.Clis.Codex.Path)
	require.Equal(t, preset, stored.Clis.Codex.PresetArgs)
	require.Equal(t, "", stored.Clis.Opencode.Path)
	// Inspect the actual file, not only an in-memory GET echo.
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	var configJSON map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &configJSON))
	var sandbox map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(configJSON["sandbox"], &sandbox))
	var persisted gen.ThirdPartyAgentSettings
	require.NoError(t, json.Unmarshal(sandbox["third_party_agents"], &persisted))
	require.Equal(t, req, persisted)
}

func TestSettingsThirdPartyAgents_PutRefusedPathNamesFieldAndSavesNothing(t *testing.T) {
	_, mux, home := t11bSettingsAPI(t)
	claude := t11bWriteExe(t, "claude-valid")
	codex := t11bWriteExe(t, "codex-valid")
	base := t11bSettingsBody(t, gen.CliPreset{Path: claude, PresetArgs: "--max-turns 40"}, gen.CliPreset{Path: codex}, gen.CliPreset{})
	w := t11bPut(t, mux, base)
	require.Equal(t, http.StatusOK, w.Code, "baseline save: %s", w.Body.String())
	before, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	planted := t11bWorkspaceExe(t, home)
	// Change a VALID earlier row too: a server that writes rows incrementally
	// before refusing Codex must fail the atomicity assertion.
	refused := t11bSettingsBody(t, gen.CliPreset{Path: claude, PresetArgs: "--max-turns 99"}, gen.CliPreset{Path: planted}, gen.CliPreset{})
	w = t11bPut(t, mux, refused)
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	resp := t11bDecodeError(t, w)
	require.NotNil(t, resp.Code)
	require.Equal(t, "cli_path_invalid", *resp.Code)
	require.NotNil(t, resp.Field)
	require.Equal(t, "clis.codex.path", *resp.Field)
	require.NotNil(t, resp.Details)
	require.Equal(t, "codex", (*resp.Details)["cli"])
	require.Equal(t, "agent_writable_location", (*resp.Details)["check"])
	require.NotEmpty(t, resp.Error, "the person receives a repair/refusal explanation")
	after, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	require.Equal(t, before, after, "nothing is saved, including changed valid rows")
	w = t11bRequest(t, mux, t11bPersonToken, http.MethodGet, t11bSettingsURL, "")
	require.Equal(t, http.StatusOK, w.Code)
	view := t11bDecodeView(t, w)
	require.Equal(t, codex, view.Clis.Codex.Path)
	require.Equal(t, "--max-turns 40", view.Clis.ClaudeCode.PresetArgs)
}

func TestSettingsThirdPartyAgents_OmittedRowsAreClearedByWholeSectionSave(t *testing.T) {
	_, mux, _ := t11bSettingsAPI(t)
	initial := t11bSettingsBody(t,
		gen.CliPreset{Path: t11bWriteExe(t, "claude-original"), PresetArgs: "--max-turns 40"},
		gen.CliPreset{Path: t11bWriteExe(t, "codex-original"), PresetArgs: "--model gpt-5-codex"},
		gen.CliPreset{Path: t11bWriteExe(t, "opencode-original"), PresetArgs: "--model vendor/model"},
	)
	w := t11bPut(t, mux, initial)
	require.Equal(t, http.StatusOK, w.Code, "baseline save: %s", w.Body.String())
	// ThirdPartyAgentSettings: absent CLI keys are cleared, not retained.
	initial.Clis.ClaudeCode, initial.Clis.Opencode = nil, nil
	w = t11bPut(t, mux, initial)
	require.Equal(t, http.StatusOK, w.Code, "whole-section replace: %s", w.Body.String())
	w = t11bRequest(t, mux, t11bPersonToken, http.MethodGet, t11bSettingsURL, "")
	require.Equal(t, http.StatusOK, w.Code)
	stored := t11bDecodeView(t, w)
	require.Equal(t, "", stored.Clis.ClaudeCode.Path)
	require.Equal(t, "", stored.Clis.ClaudeCode.PresetArgs)
	require.Equal(t, "", stored.Clis.Opencode.Path)
	require.Equal(t, "", stored.Clis.Opencode.PresetArgs)
	require.Equal(t, initial.Clis.Codex.Path, stored.Clis.Codex.Path)
	require.Equal(t, initial.Clis.Codex.PresetArgs, stored.Clis.Codex.PresetArgs)
}

func TestSettingsThirdPartyAgents_ReturnsFirstInvalidRowAndFieldWithoutWriting(t *testing.T) {
	// The request contract fixes order: claude-code -> codex -> opencode,
	// then path before preset_args within a row. All errors below coexist.
	for _, tc := range []struct {
		name, cli, field, code, check                string
		claudeBadPath, claudeBadPreset, codexBadPath bool
	}{
		{"path_before_preset", "claude-code", "path", "cli_path_invalid", "agent_writable_location", true, true, true},
		{"earlier_preset_before_later_path", "claude-code", "preset_args", "cli_preset_invalid", "contains_line_break", false, true, true},
		{"codex_before_opencode", "codex", "path", "cli_path_invalid", "agent_writable_location", false, false, true},
		{"opencode_after_valid_rows", "opencode", "preset_args", "cli_preset_invalid", "contains_line_break", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, mux, home := t11bSettingsAPI(t)
			planted := t11bWorkspaceExe(t, home)
			claude, codex := gen.CliPreset{}, gen.CliPreset{}
			if tc.claudeBadPath {
				claude.Path = planted
			}
			if tc.claudeBadPreset {
				claude.PresetArgs = "--model x\n--debug"
			}
			if tc.codexBadPath {
				codex.Path = planted
			}
			req := t11bSettingsBody(t, claude, codex, gen.CliPreset{PresetArgs: "--model x\n--debug"})
			before, err := os.ReadFile(filepath.Join(home, "config.json"))
			require.NoError(t, err)
			w := t11bPut(t, mux, req)
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			refusal := t11bDecodeError(t, w)
			require.NotNil(t, refusal.Code)
			require.Equal(t, tc.code, *refusal.Code)
			require.NotNil(t, refusal.Field)
			require.Equal(t, "clis."+tc.cli+"."+tc.field, *refusal.Field)
			require.NotNil(t, refusal.Details)
			require.Equal(t, tc.cli, (*refusal.Details)["cli"])
			require.Equal(t, tc.check, (*refusal.Details)["check"])
			after, err := os.ReadFile(filepath.Join(home, "config.json"))
			require.NoError(t, err)
			require.Equal(t, before, after, "validation of every row precedes every write")
		})
	}
}

func TestSettingsThirdPartyAgents_PresetArgsValidationBoundaries(t *testing.T) {
	// CliPreset maxLength=4096; no NUL/CR/LF. The limits are contract values,
	// not values discovered by running the implementation.
	for _, tc := range []struct {
		name, preset, check string
	}{
		{"empty", "", ""}, {"one", "x", ""},
		{"max_minus_one", strings.Repeat("x", 4095), ""},
		{"max", strings.Repeat("x", 4096), ""},
		{"max_plus_one", strings.Repeat("x", 4097), "too_long"},
		{"NUL", "--model x\x00y", "contains_nul"},
		{"LF", "--model x\n--debug", "contains_line_break"},
		{"CR", "--model x\r--debug", "contains_line_break"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, mux, home := t11bSettingsAPI(t)
			before, err := os.ReadFile(filepath.Join(home, "config.json"))
			require.NoError(t, err)
			body := t11bSettingsBody(t, gen.CliPreset{}, gen.CliPreset{PresetArgs: tc.preset}, gen.CliPreset{})
			w := t11bPut(t, mux, body)
			if tc.check == "" {
				require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
				require.Equal(t, tc.preset, t11bDecodeView(t, w).Clis.Codex.PresetArgs)
				return
			}
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			resp := t11bDecodeError(t, w)
			require.NotNil(t, resp.Code)
			require.Equal(t, "cli_preset_invalid", *resp.Code)
			require.NotNil(t, resp.Field)
			require.Equal(t, "clis.codex.preset_args", *resp.Field)
			require.NotNil(t, resp.Details)
			require.Equal(t, "codex", (*resp.Details)["cli"])
			require.Equal(t, tc.check, (*resp.Details)["check"])
			after, err := os.ReadFile(filepath.Join(home, "config.json"))
			require.NoError(t, err)
			require.Equal(t, before, after, "an invalid preset must not write config")
		})
	}
}
