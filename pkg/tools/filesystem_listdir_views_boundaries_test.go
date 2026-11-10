// Agent saved-view listing policy boundaries (#1017).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A VIEW line names its knowledge base from the marker file
// (.omnipus-vault/vault.json). The marker is a file like any other: when the
// turn's read policy refuses it (here an in-root hard link to a protected
// file whose bytes parse as a marker), its bytes must not reach the listing,
// and the line falls back to the folder name.
func TestListDirViewsPolicy_ProtectedMarkerNeverNamesTheKnowledgeBase(t *testing.T) {
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
			writeListdirVault(t, vault, "Ordinary name", map[string]string{
				"visible--view.yaml": "name: visible--view\ntype: invoice\nlabel: Visible view\nproperties: [client]\n",
			})
			require.NoError(t, os.WriteFile(filepath.Join(vault, "note.md"), []byte("# Note\n"), 0o600))

			ctx := WithReadConfined(WithTurnWorkspaceDir(WithWorkspaceID(
				WithAgentID(context.Background(), "self"), "alpha"), ws), confined)
			policy, err := ResolveTurnFSPolicy(ctx, agentHome, confined)
			require.NoError(t, err)

			// Control inside the probe: an ordinary marker names the knowledge
			// base, so a leak below would be visible to this check.
			out := NewListDirTool(agentHome, confined).Execute(ctx, map[string]any{"path": "Vault"})
			require.False(t, out.IsError, "instrument: the ordinary listing succeeds: %s", out.ForLLM)
			require.Contains(t, out.ForLLM, "VIEW: Visible view (view visible--view, knowledge base Ordinary name)\n",
				"instrument: an ordinary marker names the knowledge base")

			// Swap the marker for an in-root hard link to a protected file.
			const leaked = "LEAKED-MARKER-NAME"
			original := filepath.Join(home, "credentials.json")
			require.NoError(t, os.WriteFile(original, []byte(`{"display_name":"`+leaked+`"}`), 0o600))
			marker := filepath.Join(vault, ".omnipus-vault", "vault.json")
			require.NoError(t, os.Remove(marker))
			require.NoError(t, os.Link(original, marker))
			info, err := os.Lstat(marker)
			require.NoError(t, err)
			require.True(t, info.Mode().IsRegular(), "instrument: the in-root marker is a real regular file")
			originalInfo, err := os.Stat(original)
			require.NoError(t, err)
			require.True(t, os.SameFile(info, originalInfo), "instrument: the marker has the protected file's identity")
			require.True(t, fspolicy.IsCarveOut(marker, policy), "instrument: the marker is protected by identity")
			handle, err := ResolvePath(ctx, policy, "list_directory", "", FSOpRead, marker)
			if handle != nil {
				require.NoError(t, handle.Close())
			}
			require.ErrorIs(t, err, ErrCarveOut, "instrument: the turn's real read policy refuses the marker")
			read := NewReadFileTool(agentHome, confined, MaxReadFileSize).Execute(ctx, map[string]any{"path": marker})
			require.True(t, read.IsError, "instrument: read_file refuses the same marker: %s", read.ForLLM)

			out = NewListDirTool(agentHome, confined).Execute(ctx, map[string]any{"path": "Vault"})
			require.False(t, out.IsError, "a refused marker does not fail the ordinary listing: %s", out.ForLLM)
			assert.Contains(t, out.ForLLM, "FILE: note.md\n", "ordinary file rows survive")
			assert.Contains(t, out.ForLLM, "VIEW: Visible view (view visible--view, knowledge base Vault)\n",
				"a marker the turn may not read falls back to the folder name")
			assert.NotContains(t, out.ForLLM, leaked, "a protected file's bytes are never presented as the knowledge base name")
		})
	}
}

// Listing a control folder needs its own authorization, even if the turn may
// read a known file in it. This resolver-level policy fixture grants the base
// as a mount and admits specific home-guard paths, not the whole base subtree.
// The resolver, rooted filesystem, checked loader and formatter are all real.
func TestListDirViewsPolicy_UnlistedControlFolderContributesNoViews(t *testing.T) {
	for _, confined := range []bool{false, true} {
		mode := "host"
		if confined {
			mode = "confined"
		}
		t.Run(mode, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			home := filepath.Join(base, "home")
			work := filepath.Join(base, "work")
			vault := filepath.Join(home, "Vault")
			notes := filepath.Join(vault, "notes")
			viewsDir := filepath.Join(vault, ".omnipus-vault", "views")
			const viewBody = "name: visible--view\ntype: invoice\nlabel: Visible view\nproperties: [client]\nsource: notes/invoices.base\n"
			writeListdirVault(t, vault, "Policy vault", map[string]string{"visible--view.yaml": viewBody})
			require.NoError(t, os.MkdirAll(work, 0o700))
			require.NoError(t, os.MkdirAll(notes, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(notes, "note.md"), []byte("# Note\n"), 0o600))
			t.Setenv(config.EnvHome, home)
			ctx := WithReadConfined(WithAgentID(context.Background(), "self"), confined)
			policy := fspolicy.FSPolicy{
				WorkDir: work, Scope: fspolicy.FSScopeConfined,
				ReadConfined: confined, AllowedRoots: []string{vault},
				HomeGuard: fspolicy.HomeGuard{Home: home, Allow: []fspolicy.AllowRoot{
					{Path: notes},
					{Path: filepath.Join(vault, ".omnipus-vault", "records")},
					{Path: filepath.Join(vault, ".omnipus-vault", "vault.json")},
					{Path: filepath.Join(viewsDir, "visible--view.yaml")},
					{Path: viewsDir},
				}},
			}
			require.NoError(t, policy.Validate(), "instrument: a valid turn policy, not a mocked resolver")

			// Prove the view is placed in this folder while enumeration is allowed.
			allowed, err := ResolvePath(ctx, policy, "list_directory", "", FSOpList, notes)
			require.NoError(t, err)
			entries, err := allowed.ReadDir()
			require.NoError(t, err)
			out := formatDirEntries(ctx, allowed, entries, 0, maxListDirEntries)
			require.False(t, out.IsError, out.ForLLM)
			require.Equal(t, "FILE: note.md\nVIEW: Visible view (view visible--view, knowledge base Policy vault)\n", out.ForLLM,
				"instrument: the same ordinary view is visible when its control folder may be listed")
			require.NoError(t, allowed.Close())

			// Remove only the folder allowance; the known view file stays readable.
			policy.HomeGuard.Allow = policy.HomeGuard.Allow[:len(policy.HomeGuard.Allow)-1]
			require.NoError(t, policy.Validate())
			refused, err := ResolvePath(ctx, policy, "list_directory", "", FSOpList, viewsDir)
			if refused != nil {
				require.NoError(t, refused.Close())
			}
			require.ErrorIs(t, err, ErrCarveOut, "instrument: FSOpList really denies the in-base control folder")
			listed, err := ResolvePath(ctx, policy, "list_directory", "", FSOpList, notes)
			require.NoError(t, err, "the ordinary listing remains authorized")
			defer func() { require.NoError(t, listed.Close()) }()
			scope, ok := openViewScope(ctx, listed)
			require.True(t, ok, "instrument: the real mount supplies the anchored listing scope")
			defer scope.close()
			cfs := controlFS{s: scope, kbRel: "."}
			physical, err := fs.ReadDir(scope.root.FS(), ".omnipus-vault/views")
			require.NoError(t, err, "instrument: the real control folder exists and is physically listable")
			require.Len(t, physical, 1)
			require.Equal(t, "visible--view.yaml", physical[0].Name())
			file, err := cfs.OpenRegular(".omnipus-vault/views/visible--view.yaml")
			require.NoError(t, err, "instrument: FSOpRead is allowed, so the file-read guard cannot mask a missing list guard")
			raw, err := io.ReadAll(file)
			require.NoError(t, err)
			require.NoError(t, file.Close())
			require.Equal(t, viewBody, string(raw))

			controlEntries, err := cfs.ReadDir(".omnipus-vault/views")
			assert.ErrorIs(t, err, ErrCarveOut, "the checked loader's filesystem must enforce FSOpList")
			assert.Nil(t, controlEntries, "a denied control folder yields no entries")
			entries, err = listed.ReadDir()
			require.NoError(t, err)
			out = formatDirEntries(ctx, listed, entries, 0, maxListDirEntries)
			require.False(t, out.IsError, out.ForLLM)
			assert.Equal(t, "FILE: note.md\n", out.ForLLM,
				"a control folder the turn may not list contributes no view rows; ordinary files remain")
			assert.NotContains(t, out.ForLLM, "VIEW:", "no view row may come from the denied control folder")
		})
	}
}
