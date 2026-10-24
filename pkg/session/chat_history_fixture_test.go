package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Source: seams-decision.md G. Test-only private plan/receipt types; neither a
// product API nor a second archive/index/projection implementation.
type chatHistoryFixtureDay struct { // not-wire-format: private test-fixture input.
	Day     string            `json:"day"`
	Entries []TranscriptEntry `json:"entries"`
	MTime   *time.Time        `json:"mtime,omitempty"`
}

type chatHistoryFixturePlan struct { // not-wire-format: private driver plan v1.
	SchemaVersion int                     `json:"schema_version"`
	DataRoot      string                  `json:"data_root"`
	Action        string                  `json:"action"`
	SessionID     string                  `json:"session_id,omitempty"`
	Days          []chatHistoryFixtureDay `json:"days,omitempty"`
	RetentionDays *int                    `json:"retention_days,omitempty"`
}

type chatHistoryFixtureResult struct { // not-wire-format: private driver receipt v1.
	SchemaVersion     int    `json:"schema_version"`
	Action            string `json:"action"`
	SessionID         string `json:"session_id,omitempty"`
	WrittenRecords    int    `json:"written_records"`
	RolledPartitions  int    `json:"rolled_partitions"`
	RemovedPartitions int    `json:"removed_partitions"`
}

func buildChatHistoryDayFixture(t testing.TB, store *UnifiedStore, sessionID string, days []chatHistoryFixtureDay) []ArchiveAddress {
	t.Helper()
	_, err := store.GetMeta(sessionID)
	require.NoError(t, err, "fixture uses an existing identity, never creates/adopts one")
	dir := filepath.Join(store.BaseDir(), sessionID)
	state, err := store.readChatHistoryIndexState(context.Background(), sessionID)
	require.NoError(t, err, "real published baseline accessor must be operational before fixture seeding")
	require.Equal(t, ChatHistoryIndexFormatVersion, state.FormatVersion)
	require.NotEmpty(t, state.IndexGeneration)
	// Fixed artifact bytes are an architect-owned storage contract, not guessed
	// generation/log filenames. This positive control must fail on missing index.
	format, err := os.ReadFile(filepath.Join(dir, ".chat-history", "FORMAT"))
	require.NoError(t, err, "G requires a healthy new-session baseline before seeding")
	require.Equal(t, "omnipus-chat-history-index/1\n", string(format))
	require.FileExists(t, filepath.Join(dir, ".chat-history", "checkpoint.json"))
	partitions, err := store.transcriptPartitionPaths(sessionID)
	require.NoError(t, err)
	for _, path := range partitions {
		info, statErr := os.Stat(path)
		require.NoError(t, statErr)
		require.Zero(t, info.Size(), "seed_days requires an empty canonical session")
	}
	archive, err := store.archiveStore(sessionID)
	require.NoError(t, err)
	oldStoreClock, oldArchiveClock := store.archiveNow, archive.now
	defer func() { store.archiveNow = oldStoreClock; archive.now = oldArchiveClock }()
	written := make([]ArchiveAddress, 0)
	writtenDays := make([]chatHistoryFixtureDay, 0)
	var previous time.Time
	for _, day := range days {
		at, err := time.Parse("2006-01-02", day.Day)
		require.NoError(t, err, "strict canonical UTC append day")
		require.Equal(t, day.Day, at.Format("2006-01-02"))
		require.True(t, previous.IsZero() || at.After(previous), "fixture days strictly increase")
		previous = at
		store.archiveNow = func() time.Time { return at }
		archive.now = store.archiveNow // The addressed store may already be cached.
		for _, entry := range day.Entries {
			address, err := store.AppendTranscriptAddressed(sessionID, entry)
			require.NoError(t, err, "real canonical writer/index publication on %s", day.Day)
			require.Equal(t, day.Day, address.PartitionKey, "server append clock, not entry timestamp, chooses partition")
			written = append(written, address)
		}
		if len(day.Entries) > 0 {
			writtenDays = append(writtenDays, day)
		}
	}
	for i, day := range writtenDays {
		if i == len(writtenDays)-1 {
			mark, err := os.ReadFile(filepath.Join(dir, "transcript.day"))
			require.NoError(t, err)
			require.Equal(t, day.Day+"\n", string(mark), "last actual append day stays current")
			require.FileExists(t, filepath.Join(dir, "transcript.jsonl"))
			continue
		}
		rolled := filepath.Join(dir, day.Day+".jsonl")
		require.FileExists(t, rolled, "real partitionForAppendLocked rollover")
		if day.MTime != nil {
			require.NoError(t, os.Chtimes(rolled, *day.MTime, *day.MTime))
		}
	}
	return written
}

func chatHistoryFixtureRoot(t testing.TB, planPath, dataRoot string) string {
	t.Helper()
	require.True(t, filepath.IsAbs(dataRoot), "no developer-home/default-root fallback")
	require.True(t, filepath.IsAbs(planPath), "driver plan must be an explicit absolute path")
	root, err := filepath.EvalSymlinks(filepath.Clean(dataRoot))
	require.NoError(t, err)
	temp, err := filepath.EvalSymlinks(os.TempDir())
	require.NoError(t, err)
	rel, err := filepath.Rel(temp, root)
	require.NoError(t, err)
	require.True(t, rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), "owned gateway fixture must be under the native temporary root")
	require.True(t, strings.HasPrefix(filepath.Base(root), "omnipus-e2e-gwproc-"), "independent GatewayProcess throwaway-home naming control")
	leaf, err := os.Lstat(dataRoot)
	require.NoError(t, err)
	require.Zero(t, leaf.Mode()&os.ModeSymlink, "fixture root itself cannot be a symlink")
	plan, err := filepath.EvalSymlinks(planPath)
	require.NoError(t, err)
	rel, err = filepath.Rel(root, plan)
	require.NoError(t, err)
	require.True(t, rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)), "plan is outside canonical source")
	for _, name := range []string{"config.json", "sessions"} {
		info, err := os.Lstat(filepath.Join(root, name))
		require.NoError(t, err, "independent owned gateway-root control")
		require.Zero(t, info.Mode()&os.ModeSymlink, "no canonical-tree symlink escape")
	}
	require.NoError(t, filepath.WalkDir(filepath.Join(root, "sessions"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("fixture refuses session-tree symlink: %s", path)
		}
		return nil
	}), "no source/index path may escape the isolated session tree through a symlink")
	return root
}

// Explicit section-G utility exception: only this prebuilt fixture driver skips
// without its plan. No feature assertion/test is skipped or hidden by this.
func TestChatHistoryFixtureDriver(t *testing.T) {
	planPath := os.Getenv("OMNIPUS_CHAT_HISTORY_FIXTURE_PLAN")
	if planPath == "" {
		t.Skip("fixture driver requires an explicitly supplied private plan; no feature assertions here")
	}
	b, err := os.ReadFile(planPath)
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	var plan chatHistoryFixturePlan
	require.NoError(t, decoder.Decode(&plan))
	require.Equal(t, 1, plan.SchemaVersion)
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF, "one strict plan, no trailing JSON")
	root := chatHistoryFixtureRoot(t, planPath, plan.DataRoot)
	require.Contains(t, []string{"seed_days", "sweep"}, plan.Action)
	if plan.Action == "seed_days" {
		require.True(t, plan.SessionID != "" && plan.SessionID != "." && plan.SessionID != ".." && !strings.ContainsAny(plan.SessionID, "/\\\x00"), "session identity is exactly one native path component")
		info, statErr := os.Lstat(filepath.Join(root, "sessions", plan.SessionID))
		require.NoError(t, statErr)
		require.True(t, info.IsDir())
		require.Zero(t, info.Mode()&os.ModeSymlink)
	} else {
		require.NotNil(t, plan.RetentionDays, "sweep has an explicit value, including disabled0/negative")
	}
	store, err := NewUnifiedStoreWithHome(filepath.Join(root, "sessions"), root)
	require.NoError(t, err)
	closed := false
	t.Cleanup(func() {
		if !closed {
			require.NoError(t, store.Close())
		}
	})
	result := chatHistoryFixtureResult{SchemaVersion: 1, Action: plan.Action, SessionID: plan.SessionID}
	if plan.Action == "seed_days" {
		addresses := buildChatHistoryDayFixture(t, store, plan.SessionID, plan.Days)
		result.WrittenRecords = len(addresses)
		paths, pathsErr := store.transcriptPartitionPaths(plan.SessionID)
		require.NoError(t, pathsErr)
		for _, path := range paths {
			if filepath.Base(path) != "transcript.jsonl" {
				result.RolledPartitions++
			}
		}
	} else {
		result.SessionID = ""
		result.RemovedPartitions, err = store.RetentionSweep(*plan.RetentionDays)
		require.NoError(t, err, "actual retention owner, never direct fixture unlink")
	}
	require.NoError(t, store.Close(), "close/join background work before receipt or gateway restart")
	closed = true
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	_, err = fmt.Printf("CHAT_HISTORY_FIXTURE_RESULT=%s\n", encoded)
	require.NoError(t, err)
}

func TestChatHistoryFixture_DayBuilderUsesAppendDayAndRealRollover(t *testing.T) {
	store, sid := newEffectsSession(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	days := []chatHistoryFixtureDay{
		{Day: "2026-01-01", Entries: []TranscriptEntry{{ID: "one", Role: "user", Content: "old", AgentID: "mia", Timestamp: at.AddDate(0, 0, 20)}}, MTime: &old},
		{Day: "2026-01-02", Entries: []TranscriptEntry{{ID: "two", Role: "user", Content: "middle", AgentID: "mia", Timestamp: at}}},
		{Day: "2026-01-03", Entries: []TranscriptEntry{{ID: "three", Role: "user", Content: "current", AgentID: "mia", Timestamp: at}}},
	}
	addresses := buildChatHistoryDayFixture(t, store, sid, days)
	require.Len(t, addresses, 3)
	require.Equal(t, []string{"2026-01-01", "2026-01-02", "2026-01-03"}, []string{addresses[0].PartitionKey, addresses[1].PartitionKey, addresses[2].PartitionKey})
	info, err := os.Stat(filepath.Join(store.BaseDir(), sid, "2026-01-01.jsonl"))
	require.NoError(t, err)
	require.Equal(t, old.UnixNano(), info.ModTime().UnixNano(), "age only an actual rolled partition")
}
