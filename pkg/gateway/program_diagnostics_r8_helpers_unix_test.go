//go:build darwin || linux

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
	"github.com/elicify-ai/omnipus/tests/fixtures/t11combined"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real issued person session, registered routes, Settings persistence and shared
// checker. The only program is a harmless local protocol/marker fixture.
func r8PersonProgram(t *testing.T) (*t8SettingsFixture, string, string) {
	t.Helper()
	f := t8SettingsAPI(t)
	chain := f.handler
	f.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = t.Name() + ":0"
		chain.ServeHTTP(w, r)
	})
	t.Setenv("ANTHROPIC_API_KEY", "r8-synthetic-local-auth")
	dir := filepath.Join(t11combined.TrustedProgramDir(t), "r8-private-folder-marker")
	require.NoError(t, os.Mkdir(dir, 0o700))
	program, marker := t11bConnectionProgram(t, dir, "r8-private-program-marker", "claude-code 8.1.0")
	r8RequireCheckedProgram(t, f, program)
	w := f.call(t, http.MethodPut, t8SettingsRoute,
		t11bSettingsBody(t, gen.CliPreset{Path: program}, gen.CliPreset{}, gen.CliPreset{}), "person")
	require.Equal(t, http.StatusOK, w.Code, "instrument: person saves the checked program: %s", w.Body.String())
	require.Equal(t, program, f.api.agentLoop.GetConfig().Sandbox.ThirdPartyCLI("claude-code").Path)
	t11cSetConfig(t, f, func(cfg *config.Config) {
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: "r8-diagnostic-worker",
			Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{
				Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"}}})
	})
	return f, program, marker
}

func r8RequireCheckedProgram(t *testing.T, f *t8SettingsFixture, program string) {
	t.Helper()
	roots, rootsErr := thirdpartycli.AgentWritableRoots(f.api.homePath, f.api.agentLoop.GetConfig())
	require.NoError(t, rootsErr)
	resolved, err := thirdpartycli.CheckProgramPath("claude-code", program, roots, rootsErr)
	require.NoError(t, err, "instrument: this failure occurs after the actual location check")
	want, err := filepath.EvalSymlinks(program)
	require.NoError(t, err)
	require.Equal(t, want, resolved)
}

func r8RunnerResponse(t *testing.T, f *t8SettingsFixture) (*httptest.ResponseRecorder, gen.RunnerTestResponse) {
	t.Helper()
	w := f.call(t, http.MethodPost, "/api/v1/agents/r8-diagnostic-worker/runner/test", nil, "person")
	require.Equal(t, http.StatusOK, w.Code, "runner diagnostic remains an HTTP 200 result: %s", w.Body.String())
	var result gen.RunnerTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	return w, result
}

func r8AssertRefusalClass(t *testing.T, text, check string) {
	t.Helper()
	assert.Regexp(t, `(?i)claude[ -]code`, text, "Q48: program identity remains visible")
	assert.Regexp(t, `\b`+check+`\b`, text, "Q48: exact failure class remains visible")
}

// Inspect raw bytes AND every decoded JSON string, including unknown fields.
// Encoding a private diagnostic or moving it to another field is not removal.
func r8AssertCleanDiagnosticBody(t *testing.T, raw []byte, private ...string) {
	t.Helper()
	r5AssertClean(t, string(raw), private...)
	var decoded any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	var inspect func(any)
	inspect = func(value any) {
		switch value := value.(type) {
		case string:
			r5AssertClean(t, value, private...)
		case map[string]any:
			for key, entry := range value {
				r5AssertClean(t, key, private...)
				inspect(entry)
			}
		case []any:
			for _, entry := range value {
				inspect(entry)
			}
		}
	}
	inspect(decoded)
}
