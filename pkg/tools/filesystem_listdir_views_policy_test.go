// Tests for saved-view listing limits and turn read-policy parity (#1017).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A candidate being regular and within the root is not sufficient permission
// to read it. The oracle is the existing read policy (design L16); the root
// and explicit read controls distinguish permission from root containment.
func TestListDirTool_InRootViewsFollowReadPolicy(t *testing.T) {
	for _, confined := range []bool{false, true} {
		mode := "host"
		if confined {
			mode = "confined"
		}
		t.Run(mode, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			home := filepath.Join(base, "home")
			ws := filepath.Join(home, "workspaces", "alpha", "work")
			agentHome := filepath.Join(home, "agents", "self")
			t.Setenv(config.EnvHome, home)
			require.NoError(t, os.MkdirAll(agentHome, 0o700))
			vault := filepath.Join(ws, "Vault")
			writeListdirVault(t, vault, "Policy vault", map[string]string{
				"visible--view.yaml": "name: visible--view\ntype: invoice\nlabel: Visible view\nproperties: [client]\n",
			})
			require.NoError(t, os.WriteFile(filepath.Join(vault, "note.md"), []byte("# Note\n"), 0o600))
			body := "name: unlisted--view\ntype: invoice\nlabel: Unlisted label\nkind: summary\nproperties: [client]\n"
			original := filepath.Join(home, "credentials.json")
			require.NoError(t, os.WriteFile(original, []byte(body), 0o600))
			rel := filepath.Join("Vault", ".omnipus-vault", "views", "reviewed.yaml")
			candidate := filepath.Join(ws, rel)
			require.NoError(t, os.Link(original, candidate))
			info, err := os.Lstat(candidate)
			require.NoError(t, err)
			require.True(t, info.Mode().IsRegular(), "instrument: the in-root candidate is a real regular file")
			originalInfo, err := os.Stat(original)
			require.NoError(t, err)
			require.True(t, os.SameFile(info, originalInfo), "instrument: the in-root file has the protected fixture's identity")

			root, err := os.OpenRoot(ws)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, root.Close()) })
			f, err := root.Open(rel)
			require.NoError(t, err, "instrument: root containment alone allows this candidate")
			raw, err := io.ReadAll(f)
			require.NoError(t, err)
			require.NoError(t, f.Close())
			require.Equal(t, body, string(raw), "instrument: the actual bytes are readable through the root")

			ctx := WithReadConfined(WithTurnWorkspaceDir(WithWorkspaceID(
				WithAgentID(context.Background(), "self"), "alpha"), ws), confined)
			policy, err := ResolveTurnFSPolicy(ctx, agentHome, confined)
			require.NoError(t, err)
			require.Equal(t, home, policy.HomeGuard.Home, "instrument: the policy is anchored at this fixture's home")
			require.Equal(t, ws, policy.WorkDir, "instrument: the turn is rooted in this fixture's work folder")
			require.True(t, fspolicy.IsCarveOut(original, policy), "instrument: the original synthetic file is protected")
			require.True(t, fspolicy.IsCarveOut(candidate, policy), "instrument: the in-root candidate is protected by identity")
			handle, err := ResolvePath(ctx, policy, "list_directory", "", FSOpRead, candidate)
			if handle != nil {
				require.NoError(t, handle.Close())
			}
			require.ErrorIs(t, err, ErrCarveOut, "instrument: the turn's real read policy refuses this in-root file")
			read := NewReadFileTool(agentHome, confined, MaxReadFileSize).Execute(ctx, map[string]any{"path": candidate})
			require.True(t, read.IsError, "instrument: read_file refuses the same candidate: %s", read.ForLLM)

			out := NewListDirTool(agentHome, confined).Execute(ctx, map[string]any{"path": "Vault"})
			require.False(t, out.IsError, "an unreadable view does not fail the ordinary listing: %s", out.ForLLM)
			assert.Contains(t, out.ForLLM, "FILE: note.md\n", "ordinary file rows survive")
			assert.Contains(t, out.ForLLM, "VIEW: Visible view (view visible--view, knowledge base Policy vault)\n",
				"instrument: the allowed view still renders")
			assert.NotContains(t, out.ForLLM, "Unlisted label", "listing follows the turn's read policy for an in-root view")
			assert.NotContains(t, out.ForLLM, "unlisted--view", "a refused in-root view's identifier is not presented")
			assert.NotContains(t, out.ForLLM, "kind summary", "a refused in-root view's kind is not presented")
		})
	}
}

// Contract commit 9ce7b0cc8 sets 256 KiB per file. This cheap agent-side pin
// complements the REST boundary tests without importing production constants.
func TestListDirTool_ViewFileSizeBoundary(t *testing.T) {
	const fileBytes = 256 << 10
	padded := func(slug, label string, size int) string {
		header := "name: " + slug + "\ntype: invoice\nlabel: " + label + "\nproperties: [client]\n"
		body := header + "#" + strings.Repeat("x", size-len(header)-2) + "\n"
		require.Len(t, body, size, "fixture drift: exact byte length")
		return body
	}
	ws := t.TempDir()
	writeListdirVault(t, filepath.Join(ws, "Vault"), "Limits vault", map[string]string{
		"fit.yaml":  padded("fitting-view", "At boundary", fileBytes),
		"over.yaml": padded("oversized-view", "Over boundary", fileBytes+1),
	})
	out := NewListDirTool(ws, true).Execute(context.Background(), map[string]any{"path": "Vault"})
	require.False(t, out.IsError, "oversized metadata does not error the listing: %s", out.ForLLM)
	var views []string
	for _, line := range strings.Split(out.ForLLM, "\n") {
		if strings.HasPrefix(line, "VIEW:") {
			views = append(views, line)
		}
	}
	require.Equal(t, []string{"VIEW: At boundary (view fitting-view, knowledge base Limits vault)"}, views,
		"a file at exactly 256 KiB renders, and one byte more does not")
	assert.NotContains(t, out.ForLLM, "oversized-view")
	assert.NotContains(t, out.ForLLM, "Over boundary")
}
