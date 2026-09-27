package gateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/stretchr/testify/require"
)

// newTestFileMediaStore constructs a media store whose debounced registry
// writer is drained before the calling test's temporary OMNIPUS_HOME is
// removed. Every gateway test must use this helper instead of constructing a
// FileMediaStore directly: Stop cancels a pending debounce timer, flushes its
// registry synchronously, and joins an in-flight save.
func newTestFileMediaStore(t *testing.T) *media.FileMediaStore {
	t.Helper()
	store := media.NewFileMediaStore()
	t.Cleanup(store.Stop)
	return store
}

func TestNewTestFileMediaStore_DrainsRegistryWriterBeforeHomeChanges(t *testing.T) {
	root := t.TempDir()
	firstHome := filepath.Join(root, "first-home")
	secondHome := filepath.Join(root, "second-home")

	t.Run("schedule registry save", func(t *testing.T) {
		t.Setenv("OMNIPUS_HOME", firstHome)
		require.NoError(t, os.MkdirAll(firstHome, 0o700))
		source := filepath.Join(firstHome, "upload.txt")
		require.NoError(t, os.WriteFile(source, []byte("upload"), 0o600))

		store := newTestFileMediaStore(t)
		_, err := store.Store(source, media.MediaMeta{
			Filename:      "upload.txt",
			CleanupPolicy: media.CleanupPolicyForgetOnly,
		}, "test-upload")
		require.NoError(t, err)
	})

	require.NoError(t, os.RemoveAll(firstHome))
	t.Setenv("OMNIPUS_HOME", secondHome)
	time.Sleep(2 * time.Second)

	require.NoDirExists(t, firstHome, "the stopped store must not recreate its original home")
	require.NoDirExists(t, filepath.Join(secondHome, "media"),
		"a delayed registry save must not follow a later test's OMNIPUS_HOME")
}
