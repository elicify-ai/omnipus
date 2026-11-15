// Path-boundary and resolved-root pinning cases requested by the round-3 brief.
// Expected display names are fixed literals, not values copied from the helper.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectionFileNames_PathBoundaries(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	require.NoError(t, os.MkdirAll(root, 0o700))
	other := filepath.Join(t.TempDir(), "elsewhere")
	sep := string(filepath.Separator)
	names := newCollectionFileNames(root)
	cases := []struct {
		name string
		path string
		want string
	}{
		{"in_root", filepath.Join(root, ".omnipus-vault", "records", "a.yaml"), ".omnipus-vault/records/a.yaml"},
		{"prefix_sibling", filepath.Join(root+"2", "x.yaml"), "x.yaml"},
		{"outside", filepath.Join(other, "o.yaml"), "o.yaml"},
		// Preserve the literal parent segment in the input: filepath.Join
		// would clean it away before the helper ever saw it.
		{"parent_segment", root + sep + ".." + sep + "q.yaml", "q.yaml"},
		{"root_itself", root, "vault"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := names.name(tc.path)
			assert.Equal(t, tc.want, got, "name must retain the exact boundary-case display filename")
			assert.Falsef(t, filepath.IsAbs(got), "display filename must not be absolute: %q", got)
		})
	}
}

func TestCollectionFileNames_OutsideReasonNamesReportedFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	require.NoError(t, os.MkdirAll(root, 0o700))
	outside := filepath.Join(t.TempDir(), "elsewhere", "o.yaml")
	require.True(t, filepath.IsAbs(outside), "fixture supplies an absolute outside filename")
	names := newCollectionFileNames(root)
	got := names.text("open "+outside+": not available", []string{outside})
	assert.Equal(t, "open o.yaml: not available", got, "a reported outside filename keeps its basename in the reason")
	assert.NotContains(t, got, outside, "reason must not retain the reported absolute filename")
	assert.NotContains(t, got, filepath.ToSlash(outside), "reason must not retain the slash-form absolute filename")
}

func TestCollectionFileNames_SymlinkedRootUsesResolvedNames(t *testing.T) {
	realRoot := filepath.Join(t.TempDir(), "real-vault")
	const rel = ".omnipus-vault/records/a.yaml"
	writeNote(t, realRoot, rel, "schema fixture\n")
	configuredRoot := filepath.Join(t.TempDir(), "linked-vault")
	require.NoError(t, os.Symlink(realRoot, configuredRoot), "the symlinked-root fixture must be created, never silently skipped")
	resolved, err := filepath.EvalSymlinks(realRoot)
	require.NoError(t, err, "resolve the actual target's spelling for the reported filename")
	reported := filepath.Join(resolved, filepath.FromSlash(rel))
	names := newCollectionFileNames(configuredRoot)
	got := names.name(reported)
	assert.Equal(t, rel, got, "a resolved-spelling report must remain relative to the configured symlinked root, not become a basename")
	assert.Falsef(t, filepath.IsAbs(got), "display filename must not be absolute: %q", got)
	assert.Equal(t, "open .omnipus-vault/records/a.yaml: not available",
		names.text("open "+reported+": not available", []string{reported}),
		"the reason must use the same full relative name for a resolved-spelling report")
}
