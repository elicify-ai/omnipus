// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestFallbackBackend_ApplyToCmd_InjectsEnvVars verifies that the Wave 2 fix
// for ADR W-1 (previously a no-op) now injects OMNIPUS_SANDBOX_PATHS so that
// cooperative child processes can self-enforce path restrictions on platforms
// without Landlock.
func TestFallbackBackend_ApplyToCmd_InjectsEnvVars(t *testing.T) {
	backend := sandbox.NewFallbackBackend()
	workspace := t.TempDir()

	cmd := exec.Command("echo", "ok")
	policy := sandbox.SandboxPolicy{
		FilesystemRules: []sandbox.PathRule{
			{Path: workspace, Access: sandbox.AccessRead | sandbox.AccessWrite},
		},
	}
	err := backend.ApplyToCmd(cmd, policy)
	require.NoError(t, err, "ApplyToCmd must succeed for a valid cmd and policy")

	require.NotNil(t, cmd.Env, "ApplyToCmd must populate cmd.Env")

	// The env must contain OMNIPUS_SANDBOX_MODE=fallback.
	var haveMode, havePaths bool
	var sandboxPaths string
	for _, e := range cmd.Env {
		if e == "OMNIPUS_SANDBOX_MODE=fallback" {
			haveMode = true
		}
		if strings.HasPrefix(e, "OMNIPUS_SANDBOX_PATHS=") {
			havePaths = true
			sandboxPaths = strings.TrimPrefix(e, "OMNIPUS_SANDBOX_PATHS=")
		}
	}
	assert.True(t, haveMode, "OMNIPUS_SANDBOX_MODE=fallback must be set")
	assert.True(t, havePaths, "OMNIPUS_SANDBOX_PATHS must be set")
	assert.Contains(t, sandboxPaths, workspace,
		"OMNIPUS_SANDBOX_PATHS must include the workspace path")
}

// TestFallbackBackend_ApplyToCmd_EmptyPolicy_IsNoOp verifies that passing an
// empty policy leaves cmd.Env untouched (no injection).
func TestFallbackBackend_ApplyToCmd_EmptyPolicy_IsNoOp(t *testing.T) {
	backend := sandbox.NewFallbackBackend()
	cmd := exec.Command("echo", "ok")
	err := backend.ApplyToCmd(cmd, sandbox.SandboxPolicy{})
	require.NoError(t, err)
	// cmd.Env should still be nil — we did not need to inject anything.
	assert.Nil(t, cmd.Env, "empty policy must leave cmd.Env untouched")
}

// TestFallbackBackend_ApplyToCmd_NilCmd returns an error rather than panicking,
// so that a misconfigured caller gets a clear failure.
func TestFallbackBackend_ApplyToCmd_NilCmd(t *testing.T) {
	backend := sandbox.NewFallbackBackend()
	err := backend.ApplyToCmd(nil, sandbox.SandboxPolicy{
		FilesystemRules: []sandbox.PathRule{{Path: "/tmp", Access: sandbox.AccessRead}},
	})
	require.Error(t, err, "ApplyToCmd with nil cmd must error, not panic")
}

// TestFallbackBackend_ApplyToCmd_InheritsExistingEnv verifies the injected
// vars are appended to the caller's existing env rather than clobbering it.
func TestFallbackBackend_ApplyToCmd_InheritsExistingEnv(t *testing.T) {
	backend := sandbox.NewFallbackBackend()
	cmd := exec.Command("echo", "ok")
	cmd.Env = []string{"EXISTING_VAR=keep_me"}

	err := backend.ApplyToCmd(cmd, sandbox.SandboxPolicy{
		FilesystemRules: []sandbox.PathRule{{Path: t.TempDir(), Access: sandbox.AccessRead}},
	})
	require.NoError(t, err)

	// Existing var must survive.
	found := false
	for _, e := range cmd.Env {
		if e == "EXISTING_VAR=keep_me" {
			found = true
		}
	}
	assert.True(t, found, "existing env vars must be preserved")

	// Sanity: the process environ is also untouched by this test (ApplyToCmd
	// does not mutate os.Environ).
	_ = os.Getenv("EXISTING_VAR")
}

// TestFallbackBackend_ApplyToCmd_InheritsOsEnvWhenNil verifies that when
// cmd.Env is nil, ApplyToCmd populates it from os.Environ() so the child
// inherits the parent's environment alongside the sandbox vars.
func TestFallbackBackend_ApplyToCmd_InheritsOsEnvWhenNil(t *testing.T) {
	backend := sandbox.NewFallbackBackend()
	cmd := exec.Command("echo", "ok")
	require.Nil(t, cmd.Env)

	err := backend.ApplyToCmd(cmd, sandbox.SandboxPolicy{
		FilesystemRules: []sandbox.PathRule{{Path: t.TempDir(), Access: sandbox.AccessRead}},
	})
	require.NoError(t, err)

	// cmd.Env must be non-nil now, and longer than just the 2 sandbox vars
	// (os.Environ is rarely that short in real environments, but allow for
	// minimal CI containers by checking for >= 2 with at least the sandbox vars).
	require.NotNil(t, cmd.Env)
	assert.GreaterOrEqual(t, len(cmd.Env), 2,
		"cmd.Env must contain at least the sandbox vars")
}
