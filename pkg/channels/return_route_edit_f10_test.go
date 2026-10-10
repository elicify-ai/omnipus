package channels

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// U8 security F10: the captured return route is re-checked immediately before
// every content-carrying EditMessage, AFTER the cleanup callbacks (typing stop,
// reaction undo) - a rebind that happens inside a callback must stop the edit,
// and a successful edit must not skip the check before Send.

func editorWorker(edits *atomic.Int32, sends *atomic.Int32) (*mockMessageEditor, *channelWorker) {
	ch := &mockMessageEditor{editFn: func(context.Context, string, string, string) error {
		edits.Add(1)
		return nil
	}}
	ch.sendFn = func(context.Context, bus.OutboundMessage) error { sends.Add(1); return nil }
	return ch, &channelWorker{ch: ch, limiter: rate.NewLimiter(rate.Inf, 1)}
}

func TestSendWithRetry_RebindInsideCleanupCallbackStopsThePlaceholderEdit(t *testing.T) {
	m := newTestManager()
	m.liveCfg.Store(boundCfg("telegram.a", "ws-1", "mia"))
	var edits, sends atomic.Int32
	_, w := editorWorker(&edits, &sends)
	m.RecordPlaceholder("telegram.a", "chat-1", "ph-1")
	m.typingStops.Store("telegram.a:chat-1", typingEntry{createdAt: time.Now(), stop: func() {
		m.liveCfg.Store(boundCfg("telegram.a", "ws-1", "ray")) // rebound while the callback runs
	}})
	before := ReturnRouteRefusals()

	m.sendWithRetry(context.Background(), "telegram.a", w, guestAnswer("telegram.a"), false)

	if edits.Load() != 0 || sends.Load() != 0 {
		t.Fatalf("after a rebind inside a cleanup callback: edits=%d sends=%d, want 0 and 0", edits.Load(), sends.Load())
	}
	if ReturnRouteRefusals() != before+1 {
		t.Fatalf("the refusal at the edit boundary must be counted once, got %d", ReturnRouteRefusals()-before)
	}
}

func TestSendWithRetry_RebindInsideReactionUndoStopsTheStreamFallbackEdit(t *testing.T) {
	m := newTestManager()
	m.liveCfg.Store(boundCfg("telegram.a", "ws-1", "mia"))
	var edits, sends atomic.Int32
	_, w := editorWorker(&edits, &sends)
	m.RecordPlaceholder("telegram.a", "chat-1", "ph-1")
	m.streamActive.Store("telegram.a:chat-1", streamEntry{createdAt: time.Now()})
	m.reactionUndos.Store("telegram.a:chat-1", reactionEntry{createdAt: time.Now(), undo: func() {
		m.liveCfg.Store(boundCfg("telegram.a", "ws-1", "ray"))
	}})

	m.sendWithRetry(context.Background(), "telegram.a", w, guestAnswer("telegram.a"), false)

	if edits.Load() != 0 || sends.Load() != 0 {
		t.Fatalf("stream fallback after a rebind: edits=%d sends=%d, want 0 and 0", edits.Load(), sends.Load())
	}
}

// Positive controls: an unchanged binding still edits the placeholder once and
// sends nothing; an ordinary message with no captured return is untouched.
func TestSendWithRetry_UnchangedBindingStillEditsOnce(t *testing.T) {
	m := newTestManager()
	m.liveCfg.Store(boundCfg("telegram.a", "ws-1", "mia"))
	var edits, sends atomic.Int32
	_, w := editorWorker(&edits, &sends)
	m.RecordPlaceholder("telegram.a", "chat-1", "ph-1")
	m.typingStops.Store("telegram.a:chat-1", typingEntry{createdAt: time.Now(), stop: func() {}})

	m.sendWithRetry(context.Background(), "telegram.a", w, guestAnswer("telegram.a"), false)

	if edits.Load() != 1 || sends.Load() != 0 {
		t.Fatalf("authorized answer: edits=%d sends=%d, want 1 and 0", edits.Load(), sends.Load())
	}
}

func TestSendWithRetry_OrdinaryMessageEditsWithoutAReturnRoute(t *testing.T) {
	m := newTestManager()
	m.liveCfg.Store(boundCfg("telegram.a", "ws-1", "mia"))
	var edits, sends atomic.Int32
	_, w := editorWorker(&edits, &sends)
	m.RecordPlaceholder("telegram.a", "chat-1", "ph-1")
	plain := guestAnswer("telegram.a")
	plain.Return = nil

	m.sendWithRetry(context.Background(), "telegram.a", w, plain, false)

	if edits.Load() != 1 {
		t.Fatalf("an ordinary message must still edit its placeholder, edits=%d", edits.Load())
	}
}
