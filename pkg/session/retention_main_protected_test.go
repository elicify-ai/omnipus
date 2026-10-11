// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Founder rule: "a main chat is deleted only with its agent". Retention may
// expire a main chat's old content (FR-007) but must never delete the main
// session itself - its directory, identity and live partition.

package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func ageEverythingUnder(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	old := time.Now().Add(-age)
	require.NoError(t, filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		return os.Chtimes(p, old, old)
	}))
}

func seedTwoDayTranscript(t *testing.T, s *UnifiedStore, sid string) *time.Time {
	t.Helper()
	day := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	setArchiveDay(s, &day)
	for i, d := range []time.Time{day, day.AddDate(0, 0, 1)} {
		day = d
		_, err := s.AppendTranscriptAddressed(sid, TranscriptEntry{ID: "m" + string(rune('a'+i)), Role: "user",
			Content: "message", Timestamp: d})
		require.NoError(t, err)
	}
	return &day
}

func TestRetentionSweep_NeverDeletesAnIdleMainSession(t *testing.T) {
	s := newTestStore(t)
	meta, err := s.GetOrCreateMainSession("ws-1", "mia")
	require.NoError(t, err)
	clock := seedTwoDayTranscript(t, s, meta.ID)
	dir := filepath.Join(s.baseDir, meta.ID)
	ageEverythingUnder(t, dir, 400*24*time.Hour)
	*clock = time.Now()

	_, err = s.RetentionSweep(90)
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(dir, "meta.json"))
	require.NoError(t, statErr, "the main session's identity must survive retention")
	got, err := s.GetMeta(meta.ID)
	require.NoError(t, err)
	require.Equal(t, SessionTypeMain, got.Type)
	// The live partition is never expired; the older rolled day is.
	_, err = s.ReadTranscript(meta.ID)
	require.NoError(t, err)
}

// Control: the same idle history on an ordinary chat is still removed whole, so
// the exemption is main-only and the check above could have seen a deletion.
func TestRetentionSweep_StillDeletesAnIdleOrdinaryChat(t *testing.T) {
	s, sid := newEffectsSession(t)
	clock := seedTwoDayTranscript(t, s, sid)
	dir := filepath.Join(s.baseDir, sid)
	ageEverythingUnder(t, dir, 400*24*time.Hour)
	*clock = time.Now()

	_, err := s.RetentionSweep(90)
	require.NoError(t, err)
	_, statErr := os.Stat(dir)
	require.True(t, os.IsNotExist(statErr), "an idle ordinary chat is removed")
}
