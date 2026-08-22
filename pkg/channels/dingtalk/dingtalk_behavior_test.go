package dingtalk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// mustNotReceiveInbound fails if any inbound message is published within a short window.
func mustNotReceiveInbound(t *testing.T, msgBus *bus.MessageBus) {
	t.Helper()
	select {
	case msg := <-msgBus.InboundChan():
		t.Fatalf("unexpected inbound message: %+v", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNewDingTalkChannel_RequiresCredentials(t *testing.T) {
	msgBus := bus.NewMessageBus()

	t.Run("missing client id", func(t *testing.T) {
		_, err := NewDingTalkChannel(
			config.DingTalkConfig{ClientSecretRef: "REF"},
			credentials.SecretBundle{},
			msgBus,
		)
		if err == nil {
			t.Fatal("expected error when client_id is empty")
		}
	})

	t.Run("missing secret", func(t *testing.T) {
		// ClientID present but secret ref resolves to empty.
		_, err := NewDingTalkChannel(
			config.DingTalkConfig{ClientID: "id", ClientSecretRef: "MISSING_REF"},
			credentials.SecretBundle{},
			msgBus,
		)
		if err == nil {
			t.Fatal("expected error when client_secret is empty")
		}
	})
}

func TestOnChatBotMessageReceived_NilDataIsIgnored(t *testing.T) {
	ch, msgBus := newTestDingTalkChannel(t, config.DingTalkConfig{})

	resp, err := ch.onChatBotMessageReceived(context.Background(), nil)
	if err != nil || resp != nil {
		t.Fatalf("nil data: resp=%v err=%v", resp, err)
	}
	mustNotReceiveInbound(t, msgBus)
}

func TestOnChatBotMessageReceived_EmptyContentIgnored(t *testing.T) {
	ch, msgBus := newTestDingTalkChannel(t, config.DingTalkConfig{})

	_, err := ch.onChatBotMessageReceived(context.Background(), &chatbot.BotCallbackDataModel{
		Text:             chatbot.BotCallbackDataTextModel{Content: "   "},
		SenderStaffId:    "staff-1",
		ConversationType: "1",
		ConversationId:   "conv-1",
		SessionWebhook:   "https://example.com/wh",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	mustNotReceiveInbound(t, msgBus)
}

func TestOnChatBotMessageReceived_ContentFallbackFromContentMap(t *testing.T) {
	ch, msgBus := newTestDingTalkChannel(t, config.DingTalkConfig{})

	_, err := ch.onChatBotMessageReceived(context.Background(), &chatbot.BotCallbackDataModel{
		// Text empty → should fall back to Content map.
		Content:          map[string]any{"content": "  hello via map  "},
		SenderStaffId:    "staff-7",
		SenderNick:       "Carol",
		ConversationType: "1",
		ConversationId:   "conv-7",
		SessionWebhook:   "https://example.com/wh7",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	inbound := mustReceiveInbound(t, msgBus)
	if inbound.Content != "hello via map" {
		t.Fatalf("content=%q want %q", inbound.Content, "hello via map")
	}
}

func TestOnChatBotMessageReceived_RejectedByAllowlist(t *testing.T) {
	ch, msgBus := newTestDingTalkChannel(t, config.DingTalkConfig{
		AllowFrom: []string{"dingtalk:allowed-staff"},
	})

	_, err := ch.onChatBotMessageReceived(context.Background(), &chatbot.BotCallbackDataModel{
		Text:             chatbot.BotCallbackDataTextModel{Content: "ping"},
		SenderStaffId:    "blocked-staff",
		ConversationType: "1",
		ConversationId:   "conv-block",
		SessionWebhook:   "https://example.com/wh",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	mustNotReceiveInbound(t, msgBus)
}

func TestOnChatBotMessageReceived_GroupNotMentionedDropped(t *testing.T) {
	ch, msgBus := newTestDingTalkChannel(t, config.DingTalkConfig{
		GroupTrigger: config.GroupTriggerConfig{MentionOnly: true},
	})

	_, err := ch.onChatBotMessageReceived(context.Background(), &chatbot.BotCallbackDataModel{
		Text:             chatbot.BotCallbackDataTextModel{Content: "hello group"},
		SenderStaffId:    "staff-9",
		ConversationType: "2",
		ConversationId:   "group-9",
		SessionWebhook:   "https://example.com/wh9",
		IsInAtList:       false, // not mentioned
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	mustNotReceiveInbound(t, msgBus)
}

func TestSend_FailsWhenNotRunning(t *testing.T) {
	ch, _ := newTestDingTalkChannel(t, config.DingTalkConfig{})

	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "c1", Content: "hi"})
	if err == nil {
		t.Fatal("expected error sending while not running")
	}
}

func TestSend_FailsWhenNoSessionWebhook(t *testing.T) {
	ch, _ := newTestDingTalkChannel(t, config.DingTalkConfig{})
	ch.SetRunning(true)
	t.Cleanup(func() { ch.SetRunning(false) })

	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "unknown-chat", Content: "hi"})
	if err == nil {
		t.Fatal("expected error when no session webhook stored for chat")
	}
}

func TestOnChatBotMessageReceived_StoresSessionWebhookForReply(t *testing.T) {
	ch, msgBus := newTestDingTalkChannel(t, config.DingTalkConfig{})

	_, err := ch.onChatBotMessageReceived(context.Background(), &chatbot.BotCallbackDataModel{
		Text:             chatbot.BotCallbackDataTextModel{Content: "remember me"},
		SenderStaffId:    "staff-22",
		ConversationType: "1",
		ConversationId:   "conv-22",
		SessionWebhook:   "https://example.com/reply-22",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	_ = mustReceiveInbound(t, msgBus)

	got, ok := ch.sessionWebhooks.Load("conv-22")
	if !ok {
		t.Fatal("expected session webhook stored under conversation id")
	}
	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("stored webhook has unexpected type %T, want string", got)
	}
	if gotStr != "https://example.com/reply-22" {
		t.Fatalf("stored webhook=%q", gotStr)
	}
}

// TestSend_WebhookRequestShape exercises the happy-path Send: it points the
// stored session webhook at an httptest server and asserts the POSTed JSON body
// is a DingTalk markdown reply (msgtype=markdown, title="Omnipus", text carries
// the message content) sent with a JSON content type. Previously the Send path
// only had failure-case coverage.
func TestSend_WebhookRequestShape(t *testing.T) {
	type captured struct {
		method      string
		contentType string
		body        map[string]any
	}
	got := make(chan captured, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got <- captured{
			method:      r.Method,
			contentType: r.Header.Get("Content-Type"),
			body:        body,
		}
		// The SDK requires a 200 to treat the send as successful.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	defer srv.Close()

	ch, _ := newTestDingTalkChannel(t, config.DingTalkConfig{})
	ch.SetRunning(true)
	t.Cleanup(func() { ch.SetRunning(false) })

	// Seed the session webhook for the target chat (normally stored on inbound).
	ch.sessionWebhooks.Store("conv-shape", srv.URL)

	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "conv-shape",
		Content: "hello from omnipus",
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	select {
	case c := <-got:
		if c.method != http.MethodPost {
			t.Fatalf("expected POST, got %s", c.method)
		}
		if !strings.HasPrefix(c.contentType, "application/json") {
			t.Fatalf("expected application/json content type, got %q", c.contentType)
		}
		if c.body["msgtype"] != "markdown" {
			t.Fatalf("expected msgtype=markdown, got %v", c.body["msgtype"])
		}
		md, ok := c.body["markdown"].(map[string]any)
		if !ok {
			t.Fatalf("expected a markdown object in body, got %T", c.body["markdown"])
		}
		if md["title"] != "Omnipus" {
			t.Fatalf("expected title=Omnipus, got %v", md["title"])
		}
		text, _ := md["text"].(string)
		if !strings.Contains(text, "hello from omnipus") {
			t.Fatalf("expected message content in markdown text, got %q", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook was never called by Send")
	}
}
