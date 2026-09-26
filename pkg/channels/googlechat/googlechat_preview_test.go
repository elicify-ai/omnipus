package googlechat

// Regression tests for #648: processEvent built its log-preview field with a
// bare `strings.TrimSpace(...)[:50]` byte slice. Any MESSAGE whose content was
// shorter than 50 bytes after trimming panicked, and because processEvent runs
// as a bare goroutine (webhookHandler: go c.processEvent(event)) with no
// recover anywhere on the chain, that panic crashed the whole gateway. These
// tests drive the REAL processEvent path so the old code fails here with the
// exact production panic, not a lookalike.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// newPreviewTestChannel builds a running webhook-mode channel on a fresh bus.
// No AllowFrom is set, so every sender passes the allow-list (base.go's
// empty-list-is-permissive rule) and events reach the preview code path.
func newPreviewTestChannel(t *testing.T) (*GoogleChatChannel, *bus.MessageBus) {
	t.Helper()
	mb := bus.NewMessageBus()
	cfg := config.GoogleChatConfig{
		Enabled:    true,
		Mode:       "webhook",
		WebhookURL: newSS("https://chat.googleapis.com/example"),
	}
	ch, err := NewGoogleChatChannel(cfg, nil, mb)
	if err != nil {
		t.Fatalf("NewGoogleChatChannel() = %v", err)
	}
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	t.Cleanup(func() { _ = ch.Stop(context.Background()) })
	return ch, mb
}

// previewMessageEvent builds a MESSAGE event carrying text as its message body.
func previewMessageEvent(t *testing.T, text string) googleChatEvent {
	t.Helper()
	raw, err := json.Marshal(googleChatMessage{
		Name: "spaces/AAA/messages/m1",
		Text: text,
	})
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	return googleChatEvent{
		Type:    "MESSAGE",
		Space:   googleChatSpace{Name: "spaces/AAA"},
		Sender:  googleChatUser{Name: "users/123", DisplayName: "Tester"},
		Message: raw,
	}
}

// awaitInbound waits for one inbound bus message or fails after a deadline.
func awaitInbound(t *testing.T, mb *bus.MessageBus) bus.InboundMessage {
	t.Helper()
	select {
	case msg := <-mb.InboundChan():
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the message to reach the bus")
		return bus.InboundMessage{}
	}
}

// assertNoInbound proves the bus stayed empty (negative case: the empty
// message must keep taking the early return, publishing nothing).
func assertNoInbound(t *testing.T, mb *bus.MessageBus) {
	t.Helper()
	select {
	case msg := <-mb.InboundChan():
		t.Fatalf("unexpected inbound message: %+v", msg)
	default:
	}
}

// TestProcessEvent_ShortMessage_NoPanic is the #648 regression: every case here
// used to panic (or, for the 52-byte multi-byte string, cut mid-rune) inside
// processEvent's log-preview construction. The empty case exercises the
// content=="" early return, which must keep publishing nothing.
func TestProcessEvent_ShortMessage_NoPanic(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"empty message", ""},
		{"one character", "h"},
		{"49 ascii characters", strings.Repeat("a", 49)},
		{"exactly 50 ascii characters", strings.Repeat("a", 50)},
		{"51 ascii characters", strings.Repeat("a", 51)},
		{"24 accented + emoji: 25 runes, 52 bytes (old code cut mid-rune)", strings.Repeat("é", 24) + "👍"},
		{"10 accented characters: 20 bytes (old code panicked)", strings.Repeat("é", 10)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch, mb := newPreviewTestChannel(t)

			// Synchronous call on purpose: the old bug was an unrecovered
			// panic in this exact frame; as a bare goroutine it killed the
			// whole process, here it must fail the test instead.
			ch.processEvent(previewMessageEvent(t, tc.content))

			if tc.content == "" {
				assertNoInbound(t, mb)
				return
			}
			got := awaitInbound(t, mb)
			if got.Content != tc.content {
				t.Errorf("bus content = %q, want %q", got.Content, tc.content)
			}
			if got.ChatID != "spaces/AAA" {
				t.Errorf("bus chat_id = %q, want %q", got.ChatID, "spaces/AAA")
			}
		})
	}
}

// TestTruncatePreview pins the rune-safe truncation semantics of the #648
// helper: never panics at any length, truncates at exactly max runes, and —
// the multi-byte half of the bug — never cuts through a multi-byte character
// (every output must stay valid UTF-8, which a byte slice cannot guarantee).
func TestTruncatePreview(t *testing.T) {
	const maxRunes = 50
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty string", "", ""},
		{"one character", "h", "h"},
		{"49 ascii characters", strings.Repeat("a", 49), strings.Repeat("a", 49)},
		{"exactly 50 ascii characters", strings.Repeat("a", 50), strings.Repeat("a", 50)},
		{"51 ascii characters truncates to 50", strings.Repeat("a", 51), strings.Repeat("a", 50)},
		{"25 two-byte runes = 50 bytes exactly, kept whole", strings.Repeat("é", 25), strings.Repeat("é", 25)},
		{"51 two-byte runes truncate to 50 runes", strings.Repeat("é", 51), strings.Repeat("é", 50)},
		{
			"25 runes incl. 4-byte emoji: old [:50] split the emoji",
			strings.Repeat("é", 24) + "👍",
			strings.Repeat("é", 24) + "👍",
		},
		{
			"51 runes: cut lands between 49 é and the first 👍, never mid-rune",
			strings.Repeat("é", 49) + "👍👍",
			strings.Repeat("é", 49) + "👍",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncatePreview(tc.in, maxRunes)
			if got != tc.want {
				t.Errorf("truncatePreview(len=%d bytes, max=%d) = %d runes, want %d runes",
					len(tc.in), maxRunes, len([]rune(got)), len([]rune(tc.want)))
			}
			if !utf8.ValidString(got) {
				t.Errorf("truncatePreview output is not valid UTF-8: %q", got)
			}
		})
	}
	// Off-limit inputs: non-positive max clears, oversized max is a no-op.
	if got := truncatePreview("anything", 0); got != "" {
		t.Errorf("truncatePreview with max=0 = %q, want empty", got)
	}
	if got := truncatePreview("hi", 100); got != "hi" {
		t.Errorf("truncatePreview with max=100 on %q = %q, want unchanged", "hi", got)
	}
}
