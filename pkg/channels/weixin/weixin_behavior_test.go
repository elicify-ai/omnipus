package weixin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	basechannels "github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// newTestWeixinChannel builds a channel wired to an httptest server so outbound
// API calls and inbound dispatch can be exercised without real network.
func newTestWeixinChannel(t *testing.T, handler http.HandlerFunc) (*WeixinChannel, *bus.MessageBus) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	api, err := NewApiClient(srv.URL+"/", "test-token", "")
	if err != nil {
		t.Fatalf("NewApiClient: %v", err)
	}

	msgBus := bus.NewMessageBus()
	base := basechannels.NewBaseChannel("weixin", config.WeixinConfig{}, msgBus, nil)
	dir := t.TempDir()
	ch := &WeixinChannel{
		BaseChannel:       base,
		api:               api,
		config:            config.WeixinConfig{},
		bus:               msgBus,
		typingCache:       make(map[string]typingTicketCacheEntry),
		syncBufPath:       filepath.Join(dir, "sync.json"),
		contextTokensPath: filepath.Join(dir, "ctx.json"),
	}
	return ch, msgBus
}

func wxReceive(t *testing.T, msgBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()
	select {
	case msg := <-msgBus.InboundChan():
		return msg
	case <-time.After(time.Second):
		t.Fatal("expected inbound message")
		return bus.InboundMessage{}
	}
}

func wxExpectNone(t *testing.T, msgBus *bus.MessageBus) {
	t.Helper()
	select {
	case msg := <-msgBus.InboundChan():
		t.Fatalf("unexpected inbound message: %+v", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

// --- Inbound message parsing ---

func TestHandleInboundMessage_TextDispatchesAndStoresContextToken(t *testing.T) {
	ch, msgBus := newTestWeixinChannel(t, func(w http.ResponseWriter, r *http.Request) {})

	ch.handleInboundMessage(context.Background(), WeixinMessage{
		FromUserID:   "user-1",
		ClientID:     "cid-1",
		ContextToken: "ctx-1",
		ItemList: []MessageItem{
			{Type: MessageItemTypeText, TextItem: &TextItem{Text: "hello"}},
		},
	})

	inbound := wxReceive(t, msgBus)
	if inbound.Content != "hello" {
		t.Fatalf("content=%q", inbound.Content)
	}
	if inbound.Peer.Kind != bus.PeerDirect || inbound.Peer.ID != "user-1" {
		t.Fatalf("peer=%+v", inbound.Peer)
	}
	if inbound.MessageID != "cid-1" {
		t.Fatalf("message_id=%q", inbound.MessageID)
	}
	// context_token must be cached so an outbound reply can be associated.
	if v, ok := ch.contextTokens.Load("user-1"); !ok || v.(string) != "ctx-1" {
		t.Fatalf("context token not stored: %v ok=%v", v, ok)
	}
}

func TestHandleInboundMessage_VoiceTranscriptionUsed(t *testing.T) {
	ch, msgBus := newTestWeixinChannel(t, func(w http.ResponseWriter, r *http.Request) {})

	ch.handleInboundMessage(context.Background(), WeixinMessage{
		FromUserID: "user-2",
		ItemList: []MessageItem{
			{Type: MessageItemTypeVoice, VoiceItem: &VoiceItem{Text: "transcribed text"}},
		},
	})

	inbound := wxReceive(t, msgBus)
	if inbound.Content != "transcribed text" {
		t.Fatalf("content=%q want transcribed text", inbound.Content)
	}
}

func TestHandleInboundMessage_EmptyFromUserDropped(t *testing.T) {
	ch, msgBus := newTestWeixinChannel(t, func(w http.ResponseWriter, r *http.Request) {})

	ch.handleInboundMessage(context.Background(), WeixinMessage{
		ItemList: []MessageItem{{Type: MessageItemTypeText, TextItem: &TextItem{Text: "x"}}},
	})
	wxExpectNone(t, msgBus)
}

func TestHandleInboundMessage_EmptyContentNoMediaDropped(t *testing.T) {
	ch, msgBus := newTestWeixinChannel(t, func(w http.ResponseWriter, r *http.Request) {})

	ch.handleInboundMessage(context.Background(), WeixinMessage{
		FromUserID: "user-3",
		ItemList:   []MessageItem{{Type: MessageItemTypeText, TextItem: &TextItem{Text: ""}}},
	})
	wxExpectNone(t, msgBus)
}

func TestHandleInboundMessage_AllowlistRejects(t *testing.T) {
	ch, msgBus := newTestWeixinChannel(t, func(w http.ResponseWriter, r *http.Request) {})
	// Restrict allow-list to a different user.
	ch.BaseChannel = basechannels.NewBaseChannel("weixin", config.WeixinConfig{}, msgBus, []string{"weixin:allowed"})

	ch.handleInboundMessage(context.Background(), WeixinMessage{
		FromUserID: "blocked-user",
		ItemList:   []MessageItem{{Type: MessageItemTypeText, TextItem: &TextItem{Text: "hi"}}},
	})
	wxExpectNone(t, msgBus)
}

// --- Outbound send ---

func TestSend_FailsWhenNotRunning(t *testing.T) {
	ch, _ := newTestWeixinChannel(t, func(w http.ResponseWriter, r *http.Request) {})

	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "u", Content: "hi"})
	if err != basechannels.ErrNotRunning {
		t.Fatalf("err=%v want ErrNotRunning", err)
	}
}

func TestSend_FailsWithoutContextToken(t *testing.T) {
	ch, _ := newTestWeixinChannel(t, func(w http.ResponseWriter, r *http.Request) {})
	ch.SetRunning(true)
	t.Cleanup(func() { ch.SetRunning(false) })

	// No context token cached for this user → non-temporary send failure.
	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "no-token-user", Content: "hi"})
	if err == nil || !errors.Is(err, basechannels.ErrSendFailed) {
		t.Fatalf("err=%v want ErrSendFailed", err)
	}
}

func TestSend_TextRequestShape(t *testing.T) {
	var gotPath string
	var gotReq SendMessageReq
	ch, _ := newTestWeixinChannel(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotReq)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ret":0,"errcode":0}`))
	})
	ch.SetRunning(true)
	t.Cleanup(func() { ch.SetRunning(false) })
	ch.contextTokens.Store("user-x", "ctx-x")

	if err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "user-x", Content: "outbound hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.HasSuffix(gotPath, "ilink/bot/sendmessage") {
		t.Fatalf("path=%q", gotPath)
	}
	if gotReq.Msg.ToUserID != "user-x" {
		t.Fatalf("to_user_id=%q", gotReq.Msg.ToUserID)
	}
	if gotReq.Msg.ContextToken != "ctx-x" {
		t.Fatalf("context_token=%q", gotReq.Msg.ContextToken)
	}
	if len(gotReq.Msg.ItemList) != 1 ||
		gotReq.Msg.ItemList[0].Type != MessageItemTypeText ||
		gotReq.Msg.ItemList[0].TextItem == nil ||
		gotReq.Msg.ItemList[0].TextItem.Text != "outbound hi" {
		t.Fatalf("item_list=%+v", gotReq.Msg.ItemList)
	}
}

func TestSend_APIErrorPropagatesAsTemporary(t *testing.T) {
	ch, _ := newTestWeixinChannel(t, func(w http.ResponseWriter, r *http.Request) {
		// Non-session-expired API error → temporary so the manager may retry.
		_, _ = w.Write([]byte(`{"ret":1,"errcode":500,"errmsg":"boom"}`))
	})
	ch.SetRunning(true)
	t.Cleanup(func() { ch.SetRunning(false) })
	ch.contextTokens.Store("user-y", "ctx-y")

	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "user-y", Content: "hi"})
	if err == nil || !errors.Is(err, basechannels.ErrTemporary) {
		t.Fatalf("err=%v want ErrTemporary", err)
	}
}

func TestSend_SessionExpiredPausesAndFailsSend(t *testing.T) {
	ch, _ := newTestWeixinChannel(t, func(w http.ResponseWriter, r *http.Request) {
		// ret == weixinSessionExpiredCode triggers a session pause.
		_, _ = w.Write([]byte(`{"ret":-14,"errcode":0,"errmsg":"expired"}`))
	})
	ch.SetRunning(true)
	t.Cleanup(func() { ch.SetRunning(false) })
	ch.contextTokens.Store("user-z", "ctx-z")

	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "user-z", Content: "hi"})
	if err == nil {
		t.Fatal("expected send error on session expiry")
	}
	// After the pause is set, the session must report inactive.
	if ch.remainingPause() <= 0 {
		t.Fatal("expected session to be paused after -14")
	}
	if perr := ch.ensureSessionActive(); perr == nil {
		t.Fatal("expected ensureSessionActive to report paused session")
	}
}

// --- Zombie regression: pollLoop exit must flip IsRunning() to false. ---

func TestPollLoopExit_MarksNotRunning_OnParentCancel(t *testing.T) {
	// Server that always errors so the loop never blocks long; the loop should
	// still exit promptly when its context is canceled, and mark not-running.
	ch, _ := newTestWeixinChannel(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	// Simulate a parent-context teardown that is NOT routed through Stop():
	// IsRunning() is true, but once pollLoop returns the channel is dead and
	// must report not-running so the manager stops routing into it.
	ch.SetRunning(true)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		ch.pollLoop(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("pollLoop did not exit after context cancel")
	}

	if ch.IsRunning() {
		t.Fatal("zombie: IsRunning() still true after pollLoop exit")
	}
}
