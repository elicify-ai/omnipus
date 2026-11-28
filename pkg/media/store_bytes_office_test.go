package media

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// StoreBytes' documented contract: writing or registration failure removes
// the staged file; success registers the exact bytes, metadata and scope.
// Only the external registration boundary is substituted in failure cases.
// Historical RED and implementation mutation proof are not claimed here;
// StoreBytes already exists, and independent CHECK owns mutation probes.
func TestStoreBytes_Cleanup(t *testing.T) {
	for _, policy := range []CleanupPolicy{CleanupPolicyDeleteOnCleanup, CleanupPolicyForgetOnly} {
		t.Run(string(policy), func(t *testing.T) {
			store := newTestStore(t)
			dir := filepath.Join(t.TempDir(), "new", "staging")
			data := []byte("exact preview page\x00\xff")
			sum := sha256.Sum256(data)
			meta := MediaMeta{Filename: "page-1.png", ContentType: "image/png", SHA256: hex.EncodeToString(sum[:]), Source: "tool:preview_document", CleanupPolicy: policy}
			const scope = "tool:inline:session:store-bytes-test"
			ref, err := StoreBytes(store, dir, "page-*.png", data, meta, scope)
			require.NoError(t, err)
			path, gotMeta, resolveErr := store.ResolveWithMeta(ref)
			require.NoError(t, resolveErr)
			require.Equal(t, meta, gotMeta)
			require.Equal(t, dir, filepath.Dir(path))
			matches, globErr := filepath.Glob(filepath.Join(dir, "page-*.png"))
			require.NoError(t, globErr)
			require.Equal(t, []string{path}, matches)
			got, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			require.Equal(t, data, got)
			require.NoError(t, store.ReleaseAll("unrelated-scope"))
			require.FileExists(t, path, "the supplied scope, not a different scope, owns cleanup")
			require.NoError(t, store.ReleaseAll(scope))
			_, resolveErr = store.Resolve(ref)
			require.Error(t, resolveErr, "release removes the reference under either cleanup policy")
			if policy == CleanupPolicyDeleteOnCleanup {
				_, statErr := os.Stat(path)
				require.ErrorIs(t, statErr, os.ErrNotExist)
			} else {
				remaining, remainingErr := os.ReadFile(path)
				require.NoError(t, remainingErr)
				require.Equal(t, data, remaining, "forget-only never deletes the registered file")
			}
		})
	}

	t.Run("registration_failure_removes_only_staged_file", func(t *testing.T) {
		dir := t.TempDir()
		keep := filepath.Join(dir, "keep.txt")
		require.NoError(t, os.WriteFile(keep, []byte("unrelated"), 0600))
		failure := errors.New("registration refused by test store")
		data := []byte("fully written before registration")
		meta := MediaMeta{Filename: "page.png", ContentType: "image/png", Source: "tool:preview_document", CleanupPolicy: CleanupPolicyDeleteOnCleanup}
		store := &storeBytesRegistration{t: t, data: data, meta: meta, scope: "failed-scope", err: failure}
		ref, err := StoreBytes(store, dir, "page-*.png", data, meta, "failed-scope")
		require.Empty(t, ref)
		require.ErrorIs(t, err, failure)
		require.EqualError(t, err, "registration refused by test store")
		require.Equal(t, 1, store.calls)
		_, statErr := os.Stat(store.path)
		require.ErrorIs(t, statErr, os.ErrNotExist, "registration failure must unlink the staged file")
		entries, readErr := os.ReadDir(dir)
		require.NoError(t, readErr)
		require.Len(t, entries, 1)
		require.Equal(t, "keep.txt", entries[0].Name())
		remaining, keepErr := os.ReadFile(keep)
		require.NoError(t, keepErr)
		require.Equal(t, []byte("unrelated"), remaining)
	})

	t.Run("mkdir_failure_never_registers", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "not-a-directory")
		require.NoError(t, os.WriteFile(file, []byte("keep"), 0600))
		store := &storeBytesRegistration{t: t}
		ref, err := StoreBytes(store, file, "page-*", nil, MediaMeta{}, "scope")
		require.Empty(t, ref)
		var pathErr *os.PathError
		require.ErrorAs(t, err, &pathErr)
		require.Equal(t, "mkdir", pathErr.Op)
		require.Equal(t, file, pathErr.Path)
		require.Zero(t, store.calls)
		got, readErr := os.ReadFile(file)
		require.NoError(t, readErr)
		require.Equal(t, []byte("keep"), got)
	})

	t.Run("create_failure_never_registers", func(t *testing.T) {
		dir := t.TempDir()
		store := &storeBytesRegistration{t: t}
		ref, err := StoreBytes(store, dir, "nested/page-*", nil, MediaMeta{}, "scope")
		require.Empty(t, ref)
		require.EqualError(t, err, "createtemp nested/page-*: pattern contains path separator")
		require.Zero(t, store.calls)
		entries, readErr := os.ReadDir(dir)
		require.NoError(t, readErr)
		require.Empty(t, entries)
	})
}

type storeBytesRegistration struct {
	MediaStore
	t     *testing.T
	data  []byte
	meta  MediaMeta
	scope string
	err   error
	calls int
	path  string
}

func (s *storeBytesRegistration) Store(path string, meta MediaMeta, scope string) (string, error) {
	s.t.Helper()
	s.calls++
	s.path = path
	got, err := os.ReadFile(path)
	require.NoError(s.t, err, "registration sees the actual fully written staged file")
	require.Equal(s.t, s.data, got)
	require.Equal(s.t, s.meta, meta)
	require.Equal(s.t, s.scope, scope)
	return "", s.err
}
