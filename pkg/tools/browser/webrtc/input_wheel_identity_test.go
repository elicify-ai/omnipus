package webrtc

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// Input belongs to its original picture and gesture. Coalescing may add
// deltas only when every other field has the same meaning.
func TestInputQueueWheelPreservesOriginalSemantics(t *testing.T) {
	for _, field := range []string{"capture_id", "capture_generation", "modifiers", "x", "y", "capture_width", "capture_height", "future_basis"} {
		t.Run(field, func(t *testing.T) {
			first := map[string]any{"kind": "wheel", "capture_id": "old", "capture_generation": float64(1), "modifiers": float64(0), "x": float64(10), "y": float64(20), "capture_width": float64(800), "capture_height": float64(600), "future_basis": "old", "delta_x": float64(0), "delta_y": float64(100)}
			second := make(map[string]any, len(first))
			for key, value := range first {
				second[key] = value
			}
			if value, ok := second[field].(float64); ok {
				second[field] = value + 1
			} else {
				second[field] = "new"
			}
			second["delta_y"] = float64(1)
			var seen []map[string]any
			s := &Session{contextSink: func(_ context.Context, viewer string, raw []byte) {
				if viewer != "viewer" {
					t.Fatalf("wrong viewer %q", viewer)
				}
				var value map[string]any
				if err := json.Unmarshal(raw, &value); err != nil {
					t.Fatal(err)
				}
				seen = append(seen, value)
			}}
			q := newInputQueue()
			for _, value := range []map[string]any{first, second} {
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				q.push(raw, inputQueueCapacity)
			}
			q.close()
			s.runInputQueueContext(context.Background(), "viewer", q)
			if want := []map[string]any{first, second}; !reflect.DeepEqual(seen, want) {
				t.Fatalf("wheel input crossed %s boundary: got %#v, want %#v", field, seen, want)
			}
		})
	}
}
