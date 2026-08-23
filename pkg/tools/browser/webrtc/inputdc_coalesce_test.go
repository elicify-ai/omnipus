// inputdc_coalesce_test.go — unit tests for coalesceInputBatch (2026-08-13),
// the dequeue-time compaction that bounds input LATENCY (push-time shedding
// only ever bounded memory): a drained backlog is dispatched as the few
// events that still matter, not the history of how the cursor got there.

package webrtc

import (
	"encoding/json"
	"fmt"
	"testing"
)

func frames(t *testing.T, raws ...string) [][]byte {
	t.Helper()
	out := make([][]byte, len(raws))
	for i, r := range raws {
		out[i] = []byte(r)
	}
	return out
}

func kinds(t *testing.T, batch [][]byte) []string {
	t.Helper()
	out := make([]string, len(batch))
	for i, raw := range batch {
		out[i] = inputKindOf(raw)
	}
	return out
}

func TestCoalesce_MoveRunCollapsesToNewest(t *testing.T) {
	batch := make([][]byte, 0, 100)
	for i := 0; i < 100; i++ {
		batch = append(batch, []byte(fmt.Sprintf(`{"kind":"mouse_move","x":%d,"y":1}`, i)))
	}
	got := coalesceInputBatch(batch)
	if len(got) != 1 {
		t.Fatalf("100-move run should collapse to 1 frame, got %d", len(got))
	}
	var probe struct {
		X int `json:"x"`
	}
	if err := json.Unmarshal(got[0], &probe); err != nil || probe.X != 99 {
		t.Fatalf("survivor must be the NEWEST move (x=99), got %s (err=%v)", got[0], err)
	}
}

func TestCoalesce_WheelRunMergesDeltas(t *testing.T) {
	batch := frames(t,
		`{"kind":"wheel","x":10,"y":20,"delta_x":1,"delta_y":100}`,
		`{"kind":"wheel","x":11,"y":21,"delta_x":2,"delta_y":100}`,
		`{"kind":"wheel","x":12,"y":22,"delta_x":3,"delta_y":100,"capture_width":1122}`,
	)
	got := coalesceInputBatch(batch)
	if len(got) != 1 {
		t.Fatalf("wheel run should merge to 1 frame, got %d", len(got))
	}
	var probe struct {
		DeltaX       float64 `json:"delta_x"`
		DeltaY       float64 `json:"delta_y"`
		X            int     `json:"x"`
		CaptureWidth int     `json:"capture_width"`
	}
	if err := json.Unmarshal(got[0], &probe); err != nil {
		t.Fatal(err)
	}
	if probe.DeltaX != 6 || probe.DeltaY != 300 {
		t.Fatalf("total scroll distance must be preserved: want delta (6,300), got (%v,%v)", probe.DeltaX, probe.DeltaY)
	}
	if probe.X != 12 || probe.CaptureWidth != 1122 {
		t.Fatalf("non-delta fields must ride along from the NEWEST frame, got %s", got[0])
	}
}

func TestCoalesce_NeverCollapsesAcrossDiscreteEvents(t *testing.T) {
	batch := frames(t,
		`{"kind":"mouse_move","x":1}`,
		`{"kind":"mouse_move","x":2}`,
		`{"kind":"mouse_down","x":2}`,
		`{"kind":"mouse_move","x":3}`,
		`{"kind":"mouse_up","x":3}`,
		`{"kind":"wheel","delta_y":10}`,
		`{"kind":"wheel","delta_y":20}`,
		`{"kind":"key_down","key":"a"}`,
		`{"kind":"key_up","key":"a"}`,
	)
	got := kinds(t, coalesceInputBatch(batch))
	want := []string{"mouse_move", "mouse_down", "mouse_move", "mouse_up", "wheel", "key_down", "key_up"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ordering across discrete events must be preserved exactly:\n got %v\nwant %v", got, want)
	}
}

func TestCoalesce_UnparseableAndUnknownPassThrough(t *testing.T) {
	batch := frames(t,
		`{"kind":"mouse_move","x":1}`,
		`not json at all`,
		`{"kind":"mouse_move","x":2}`,
		`{"kind":"somekind_future"}`,
	)
	got := coalesceInputBatch(batch)
	if len(got) != 4 {
		t.Fatalf("unparseable/unknown frames are non-coalescable and break runs; want all 4 preserved, got %d", len(got))
	}
}

func TestCoalesce_WheelRunWithUnparseableDeltaPassesThroughUnmerged(t *testing.T) {
	batch := frames(t,
		`{"kind":"wheel","delta_y":10}`,
		`{"kind":"wheel","delta_y":"bogus"}`,
	)
	got := coalesceInputBatch(batch)
	if len(got) != 2 {
		t.Fatalf("a wheel run with an unparseable member must pass through unmerged (correctness over compaction), got %d frames", len(got))
	}
}
