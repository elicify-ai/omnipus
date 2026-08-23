// Omnipus — ADR-067 US-16: lifecycle edges of the knowledge write path.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
package knowledge

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ---------------------------------------------------------------------------
// Fixtures. Every helper here is prefixed a4 so it cannot collide with the
// helpers the other test files in this package already define.
// ---------------------------------------------------------------------------

var a4Seq atomic.Uint64

// a4Home returns a fresh $OMNIPUS_HOME, real-path resolved.
func a4Home(t *testing.T) string {
	t.Helper()
	return a4Real(t, t.TempDir())
}

// a4Real resolves a path the way this package does, so a macOS /var →
// /private/var symlink cannot make an otherwise-correct assertion fail.
func a4Real(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return filepath.Clean(resolved)
}

// a4Workspace seeds a minimal valid workspace record and returns its id.
func a4Workspace(t *testing.T, home string) string {
	t.Helper()
	id := "a4ws-" + strconv.FormatUint(a4Seq.Add(1), 10)
	now := time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID: id, Name: id, Status: "active", CreatedAt: now, UpdatedAt: now,
	}))
	return id
}

// a4Vault creates a knowledge base at dir with an Omnipus marker naming it,
// and returns the resolved root.
func a4Vault(t *testing.T, dir, displayName string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, MarkerDirName), 0o700))
	raw, err := json.Marshal(Marker{DisplayName: displayName})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, MarkerDirName, markerFileName), raw, 0o600))
	return a4Real(t, dir)
}

// a4Note writes a note inside a collection and returns its absolute path.
func a4Note(t *testing.T, root, relPath, body string) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
	return full
}

// a4Mount mounts hostPath into workspace wsID under name.
func a4Mount(t *testing.T, home, wsID, name, hostPath string) {
	t.Helper()
	_, _, err := workspace.CreateMount(home, wsID, name, hostPath)
	require.NoError(t, err)
}

// a4Scoped builds the ScopedCollection the write path is handed, by resolving
// the real scope rather than by hand-constructing one — a hand-constructed
// value would let a test pass while ResolveScope refused the same collection.
func a4Scoped(t *testing.T, home, wsID, name string) ScopedCollection {
	t.Helper()
	col, ok := ResolveScope(home, wsID).Select(name)
	require.True(t, ok, "collection %q must be in scope for workspace %s", name, wsID)
	return col
}

// ---------------------------------------------------------------------------
// a4DatalessFS — a filesystem whose files stat with a size and read as empty.
//
// This is what a cloud provider's dematerialised file looks like from Go: the
// directory entry is real, stat reports the real size, and opening it yields
// nothing. Some providers return an errno; some return a clean EOF. The clean
// EOF is the dangerous one and is what this fake reproduces, because it is the
// variant that indexes as "present and empty" with nothing anywhere saying so.
// ---------------------------------------------------------------------------

type a4DatalessFS struct {
	inner    LinkFS
	dataless map[string]int64 // abs path → the size stat should report
	openErr  map[string]error // abs path → the error Open should return
	readErr  map[string]error // abs path → the error the read should end with
}

func a4NewDatalessFS() *a4DatalessFS {
	return &a4DatalessFS{
		inner:    OSLinkFS(),
		dataless: map[string]int64{},
		openErr:  map[string]error{},
		readErr:  map[string]error{},
	}
}

func (f *a4DatalessFS) Lstat(name string) (fs.FileInfo, error) {
	if size, ok := f.dataless[name]; ok {
		return a4FakeInfo{name: filepath.Base(name), size: size}, nil
	}
	return f.inner.Lstat(name)
}

func (f *a4DatalessFS) ReadDir(name string) ([]fs.DirEntry, error) { return f.inner.ReadDir(name) }
func (f *a4DatalessFS) EvalSymlinks(name string) (string, error)   { return f.inner.EvalSymlinks(name) }

func (f *a4DatalessFS) Open(name string) (fs.File, error) {
	if err, ok := f.openErr[name]; ok {
		return nil, err
	}
	if size, ok := f.dataless[name]; ok {
		return &a4DatalessFile{
			info:    a4FakeInfo{name: filepath.Base(name), size: size},
			readErr: f.readErr[name],
		}, nil
	}
	return f.inner.Open(name)
}

type a4FakeInfo struct {
	name string
	size int64
}

func (i a4FakeInfo) Name() string       { return i.name }
func (i a4FakeInfo) Size() int64        { return i.size }
func (i a4FakeInfo) Mode() fs.FileMode  { return 0o600 }
func (i a4FakeInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (i a4FakeInfo) IsDir() bool        { return false }
func (i a4FakeInfo) Sys() any           { return nil }

type a4DatalessFile struct {
	info    a4FakeInfo
	readErr error
}

func (f *a4DatalessFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *a4DatalessFile) Close() error               { return nil }
func (f *a4DatalessFile) Read([]byte) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	return 0, io.EOF
}

// ---------------------------------------------------------------------------
// US-16 AS-2 — revoking one mount must not disturb the other workspace.
//
// Spec test 52. This is the half that belongs to the write path: after a
// revoke, workspace A must be told the collection is no longer writable, and
// workspace B must be unaffected. The retrieval half (search in B still
// returns results) is scope.go's and is covered there.
// ---------------------------------------------------------------------------

func TestLifecycle_RevokeAffectsOnlyTheRevokingWorkspace(t *testing.T) {
	home := a4Home(t)
	shared := a4Vault(t, filepath.Join(t.TempDir(), "Shared"), "Shared")

	wsA := a4Workspace(t, home)
	wsB := a4Workspace(t, home)
	a4Mount(t, home, wsA, "shared", shared)
	a4Mount(t, home, wsB, "shared", shared)

	colA := a4Scoped(t, home, wsA, "Shared")
	colB := a4Scoped(t, home, wsB, "Shared")

	require.NoError(t, RequireWritableCollection("knowledge_create", home, wsA, colA))
	require.NoError(t, RequireWritableCollection("knowledge_create", home, wsB, colB))

	require.NoError(t, workspace.DeleteMount(home, wsA, "shared"))

	err := RequireWritableCollection("knowledge_create", home, wsA, colA)
	require.Error(t, err, "workspace A must be refused after its mount is revoked")
	assert.True(t, errors.Is(err, ErrMountRevoked), "want ErrMountRevoked, got %v", err)

	assert.NoError(t, RequireWritableCollection("knowledge_create", home, wsB, colB),
		"workspace B's grant is independent and must survive A's revoke")

	stateA, _ := ResolveMountState(home, wsA, colA.Root)
	stateB, nameB := ResolveMountState(home, wsB, colB.Root)
	assert.Equal(t, MountStateRevoked, stateA)
	assert.Equal(t, MountStateActive, stateB)
	assert.Equal(t, "shared", nameB, "the mount's operator-facing name is what a refusal must quote")
}

// ---------------------------------------------------------------------------
// US-16 AS-4 / FR-110 — a moved collection folder surfaces a BROKEN mount with
// an action, not a revoked one and not a bare failure.
//
// Spec test 53. The distinction is the requirement: broken offers "point it at
// the new location", revoked offers nothing because the operator meant it. A
// implementation that collapses the two loses the only actionable half.
// ---------------------------------------------------------------------------

func TestLifecycle_MovedFolderIsBrokenNotRevoked(t *testing.T) {
	home := a4Home(t)
	parent := t.TempDir()
	vault := a4Vault(t, filepath.Join(parent, "Vault"), "Vault")

	ws := a4Workspace(t, home)
	a4Mount(t, home, ws, "vault", vault)
	col := a4Scoped(t, home, ws, "Vault")

	// The operator renames the folder in Finder.
	require.NoError(t, os.Rename(vault, filepath.Join(a4Real(t, parent), "Vault-2026")))

	state, name := ResolveMountState(home, ws, col.Root)
	require.Equal(t, MountStateBroken, state,
		"a mount whose recorded target is gone is BROKEN; revoked would mean the operator removed the grant")
	assert.Equal(t, "vault", name)

	err := RequireWritableCollection("knowledge_create", home, ws, col)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrMountBroken), "want ErrMountBroken, got %v", err)

	var lc *LifecycleError
	require.True(t, errors.As(err, &lc), "the refusal must be typed, not prose")
	assert.Equal(t, "repoint_mount", lc.Remedy(),
		"FR-110 requires an ACTION; a message is not one")
	assert.Contains(t, err.Error(), "vault", "the refusal must name the mount")
	assert.Contains(t, err.Error(), "Vault", "the refusal must name the collection")
}

// ---------------------------------------------------------------------------
// Fail-closed: anything this function cannot establish is REVOKED.
// ---------------------------------------------------------------------------

func TestLifecycle_UnknownWorkspaceIsRevokedNeverActive(t *testing.T) {
	home := a4Home(t)
	vault := a4Vault(t, filepath.Join(t.TempDir(), "Vault"), "Vault")

	cases := []struct {
		name        string
		home        string
		workspaceID string
		root        string
	}{
		{"no home", "", "ws-1", vault},
		{"no workspace", home, "", vault},
		{"no collection", home, "ws-1", ""},
		{"workspace that does not exist", home, "ws-never-created", vault},
		{"workspace with no mounts", home, a4Workspace(t, home), vault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, name := ResolveMountState(tc.home, tc.workspaceID, tc.root)
			assert.Equal(t, MountStateRevoked, state)
			assert.Empty(t, name)

			err := RequireWritableCollection("knowledge_create", tc.home, tc.workspaceID,
				ScopedCollection{Name: "Vault", Root: tc.root})
			require.Error(t, err, "a write must never proceed on a grant that could not be established")
			assert.True(t, errors.Is(err, ErrMountRevoked))
		})
	}
}

// ---------------------------------------------------------------------------
// The work tree (D11). A knowledge base Omnipus created lives in the
// workspace's own work directory and has no mount record at all — a
// mounts-only check would call every Omnipus-authored collection revoked.
// ---------------------------------------------------------------------------

func TestLifecycle_WorkTreeCollectionIsActiveWithoutAMount(t *testing.T) {
	home := a4Home(t)
	ws := a4Workspace(t, home)

	workDir, err := workspace.SafeWorkDir(home, ws)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(workDir, 0o755))
	vault := a4Vault(t, filepath.Join(workDir, "Notes"), "Notes")

	state, origin := ResolveMountState(home, ws, vault)
	assert.Equal(t, MountStateActive, state)
	assert.Equal(t, WorkTreeOrigin, origin)
	assert.NoError(t, RequireWritableCollection("knowledge_create", home, ws,
		ScopedCollection{Name: "Notes", Root: vault, Origin: WorkTreeOrigin}))
}

// ---------------------------------------------------------------------------
// US-16 AS-5 / FR-111 — an evicted file fails LOUDLY and is never read as
// empty.
//
// Spec test 54. The oracle is the size disagreement, not an errno: the
// dangerous variant of eviction returns a clean EOF, so "did it error" would
// pass on exactly the case that loses data.
// ---------------------------------------------------------------------------

func TestLifecycle_EvictedNoteIsLoudNotEmpty(t *testing.T) {
	dir := t.TempDir()
	materialised := filepath.Join(dir, "present.md")
	require.NoError(t, os.WriteFile(materialised, []byte("# Present\n\nreal bytes\n"), 0o600))
	evicted := filepath.Join(dir, "evicted.md")
	require.NoError(t, os.WriteFile(evicted, []byte("placeholder"), 0o600))

	fsys := a4NewDatalessFS()
	// stat says 4096 bytes; the read returns nothing, with NO error. That is
	// the shape that indexes as present-and-empty.
	fsys.dataless[evicted] = 4096

	// Positive control: without it, an implementation that failed on every
	// file would pass the negative half.
	content, err := ReadNoteContent(fsys, materialised)
	require.NoError(t, err, "a materialised note must still read")
	assert.Contains(t, string(content), "real bytes")

	_, err = ReadNoteContent(fsys, evicted)
	require.Error(t, err, "an evicted note must FAIL, not return empty content")
	assert.True(t, errors.Is(err, ErrNoteEvicted), "want ErrNoteEvicted, got %v", err)

	var lc *LifecycleError
	require.True(t, errors.As(err, &lc))
	assert.Equal(t, "materialize_file", lc.Remedy())
	assert.Contains(t, err.Error(), "4096",
		"the refusal must state the size disagreement that detected it")
}

// ClassifyContentFailure is the single oracle both the indexer and the write
// path use. Two independent classifications would drift, and the direction
// they drift in is "one of them starts calling an evicted file empty".
func TestLifecycle_ClassifyContentFailure_Table(t *testing.T) {
	someErr := errors.New("input/output error")

	cases := []struct {
		name         string
		declaredSize int64
		readBytes    int
		readErr      error
		want         error
	}{
		{"materialised file reads fully", 12, 12, nil, nil},
		{"genuinely empty file", 0, 0, nil, nil},
		{"short read is not eviction", 4096, 10, nil, nil},
		{"clean EOF on a sized file is eviction", 4096, 0, nil, ErrNoteEvicted},
		{"errored read on a sized file is eviction", 4096, 0, someErr, ErrNoteEvicted},
		{"errored read that still produced bytes is unreadable", 4096, 10, someErr, ErrNoteUnreadable},
		{"vanished file is unreadable, not evicted", 4096, 0, fs.ErrNotExist, ErrNoteUnreadable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyContentFailure("/notes/x.md", tc.declaredSize, tc.readBytes, tc.readErr)
			if tc.want == nil {
				assert.NoError(t, got)
				return
			}
			require.Error(t, got)
			assert.True(t, errors.Is(got, tc.want), "want %v, got %v", tc.want, got)
			assert.Contains(t, got.Error(), "/notes/x.md", "every refusal names its subject")
		})
	}
}

// An open that fails outright must still be classified, not panic and not
// return an empty note.
func TestLifecycle_UnreadableNoteIsTypedNotEmpty(t *testing.T) {
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked.md")
	require.NoError(t, os.WriteFile(locked, []byte("secret"), 0o600))

	fsys := a4NewDatalessFS()
	fsys.dataless[locked] = 6
	fsys.openErr[locked] = fs.ErrPermission

	got, err := ReadNoteContent(fsys, locked)
	require.Error(t, err)
	assert.Nil(t, got, "a failed read must return no content, never a partial or empty note")
	assert.True(t, errors.Is(err, ErrNoteEvicted) || errors.Is(err, ErrNoteUnreadable),
		"want a typed lifecycle error, got %v", err)

	missing, err := ReadNoteContent(OSLinkFS(), filepath.Join(dir, "never-written.md"))
	require.Error(t, err)
	assert.Nil(t, missing)
	assert.True(t, errors.Is(err, ErrNoteUnreadable))
}

// A directory is not a note. Reading one must be refused rather than producing
// whatever io.ReadAll makes of it on this platform.
func TestLifecycle_DirectoryIsNotANote(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "folder")
	require.NoError(t, os.Mkdir(sub, 0o755))

	_, err := ReadNoteContent(OSLinkFS(), sub)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoteUnreadable))
	assert.Contains(t, err.Error(), "directory")
}

// ---------------------------------------------------------------------------
// The refusal has to be readable. Every LifecycleError names its subject,
// because a refusal nobody can route is a refusal nobody acts on.
// ---------------------------------------------------------------------------

func TestLifecycle_ErrorNamesItsSubject(t *testing.T) {
	e := &LifecycleError{
		Op: "knowledge_rename", State: MountStateBroken,
		Collection: "Elicify KB", Path: "projects/kickoff.md",
		Detail: "the folder recorded for mount \"kb\" is not on disk",
		Err:    ErrMountBroken,
	}
	msg := e.Error()
	for _, want := range []string{"knowledge_rename", "Elicify KB", "projects/kickoff.md", "kb"} {
		assert.True(t, strings.Contains(msg, want), "%q must appear in %q", want, msg)
	}
	assert.True(t, errors.Is(e, ErrMountBroken))
	assert.Equal(t, "repoint_mount", e.Remedy())
}
