// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the final-gate S3 findings on the archive session store: H1 (the
// live retention sweep must go through the one expiry path and leave a readable
// window), H2 (a read error is not "expired"), H3 (only a provably torn tail of
// the ordinal row log may be cut) and M1 (control files are written through the
// fsyncing atomic writer). Oracles come from FR-007 and the S3 report, never
// from the implementation.

package session

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// s3AppendModel appends one checked model message and returns its slot.
func s3AppendModel(t *testing.T, s *UnifiedStore, key string, msg providers.Message, toolFor *ArchiveAddress) ModelSlot {
	t.Helper()
	slot, _, err := s.AppendModelMessage(context.Background(), key, ModelAppend{
		Message: msg, ViewMembership: ViewMembershipModel, Source: EntrySource{Kind: "user"}, ToolResultFor: toolFor,
	})
	require.NoError(t, err)
	return slot
}

// s3SeedTwoDays writes a complete day-one group (user, assistant call, tool
// result) and a day-two user message, leaving day one in a rolled partition.
// It returns the store, session id, rolled partition path and the append clock
// (the store keeps reading it, so a test moves it to "now" before sweeping).
func s3SeedTwoDays(t *testing.T) (*UnifiedStore, string, string, *time.Time) {
	t.Helper()
	s, sid := newEffectsSession(t)
	day := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	setArchiveDay(s, &day)
	s3AppendModel(t, s, sid, providers.Message{Role: "user", Content: "day one question"}, nil)
	asst := s3AppendModel(t, s, sid, providers.Message{Role: "assistant", Content: "calling", ToolCalls: []providers.ToolCall{{
		ID: "call_0", Type: "function", Function: &providers.FunctionCall{Name: "t", Arguments: "{}"}}}}, nil)
	s3AppendModel(t, s, sid, providers.Message{Role: "tool", ToolCallID: "call_0", Content: "result"}, &asst.Addr)
	day = time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	s3AppendModel(t, s, sid, providers.Message{Role: "user", Content: "day two question"}, nil)
	rolled := filepath.Join(s.baseDir, sid, "2026-03-04.jsonl")
	_, err := os.Stat(rolled)
	require.NoError(t, err, "instrument check: day one must be a rolled partition")
	return s, sid, rolled, &day
}

// H1: the LIVE retention sweep (UnifiedStore.RetentionSweep, not a helper) must
// expire a rolled partition through the archive path, so the next window read
// succeeds and starts at the first retained complete group.
func TestRetentionSweep_ExpiredPartitionLeavesAReadableWindow(t *testing.T) {
	s, sid, rolled, clock := s3SeedTwoDays(t)
	old := time.Now().Add(-200 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(rolled, old, old))
	*clock = time.Now()

	removed, err := s.RetentionSweep(90)
	require.NoError(t, err)
	require.GreaterOrEqual(t, removed, 1)
	_, statErr := os.Stat(rolled)
	require.True(t, os.IsNotExist(statErr), "the aged rolled partition must be gone")

	view, err := s.WindowView(context.Background(), sid)
	require.NoError(t, err, "the window must stay readable after the live retention sweep")
	require.Len(t, view.Live, 1)
	require.Equal(t, "day two question", view.Live[0].Message.Content)
	require.Equal(t, 3, view.State.Skip, "the cursor must advance past the expired group")
	require.Empty(t, view.Lead, "expired lead rows cannot be loaded and must not be reported")
}

// H1: a rolled partition that is younger than the cutoff is untouched and the
// window is unchanged (the strict-older rule holds on the wired path too).
func TestRetentionSweep_RetainedPartitionKeepsTheWindow(t *testing.T) {
	s, sid, rolled, clock := s3SeedTwoDays(t)
	recent := time.Now().Add(-10 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(rolled, recent, recent))
	*clock = time.Now()

	_, err := s.RetentionSweep(90)
	require.NoError(t, err)
	_, statErr := os.Stat(rolled)
	require.NoError(t, statErr, "a young partition must survive")
	view, err := s.WindowView(context.Background(), sid)
	require.NoError(t, err)
	require.Len(t, view.Live, 4)
	require.Equal(t, 0, view.State.Skip)
}

// H2: a read error that is not "the partition is gone" must NOT be treated as
// expiry. The window stays as it was and the sweep reports the error.
func TestArchiveExpiry_ReadErrorIsNotExpiry(t *testing.T) {
	store := mustStore(t, t.TempDir())
	day1, day2, rolled := rollDayOne(t, store)
	store.now = func() time.Time { return time.Now() }
	w := AddressedWindow{HasStart: true, Start: day1[0], HasEnd: true, End: day2,
		Model: []ArchiveAddress{day1[0], day1[1], day1[2], day2}}
	require.NoError(t, store.SaveWindow(w))

	// The partition exists but its record is unreadable: not a swept partition.
	raw, err := os.ReadFile(rolled)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(rolled, bytes.Repeat([]byte("X"), len(raw)), 0o600))

	_, err = store.SweepExpired(90)
	require.Error(t, err, "an unreadable slot must surface, not shorten the window")
	got, found, err := store.LoadWindow()
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, got.Model, 4, "the persisted window must be untouched")
}

// s3ThreeRows builds a session with three ordinals and returns the row-log path.
func s3ThreeRows(t *testing.T) (*UnifiedStore, string, string) {
	t.Helper()
	s, sid := newEffectsSession(t)
	for _, c := range []string{"one", "two", "three"} {
		s3AppendModel(t, s, sid, providers.Message{Role: "user", Content: c}, nil)
	}
	return s, sid, filepath.Join(s.baseDir, sid, ordinalRowsFile)
}

// s3CorruptRow overwrites row i of the log with filler of the same length,
// keeping its newline, so the log stays newline-framed.
func s3CorruptRow(t *testing.T, path string, i int) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := bytes.SplitAfter(data, []byte("\n"))
	require.Greater(t, len(lines), i+1)
	lines[i] = append(bytes.Repeat([]byte("X"), len(lines[i])-1), '\n')
	require.NoError(t, os.WriteFile(path, bytes.Join(lines, nil), 0o600))
}

// H3: a newline-terminated row that cannot be decoded is corruption, not a torn
// tail. The log must not be truncated; the error surfaces.
func TestOrdinalIndex_CorruptMiddleRowIsNotTruncated(t *testing.T) {
	s, sid, rowsPath := s3ThreeRows(t)
	s3CorruptRow(t, rowsPath, 1)
	before, err := os.ReadFile(rowsPath)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(s.baseDir, sid, ordinalOffsetsFile)))

	store, err := s.archiveStore(sid)
	require.NoError(t, err)
	_, err = store.OrdinalCount()
	require.Error(t, err)
	after, err := os.ReadFile(rowsPath)
	require.NoError(t, err)
	require.Equal(t, before, after, "a corrupt middle row must not delete the rows after it")
}

// H3: the same, reached through the offsets-table rebuild.
func TestOrdinalIndex_RebuildDoesNotTruncateOnCorruptRow(t *testing.T) {
	s, sid, rowsPath := s3ThreeRows(t)
	s3CorruptRow(t, rowsPath, 1)
	before, err := os.ReadFile(rowsPath)
	require.NoError(t, err)
	// Three zero entries: the table disagrees with the log, so a rebuild runs.
	require.NoError(t, os.WriteFile(filepath.Join(s.baseDir, sid, ordinalOffsetsFile), make([]byte, 3*ordinalOffsetSize), 0o600))

	store, err := s.archiveStore(sid)
	require.NoError(t, err)
	_, err = store.OrdinalCount()
	require.Error(t, err)
	after, err := os.ReadFile(rowsPath)
	require.NoError(t, err)
	require.Equal(t, before, after, "a rebuild must not delete rows after a corrupt one")
}

// H3 positive control: a genuinely torn tail (a partial row with no newline) is
// still cut, and the published rows survive.
func TestOrdinalIndex_TornTailIsStillCut(t *testing.T) {
	s, sid, rowsPath := s3ThreeRows(t)
	f, err := os.OpenFile(rowsPath, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(`{"o":3,"a":{"entry`)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, os.Remove(filepath.Join(s.baseDir, sid, ordinalOffsetsFile)))

	store, err := s.archiveStore(sid)
	require.NoError(t, err)
	n, err := store.OrdinalCount()
	require.NoError(t, err)
	require.Equal(t, 3, n)
	after, err := os.ReadFile(rowsPath)
	require.NoError(t, err)
	require.True(t, bytes.HasSuffix(after, []byte("\n")), "the torn partial row must be cut")
}

// M1: each archive control file is written through the atomic writer, whose
// temp name is random and which fsyncs before the rename. The old fixed
// "<name>.tmp" is not used: a directory squatting on that name must not block
// the write. (fsync itself is not observable from a test; this proves the old
// temp+rename sites are gone.)
func TestArchiveControlFiles_UseTheAtomicWriter(t *testing.T) {
	squat := func(t *testing.T, dir, name string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, name+".tmp"), 0o700))
	}
	t.Run("day mark", func(t *testing.T) {
		store := mustStore(t, t.TempDir())
		squat(t, store.dir(), archiveDayMarkFile)
		atDay(store, "2026-03-04")
		_, err := store.Append(payloadRecord(t, "a", "2026-03-04", "x"))
		require.NoError(t, err)
	})
	t.Run("window", func(t *testing.T) {
		store := mustStore(t, t.TempDir())
		squat(t, store.dir(), archiveWindowFile)
		require.NoError(t, store.SaveWindow(AddressedWindow{}))
	})
	t.Run("expiry window", func(t *testing.T) {
		store := mustStore(t, t.TempDir())
		day1, day2, rolled := rollDayOne(t, store)
		store.now = func() time.Time { return time.Now() }
		require.NoError(t, store.SaveWindow(AddressedWindow{HasStart: true, Start: day1[0], HasEnd: true, End: day2,
			Model: []ArchiveAddress{day1[0], day1[1], day1[2], day2}}))
		squat(t, store.dir(), archiveWindowFile)
		old := time.Now().Add(-200 * 24 * time.Hour)
		require.NoError(t, os.Chtimes(rolled, old, old))
		_, err := store.SweepExpired(90)
		require.NoError(t, err)
	})
	t.Run("backend meta", func(t *testing.T) {
		s, sid := newEffectsSession(t)
		squat(t, filepath.Join(s.baseDir, sid), archiveBackendMetaFile)
		s3AppendModel(t, s, sid, providers.Message{Role: "user", Content: "x"}, nil)
	})
	t.Run("ordinal offsets rebuild", func(t *testing.T) {
		s, sid, _ := s3ThreeRows(t)
		dir := filepath.Join(s.baseDir, sid)
		squat(t, dir, ordinalOffsetsFile)
		require.NoError(t, os.WriteFile(filepath.Join(dir, ordinalOffsetsFile), make([]byte, 3*ordinalOffsetSize), 0o600))
		store, err := s.archiveStore(sid)
		require.NoError(t, err)
		n, err := store.OrdinalCount()
		require.NoError(t, err)
		require.Equal(t, 3, n)
	})
}

// M4: when the day partitions cannot be listed, ReadTranscript must fail, not
// return only the current day as if it were the whole transcript.
func TestReadTranscript_PartitionListFailureIsAnError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX directory permissions and a non-root user")
	}
	s, sid := newEffectsSession(t)
	day := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	setArchiveDay(s, &day)
	for i, d := range []time.Time{day, day.AddDate(0, 0, 1)} {
		day = d
		_, err := s.AppendTranscriptAddressed(sid, TranscriptEntry{ID: fmt.Sprintf("m%d", i), Role: "user",
			Content: fmt.Sprintf("message %d", i), Timestamp: d})
		require.NoError(t, err)
	}
	entries, err := s.ReadTranscript(sid)
	require.NoError(t, err)
	require.Len(t, entries, 2, "instrument check: both days are readable while the directory lists")

	dir := filepath.Join(s.baseDir, sid)
	require.NoError(t, os.Chmod(dir, 0o300)) // searchable but not listable
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	_, err = s.ReadTranscript(sid)
	require.Error(t, err, "a failed partition listing must not read as a one-day transcript")
}
