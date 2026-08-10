package protocoltypes

import "testing"

// TestSafeInvoke_PanicIsContained is the unit-level half of ADR-059 AC-06.
// The production-caller half lives in pkg/providers/openai_compat and
// pkg/providers/anthropic, where a panicking handler is driven through a real
// SSE stream — a green test here alone would not satisfy the ADR §8 bar.
func TestSafeInvoke_PanicIsContained(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SafeInvoke let a handler panic escape: %v", r)
		}
	}()
	SafeInvoke(func(ToolCallProgress) { panic("handler exploded") },
		ToolCallProgress{Name: "write_file", ArgsBytes: 7})
}

func TestSafeInvoke_NilIsNoOp(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SafeInvoke(nil) panicked: %v", r)
		}
	}()
	SafeInvoke(nil, ToolCallProgress{})
}

func TestSafeInvoke_ForwardsPayload(t *testing.T) {
	var got ToolCallProgress
	SafeInvoke(func(p ToolCallProgress) { got = p }, ToolCallProgress{
		Index: 3, Name: "bash", ArgsBytes: 11, TotalArgsBytes: 40,
	})
	if got.Index != 3 || got.Name != "bash" || got.ArgsBytes != 11 || got.TotalArgsBytes != 40 {
		t.Errorf("payload mangled in transit: %+v", got)
	}
}

// TestSafeInvoke_SurvivesRepeatedPanics guards the case that actually matters:
// the handler is called once per argument delta, so a consumer that panics
// panics on EVERY delta. The recover must be per-call, not one-shot.
func TestSafeInvoke_SurvivesRepeatedPanics(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a later SafeInvoke let a panic escape: %v", r)
		}
	}()
	for i := 0; i < 50; i++ {
		SafeInvoke(func(ToolCallProgress) { panic("again") }, ToolCallProgress{ArgsBytes: i})
	}
}
