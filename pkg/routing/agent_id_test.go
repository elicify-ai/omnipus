package routing

import (
	"strings"
	"testing"
)

// Traces to pkg/routing/agent_id.go::NormalizeAgentID doc comment: empty
// input now returns EMPTY, not the retired "main" sentinel. Callers that
// need a default must resolve it themselves (see resolveDefaultAgentID).
func TestNormalizeAgentID_Empty(t *testing.T) {
	if got := NormalizeAgentID(""); got != "" {
		t.Errorf("NormalizeAgentID('') = %q, want empty string", got)
	}
}

func TestNormalizeAgentID_Whitespace(t *testing.T) {
	if got := NormalizeAgentID("  "); got != "" {
		t.Errorf("NormalizeAgentID('  ') = %q, want empty string", got)
	}
}

func TestNormalizeAgentID_Valid(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"main", "main"},
		{"Main", "main"},
		{"SALES", "sales"},
		{"support-bot", "support-bot"},
		{"agent_1", "agent_1"},
		{"a", "a"},
		{"0test", "0test"},
	}
	for _, tt := range tests {
		if got := NormalizeAgentID(tt.input); got != tt.want {
			t.Errorf("NormalizeAgentID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeAgentID_InvalidChars(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Hello World", "hello-world"},
		{"agent@123", "agent-123"},
		{"foo.bar.baz", "foo-bar-baz"},
		{"--leading", "leading"},
		{"--both--", "both"},
	}
	for _, tt := range tests {
		if got := NormalizeAgentID(tt.input); got != tt.want {
			t.Errorf("NormalizeAgentID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeAgentID_AllInvalid(t *testing.T) {
	if got := NormalizeAgentID("@@@"); got != "" {
		t.Errorf("NormalizeAgentID('@@@') = %q, want empty string", got)
	}
}

func TestNormalizeAgentID_TruncatesAt64(t *testing.T) {
	var long strings.Builder
	for range 100 {
		long.WriteString("a")
	}
	got := NormalizeAgentID(long.String())
	if len(got) > MaxAgentIDLength {
		t.Errorf("length = %d, want <= %d", len(got), MaxAgentIDLength)
	}
}

func TestNormalizeAccountID_Empty(t *testing.T) {
	if got := NormalizeAccountID(""); got != DefaultAccountID {
		t.Errorf("NormalizeAccountID('') = %q, want %q", got, DefaultAccountID)
	}
}

func TestNormalizeAccountID_Valid(t *testing.T) {
	if got := NormalizeAccountID("MyBot"); got != "mybot" {
		t.Errorf("NormalizeAccountID('MyBot') = %q, want 'mybot'", got)
	}
}

func TestNormalizeAccountID_InvalidChars(t *testing.T) {
	if got := NormalizeAccountID("bot@home"); got != "bot-home" {
		t.Errorf("NormalizeAccountID('bot@home') = %q, want 'bot-home'", got)
	}
}

// TestNormalizeAgentID_EmptyNeverReturnsHardcodedName is an explicit
// anti-shortcut check for the sentinel removal: NormalizeAgentID("") must
// return the empty string and never "main" (the retired sentinel) or any
// other invented name. A hollowed-out implementation that quietly kept
// returning "main" would compile and look correct at a glance; this test
// asserts on the actual output, not just "it didn't panic". Traces to
// agent_id.go::NormalizeAgentID's doc comment ("there is no honest name to
// invent here").
func TestNormalizeAgentID_EmptyNeverReturnsHardcodedName(t *testing.T) {
	got := NormalizeAgentID("")
	if got == "main" {
		t.Fatalf("NormalizeAgentID('') = %q — the retired sentinel must never be reintroduced", got)
	}
	if got != "" {
		t.Fatalf("NormalizeAgentID('') = %q, want empty string (no name to invent)", got)
	}

	// Differentiation: a real agent ID and the empty ID must produce
	// different, non-hardcoded results — proves the function isn't
	// collapsing every input onto one constant.
	if realID := NormalizeAgentID("mia"); realID == got {
		t.Fatalf("NormalizeAgentID('mia') = %q, same as NormalizeAgentID('') = %q — expected different outputs", realID, got)
	}
}
