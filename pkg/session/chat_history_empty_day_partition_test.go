package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Issue #1470: after a calendar-day change a chat's history stops loading
// ("Session history is unavailable. Retry after the operator restores its
// index."). The saved conversation is intact; the derived index stops updating.
//
// The sequence: a session that is still EMPTY when a gateway boot stamps it
// with a day, and that receives its first message on a LATER UTC day. The
// rollover (archive_day_store.go::partitionForAppendLocked) renames the empty
// transcript to the earlier day's partition file. That partition is unknown to
// the checkpoint and sorts before the checkpoint's current key, so the reconcile
// refused it as "archive catalog repointed" and every later record stayed
// unindexed.
//
// An empty partition carries no record address: it cannot re-point one. It is
// known-and-empty, exactly as the offline builder already handles this layout
// (chat_history_build.go::PrepareChatHistoryIndex records every stream end,
// empty ones included).
const (
	// The two UTC days the rollover under test uses. They are fixed so the
	// sequence does not depend on the wall clock.
	historyEarlierDay = "2026-01-02"
	historyLaterDay   = "2026-01-03"
)

// historyArchiveDay makes the session's append clock return day, so the UTC
// day an append is written under (the rollover rule) is deterministic.
func historyArchiveDay(t testing.TB, s *UnifiedStore, sid, day string) {
	t.Helper()
	at, err := time.Parse(transcriptDayLayout, day)
	require.NoError(t, err)
	s.archiveNow = func() time.Time { return at }
	archive, err := s.archiveStore(sid)
	require.NoError(t, err)
	archive.now = s.archiveNow
}

// historyStampEmptySessionDay reproduces the boot stamp: a gateway boot stamps
// the UTC day of a transcript that holds no record
// (unified_conv_transcript.go::convConvergeSessionTranscript). The stamp is
// normalized to a fixed day so the rollover under test does not depend on the
// wall clock.
func historyStampEmptySessionDay(t testing.TB, s *UnifiedStore, sid, day string) {
	t.Helper()
	dir := filepath.Join(s.BaseDir(), sid)
	require.NoError(t, convConvergeSessionTranscript(dir, sid))
	mark, err := os.ReadFile(filepath.Join(dir, transcriptDayMarkFile))
	require.NoError(t, err, "a boot stamps the day of a record-less transcript")
	require.NotEmpty(t, strings.TrimSpace(string(mark)))
	require.NoError(t, os.WriteFile(filepath.Join(dir, transcriptDayMarkFile), []byte(day+"\n"), 0o600))
}

// historyCheckpointOf reads the published checkpoint of one session.
func historyCheckpointOf(t testing.TB, s *UnifiedStore, sid string) historyCheckpoint {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(historyIndexDir(s, sid), "checkpoint.json"))
	require.NoError(t, err)
	var cp historyCheckpoint
	require.NoError(t, json.Unmarshal(b, &cp))
	return cp
}

// historyRequireAvailable fails with the public reason AND the private cause,
// so a broken index is reported with its mechanism and not a sanitized
// sentence.
func historyRequireAvailable(t testing.TB, err error) {
	t.Helper()
	if err == nil {
		return
	}
	var unavailable *ChatHistoryUnavailableError
	require.ErrorAs(t, err, &unavailable)
	t.Fatalf("chat history must stay available: reason=%q cause=%q", unavailable.Reason, errors.Unwrap(err))
}

// historyUserEntry is one ordinary chat message.
func historyUserEntry(id, content string, minute int) TranscriptEntry {
	return TranscriptEntry{ID: id, Role: "user", Content: content, AgentID: "mia",
		Timestamp: time.Date(2026, 1, 3, 12, minute, 0, 0, time.UTC)}
}

// The report's sequence: an empty session with a stamped day mark, its first
// message on the next UTC day (which renames the empty transcript to the
// earlier day's partition) and its second message.
func TestChatHistoryEmptyEarlierPartition_RolloverAfterStampedEmptySessionKeepsIndexConsistent(t *testing.T) {
	s, sid := newEffectsSession(t)
	dir := filepath.Join(s.BaseDir(), sid)

	historyStampEmptySessionDay(t, s, sid, historyEarlierDay)

	// The first message lands on the NEXT UTC day: the rollover renames the
	// still-empty transcript to the earlier day's partition.
	historyArchiveDay(t, s, sid, historyLaterDay)
	first, err := s.AppendTranscriptAddressed(sid, historyUserEntry("first", "first message", 0))
	require.NoError(t, err)
	require.Equal(t, historyLaterDay, first.PartitionKey)

	rolled := filepath.Join(dir, historyEarlierDay+".jsonl")
	require.FileExists(t, rolled, "the rollover renames the empty transcript to the earlier day's partition")
	info, err := os.Stat(rolled)
	require.NoError(t, err)
	require.Zero(t, info.Size(), "the rolled partition holds no record address")

	// The first message indexed cleanly: the empty partition does not exist yet.
	historyRequireAvailable(t, s.CheckChatHistoryReadiness(context.Background(), sid))

	// The second message must not break the derived index.
	second, err := s.AppendTranscriptAddressed(sid, historyUserEntry("second", "second message", 1))
	require.NoError(t, err, "the canonical save is independent of the derived index")
	require.Equal(t, historyLaterDay, second.PartitionKey)

	historyRequireAvailable(t, s.CheckChatHistoryReadiness(context.Background(), sid))

	// The index covers both records and knows the empty earlier partition.
	cp := historyCheckpointOf(t, s, sid)
	require.Equal(t, historyLaterDay, cp.CurrentKey)
	require.Contains(t, cp.ArchiveEnds, historyEarlierDay, "an absent map key must not pass as a saved empty partition")
	require.Equal(t, int64(0), cp.ArchiveEnds[historyEarlierDay],
		"the empty earlier partition is known-and-empty, exactly as the offline builder records it")
	transcript, err := os.Stat(filepath.Join(dir, archiveCurrentFile))
	require.NoError(t, err)
	require.Equal(t, transcript.Size(), cp.ArchiveEnds[historyLaterDay], "both records are indexed")
	healFixEmptyCatalogFixedPoint(t, s, sid)
}

// The exact on-disk layout of the incident: an earlier day's partition the
// checkpoint does not know, sorting before the checkpoint's current key.
func TestChatHistoryEmptyEarlierPartition_ExactLayoutIsKnownAndEmpty(t *testing.T) {
	s, sid := newEffectsSession(t)
	dir := filepath.Join(s.BaseDir(), sid)

	historyArchiveDay(t, s, sid, historyLaterDay)
	_, err := s.AppendTranscriptAddressed(sid, historyUserEntry("only", "only message", 0))
	require.NoError(t, err)

	rolled := filepath.Join(dir, historyEarlierDay+".jsonl")
	require.NoError(t, os.WriteFile(rolled, nil, 0o600))

	before := historyCheckpointOf(t, s, sid)
	require.Equal(t, historyLaterDay, before.CurrentKey)
	require.NotContains(t, before.ArchiveEnds, historyEarlierDay, "the earlier partition is unknown to the checkpoint")

	historyRequireAvailable(t, s.CheckChatHistoryReadiness(context.Background(), sid))

	after := historyCheckpointOf(t, s, sid)
	require.Contains(t, after.ArchiveEnds, historyEarlierDay, "saved coverage must contain the earlier day, not merely return a zero default")
	require.Equal(t, int64(0), after.ArchiveEnds[historyEarlierDay],
		"the known-empty partition is recorded, exactly as the offline builder records it")
	healFixEmptyCatalogFixedPoint(t, s, sid)
}

// The protection stays: a NON-empty earlier partition the checkpoint does not
// know still fails closed. It may hold record addresses the index already
// issued, so it cannot be waved through as empty.
func TestChatHistoryEmptyEarlierPartition_NonEmptyUnknownEarlierPartitionIsRefused(t *testing.T) {
	s, sid := newEffectsSession(t)
	dir := filepath.Join(s.BaseDir(), sid)

	historyArchiveDay(t, s, sid, historyLaterDay)
	_, err := s.AppendTranscriptAddressed(sid, historyUserEntry("only", "only message", 0))
	require.NoError(t, err)

	// An earlier partition holding real record bytes.
	record, err := os.ReadFile(filepath.Join(dir, archiveCurrentFile))
	require.NoError(t, err)
	require.NotEmpty(t, record)
	require.NoError(t, os.WriteFile(filepath.Join(dir, historyEarlierDay+".jsonl"), record, 0o600))

	err = s.CheckChatHistoryReadiness(context.Background(), sid)
	require.Error(t, err, "a non-empty unknown earlier partition stays refused")
	var unavailable *ChatHistoryUnavailableError
	require.ErrorAs(t, err, &unavailable)
	require.Equal(t, "index_inconsistent", unavailable.Reason)
	require.EqualError(t, errors.Unwrap(err), "archive catalog repointed")
}

// A session that is already broken (its owner holds a failure and its index
// is behind the canonical archive) heals after a restart: the fresh owner
// reconciles the bounded suffix again.
func TestChatHistoryEmptyEarlierPartition_BrokenSessionSelfHealsAfterRestart(t *testing.T) {
	s, sid := newEffectsSession(t)
	base := s.BaseDir()
	dir := filepath.Join(base, sid)

	historyArchiveDay(t, s, sid, historyLaterDay)
	_, err := s.AppendTranscriptAddressed(sid, historyUserEntry("first", "first message", 0))
	require.NoError(t, err)

	// The index falls behind the canonical archive: real archive appends that
	// bypass derived publication (a crash residue), plus the empty earlier
	// partition the checkpoint does not know. The backlog is exactly the
	// bounded catch-up allowance, so this also pins that the known-empty
	// partition is a catalog fact and not a catch-up record.
	historyUnpublishedArchive(t, s, sid, chatHistoryRecoveryMaxItems)
	require.NoError(t, os.WriteFile(filepath.Join(dir, historyEarlierDay+".jsonl"), nil, 0o600))

	// The restart: a fresh owner holds no failure.
	require.NoError(t, s.Close())
	restarted, err := NewUnifiedStore(base)
	require.NoError(t, err)
	t.Cleanup(func() { _ = restarted.Close() })

	historyRequireAvailable(t, restarted.CheckChatHistoryReadiness(context.Background(), sid))

	// The unindexed records are indexed by the restart's reconcile.
	cp := historyCheckpointOf(t, restarted, sid)
	transcript, err := os.Stat(filepath.Join(dir, archiveCurrentFile))
	require.NoError(t, err)
	require.Equal(t, transcript.Size(), cp.ArchiveEnds[historyLaterDay], "the bounded suffix is indexed again")
	require.Contains(t, cp.ArchiveEnds, historyEarlierDay, "restart must preserve membership for the empty earlier partition")
	require.Equal(t, int64(0), cp.ArchiveEnds[historyEarlierDay])
	healFixEmptyCatalogFixedPoint(t, restarted, sid)
}

// The bounded catch-up allowance stays a bound: the boot/sweep reconciler
// refuses a session further behind than the allowance. Demand readiness may
// self-heal it under selfheal-design.md; this test is retargeted to the
// non-healing entry point with its original catchup_limit oracle unchanged.
func TestChatHistoryEmptyEarlierPartition_BeyondCatchupAllowanceStaysUnavailable(t *testing.T) {
	s, sid := newEffectsSession(t)
	base := s.BaseDir()
	dir := filepath.Join(base, sid)

	historyArchiveDay(t, s, sid, historyLaterDay)
	_, err := s.AppendTranscriptAddressed(sid, historyUserEntry("first", "first message", 0))
	require.NoError(t, err)

	// 213 records behind: over the 64-fact catch-up allowance.
	historyUnpublishedArchive(t, s, sid, 213)
	require.NoError(t, os.WriteFile(filepath.Join(dir, historyEarlierDay+".jsonl"), nil, 0o600))

	require.NoError(t, s.Close())
	restarted, err := NewUnifiedStore(base)
	require.NoError(t, err)
	t.Cleanup(func() { _ = restarted.Close() })

	_, err = restarted.reconcileChatHistoryIndex(context.Background(), sid)
	require.Error(t, err, "a session over the catch-up allowance stays unavailable")
	var unavailable *ChatHistoryUnavailableError
	require.ErrorAs(t, err, &unavailable)
	require.Equal(t, "catchup_limit", unavailable.Reason)
}
