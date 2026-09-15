package webrtc

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

// This replaces the earlier diagnostic skip with exact production counters.
// The consumer has entered its first sink before the burst begins, so it
// cannot remove any part of the measured backlog until we release it.
func TestInputQueueOverflowCountersAndFreshness(t *testing.T) {
	first := []byte(`{"kind":"mouse_move","seq":-1}`)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var seen []string
	s := NewSession(Config{}, func(_ string, raw []byte) {
		if len(seen) == 0 {
			close(entered)
			<-release
		}
		seen = append(seen, string(raw))
	}, nil)
	t.Cleanup(func() { _ = s.Close() })
	q := newInputQueue()
	defer q.close()
	s.enqueueInput("test", "viewer", q, first)
	go func() { defer close(done); s.runInputQueue("viewer", q) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter first sink")
	}
	for i := 0; i < inputQueueCapacity+36; i++ {
		s.enqueueInput("test", "viewer", q, []byte(fmt.Sprintf(`{"kind":"mouse_move","seq":%d}`, i)))
	}
	got := s.Stats()
	if got.InputShedPositional != 36 || got.InputDroppedPositional != 0 || got.InputDroppedDiscrete != 0 {
		t.Errorf("overflow counters=(%d,%d,%d) want(36,0,0)", got.InputShedPositional, got.InputDroppedPositional, got.InputDroppedDiscrete)
	}
	q.close()
	once.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
	want := []string{string(first), fmt.Sprintf(`{"kind":"mouse_move","seq":%d}`, inputQueueCapacity+35)}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("dispatch=%v want only first and newest %v", seen, want)
	}
	if after := s.Stats(); after.InputShedPositional != 36 || after.InputDroppedPositional != 0 || after.InputDroppedDiscrete != 0 {
		t.Fatalf("lossless batch coalescing changed overflow counters: %+v", after)
	}
}

func TestInputQueueDiscreteAndPositionalLossCounters(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	q := newInputQueue()
	defer q.close()
	for i := 0; i < inputQueueCapacity; i++ {
		s.enqueueInput("test", "viewer", q, []byte(`{"kind":"key_down","key":"a"}`))
	}
	s.enqueueInput("test", "viewer", q, []byte(`{"kind":"mouse_move"}`))
	s.enqueueInput("test", "viewer", q, []byte(`{"kind":"key_up","key":"a"}`))
	s.enqueueInput("test", "viewer", q, []byte(`{"kind":"key_down","key":"b"}`))
	got := s.Stats()
	if got.InputShedPositional != 0 || got.InputDroppedPositional != 1 || got.InputDroppedDiscrete != 2 {
		t.Fatalf("discrete backlog counters=(%d,%d,%d) want(0,1,2)", got.InputShedPositional, got.InputDroppedPositional, got.InputDroppedDiscrete)
	}
	if q.Len() != inputQueueCapacity {
		t.Fatalf("backlog=%d want capacity%d", q.Len(), inputQueueCapacity)
	}
	q.close()
	s.enqueueInput("test", "viewer", q, []byte(`{"kind":"key_up"}`))
	if after := s.Stats(); after.InputDroppedDiscrete != 2 {
		t.Fatalf("closed source inflated discrete losses to%d", after.InputDroppedDiscrete)
	}
}
