package protocoltypes

import "testing"

// TestToolChoice_ResolveAbsentKey covers the overwhelmingly common case: no
// caller ever set the option. ok must be false and the zero value returned,
// with no panic and no special-casing required by callers.
func TestToolChoice_ResolveAbsentKey(t *testing.T) {
	tc, ok := ResolveToolChoice(map[string]any{}, "test-provider")
	if ok {
		t.Fatalf("ok = true for an absent key, want false (got %+v)", tc)
	}
	if tc != (ToolChoice{}) {
		t.Errorf("tc = %+v, want the zero value", tc)
	}

	tc, ok = ResolveToolChoice(nil, "test-provider")
	if ok {
		t.Fatalf("ok = true for a nil options map, want false (got %+v)", tc)
	}
	if tc != (ToolChoice{}) {
		t.Errorf("tc = %+v, want the zero value", tc)
	}
}

// TestToolChoice_ResolveValidModes covers the two recognized modes round-tripping.
func TestToolChoice_ResolveValidModes(t *testing.T) {
	cases := []struct {
		name string
		mode ToolChoiceMode
	}{
		{"auto", ToolChoiceAuto},
		{"required", ToolChoiceRequired},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			options := map[string]any{OptionKeyToolChoice: ToolChoice{Mode: tt.mode}}
			tc, ok := ResolveToolChoice(options, "test-provider")
			if !ok {
				t.Fatalf("ok = false for a valid ToolChoice{Mode: %q}, want true", tt.mode)
			}
			if tc.Mode != tt.mode {
				t.Errorf("Mode = %q, want %q", tc.Mode, tt.mode)
			}
		})
	}
}

// TestToolChoice_ResolveWrongGoType covers a caller putting the WRONG Go type
// under the well-known key (e.g. a leftover bare string from before this type
// existed). This must degrade to ok=false, never panic, and never be
// misread as a valid choice — the whole point of the typed key over a bare
// options["tool_choice"] string (ADR-081 D3 [G-B1]).
func TestToolChoice_ResolveWrongGoType(t *testing.T) {
	cases := []struct {
		name string
		raw  any
	}{
		{"bare string", "required"},
		{"bare bool", true},
		{"nil interface value present", nil},
		{"pointer to ToolChoice", &ToolChoice{Mode: ToolChoiceRequired}},
		{"unrelated struct", struct{ Mode string }{Mode: "required"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			options := map[string]any{OptionKeyToolChoice: tt.raw}
			tc, ok := ResolveToolChoice(options, "test-provider")
			if ok {
				t.Fatalf("ok = true for a %s under the key, want false (got %+v)", tt.name, tc)
			}
			if tc != (ToolChoice{}) {
				t.Errorf("tc = %+v, want the zero value on degrade", tc)
			}
		})
	}
}

// TestToolChoice_ResolveUnrecognizedMode covers a syntactically-valid
// ToolChoice value whose Mode is neither "auto" nor "required" — a typo, or
// a value from a future schema version this build does not know about yet.
func TestToolChoice_ResolveUnrecognizedMode(t *testing.T) {
	options := map[string]any{OptionKeyToolChoice: ToolChoice{Mode: "requried"}} // typo, deliberate
	tc, ok := ResolveToolChoice(options, "test-provider")
	if ok {
		t.Fatalf("ok = true for an unrecognized Mode, want false (got %+v)", tc)
	}
	if tc != (ToolChoice{}) {
		t.Errorf("tc = %+v, want the zero value on degrade", tc)
	}
}

// TestToolChoice_ResolveEmptyMode covers the zero-value ToolChoice{} itself
// (Mode == "") landing under the key — e.g. a caller that constructed the
// struct but forgot to set Mode. Must degrade, not be treated as auto.
func TestToolChoice_ResolveEmptyMode(t *testing.T) {
	options := map[string]any{OptionKeyToolChoice: ToolChoice{}}
	tc, ok := ResolveToolChoice(options, "test-provider")
	if ok {
		t.Fatalf("ok = true for ToolChoice{} (empty Mode), want false (got %+v)", tc)
	}
	if tc != (ToolChoice{}) {
		t.Errorf("tc = %+v, want the zero value on degrade", tc)
	}
}
