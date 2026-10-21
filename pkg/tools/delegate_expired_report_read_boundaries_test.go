package tools

// Founder B / lifecycle-design-b.md section 4: every authorized reader checks
// the addressed original's disposition BEFORE exposing retained inbox/outbox
// bytes. WRITTEN, NOT EXECUTED. The callbacks below adapt real session-store
// APIs; they do not fabricate expired/consumed outcomes or mock tool readers.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/require"
)

func TestDelegateExpiredReportReadBoundaries(t *testing.T) {
	for _, action := range []string{"status", "result", "inbox"} {
		t.Run("expired_direct_"+action, func(t *testing.T) {
			bExpiryRuntimeToolExpired(t, action, false)
		})
		t.Run("expired_authorized_ancestor_"+action, func(t *testing.T) {
			bExpiryRuntimeToolExpired(t, action, true)
		})
		t.Run("healthy_ancestor_lookup_is_non_consuming_"+action, func(t *testing.T) {
			bExpiryRuntimeToolHealthyAncestor(t, action)
		})
		t.Run("expiry_racing_final_read_scrubs_original_"+action, func(t *testing.T) {
			bExpiryRuntimeToolReadRace(t, action)
		})
		t.Run("unexplained_missing_source_is_not_expiry_"+action, func(t *testing.T) {
			bExpiryRuntimeToolUnknownMissing(t, action)
		})
	}
	t.Run("result_checks_retained_outbox_without_inbox", bExpiryRuntimeToolOutbox)
	t.Run("mixed_inbox_does_not_consume_healthy_sibling", bExpiryRuntimeToolMixedInbox)
	for _, kind := range []string{"question", "blocker"} {
		for _, action := range []string{"status", "inbox"} {
			t.Run("expired_nonfinal_"+kind+"_"+action, func(t *testing.T) {
				bExpiryRuntimeToolNonfinal(t, kind, action)
			})
		}
	}
}

type bExpiryRuntimeToolLookup struct {
	parent  string
	message string
}

type bExpiryRuntimeToolFixture struct {
	tool      *DelegateTool
	store     *session.UnifiedStore
	life      *session.LifecycleStore
	inbox     *session.MessageInboxStore
	grand     string
	parent    string
	child     string
	messageID string
	body      string
	lookups   []bExpiryRuntimeToolLookup
	reads     []bExpiryRuntimeToolLookup
	expiry    *session.SavedInputExpiry
}

func bExpiryRuntimeNewToolFixture(t *testing.T, appendInbox bool) *bExpiryRuntimeToolFixture {
	t.Helper()
	home := t.TempDir()
	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	f := &bExpiryRuntimeToolFixture{store: store,
		life:  session.NewLifecycleStore(filepath.Join(home, "session_lifecycle")),
		inbox: session.NewMessageInboxStore(filepath.Join(home, "session_messages")),
		child: "b-runtime-tool-child", body: "PRIVATE_B_TOOL_REPORT_NEVER_SERIALIZE_AFTER_EXPIRY"}
	grand, err := store.NewSession(session.SessionTypeChat, "webchat", "grand-agent")
	require.NoError(t, err)
	f.grand = grand.ID
	f.parent = "b-runtime-tool-parent"
	_, err = store.CreateSessionWithID(f.parent, f.grand, session.SessionTypeDelegate, "webchat", "parent-agent")
	require.NoError(t, err)
	_, err = store.CreateSessionWithID(f.child, f.parent, session.SessionTypeDelegate, "", "worker")
	require.NoError(t, err)
	for _, rec := range []*session.LifecycleRecord{
		{SessionID: f.grand, Generation: 1, State: session.LifecycleCompleted, AgentID: "grand-agent",
			OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-b-runtime", Origin: &session.Origin{Kind: session.OriginKindChat}},
		{SessionID: f.parent, Generation: 1, State: session.LifecycleRunning, AgentID: "parent-agent",
			OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: f.grand, WorkspaceID: "ws-b-runtime",
			SteeredBy: &session.SteeredBy{SteeringSessionID: f.grand, RootSessionID: f.grand}},
	} {
		require.NoError(t, f.life.Persist(rec))
	}
	f.messageID = f.child + ":1:final"
	msg := bExpiryRuntimeToolHandback(t, f.child, f.messageID, f.body)
	payload, err := json.Marshal(msg)
	require.NoError(t, err)
	digest := sha256.Sum256(payload)
	require.NoError(t, f.life.Persist(&session.LifecycleRecord{
		SessionID: f.child, Generation: 1, State: session.LifecycleCompleted, AgentID: "worker",
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: f.parent, WorkspaceID: "ws-b-runtime",
		SteeredBy: &session.SteeredBy{SteeringSessionID: f.parent, RootSessionID: f.grand},
		FinalDelivery: &session.FinalDeliveryCommit{Generation: 1, CommitID: "b-runtime-producing-run",
			MessageID: f.messageID, Outcome: string(steer.OutcomeFinalAnswer), ParentSessionID: f.parent,
			PayloadHash: hex.EncodeToString(digest[:]), Payload: payload},
	}))
	if appendInbox {
		_, err = f.inbox.Append(f.parent, msg)
		require.NoError(t, err)
	}
	_, err = store.SaveChatInput(f.parent, "saved-input-"+f.messageID, "parent-agent", f.body)
	require.NoError(t, err)
	f.tool = NewDelegateTool("test-model", 0, 0)
	f.tool.SetSessionMessagingEnabled(func() bool { return true })
	f.tool.SetLifecycleStore(f.life)
	f.tool.SetMessageInbox(f.inbox)
	f.tool.SetUnifiedStore(f.store)
	f.tool.SetFinalDispositionHook(func(parent, message string) error {
		f.lookups = append(f.lookups, bExpiryRuntimeToolLookup{parent, message})
		addr, placed, err := f.store.SavedChatInput(parent, "saved-input-"+message)
		if err != nil || placed || addr.EntryID == "" {
			return err
		}
		archive, err := session.NewArchiveDayStore(f.store.BaseDir(), parent)
		if err != nil {
			return err
		}
		_, err = archive.ReadAt(addr)
		return err // Real exact-address validity check; NON-CONSUMING for every reader.
	})
	f.tool.SetFinalReadHook(func(parent, _ string, message string) (bool, error) {
		f.reads = append(f.reads, bExpiryRuntimeToolLookup{parent, message})
		already, err := f.store.RecordSavedInputPollConsumed(parent, "saved-input-"+message)
		if err != nil {
			return false, err
		}
		return already, f.inbox.Ack(parent, []string{message})
	})
	return f
}

func bExpiryRuntimeToolHandback(t *testing.T, child, id, body string) generated.SessionMessage {
	t.Helper()
	var msg generated.SessionMessage
	require.NoError(t, msg.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId: id, SessionId: child, CreatedAt: time.Now().UTC(), Depth: 1, SenderIdentity: "worker",
		Direction: generated.SessionMessageHandbackDirectionChildToParent,
		Mode:      generated.SessionMessageHandbackModeFinal, ResultSoFar: body,
		Artifacts: []string{}, OpenQuestions: []string{},
	}))
	return msg
}

func bExpiryRuntimeToolExpire(t *testing.T, f *bExpiryRuntimeToolFixture) {
	t.Helper()
	path := filepath.Join(f.store.BaseDir(), f.parent, "transcript.jsonl")
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(before), f.body, "SETUP: retention targets the actual saved source")
	old := time.Now().Add(-8 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))
	removed, err := f.store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	_, _, err = f.store.SavedChatInput(f.parent, "saved-input-"+f.messageID)
	require.ErrorIs(t, err, session.ErrSavedInputExpired)
	require.ErrorAs(t, err, &f.expiry)
	require.Equal(t, f.parent, f.expiry.SessionID)
	require.Equal(t, "saved-input-"+f.messageID, f.expiry.InputID)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(after), f.body)
}

func bExpiryRuntimeToolExecute(f *bExpiryRuntimeToolFixture, action string, ancestor bool) *ToolResult {
	owner, agentID := f.parent, "parent-agent"
	if ancestor {
		owner, agentID = f.grand, "grand-agent"
	}
	ctx := WithAgentID(WithTranscriptSessionID(context.Background(), owner), agentID)
	return f.tool.Execute(ctx, map[string]any{"action": action, "session_id": f.child})
}

func bExpiryRuntimeToolAssertExpired(t *testing.T, f *bExpiryRuntimeToolFixture, result *ToolResult) {
	t.Helper()
	require.NotNil(t, result)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw), f.body, "expired source cannot leak via ForLLM, ForUser or a tool-result field")
	text := strings.ToLower(result.ForLLM)
	require.Contains(t, text, "expired")
	require.Contains(t, text, "undelivered")
	require.Contains(t, result.ForLLM, f.messageID, "operational expiry identifies the exact lost report")
	require.NotContains(t, text, "final already delivered")
	require.NotContains(t, text, "could not record that this result was received")
	require.NotContains(t, text, "helper failed")
	child, err := f.life.Load(f.child)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleCompleted, child.State)
	require.Empty(t, child.FailedReason, "report expiry is not helper-work failure")
	require.Contains(t, string(child.FinalDelivery.Payload), f.body,
		"instrument: reader must filter even while the original outbox still retains the body")
}

func bExpiryRuntimeToolExpired(t *testing.T, action string, ancestor bool) {
	f := bExpiryRuntimeNewToolFixture(t, true)
	bExpiryRuntimeToolExpire(t, f)
	entries, err := f.inbox.Entries(f.parent)
	require.NoError(t, err)
	raw, err := json.Marshal(entries)
	require.NoError(t, err)
	require.Contains(t, string(raw), f.body, "SETUP: acknowledgement alone cannot hide retained original bytes")
	result := bExpiryRuntimeToolExecute(f, action, ancestor)
	bExpiryRuntimeToolAssertExpired(t, f, result)
	require.Contains(t, f.lookups, bExpiryRuntimeToolLookup{f.parent, f.messageID}, "query the actual recipient, not the ancestor's own store key")
	if ancestor {
		require.Empty(t, f.reads, "ancestor validity lookup must not consume the direct parent's message")
	}
}

func bExpiryRuntimeToolOutbox(t *testing.T) {
	f := bExpiryRuntimeNewToolFixture(t, false)
	bExpiryRuntimeToolExpire(t, f)
	entries, err := f.inbox.Entries(f.parent)
	require.NoError(t, err)
	require.Empty(t, entries, "SETUP: only the real committed outbox can supply result's body")
	bExpiryRuntimeToolAssertExpired(t, f, bExpiryRuntimeToolExecute(f, "result", false))
	require.Contains(t, f.lookups, bExpiryRuntimeToolLookup{f.parent, f.messageID})
}

func bExpiryRuntimeToolHealthyAncestor(t *testing.T, action string) {
	f := bExpiryRuntimeNewToolFixture(t, true)
	result := bExpiryRuntimeToolExecute(f, action, true)
	require.False(t, result.IsError, result.ForLLM)
	require.Contains(t, result.ForLLM, f.body, "positive control: authorized ancestor reaches the retained real message")
	require.Contains(t, f.lookups, bExpiryRuntimeToolLookup{f.parent, f.messageID})
	require.Empty(t, f.reads)
	consumed, err := f.store.SavedInputConsumed(f.parent, "saved-input-"+f.messageID)
	require.NoError(t, err)
	require.False(t, consumed)
	messages, _, _, err := f.inbox.Drain(f.parent, f.child, "", 0)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	pending, err := f.store.PendingChatInputs(context.Background(), f.parent)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "saved-input-"+f.messageID, pending[0].ID)
}

func bExpiryRuntimeToolMixedInbox(t *testing.T) {
	f := bExpiryRuntimeNewToolFixture(t, true)
	bExpiryRuntimeToolExpire(t, f)
	const healthyBody = "fresh report still available to its parent"
	healthyID := f.child + ":2:final"
	_, err := f.inbox.Append(f.parent, bExpiryRuntimeToolHandback(t, f.child, healthyID, healthyBody))
	require.NoError(t, err)
	_, err = f.store.SaveChatInput(f.parent, "saved-input-"+healthyID, "parent-agent", healthyBody)
	require.NoError(t, err)
	result := bExpiryRuntimeToolExecute(f, "inbox", false)
	bExpiryRuntimeToolAssertExpired(t, f, result)
	decoder := json.NewDecoder(strings.NewReader(result.ForLLM))
	var envelope generated.DelegateInboxResponse
	require.NoError(t, decoder.Decode(&envelope), "existing generated inbox envelope precedes the tool-text expiry note")
	require.Len(t, envelope.Messages, 1)
	healthy, err := envelope.Messages[0].AsSessionMessageHandback()
	require.NoError(t, err)
	require.Equal(t, healthyID, healthy.MessageId)
	require.Equal(t, healthyBody, healthy.ResultSoFar)
	require.NotContains(t, f.reads, bExpiryRuntimeToolLookup{f.parent, healthyID}, "first mixed page must not consume non-expired siblings")
	consumed, err := f.store.SavedInputConsumed(f.parent, "saved-input-"+healthyID)
	require.NoError(t, err)
	require.False(t, consumed)
	messages, _, _, err := f.inbox.Drain(f.parent, f.child, "", 0)
	require.NoError(t, err)
	found := false
	for _, message := range messages {
		got, err := message.AsSessionMessageHandback()
		require.NoError(t, err)
		if got.MessageId == healthyID {
			found = true
		}
	}
	require.True(t, found, "healthy sibling remains available in the actual inbox")
}

func bExpiryRuntimeToolReadRace(t *testing.T, action string) {
	f := bExpiryRuntimeNewToolFixture(t, true)
	calls := 0
	f.tool.SetFinalReadHook(func(parent, _ string, message string) (bool, error) {
		calls++
		require.Equal(t, f.parent, parent)
		require.Equal(t, f.messageID, message)
		bExpiryRuntimeToolExpire(t, f) // Actual retention between validity query and final-read claim.
		return f.store.RecordSavedInputPollConsumed(parent, "saved-input-"+message)
	})
	result := bExpiryRuntimeToolExecute(f, action, false)
	require.Equal(t, 1, calls, "instrument: real final-read race, not an always-expired lookup stub")
	bExpiryRuntimeToolAssertExpired(t, f, result)
	entries, err := f.store.ReadTranscript(f.parent)
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotEqual(t, "consumed-"+f.messageID, entry.ID)
	}
}

func bExpiryRuntimeToolUnknownMissing(t *testing.T, action string) {
	f := bExpiryRuntimeNewToolFixture(t, true)
	require.NoError(t, os.Remove(filepath.Join(f.store.BaseDir(), f.parent, "transcript.jsonl")),
		"SETUP: generic missing source, no committed retention loss")
	result := bExpiryRuntimeToolExecute(f, action, false)
	require.True(t, result.IsError, "unknown missing source is an integrity error, not empty/expired success")
	require.NotContains(t, result.ForLLM, f.body)
	require.NotContains(t, strings.ToLower(result.ForLLM), "expired undelivered")
	require.Contains(t, f.lookups, bExpiryRuntimeToolLookup{f.parent, f.messageID})
	require.Empty(t, f.reads)
}

func bExpiryRuntimeToolNonfinal(t *testing.T, kind, action string) {
	f := bExpiryRuntimeNewToolFixture(t, false)
	f.messageID = f.child + ":nonfinal-" + kind
	f.body = "PRIVATE_B_NONFINAL_" + strings.ToUpper(kind) + "_BODY"
	var message generated.SessionMessage
	if kind == "question" {
		require.NoError(t, message.FromSessionMessageQuestion(generated.SessionMessageQuestion{
			MessageId: f.messageID, SessionId: f.child, CreatedAt: time.Now().UTC(), Depth: 1,
			SenderIdentity: "worker", Direction: generated.SessionMessageQuestionDirectionChildToParent,
			CorrelationId: "b-runtime-unrelated-question", Text: f.body,
		}))
	} else {
		require.NoError(t, message.FromSessionMessageBlocker(generated.SessionMessageBlocker{
			MessageId: f.messageID, SessionId: f.child, CreatedAt: time.Now().UTC(), Depth: 1,
			SenderIdentity: "worker", Direction: generated.SessionMessageBlockerDirectionChildToParent,
			Severity: generated.SessionMessageBlockerSeverityHigh, Text: f.body,
		}))
	}
	_, err := f.inbox.Append(f.parent, message)
	require.NoError(t, err)
	_, err = f.store.SaveChatInput(f.parent, "saved-input-"+f.messageID, "parent-agent", f.body)
	require.NoError(t, err)
	bExpiryRuntimeToolExpire(t, f)
	result := bExpiryRuntimeToolExecute(f, action, false)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw), f.body)
	require.Contains(t, strings.ToLower(result.ForLLM), "expired")
	require.Contains(t, strings.ToLower(result.ForLLM), "undelivered")
	require.Contains(t, result.ForLLM, f.messageID)
	require.Contains(t, f.lookups, bExpiryRuntimeToolLookup{f.parent, f.messageID}, "non-final messages also require terminal filtering")
	require.Empty(t, f.reads, "question/blocker filtering is not a consuming final-read")
}
