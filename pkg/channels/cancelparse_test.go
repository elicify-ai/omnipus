package channels_test

import (
	"context"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/channels"
)

// TestIsCancelCommand_T11 verifies the T11 acceptance matrix from the spec
// (FR-2, US-2.3, US-2.4): /cancel must match on exact text only, never on
// substrings or sentences containing /cancel.
func TestIsCancelCommand_T11(t *testing.T) {
	rows := []struct {
		input   string
		want    bool
		comment string
	}{
		{"/cancel", true, "exact match"},
		{"/CANCEL", true, "uppercase"},
		{"  /cancel  ", true, "leading/trailing whitespace"},
		{"/Cancel", true, "mixed case"},
		{"/cancel my reservation", false, "sentence containing /cancel"},
		{"Hey /cancel", false, "/cancel in middle of sentence"},
		{"//cancel", false, "double-slash prefix"},
		{"", false, "empty string"},
		{"cancel", false, "missing slash prefix"},
	}
	for _, row := range rows {
		t.Run(row.comment, func(t *testing.T) {
			got := channels.IsCancelCommand(row.input)
			if got != row.want {
				t.Errorf("IsCancelCommand(%q) = %v; want %v (%s)", row.input, got, row.want, row.comment)
			}
		})
	}
}

// TestDispatchCancelIfRecognized_NilInterceptorSafe ensures that a nil
// interceptor does not panic and still returns true (message consumed).
func TestDispatchCancelIfRecognized_NilInterceptorSafe(t *testing.T) {
	sent := ""
	sendFn := func(_ context.Context, _, text string) error {
		sent = text
		return nil
	}
	got := channels.DispatchCancelIfRecognized(context.Background(), "/cancel", "irc", "#room", "alice", nil, sendFn)
	if !got {
		t.Fatal("expected true (message consumed) even with nil interceptor")
	}
	if sent != "⏸ Canceling..." {
		t.Errorf("expected ack %q; got %q", "⏸ Canceling...", sent)
	}
}

// TestDispatchCancelIfRecognized_NilSendFnSafe ensures that a nil sendFn does
// not panic when /cancel is matched.
func TestDispatchCancelIfRecognized_NilSendFnSafe(t *testing.T) {
	interrupted := false
	interceptor := &mockInterceptor{onRequestCancel: func(ctx context.Context, channel, chatID, userID string) error {
		interrupted = true
		return nil
	}}
	got := channels.DispatchCancelIfRecognized(
		context.Background(),
		"/cancel",
		"matrix",
		"!room:server",
		"bob",
		interceptor,
		nil,
	)
	if !got {
		t.Fatal("expected true (message consumed)")
	}
	if !interrupted {
		t.Fatal("expected interceptor to be called")
	}
}

// TestDispatchCancelIfRecognized_PassthroughOnNonCancel confirms that non-cancel
// messages return false (caller should dispatch normally).
func TestDispatchCancelIfRecognized_PassthroughOnNonCancel(t *testing.T) {
	interrupted := false
	interceptor := &mockInterceptor{onRequestCancel: func(_ context.Context, _, _, _ string) error {
		interrupted = true
		return nil
	}}
	for _, msg := range []string{"hello", "/cancel my order", "cancel", ""} {
		got := channels.DispatchCancelIfRecognized(
			context.Background(),
			msg,
			"line",
			"chatid",
			"user1",
			interceptor,
			nil,
		)
		if got {
			t.Errorf("DispatchCancelIfRecognized(%q) returned true; expected false (passthrough)", msg)
		}
		if interrupted {
			t.Errorf("interceptor was called for non-cancel message %q", msg)
		}
	}
}

// TestDispatchCancelIfRecognized_InterceptorCalledWithCorrectArgs verifies
// the channel and chatID fields passed to RequestCancelByChannelChat.
func TestDispatchCancelIfRecognized_InterceptorCalledWithCorrectArgs(t *testing.T) {
	var gotChannel, gotChatID string
	interceptor := &mockInterceptor{onRequestCancel: func(ctx context.Context, channel, chatID, userID string) error {
		gotChannel = channel
		gotChatID = chatID
		return nil
	}}
	channels.DispatchCancelIfRecognized(
		context.Background(),
		"  /CANCEL  ",
		"telegram",
		"chat123",
		"user42",
		interceptor,
		nil,
	)
	if gotChannel != "telegram" {
		t.Errorf("channel = %q; want %q", gotChannel, "telegram")
	}
	if gotChatID != "chat123" {
		t.Errorf("chatID = %q; want %q", gotChatID, "chat123")
	}
}

// mockInterceptor implements CancelInterceptor for testing.
type mockInterceptor struct {
	onRequestCancel func(ctx context.Context, channel, chatID, userID string) error
}

func (m *mockInterceptor) RequestCancelByChannelChat(ctx context.Context, channel, chatID, userID string) error {
	if m.onRequestCancel != nil {
		return m.onRequestCancel(ctx, channel, chatID, userID)
	}
	return nil
}

// TestDispatchCancelIfRecognized_UsesInstanceName is a regression test for
// MAJ-001: DispatchCancelIfRecognized call sites must pass c.Name() (the current
// channel name, set by SetInstanceID) rather than a hardcoded type-literal.
//
// After SetInstanceID("whatsapp.eu"), BaseChannel.Name() returns "whatsapp.eu".
// The inbound message's Channel field is also "whatsapp.eu" (stamped by
// HandleMessage from the same c.name). If a call site passes the hardcoded literal
// "whatsapp_native" instead of c.Name(), the cancel state machine cannot match the
// active turn (keyed by (channelName, chatID)) and /cancel becomes a silent no-op.
func TestDispatchCancelIfRecognized_UsesInstanceName(t *testing.T) {
	// Construct a channel that starts life as type "whatsapp_native" (as the
	// factory would set it), then receives its instance key via SetInstanceID.
	bc := channels.NewBaseChannel("whatsapp_native", nil, nil, nil)
	bc.SetInstanceID("whatsapp.eu")

	// Confirm the precondition: Name() now returns the instance key.
	if bc.Name() != "whatsapp.eu" {
		t.Fatalf("precondition: Name() = %q, want %q", bc.Name(), "whatsapp.eu")
	}

	// Simulate what the adapter call site does: pass c.Name() as the channel arg.
	var gotChannel string
	interceptor := &mockInterceptor{
		onRequestCancel: func(_ context.Context, channel, _, _ string) error {
			gotChannel = channel
			return nil
		},
	}

	got := channels.DispatchCancelIfRecognized(
		context.Background(),
		"/cancel",
		bc.Name(), // correct: uses the instance key, not "whatsapp_native"
		"chat123",
		"user1",
		interceptor,
		nil,
	)

	if !got {
		t.Fatal("expected DispatchCancelIfRecognized to return true for /cancel")
	}
	// The interceptor must receive the instance key, not the factory/type name.
	if gotChannel != "whatsapp.eu" {
		t.Errorf("interceptor called with channel=%q; want %q (instance key, not type literal)",
			gotChannel, "whatsapp.eu")
	}
}
