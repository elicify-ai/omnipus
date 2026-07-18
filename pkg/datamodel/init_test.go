// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package datamodel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDirectoryInitialization verifies first-run creates the complete directory tree.
// Traces to: wave1-core-foundation-spec.md Scenario: First-run creates complete directory tree (US-1 AC1)
func TestDirectoryInitialization(t *testing.T) {
	home := t.TempDir()
	omnipusHome := filepath.Join(home, ".omnipus")

	require.NoError(t, Init(omnipusHome), "Init must succeed on clean directory")

	// Verify root directory has 0700 permissions.
	info, err := os.Stat(omnipusHome)
	require.NoError(t, err)
	assert.True(t, info.IsDir(), "home must be a directory")
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "home must have 0700 permissions")

	// Verify all required subdirectories exist.
	expectedDirs := []string{
		"agents",
		"workspaces",
		"tasks",
		"pins",
		"channels",
		"skills",
		"backups",
		"system",
		"logs",
	}
	for _, dir := range expectedDirs {
		path := filepath.Join(omnipusHome, dir)
		dirInfo, statErr := os.Stat(path)
		require.NoError(t, statErr, "directory %q must exist", dir)
		assert.True(t, dirInfo.IsDir(), "%q must be a directory", dir)
	}

	// Verify config.json exists.
	configPath := filepath.Join(omnipusHome, "config.json")
	_, err = os.Stat(configPath)
	require.NoError(t, err, "config.json must be created on first run")

	// Verify config.json is valid JSON.
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(data, &cfg), "config.json must be valid JSON")

	// Verify default config structure.
	assert.Contains(t, cfg, "version", "default config must have 'version'")
	assert.Contains(t, cfg, "agents", "default config must have 'agents'")
	assert.Contains(t, cfg, "gateway", "default config must have 'gateway'")

	// Verify system/state.json exists.
	statePath := filepath.Join(omnipusHome, "system", "state.json")
	_, err = os.Stat(statePath)
	require.NoError(t, err, "system/state.json must be created on first run")
}

// TestDirectoryInitPartialExists verifies incomplete directory is completed without overwriting.
// Traces to: wave1-core-foundation-spec.md Scenario: Partial directory is completed without overwriting (US-1 AC2)
func TestDirectoryInitPartialExists(t *testing.T) {
	home := t.TempDir()
	omnipusHome := filepath.Join(home, ".omnipus")

	// Pre-create partial directory structure (missing "tasks", "backups").
	require.NoError(t, os.MkdirAll(filepath.Join(omnipusHome, "agents"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(omnipusHome, "system"), 0o700))

	// Write a pre-existing config.json to verify it is not overwritten.
	preExistingConfig := map[string]any{"version": 1, "custom_field": "must-survive"}
	configPath := filepath.Join(omnipusHome, "config.json")
	data, err := json.MarshalIndent(preExistingConfig, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, data, 0o644))

	// Run Init on the partial directory.
	require.NoError(t, Init(omnipusHome), "Init must succeed on partial directory")

	// Verify previously missing directories are now present.
	for _, dir := range []string{"tasks", "backups", "pins", "skills", "channels", "logs"} {
		path := filepath.Join(omnipusHome, dir)
		dirInfo, statErr := os.Stat(path)
		require.NoError(t, statErr, "directory %q must be created", dir)
		assert.True(t, dirInfo.IsDir(), "%q must be a directory", dir)
	}

	// Verify config.json was NOT overwritten.
	got, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var gotCfg map[string]any
	require.NoError(t, json.Unmarshal(got, &gotCfg))
	assert.Equal(t, "must-survive", gotCfg["custom_field"],
		"pre-existing config.json must NOT be overwritten")
}

// TestDirectoryInitIdempotent verifies Init can be called multiple times safely.
// Traces to: wave1-core-foundation-spec.md Scenario: First-run creates complete directory tree (idempotency)
func TestDirectoryInitIdempotent(t *testing.T) {
	home := t.TempDir()
	omnipusHome := filepath.Join(home, ".omnipus")

	require.NoError(t, Init(omnipusHome))
	require.NoError(t, Init(omnipusHome), "second Init call must be idempotent")
	require.NoError(t, Init(omnipusHome), "third Init call must be idempotent")
}

// TestAgentWorkspaceInitialization verifies agent workspace creates correct subdirectories.
// Traces to: wave1-core-foundation-spec.md Scenario: Agent activation creates workspace (US-7 AC1)
func TestAgentWorkspaceInitialization(t *testing.T) {
	home := t.TempDir()
	omnipusHome := filepath.Join(home, ".omnipus")
	require.NoError(t, Init(omnipusHome))

	agentID := "general-assistant"
	require.NoError(t, InitAgentHome(omnipusHome, agentID))

	base := filepath.Join(omnipusHome, "agents", agentID)

	expectedDirs := []string{
		"sessions",
		"memory",
		filepath.Join("memory", "daily"),
		"skills",
	}
	for _, sub := range expectedDirs {
		path := filepath.Join(base, sub)
		info, err := os.Stat(path)
		require.NoError(t, err, "workspace subdir %q must exist", sub)
		assert.True(t, info.IsDir(), "%q must be a directory", sub)
	}
}

// TestWorkspaceIsolation verifies agent workspace paths are isolated.
// Traces to: wave1-core-foundation-spec.md Scenario: Cross-agent workspace access is denied (US-7 AC2)
func TestWorkspaceIsolation(t *testing.T) {
	home := t.TempDir()
	omnipusHome := filepath.Join(home, ".omnipus")
	require.NoError(t, Init(omnipusHome))

	require.NoError(t, InitAgentHome(omnipusHome, "agent-a"))
	require.NoError(t, InitAgentHome(omnipusHome, "agent-b"))

	workspaceA := AgentHomePath(omnipusHome, "agent-a")
	workspaceB := AgentHomePath(omnipusHome, "agent-b")

	// Write a file in agent-a's workspace.
	secretFile := filepath.Join(workspaceA, "sessions", "secret.txt")
	require.NoError(t, os.WriteFile(secretFile, []byte("agent-a-secret"), 0o600))

	// Simulate application-level cross-agent access check:
	// Agent B's workspace path must not be a prefix of agent A's secret file.
	// This represents the application-level enforcement required by the spec.
	// (Wave 1: application-level; kernel-level Landlock is Wave 2.)
	isContained := func(file, workspace string) bool {
		rel, err := filepath.Rel(workspace, file)
		if err != nil {
			return false
		}
		// Rel returns ".." paths for files outside the workspace.
		return len(rel) > 0 && rel[:2] != ".."
	}

	assert.False(t, isContained(secretFile, workspaceB),
		"agent-a's file must not be within agent-b's workspace path")
	assert.True(t, isContained(secretFile, workspaceA),
		"agent-a's file must be within agent-a's workspace path")

	// Verify workspaces are at distinct paths.
	assert.NotEqual(t, workspaceA, workspaceB, "workspaces must be at different paths")
}

// TestAgentHomeDefaultPath verifies AgentHomePath returns the correct default.
// Traces to: wave1-core-foundation-spec.md Scenario: Agent workspace defaults (US-7 AC3)
func TestAgentHomeDefaultPath(t *testing.T) {
	home := "/home/user/.omnipus"
	path := AgentHomePath(home, "my-agent")
	expected := "/home/user/.omnipus/agents/my-agent"
	assert.Equal(t, expected, path)
}
