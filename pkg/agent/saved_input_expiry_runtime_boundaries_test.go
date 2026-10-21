package agent

// Founder B / lifecycle-design-b.md sections 4-5. Independent runtime RED
// artifact: WRITTEN, NOT EXECUTED. Only the provider/transport edge is fake;
// report storage, age-based expiry, receivers, queues and fences are real.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

func TestSavedInputExpiryRuntimeBoundaries(t *testing.T) {
	t.Run("expired_original_wake_is_terminal_not_unreadable", bExpiryRuntimeOriginalWake)
	t.Run("final_read_returns_typed_expiry_without_consumption", bExpiryRuntimeFinalRead)
	t.Run("stale_idle_wake_acks_only_expired_original", bExpiryRuntimeStaleIdle)
	t.Run("stale_steering_skips_original_and_releases_only_its_claim", bExpiryRuntimeStaleSteering)
	t.Run("ordinary_next_turn_recovers_prepared_notice_once", bExpiryRuntimeOrdinaryRecovery)
	t.Run("dispatch_idle_notice_reaches_real_receiver_once", bExpiryRuntimeIdleNotice)
	t.Run("dispatch_live_notice_joins_real_turn", bExpiryRuntimeLiveNotice)
	t.Run("stop_holds_notice_without_reviving_parent", bExpiryRuntimeStopHold)
	t.Run("unanswered_real_question_is_preserved", bExpiryRuntimeQuestionHold)
	t.Run("sink_error_is_returned_and_retryable", bExpiryRuntimeSinkFailure)
	t.Run("cancelled_publication_is_retryable", bExpiryRuntimePublicationCancellation)
	t.Run("closed_bus_does_not_acknowledge_model_delivery", bExpiryRuntimeClosedBus)
}

type bExpiryRuntimeAgentFixture struct {
	al       *AgentLoop
	life     *session.LifecycleStore
	inbox    *session.MessageInboxStore
	store    *session.UnifiedStore
	parent   string
	child    string
	outcome  string
	body     string
	expiry   *session.SavedInputExpiry
	notice   session.RetentionNotice
	provider *bExpiryRuntimeImmediateProvider
}

// This external edge returns immediately: a forbidden direct provider call
// inside the dispatcher must fail an assertion, not deadlock on a held fake.
type bExpiryRuntimeImmediateProvider struct {
	mu       sync.Mutex
	requests [][]providers.Message
}

func (p *bExpiryRuntimeImmediateProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	p.mu.Unlock()
	return &providers.LLMResponse{Content: "parent continuation completed"}, nil
}

func (*bExpiryRuntimeImmediateProvider) GetDefaultModel() string { return "test-model" }

func (p *bExpiryRuntimeImmediateProvider) bExpiryRuntimeRequests() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]providers.Message(nil), p.requests...)
}

func bExpiryRuntimeNewAgentFixture(t *testing.T) *bExpiryRuntimeAgentFixture {
	t.Helper()
	f := bExpiryRuntimeNewUnexpiredAgentFixture(t)
	f.expiry, f.notice = bExpiryRuntimeExpire(t, f.store, f.parent, "parent-agent", f.outcome, f.body)
	return f
}

func bExpiryRuntimeNewUnexpiredAgentFixture(t *testing.T) *bExpiryRuntimeAgentFixture {
	t.Helper()
	al, life, inbox, _ := newPWDeliverTestLoop(t)
	f := &bExpiryRuntimeAgentFixture{al: al, life: life, inbox: inbox, store: al.GetSessionStore(),
		parent: "b-expiry-runtime-parent", child: "b-expiry-runtime-child",
		outcome: "b-expiry-runtime-child:1:final", body: "PRIVATE_B_RUNTIME_ORIGINAL_BODY_NEVER_DELIVER"}
	pwSeedDeliverySessions(t, al, f.parent, f.child)
	f.provider = &bExpiryRuntimeImmediateProvider{}
	inst, ok := al.GetRegistry().GetAgent("parent-agent")
	require.True(t, ok)
	inst.Provider = f.provider
	require.NoError(t, life.Mutate(f.child, func(rec *session.LifecycleRecord) error {
		rec.State = session.LifecycleCompleted
		return nil
	}))
	bExpiryRuntimeAppendHandback(t, inbox, f.parent, f.child, f.outcome, f.body)
	_, err := f.store.SaveChatInput(f.parent, "saved-input-"+f.outcome, "parent-agent", f.body)
	require.NoError(t, err)
	return f
}

func bExpiryRuntimeAppendHandback(t *testing.T, inbox *session.MessageInboxStore, parent, child, id, body string) {
	t.Helper()
	var msg generated.SessionMessage
	require.NoError(t, msg.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId: id, SessionId: child, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Direction: generated.SessionMessageHandbackDirectionChildToParent,
		Mode:        generated.SessionMessageHandbackModeFinal,
		ResultSoFar: body, Artifacts: []string{}, OpenQuestions: []string{},
	}))
	_, err := inbox.Append(parent, msg)
	require.NoError(t, err)
}

func bExpiryRuntimeExpire(t *testing.T, store *session.UnifiedStore, sid, agentID, outcome, body string) (*session.SavedInputExpiry, session.RetentionNotice) {
	t.Helper()
	id := "saved-input-" + outcome
	addr, err := store.SaveChatInput(sid, id, agentID, body)
	require.NoError(t, err)
	archive, err := session.NewArchiveDayStore(store.BaseDir(), sid)
	require.NoError(t, err)
	rec, err := archive.ReadAt(addr)
	require.NoError(t, err)
	require.Equal(t, body, rec.Content, "SETUP: exact saved source is real")
	path := filepath.Join(store.BaseDir(), sid, "transcript.jsonl")
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(before), body, "SETUP: age the actual report file")
	old := time.Now().Add(-8 * 24 * time.Hour) // Strictly older than the specified seven-day policy.
	require.NoError(t, os.Chtimes(path, old, old))
	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	_, placed, err := store.SavedChatInput(sid, id)
	require.ErrorIs(t, err, session.ErrSavedInputExpired)
	require.False(t, placed)
	var expired *session.SavedInputExpiry
	require.ErrorAs(t, err, &expired)
	require.Equal(t, sid, expired.SessionID)
	require.Equal(t, id, expired.InputID)
	require.Equal(t, addr, expired.Source)
	notices, err := store.PendingRetentionNotices(context.Background(), sid)
	require.NoError(t, err)
	require.Len(t, notices, 1)
	notice := notices[0]
	require.Equal(t, sid, notice.SessionID)
	require.Equal(t, expired.ReceiptID, notice.ReceiptID)
	require.Equal(t, "retention-expiry:"+expired.ReceiptID, notice.OutcomeID)
	require.Equal(t, "saved-input-retention-expiry:"+expired.ReceiptID, notice.Entry.ID)
	require.True(t, notice.ChatPresent)
	require.Equal(t, session.EntryTypeSystem, notice.Entry.Type)
	require.Equal(t, "system", notice.Entry.Role)
	require.Equal(t, session.SystemSubtypeExpiryNotice, notice.Entry.SystemSubtype)
	require.Contains(t, notice.Body, id)
	require.Contains(t, notice.Body, sid)
	require.Contains(t, notice.Body, expired.ExpiredAt.UTC().Format(time.RFC3339))
	require.Contains(t, strings.ToLower(notice.Body), "expired undelivered")
	require.NotContains(t, notice.Body, body)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(after), body, "expired original bytes are absent from the replacement current file")
	return expired, notice
}

func bExpiryRuntimeNoRetryOrBody(t *testing.T, f *bExpiryRuntimeAgentFixture, events []Event) {
	t.Helper()
	entries, err := f.store.ReadTranscript(f.parent)
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotContains(t, entry.Content, f.body)
		require.NotEqual(t, "repair-pending-saved-input-"+f.outcome, entry.ID)
	}
	view, err := f.store.WindowView(context.Background(), f.parent)
	require.NoError(t, err)
	messages, _ := view.History()
	for _, message := range messages {
		require.NotContains(t, message.Content, f.body, "expired original never re-enters model history")
	}
	for _, event := range events {
		if event.Kind == EventKindHelperEvent {
			frame, ok := event.Payload.(generated.HelperEventFrame)
			require.True(t, ok)
			require.NotEqual(t, "repair-pending-saved-input-"+f.outcome, frame.MessageId)
			require.NotContains(t, frame.HelperEvent.Text, "could not be read yet")
			require.NotContains(t, frame.HelperEvent.Text, f.body)
		}
	}
	child, err := f.life.Load(f.child)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleCompleted, child.State, "report loss does not fail completed helper work")
}

func bExpiryRuntimeCaptureEvents(t *testing.T, al *AgentLoop) func() []Event {
	t.Helper()
	var mu sync.Mutex
	var events []Event
	al.SetEventSyncTap(func(event Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	})
	t.Cleanup(func() { al.SetEventSyncTap(nil) })
	return func() []Event {
		mu.Lock()
		defer mu.Unlock()
		return append([]Event(nil), events...)
	}
}

func bExpiryRuntimeOriginalWake(t *testing.T) {
	f := bExpiryRuntimeNewAgentFixture(t)
	events := bExpiryRuntimeCaptureEvents(t, f.al)
	result, err := f.al.WakeSession(context.Background(), f.parent, "finished", f.outcome, f.body)
	require.NoError(t, err, "authorized report loss is a terminal disposition, not a retryable read failure")
	require.Equal(t, "expired_undelivered", result.Disposition)
	require.False(t, result.RowPending)
	require.False(t, f.al.wakePublicationInFlight(f.parent, f.outcome))
	bExpiryRuntimeNoRetryOrBody(t, f, events())
	view, err := f.store.WindowView(context.Background(), f.parent)
	require.NoError(t, err)
	messages, _ := view.History()
	for _, message := range messages {
		require.NotContains(t, message.Content, f.body)
	}
}

func bExpiryRuntimeFinalRead(t *testing.T) {
	f := bExpiryRuntimeNewAgentFixture(t)
	already, err := f.al.recordPolledFinalConsumed(f.parent, "parent-agent", f.outcome)
	require.ErrorIs(t, err, session.ErrSavedInputExpired)
	var expired *session.SavedInputExpiry
	require.ErrorAs(t, err, &expired)
	require.Equal(t, f.expiry, expired)
	require.False(t, already, "expired undelivered is not a consumed claim")
	entries, err := f.store.ReadTranscript(f.parent)
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotEqual(t, "consumed-"+f.outcome, entry.ID)
	}
}

func bExpiryRuntimeHealthySibling(t *testing.T, f *bExpiryRuntimeAgentFixture) string {
	t.Helper()
	id := f.child + ":2:final"
	bExpiryRuntimeAppendHandback(t, f.inbox, f.parent, f.child, id, "fresh unexpired sibling")
	_, err := f.store.SaveChatInput(f.parent, "saved-input-"+id, "parent-agent", "fresh unexpired sibling")
	require.NoError(t, err)
	return id
}

func bExpiryRuntimeAssertOnlySibling(t *testing.T, f *bExpiryRuntimeAgentFixture, sibling string) {
	t.Helper()
	messages, _, _, err := f.inbox.Drain(f.parent, f.child, "", 0)
	require.NoError(t, err)
	require.Len(t, messages, 1, "only the expired original was acknowledged")
	var handback generated.SessionMessageHandback
	handback, err = messages[0].AsSessionMessageHandback()
	require.NoError(t, err)
	require.Equal(t, sibling, handback.MessageId)
	consumed, err := f.store.SavedInputConsumed(f.parent, "saved-input-"+sibling)
	require.NoError(t, err)
	require.False(t, consumed)
}

func bExpiryRuntimeStaleIdle(t *testing.T) {
	f := bExpiryRuntimeNewUnexpiredAgentFixture(t)
	published, err := f.al.WakeSession(context.Background(), f.parent, "finished", f.outcome, f.body)
	require.NoError(t, err)
	require.Equal(t, "published_idle_wake", published.Disposition)
	var wake bus.InboundMessage
	select {
	case wake = <-f.al.bus.InboundChan():
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: original real wake was not published before expiry")
	}
	require.True(t, f.al.wakePublicationInFlight(f.parent, f.outcome))
	f.expiry, f.notice = bExpiryRuntimeExpire(t, f.store, f.parent, "parent-agent", f.outcome, f.body)
	sibling := bExpiryRuntimeHealthySibling(t, f)
	events := bExpiryRuntimeCaptureEvents(t, f.al)
	_, err = f.al.processSteeredSystemWake(context.Background(), wake)
	require.NoError(t, err)
	require.False(t, f.al.wakePublicationInFlight(f.parent, f.outcome))
	bExpiryRuntimeAssertOnlySibling(t, f, sibling)
	bExpiryRuntimeNoRetryOrBody(t, f, events())
	require.Empty(t, f.provider.bExpiryRuntimeRequests(), "expired wake never starts a provider request")
}

func bExpiryRuntimeStaleSteering(t *testing.T) {
	f := bExpiryRuntimeNewUnexpiredAgentFixture(t)
	f.al.asyncNotifier.sessionWakes.Store(f.parent+"\x00"+f.outcome, true)
	require.NoError(t, f.al.EnqueueSteeringWake(f.parent, "parent-agent", f.parent, f.outcome,
		providers.Message{Role: "user", Content: f.body}))
	f.expiry, f.notice = bExpiryRuntimeExpire(t, f.store, f.parent, "parent-agent", f.outcome, f.body)
	sibling := bExpiryRuntimeHealthySibling(t, f)
	f.al.asyncNotifier.sessionWakes.Store(f.parent+"\x00"+sibling, true)
	events := bExpiryRuntimeCaptureEvents(t, f.al)
	actualScope, items := f.al.steering.dequeueItemsScope(f.parent)
	require.Len(t, items, 1, "SETUP: consume the real queued wake, not a substitute item")
	messages, correlations, consumed, err := f.al.consumeDequeuedSteeringResult(actualScope, items)
	require.NoError(t, err)
	require.Empty(t, messages)
	require.Empty(t, correlations)
	require.Empty(t, consumed)
	_, restored := f.al.steering.dequeueItemsScope(f.parent)
	require.Empty(t, restored, "terminal expiry is not a temporary requeue")
	require.False(t, f.al.wakePublicationInFlight(f.parent, f.outcome))
	require.True(t, f.al.wakePublicationInFlight(f.parent, sibling), "unrelated publication claim remains owned")
	bExpiryRuntimeAssertOnlySibling(t, f, sibling)
	bExpiryRuntimeNoRetryOrBody(t, f, events())
}

func bExpiryRuntimeAssertModelNotice(t *testing.T, messages []providers.Message, notice session.RetentionNotice, original string) {
	t.Helper()
	count := 0
	for _, message := range messages {
		require.NotContains(t, message.Content, original)
		if message.Content == notice.Body {
			require.Equal(t, "user", message.Role)
			count++
		}
	}
	require.Equal(t, 1, count, "real model boundary carries one operational notice, not just chat history")
}

func bExpiryRuntimeOrdinaryMessage(sid, text string) bus.InboundMessage {
	return bus.InboundMessage{Channel: "webchat", ChatID: sid, SessionID: sid, Content: text,
		UserInitiated: true, Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "daniel",
		Metadata: map[string]string{"agent_id": "mia"}}
}

func bExpiryRuntimeOrdinaryRecovery(t *testing.T) {
	provider := &scriptChatProvider{fallback: "ordinary continuation completed"}
	al, _ := newChatTargetAskUserLoop(t, provider)
	gate1WireSessionMessaging(t, al)
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	const body = "PRIVATE_B_ORDINARY_OLD_REPORT"
	_, notice := bExpiryRuntimeExpire(t, al.GetSessionStore(), meta.ID, "mia", "ordinary-helper:1:final", body)
	for call := 1; call <= 2; call++ {
		_, _, err = al.processMessageAsPrimaryForTest(context.Background(), bExpiryRuntimeOrdinaryMessage(meta.ID, "Continue the conversation."))
		require.NoError(t, err)
		require.Equal(t, call, provider.callCount())
		request, ok := provider.lastMessages(call)
		require.True(t, ok)
		bExpiryRuntimeAssertModelNotice(t, request, notice, body)
	}
	bExpiryRuntimeAssertNoticeConsumed(t, al.GetSessionStore(), meta.ID, notice)
}

func bExpiryRuntimeAssertNoticeConsumed(t *testing.T, store *session.UnifiedStore, sid string, notice session.RetentionNotice) {
	t.Helper()
	addr, placed, err := store.SavedChatInput(sid, notice.Entry.ID)
	require.NoError(t, err)
	require.True(t, placed)
	_, _, err = store.PlaceSavedInput(context.Background(), sid, addr)
	require.ErrorIs(t, err, session.ErrAlreadyConsumed)
	owed, err := store.PendingRetentionNotices(context.Background(), sid)
	require.NoError(t, err)
	require.Empty(t, owed, "durable Source proof retires the owed notification")
	archive, err := session.NewArchiveDayStore(store.BaseDir(), sid)
	require.NoError(t, err)
	chatRows, placements := 0, 0
	require.NoError(t, archive.ScanAll(func(_ session.ArchiveAddress, rec session.ArchiveRecord) bool {
		if rec.ID == notice.Entry.ID {
			chatRows++
		}
		if rec.ModelRef != nil && rec.ModelRef.EntryID == notice.Entry.ID {
			placements++
		}
		return true
	}))
	require.Equal(t, 1, chatRows)
	require.Equal(t, 1, placements, "exactly one durable placement, not exactly-once provider execution")
}

func bExpiryRuntimeDriveIdle(t *testing.T, f *bExpiryRuntimeAgentFixture) []providers.Message {
	t.Helper()
	var wake bus.InboundMessage
	select {
	case wake = <-f.al.bus.InboundChan():
	case <-time.After(5 * time.Second): // Existing pwDrivePublishedWake fixture budget, not a pass condition.
		t.Fatal("real dispatcher published no wake for the runtime receiver")
	}
	require.Equal(t, f.parent, wake.AsyncTranscriptSessionID)
	require.Equal(t, f.notice.OutcomeID, wake.Metadata["steer_message_id"])
	require.Empty(t, f.provider.bExpiryRuntimeRequests(), "publication alone does not execute the provider")
	_, err := f.al.processSystemMessage(context.Background(), wake)
	require.NoError(t, err)
	requests := f.provider.bExpiryRuntimeRequests()
	require.Len(t, requests, 1, "one receiver-driven real provider boundary")
	return requests[0]
}

func bExpiryRuntimeIdleNotice(t *testing.T) {
	f := bExpiryRuntimeNewAgentFixture(t)
	var emitted []session.RetentionNotice
	require.NoError(t, f.al.DispatchRetentionNotices(context.Background(), func(n session.RetentionNotice) error {
		emitted = append(emitted, n)
		return nil
	}))
	require.Len(t, emitted, 1)
	require.Equal(t, f.notice, emitted[0])
	messages := bExpiryRuntimeDriveIdle(t, f)
	bExpiryRuntimeAssertModelNotice(t, messages, f.notice, f.body)
	bExpiryRuntimeAssertNoticeConsumed(t, f.store, f.parent, f.notice)
	require.NoError(t, f.al.DispatchRetentionNotices(context.Background(), func(n session.RetentionNotice) error {
		emitted = append(emitted, n)
		return nil
	}))
	require.Len(t, emitted, 1, "model-admitted notice is no longer owed")
}

type bExpiryRuntimeLiveProvider struct {
	requests chan []providers.Message
	release  chan struct{}
	once     sync.Once
	calls    atomic.Int32
}

func (p *bExpiryRuntimeLiveProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	first := p.calls.Add(1) == 1
	select {
	case p.requests <- append([]providers.Message(nil), messages...):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if first {
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &providers.LLMResponse{Content: "live parent continued"}, nil
}

func (*bExpiryRuntimeLiveProvider) GetDefaultModel() string { return "test-model" }
func (p *bExpiryRuntimeLiveProvider) bExpiryRuntimeOpen()   { p.once.Do(func() { close(p.release) }) }

func bExpiryRuntimeLiveNotice(t *testing.T) {
	p := &bExpiryRuntimeLiveProvider{requests: make(chan []providers.Message, 8), release: make(chan struct{})}
	al, _ := newChatTargetAskUserLoop(t, p)
	gate1WireSessionMessaging(t, al)
	t.Cleanup(p.bExpiryRuntimeOpen)
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	done := make(chan error, 1)
	joined := false
	go func() {
		_, _, err := al.processMessageAsPrimaryForTest(context.Background(), bExpiryRuntimeOrdinaryMessage(meta.ID, "Keep working."))
		done <- err
	}()
	t.Cleanup(func() {
		p.bExpiryRuntimeOpen()
		if !joined {
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Error("live ordinary turn did not join before teardown")
			}
		}
	})
	select {
	case first := <-p.requests:
		require.Equal(t, 1, gate1CountInMessages(first, "Keep working."), "SETUP: real ordinary input reached the provider edge once")
	case <-time.After(5 * time.Second):
		t.Fatal("real ordinary turn did not reach its provider")
	}
	require.NotNil(t, al.liveTurnForSession(meta.ID))
	const original = "PRIVATE_B_REPORT_EXPIRING_DURING_LIVE_TURN"
	_, notice := bExpiryRuntimeExpire(t, al.GetSessionStore(), meta.ID, "mia", "live-child:1:final", original)
	require.NoError(t, al.DispatchRetentionNotices(context.Background(), nil))
	require.True(t, al.wakePublicationInFlight(meta.ID, notice.OutcomeID))
	select {
	case unexpected := <-al.bus.InboundChan():
		t.Fatalf("live parent received a separate idle wake: %v", unexpected)
	default:
	}
	p.bExpiryRuntimeOpen()
	select {
	case request := <-p.requests:
		bExpiryRuntimeAssertModelNotice(t, request, notice, original)
	case <-time.After(5 * time.Second):
		t.Fatal("live join never reached the existing continuation consumption fence")
	}
	select {
	case err := <-done:
		joined = true
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary parent did not complete after provider release")
	}
	bExpiryRuntimeAssertNoticeConsumed(t, al.GetSessionStore(), meta.ID, notice)
}

func bExpiryRuntimeStopHold(t *testing.T) {
	f := bExpiryRuntimeNewAgentFixture(t)
	require.NoError(t, f.life.Mutate(f.parent, func(rec *session.LifecycleRecord) error {
		rec.State = session.LifecycleStopped
		rec.StopNote = &session.StopNote{At: time.Now().UTC(), By: "human:owner", Seq: 1, Cause: session.StopCauseStop}
		return nil
	}))
	before, err := f.life.Load(f.parent)
	require.NoError(t, err)
	emitted := 0
	require.NoError(t, f.al.DispatchRetentionNotices(context.Background(), func(n session.RetentionNotice) error {
		require.Equal(t, f.notice.Entry.ID, n.Entry.ID)
		emitted++
		return nil
	}))
	require.Equal(t, 1, emitted, "Stop holds model delivery, not saved operational visibility")
	after, err := f.life.Load(f.parent)
	require.NoError(t, err)
	require.Equal(t, before, after, "expiry notification never revives or rewrites Stop authority")
	_, placed, err := f.store.SavedChatInput(f.parent, f.notice.Entry.ID)
	require.NoError(t, err)
	require.False(t, placed)
	owed, err := f.store.PendingRetentionNotices(context.Background(), f.parent)
	require.NoError(t, err)
	require.Len(t, owed, 1)
	require.False(t, f.al.wakePublicationInFlight(f.parent, f.notice.OutcomeID))
}

func bExpiryRuntimeQuestionHold(t *testing.T) {
	provider := &scriptChatProvider{scripted: []*providers.LLMResponse{
		e2eToolCallResponse("call_b_expiry_question", tools.AskUserQuestionToolName,
			`{"questions":[{"header":"Scope","question":"Which scope?","options":[{"label":"Small","description":"one item"},{"label":"Large","description":"several items"}]}]}`),
	}, fallback: "unexpected question continuation"}
	al, msgBus := newChatTargetAskUserLoop(t, provider)
	gate1WireSessionMessaging(t, al)
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	registry := askuser.NewRegistry(store, &throughBusResumeDispatcher{msgBus: msgBus}, askuser.Options{})
	al.SetAskUserRegistry(registry)
	_, _, err = al.processMessageAsPrimaryForTest(context.Background(), bExpiryRuntimeOrdinaryMessage(meta.ID, "Ask me to choose the scope."))
	require.NoError(t, err)
	card := waitForPendingCard(t, registry, meta.ID, busWaitBudget)
	require.NotEmpty(t, card.CardID, "SETUP: use an actual question produced by the real tool/registry")
	beforeMeta, err := store.GetMeta(meta.ID)
	require.NoError(t, err)
	beforeLife, err := al.GetSessionLifecycleStore().Load(meta.ID)
	require.NoError(t, err)
	_, notice := bExpiryRuntimeExpire(t, store, meta.ID, "mia", "question-child:1:final", "PRIVATE_B_QUESTION_HELD_REPORT")
	require.NoError(t, al.DispatchRetentionNotices(context.Background(), nil))
	afterMeta, err := store.GetMeta(meta.ID)
	require.NoError(t, err)
	afterLife, err := al.GetSessionLifecycleStore().Load(meta.ID)
	require.NoError(t, err)
	require.Equal(t, beforeMeta.PendingAskJSON, afterMeta.PendingAskJSON)
	require.Equal(t, beforeLife.State, afterLife.State)
	require.Equal(t, beforeLife.Generation, afterLife.Generation)
	require.Equal(t, 1, provider.callCount(), "synchronous dispatcher does not start a question-answering turn")
	_, placed, err := store.SavedChatInput(meta.ID, notice.Entry.ID)
	require.NoError(t, err)
	require.False(t, placed)
	var pending askuser.PendingSet
	require.NoError(t, json.Unmarshal([]byte(afterMeta.PendingAskJSON), &pending))
	require.Equal(t, askuser.StatusPending, pending.Status)
	require.False(t, al.wakePublicationInFlight(meta.ID, notice.OutcomeID))
}

func bExpiryRuntimeSinkFailure(t *testing.T) {
	f := bExpiryRuntimeNewAgentFixture(t)
	fault := errors.New("b-expiry-runtime: live sink unavailable")
	calls := 0
	err := f.al.DispatchRetentionNotices(context.Background(), func(n session.RetentionNotice) error {
		calls++
		require.Equal(t, f.notice, n)
		return fault
	})
	require.ErrorIs(t, err, fault)
	require.Equal(t, 1, calls)
	require.False(t, f.al.wakePublicationInFlight(f.parent, f.notice.OutcomeID))
	owed, err := f.store.PendingRetentionNotices(context.Background(), f.parent)
	require.NoError(t, err)
	require.Equal(t, []session.RetentionNotice{f.notice}, owed)
	_, placed, err := f.store.SavedChatInput(f.parent, f.notice.Entry.ID)
	require.NoError(t, err)
	require.False(t, placed)
	require.NoError(t, f.al.DispatchRetentionNotices(context.Background(), nil))
	bExpiryRuntimeAssertModelNotice(t, bExpiryRuntimeDriveIdle(t, f), f.notice, f.body)
	bExpiryRuntimeAssertNoticeConsumed(t, f.store, f.parent, f.notice)
}

func bExpiryRuntimePublicationCancellation(t *testing.T) {
	f := bExpiryRuntimeNewAgentFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	calls := 0
	err := f.al.DispatchRetentionNotices(ctx, func(n session.RetentionNotice) error {
		calls++
		require.Equal(t, f.notice, n)
		cancel() // Real cancellation after visibility, before model-wake publication.
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
	require.False(t, f.al.wakePublicationInFlight(f.parent, f.notice.OutcomeID))
	owed, err := f.store.PendingRetentionNotices(context.Background(), f.parent)
	require.NoError(t, err)
	require.Equal(t, []session.RetentionNotice{f.notice}, owed)
	require.NoError(t, f.al.DispatchRetentionNotices(context.Background(), nil))
	bExpiryRuntimeAssertModelNotice(t, bExpiryRuntimeDriveIdle(t, f), f.notice, f.body)
}

func bExpiryRuntimeClosedBus(t *testing.T) {
	f := bExpiryRuntimeNewAgentFixture(t)
	f.al.bus.Close() // Real transport refusal, not a mock publisher inside the loop.
	err := f.al.DispatchRetentionNotices(context.Background(), nil)
	require.ErrorIs(t, err, bus.ErrBusClosed)
	require.False(t, f.al.wakePublicationInFlight(f.parent, f.notice.OutcomeID))
	owed, err := f.store.PendingRetentionNotices(context.Background(), f.parent)
	require.NoError(t, err)
	require.Equal(t, []session.RetentionNotice{f.notice}, owed)
	_, placed, err := f.store.SavedChatInput(f.parent, f.notice.Entry.ID)
	require.NoError(t, err)
	require.False(t, placed)
}
