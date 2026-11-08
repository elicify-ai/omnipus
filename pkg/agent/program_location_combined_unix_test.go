//go:build !windows

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/fixtures/t11combined"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type t11cNativeFixture struct { // not-wire-format: actual native-driver test fixture
	al                    *AgentLoop
	ts                    *turnState
	ctx                   context.Context
	home, program, marker string
	beforeCalls           [][]string
	beforeStarts          []byte
}

func t11cNativeFixtureFor(t *testing.T, transform func(string) string) *t11cNativeFixture {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Setenv(config.EnvHome, home)
	al, ts := newExternalTestLoop(t, "codex", "")
	ts.agent.MaxIterations = extTestEffectiveLimit
	ws, found := workspace.FindForAgent(home, ts.agent.ID)
	require.True(t, found, "instrument: actual dispatching agent has a real workspace")
	ts.opts.WorkspaceID = ws
	program, marker := t11bNativeProgram(t, t11bNativeCLIs()[1], "combined-codex")
	if transform != nil {
		program = transform(program)
	}
	t11bSaveRunSettings(t, al, "codex", program, "")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second) // established native-driver fixture bound
	t.Cleanup(cancel)
	_, runErr := runExternalCLISubTurn(ctx, al, ts, "combined positive native conversation")
	require.NoError(t, runErr, "instrument: real native subprocess must succeed before the location change")
	calls := t11bNativeCalls(t, marker)
	require.Len(t, calls, 1, "instrument: native executable recorded its own work argv")
	starts, err := os.ReadFile(marker + ".all")
	require.NoError(t, err, "instrument: all starts, including --version, are recorded")
	require.NotEmpty(t, starts)
	return &t11cNativeFixture{al: al, ts: ts, ctx: ctx, home: home, program: program,
		marker: marker, beforeCalls: calls, beforeStarts: starts}
}

func t11cMutateRunConfig(t *testing.T, f *t11cNativeFixture, change func(*config.Config)) {
	t.Helper()
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		change(cfg)
		return config.SaveConfig(filepath.Join(f.home, "config.json"), cfg)
	}))
}

func t11cAssertNativeRefused(t *testing.T, f *t11cNativeFixture, check string) {
	t.Helper()
	f.ts.opts.ExternalCLIResume = true
	_, runErr := runExternalCLISubTurn(f.ctx, f.al, f.ts, "must refuse before every native start")
	if assert.Error(t, runErr, "combined root rule must reach actual dispatch") {
		text := runErr.Error()
		assert.Regexp(t, `^This agent cannot run: `, text)
		assert.Contains(t, text, "("+check+" at ", "actual refusal names the failed check")
		if check == "location_unverifiable" {
			assert.Contains(t, text, t11combined.LocationUnverifiableCause, "Q37 A: exact approved cause clause")
			assert.Contains(t, text, "Details are in the server log.")
			assert.NotContains(t, text, "Omnipus could not read the list of mounted folders")
			assert.NotContains(t, text, "agent_writable_location", "unknown is not proven writable")
		}
	}
	assert.Equal(t, f.beforeCalls, t11bNativeCalls(t, f.marker), "no extra real native work process")
	after, err := os.ReadFile(f.marker + ".all")
	require.NoError(t, err)
	assert.Equal(t, f.beforeStarts, after, "not even a version probe may start before refusal")
}

func t11cNativeRecovery(t *testing.T, f *t11cNativeFixture) {
	t.Helper()
	before := len(t11bNativeCalls(t, f.marker))
	_, err := runExternalCLISubTurn(f.ctx, f.al, f.ts, "same actual fixture after removing the forbidden condition")
	require.NoError(t, err, "control: refusal must not be a broken driver or sticky unrelated error")
	require.Len(t, t11bNativeCalls(t, f.marker), before+1, "control: exactly one further actual work process")
}

func TestCombinedProgramLocation_PerAgentHome_ActualDispatch_R1(t *testing.T) {
	f := t11cNativeFixtureFor(t, nil)
	n := len(f.al.GetConfig().Agents.List)
	t11cMutateRunConfig(t, f, func(cfg *config.Config) {
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: f.ts.agent.ID, Home: filepath.Dir(f.program)})
	})
	t.Run("forbidden_home", func(t *testing.T) { t11cAssertNativeRefused(t, f, "agent_writable_location") })
	t11cMutateRunConfig(t, f, func(cfg *config.Config) { cfg.Agents.List = cfg.Agents.List[:n] })
	t.Run("home_removed_control", func(t *testing.T) { t11cNativeRecovery(t, f) })
}

func TestCombinedProgramLocation_DefaultAgentHome_ActualDispatch_R2(t *testing.T) {
	f := t11cNativeFixtureFor(t, nil)
	prior := f.al.GetConfig().Agents.Defaults.Home
	t11cMutateRunConfig(t, f, func(cfg *config.Config) { cfg.Agents.Defaults.Home = filepath.Dir(f.program) })
	t.Run("forbidden_home", func(t *testing.T) { t11cAssertNativeRefused(t, f, "agent_writable_location") })
	t11cMutateRunConfig(t, f, func(cfg *config.Config) { cfg.Agents.Defaults.Home = prior })
	t.Run("home_removed_control", func(t *testing.T) { t11cNativeRecovery(t, f) })
}

func TestCombinedProgramLocation_UnsafeWorkspace_ActualDispatch_R6(t *testing.T) {
	f := t11cNativeFixtureFor(t, nil)
	const id = "unsafe..workspace"
	_, err := workspace.SafeWorkspaceDir(f.home, id)
	require.ErrorIs(t, err, workspace.ErrInvalidWorkspaceID)
	record := filepath.Join(f.home, "workspaces", id+".json")
	require.NoError(t, os.WriteFile(record, []byte(`{"id":"unsafe..workspace","name":"Unsafe fixture"}`), 0o600))
	ids, err := workspace.ListWorkspaceIDs(f.home)
	require.NoError(t, err)
	require.Contains(t, ids, id, "instrument: real discovery exposes this unsafe record")
	t.Run("unsafe_record_Q37", func(t *testing.T) { t11cAssertNativeRefused(t, f, "location_unverifiable") })
	require.NoError(t, os.Remove(record))
	t.Run("record_removed_control", func(t *testing.T) { t11cNativeRecovery(t, f) })
}
