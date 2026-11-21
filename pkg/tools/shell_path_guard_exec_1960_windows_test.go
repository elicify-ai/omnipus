// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestGuard1960_WindowsCrossDriveProgramRuns is CHECK F4 (#1960 S0, FR-1):
// a program on another drive than the working folder is run by its full path.
// filepath.Rel cannot relate two drives, which used to end in "cannot resolve
// relative path" before the program rule was considered. An operand on that
// drive keeps a refusal. The program path has one folder level because the
// scan reads a drive path only up to its first backslash (re-check 3).
func TestGuard1960_WindowsCrossDriveProgramRuns(t *testing.T) {
	tool, cwd := guardFixture(t)
	drive := "D:"
	if strings.EqualFold(filepath.VolumeName(cwd), "D:") {
		drive = "E:"
	}
	prog := drive + `\gh.exe`
	require.Empty(t, tool.guardCommand(context.Background(), prog+" auth status", cwd),
		"FR-1: a program on another drive must not be refused for its location")

	write := tool.guardCommand(context.Background(), "echo x > "+drive+`\Out\x.txt`, cwd)
	require.NotEmpty(t, write, "a write to another drive stays refused")
}

// TestGuard1960_WindowsTruncatedProgramPathRefused is re-check 3's Windows
// residual: the path scan stops a drive path at its first backslash, so for
// `C:\Users\...\credentials.json` it would judge only `C:\Users`. A candidate
// that is not the whole program word is not treated as the program, so a
// multi-level program path — including one naming a protected file — keeps
// a refusal (conservative; see docs/tools.md).
func TestGuard1960_WindowsTruncatedProgramPathRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	credential := filepath.Join(home, "credentials.json")
	require.NoError(t, os.WriteFile(credential, []byte(`{"fixture":true}`), 0o600))
	tool, cwd := guardFixture(t)

	deep := tool.guardCommand(context.Background(), `C:\Windows\System32\cmd.exe /c ver`, cwd)
	require.Contains(t, deep, "not the program itself", "a truncated program path is not the program")

	protected := tool.guardCommand(context.Background(), credential+" --version", cwd)
	require.NotEmpty(t, protected, "a protected file named as a program stays refused")

	oneLevel := filepath.VolumeName(cwd) + `\tool.exe`
	require.Empty(t, tool.guardCommand(context.Background(), oneLevel+" --version", cwd),
		"a one-level program path is a complete program word and runs")
}
