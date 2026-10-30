//go:build !windows

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
)

// TestCliValidate_LaunchesExactCheckedPath is R2-C1. The handshake runs the
// checked path bytes. A sibling whose name is that path with the trailing
// space removed must not run. The launch goes through
// runner.TestConnectionCheckedFile, which does not trim.
func TestCliValidate_LaunchesExactCheckedPath(t *testing.T) {
	api, _, home := t11ValidateMux(t)
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o700))
	plainMarker := filepath.Join(t.TempDir(), "plain")
	spacedMarker := filepath.Join(t.TempDir(), "spaced")
	plain := t11BuildMarkerProgram(t, plainMarker)
	spaced := t11BuildMarkerProgram(t, spacedMarker)
	base := filepath.Join(dir, "fakecli")
	withSpace := base + " "
	t11CopyExe(t, plain, base, 0o755)
	t11CopyExe(t, spaced, withSpace, 0o755)

	_, cfg := t11Person(t, home)
	ctx := context.WithValue(context.Background(), ctxkey.ConfigContextKey{}, cfg)
	spawns, launched := t11CountSpawns(t)
	resp := api.validateCLI(ctx, "claude-code", withSpace)

	checked, err := filepath.EvalSymlinks(withSpace)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(checked, " "), "the resolved file keeps the trailing space")
	require.NotEqual(t, base, checked, "resolving must not select the sibling without the space")
	require.Equal(t, []string{checked}, *launched, "the handshake receives the checked file, not the sibling")
	require.Equal(t, 1, *spawns)
	// "ok" and "unauthenticated" differ only by this machine's CLI login.
	// Both mean the checked file ran and reported a version (spec FR-5).
	require.Truef(t, t11ReasonMeansProgramRan(resp.Reason),
		"the checked file ran; body detail %v reason %s", resp.Detail, resp.Reason)
	require.NotNil(t, resp.Version)
	require.Equal(t, t11Version, *resp.Version, "the version is the fake's, so this file is the one that ran")
	_, err = os.Stat(spacedMarker)
	require.NoError(t, err, "the space-suffixed file must be the one that ran")
	_, err = os.Stat(plainMarker)
	require.ErrorIs(t, err, fs.ErrNotExist, "the sibling without the space must not run")
}
