// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A directory with window/archive files and no meta.json (what a window key
// that is not a session leaves behind) is not a session: it is skipped with one
// clear warning and counted separately - not reported, retried and counted as an
// unreadable session at every boot.
func TestLoadMetaCache_DirectoryWithoutMetaIsAnOrphanNotAFailedSession(t *testing.T) {
	base := t.TempDir()
	orphan := filepath.Join(base, "agent:judge:verify:5f0e9c1c-0000-4000-8000-000000000001")
	require.NoError(t, os.MkdirAll(orphan, 0o700))
	for _, f := range []string{"transcript.jsonl", "transcript.day", "backend_meta.json", "ordinal_rows.log", "ordinal_offsets.bin"} {
		require.NoError(t, os.WriteFile(filepath.Join(orphan, f), []byte("x\n"), 0o600))
	}

	s, err := NewUnifiedStore(base)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	require.Equal(t, 1, s.OrphanSessionDirCount())
	require.Equal(t, 0, s.CacheLoadFailureCount(), "an orphan directory is not an unreadable session")
	listed, err := s.ListSessions()
	require.NoError(t, err)
	require.Empty(t, listed, "it is not listed as a session")
	_, statErr := os.Stat(filepath.Join(orphan, "transcript.jsonl"))
	require.NoError(t, statErr, "the directory is left alone")
}

// A session whose meta.json exists but is corrupt is still an unreadable
// session, counted as before - the orphan rule must not hide real corruption.
func TestLoadMetaCache_CorruptMetaIsStillAFailedSession(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "session_corrupt")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"), []byte("{not json"), 0o600))

	s, err := NewUnifiedStore(base)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	require.Equal(t, 1, s.CacheLoadFailureCount())
	require.Equal(t, 0, s.OrphanSessionDirCount())
}
