package channels

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

type narrationRecordingStreamer struct {
	updates   []string
	finalized []string
	cancels   int
}

func (s *narrationRecordingStreamer) Update(_ context.Context, content string) error {
	s.updates = append(s.updates, content)
	return nil
}

func (s *narrationRecordingStreamer) Finalize(_ context.Context, content string) error {
	s.finalized = append(s.finalized, content)
	return nil
}

func (s *narrationRecordingStreamer) Cancel(_ context.Context) {
	s.cancels++
}

type narrationStreamingChannel struct {
	mockChannel
	streamer bus.Streamer
}

func (c *narrationStreamingChannel) BeginStream(_ context.Context, _ string) (Streamer, error) {
	return c.streamer, nil
}

func stampNarrationStreamNesting(streamer bus.Streamer, parentSpawnCallID string) {
	if setter, ok := streamer.(interface {
		SetParentSpawnCallID(parentSpawnCallID string)
	}); ok {
		setter.SetParentSpawnCallID(parentSpawnCallID)
	}
}

func TestManagerGetStreamer_ContainsChildNarrationAndDeliversParentNarration(t *testing.T) {
	for _, channelName := range []string{"telegram", "wecom"} {
		t.Run(channelName, func(t *testing.T) {
			t.Run("delegated_child_is_contained", func(t *testing.T) {
				underlying := &narrationRecordingStreamer{}
				manager := newTestManager()
				manager.channels[channelName] = &narrationStreamingChannel{streamer: underlying}

				streamer, ok := manager.GetStreamer(context.Background(), channelName, "chat-1", "session-1")
				if !ok {
					t.Fatal("expected external streaming channel to provide a streamer")
				}
				stampNarrationStreamNesting(streamer, "delegate-call-1")

				if err := streamer.Update(context.Background(), "child draft"); err != nil {
					t.Fatalf("Update() error = %v", err)
				}
				if err := streamer.Finalize(context.Background(), "child final"); err != nil {
					t.Fatalf("Finalize() error = %v", err)
				}
				streamer.Cancel(context.Background())

				if len(underlying.updates) != 0 || len(underlying.finalized) != 0 || underlying.cancels != 0 {
					t.Fatalf("delegated child stream reached %s: updates=%q finalized=%q cancels=%d",
						channelName, underlying.updates, underlying.finalized, underlying.cancels)
				}
				if _, loaded := manager.streamActive.Load(channelName + ":chat-1"); loaded {
					t.Fatal("contained child stream must not arm outbound duplicate suppression")
				}
			})

			t.Run("parent_is_delivered", func(t *testing.T) {
				underlying := &narrationRecordingStreamer{}
				manager := newTestManager()
				manager.channels[channelName] = &narrationStreamingChannel{streamer: underlying}

				streamer, ok := manager.GetStreamer(context.Background(), channelName, "chat-1", "session-1")
				if !ok {
					t.Fatal("expected external streaming channel to provide a streamer")
				}
				stampNarrationStreamNesting(streamer, "")

				if err := streamer.Update(context.Background(), "parent draft"); err != nil {
					t.Fatalf("Update() error = %v", err)
				}
				if err := streamer.Finalize(context.Background(), "parent final"); err != nil {
					t.Fatalf("Finalize() error = %v", err)
				}
				streamer.Cancel(context.Background())

				if len(underlying.updates) != 1 || underlying.updates[0] != "parent draft" {
					t.Fatalf("parent draft did not reach %s: updates=%q", channelName, underlying.updates)
				}
				if len(underlying.finalized) != 1 || underlying.finalized[0] != "parent final" {
					t.Fatalf("parent final did not reach %s: finalized=%q", channelName, underlying.finalized)
				}
				if underlying.cancels != 1 {
					t.Fatalf("parent cancel did not reach %s: cancels=%d", channelName, underlying.cancels)
				}
				if _, loaded := manager.streamActive.Load(channelName + ":chat-1"); !loaded {
					t.Fatal("delivered parent stream must arm outbound duplicate suppression")
				}
			})
		})
	}
}
