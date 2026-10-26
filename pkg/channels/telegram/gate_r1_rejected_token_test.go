package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

// tokenCaller answers getMe per mode: "rejected" with Telegram's 401,
// "unreachable" with a transport error, "ok" with a bot; everything else
// (getUpdates, setMyCommands, …) answers an empty success.
type tokenCaller struct{ mode string }

func (c tokenCaller) Call(_ context.Context, url string, _ *ta.RequestData) (*ta.Response, error) {
	if strings.HasSuffix(url, "/getMe") {
		switch c.mode {
		case "rejected":
			return &ta.Response{Ok: false, Error: &ta.Error{ErrorCode: 401, Description: "Unauthorized"}}, nil
		case "unreachable":
			return nil, errors.New("dial tcp: connection refused")
		}
		return &ta.Response{Ok: true, Result: []byte(`{"id":1,"is_bot":true,"first_name":"bot","username":"gate_bot"}`)}, nil
	}
	if strings.HasSuffix(url, "/getUpdates") {
		return &ta.Response{Ok: true, Result: []byte("[]")}, nil
	}
	return &ta.Response{Ok: true, Result: []byte("true")}, nil
}

func newTokenTestChannel(t *testing.T, mode string) *TelegramChannel {
	t.Helper()
	bot, err := telego.NewBot("123456:"+strings.Repeat("a", 35), telego.WithAPICaller(tokenCaller{mode: mode}), telego.WithDiscardLogger())
	if err != nil {
		t.Fatalf("NewBot: %v", err)
	}
	return &TelegramChannel{BaseChannel: channels.NewBaseChannel("telegram", nil, bus.NewMessageBus(), nil), bot: bot}
}

// TestTelegramStart_RejectedTokenFailsTheStart is Train 2 gate r1 (BDD-8.3
// channel survey): a token Telegram rejects (getMe 401) must fail Start and
// leave the channel not running, so connect_channel never certifies it.
// A transport error keeps today's behaviour (background retry); a valid
// token starts.
func TestTelegramStart_RejectedTokenFailsTheStart(t *testing.T) {
	ch := newTokenTestChannel(t, "rejected")
	if err := ch.Start(context.Background()); err == nil {
		_ = ch.Stop(context.Background())
		t.Fatal("a rejected token must fail Start")
	}
	if ch.IsRunning() {
		t.Fatal("a rejected token must leave the channel not running")
	}

	for _, mode := range []string{"ok", "unreachable"} {
		ch := newTokenTestChannel(t, mode)
		if err := ch.Start(context.Background()); err != nil {
			t.Fatalf("%s: Start must succeed, got %v", mode, err)
		}
		if !ch.IsRunning() {
			t.Fatalf("%s: the channel must be running", mode)
		}
		_ = ch.Stop(context.Background())
	}
}
