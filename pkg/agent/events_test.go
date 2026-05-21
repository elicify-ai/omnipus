package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEventKind_MarshalJSON_RoundTrip is the regression guard for #164.
//
// Before the fix, EventKind was a uint8 with no MarshalJSON method, so the
// wire payload for hook.event notifications carried an integer index rather
// than the canonical string name (`"tool_exec_start"`). Every subprocess hook
// author had to keep their own int→name map, and the indices shifted silently
// whenever a new EventKind was added in the middle of the enum.
//
// This test asserts the canonical string form is on the wire for every
// EventKind, and that string → EventKind round-trips losslessly.
func TestEventKind_MarshalJSON_AllValues(t *testing.T) {
	for i := EventKind(0); i < eventKindCount; i++ {
		t.Run(i.String(), func(t *testing.T) {
			raw, err := json.Marshal(i)
			if err != nil {
				t.Fatalf("MarshalJSON failed: %v", err)
			}
			want := `"` + i.String() + `"`
			if string(raw) != want {
				t.Fatalf("wire form: got %s, want %s", raw, want)
			}
			// Round-trip: name string → EventKind must produce the original value.
			var back EventKind
			if err := json.Unmarshal(raw, &back); err != nil {
				t.Fatalf("UnmarshalJSON failed: %v", err)
			}
			if back != i {
				t.Fatalf("round-trip mismatch: marshaled %d, unmarshaled %d", i, back)
			}
		})
	}
}

// TestEventKind_MarshalJSON_OnEventStruct verifies the canonical wire form
// when an EventKind is embedded inside the Event envelope (the actual
// payload subprocess hooks receive on hook.event notifications).
func TestEventKind_MarshalJSON_OnEventStruct(t *testing.T) {
	evt := Event{
		Kind:    EventKindToolExecStart,
		Payload: map[string]any{"tool": "echo"},
	}
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Before the fix this would contain `"Kind":8`. After the fix it must
	// contain the canonical string.
	if !strings.Contains(string(raw), `"Kind":"tool_exec_start"`) {
		t.Fatalf("Event.Kind must serialize as string name; got %s", raw)
	}
	if strings.Contains(string(raw), `"Kind":8`) {
		t.Fatalf("Event.Kind must NOT serialize as integer index; got %s", raw)
	}
}

// TestEventKind_UnmarshalJSON_UnknownNameRejected guards the negative
// direction: an unknown name fails loudly rather than silently mapping to
// EventKindTurnStart (the zero value).
func TestEventKind_UnmarshalJSON_UnknownNameRejected(t *testing.T) {
	var k EventKind
	err := json.Unmarshal([]byte(`"wholly_unknown_kind"`), &k)
	if err == nil {
		t.Fatalf("UnmarshalJSON must fail on unknown name; got %v", k)
	}
}
