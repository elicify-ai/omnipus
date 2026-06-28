//go:build goolm && stdjson

package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestNewOmnipusCommand_KeptCommandsPresent verifies that the minimized command
// tree registers exactly the kept subcommands and no removed verbs.
//
// Kept: onboard, start (+ gateway/g aliases), credentials, audit, doctor, version.
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
		"start",
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
// is not registered as a subcommand (cobra will return "unknown command" at
// runtime). We test the absence by checking the subcommand list.
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
