// Published saved-view listing bounds (#1017, contract commit 9ce7b0cc8).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListingLoadLimits_MatchPublishedContract(t *testing.T) {
	// Independent literals from the include_views contract, not aliases to
	// production constants: 256 KiB/file, 128 files/folder, 4 MiB/listing.
	require.Equal(t, LoadLimits{
		MaxFileBytes: 256 << 10, MaxCandidates: 128, MaxTotalBytes: 4 << 20,
	}, ListingLoadLimits())
}

func TestLoadViewSetChecked_PublishedFileBoundary(t *testing.T) {
	const fileBytes = 256 << 10 // include_views, contract commit 9ce7b0cc8
	vault := t.TempDir()
	for _, dir := range []string{"records", "views"} {
		require.NoError(t, os.MkdirAll(filepath.Join(vault, ".omnipus-vault", dir), 0o700))
	}
	require.NoError(t, os.WriteFile(filepath.Join(vault, ".omnipus-vault", "records", "invoice.yaml"),
		[]byte("schema_version: 1\ntype: invoice\nproperties:\n  client: { type: text }\n"), 0o600))
	for _, tc := range []struct {
		slug string
		size int
	}{{"fit", fileBytes}, {"over", fileBytes + 1}} {
		header := "name: " + tc.slug + "\ntype: invoice\nproperties: [client]\n"
		body := header + "#" + strings.Repeat("x", tc.size-len(header)-2) + "\n"
		require.Len(t, body, tc.size, "fixture drift: exact file byte length")
		require.NoError(t, os.WriteFile(filepath.Join(vault, ".omnipus-vault", "views", tc.slug+".yaml"), []byte(body), 0o600))
	}
	root, err := os.OpenRoot(vault)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	views, report, err := LoadViewSetChecked(context.Background(), RootControlFS{Root: root}, vault, ListingLoadLimits())
	require.NoError(t, err, "a candidate beyond the limit is a per-file rejection, not a set-level error")
	require.Equal(t, []string{"fit"}, views.Names(), "exactly 256 KiB loads; one byte more does not")
	require.Len(t, report.Rejections, 1, "the oversize file is the only rejected candidate")
	require.Equal(t, RejectViewUnreadable, report.Rejections[0].Code)
	require.Equal(t, []string{filepath.Join(vault, ".omnipus-vault", "views", "over.yaml")}, report.Rejections[0].Paths)
}
