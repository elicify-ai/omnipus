package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

type laneRecorder struct {
	mu      sync.Mutex
	order   []string
	release chan struct{}
	entered chan struct{}
}

func (r *laneRecorder) hook(_ context.Context, m bus.InboundMessage) {
	if m.Content == "[voice]" {
		close(r.entered)
		<-r.release // the voice note is "being transcribed"
	}
	r.mu.Lock()
	r.order = append(r.order, m.ChatID+":"+m.Content)
	r.mu.Unlock()
}

func (r *laneRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

func laneWaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Founder ruling (U8 r2 F5): within one chat messages are handled strictly in
// arrival order - a text sent right after a voice note must not overtake it -
// while another chat keeps flowing.
func TestBoundConnectorInput_PerChatOrderPreservedAndOtherChatsNotBlocked(t *testing.T) {
	f, _, ref := audioConnFixture(t, "irrelevant")
	rec := &laneRecorder{release: make(chan struct{}), entered: make(chan struct{})}
	f.al.admitDispatchHook = rec.hook

	voice := connMsg("chat-A", "[voice]")
	voice.Media = []string{ref}
	f.al.routeBoundConnectorInput(context.Background(), voice)
	<-rec.entered // the voice note is in flight

	f.al.routeBoundConnectorInput(context.Background(), connMsg("chat-A", "quick text"))
	f.al.routeBoundConnectorInput(context.Background(), connMsg("chat-B", "other chat"))

	// Control: a different chat is not blocked by chat A's voice note.
	laneWaitFor(t, "chat B to flow while chat A's voice note is in flight", func() bool {
		for _, s := range rec.snapshot() {
			if s == "chat-B:other chat" {
				return true
			}
		}
		return false
	})
	for _, s := range rec.snapshot() {
		if s == "chat-A:quick text" {
			t.Fatal("a later text overtook the voice note still being transcribed in the same chat")
		}
	}

	close(rec.release)
	laneWaitFor(t, "chat A to drain", func() bool { return len(rec.snapshot()) == 3 })
	var a []string
	for _, s := range rec.snapshot() {
		if s[:6] == "chat-A" {
			a = append(a, s)
		}
	}
	if len(a) != 2 || a[0] != "chat-A:[voice]" || a[1] != "chat-A:quick text" {
		t.Fatalf("chat A order = %v, want voice then text", a)
	}
	// The lane is gone once drained: the next message is admitted inline again.
	laneWaitFor(t, "the lane to end", func() bool {
		f.al.connLanes.Lock()
		defer f.al.connLanes.Unlock()
		return len(f.al.connLaneMap) == 0
	})
}
