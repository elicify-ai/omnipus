package telegram

// T-10b / D1 / FR-012: accepted canonical bare ids must survive the REAL
// Telegram adapter Send and telego request serialization, including a nonzero
// MessageThreadID for every topic. This is a protocol-preservation POSITIVE
// CONTROL, not evidence that outbound authorization was implemented.
//
// Expected integers are D1's accepted rows, fixed before running Send. JSON is
// decoded into integer fields (never float64, which rounds signed64 extremes).
// Unlike newTestChannel, this fixture does NOT supply a request constructor:
// telego v1.9.0's real default JSON serializer remains in the observed path.

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/url"
	"strings"
	"testing"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestT10b_Send_AcceptedTargetsPreserveChatAndTopicThroughRealSerialization(t *testing.T) {
	rows := []struct {
		name   string
		bareID string
		chatID int64
		topic  int
	}{
		{"person", "123456", 123456, 0},
		{"group", "-1001234567890", -1001234567890, 0},
		{"topic", "-1001234567890/45", -1001234567890, 45},
		{"int64 max", "9223372036854775807", math.MaxInt64, 0},
		{"int64 min", "-9223372036854775808", math.MinInt64, 0},
		{"topic one", "-100/1", -100, 1},
		{"topic int32 max", "-100/2147483647", -100, math.MaxInt32},
		{"int64 min with topic int32 max", "-9223372036854775808/2147483647", math.MinInt64, math.MaxInt32},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			caller := &stubCaller{callFn: func(context.Context, string, *ta.RequestData) (*ta.Response, error) {
				// Only the external Telegram API is replaced. A literal successful
				// protocol response avoids issuing any real network request.
				return &ta.Response{Ok: true, Result: json.RawMessage(`{"message_id":1}`)}, nil
			}}
			bot, err := telego.NewBot(testToken, telego.WithAPICaller(caller), telego.WithDiscardLogger())
			require.NoError(t, err)
			base := channels.NewBaseChannel("telegram", nil, nil, nil, channels.WithMaxMessageLength(4000))
			base.SetRunning(true)
			ch := &TelegramChannel{BaseChannel: base, bot: bot, config: config.DefaultConfig()}
			const content = "canonical target protocol fixture"
			require.NoError(t, ch.Send(context.Background(), bus.OutboundMessage{
				Channel: "telegram", ChatID: row.bareID, Content: content,
			}))

			require.Len(t, caller.calls, 1, "T-10b: the short fixture is one real SendMessage request")
			call := caller.calls[0]
			endpoint, err := url.Parse(call.URL)
			require.NoError(t, err)
			require.True(t, strings.HasSuffix(endpoint.Path, "/sendMessage"), "instrument: observe SendMessage serialization, not a different Telegram method")
			require.NotNil(t, call.Data)
			require.Equal(t, "application/json", call.Data.ContentType)
			raw := call.Data.BodyRaw
			if len(raw) == 0 {
				// RequestData's documented wire carrier also permits a stream.
				require.NotNil(t, call.Data.BodyStream)
				raw, err = io.ReadAll(call.Data.BodyStream)
				require.NoError(t, err)
			}
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &fields), "instrument: decode actual library-produced JSON")
			require.Contains(t, fields, "chat_id")
			var chatID int64
			require.NoError(t, json.Unmarshal(fields["chat_id"], &chatID), "T-10b: chat id must retain its exact integer representation")
			require.Equal(t, row.chatID, chatID, "D1: validated signed64 chat id survives the adapter and real serializer")
			var text string
			require.NoError(t, json.Unmarshal(fields["text"], &text))
			require.Equal(t, content, text, "instrument: this is the actual fixture message")
			if row.topic != 0 {
				require.Contains(t, fields, "message_thread_id", "T-10b: a topic must never silently become a whole-chat send")
			}
			var topic int
			if encoded, present := fields["message_thread_id"]; present {
				require.NoError(t, json.Unmarshal(encoded, &topic))
			}
			require.Equal(t, row.topic, topic, "D1: validated topic survives actual MessageThreadID serialization")
			if row.topic != 0 {
				require.NotZero(t, topic, "T-10b: every accepted topic target has a nonzero wire MessageThreadID")
			}
		})
	}
}
