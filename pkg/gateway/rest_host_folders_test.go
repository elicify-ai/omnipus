// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getFolders drives the handler and decodes the listing.
func getFolders(t *testing.T, a *restAPI, path string) (*httptest.ResponseRecorder, gen.HostFolderListing) {
	t.Helper()
	url := "/api/v1/system/folders"
	if path != "" {
		url += "?path=" + path
	}
	rec := httptest.NewRecorder()
	a.HandleSystemFolders(rec, httptest.NewRequest(http.MethodGet, url, nil))

	var out gen.HostFolderListing
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	}
	return rec, out
}

// TestHostFolders_ListsOnlyDirectories covers the picker's basic contract: a
// mount target is always a folder, so listing files would bury the operator in
// rows none of which they can pick.
func TestHostFolders_ListsOnlyDirectories(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "projects"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "notes"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(base, "loose.txt"), []byte("x"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(base, ".hidden"), 0o700))

	a := &restAPI{homePath: filepath.Join(base, "omnipus-home")}
	rec, out := getFolders(t, a, base)
	require.Equal(t, http.StatusOK, rec.Code)

	names := make([]string, 0, len(out.Entries))
	for _, e := range out.Entries {
		names = append(names, e.Name)
	}
	assert.Contains(t, names, "projects")
	assert.Contains(t, names, "notes")
	assert.NotContains(t, names, "loose.txt", "a file is not a mount target")
	assert.NotContains(t, names, ".hidden", "hidden directories are tooling state, not places to mount")

	assert.NotNil(t, out.Parent, "a non-root directory must offer somewhere to go up to")
}

// TestHostFolders_RefusesTheOmnipusDataDirectory is the picker half of the one
// hard boundary (FR-7.5). The verdict must travel WITH the row so the client
// can disable the choice at the point of selection — letting the operator pick
// a folder and only then telling them no teaches them to ignore refusals.
func TestHostFolders_RefusesTheOmnipusDataDirectory(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "omnipus-data")
	require.NoError(t, os.MkdirAll(home, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "safe"), 0o700))

	a := &restAPI{homePath: home}
	rec, out := getFolders(t, a, base)
	require.Equal(t, http.StatusOK, rec.Code)

	var sawData, sawSafe bool
	for _, e := range out.Entries {
		switch e.Name {
		case "omnipus-data":
			sawData = true
			assert.False(t, e.Mountable, "the Omnipus data directory must be unpickable")
			require.NotNil(t, e.Reason, "a refusal must say why")
			assert.Contains(t, *e.Reason, "Omnipus data directory")
		case "safe":
			sawSafe = true
			assert.True(t, e.Mountable, "an ordinary folder must stay selectable")
			assert.Nil(t, e.Reason, "no reason belongs on an unremarkable folder")
		}
	}
	require.True(t, sawData, "the data directory must still be LISTED, just not selectable")
	require.True(t, sawSafe)
}

// TestHostFolders_RejectsRelativeAndMissingPaths pins the input validation. A
// relative path is meaningless here — it would resolve against the gateway's
// working directory, which the operator neither knows nor controls.
func TestHostFolders_RejectsRelativeAndMissingPaths(t *testing.T) {
	a := &restAPI{homePath: t.TempDir()}

	rec, _ := getFolders(t, a, "relative/path")
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a relative path must be refused, not resolved silently")

	rec, _ = getFolders(t, a, filepath.Join(t.TempDir(), "does-not-exist"))
	assert.Equal(t, http.StatusNotFound, rec.Code)

	file := filepath.Join(t.TempDir(), "a-file.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	rec, _ = getFolders(t, a, file)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a file is not a folder to browse into")
}

// TestHostFolders_ResolvesSymlinks matters because the path shown, the path
// stored, and the path the mount rules are evaluated against must be the same
// string. On macOS /tmp is a symlink to /private/tmp, and skipping resolution is
// exactly how those two end up disagreeing.
func TestHostFolders_ResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(real, "inner"), 0o700))

	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "link-to-real")
	require.NoError(t, os.Symlink(real, link))

	a := &restAPI{homePath: t.TempDir()}
	rec, out := getFolders(t, a, link)
	require.Equal(t, http.StatusOK, rec.Code)

	resolvedReal, err := filepath.EvalSymlinks(real)
	require.NoError(t, err)
	assert.Equal(t, resolvedReal, out.Path,
		"the listing must report where the path really goes, not the name used to reach it")

	require.Len(t, out.Entries, 1)
	assert.Equal(t, "inner", out.Entries[0].Name)
}

// TestHostFolders_MethodNotAllowed keeps this read-only. A picker that accepts a
// POST invites someone to wire a mutation onto it later.
func TestHostFolders_MethodNotAllowed(t *testing.T) {
	a := &restAPI{homePath: t.TempDir()}
	rec := httptest.NewRecorder()
	a.HandleSystemFolders(rec, httptest.NewRequest(http.MethodPost, "/api/v1/system/folders", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
