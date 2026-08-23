package providers

import "testing"

func TestFindMatchingBrace(t *testing.T) {
	tests := []struct {
		text string
		pos  int
		want int
	}{
		{`{"a":1}`, 0, 7},
		{`{"a":{"b":2}}`, 0, 13},
		{`text {"a":1} more`, 5, 12},
		{`{unclosed`, 0, 0},      // no match returns pos
		{`{}`, 0, 2},             // empty object
		{`{{{}}}`, 0, 6},         // deeply nested
		{`{"a":"b{c}d"}`, 0, 13}, // braces in strings (simplified matcher)
	}
	for _, tt := range tests {
		got := findMatchingBrace(tt.text, tt.pos)
		if got != tt.want {
			t.Errorf("findMatchingBrace(%q, %d) = %d, want %d", tt.text, tt.pos, got, tt.want)
		}
	}
}

func TestExtractToolCallsFromText(t *testing.T) {
	t.Run("no tool calls", func(t *testing.T) {
		if got := extractToolCallsFromText("plain text"); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("unmatched brace", func(t *testing.T) {
		if got := extractToolCallsFromText(`{"tool_calls": [`); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("two calls with arguments", func(t *testing.T) {
		text := `prefix {"tool_calls":[` +
			`{"id":"1","type":"function","function":{"name":"a","arguments":"{\"x\":1}"}},` +
			`{"id":"2","type":"function","function":{"name":"b","arguments":"{}"}}]} suffix`
		got := extractToolCallsFromText(text)
		if len(got) != 2 {
			t.Fatalf("got %d tool calls, want 2", len(got))
		}
		if got[0].Name != "a" || got[1].Name != "b" {
			t.Errorf("names = %q, %q", got[0].Name, got[1].Name)
		}
		if got[0].Arguments["x"] != float64(1) {
			t.Errorf("arguments[x] = %v, want 1", got[0].Arguments["x"])
		}
	})
}
