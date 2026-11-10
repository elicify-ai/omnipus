package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/stretchr/testify/require"
)

type cleanupRegistrar interface {
	Helper()
	Cleanup(fn func())
}

// newTestFileMediaStore constructs a media store whose debounced registry
// writer is drained before the calling test's temporary OMNIPUS_HOME is
// removed. Every gateway test must use this helper instead of constructing a
// FileMediaStore directly: Stop cancels a pending debounce timer, flushes its
// registry synchronously, and joins an in-flight save.
func newTestFileMediaStore(t cleanupRegistrar) *media.FileMediaStore {
	t.Helper()
	store := media.NewFileMediaStore()
	t.Cleanup(store.Stop)
	return store
}

type cleanupRecorder struct {
	cleanups []func()
}

func (*cleanupRecorder) Helper() {}

func (r *cleanupRecorder) Cleanup(cleanup func()) {
	r.cleanups = append(r.cleanups, cleanup)
}

func TestNewTestFileMediaStore_DrainsRegistryWriterBeforeHomeChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	source := filepath.Join(home, "upload.txt")
	require.NoError(t, os.WriteFile(source, []byte("upload"), 0o600))

	recorder := &cleanupRecorder{}
	store := newTestFileMediaStore(recorder)
	ref, err := store.Store(source, media.MediaMeta{
		Filename:      "upload.txt",
		CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, "test-upload")
	require.NoError(t, err)
	require.Len(t, recorder.cleanups, 1, "the helper must register exactly one store cleanup")

	recorder.cleanups[0]()

	reloaded := media.NewFileMediaStore()
	require.NoError(t, reloaded.LoadRegistry(), "registered cleanup must flush synchronously")
	resolved, err := reloaded.Resolve(ref)
	require.NoError(t, err)
	require.Equal(t, source, resolved)
}
