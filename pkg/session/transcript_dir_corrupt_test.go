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

// A directory where a transcript partition belongs is a corrupt store. It must
// surface as a read error, never read as an empty transcript.
func TestReadTranscript_DirectoryWhereAPartitionBelongsIsAnError(t *testing.T) {
	for _, name := range []string{transcriptFileName, "2026-03-04.jsonl"} {
		t.Run(name, func(t *testing.T) {
			s, sid := newEffectsSession(t)
			require.NoError(t, os.MkdirAll(filepath.Join(s.baseDir, sid, name), 0o700))
			_, err := s.ReadTranscript(sid)
			require.Error(t, err, "a directory named %s must not read as 'nothing there'", name)
		})
	}
}

// Control: an unrelated subdirectory (and a session with no transcript at all)
// is still a normal, empty read.
func TestReadTranscript_UnrelatedDirectoryAndMissingTranscriptStayEmpty(t *testing.T) {
	s, sid := newEffectsSession(t)
	require.NoError(t, os.MkdirAll(filepath.Join(s.baseDir, sid, "uploads"), 0o700))
	entries, err := s.ReadTranscript(sid)
	require.NoError(t, err)
	require.Empty(t, entries)
}
