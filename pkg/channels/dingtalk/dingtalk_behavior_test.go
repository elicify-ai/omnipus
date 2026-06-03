package dingtalk

import (
	"context"
	"testing"
	"time"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
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
	if got.(string) != "https://example.com/reply-22" {
		t.Fatalf("stored webhook=%q", got)
	}
}
