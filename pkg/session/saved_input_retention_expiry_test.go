package session

// Founder B storage acceptance pack. Oracles: the founder's B dispatch and
// lifecycle-design-b.md sections 2-5, qualified by the frozen API/storage shape
// in qa-b-brief.md. No expected value was obtained by executing the implementation.
// WRITTEN, NOT EXECUTED: the parent owns runtime RED evidence through slotrun;
// GREEN and mutation/proof-of-failability checks belong to an independent CHECK.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

func TestSavedInputRetentionExpiryMatrix(t *testing.T) {
	// Global filesystem fault seams are restored in Cleanup; no parallel cases.
	t.Run("rolled_pending_expires_with_exact_body_free_receipts", func(t *testing.T) {
		bExpiryNormal(t, false)
	})
	t.Run("ordinary_current_pending_expires_without_losing_chat_identity", func(t *testing.T) {
		bExpiryNormal(t, true)
	})
	t.Run("age_and_disabled_boundaries", bExpiryAgeBoundaries)
	t.Run("receipt_identity_is_scoped_to_the_addressed_chat", bExpiryChatIsolation)
	t.Run("invalid_raw_metadata_refuses_before_any_unlink", bExpiryInvalidMetadata)
	t.Run("unknown_missing_source_is_integrity_error_not_expiry", bExpiryUnknownMissing)
	t.Run("receipt_does_not_excuse_mismatching_raw_pending_source", bExpiryMismatchedReceipt)
	t.Run("completed_current_intent_preserves_new_current_and_notice", bExpiryCompletedCurrent)
	t.Run("unfinished_intent_refuses_same_size_same_mtime_replacement", bExpiryReplacementConflict)
	t.Run("placed_but_index_retirement_failed_is_not_undelivered", bExpiryPlacedIndexFault)
	t.Run("legacy_poll_marker_expiry_preserves_consumption", bExpiryLegacyPoll)
	t.Run("durable_poll_before_save_refuses_original_body_after_restart", bExpiryPollBeforeSave)
	t.Run("unlink_error_is_visible_and_replays_with_retention_disabled", bExpiryUnlinkError)
	t.Run("index_error_after_unlink_is_visible_and_retryable", bExpiryIndexError)
	t.Run("interruption_boundaries", bExpiryInterruptions)
}

type bExpiryInput struct {
	id      string
	body    string
	address ArchiveAddress
	seq     int64
}

type bExpiryFixture struct {
	store    *UnifiedStore
	archive  *ArchiveDayStore
	sid      string
	current  bool
	days     int
	now      time.Time
	target   string
	original []byte
	mtime    time.Time
	inputs   []bExpiryInput
}

func bExpiryNewFixture(t *testing.T, current bool, count int) *bExpiryFixture {
	t.Helper()
	f := &bExpiryFixture{store: newUnifiedStoreForTest(t), current: current, days: 7}
	// Stats flushing is unrelated to report disposition. Quiesce its background
	// writer, rather than allowing an abandoned crash-test instance to flush.
	f.store.stopStatsFlusher()
	meta, err := f.store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	f.sid = meta.ID
	f.archive, err = f.store.backend.store(f.sid)
	require.NoError(t, err)
	const oldDay = "2026-04-01"
	atDay(f.archive, oldDay)
	// A real model row makes window projection observable. Its size also makes
	// the replacement-current notice land below the old file's size boundary.
	lifecycleAppendUser(t, f.store, f.sid, strings.Repeat("old model context; ", 256))
	for i := 0; i < count; i++ {
		in := bExpiryInput{
			id:   fmt.Sprintf("saved-input-b-expiry-old-%d", i),
			body: fmt.Sprintf("PRIVATE_B_EXPIRY_BODY_%d_NEVER_RESTORE_OR_COPY", i),
		}
		in.address, err = f.store.SaveChatInput(f.sid, in.id, "mia", in.body)
		require.NoError(t, err)
		f.inputs = append(f.inputs, in)
	}
	f.target = filepath.Join(f.archive.dir(), archiveCurrentFile)
	if !current {
		atDay(f.archive, "2026-04-10")
		lifecycleAppendUser(t, f.store, f.sid, "retained conversation after old reports")
		f.target = filepath.Join(f.archive.dir(), oldDay+".jsonl")
	}
	// Keep the current fixture on its ORIGINAL append day. A new operational
	// notice then reuses the current path/day key, but is a different full Source.
	f.now = f.archive.now().UTC()
	ageFileTo(t, f.target, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	f.original, err = os.ReadFile(f.target)
	require.NoError(t, err)
	info, err := os.Stat(f.target)
	require.NoError(t, err)
	f.mtime = info.ModTime()
	idx := bExpiryRawIndex(t, f)
	require.Len(t, idx, count, "SETUP: every old report really is pending")
	for i := range f.inputs {
		in := &f.inputs[i]
		require.Equal(t, in.address, idx[in.id].Address)
		in.seq = idx[in.id].Seq
		rec, err := f.archive.ReadAt(in.address)
		require.NoError(t, err, "SETUP: the exact original source exists")
		require.Equal(t, in.body, rec.Content)
		require.Contains(t, string(f.original), in.body, "SETUP: target contains the actual report bytes")
	}
	return f
}

func bExpiryRawIndex(t *testing.T, f *bExpiryFixture) savedInputIndex {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.archive.dir(), "saved_inputs.json"))
	require.NoError(t, err)
	var idx savedInputIndex
	require.NoError(t, json.Unmarshal(data, &idx))
	require.NotNil(t, idx)
	return idx
}

func bExpiryState(t *testing.T, f *bExpiryFixture) savedInputLifecycle {
	t.Helper()
	state, exists := bExpiryStateIfPresent(t, f)
	require.True(t, exists, "a committed loss/poll claim must have a real lifecycle file")
	return state
}

func bExpiryStateIfPresent(t *testing.T, f *bExpiryFixture) (savedInputLifecycle, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.archive.dir(), "saved_input_lifecycle.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return savedInputLifecycle{}, false
	}
	require.NoError(t, err)
	// Decode the RAW bytes independently; do not use the production decoder or
	// its filtered projection as the oracle for what was actually persisted.
	var state savedInputLifecycle
	require.NoError(t, json.Unmarshal(data, &state))
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))
	require.ElementsMatch(t, []string{"version", "session_id", "deletions", "expired_reports", "poll_consumed", "notices"}, bExpiryKeys(raw))
	require.Equal(t, 1, state.Version)
	require.Equal(t, f.sid, state.SessionID)
	require.NotNil(t, state.Deletions)
	require.NotNil(t, state.ExpiredReports)
	require.NotNil(t, state.PollConsumed)
	require.NotNil(t, state.Notices)
	for _, in := range f.inputs {
		require.NotContains(t, string(data), in.body, "control metadata must never retain the expired body")
	}
	return state, true
}

func bExpiryKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

// Read actual archive bytes with a strict decoder. Production ScanAll tolerates
// malformed lines; that tolerance must not make an absence/count assertion green.
func bExpiryRecords(t *testing.T, f *bExpiryFixture) []ArchiveRecord {
	t.Helper()
	entries, err := os.ReadDir(f.archive.dir())
	require.NoError(t, err)
	var records []ArchiveRecord
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(f.archive.dir(), entry.Name())
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		decoder := json.NewDecoder(bytes.NewReader(data))
		for {
			var rec ArchiveRecord
			err := decoder.Decode(&rec)
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err, "strict evidence scan failed in %s", path)
			require.NotEmpty(t, rec.ID, "evidence scan must not accept a null/empty archive record")
			records = append(records, rec)
		}
	}
	return records
}

func bExpiryCountID(records []ArchiveRecord, id string) int {
	count := 0
	for _, rec := range records {
		if rec.ID == id {
			count++
		}
	}
	return count
}

func bExpiryPendingIDs(t *testing.T, f *bExpiryFixture) []string {
	t.Helper()
	pending, err := f.store.PendingChatInputs(context.Background(), f.sid)
	require.NoError(t, err)
	ids := make([]string, 0, len(pending))
	for _, input := range pending {
		ids = append(ids, input.ID)
	}
	return ids
}

func bExpiryRequireTyped(t *testing.T, err error, f *bExpiryFixture, in bExpiryInput, receipt savedInputExpiration) {
	t.Helper()
	require.ErrorIs(t, err, ErrSavedInputExpired, "known retention loss has its own terminal code")
	var got *SavedInputExpiry
	require.ErrorAs(t, err, &got)
	require.Equal(t, f.sid, got.SessionID)
	require.Equal(t, in.id, got.InputID)
	require.Equal(t, in.address, got.Source)
	require.Equal(t, receipt.ReceiptID, got.ReceiptID)
	require.True(t, f.now.Equal(got.ExpiredAt), "typed expiry carries committed logical time")
	require.Contains(t, err.Error(), in.id)
	require.Contains(t, err.Error(), f.sid)
	require.NotContains(t, err.Error(), in.body)
}

func bExpiryAssertReceipt(t *testing.T, f *bExpiryFixture) savedInputLifecycle {
	t.Helper()
	state := bExpiryState(t, f)
	require.Len(t, state.Deletions, 1, "one aged file incarnation was selected")
	require.Len(t, state.ExpiredReports, len(f.inputs))
	require.Len(t, state.Notices, len(f.inputs), "one durable outbox entry per report")
	require.Empty(t, state.PollConsumed, "expiry is not a consumption claim")
	sum := sha256.Sum256(f.original)
	seen := make(map[string]bool)
	for _, in := range f.inputs {
		require.Contains(t, state.ExpiredReports, in.id)
		e := state.ExpiredReports[in.id]
		require.Equal(t, in.id, e.InputID)
		require.Equal(t, in.address, e.Source, "loss binds the full address, not merely a day key")
		require.Equal(t, in.seq, e.SavedSeq)
		require.True(t, f.now.Equal(e.ExpiredAt), "expiry time is the fixture's committed time, not reopen/retry time")
		digest, err := hex.DecodeString(e.ReceiptID)
		require.NoError(t, err)
		require.Len(t, digest, sha256.Size, "receipt ID has the frozen hash identity shape")
		require.False(t, seen[e.ReceiptID], "different reports must not share one loss identity")
		seen[e.ReceiptID] = true
		require.Equal(t, "saved-input-retention-expiry:"+e.ReceiptID, e.NoticeID)
		require.Contains(t, state.Deletions, e.DeletionID)
		d := state.Deletions[e.DeletionID]
		require.Equal(t, e.DeletionID, d.ID)
		require.Equal(t, in.address.PartitionKey, d.PartitionKey)
		require.Equal(t, filepath.Base(f.target), d.FileName)
		require.Equal(t, int64(len(f.original)), d.SizeBytes)
		require.Equal(t, f.mtime.UnixNano(), d.MtimeUnixNano)
		require.Equal(t, hex.EncodeToString(sum[:]), d.SHA256, "fingerprint was captured before deletion")
		require.Equal(t, f.days, d.RetentionDays)
		require.True(t, f.mtime.Before(d.CutoffUTC), "eligibility is actual file age")
		require.True(t, f.now.Equal(d.CommittedAt))
		if !f.current {
			require.True(t, f.now.Add(-time.Duration(f.days)*24*time.Hour).Equal(d.CutoffUTC))
		}
		require.Contains(t, state.Notices, e.ReceiptID)
		n := state.Notices[e.ReceiptID]
		require.Equal(t, e.ReceiptID, n.ReceiptID)
		require.Equal(t, "retention-expiry:"+e.ReceiptID, n.OutcomeID)
		require.Equal(t, 1, n.CopyVersion)
		require.True(t, f.now.Equal(n.CreatedAt))
	}
	return state
}

func bExpiryAssertCompleted(t *testing.T, f *bExpiryFixture) savedInputLifecycle {
	t.Helper()
	state := bExpiryAssertReceipt(t, f)
	records := bExpiryRecords(t, f)
	var wantPending []string
	for _, in := range f.inputs {
		e := state.ExpiredReports[in.id]
		d := state.Deletions[e.DeletionID]
		require.True(t, d.Unlinked, "deletion checkpoint must be durable before path reuse")
		require.Zero(t, bExpiryCountID(records, in.id), "the exact old payload is physically absent")
		for _, rec := range records {
			require.NotContains(t, rec.Content, in.body)
			if rec.ModelMessage != nil {
				msg, err := DecodeModelPayload(*rec.ModelMessage)
				require.NoError(t, err)
				require.NotContains(t, msg.Content, in.body, "the model outbox cannot retain the original report body")
			}
		}
		n := state.Notices[e.ReceiptID]
		require.True(t, n.ChatSaved, "owed notice was actually saved, not just attempted")
		require.NotNil(t, n.Source)
		require.Equal(t, e.NoticeID, n.Source.EntryID)
		require.Equal(t, 1, bExpiryCountID(records, e.NoticeID), "exactly one retained operational notice")
		rec, err := f.archive.ReadAt(*n.Source)
		require.NoError(t, err)
		require.Equal(t, EntryTypeSystem, rec.Type)
		require.Equal(t, "system", rec.Role)
		require.Equal(t, SystemSubtypeExpiryNotice, rec.SystemSubtype)
		require.Equal(t, ViewMembershipChat, rec.ViewMembership)
		// Independently specified operational facts, never savedInputExpiryCopy.
		require.Contains(t, rec.Content, in.id)
		require.Contains(t, rec.Content, f.sid)
		require.Contains(t, rec.Content, f.now.UTC().Format(time.RFC3339))
		require.Contains(t, strings.ToLower(rec.Content), "expired undelivered")
		require.Contains(t, strings.ToLower(rec.Content), "retention")
		require.Contains(t, rec.Content, "The original report will not arrive.")
		require.NotContains(t, strings.ToLower(rec.Content), "helper failed")
		require.NotContains(t, strings.ToLower(rec.Content), "work failed")
		require.Nil(t, rec.HelperEvent, "original report loss is not a helper failure event")
		require.NotNil(t, rec.ModelMessage, "chat display alone is not a model-delivery outbox")
		msg, err := DecodeModelPayload(*rec.ModelMessage)
		require.NoError(t, err)
		require.Equal(t, "user", msg.Role, "system presentation still prepares user-role model input")
		require.Equal(t, rec.Content, msg.Content)
		wantPending = append(wantPending, e.NoticeID)
	}
	// A pending EXPIRY NOTICE is legitimate; only the expired originals must go.
	require.ElementsMatch(t, wantPending, bExpiryPendingIDs(t, f))
	if f.current {
		current, err := os.ReadFile(f.target)
		require.NoError(t, err, "notice legitimately creates a replacement current transcript")
		require.NotEqual(t, f.original, current, "actual current bytes changed, not an unrelated missing path")
		for _, in := range f.inputs {
			require.NotContains(t, string(current), in.body)
			require.NotContains(t, string(current), `"id":"`+in.id+`"`)
		}
	} else {
		_, err := os.Stat(f.target)
		require.ErrorIs(t, err, fs.ErrNotExist, "the exact aged rolled file was removed")
	}
	return state
}

func bExpiryAssertRefusals(t *testing.T, f *bExpiryFixture, state savedInputLifecycle) {
	t.Helper()
	for _, in := range f.inputs {
		e := state.ExpiredReports[in.id]
		_, placed, err := f.store.SavedChatInput(f.sid, in.id)
		bExpiryRequireTyped(t, err, f, in, e)
		require.False(t, placed, "expired undelivered is not falsely reported as delivered")
		_, err = f.store.SaveChatInput(f.sid, in.id, "mia", in.body)
		bExpiryRequireTyped(t, err, f, in, e)
		_, _, err = f.store.PlaceSavedInput(context.Background(), f.sid, in.address)
		bExpiryRequireTyped(t, err, f, in, e)
	}
}

func bExpiryAdmitNotices(t *testing.T, f *bExpiryFixture, state savedInputLifecycle) {
	t.Helper()
	for _, in := range f.inputs {
		n := state.Notices[state.ExpiredReports[in.id].ReceiptID]
		require.NotNil(t, n.Source)
		slot, _, err := f.store.PlaceSavedInput(context.Background(), f.sid, *n.Source)
		require.NoError(t, err, "normal saved-input fence admits the prepared notice")
		rows, err := f.archive.readOrdinalRows(slot.Ordinal, slot.Ordinal+1)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.NotNil(t, rows[0].Source, "normal ordinal Source, not a volatile flag, proves model admission")
		require.Equal(t, *n.Source, *rows[0].Source)
		lookedUp, placed, err := f.store.SavedChatInput(f.sid, n.Source.EntryID)
		require.NoError(t, err)
		require.Equal(t, *n.Source, lookedUp)
		require.True(t, placed)
		_, _, err = f.store.PlaceSavedInput(context.Background(), f.sid, *n.Source)
		require.ErrorIs(t, err, ErrAlreadyConsumed)
		view, err := f.store.WindowView(context.Background(), f.sid)
		require.NoError(t, err)
		messages, _ := view.History()
		occurrences := 0
		for _, msg := range messages {
			if strings.Contains(msg.Content, in.id) && strings.Contains(msg.Content, "expired undelivered") {
				require.Equal(t, "user", msg.Role)
				occurrences++
			}
			require.NotContains(t, msg.Content, in.body)
		}
		require.Equal(t, 1, occurrences, "one durable model placement for this loss notice")
	}
	require.Empty(t, bExpiryPendingIDs(t, f), "notice consumption does not resurrect an original")
}

func bExpiryNormal(t *testing.T, current bool) {
	f := bExpiryNewFixture(t, current, 2)
	removed, err := f.store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed, "two undelivered reports do not pin their one aged partition")
	state := bExpiryAssertCompleted(t, f)
	bExpiryAssertRefusals(t, f, state)
	// A second sweep is idempotent even when no old pending row remains.
	removed, err = f.store.RetentionSweep(7)
	require.NoError(t, err)
	require.Zero(t, removed)
	again := bExpiryAssertCompleted(t, f)
	require.Equal(t, state.ExpiredReports, again.ExpiredReports)
	require.Equal(t, state.Notices, again.Notices)
	bExpiryAdmitNotices(t, f, state)
	lifecycleAssertFreshHelperReport(t, f.store, f.sid, "b-expiry-normal")
	bExpiryAssertRefusals(t, f, state)
}

func bExpiryAgeBoundaries(t *testing.T) {
	// The enabled cutoff's complete +/- one-second neighborhood and disabled
	// min-1/min are derived from one fixed archive clock, never wall-clock sleeps.
	for _, tc := range []struct {
		name    string
		days    int
		offset  time.Duration
		expires bool
	}{
		{"negative_disabled", -1, -time.Second, false},
		{"zero_disabled", 0, -time.Second, false},
		{"first_enabled_day", 1, -time.Second, true},
		{"strictly_older", 7, -time.Second, true},
		{"equal_cutoff", 7, 0, false},
		{"younger_cutoff", 7, time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := bExpiryNewFixture(t, false, 1)
			if tc.days > 0 {
				f.days = tc.days
			}
			f.mtime = f.now.Add(-time.Duration(f.days) * 24 * time.Hour).Add(tc.offset)
			ageFileTo(t, f.target, f.mtime)
			removed, err := f.store.RetentionSweep(tc.days)
			require.NoError(t, err)
			if tc.expires {
				require.Equal(t, 1, removed)
				bExpiryAssertCompleted(t, f)
				return
			}
			require.Zero(t, removed)
			actual, err := os.ReadFile(f.target)
			require.NoError(t, err)
			require.Equal(t, f.original, actual)
			in := f.inputs[0]
			addr, placed, err := f.store.SavedChatInput(f.sid, in.id)
			require.NoError(t, err)
			require.Equal(t, in.address, addr)
			require.False(t, placed)
			require.Equal(t, []string{in.id}, bExpiryPendingIDs(t, f))
			state, exists := bExpiryStateIfPresent(t, f)
			if exists {
				require.Empty(t, state.ExpiredReports)
				require.Empty(t, state.Notices)
				require.Empty(t, state.Deletions)
			}
		})
	}
}

func bExpiryChatIsolation(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	store.stopStatsFlusher()
	const id, body = "saved-input-b-same-id-two-chats", "private report shared only as fixture input"
	var fixtures []*bExpiryFixture
	for i := 0; i < 2; i++ {
		meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
		require.NoError(t, err)
		archive, err := store.backend.store(meta.ID)
		require.NoError(t, err)
		atDay(archive, "2026-04-01")
		addr, err := store.SaveChatInput(meta.ID, id, "mia", body)
		require.NoError(t, err)
		require.Zero(t, addr.ByteOffset, "SETUP: same exact source triple in two different chats")
		atDay(archive, "2026-04-10")
		lifecycleAppendUser(t, store, meta.ID, "retained chat")
		f := &bExpiryFixture{store: store, archive: archive, sid: meta.ID, now: archive.now(),
			target: filepath.Join(archive.dir(), "2026-04-01.jsonl"), inputs: []bExpiryInput{{id: id, body: body, address: addr}}}
		ageFileTo(t, f.target, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
		fixtures = append(fixtures, f)
	}
	require.NotEqual(t, fixtures[0].sid, fixtures[1].sid)
	require.Equal(t, fixtures[0].inputs[0].address, fixtures[1].inputs[0].address)
	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 2, removed)
	first, second := bExpiryState(t, fixtures[0]), bExpiryState(t, fixtures[1])
	require.Len(t, first.ExpiredReports, 1)
	require.Len(t, second.ExpiredReports, 1)
	a, b := first.ExpiredReports[id], second.ExpiredReports[id]
	require.NotEmpty(t, a.ReceiptID)
	require.NotEmpty(t, b.ReceiptID)
	require.NotEqual(t, a.ReceiptID, b.ReceiptID, "session identity is part of the loss identity")
	require.NotEqual(t, a.NoticeID, b.NoticeID)
	for i, f := range fixtures {
		s := []savedInputLifecycle{first, second}[i]
		n := s.Notices[s.ExpiredReports[id].ReceiptID]
		require.NotNil(t, n.Source)
		rec, err := f.archive.ReadAt(*n.Source)
		require.NoError(t, err)
		require.Contains(t, rec.Content, f.sid)
		require.NotContains(t, rec.Content, fixtures[1-i].sid, "notice must not address the other chat")
	}
}

func bExpiryInvalidMetadata(t *testing.T) {
	t.Run("index_invalid_full_source", func(t *testing.T) {
		bExpiryMalformedCase(t, "index", []byte(`{"saved-input-b-expiry-old-0":{"address":{"partition_key":"2026-04-01","byte_offset":-1,"entry_id":"saved-input-b-expiry-old-0"},"from_ordinal":1,"seq":1}}`))
	})
	for _, target := range []string{"index", "lifecycle"} {
		for _, tc := range []struct {
			name string
			raw  string
		}{
			{"zero_bytes", ""},
			{"null", "null"},
			{"malformed_json", "{"},
		} {
			t.Run(target+"_"+tc.name, func(t *testing.T) {
				bExpiryMalformedCase(t, target, []byte(tc.raw))
			})
		}
	}
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"zero_version", `{"version":0,"session_id":"SESSION","deletions":{},"expired_reports":{},"poll_consumed":{},"notices":{}}`},
		{"null_required_map", `{"version":1,"session_id":"SESSION","deletions":{},"expired_reports":{},"poll_consumed":{},"notices":null}`},
		{"wrong_session", `{"version":1,"session_id":"another-chat","deletions":{},"expired_reports":{},"poll_consumed":{},"notices":{}}`},
	} {
		t.Run("lifecycle_"+tc.name, func(t *testing.T) {
			bExpiryMalformedCase(t, "lifecycle", []byte(tc.raw))
		})
	}
}

func bExpiryMalformedCase(t *testing.T, target string, corrupt []byte) {
	t.Helper()
	for _, current := range []bool{false, true} {
		t.Run(fmt.Sprintf("current_%t", current), func(t *testing.T) {
			bExpiryMalformedCurrentCase(t, target, corrupt, current)
		})
	}
}

func bExpiryMalformedCurrentCase(t *testing.T, target string, corrupt []byte, current bool) {
	t.Helper()
	f := bExpiryNewFixture(t, current, 1)
	corrupt = bytes.ReplaceAll(corrupt, []byte("SESSION"), []byte(f.sid))
	if corrupt == nil {
		corrupt = make([]byte, 0)
	}
	name, contextText := "saved_inputs.json", "saved input index"
	if target == "lifecycle" {
		name, contextText = "saved_input_lifecycle.json", "saved input lifecycle"
	}
	path := filepath.Join(f.archive.dir(), name)
	require.NoError(t, os.WriteFile(path, corrupt, 0o600))
	oldRemove := removePartitionFn
	attempts := 0
	t.Cleanup(func() { removePartitionFn = oldRemove })
	removePartitionFn = func(path string) error {
		attempts++
		return oldRemove(path)
	}
	removed, err := f.store.RetentionSweep(7)
	require.Error(t, err)
	require.ErrorContains(t, err, contextText, "the failing metadata is identified, not demoted")
	require.False(t, errors.Is(err, ErrSavedInputExpired), "corruption cannot authorize expiry")
	require.Zero(t, removed)
	require.Zero(t, attempts, "raw metadata must be validated BEFORE destructive unlink")
	actual, readErr := os.ReadFile(f.target)
	require.NoError(t, readErr)
	require.Equal(t, f.original, actual, "original source bytes remain untouched")
	actual, readErr = os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, corrupt, actual, "invalid metadata must not be rewritten as an empty snapshot")
	if target == "index" {
		_, statErr := os.Stat(filepath.Join(f.archive.dir(), "saved_input_lifecycle.json"))
		require.ErrorIs(t, statErr, fs.ErrNotExist, "no fabricated loss receipt over corrupt raw ownership")
	}
}

func bExpiryUnknownMissing(t *testing.T) {
	f := bExpiryNewFixture(t, false, 1)
	in := f.inputs[0]
	require.NoError(t, os.Remove(f.target), "SETUP: unexplained loss, not a retention transaction")
	_, err := f.store.PendingChatInputs(context.Background(), f.sid)
	require.Error(t, err, "unexplained source loss is a visible integrity error")
	require.ErrorContains(t, err, in.id)
	require.False(t, errors.Is(err, ErrSavedInputExpired))
	removed, err := f.store.RetentionSweep(7)
	require.Error(t, err)
	require.ErrorContains(t, err, in.id)
	require.False(t, errors.Is(err, ErrSavedInputExpired))
	require.Zero(t, removed)
	state, exists := bExpiryStateIfPresent(t, f)
	if exists {
		require.Empty(t, state.ExpiredReports, "a missing file is not positive expiry evidence")
		require.Empty(t, state.Notices)
		require.Empty(t, state.Deletions)
	}
	require.Contains(t, bExpiryRawIndex(t, f), in.id, "do not silently discard unexplained ownership")
}

func bExpiryMismatchedReceipt(t *testing.T) {
	f := bExpiryNewFixture(t, false, 1)
	removed, err := f.store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	state := bExpiryAssertCompleted(t, f)
	in := f.inputs[0]
	wrong := in.address
	wrong.ByteOffset++ // Structurally valid, but not the Source authorized by the receipt.
	idx := bExpiryRawIndex(t, f)
	idx[in.id] = savedInputIndexEntry{Address: wrong, FromOrdinal: 0, Seq: in.seq}
	corrupt, err := json.Marshal(idx)
	require.NoError(t, err)
	path := filepath.Join(f.archive.dir(), "saved_inputs.json")
	require.NoError(t, os.WriteFile(path, corrupt, 0o600))
	_, err = f.store.PendingChatInputs(context.Background(), f.sid)
	require.Error(t, err, "same ID/day is insufficient: full Source must match the committed receipt")
	require.ErrorContains(t, err, in.id)
	require.False(t, errors.Is(err, ErrSavedInputExpired), "wrong-address ownership is corruption, not authorized expiry")
	removed, err = f.store.RetentionSweep(7)
	require.Error(t, err)
	require.ErrorContains(t, err, in.id)
	require.False(t, errors.Is(err, ErrSavedInputExpired))
	require.Zero(t, removed)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, corrupt, after, "do not repair away a mismatching raw owner using a blanket expiry exemption")
	require.Equal(t, state.ExpiredReports, bExpiryState(t, f).ExpiredReports)
}

func bExpiryReopen(t *testing.T, f *bExpiryFixture) {
	t.Helper()
	baseDir := f.store.BaseDir()
	require.NoError(t, f.store.Close())
	reopened, err := NewUnifiedStore(baseDir)
	if reopened != nil {
		t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	}
	require.NoError(t, err, "real constructor must replay committed work before saved-input interpretation")
	f.store = reopened
	f.store.stopStatsFlusher()
	f.archive, err = f.store.backend.store(f.sid)
	require.NoError(t, err)
	// This only fixes future append/sweep time; replay above already ran under
	// the real constructor. Receipt identity/time must survive that change.
	atDay(f.archive, f.now.Format("2006-01-02"))
}

func bExpiryCompletedCurrent(t *testing.T) {
	f := bExpiryNewFixture(t, true, 1)
	removed, err := f.store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	state := bExpiryAssertCompleted(t, f)
	e := state.ExpiredReports[f.inputs[0].id]
	d := state.Deletions[e.DeletionID]
	n := state.Notices[e.ReceiptID]
	require.NotNil(t, n.Source)
	require.Equal(t, d.PartitionKey, n.Source.PartitionKey, "SETUP: replacement intentionally reuses the old day key")
	require.Less(t, n.Source.ByteOffset, d.SizeBytes, "SETUP: a key/offset-only exemption would misclassify this NEW notice")
	require.NotEqual(t, f.inputs[0].address, *n.Source)
	require.NotContains(t, d.RetiredNoticeSources, *n.Source, "the old deletion captured no future notice Source")
	current, err := os.ReadFile(f.target)
	require.NoError(t, err)
	bExpiryReopen(t, f)
	after, err := os.ReadFile(f.target)
	require.NoError(t, err)
	require.Equal(t, current, after, "completed old intent cannot unlink or replace a new current incarnation")
	require.Equal(t, []string{e.NoticeID}, bExpiryPendingIDs(t, f), "fresh notice survives exact-source retirement projection")
	removed, err = f.store.RetentionSweep(0)
	require.NoError(t, err)
	require.Zero(t, removed)
	after, err = os.ReadFile(f.target)
	require.NoError(t, err)
	require.Equal(t, current, after)
	bExpiryAdmitNotices(t, f, state)
	lifecycleAssertFreshHelperReport(t, f.store, f.sid, "b-completed-current")
	bExpiryAssertRefusals(t, f, state)
}

func bExpiryCut(t *testing.T, f *bExpiryFixture, boundary string) {
	t.Helper()
	// Flush before the interruption: no surviving background writer or dirty
	// stats may access the abandoned instance's process-local locks afterward.
	f.store.stopStatsFlusher()
	require.NoError(t, f.store.FlushSessionStats(f.sid))
	archive := f.archive
	oldObserver := archive.onWriteStep
	t.Cleanup(func() { archive.onWriteStep = oldObserver })
	sentinel := &struct{ boundary string }{boundary}
	observed := false
	archive.onWriteStep = func(step string) {
		if oldObserver != nil {
			oldObserver(step)
		}
		if step == boundary {
			observed = true
			panic(sentinel)
		}
	}
	var caught any
	var sweepErr error
	func() {
		defer func() { caught = recover() }()
		_, sweepErr = f.store.RetentionSweep(7)
	}()
	archive.onWriteStep = oldObserver
	if caught != nil && caught != sentinel {
		panic(caught)
	}
	if !observed {
		t.Fatalf("BLOCKED: observation boundary %s was not reached by the real sweep (err=%v); required by founder B crash pack", boundary, sweepErr)
	}
	require.Same(t, sentinel, caught, "instrument: interruption came from the requested real boundary")
}

func bExpiryReplacementConflict(t *testing.T) {
	f := bExpiryNewFixture(t, false, 1)
	bExpiryCut(t, f, "expiry.intent_committed")
	state := bExpiryAssertReceipt(t, f)
	in := f.inputs[0]
	require.False(t, state.Deletions[state.ExpiredReports[in.id].DeletionID].Unlinked)
	newID := strings.Replace(in.id, "old", "new", 1)
	newBody := strings.Repeat("R", len(in.body))
	replacement := bytes.ReplaceAll(f.original, []byte(in.id), []byte(newID))
	replacement = bytes.ReplaceAll(replacement, []byte(in.body), []byte(newBody))
	require.Equal(t, len(f.original), len(replacement), "SETUP: size alone must NOT distinguish the replacement")
	require.NotEqual(t, sha256.Sum256(f.original), sha256.Sum256(replacement))
	require.NoError(t, os.Remove(f.target))
	require.NoError(t, os.WriteFile(f.target, replacement, 0o600))
	ageFileTo(t, f.target, f.mtime)
	info, err := os.Stat(f.target)
	require.NoError(t, err)
	require.Equal(t, int64(len(f.original)), info.Size())
	require.Equal(t, f.mtime.UnixNano(), info.ModTime().UnixNano())
	baseDir := f.store.BaseDir()
	require.NoError(t, f.store.Close())
	reopened, err := NewUnifiedStore(baseDir)
	if reopened != nil {
		t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	}
	require.Error(t, err, "unfinished exact-file intent must refuse a replacement")
	require.ErrorContains(t, err, "incarnation")
	require.ErrorContains(t, err, "replacement")
	actual, readErr := os.ReadFile(f.target)
	require.NoError(t, readErr)
	require.Equal(t, replacement, actual, "replay preserved the replacement's ACTUAL bytes")
	after := bExpiryState(t, f)
	require.Equal(t, state.ExpiredReports, after.ExpiredReports)
	require.NotContains(t, after.ExpiredReports, newID, "old receipt is not authority over the new report")
}

func bExpiryPlacedIndexFault(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	store.stopStatsFlusher()
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	archive, err := store.backend.store(meta.ID)
	require.NoError(t, err)
	atDay(archive, "2026-04-01")
	const id, body = "saved-input-b-placed-stale-index", "already consumed report, not undelivered"
	addr, err := store.SaveChatInput(meta.ID, id, "mia", body)
	require.NoError(t, err)
	previousFault := savedInputIndexWriteFault.Load()
	t.Cleanup(func() { savedInputIndexWriteFault.Store(previousFault) })
	fault := errors.New("b-expiry: pending-index retirement failed after placement")
	calls := 0
	SetSavedInputIndexWriteFaultForTest(func(sid string) error {
		if sid == meta.ID {
			calls++
			return fault
		}
		return nil
	})
	slot, _, err := store.PlaceSavedInput(context.Background(), meta.ID, addr)
	require.ErrorIs(t, err, fault)
	require.GreaterOrEqual(t, calls, 1, "instrument: failure was the post-placement index retirement")
	require.Equal(t, body, slot.Message.Content)
	savedInputIndexWriteFault.Store(previousFault)
	f := &bExpiryFixture{store: store, archive: archive, sid: meta.ID}
	require.Contains(t, bExpiryRawIndex(t, f), id, "SETUP: consumed source still has a stale raw pending owner")
	rows, err := archive.readOrdinalRows(slot.Ordinal, slot.Ordinal+1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].Source)
	require.Equal(t, addr, *rows[0].Source, "instrument: durable model placement really committed")
	atDay(archive, "2026-04-10")
	lifecycleAppendUser(t, store, meta.ID, "retained conversation")
	f.now = archive.now()
	oldPath := filepath.Join(archive.dir(), "2026-04-01.jsonl")
	ageFileTo(t, oldPath, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	_, err = os.Stat(oldPath)
	require.ErrorIs(t, err, fs.ErrNotExist)
	state := bExpiryState(t, f)
	require.NotContains(t, state.ExpiredReports, id, "placement is positive delivery proof, even if retirement failed")
	require.Empty(t, state.Notices, "do not tell the parent this consumed report expired undelivered")
	require.Empty(t, bExpiryPendingIDs(t, f))
	bExpiryAssertConsumed(t, f, id, addr)
	bExpiryReopen(t, f)
	bExpiryAssertConsumed(t, f, id, addr)
	require.NotContains(t, bExpiryState(t, f).ExpiredReports, id)
	lifecycleAssertFreshHelperReport(t, f.store, f.sid, "b-placed-index-fault")
}

func bExpiryAssertConsumed(t *testing.T, f *bExpiryFixture, id string, source ArchiveAddress) {
	t.Helper()
	consumed, err := f.store.SavedInputConsumed(f.sid, id)
	require.NoError(t, err)
	require.True(t, consumed, "terminal consumption survives source/marker/placement payload retention")
	require.NotContains(t, bExpiryPendingIDs(t, f), id)
	_, _, err = f.store.PlaceSavedInput(context.Background(), f.sid, source)
	require.ErrorIs(t, err, ErrAlreadyConsumed)
	require.False(t, errors.Is(err, ErrSavedInputExpired), "consumed is not expired-undelivered")
}

func bExpiryLegacyPoll(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	store.stopStatsFlusher()
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	archive, err := store.backend.store(meta.ID)
	require.NoError(t, err)
	atDay(archive, "2026-04-01")
	const id, body = "saved-input-b-legacy-polled", "already polled surviving report body"
	addr, err := store.SaveChatInput(meta.ID, id, "mia", body)
	require.NoError(t, err)
	atDay(archive, "2026-04-02")
	rest := strings.TrimPrefix(id, "saved-input-")
	markerAddr, err := archive.Append(ArchiveRecord{TranscriptEntry: TranscriptEntry{
		ID: "consumed-" + rest, Type: EntryTypeSystem, Role: "system", Content: "consumed " + rest,
		AgentID: "mia", Timestamp: archive.now(), ViewMembership: ViewMembershipChat,
	}})
	require.NoError(t, err, "SETUP: legacy marker, deliberately without the new poll API")
	atDay(archive, "2026-04-10")
	lifecycleAppendUser(t, store, meta.ID, "retained conversation")
	f := &bExpiryFixture{store: store, archive: archive, sid: meta.ID, now: archive.now()}
	before, exists := bExpiryStateIfPresent(t, f)
	if exists {
		require.NotContains(t, before.PollConsumed, id, "SETUP: consumption initially lives only in the legacy marker")
	}
	require.Contains(t, bExpiryRawIndex(t, f), id)
	sourcePath := filepath.Join(archive.dir(), "2026-04-01.jsonl")
	markerPath := filepath.Join(archive.dir(), "2026-04-02.jsonl")
	// Age is mtime, not key: only the later marker file is eligible.
	ageFileTo(t, sourcePath, time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC))
	ageFileTo(t, markerPath, time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC))
	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	_, err = os.Stat(markerPath)
	require.ErrorIs(t, err, fs.ErrNotExist, "actual legacy marker partition expired")
	rec, err := archive.ReadAt(addr)
	require.NoError(t, err)
	require.Equal(t, body, rec.Content, "original report source deliberately survives")
	state := bExpiryState(t, f)
	require.Contains(t, state.PollConsumed, id)
	poll := state.PollConsumed[id]
	require.Equal(t, id, poll.InputID)
	require.Equal(t, "consumed-"+rest, poll.MarkerID)
	require.NotNil(t, poll.MarkerAddress)
	require.Equal(t, markerAddr, *poll.MarkerAddress)
	require.NotNil(t, poll.Source)
	require.Equal(t, addr, *poll.Source)
	require.True(t, time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC).Equal(poll.ConsumedAt))
	require.NotContains(t, state.ExpiredReports, id)
	require.Empty(t, state.Notices)
	bExpiryAssertConsumed(t, f, id, addr)
	bExpiryReopen(t, f)
	bExpiryAssertConsumed(t, f, id, addr)
	retried, err := f.store.SaveChatInput(f.sid, id, "mia", body)
	if err == nil {
		require.Equal(t, addr, retried, "source-intact consumed retry may be idempotent, never a new append")
	} else {
		require.ErrorIs(t, err, ErrAlreadyConsumed, "the other allowed disposition is explicit consumed refusal")
	}
	require.Equal(t, 1, bExpiryCountID(bExpiryRecords(t, f), id))
	require.NotContains(t, bExpiryPendingIDs(t, f), id)
	require.Equal(t, poll, bExpiryState(t, f).PollConsumed[id], "promotion survives restart and a retry")
	lifecycleAssertFreshHelperReport(t, f.store, f.sid, "b-legacy-marker-expiry")
}

func bExpiryPollBeforeSave(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	store.stopStatsFlusher()
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	archive, err := store.backend.store(meta.ID)
	require.NoError(t, err)
	f := &bExpiryFixture{store: store, archive: archive, sid: meta.ID, now: time.Now().UTC()}
	const id, body = "saved-input-b-poll-before-save", "PRIVATE_POLLED_FIRST_BODY_MUST_NEVER_BE_APPENDED"
	already, err := store.RecordSavedInputPollConsumed(meta.ID, id)
	require.NoError(t, err)
	require.False(t, already, "first durable final-read claim wins")
	state := bExpiryState(t, f)
	require.Len(t, state.PollConsumed, 1)
	require.Contains(t, state.PollConsumed, id)
	poll := state.PollConsumed[id]
	require.Equal(t, id, poll.InputID)
	require.Nil(t, poll.Source, "poll-before-save must not invent a payload address")
	require.False(t, poll.ConsumedAt.IsZero())
	require.Equal(t, "consumed-b-poll-before-save", poll.MarkerID)
	already, err = store.RecordSavedInputPollConsumed(meta.ID, id)
	require.NoError(t, err)
	require.True(t, already)
	require.Equal(t, poll, bExpiryState(t, f).PollConsumed[id])
	bExpiryReopen(t, f)
	consumed, err := f.store.SavedInputConsumed(f.sid, id)
	require.NoError(t, err)
	require.True(t, consumed)
	_, err = f.store.SaveChatInput(f.sid, id, "mia", body)
	require.ErrorIs(t, err, ErrAlreadyConsumed)
	require.False(t, errors.Is(err, ErrSavedInputExpired))
	records := bExpiryRecords(t, f)
	require.Zero(t, bExpiryCountID(records, id), "same-ID save after durable poll must not append an old body")
	for _, rec := range records {
		require.NotContains(t, rec.Content, body)
	}
	require.Empty(t, bExpiryPendingIDs(t, f))
	already, err = f.store.RecordSavedInputPollConsumed(f.sid, id)
	require.NoError(t, err)
	require.True(t, already, "consumption proof did not disappear across restart")
	after := bExpiryState(t, f)
	require.Equal(t, poll, after.PollConsumed[id])
	require.Empty(t, after.ExpiredReports)
	require.Empty(t, after.Notices)
	lifecycleAssertFreshHelperReport(t, f.store, f.sid, "b-poll-before-save")
}

func bExpiryUnlinkError(t *testing.T) {
	for _, current := range []bool{false, true} {
		t.Run(fmt.Sprintf("current_%t", current), func(t *testing.T) {
			f := bExpiryNewFixture(t, current, 1)
			oldRemove := removePartitionFn
			t.Cleanup(func() { removePartitionFn = oldRemove })
			fault := errors.New("b-expiry: committed partition unlink denied")
			calls := 0
			removePartitionFn = func(path string) error {
				if path == f.target {
					calls++
					return fault
				}
				return oldRemove(path)
			}
			removed, err := f.store.RetentionSweep(7)
			require.ErrorIs(t, err, fault)
			require.Zero(t, removed)
			require.GreaterOrEqual(t, calls, 1, "instrument: real unlink was attempted and failed")
			original, err := os.ReadFile(f.target)
			require.NoError(t, err)
			require.Equal(t, f.original, original)
			state := bExpiryAssertReceipt(t, f)
			e := state.ExpiredReports[f.inputs[0].id]
			require.False(t, state.Deletions[e.DeletionID].Unlinked)
			removePartitionFn = oldRemove
			if current {
				// Disabled retention must also complete an already committed loss
				// in the SAME process, not only when boot replay happens to run.
				removed, err = f.store.RetentionSweep(0)
				require.NoError(t, err)
				require.Equal(t, 1, removed, "disabled replay reports the committed file it actually removed")
				bExpiryAssertCompleted(t, f)
			}
			bExpiryReopen(t, f)
			completed := bExpiryAssertCompleted(t, f)
			require.Equal(t, state.ExpiredReports, completed.ExpiredReports)
			removed, err = f.store.RetentionSweep(0)
			require.NoError(t, err)
			require.Zero(t, removed)
			bExpiryAssertCompleted(t, f)
			lifecycleAssertFreshHelperReport(t, f.store, f.sid, "b-unlink-error")
			bExpiryAssertRefusals(t, f, completed)
		})
	}
}

func bExpiryIndexError(t *testing.T) {
	f := bExpiryNewFixture(t, false, 1)
	previousFault := savedInputIndexWriteFault.Load()
	t.Cleanup(func() { savedInputIndexWriteFault.Store(previousFault) })
	fault := errors.New("b-expiry: index cleanup failed after irreversible unlink")
	calls := 0
	SetSavedInputIndexWriteFaultForTest(func(sid string) error {
		if sid == f.sid {
			calls++
			return fault
		}
		return nil
	})
	removed, err := f.store.RetentionSweep(7)
	require.ErrorIs(t, err, fault)
	require.Equal(t, 1, removed, "a failed projection cannot undo a real unlink")
	require.GreaterOrEqual(t, calls, 1, "instrument: real index publication failed")
	_, err = os.Stat(f.target)
	require.ErrorIs(t, err, fs.ErrNotExist)
	state := bExpiryAssertReceipt(t, f)
	require.Contains(t, bExpiryRawIndex(t, f), f.inputs[0].id, "stale RAW ownership remains but exact receipt supersedes it")
	savedInputIndexWriteFault.Store(previousFault)
	bExpiryReopen(t, f)
	completed := bExpiryAssertCompleted(t, f)
	require.Equal(t, state.ExpiredReports, completed.ExpiredReports)
	bExpiryAssertRefusals(t, f, completed)
	lifecycleAssertFreshHelperReport(t, f.store, f.sid, "b-index-cleanup-error")
}

func bExpiryInterruptions(t *testing.T) {
	for _, current := range []bool{false, true} {
		for _, boundary := range []string{
			"expiry.intent_prepared",
			"expiry.intent_committed",
			"expiry.partition_unlinked",
			"expiry.unlink_checkpointed",
			"expiry.index_cleaned",
			"expiry.window_projected",
			"expiry.notice_saved",
			"expiry.notice_checkpointed",
		} {
			t.Run(fmt.Sprintf("current_%t/%s", current, boundary), func(t *testing.T) {
				bExpiryInterruptedCase(t, current, boundary)
			})
		}
	}
}

func bExpiryInterruptedCase(t *testing.T, current bool, boundary string) {
	t.Helper()
	f := bExpiryNewFixture(t, current, 1)
	bExpiryCut(t, f, boundary)
	var committed savedInputLifecycle
	if boundary == "expiry.intent_prepared" {
		actual, err := os.ReadFile(f.target)
		require.NoError(t, err)
		require.Equal(t, f.original, actual, "precommit interruption preserves the actual source file")
		state, exists := bExpiryStateIfPresent(t, f)
		if exists {
			require.Empty(t, state.Deletions)
			require.Empty(t, state.ExpiredReports)
			require.Empty(t, state.Notices)
		}
	} else {
		committed = bExpiryAssertReceipt(t, f)
	}
	bExpiryReopen(t, f)
	if boundary == "expiry.intent_prepared" {
		// Disable does not authorize a new loss when no intent ever committed.
		removed, err := f.store.RetentionSweep(0)
		require.NoError(t, err)
		require.Zero(t, removed)
		actual, err := os.ReadFile(f.target)
		require.NoError(t, err)
		require.Equal(t, f.original, actual)
		require.Equal(t, []string{f.inputs[0].id}, bExpiryPendingIDs(t, f))
		addr, placed, err := f.store.SavedChatInput(f.sid, f.inputs[0].id)
		require.NoError(t, err)
		require.Equal(t, f.inputs[0].address, addr)
		require.False(t, placed)
		removed, err = f.store.RetentionSweep(7)
		require.NoError(t, err)
		require.Equal(t, 1, removed, "a subsequent enabled sweep makes the first actual expiry decision")
	} else {
		// Constructor replay, not an enabled nightly tick, must finish the loss
		// and prepare its owed notice before any saved-input interpretation.
		completed := bExpiryAssertCompleted(t, f)
		require.Equal(t, committed.ExpiredReports, completed.ExpiredReports)
		removed, err := f.store.RetentionSweep(0)
		require.NoError(t, err)
		require.Zero(t, removed, "disabled retention still replays, but makes no new expiry decision")
	}
	completed := bExpiryAssertCompleted(t, f)
	bExpiryAssertRefusals(t, f, completed)
	// A second real reopen must reuse the existing notice, including the
	// append-before-checkpoint boundary. This is process-crash/error testing,
	// not power-loss or filesystem/syscall atomicity certification.
	bExpiryReopen(t, f)
	again := bExpiryAssertCompleted(t, f)
	require.Equal(t, completed.ExpiredReports, again.ExpiredReports)
	require.Equal(t, completed.Notices, again.Notices)
	bExpiryAdmitNotices(t, f, again)
	lifecycleAssertFreshHelperReport(t, f.store, f.sid, "b-interrupted-"+strings.TrimPrefix(boundary, "expiry."))
	bExpiryAssertRefusals(t, f, again)
}
