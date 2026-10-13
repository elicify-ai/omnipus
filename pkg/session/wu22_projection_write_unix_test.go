//go:build darwin || linux

package session

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// §4.2 explicitly requires saveMetaLocked's error, not just validation/read
// failures. A named pipe gives a deterministic filesystem barrier: the reader
// opens valid metadata, then the fixture replaces the target with a directory
// BEFORE releasing valid JSON and EOF. The subsequent atomic rename must fail.
// This injects only at the filesystem edge, without mocking any store method,
// relying on chmod, or depending on whether the test runs as root.
func TestWU22ProjectionMetadataWriteErrorIsReturned(t *testing.T) {
	wu22Stores(t, func(t *testing.T, writer SessionStore, base, key string) {
		path := filepath.Join(base, key, "backend_meta.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, syscall.Mkfifo(path, 0o600))
		done := make(chan error, 1)
		go func() {
			pipe, err := os.OpenFile(path, os.O_WRONLY, 0o600)
			if err != nil {
				done <- err
				return
			}
			// The writer's open cannot complete without the real metadata
			// reader. That reader now holds the inode we can safely unlink.
			if err := os.Remove(path); err != nil {
				done <- errors.Join(err, pipe.Close())
				return
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				done <- errors.Join(err, pipe.Close())
				return
			}
			_, writeErr := pipe.Write([]byte("{}"))
			done <- errors.Join(writeErr, pipe.Close())
		}()
		pk := memory.ProjectionKey{ToolCallID: "wu22-call", ArchiveLine: 0}
		err, returned := wu22WriterCall(t, writer, "SetProjectionState", key, pk, memory.ProjectionCapped)
		select {
		case fixtureErr := <-done:
			require.NoError(t, fixtureErr, "instrument: valid metadata released only after rename target became a directory")
		case <-time.After(5 * time.Second): // hang guard, not a performance oracle
			t.Fatal("SETUP: metadata-read filesystem barrier did not finish")
		}
		assert.True(t, returned, "saveMetaLocked failure must not be log-only (§4.2)")
		if assert.Error(t, err, "failed projection publication must reach caller") {
			var rename *os.LinkError
			require.ErrorAs(t, err, &rename, "original atomic-rename failure must survive")
			assert.Equal(t, "rename", rename.Op)
			assert.Equal(t, path, rename.New)
		}
		require.NoError(t, os.Remove(path))
		assert.Empty(t, writer.Projection(key).Entries, "failed projection was not published")
		assert.NoError(t, writer.Save(key), "Save has no sticky error state")
	})
}
