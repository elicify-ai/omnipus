package gateway

// Founder B / lifecycle-design-b.md section 5. This drives the existing shared
// store, WS constructor, manual/nightly entry points and common session hub.
// Fake connections stop only at the socket edge. WRITTEN, NOT EXECUTED; not
// browser UAT, exactly-once socket delivery or provider-execution certification.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func TestRetentionExpiryNoticeLiveBoundary(t *testing.T) {
	for _, path := range []string{"direct_store", "manual_handler", "nightly_tick", "boot_tick_disabled_with_owed_notice"} {
		t.Run(path, func(t *testing.T) { bExpiryRuntimeGatewayCase(t, path) })
	}
}

type bExpiryRuntimeGatewayProvider struct{ calls atomic.Int32 }

func (p *bExpiryRuntimeGatewayProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	return &providers.LLMResponse{Content: "unexpected synchronous provider work"}, nil
}

func (*bExpiryRuntimeGatewayProvider) GetDefaultModel() string { return "test-model" }

type bExpiryRuntimeGatewayFixture struct {
	api      *restAPI
	store    *session.UnifiedStore
	life     *session.LifecycleStore
	sid      string
	other    string
	outcome  string
	body     string
	provider *bExpiryRuntimeGatewayProvider
}

func bExpiryRuntimeGatewayNew(t *testing.T) *bExpiryRuntimeGatewayFixture {
	t.Helper()
	api := lifecycleGatewayAPI(t) // One actual home for metadata, shared store and gateway.
	store, sid := lifecycleGatewaySeed(t, api, "mia")
	require.Equal(t, filepath.Join(api.homePath, "sessions"), store.BaseDir())
	life := session.NewLifecycleStore(filepath.Join(api.homePath, "session_lifecycle"))
	api.agentLoop.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(api.homePath, "session_messages")), life)
	require.NoError(t, life.Persist(&session.LifecycleRecord{
		SessionID: sid, Generation: 1, State: session.LifecycleStopped, AgentID: "mia",
		OwnerScopeKind: session.OwnerScopeHuman, Origin: &session.Origin{Kind: session.OriginKindChat},
		OriginChannel: "webchat", OriginChatID: sid,
		StopNote: &session.StopNote{At: time.Now().UTC(), By: "human:owner", Seq: 1, Cause: session.StopCauseStop},
	}))
	other, err := store.NewSession(session.SessionTypeChat, "webchat", "jim")
	require.NoError(t, err)
	provider := &bExpiryRuntimeGatewayProvider{}
	inst, ok := api.agentLoop.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.Provider = provider
	api.agentLoop.GetConfig().Storage.Retention.SessionDays = 7
	api.agentLoop.GetConfig().Storage.Retention.Disabled = false
	f := &bExpiryRuntimeGatewayFixture{api: api, store: store, life: life, sid: sid, other: other.ID,
		outcome: "gateway-helper:1:final", body: "PRIVATE_B_GATEWAY_EXPIRED_BODY_NEVER_SEND", provider: provider}
	_, err = store.SaveChatInput(sid, "saved-input-"+f.outcome, "mia", f.body)
	require.NoError(t, err)
	path := filepath.Join(store.BaseDir(), sid, "transcript.jsonl")
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(before), f.body, "SETUP: use the exact pending report's current file")
	old := time.Now().Add(-8 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))
	return f
}

func bExpiryRuntimeGatewayBind(t *testing.T, h *WSHandler, sid, name string) *fakeHubConn {
	t.Helper()
	hub := h.hubs.getOrCreate(sid)
	conn := newFakeHubConn(name)
	cursor, bootID := int64(hub.base), h.hubs.bootID
	binding := hub.bind(conn, &cursor, &bootID, bootID)
	require.True(t, binding.Servable, binding.Reason)
	t.Cleanup(func() { hub.unbind(conn) })
	return conn
}

func bExpiryRuntimeGatewayJournal(h *WSHandler, sid string) [][]byte {
	hub := h.hubs.getOrCreate(sid)
	hub.mu.Lock()
	defer hub.mu.Unlock()
	out := make([][]byte, 0, len(hub.journal))
	for _, item := range hub.journal {
		out = append(out, append([]byte(nil), item.bytes...))
	}
	return out
}

func bExpiryRuntimeGatewayTrigger(t *testing.T, f *bExpiryRuntimeGatewayFixture, path string) {
	t.Helper()
	switch path {
	case "direct_store":
		removed, err := f.store.RetentionSweep(7)
		require.NoError(t, err)
		require.Equal(t, 1, removed)
	case "manual_handler":
		w := httptest.NewRecorder()
		f.api.HandleRetentionSweep(w, httptest.NewRequest(http.MethodPost, "/api/v1/security/retention/sweep", nil))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var response generated.RetentionSweepResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, 1, response.Removed)
	case "nightly_tick", "boot_tick_disabled_with_owed_notice":
		// This is the exact synchronous body used for boot and timer ticks,
		// not a test substitute for retentionSweepFn or a shortened timer.
		executeSweepTick(f.store, f.api.agentLoop.GetConfig)
	default:
		t.Fatalf("unknown fixture entry path %q", path)
	}
}

func bExpiryRuntimeGatewayFrames(t *testing.T, rawFrames [][]byte, f *bExpiryRuntimeGatewayFixture, expired *session.SavedInputExpiry, notice session.RetentionNotice) []generated.ReplayMessageFrame {
	t.Helper()
	var matched []generated.ReplayMessageFrame
	for _, raw := range rawFrames {
		require.NotContains(t, string(raw), f.body)
		var envelope map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &envelope))
		for _, private := range []string{"model_message", "model_ref", "source", "view_membership", "expired_reports"} {
			require.NotContains(t, envelope, private, "no private receipt/archive field becomes a new wire member")
		}
		var frame generated.ReplayMessageFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame.Type != "replay_message" || frame.Id == nil || *frame.Id != notice.Entry.ID {
			continue
		}
		require.Equal(t, "system", frame.Role)
		require.Equal(t, f.sid, frame.SessionId)
		require.NotNil(t, frame.AgentId)
		require.Equal(t, "mia", *frame.AgentId)
		require.Equal(t, "saved-input-retention-expiry:"+expired.ReceiptID, *frame.Id)
		require.Equal(t, notice.Entry.Content, frame.Content)
		require.Contains(t, frame.Content, "saved-input-"+f.outcome)
		require.Contains(t, frame.Content, f.sid)
		require.Contains(t, strings.ToLower(frame.Content), "expired undelivered")
		require.NotNil(t, frame.Timestamp)
		stamp, err := time.Parse(time.RFC3339Nano, *frame.Timestamp)
		require.NoError(t, err)
		// The existing replay frame is ISO date-time; compare its canonical
		// whole-second rendering, not an invented nanosecond wire requirement.
		require.Equal(t, expired.ExpiredAt.UTC().Format(time.RFC3339), stamp.UTC().Format(time.RFC3339))
		matched = append(matched, frame)
	}
	require.NotEmpty(t, matched, "the common hub must receive the addressed operational system frame")
	return matched
}

func bExpiryRuntimeGatewayCase(t *testing.T, path string) {
	f := bExpiryRuntimeGatewayNew(t)
	before, err := f.life.Load(f.sid)
	require.NoError(t, err)
	if path == "boot_tick_disabled_with_owed_notice" {
		// The loss committed before WS sink installation. The boot-tick body
		// must deliver that owed notice even when new age decisions are disabled.
		removed, err := f.store.RetentionSweep(7)
		require.NoError(t, err)
		require.Equal(t, 1, removed)
		f.api.agentLoop.GetConfig().Storage.Retention.Disabled = true
	}
	transport := bus.NewMessageBus()
	t.Cleanup(transport.Close)
	h := newWSHandler(transport, f.api.agentLoop, "") // The real constructor must install the shared-store dispatcher hook.
	first := bExpiryRuntimeGatewayBind(t, h, f.sid, "first-addressed-tab")
	second := bExpiryRuntimeGatewayBind(t, h, f.sid, "second-addressed-tab")
	other := bExpiryRuntimeGatewayBind(t, h, f.other, "unrelated-chat")
	bExpiryRuntimeGatewayTrigger(t, f, path)
	_, placed, err := f.store.SavedChatInput(f.sid, "saved-input-"+f.outcome)
	require.ErrorIs(t, err, session.ErrSavedInputExpired)
	require.False(t, placed)
	var expired *session.SavedInputExpiry
	require.ErrorAs(t, err, &expired)
	notices, err := f.store.PendingRetentionNotices(context.Background(), f.sid)
	require.NoError(t, err)
	require.Len(t, notices, 1, "Stop preserves owed model delivery after live visibility")
	notice := notices[0]
	require.True(t, notice.ChatPresent)
	frames := bExpiryRuntimeGatewayFrames(t, bExpiryRuntimeGatewayJournal(h, f.sid), f, expired, notice)
	if path != "boot_tick_disabled_with_owed_notice" {
		require.Equal(t, first.snapshot(), second.snapshot(), "common hub sends identical sequenced bytes to both tabs")
		bExpiryRuntimeGatewayFrames(t, first.snapshot(), f, expired, notice)
	}
	require.Empty(t, other.snapshot(), "no frame is broadcast to an unrelated chat")
	require.Empty(t, bExpiryRuntimeGatewayJournal(h, f.other))
	// Re-dispatch while held. Repeated transport publication is permitted;
	// immutable ID/content/time must still collapse to one logical UI row.
	_, err = f.store.RetentionSweep(0)
	require.NoError(t, err)
	again := bExpiryRuntimeGatewayFrames(t, bExpiryRuntimeGatewayJournal(h, f.sid), f, expired, notice)
	for _, frame := range again {
		require.Equal(t, frames[0].Id, frame.Id)
		require.Equal(t, frames[0].Timestamp, frame.Timestamp)
		require.Equal(t, frames[0].Content, frame.Content)
	}
	entries, err := f.store.ReadTranscript(f.sid)
	require.NoError(t, err)
	count := 0
	for _, entry := range entries {
		require.NotContains(t, entry.Content, f.body)
		if entry.ID == notice.Entry.ID {
			count++
		}
	}
	require.Equal(t, 1, count, "one retained notice; not exactly-once socket publication")
	after, err := f.life.Load(f.sid)
	require.NoError(t, err)
	require.Equal(t, before, after, "manual/nightly/boot dispatch cannot revive a stopped parent")
	require.Zero(t, f.provider.calls.Load(), "store/WS dispatcher hook does no direct provider work")
	_, admitted, err := f.store.SavedChatInput(f.sid, notice.Entry.ID)
	require.NoError(t, err)
	require.False(t, admitted, "chat/frame visibility alone does not acknowledge model admission")
}
