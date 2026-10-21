package agent

// Founder B, lifecycle-design-b.md section 5 / docs audit D2: after the last
// ordinary-chat notice payload expires, identity and exact owed controls remain.
// A real ordinary turn must regenerate fixed MODEL-ONLY context, not new UI or
// the original helper report. WRITTEN, NOT EXECUTED. No missing identity is
// repaired by this fixture; it is an acceptance failure before continuation.

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

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

type bNoticeAgeAgentFixture struct {
	al       *AgentLoop
	store    *session.UnifiedStore
	provider *scriptChatProvider
	sid      string
	workID   string
	inputID  string
	body     string
	expiry   *session.SavedInputExpiry
	notice   session.RetentionNotice
	source   session.ArchiveAddress
	work     *session.LifecycleRecord
	parent   *session.LifecycleRecord
	receipt  json.RawMessage
}

type bNoticeAgeAgentControlState struct {
	expired map[string]json.RawMessage
	notices map[string]json.RawMessage
}

func TestRetentionNoticeAgedParentContinuation(t *testing.T) {
	f := bNoticeAgeAgentSeed(t)
	path := filepath.Join(f.store.BaseDir(), f.sid, "transcript.jsonl")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), f.notice.Body, "SETUP: age the actual last UI-notice file")
	require.NotContains(t, string(raw), f.body, "original report was already physically expired")
	old := time.Now().Add(-8 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))
	removed, err := f.store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed, "an owed model notice does not pin its current-file payload")
	// This is D2, not a setup fallback: inspect disk BEFORE any public lookup
	// or new turn could accidentally recreate a removed recipient.
	_, err = os.Stat(filepath.Join(f.store.BaseDir(), f.sid))
	require.NoError(t, err, "ordinary parent identity/control bundle must outlive owed notice payloads")
	_, err = os.Stat(filepath.Join(f.store.BaseDir(), f.sid, "meta.json"))
	require.NoError(t, err, "real recipient identity was retained, not only a stale in-memory cache")
	bNoticeAgeAgentNoPayload(t, f)
	meta, err := f.store.GetMeta(f.sid)
	require.NoError(t, err)
	require.Equal(t, f.sid, meta.ID)
	require.Equal(t, session.SessionTypeChat, meta.Type)
	require.Equal(t, "mia", meta.AgentID)
	bNoticeAgeAgentWorkUnchanged(t, f, true)
	bNoticeAgeAgentAssertControl(t, f, false)
	owed, err := f.store.PendingRetentionNotices(context.Background(), f.sid)
	require.NoError(t, err)
	require.Len(t, owed, 1)
	require.Equal(t, f.notice.ReceiptID, owed[0].ReceiptID)
	require.Equal(t, f.notice.OutcomeID, owed[0].OutcomeID)
	require.Equal(t, f.notice.Body, owed[0].Body)
	require.False(t, owed[0].ChatPresent)
	require.Equal(t, 1, f.provider.callCount(), "expiry/notice cleanup did not run a helper-failure or model turn")
	_, _, err = f.al.processMessageAsPrimaryForTest(context.Background(),
		bExpiryRuntimeOrdinaryMessage(f.sid, "Continue this existing conversation after retention."))
	require.NoError(t, err, "the real next legitimate ordinary turn can use the retained exact controls")
	require.Equal(t, 2, f.provider.callCount(), "one initial ordinary turn, then one requested continuation")
	request, ok := f.provider.lastMessages(2)
	require.True(t, ok)
	bExpiryRuntimeAssertModelNotice(t, request, f.notice, f.body)
	bNoticeAgeAgentAssertControl(t, f, true)
	bNoticeAgeAgentWorkUnchanged(t, f, false)
	bNoticeAgeAgentModelOnly(t, f)
	_, _, err = f.store.PlaceSavedInput(context.Background(), f.sid, f.source)
	require.ErrorIs(t, err, session.ErrAlreadyConsumed)
	owed, err = f.store.PendingRetentionNotices(context.Background(), f.sid)
	require.NoError(t, err)
	require.Empty(t, owed)
	_, _, err = f.store.SavedChatInput(f.sid, f.inputID)
	require.ErrorIs(t, err, session.ErrSavedInputExpired, "original helper report remains terminal after model-only recovery")
}

func bNoticeAgeAgentSeed(t *testing.T) *bNoticeAgeAgentFixture {
	t.Helper()
	provider := &scriptChatProvider{fallback: "ordinary parent work completed"}
	al, _ := newChatTargetAskUserLoop(t, provider)
	gate1WireSessionMessaging(t, al)
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	_, _, err = al.processMessageAsPrimaryForTest(context.Background(), bExpiryRuntimeOrdinaryMessage(meta.ID, "Start this ordinary conversation."))
	require.NoError(t, err)
	require.Equal(t, 1, provider.callCount(), "SETUP: real ordinary entry created its lifecycle/execution")
	f := &bNoticeAgeAgentFixture{al: al, store: store, provider: provider, sid: meta.ID,
		workID: "b-notice-age-original-worker", body: "PRIVATE_B_AGED_PARENT_ORIGINAL_REPORT"}
	f.parent, err = al.GetSessionLifecycleStore().Load(f.sid)
	require.NoError(t, err)
	parentMeta, err := store.GetMeta(f.sid)
	require.NoError(t, err)
	_, err = store.CreateSessionWithID(f.workID, f.sid, session.SessionTypeDelegate, "", "mia")
	require.NoError(t, err)
	workspaceID := parentMeta.WorkspaceID
	require.NoError(t, store.SetMeta(f.workID, session.MetaPatch{WorkspaceID: &workspaceID}))
	require.NoError(t, al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: f.workID, Generation: 1, State: session.LifecycleCompleted, AgentID: "mia",
		WorkspaceID: workspaceID, ParentAgentID: "mia", OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: f.sid,
		Origin: &session.Origin{Kind: session.OriginKindDelegate, CallID: "b-notice-age-original-call"},
		SteeredBy: &session.SteeredBy{SteeringSessionID: f.sid, RootSessionID: f.sid,
			ReportingTarget: session.ReportingTarget{Channel: "webchat", ChatID: f.sid, SessionID: f.sid}},
	}))
	f.work, err = al.GetSessionLifecycleStore().Load(f.workID)
	require.NoError(t, err)
	outcome := f.workID + ":1:final"
	f.inputID = "saved-input-" + outcome
	f.expiry, f.notice = bExpiryRuntimeExpire(t, store, f.sid, "mia", outcome, f.body)
	f.source, _, err = store.SavedChatInput(f.sid, f.notice.Entry.ID)
	require.NoError(t, err)
	require.Equal(t, f.notice.Entry.ID, f.source.EntryID)
	state := bNoticeAgeAgentState(t, f)
	require.Len(t, state.expired, 1)
	require.Contains(t, state.expired, f.inputID)
	f.receipt = append(json.RawMessage(nil), state.expired[f.inputID]...)
	bNoticeAgeAgentAssertControl(t, f, false)
	return f
}

func bNoticeAgeAgentState(t *testing.T, f *bNoticeAgeAgentFixture) bNoticeAgeAgentControlState {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.store.BaseDir(), f.sid, "saved_input_lifecycle.json"))
	require.NoError(t, err, "owed receipt must exist on disk, not only in process memory")
	require.NotContains(t, string(raw), f.body)
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &top))
	var version int
	var sid string
	require.NoError(t, json.Unmarshal(top["version"], &version))
	require.NoError(t, json.Unmarshal(top["session_id"], &sid))
	require.Equal(t, 1, version)
	require.Equal(t, f.sid, sid)
	var state bNoticeAgeAgentControlState
	require.NoError(t, json.Unmarshal(top["expired_reports"], &state.expired))
	require.NoError(t, json.Unmarshal(top["notices"], &state.notices))
	return state
}

func bNoticeAgeAgentAssertControl(t *testing.T, f *bNoticeAgeAgentFixture, admitted bool) {
	t.Helper()
	state := bNoticeAgeAgentState(t, f)
	require.Len(t, state.expired, 1, "no recursive expiry of the operational notice")
	require.NotContains(t, state.expired, f.notice.Entry.ID)
	require.JSONEq(t, string(f.receipt), string(state.expired[f.inputID]))
	require.Len(t, state.notices, 1)
	require.Contains(t, state.notices, f.notice.ReceiptID)
	var control map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(state.notices[f.notice.ReceiptID], &control))
	var source session.ArchiveAddress
	var receiptID, outcomeID string
	var chatSaved, modelAdmitted bool
	require.NoError(t, json.Unmarshal(control["source"], &source))
	require.NoError(t, json.Unmarshal(control["receipt_id"], &receiptID))
	require.NoError(t, json.Unmarshal(control["outcome_id"], &outcomeID))
	require.NoError(t, json.Unmarshal(control["chat_saved"], &chatSaved))
	require.NoError(t, json.Unmarshal(control["model_admitted"], &modelAdmitted))
	require.Equal(t, f.source, source)
	require.Equal(t, f.expiry.ReceiptID, receiptID)
	require.Equal(t, f.notice.OutcomeID, outcomeID)
	require.True(t, chatSaved, "positive old chat-save receipt survives without reappending its UI source")
	require.Equal(t, admitted, modelAdmitted)
}

func bNoticeAgeAgentNoPayload(t *testing.T, f *bNoticeAgeAgentFixture) {
	t.Helper()
	dir := filepath.Join(f.store.BaseDir(), f.sid)
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".jsonl") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, file.Name()))
		require.NoError(t, err)
		require.Empty(t, raw, "no payload is pinned; a recreated EMPTY append target is harmless")
	}
	_, err = os.Stat(filepath.Join(dir, "transcript.jsonl"))
	if err != nil {
		require.ErrorIs(t, err, fs.ErrNotExist, "only actual absence is an allowed missing-current result")
	}
}

func bNoticeAgeAgentWorkUnchanged(t *testing.T, f *bNoticeAgeAgentFixture, includeParent bool) {
	t.Helper()
	work, err := f.al.GetSessionLifecycleStore().Load(f.workID)
	require.NoError(t, err)
	require.Equal(t, f.work, work, "loss of report/notice is not a transition or failure of the original work")
	require.Equal(t, session.LifecycleCompleted, work.State)
	require.Empty(t, work.FailedReason)
	if includeParent {
		parent, err := f.al.GetSessionLifecycleStore().Load(f.sid)
		require.NoError(t, err)
		require.Equal(t, f.parent, parent, "retention alone does not invent a parent execution or failure")
	}
}

func bNoticeAgeAgentModelOnly(t *testing.T, f *bNoticeAgeAgentFixture) {
	t.Helper()
	entries, err := f.store.ReadTranscript(f.sid)
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotEqual(t, f.notice.Entry.ID, entry.ID)
		require.NotEqual(t, f.inputID, entry.ID)
		require.NotEqual(t, f.notice.Body, entry.Content, "ordinary continuation did not recreate a UI notice")
		require.NotContains(t, entry.Content, f.body)
	}
	files, err := os.ReadDir(filepath.Join(f.store.BaseDir(), f.sid))
	require.NoError(t, err)
	modelOnly := 0
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".jsonl") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(f.store.BaseDir(), f.sid, file.Name()))
		require.NoError(t, err)
		decoder := json.NewDecoder(bytes.NewReader(raw))
		for {
			var record session.ArchiveRecord
			err := decoder.Decode(&record)
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err, "strict evidence scan cannot hide a malformed duplicate UI row")
			require.NotEmpty(t, record.ID)
			require.NotEqual(t, f.notice.Entry.ID, record.ID)
			require.NotEqual(t, f.inputID, record.ID)
			require.NotContains(t, record.Content, f.body)
			if record.ID == "retention-model:"+f.notice.ReceiptID {
				modelOnly++
				require.Equal(t, session.ViewMembershipModel, record.ViewMembership)
			}
		}
	}
	require.Equal(t, 1, modelOnly, "fixed model-only context has one deterministic durable slot; no model_ref required")
}
