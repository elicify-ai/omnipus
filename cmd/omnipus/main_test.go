//go:build goolm && stdjson

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/cmd/omnipus/internal/run"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestNewOmnipusCommand_KeptCommandsPresent verifies that the minimized command
// tree registers exactly the kept subcommands and no removed verbs.
//
// Kept: onboard, start (+ gateway/g aliases), stop, credentials, audit, doctor,
// records, version.
// Removed: agent, auth, status, cron, migrate, model, skills.
func TestNewOmnipusCommand_KeptCommandsPresent(t *testing.T) {
	cmd := NewOmnipusCommand()
	require.NotNil(t, cmd)

	assert.Equal(t, "omnipus", cmd.Use)
	assert.True(t, cmd.HasSubCommands())

	// Canonical subcommand names (no aliases).
	wantCommands := []string{
		"audit",
		"credentials",
		"doctor",
		"onboard",
		// "records" (omnipus records import-obsidian) was added deliberately in
		// 9b0280dd4 ("feat(records): add the Obsidian-vault importer (FR-100)")
		// as a one-shot operator/CLI command, per FR-100/FR-103 — never an agent
		// tool. It belongs in the kept-command list, not the removed-verb list.
		"records",
		"start",
		"stop",
		"version",
	}

	gotNames := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		gotNames = append(gotNames, sub.Name())
	}

	assert.Len(t, gotNames, len(wantCommands),
		"unexpected subcommand count; got %v, want %v", gotNames, wantCommands)

	for _, want := range wantCommands {
		assert.True(t, slices.Contains(gotNames, want),
			"expected subcommand %q to be present; got %v", want, gotNames)
	}
}

// TestNewOmnipusCommand_RemovedVerbsAreUnknown verifies that each removed verb
// is not registered as a subcommand. Because the root uses cobra.ArbitraryArgs,
// typing a removed verb falls into RunE rather than cobra's own "unknown command"
// handler; RunE prints a dedicated redesign-removal message instead. We assert
// here only that the verbs are absent from the registered subcommand list.
func TestNewOmnipusCommand_RemovedVerbsAreUnknown(t *testing.T) {
	cmd := NewOmnipusCommand()
	require.NotNil(t, cmd)

	removedVerbs := []string{"agent", "auth", "status", "cron", "migrate", "model", "skills"}

	registeredNames := make([]string, 0, len(cmd.Commands())*2)
	for _, sub := range cmd.Commands() {
		registeredNames = append(registeredNames, sub.Name())
		registeredNames = append(registeredNames, sub.Aliases...)
	}

	for _, verb := range removedVerbs {
		assert.False(t, slices.Contains(registeredNames, verb),
			"removed verb %q must not be registered", verb)
	}
}

// TestRemovedVerbPrintsDesignMessage verifies that when the user types a removed
// verb (e.g. "status"), the removedVerbs guard in RunE would fire — NOT the
// confusing "unknown agent" error that would appear if the verb reached the
// agent-lookup path (US-11/AC-1). Because RunE calls os.Exit(1) we cannot
// call cmd.Execute() in a unit test, so we assert the map membership that
// controls the guard directly.
func TestRemovedVerbPrintsDesignMessage(t *testing.T) {
	// Confirm that each removed verb is in the map the RunE guard consults.
	// TestRemovedVerbMessage_DirectMapCheck covers the same map for kept verbs.
	for _, verb := range []string{"agent", "auth", "status", "cron", "migrate", "model", "skills"} {
		t.Run(verb, func(t *testing.T) {
			assert.True(t, removedVerbs[verb],
				"removedVerbs map must contain %q so RunE prints the redesign message", verb)
		})
	}
}

// TestRemovedVerbMessage_DirectMapCheck is a direct table test of the
// removedVerbs map used by RunE. If a verb is in the map, RunE will print
// the removal message and exit non-zero before trying the agent-lookup.
func TestRemovedVerbMessage_DirectMapCheck(t *testing.T) {
	wantIn := []string{"agent", "auth", "status", "cron", "migrate", "model", "skills"}
	wantOut := []string{"onboard", "start", "credentials", "audit", "doctor", "version", "gateway", "jim", "mia"}

	for _, v := range wantIn {
		assert.True(t, removedVerbs[v], "expected %q in removedVerbs", v)
	}
	for _, v := range wantOut {
		assert.False(t, removedVerbs[v], "did not expect %q in removedVerbs", v)
	}
}

// TestNewOmnipusCommand_GatewayAliasResolvesToStart verifies that the `gateway`
// and `g` aliases are preserved on the `start` subcommand (US-11/AC-3).
func TestNewOmnipusCommand_GatewayAliasResolvesToStart(t *testing.T) {
	cmd := NewOmnipusCommand()
	require.NotNil(t, cmd)

	var startCmd *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "start" {
			startCmd = sub
			break
		}
	}
	require.NotNil(t, startCmd, "start subcommand must be registered")

	assert.True(t, startCmd.HasAlias("gateway"), "gateway alias must be kept on start")
	assert.True(t, startCmd.HasAlias("g"), "g alias must be kept on start")
}

// TestNewOmnipusCommand_RootHasRunE verifies that the root command has a RunE
// handler (the positional execute path) and declares the expected flags.
func TestNewOmnipusCommand_RootHasRunE(t *testing.T) {
	cmd := NewOmnipusCommand()
	require.NotNil(t, cmd)

	assert.NotNil(t, cmd.RunE, "root command must have RunE for the positional run path")

	assert.NotNil(t, cmd.Flags().Lookup("model"), "--model flag must be declared")
	assert.NotNil(t, cmd.Flags().Lookup("yes"), "--yes flag must be declared")
	assert.NotNil(t, cmd.Flags().Lookup("timeout"), "--timeout flag must be declared")
	assert.NotNil(t, cmd.Flags().Lookup("url"), "--url flag must be declared")
}

// TestNewOmnipusCommand_HelpTextIncludesExecuteForm verifies that the root
// Long / Example contain the execute form and do not contain removed verbs
// (FR-009/US-7).
func TestNewOmnipusCommand_HelpTextIncludesExecuteForm(t *testing.T) {
	cmd := NewOmnipusCommand()
	require.NotNil(t, cmd)

	helpText := cmd.Long + "\n" + cmd.Example

	// Must contain the execute form.
	assert.Contains(t, helpText, `<agent>`, "help must document the execute form")
	assert.Contains(t, helpText, "onboard", "help must document the onboard command")
	assert.Contains(t, helpText, "start", "help must document the start command")
	assert.Contains(t, helpText, "credentials", "help must document the credentials command")

	// Removed verbs must not appear as subcommand entries.
	// We check for the command-listing patterns that would indicate registration,
	// not incidental occurrences of the words as nouns (e.g. "a named agent").
	// The pattern "  <verb> " (two-space indent) is used in the Commands block.
	for _, verb := range []string{"auth", "status", "cron", "migrate"} {
		assert.NotContains(t, helpText, "  "+verb+" ", "removed verb %q must not appear as a command in help", verb)
	}
}

// --------------------------------------------------------------------------
// Roster builder tests (FR-008/US-6)
// --------------------------------------------------------------------------

// TestBuildRoster_ExcludesWorkers verifies that buildRoster filters out worker
// agents and only returns chat-target agents (IsChatTarget==true).
func TestBuildRoster_ExcludesWorkers(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCore},
				{ID: "jim", Name: "Jim", Type: config.AgentTypeCustom},
				{ID: "worker1", Name: "Worker One", Type: config.AgentTypeWorker},
			},
		},
	}

	roster := buildRoster(cfg)

	require.Len(t, roster, 2, "only the two chat-target agents should be in the roster")
	assert.Equal(t, "mia", roster[0].ID)
	assert.Equal(t, "jim", roster[1].ID)

	for _, r := range roster {
		assert.NotEqual(t, "worker1", r.ID, "worker must be excluded from roster")
	}
}

// TestBuildRoster_EmptyConfig verifies that an empty agent list returns an
// empty roster (no panic).
func TestBuildRoster_EmptyConfig(t *testing.T) {
	cfg := &config.Config{}
	roster := buildRoster(cfg)
	assert.Empty(t, roster)
}

// TestBuildRoster_AllWorkers verifies that when all agents are workers the
// roster is empty.
func TestBuildRoster_AllWorkers(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "bot1", Name: "Bot 1", Type: config.AgentTypeWorker},
				{ID: "bot2", Name: "Bot 2", Type: config.AgentTypeWorker},
			},
		},
	}
	roster := buildRoster(cfg)
	assert.Empty(t, roster, "all-worker config must produce an empty roster")
}

// TestBuildRoster_PreservesOrder verifies that the roster order mirrors the
// config list order.
func TestBuildRoster_PreservesOrder(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "ava", Name: "Ava", Type: config.AgentTypeCore},
				{ID: "ray", Name: "Ray", Type: config.AgentTypeCustom},
				{ID: "worker", Name: "Wk", Type: config.AgentTypeWorker},
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCore},
			},
		},
	}
	roster := buildRoster(cfg)
	require.Len(t, roster, 3)
	assert.Equal(t, "ava", roster[0].ID)
	assert.Equal(t, "ray", roster[1].ID)
	assert.Equal(t, "mia", roster[2].ID)
}

// --------------------------------------------------------------------------
// Help-text (FR-009)
// --------------------------------------------------------------------------

// TestHelpText_ExamplesPresent verifies that the root Long has at least two
// examples containing the execute form (FR-009 AC-1).
func TestHelpText_ExamplesPresent(t *testing.T) {
	cmd := NewOmnipusCommand()
	examples := cmd.Example
	// Count lines in examples that look like execute-form usage.
	var execLines int
	for _, line := range strings.Split(examples, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "omnipus ") && strings.Contains(line, `"`) {
			execLines++
		}
	}
	assert.GreaterOrEqual(t, execLines, 2, "at least 2 execute-form examples required in --help (FR-009)")
}

// --------------------------------------------------------------------------
// Auto-start precondition tests (FR-016)
// --------------------------------------------------------------------------

// TestHasNonInteractiveKeyMode_EnvVars verifies that hasNonInteractiveKeyMode
// returns true when OMNIPUS_MASTER_KEY or OMNIPUS_KEY_FILE is set, and false
// when neither is set and no master.key file exists in home.
func TestHasNonInteractiveKeyMode_EnvVars(t *testing.T) {
	home := t.TempDir() // empty — no master.key

	// Base case: no env vars, no file → false.
	t.Setenv("OMNIPUS_MASTER_KEY", "")
	t.Setenv("OMNIPUS_KEY_FILE", "")
	assert.False(t, hasNonInteractiveKeyMode(home),
		"no env vars and no master.key → must return false")

	// OMNIPUS_MASTER_KEY set → true.
	t.Setenv("OMNIPUS_MASTER_KEY", "aabbccdd")
	assert.True(t, hasNonInteractiveKeyMode(home),
		"OMNIPUS_MASTER_KEY set → must return true")
	t.Setenv("OMNIPUS_MASTER_KEY", "")

	// OMNIPUS_KEY_FILE set → true.
	t.Setenv("OMNIPUS_KEY_FILE", "/some/path/master.key")
	assert.True(t, hasNonInteractiveKeyMode(home),
		"OMNIPUS_KEY_FILE set → must return true")
	t.Setenv("OMNIPUS_KEY_FILE", "")
}

// TestHasNonInteractiveKeyMode_MasterKeyFile verifies that hasNonInteractiveKeyMode
// returns true when <home>/master.key exists on disk, even without env vars.
func TestHasNonInteractiveKeyMode_MasterKeyFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", "")
	t.Setenv("OMNIPUS_KEY_FILE", "")

	// No file yet → false.
	assert.False(t, hasNonInteractiveKeyMode(home),
		"no master.key on disk → must return false")

	// Create the master.key file.
	keyPath := filepath.Join(home, "master.key")
	if err := os.WriteFile(keyPath, []byte("dummykeycontents"), 0o600); err != nil {
		t.Fatalf("write master.key: %v", err)
	}

	// File present → true.
	assert.True(t, hasNonInteractiveKeyMode(home),
		"master.key present → must return true")
}

// TestNewOmnipusCommand_StopSubcommandPresent verifies that the stop subcommand
// is registered in the command tree with the correct name.
func TestNewOmnipusCommand_StopSubcommandPresent(t *testing.T) {
	cmd := NewOmnipusCommand()
	require.NotNil(t, cmd)

	var stopCmd *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "stop" {
			stopCmd = sub
			break
		}
	}
	require.NotNil(t, stopCmd, "stop subcommand must be registered")
	assert.Equal(t, "stop", stopCmd.Name())
}

// --------------------------------------------------------------------------
// Auto-start orchestration seam tests (FR-016 / MAJOR-1)
// --------------------------------------------------------------------------
//
// These tests exercise autoStartAndRetryWith with fully injected status, spawn,
// and poller functions. No real daemon processes are started; the tests verify
// the decision-tree branches without side effects.

// stubPoller is a no-op poller that always reports the gateway as ready.
func stubPoller(_ context.Context, _, _, _ string, _ int) error {
	return nil
}

// stubSpawn records calls and returns a fixed PID or error.
type stubSpawnRecorder struct {
	called bool
	retPID int
	retErr error
}

func (s *stubSpawnRecorder) spawn(home string, args []string) (int, error) {
	s.called = true
	return s.retPID, s.retErr
}

// TestAutoStart_StatusRunning_NoSpawn verifies that when daemon.Status reports
// the gateway is already running, autoStartAndRetryWith does NOT spawn a new
// process (MAJOR-1 fix). The ErrGatewayDown from the original dial was a
// transient error, not connection-refused.
func TestAutoStart_StatusRunning_NoSpawn(t *testing.T) {
	home := t.TempDir()

	spawnRec := &stubSpawnRecorder{retPID: 9999}

	statusRunning := func(_ string) (bool, int, error) {
		return true, 1234, nil // gateway is alive
	}

	result := autoStartAndRetryWith(
		context.Background(),
		home, "localhost:9999", "tok",
		func(_ string) bool { return true }, // key mode: yes
		run.Options{},
		statusRunning,
		spawnRec.spawn,
		stubPoller,
	)

	assert.False(t, spawnRec.called,
		"spawn must NOT be called when daemon.Status reports the gateway is running")
	assert.NotNil(t, result.orchErr,
		"orchErr must be set — transient dial error, not a down gateway")
}

// TestAutoStart_NoKeyMode_NoSpawn verifies that when no non-interactive key
// mode is available, autoStartAndRetryWith returns an orchErr and does NOT
// spawn (the user must run `omnipus start` manually).
func TestAutoStart_NoKeyMode_NoSpawn(t *testing.T) {
	home := t.TempDir()

	spawnRec := &stubSpawnRecorder{retPID: 9999}

	statusDown := func(_ string) (bool, int, error) {
		return false, 0, nil
	}

	result := autoStartAndRetryWith(
		context.Background(),
		home, "localhost:9999", "tok",
		func(_ string) bool { return false }, // key mode: NO
		run.Options{},
		statusDown,
		spawnRec.spawn,
		stubPoller,
	)

	assert.False(t, spawnRec.called,
		"spawn must NOT be called when no non-interactive key mode is available")
	assert.NotNil(t, result.orchErr,
		"orchErr must be set when key mode is absent")
}

// TestAutoStart_DownAndKeyPresent_SpawnsOnce verifies that when daemon.Status
// reports not-running AND a non-interactive key mode is available, exactly one
// spawn is made and the result reflects success (runErr==nil on a clean run).
func TestAutoStart_DownAndKeyPresent_SpawnsOnce(t *testing.T) {
	home := t.TempDir()

	spawnRec := &stubSpawnRecorder{retPID: 4242}
	spawnCount := 0
	countingSpawn := func(h string, args []string) (int, error) {
		spawnCount++
		return spawnRec.spawn(h, args)
	}

	statusDown := func(_ string) (bool, int, error) {
		return false, 0, nil
	}

	// The retry run.Run will fail because there is no real gateway, but we
	// are only testing that spawn happened exactly once and the poller was
	// called — not that the chat turn succeeded.
	pollerCalled := false
	recordingPoller := func(ctx context.Context, home, addr, token string, pid int) error {
		pollerCalled = true
		assert.Equal(t, 4242, pid, "poller must receive the PID returned by spawn")
		return nil
	}

	result := autoStartAndRetryWith(
		context.Background(),
		home, "localhost:9999", "tok",
		func(_ string) bool { return true }, // key mode: yes
		run.Options{Addr: "localhost:9999", Stdout: nil, Stderr: nil},
		statusDown,
		countingSpawn,
		recordingPoller,
	)

	assert.Equal(t, 1, spawnCount, "spawn must be called exactly once")
	assert.True(t, pollerCalled, "poller must be called after spawn")
	assert.Nil(t, result.orchErr, "orchestration must succeed (orchErr==nil)")
	// runErr from the retry is expected non-nil (no real gateway), that is fine.
	// We are testing the orchestration path, not the actual run.
	_ = result.runErr
}

// TestAutoStart_StatusError_NoSpawn verifies that when daemon.Status returns
// an error (e.g. corrupt PID file), autoStartAndRetryWith does NOT spawn —
// it cannot determine whether an instance is already running.
func TestAutoStart_StatusError_NoSpawn(t *testing.T) {
	home := t.TempDir()

	spawnRec := &stubSpawnRecorder{retPID: 9999}
	statusErr := errors.New("PID file corrupted")

	statusFailing := func(_ string) (bool, int, error) {
		return false, 0, statusErr
	}

	result := autoStartAndRetryWith(
		context.Background(),
		home, "localhost:9999", "tok",
		func(_ string) bool { return true }, // key mode: yes
		run.Options{},
		statusFailing,
		spawnRec.spawn,
		stubPoller,
	)

	assert.False(t, spawnRec.called,
		"spawn must NOT be called when daemon.Status returns an error")
	assert.NotNil(t, result.orchErr,
		"orchErr must be set when status check fails")
	assert.ErrorIs(t, result.orchErr, statusErr,
		"orchErr must wrap the original status error")
}
