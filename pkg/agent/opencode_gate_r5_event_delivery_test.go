package agent

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SFH-R5-1/2 and team-lead follow-up: a one-shot refused-input notice must
// receive the event bus's protected retry treatment, unlike ordinary errors.
// The real full-buffer delivery is driven after checking that policy.
func TestOpenCodeR5_RefusedInputEventUsesProtectedDeliveryWithFullSubscriber(t *testing.T) {
	payload := ErrorPayload{SessionID: "r5-backpressured-helper", Stage: "human_input",
		Code: opencodeR4Code, Message: "Refused input delivery instrument"}
	value := reflect.ValueOf(&payload).Elem()
	if marker := value.FieldByName("RefusedInput"); marker.IsValid() {
		require.Equal(t, reflect.Bool, marker.Kind())
		marker.SetBool(true)
	}
	event := Event{Kind: EventKindError, Payload: payload}
	ordinary := Event{Kind: EventKindError, Payload: ErrorPayload{Code: opencodeR4Code, Message: payload.Message}}
	assert.False(t, mustNotDropEvent(ordinary), "control: an ordinary terminal W14 error is not identified input-refusal delivery")
	require.True(t, mustNotDropEvent(event), "SFH-R5: a refused-input event must not be best-effort when a subscriber buffer is full")

	bus := NewEventBus()
	t.Cleanup(bus.Close)
	sub := bus.Subscribe(1)
	bus.Emit(Event{Kind: EventKindLLMRequest, Meta: EventMeta{TurnID: "full-buffer-instrument"}})
	require.Len(t, sub.C, 1, "instrument: real subscriber is full before refusal emission")
	atTap, releaseTap, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var release sync.Once
	releaseEmitter := func() { release.Do(func() { close(releaseTap) }) }
	bus.SetSyncTap(func(Event) { close(atTap); <-releaseTap })
	go func() { defer close(done); bus.Emit(event) }()
	t.Cleanup(func() {
		releaseEmitter()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("event emitter did not join during cleanup")
		}
	})
	select {
	case <-atTap:
	case <-time.After(5 * time.Second): // Deadlock failsafe only, not latency oracle.
		t.Fatal("event emitter did not reach its real tap")
	}
	releaseEmitter()
	// This short drain delay supplies transient backpressure, not an elapsed-
	// time oracle. The policy assertion above also rejects best-effort errors.
	time.Sleep(30 * time.Millisecond)
	control := <-sub.C
	assert.Equal(t, EventKindLLMRequest, control.Kind, "instrument: drain the known buffer occupant, not the refusal")
	assert.Equal(t, "full-buffer-instrument", control.Meta.TurnID)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("protected refusal delivery did not finish after capacity was available")
	}
	require.Len(t, sub.C, 1, "one actual refusal remains for the previously-full subscriber")
	got := <-sub.C
	assert.Equal(t, EventKindError, got.Kind)
	assert.Equal(t, payload, got.Payload)
	assert.Zero(t, bus.dropped[EventKindError].Load(), "no refused-input drop after transient subscriber backpressure")
}
