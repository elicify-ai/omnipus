package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// SG-02 (final-gate security pass): while a voice note is being transcribed,
// later messages of the same chat wait on its lane. That wait must be bounded
// by the ONE waiting-item limit (MaxQueueSize) and refused beyond it with the
// same busy reply the session worker inbox uses - before the message is
// retained - while strict per-chat order and other chats' progress are kept.

func laneWaiting(al *AgentLoop, key string) int {
	al.connLanes.Lock()
	defer al.connLanes.Unlock()
	if lane := al.connLaneMap[key]; lane != nil {
		return len(lane.queue)
	}
	return 0
}

func TestBoundConnectorLane_WaitingBacklogIsBoundedAndRefusedWithTheBusyReply(t *testing.T) {
	f, _, ref := audioConnFixture(t, "irrelevant")
	rec := &laneRecorder{release: make(chan struct{}), entered: make(chan struct{})}
	f.al.admitDispatchHook = rec.hook

	voice := connMsg("chat-A", "[voice]")
	voice.Media = []string{ref}
	f.al.routeBoundConnectorInput(context.Background(), voice)
	<-rec.entered // transcription is held open

	key := connectorLaneKey(voice)
	const extra = 25
	for i := 0; i < MaxQueueSize+extra; i++ {
		f.al.routeBoundConnectorInput(context.Background(), connMsg("chat-A", fmt.Sprintf("m%d", i)))
	}

	// The voice note itself left the queue to be admitted, so exactly
	// MaxQueueSize later messages may wait behind it.
	if got := laneWaiting(f.al, key); got != MaxQueueSize {
		t.Fatalf("waiting messages on the lane = %d, want exactly the bound %d", got, MaxQueueSize)
	}

	// Each refused message got the visible busy reply.
	busy := 0
	for busy < extra {
		select {
		case out := <-f.al.bus.OutboundChan():
			if strings.Contains(out.Content, "could not be queued") && out.ChatID == "chat-A" {
				busy++
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("got %d busy replies, want %d", busy, extra)
		}
	}

	// Release: the voice note then the first MaxQueueSize messages, in order; the
	// refused ones never run.
	close(rec.release)
	laneWaitFor(t, "chat A to drain", func() bool { return len(rec.snapshot()) == 1+MaxQueueSize })
	got := rec.snapshot()
	if got[0] != "chat-A:[voice]" {
		t.Fatalf("first admitted = %q, want the voice note", got[0])
	}
	for i := 0; i < MaxQueueSize; i++ {
		if want := fmt.Sprintf("chat-A:m%d", i); got[1+i] != want {
			t.Fatalf("position %d = %q, want %q (strict per-chat order)", 1+i, got[1+i], want)
		}
	}
}

// At the bound exactly, the next message is accepted; one more is refused.
func TestBoundConnectorLane_AcceptsExactlyUpToTheBound(t *testing.T) {
	f, _, ref := audioConnFixture(t, "irrelevant")
	rec := &laneRecorder{release: make(chan struct{}), entered: make(chan struct{})}
	f.al.admitDispatchHook = rec.hook
	voice := connMsg("chat-A", "[voice]")
	voice.Media = []string{ref}
	f.al.routeBoundConnectorInput(context.Background(), voice)
	<-rec.entered
	defer close(rec.release)

	key := connectorLaneKey(voice)
	for i := 0; i < MaxQueueSize; i++ {
		f.al.routeBoundConnectorInput(context.Background(), connMsg("chat-A", "x"))
	}
	if got := laneWaiting(f.al, key); got != MaxQueueSize {
		t.Fatalf("at the limit the lane holds %d, want %d", got, MaxQueueSize)
	}
	f.al.routeBoundConnectorInput(context.Background(), connMsg("chat-A", "one too many"))
	if got := laneWaiting(f.al, key); got != MaxQueueSize {
		t.Fatalf("the message past the limit was retained: lane holds %d, want %d", got, MaxQueueSize)
	}

	// Another chat is unaffected by chat A being full.
	f.al.routeBoundConnectorInput(context.Background(), connMsg("chat-B", "other"))
	laneWaitFor(t, "chat B to flow", func() bool {
		for _, s := range rec.snapshot() {
			if s == "chat-B:other" {
				return true
			}
		}
		return false
	})
}
