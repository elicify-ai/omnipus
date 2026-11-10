package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Docs F03-R2: the newest valid, nonzero ENTRY timestamp governs catch-up;
// changing only file modification time must not change that returned time.
func TestMemoryRound3RecapEntryClock(t *testing.T) {
	al := &AgentLoop{}
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	want := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	data := "{\"timestamp\":\"2026-10-20T10:00:00Z\"}\n" +
		"not valid JSON\n" +
		"{\"timestamp\":\"0001-01-01T00:00:00Z\"}\n" +
		"{\"timestamp\":\"2026-10-20T12:00:00Z\"}\n" +
		"{\"timestamp\":\"not a timestamp\"}\n" +
		"{\"timestamp\":\"2026-10-20T11:00:00Z\"}\n"
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	for _, mtime := range []time.Time{want.Add(-24 * time.Hour), want.Add(24 * time.Hour)} {
		require.NoError(t, os.Chtimes(path, mtime, mtime))
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.False(t, info.ModTime().Equal(want), "instrument: file age differs from newest entry")
		got, valid := al.newestTranscriptTimestamp(dir)
		require.True(t, valid)
		require.Equal(t, want, got, "max valid entry time, not file mtime or last line")
	}
	require.NoError(t, os.WriteFile(path, []byte("{\"timestamp\":\"0001-01-01T00:00:00Z\"}\nnot JSON\n{}\n"), 0o600))
	got, valid := al.newestTranscriptTimestamp(dir)
	require.False(t, valid, "no valid nonzero entry is not usable recap evidence")
	require.True(t, got.IsZero())
	require.NoError(t, os.Remove(path))
	got, valid = al.newestTranscriptTimestamp(dir)
	require.False(t, valid)
	require.True(t, got.IsZero())
}
