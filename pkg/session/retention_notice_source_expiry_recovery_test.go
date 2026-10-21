package session

// Founder B, lifecycle-design-b.md section 5: a notice may itself expire.
// Positive exact control evidence must reach model-only regeneration through
// public lookup/placement; do not pin it, restore the report, or create new UI.
// WRITTEN, NOT EXECUTED. D1 is isolated from empty-session cleanup by a real
// retained day-20 keepalive. No archive rename or synthetic receipt is used.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type bNoticeAgeSessionFixture struct {
	store   *UnifiedStore
	archive *ArchiveDayStore
	sid     string
	inputID string
	body    string
	source  ArchiveAddress
	notice  RetentionNotice
	expiry  savedInputExpiration
	control savedInputNotice
}

func TestRetentionNoticeSourceExpiryRecovery(t *testing.T) {
	ctx := context.Background()
	f := bNoticeAgeSessionSeed(t)
	// A real append on day 20 rolls the original notice's current partition.
	atDay(f.archive, "2026-04-20")
	require.NoError(t, f.store.AppendTranscriptStrict(f.sid, TranscriptEntry{
		ID: "b-notice-age-day20-keepalive", Type: EntryTypeSystem, Role: "system",
		Content: "retained day-20 chat-only keepalive", AgentID: "mia",
		Timestamp: f.archive.now(), ViewMembership: ViewMembershipChat,
	}))
	rolled := filepath.Join(f.archive.dir(), f.source.PartitionKey+".jsonl")
	raw, err := os.ReadFile(rolled)
	require.NoError(t, err)
	require.Contains(t, string(raw), f.notice.Body, "SETUP: actual rolled file holds the original UI notice")
	ageFileTo(t, rolled, time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC))
	removed, err := f.store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed, "an unadmitted notice is not pinned")
	_, err = os.Stat(rolled)
	require.ErrorIs(t, err, fs.ErrNotExist, "the exact dated notice partition was physically removed")
	meta, err := f.store.GetMeta(f.sid)
	require.NoError(t, err)
	require.Equal(t, SessionTypeChat, meta.Type)
	state := bNoticeAgeSessionState(t, f)
	bNoticeAgeSessionSameControl(t, f, state, false)
	owed, err := f.store.PendingRetentionNotices(ctx, f.sid)
	require.NoError(t, err)
	require.Len(t, owed, 1)
	require.Equal(t, f.notice.ReceiptID, owed[0].ReceiptID)
	require.Equal(t, f.notice.OutcomeID, owed[0].OutcomeID)
	require.Equal(t, f.notice.Body, owed[0].Body)
	require.False(t, owed[0].ChatPresent, "retired UI source is not reannounced as present")
	bNoticeAgeSessionNoUI(t, f)
	remembered, placed, err := f.store.SavedChatInput(f.sid, f.notice.Entry.ID)
	require.NoError(t, err)
	require.Equal(t, f.source, remembered, "positive receipt returns exact retired-but-unadmitted Source")
	require.False(t, placed)
	// A nearby address with the same known ID is NOT that positive proof.
	wrong := f.source
	wrong.ByteOffset++
	before, err := f.archive.OrdinalCount()
	require.NoError(t, err)
	_, _, err = f.store.PlaceSavedInput(ctx, f.sid, wrong)
	require.Error(t, err, "arbitrary missing source cannot enter the notice fallback")
	require.False(t, errors.Is(err, ErrAlreadyConsumed))
	require.False(t, errors.Is(err, ErrSavedInputExpired))
	after, err := f.archive.OrdinalCount()
	require.NoError(t, err)
	require.Equal(t, before, after, "wrong address publishes no synthetic model context")
	slot, _, err := f.store.PlaceSavedInput(ctx, f.sid, remembered)
	require.NoError(t, err, "public placement reaches the positive-receipt model-only fallback")
	require.Equal(t, "retention-model:"+f.notice.ReceiptID, slot.Addr.EntryID)
	require.Equal(t, "user", slot.Message.Role)
	require.Equal(t, f.notice.Body, slot.Message.Content)
	bNoticeAgeSessionModelOnly(t, f, slot)
	_, _, err = f.store.PlaceSavedInput(ctx, f.sid, remembered)
	require.ErrorIs(t, err, ErrAlreadyConsumed)
	bNoticeAgeSessionSameControl(t, f, bNoticeAgeSessionState(t, f), true)
	owed, err = f.store.PendingRetentionNotices(ctx, f.sid)
	require.NoError(t, err)
	require.Empty(t, owed)
	bNoticeAgeSessionNoUI(t, f)
	lifecycleAssertFreshHelperReport(t, f.store, f.sid, "b-notice-age-model-only")
	bNoticeAgeSessionModelOnly(t, f, slot)
}

func bNoticeAgeSessionSeed(t *testing.T) *bNoticeAgeSessionFixture {
	t.Helper()
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	archive, err := store.backend.store(meta.ID)
	require.NoError(t, err)
	f := &bNoticeAgeSessionFixture{store: store, archive: archive, sid: meta.ID,
		inputID: "saved-input-b-notice-age-original", body: "PRIVATE_B_NOTICE_AGE_ORIGINAL_REPORT"}
	atDay(archive, "2026-04-01")
	_, err = store.SaveChatInput(f.sid, f.inputID, "mia", f.body)
	require.NoError(t, err)
	atDay(archive, "2026-04-10")
	require.NoError(t, store.AppendTranscriptStrict(f.sid, TranscriptEntry{
		ID: "b-notice-age-day10-keepalive", Type: EntryTypeSystem, Role: "system",
		Content: "day-10 chat-only keepalive", AgentID: "mia", Timestamp: archive.now(), ViewMembership: ViewMembershipChat,
	}))
	oldSource := filepath.Join(archive.dir(), "2026-04-01.jsonl")
	raw, err := os.ReadFile(oldSource)
	require.NoError(t, err)
	require.Contains(t, string(raw), f.body)
	ageFileTo(t, oldSource, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	_, err = os.Stat(oldSource)
	require.ErrorIs(t, err, fs.ErrNotExist)
	notices, err := store.PendingRetentionNotices(context.Background(), f.sid)
	require.NoError(t, err)
	require.Len(t, notices, 1)
	f.notice = notices[0]
	require.True(t, f.notice.ChatPresent)
	f.source, _, err = store.SavedChatInput(f.sid, f.notice.Entry.ID)
	require.NoError(t, err)
	require.Equal(t, "2026-04-10", f.source.PartitionKey)
	state := bNoticeAgeSessionState(t, f)
	require.Contains(t, state.ExpiredReports, f.inputID)
	f.expiry = state.ExpiredReports[f.inputID]
	f.control = state.Notices[f.notice.ReceiptID]
	require.Equal(t, f.expiry.ReceiptID, f.notice.ReceiptID)
	require.Equal(t, "retention-expiry:"+f.expiry.ReceiptID, f.notice.OutcomeID)
	require.Equal(t, "saved-input-retention-expiry:"+f.expiry.ReceiptID, f.notice.Entry.ID)
	require.NotNil(t, f.control.Source)
	require.Equal(t, f.source, *f.control.Source)
	require.True(t, f.control.ChatSaved)
	require.False(t, f.control.ModelAdmitted, "SETUP: notice has never reached a model fence")
	require.Contains(t, f.notice.Body, f.inputID)
	require.Contains(t, f.notice.Body, f.sid)
	require.Contains(t, f.notice.Body, f.expiry.ExpiredAt.UTC().Format(time.RFC3339))
	require.Contains(t, strings.ToLower(f.notice.Body), "expired undelivered")
	require.Contains(t, f.notice.Body, "The original report will not arrive.")
	require.NotContains(t, f.notice.Body, f.body)
	return f
}

func bNoticeAgeSessionState(t *testing.T, f *bNoticeAgeSessionFixture) savedInputLifecycle {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.archive.dir(), "saved_input_lifecycle.json"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), f.body, "control evidence is not a second report archive")
	var state savedInputLifecycle
	require.NoError(t, json.Unmarshal(raw, &state))
	require.Equal(t, 1, state.Version)
	require.Equal(t, f.sid, state.SessionID)
	return state
}

func bNoticeAgeSessionSameControl(t *testing.T, f *bNoticeAgeSessionFixture, state savedInputLifecycle, admitted bool) {
	t.Helper()
	require.Len(t, state.ExpiredReports, 1, "no recursive expiry receipt for the expiry notice")
	require.Equal(t, f.expiry, state.ExpiredReports[f.inputID])
	require.NotContains(t, state.ExpiredReports, f.notice.Entry.ID)
	require.Len(t, state.Notices, 1)
	control := state.Notices[f.notice.ReceiptID]
	require.Equal(t, f.control.ReceiptID, control.ReceiptID)
	require.Equal(t, f.control.OutcomeID, control.OutcomeID)
	require.Equal(t, f.control.CreatedAt, control.CreatedAt)
	require.Equal(t, f.control.CopyVersion, control.CopyVersion)
	require.Equal(t, f.control.Source, control.Source)
	require.True(t, control.ChatSaved, "old chat-save fact survives without another UI append")
	require.Equal(t, admitted, control.ModelAdmitted)
}

func bNoticeAgeSessionRecords(t *testing.T, f *bNoticeAgeSessionFixture) []ArchiveRecord {
	t.Helper()
	files, err := os.ReadDir(f.archive.dir())
	require.NoError(t, err)
	var records []ArchiveRecord
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".jsonl") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(f.archive.dir(), file.Name()))
		require.NoError(t, err)
		decoder := json.NewDecoder(bytes.NewReader(raw))
		for {
			var record ArchiveRecord
			err := decoder.Decode(&record)
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err, "strict raw evidence scan must not hide malformed rows")
			require.NotEmpty(t, record.ID)
			records = append(records, record)
		}
	}
	return records
}

func bNoticeAgeSessionNoUI(t *testing.T, f *bNoticeAgeSessionFixture) {
	t.Helper()
	keepalive := 0
	for _, record := range bNoticeAgeSessionRecords(t, f) {
		require.NotEqual(t, f.notice.Entry.ID, record.ID, "expired UI source was not restored")
		require.NotEqual(t, f.inputID, record.ID)
		require.NotContains(t, record.Content, f.body)
		if record.ViewMembership == ViewMembershipChat || record.ViewMembership == ViewMembershipBoth {
			require.NotEqual(t, f.notice.Body, record.Content, "no second UI copy under a new ID")
		}
		if record.ID == "b-notice-age-day20-keepalive" {
			keepalive++
		}
	}
	require.Equal(t, 1, keepalive, "positive scan control and D2-independent chat survival")
}

func bNoticeAgeSessionModelOnly(t *testing.T, f *bNoticeAgeSessionFixture, slot ModelSlot) {
	t.Helper()
	record, err := f.archive.ReadAt(slot.Addr)
	require.NoError(t, err)
	require.Equal(t, ViewMembershipModel, record.ViewMembership)
	// A direct fallback slot may have no Source/model_ref. Do not require one.
	view, err := f.store.WindowView(context.Background(), f.sid)
	require.NoError(t, err)
	messages, _ := view.History()
	count := 0
	for _, message := range messages {
		require.NotContains(t, message.Content, f.body)
		if message.Content == f.notice.Body {
			require.Equal(t, "user", message.Role)
			count++
		}
	}
	require.Equal(t, 1, count)
	modelRows := 0
	for _, record := range bNoticeAgeSessionRecords(t, f) {
		if record.ID == "retention-model:"+f.notice.ReceiptID {
			modelRows++
			require.Equal(t, ViewMembershipModel, record.ViewMembership)
		}
	}
	require.Equal(t, 1, modelRows, "one deterministic durable fallback, not provider exactly-once")
}
