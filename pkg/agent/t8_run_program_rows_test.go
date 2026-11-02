package agent

// Oracle: tool-parity r17 scenarios 6.7, 6.8, 8.14, 9.9-9.14 and 9.17-9.19
// (areas 4 and 7b: the run program comes only from Settings, is re-checked at
// every run, and the person's preset follows the agent arguments).
//
// These rows live in pkg/agent, not pkg/gateway: the driver factory seam
// (newExternalDriver) is unexported, so only this package can observe the
// RunOptions a real dispatch hands the driver. The fixture is the real
// L11274 one: real config, real create_agent and get_agent, real delegate,
// real Settings row; only the external CLI process edge is faked. The argv
// order itself is proved in pkg/agent/runner/t8_preset_argv_test.go.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
)

const t8ProgramWorker = "t8-program-worker"

func t8RunFixture(t *testing.T) *l11274Fixture {
	t.Helper()
	f := newL11274Fixture(t, "", false)
	t8WireRealAgentTools(t, f.al, f.home)
	return f
}

func t8GetAgent(t *testing.T, f *l11274Fixture, id string) map[string]any {
	t.Helper()
	res := f.execute(t, "get_agent", map[string]any{"id": id, "fields": []any{"all"}})
	require.NotNil(t, res)
	require.False(t, res.IsError, "get_agent refused: %s", res.ForLLM)
	return l11274ResultObject(t, res)
}

func t8CreateCodexWorker(t *testing.T, f *l11274Fixture) (string, map[string]any) {
	t.Helper()
	res := f.execute(t, "create_agent", l11274CreateArgs("Program W", "codex"))
	require.NotNil(t, res)
	require.False(t, res.IsError, "create_agent refused: %s", res.ForLLM)
	body := l11274ResultObject(t, res)
	id, _ := body["id"].(string)
	require.NotEmpty(t, id)
	return id, body
}

// t8RunOptions delegates to the worker and returns the options the real
// dispatch handed the driver.
func t8RunOptions(t *testing.T, f *l11274Fixture, id string) runner.RunOptions {
	t.Helper()
	fake, _ := l11274DriverSeam(t, f)
	res := l11274Delegate(t, f, id, "t8 run program row", true)
	require.NotNil(t, res)
	require.False(t, res.IsError, "delegation must be admitted: %s", res.ForLLM)
	select {
	case opts := <-fake.runs:
		return opts
	case <-time.After(5 * time.Second):
		t.Fatal("the run never reached the driver")
	}
	return runner.RunOptions{}
}

func t8NoRunReachedDriver(t *testing.T, f *l11274Fixture, id string) string {
	t.Helper()
	fake, _ := l11274DriverSeam(t, f)
	refusal := l11274DelegateAndReadRefusal(t, f, id, "t8 must not run")
	assert.Empty(t, fake.RecordedRunOpts(), "a refused run never reaches the driver")
	return refusal
}

func t8Symlink(t *testing.T, link, target string) {
	t.Helper()
	// No platform skips this row (round 6, CK-3). Windows creates a real link
	// too; when the account may not create links (needs Developer Mode or the
	// symlink privilege) the prerequisite fails loudly instead of the row
	// disappearing from the run.
	_ = os.Remove(link)
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Fatalf("PREREQUISITE FAILED: the run-program symlink rows (r17 9.9-9.11) need real symlinks, "+
				"but os.Symlink(%q -> %q) failed on Windows: %v. Enable Developer Mode or grant "+
				"SeCreateSymbolicLinkPrivilege to the test account; do not skip this row.", link, target, err)
		}
		require.NoError(t, err)
	}
}

func t8SetPreset(t *testing.T, f *l11274Fixture, cli, preset string) {
	t.Helper()
	require.NoError(t, f.al.MutateConfig(func(c *config.Config) error {
		row := c.Sandbox.ThirdPartyAgents.CLIs[cli]
		row.PresetArgs = preset
		c.Sandbox.ThirdPartyAgents.CLIs[cli] = row
		return nil
	}))
}

// 6.7: get_agent reports the Settings program, no warning, no soul.
func TestGetAgent_ExternalWorker_SettingsProgram_6_7(t *testing.T) {
	f := t8RunFixture(t)
	program := l11274Executable(t, f.home, "codex", true)
	f.setProgram(t, "codex", program)
	id, _ := t8CreateCodexWorker(t, f)

	view := t8GetAgent(t, f, id)
	executor, _ := view["executor"].(map[string]any)
	require.NotNil(t, executor, "get_agent returns the executor: %v", view)
	assert.Equal(t, program, executor["effective_cli_path"])
	assert.NotContains(t, view, "program_warning")
	assert.NotContains(t, view, "soul")
	assert.Equal(t, true, view["runnable"])
}

// 6.8 and 9.14: with no saved path the worker is created, is not runnable and
// carries the exact NoPath warning, on create and on a later get_agent.
func TestGetAgent_ExternalWorker_NoSettingsPath_6_8_9_14(t *testing.T) {
	f := t8RunFixture(t)
	f.setProgram(t, "codex", "")
	id, created := t8CreateCodexWorker(t, f)

	want := thirdpartycli.NoPathText("codex")
	assert.Equal(t, want, created["program_warning"], "9.14 create reports the NoPath warning")
	assert.NotEqual(t, true, created["runnable"], "9.14 the new worker is not runnable")

	view := t8GetAgent(t, f, id)
	assert.Equal(t, want, view["program_warning"], "6.8 get_agent reports the NoPath warning")
	assert.NotEqual(t, true, view["runnable"])
}

// 8.14: creating a codex worker with env and cli_args while Settings has a
// path yields a runnable worker, no soul, env names only.
func TestCreateAgent_ExternalWorkerWithEnvAndArgs_8_14(t *testing.T) {
	f := t8RunFixture(t)
	f.setProgram(t, "codex", l11274Executable(t, f.home, "codex", true))
	args := l11274CreateArgs("Env W", "codex")
	args["env_overrides"] = map[string]any{"T8_ENV_NAME": "t8-secret-value-0001"}
	args["cli_args"] = "--model gpt-x"
	res := f.execute(t, "create_agent", args)
	require.NotNil(t, res)
	require.False(t, res.IsError, "create_agent refused: %s", res.ForLLM)
	body := l11274ResultObject(t, res)
	assert.Equal(t, true, body["runnable"])
	assert.NotContains(t, body, "soul")
	assert.NotContains(t, res.ForLLM, "t8-secret-value-0001", "the env value never appears, names only")
	id, _ := body["id"].(string)
	view := t8GetAgent(t, f, id)
	assert.NotContains(t, view, "soul")
	assert.NotContains(t, mustJSON(t, view), "t8-secret-value-0001")
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return string(raw)
}

// 9.9: the saved link is resolved anew at dispatch, so a retarget is followed.
// 9.11 observable half: the program handed to the driver is the resolved file,
// not the entered link. (There is no production hook to swap the target between
// the check and the exec, so the swap itself is not exercised.)
func TestSpawn_ResolveCheckExec_NoFallback_Retarget_9_9(t *testing.T) {
	f := t8RunFixture(t)
	v1 := l11274Executable(t, f.home, "codex", true)
	v2 := l11274Executable(t, f.home, "codex", true)
	link := filepath.Join(t.TempDir(), "codex-link")
	t8Symlink(t, link, v1)
	f.setProgram(t, "codex", link)
	id, _ := t8CreateCodexWorker(t, f)

	t8Symlink(t, link, v2)
	opts := t8RunOptions(t, f, id)
	want, err := filepath.EvalSymlinks(v2)
	require.NoError(t, err)
	assert.Equal(t, want, opts.CLIPath, "9.9 the retargeted v2 is executed")
	assert.NotEqual(t, link, opts.CLIPath, "9.11 the checked resolved path, not the entered link, is executed")
}

// 9.10: a link later retargeted into an agent-writable workspace is refused
// with CheckFailed text and nothing runs.
func TestSpawn_ResolveCheckExec_NoFallback_UnsafeRetarget_9_10(t *testing.T) {
	f := t8RunFixture(t)
	safe := l11274Executable(t, f.home, "codex", true)
	link := filepath.Join(t.TempDir(), "codex-link")
	t8Symlink(t, link, safe)
	f.setProgram(t, "codex", link)
	id, _ := t8CreateCodexWorker(t, f)

	workDir := filepath.Join(f.home, "workspaces", l11274WorkspaceID, "work")
	require.NoError(t, os.MkdirAll(workDir, 0o700))
	unsafe := filepath.Join(workDir, "codex")
	require.NoError(t, os.WriteFile(unsafe, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	t8Symlink(t, link, unsafe)

	refusal := t8NoRunReachedDriver(t, f, id)
	assert.Contains(t, refusal, thirdpartycli.CheckAgentWritableLocation, "names the failed check")
	resolvedUnsafe, err := filepath.EvalSymlinks(unsafe)
	require.NoError(t, err)
	assert.Contains(t, refusal, resolvedUnsafe, "names the path that failed the check (the resolved target)")
}

// 9.12: an empty Settings path refuses even with a stored legacy cli_path to a
// real executable and a codex on PATH: no fallback of either kind.
func TestSpawn_ResolveCheckExec_NoFallback_EmptyPath_9_12(t *testing.T) {
	f := t8RunFixture(t)
	f.setProgram(t, "codex", "")
	legacy := l11274Executable(t, f.home, "codex", true)
	onPath := l11274Executable(t, f.home, "codex", true)
	t.Setenv("PATH", filepath.Dir(onPath)+string(os.PathListSeparator)+os.Getenv("PATH"))
	l11274SeedWorker(t, f, t8ProgramWorker, &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "codex", CLIPath: legacy})

	refusal := t8NoRunReachedDriver(t, f, t8ProgramWorker)
	assert.Equal(t, thirdpartycli.NoPathText("codex"), refusal)
}

// 9.13: the delegating parent receives the NoPath text as the tool error.
func TestSpawn_ResolveCheckExec_NoFallback_ParentGetsRefusal_9_13(t *testing.T) {
	f := t8RunFixture(t)
	f.setProgram(t, "codex", "")
	l11274SeedWorker(t, f, t8ProgramWorker, &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "codex"})
	fake, _ := l11274DriverSeam(t, f)

	res := l11274Delegate(t, f, t8ProgramWorker, "t8 parent must be told", true)
	require.NotNil(t, res)
	assert.True(t, res.IsError, "9.13 the delegation is a tool error, not an admitted run: %s", res.ForLLM)
	assert.Contains(t, res.ForLLM, thirdpartycli.NoPathText("codex"))
	f.al.drainSteeredTurns(30 * time.Second)
	assert.Empty(t, fake.RecordedRunOpts())
	records, err := f.al.GetSessionLifecycleStore().List(session.LifecycleFilter{AgentID: t8ProgramWorker})
	require.NoError(t, err)
	assert.Empty(t, records, "no child session was created for the refused delegation")
}

// 9.17: the preset (with a flag the filter drops from an agent) reaches the
// driver unfiltered in PresetArgs while the agent copy stays in CLIArgs for the
// driver to filter; the external_cli.run audit records the bypass.
func TestSpawn_PresetUnfiltered_AuditBypass_9_17(t *testing.T) {
	f := t8RunFixture(t)
	f.setProgram(t, "codex", l11274Executable(t, f.home, "codex", true))
	const bypass = "--dangerously-bypass-approvals-and-sandbox"
	t8SetPreset(t, f, "codex", bypass)
	auditDir := t.TempDir()
	f.al.auditLogger = newAuditLoggerForCancelTest(t, auditDir)
	l11274SeedWorker(t, f, t8ProgramWorker, &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "codex", CLIArgs: bypass})

	opts := t8RunOptions(t, f, t8ProgramWorker)
	assert.Equal(t, []string{bypass}, opts.PresetArgs, "the preset is handed over unfiltered")
	assert.Equal(t, []string{bypass}, opts.CLIArgs, "the agent copy is the driver's to drop")

	require.NoError(t, f.al.auditLogger.Close())
	data, err := os.ReadFile(filepath.Join(auditDir, "audit.jsonl"))
	require.NoError(t, err)
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil || rec["event"] != audit.EventExternalCLIRun {
			continue
		}
		details, _ := rec["fields"].(map[string]any)
		found = true
		assert.Equal(t, true, details["filter_bypassed_by_person_preset"])
		assert.Equal(t, bypass, details["preset_args"])
		assert.Equal(t, "codex", details["cli"])
		assert.Equal(t, t8ProgramWorker, details["agent_id"])
	}
	assert.True(t, found, "an external_cli.run audit entry is written")
}

// 9.18: preset --model a and agent --model b: both reach the driver, the agent
// in CLIArgs and the preset in PresetArgs (which every driver appends last).
func TestSpawn_PresetAfterAgentArgs_9_18(t *testing.T) {
	f := t8RunFixture(t)
	f.setProgram(t, "codex", l11274Executable(t, f.home, "codex", true))
	t8SetPreset(t, f, "codex", "--model a")
	l11274SeedWorker(t, f, t8ProgramWorker, &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "codex", CLIArgs: "--model b"})

	opts := t8RunOptions(t, f, t8ProgramWorker)
	assert.Equal(t, []string{"--model", "b"}, opts.CLIArgs)
	assert.Equal(t, []string{"--model", "a"}, opts.PresetArgs)
}

// 9.19: the preset is tokenised exactly as cli_args is.
func TestSpawn_PresetTokenisedLikeCLIArgs_9_19(t *testing.T) {
	f := t8RunFixture(t)
	f.setProgram(t, "codex", l11274Executable(t, f.home, "codex", true))
	const raw = `--flag "two words"`
	t8SetPreset(t, f, "codex", raw)
	l11274SeedWorker(t, f, t8ProgramWorker, &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "codex", CLIArgs: raw})

	opts := t8RunOptions(t, f, t8ProgramWorker)
	assert.Equal(t, []string{"--flag", "two words"}, opts.PresetArgs)
	assert.Equal(t, opts.CLIArgs, opts.PresetArgs, "same parser, same tokens")
}
