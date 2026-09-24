package channels

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// fakeSteerAudience is a minimal steer.AudienceResolver double, keyed by
// session id.
type fakeSteerAudience struct {
	audience map[string]steer.Audience
}

var _ steer.AudienceResolver = (*fakeSteerAudience)(nil)

func (f *fakeSteerAudience) Audience(_ context.Context, sessionID string) (steer.Audience, steer.Class, error) {
	a, ok := f.audience[sessionID]
	if !ok {
		return steer.AudienceUser, steer.ClassOrdinaryRoot, nil
	}
	return a, steer.ClassSteered, nil
}

// recordingObserver is a minimal steer.BoundaryObserver double.
type recordingObserver struct {
	calls []steer.Boundary
}

var _ steer.BoundaryObserver = (*recordingObserver)(nil)

func (r *recordingObserver) Observe(b steer.Boundary, _ string, _ steer.Audience) {
	r.calls = append(r.calls, b)
}

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

// TestManagerGetStreamer_SteeredSessionContainedByAudience covers ADR-091
// boundary 7 (landing order §6, FR-B-001/FR-B-014, US-1/AS-4): a steered
// session's external-channel stream is contained by the injected
// steer.AudienceResolver alone — no parentSpawnCallID stamp needed — and
// the boundary is proven exercised via steer.BoundaryObserver.Observe.
func TestManagerGetStreamer_SteeredSessionContainedByAudience(t *testing.T) {
	for _, channelName := range []string{"telegram", "wecom"} {
		t.Run(channelName, func(t *testing.T) {
			underlying := &narrationRecordingStreamer{}
			manager := newTestManager()
			manager.channels[channelName] = &narrationStreamingChannel{streamer: underlying}
			obs := &recordingObserver{}
			manager.SetSteerAudienceResolver(&fakeSteerAudience{
				audience: map[string]steer.Audience{"child-session": steer.AudienceSteeringSession},
			}, obs)

			streamer, ok := manager.GetStreamer(context.Background(), channelName, "chat-1", "child-session")
			if !ok {
				t.Fatal("expected external streaming channel to provide a streamer")
			}
			// No parentSpawnCallID stamp — audience alone must contain it.

			if err := streamer.Update(context.Background(), "child draft"); err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			if err := streamer.Finalize(context.Background(), "child final"); err != nil {
				t.Fatalf("Finalize() error = %v", err)
			}

			if len(underlying.updates) != 0 || len(underlying.finalized) != 0 {
				t.Fatalf("steered session's stream reached %s: updates=%q finalized=%q",
					channelName, underlying.updates, underlying.finalized)
			}
			if len(obs.calls) != 1 || obs.calls[0] != steer.BoundaryExternalChannelStreaming {
				t.Fatalf("expected exactly one Observe(external_channel_streaming, ...) call, got %+v", obs.calls)
			}
		})
	}
}

// TestManagerGetStreamer_OrdinaryRootSession_UnaffectedByAudience proves a
// wired resolver does not interfere with an ordinary root's own stream.
func TestManagerGetStreamer_OrdinaryRootSession_UnaffectedByAudience(t *testing.T) {
	underlying := &narrationRecordingStreamer{}
	manager := newTestManager()
	manager.channels["telegram"] = &narrationStreamingChannel{streamer: underlying}
	manager.SetSteerAudienceResolver(&fakeSteerAudience{audience: map[string]steer.Audience{}}, nil)

	streamer, ok := manager.GetStreamer(context.Background(), "telegram", "chat-1", "root-session")
	if !ok {
		t.Fatal("expected external streaming channel to provide a streamer")
	}
	if err := streamer.Update(context.Background(), "root draft"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(underlying.updates) != 1 || underlying.updates[0] != "root draft" {
		t.Fatalf("expected the ordinary root's stream to be delivered, got: %q", underlying.updates)
	}
}
