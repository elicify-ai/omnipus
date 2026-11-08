//go:build darwin || linux

package gateway

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type t11ProgramIdentityFixture struct { // not-wire-format: filesystem fixture only
	settings                   *t8SettingsFixture
	checked, other             string
	checkedMarker, otherMarker string
}

// Spec §3.2/FR-5: validation and every consumer preserve program identity.
// Fixtures emit a fixed local protocol response and only write local markers.
func t11ProgramIdentity(t *testing.T) *t11ProgramIdentityFixture {
	t.Helper()
	f := t8SettingsAPI(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	dir := t11cCanonicalDir(t)
	other := filepath.Join(dir, "fixture-cli")
	p := &t11ProgramIdentityFixture{settings: f, checked: other + " ", other: other,
		checkedMarker: filepath.Join(dir, "checked-starts"), otherMarker: filepath.Join(dir, "other-starts")}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	for _, fixture := range []struct{ path, marker, label string }{
		{p.checked, p.checkedMarker, "checked"}, {p.other, p.otherMarker, "other"},
	} {
		script := "#!/bin/sh\nset -eu\nprintf '%s\\n' " + quote(fixture.label) + " >> " + quote(fixture.marker) + "\n"
		script += "if [ \"$1\" = '--version' ]; then printf 'codex-cli 1.2.3\\n'; exit 0; fi\n"
		script += "printf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"identity-fixture\"}'\n"
		script += "printf '%s\\n' '{\"type\":\"item.completed\",\"item\":{\"id\":\"answer\",\"type\":\"agent_message\",\"text\":\"2\"}}'\n"
		script += "printf '%s\\n' '{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n"
		require.NoError(t, os.WriteFile(fixture.path, []byte(script), 0o755))
	}
	checkedInfo, err := os.Stat(p.checked)
	require.NoError(t, err)
	otherInfo, err := os.Stat(p.other)
	require.NoError(t, err)
	require.False(t, os.SameFile(checkedInfo, otherInfo), "instrument: two distinct ordinary files")
	w := f.call(t, http.MethodPut, t8SettingsRoute, t8SettingsBody("codex", p.checked, ""), "person")
	require.Equal(t, http.StatusOK, w.Code, "instrument: the signed-in person saves the selected program: %s", w.Body.String())
	require.Equal(t, p.checked, f.api.agentLoop.GetConfig().Sandbox.ThirdPartyCLI("codex").Path)
	roots, rootsErr := thirdPartyAgentWritable(f.api.homePath, f.api.agentLoop.GetConfig())
	require.NoError(t, rootsErr)
	resolved, err := thirdpartycli.CheckProgramPath("codex", p.checked, roots, rootsErr)
	require.NoError(t, err, "instrument: the real shared check accepts the selected file")
	want, err := filepath.EvalSymlinks(p.checked)
	require.NoError(t, err)
	require.Equal(t, want, resolved, "spec §3.2: the checked file is the launch identity")
	require.NotEqual(t, p.other, resolved)
	for _, marker := range []string{p.checkedMarker, p.otherMarker} {
		_, err := os.Stat(marker)
		require.ErrorIs(t, err, fs.ErrNotExist, "instrument: validation starts neither program")
	}
	return p
}

func t11AssertProgramIdentity(t *testing.T, p *t11ProgramIdentityFixture) {
	t.Helper()
	data, err := os.ReadFile(p.checkedMarker)
	if assert.NoError(t, err, "spec §3.2: the checked program must run") {
		assert.NotEmpty(t, data)
		for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			assert.Equal(t, "checked", line, "only the selected fixture writes this marker")
		}
	}
	_, err = os.Stat(p.otherMarker)
	assert.ErrorIs(t, err, fs.ErrNotExist, "spec §3.2: a different program must never run, including version probes")
}

func TestRunnerTestEndpoint_RunsCheckedProgram(t *testing.T) {
	p := t11ProgramIdentity(t)
	f := p.settings
	const id = "identity-fixture"
	t11cSetConfig(t, f, func(cfg *config.Config) {
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: id,
			Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{
				Kind: config.ExecutorKindExternalCLI, CLI: "codex"}}})
	})
	code, resp := postRunnerTest(t, f.api, id)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, gen.RunnerTestResponseReasonUnauthenticated, resp.Reason,
		"instrument: the version handshake completes without any provider credentials")
	require.NotNil(t, resp.CliVersion)
	assert.Equal(t, "1.2.3", *resp.CliVersion)
	t11AssertProgramIdentity(t, p)
}

func TestExecutorSmokeTest_RunsCheckedProgram(t *testing.T) {
	p := t11ProgramIdentity(t)
	raw, err := json.Marshal(gen.ExecutorSmokeTestRequest{Cli: gen.ExternalCliToolCodex})
	require.NoError(t, err)
	code, resp := postExecutorSmokeTest(t, p.settings.api, string(raw))
	require.Equal(t, http.StatusOK, code)
	require.True(t, resp.Ok, "instrument: the real driver completes the fixed local conversation: %+v", resp)
	require.NotNil(t, resp.ResponseText)
	assert.Equal(t, "2", *resp.ResponseText)
	assert.Nil(t, resp.Error)
	t11AssertProgramIdentity(t, p)
}
