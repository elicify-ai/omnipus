package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestRun_RefusesNonPositiveMaxTurns: #904 FR-004 (no hidden default) and D4
// (external-CLI workers follow the resolved limit). A driver handed no turn cap
// must refuse the run with ErrMaxTurnsRequired — never substitute a built-in
// cap. The check fires before any process is spawned.
func TestRun_RefusesNonPositiveMaxTurns(t *testing.T) {
	drivers := map[string]ExternalAgentRunner{
		"claude":   NewClaudeDriver(nil),
		"codex":    NewCodexDriver(nil),
		"opencode": NewOpencodeDriver(nil),
	}
	for name, d := range drivers {
		for _, mt := range []int{0, -1} {
			ch, err := d.Run(context.Background(), RunOptions{Input: "task", MaxTurns: mt})
			if !errors.Is(err, ErrMaxTurnsRequired) {
				t.Errorf("%s: Run(MaxTurns=%d) err = %v, want ErrMaxTurnsRequired", name, mt, err)
			}
			if ch != nil {
				t.Errorf("%s: Run(MaxTurns=%d) returned a non-nil event channel", name, mt)
			}
		}
		// Resume reuses the prior Run's cap; with no prior Run there is none,
		// so it is refused the same way (no hidden default).
		if _, err := d.Resume(context.Background(), "run-no-prior"); !errors.Is(err, ErrMaxTurnsRequired) {
			t.Errorf("%s: Resume with no prior Run err = %v, want ErrMaxTurnsRequired", name, err)
		}
	}
}

// TestParseClaudeStreamJSON_EnforcesRunTurnCap: the turn cap comes from the
// run's MaxTurns (#904 D4), and the claude driver still stops a run past it
// (parseAssistantEvent). Three assistant turns under a cap of 2 must end in a
// fatal "turn cap exceeded" error naming max 2.
func TestParseClaudeStreamJSON_EnforcesRunTurnCap(t *testing.T) {
	turn := `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}` + "\n"
	events := ParseClaudeStreamJSON([]byte(turn+turn+turn), "run-cap", 2)

	var fatal []string
	for _, ev := range events {
		if ev.Kind == EventKindError && ev.Err != nil && ev.Err.Fatal {
			fatal = append(fatal, ev.Err.Message)
		}
	}
	if len(fatal) != 1 || fatal[0] != "turn cap exceeded: 3 turns (max 2)" {
		t.Fatalf("fatal errors = %q, want exactly [\"turn cap exceeded: 3 turns (max 2)\"]", fatal)
	}
}

// TestParseStreamJSON_RefusesNonPositiveMaxTurns: the offline parse helpers
// mirror Run — a missing cap yields one fatal error event wrapping the
// ErrMaxTurnsRequired text, never an uncapped parse (#904 FR-004).
func TestParseStreamJSON_RefusesNonPositiveMaxTurns(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}` + "\n")
	parsers := map[string]func([]byte, string, int) []RunEvent{
		"claude":   ParseClaudeStreamJSON,
		"codex":    ParseCodexStreamJSON,
		"opencode": ParseOpencodeStreamJSON,
	}
	for name, parse := range parsers {
		events := parse(line, "run-nocap", 0)
		if len(events) != 1 {
			t.Fatalf("%s: got %d events, want exactly 1 fatal error", name, len(events))
		}
		ev := events[0]
		if ev.Kind != EventKindError || ev.Err == nil || !ev.Err.Fatal ||
			!strings.Contains(ev.Err.Message, ErrMaxTurnsRequired.Error()) {
			t.Errorf("%s: event = %+v, want a fatal error carrying ErrMaxTurnsRequired", name, ev)
		}
	}
}
