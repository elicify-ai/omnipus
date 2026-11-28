// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// bashMountCWDFixture is one workspace with one operator-approved write mount.
type bashMountCWDFixture struct {
	tool      *ExecTool
	ctx       context.Context
	workDir   string // realpath of <workspace>/work, the tool's working directory
	inWorkDir string // an existing folder inside workDir
	mountRoot string // realpath of the mounted host folder
	project   string // an existing folder inside the mount and outside the work dir
}

// newBashMountCWDFixture mounts one host folder into a fresh workspace. When
// mountContainsData is true the mounted folder is the one that holds the Omnipus
// data folder, so the work dir lies inside the mount root: workspace.CheckMountTarget
// allows that with a warning (FR-7.6, the operator mounting their home folder).
// When false the mounted folder is unrelated to the data folder.
func newBashMountCWDFixture(t *testing.T, mountContainsData bool) bashMountCWDFixture {
	t.Helper()
	const wsID = "w-mount-cwd"
	const agentID = "agent-in-ws"

	base := t.TempDir()
	home := filepath.Join(base, "omnipus-home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	t.Setenv(config.EnvHome, home)

	now := time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID: wsID, Name: "test", Status: "active", CreatedAt: now, UpdatedAt: now, CoreTeam: []string{agentID},
	}), "seed workspace")
	work, err := workspace.EnsureWorkDir(home, wsID)
	require.NoError(t, err, "ensure work dir")
	workDir, err := filepath.EvalSymlinks(work)
	require.NoError(t, err)
	inWorkDir := filepath.Join(workDir, "inside")
	require.NoError(t, os.MkdirAll(inWorkDir, 0o700))

	hostDir := t.TempDir()
	if mountContainsData {
		hostDir = base
	}
	require.NoError(t, os.MkdirAll(filepath.Join(hostDir, "project"), 0o700))
	_, warn, err := workspace.CreateMount(home, wsID, "repo", hostDir)
	require.NoError(t, err, "operator-approved mount")
	if mountContainsData {
		require.NotEmpty(t, warn, "FR-7.6: a mount holding the Omnipus data folder is allowed with a warning; the layout under test is not what it claims")
	} else {
		require.Empty(t, warn, "an ordinary folder mounts without a warning")
	}
	mountRoot, err := filepath.EvalSymlinks(hostDir)
	require.NoError(t, err)

	tool, err := NewExecTool(workDir, true)
	require.NoError(t, err)
	ctx := WithWorkspaceID(WithAgentID(WithToolContext(context.Background(), "cli", ""), agentID), wsID)
	return bashMountCWDFixture{
		tool: tool, ctx: ctx, workDir: workDir, inWorkDir: inWorkDir,
		mountRoot: mountRoot, project: filepath.Join(mountRoot, "project"),
	}
}

// TestBashCWD_AbsoluteWorkDirPathStaysRefusedWhileAWriteMountExists pins the
// work-dir half of the one widening of bash's cwd (ADR-063 addendum 2026-11-24,
// founder ruling on #1960 S-20; shell.go::resolveMountCWD): a folder inside an
// operator-approved write mount is admitted as cwd, and "unapproved paths and
// absolute paths into the work dir stay refused". The S20 actual loops only use a
// mount cwd and a relative work-dir cwd, so nothing paired "a write mount exists"
// with "an absolute path inside the work dir" (CHECK F-2, mutant h).
//
// Oracle: the ADR-063 addendum and the SEC-05/FR-B2 cwd rule it leaves in
// place, not the observed output. Two mount layouts, because two different
// lines refuse the path: with a mount beside the data folder the work dir is not
// inside the mount root, with a mount that holds the data folder it is, and only
// the work-dir check keeps the path out.
func TestBashCWD_AbsoluteWorkDirPathStaysRefusedWhileAWriteMountExists(t *testing.T) {
	for _, layout := range []struct {
		name              string
		mountContainsData bool
	}{
		{name: "mount beside the data folder", mountContainsData: false},
		{name: "mount that holds the data folder", mountContainsData: true},
	} {
		t.Run(layout.name, func(t *testing.T) {
			f := newBashMountCWDFixture(t, layout.mountContainsData)

			// Instrument: the mount is live. A folder inside it, named by its
			// absolute host path, is admitted as cwd. Without this control the
			// refusals below would also hold if no mount were ever found.
			got, err := f.tool.resolveCWD(f.ctx, map[string]any{"cwd": f.project}, f.workDir)
			require.NoError(t, err, "a folder inside an operator-approved write mount must be admitted as bash cwd")
			require.Equal(t, f.project, got, "the admitted cwd is the mounted folder itself")

			for _, tc := range []struct{ name, cwd string }{
				{name: "the work dir itself", cwd: f.workDir},
				{name: "a folder inside the work dir", cwd: f.inWorkDir},
			} {
				t.Run(tc.name, func(t *testing.T) {
					admitted, refusal := f.tool.resolveCWD(f.ctx, map[string]any{"cwd": tc.cwd}, f.workDir)
					require.Error(t, refusal, "an absolute path inside the work dir must stay refused while a write mount exists; it was admitted as %q", admitted)
					assert.Empty(t, admitted, "a refused cwd resolves to nothing")
					assert.Contains(t, refusal.Error(), "path escapes workspace")
					assert.Contains(t, refusal.Error(), "absolute path not allowed")
				})
			}

			// The work dir keeps its earlier rule for a relative path.
			got, err = f.tool.resolveCWD(f.ctx, map[string]any{"cwd": "inside"}, f.workDir)
			require.NoError(t, err, "a relative path into the work dir stays admitted")
			require.Equal(t, f.inWorkDir, got)

			// At the tool: the refused call starts nothing.
			result := f.tool.Execute(f.ctx, map[string]any{"command": "echo ran > ran.marker", "cwd": f.inWorkDir})
			require.True(t, result.IsError, "bash with an absolute work-dir cwd must be refused, got ForLLM=%q", result.ForLLM)
			assert.Contains(t, result.ForLLM, "absolute path not allowed")
			assert.NoFileExists(t, filepath.Join(f.inWorkDir, "ran.marker"), "the refused call must not have started a process")
		})
	}
}
