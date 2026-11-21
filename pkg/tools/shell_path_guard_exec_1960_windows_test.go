// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGuard1960_WindowsCrossDriveProgramRuns is CHECK F4 (#1960 S0, FR-1):
// a program on another drive than the working folder is run by its full path.
// filepath.Rel cannot relate two drives, which used to end in "cannot resolve
// relative path" before the program rule was considered. An operand on that
// drive keeps a refusal.
func TestGuard1960_WindowsCrossDriveProgramRuns(t *testing.T) {
	tool, cwd := guardFixture(t)
	drive := "D:"
	if strings.EqualFold(filepath.VolumeName(cwd), "D:") {
		drive = "E:"
	}
	prog := drive + `\Tools\gh.exe`
	require.Empty(t, tool.guardCommand(context.Background(), prog+" auth status", cwd),
		"FR-1: a program on another drive must not be refused for its location")

	write := tool.guardCommand(context.Background(), "echo x > "+drive+`\Out\x.txt`, cwd)
	require.NotEmpty(t, write, "a write to another drive stays refused")
}
