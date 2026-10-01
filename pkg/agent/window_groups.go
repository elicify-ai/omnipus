package agent

import (
	"fmt"
	"reflect"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// windowStep owns ids only inside one assistant message, never session-wide.
type windowStep struct {
	start, end int
	results    []int
	complete   bool
}

func windowSteps(msgs []providers.Message) []windowStep {
	var out []windowStep
	for i, m := range msgs {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		s := windowStep{start: i, end: i + 1, complete: true}
		want := make(map[string]bool, len(m.ToolCalls))
		for _, c := range m.ToolCalls {
			if c.ID == "" {
				s.complete = false
			}
			if _, exists := want[c.ID]; exists {
				s.complete = false
			}
			want[c.ID] = false
		}
		for j := i + 1; j < len(msgs) && msgs[j].Role == "tool"; j++ {
			id := msgs[j].ToolCallID
			seen, declared := want[id]
			if !declared || seen {
				s.complete = false
			}
			want[id] = true
			s.results = append(s.results, j)
			s.end = j + 1
		}
		for _, seen := range want {
			if !seen {
				s.complete = false
			}
		}
		out = append(out, s)
	}
	return out
}

// validateWindowGroups runs BEFORE repair/normalization and final serialization.
// A partial group is a real invalid request, not permission to erase the group.
func validateWindowGroups(msgs []providers.Message) error {
	owned := make(map[int]bool)
	for _, s := range windowSteps(msgs) {
		if !s.complete {
			return fmt.Errorf("context request: incomplete or invalid tool-result group at message %d", s.start)
		}
		for _, i := range s.results {
			owned[i] = true
		}
	}
	for i, m := range msgs {
		if m.Role == "tool" && !owned[i] {
			return fmt.Errorf("context request: orphan tool result at message %d", i)
		}
	}
	return nil
}

func sameArchiveIdentity(a, b providers.Message) bool {
	if a.Role != b.Role || a.ToolCallID != b.ToolCallID {
		return false
	}
	if a.Role == "tool" {
		return a.ToolCallID != ""
	}
	if a.Content != b.Content || a.ReasoningContent != b.ReasoningContent || len(a.ToolCalls) != len(b.ToolCalls) {
		return false
	}
	for i := range a.ToolCalls {
		if a.ToolCalls[i].ID != b.ToolCalls[i].ID {
			return false
		}
	}
	return true
}

// Align in archive order. Explicit indices include the anchor before the suffix;
// tool ids are compared only at the next owned slot, never searched globally.
func mapWindowMessages(snap memory.WindowSnapshot, msgs []providers.Message, recallAt, recallLen int) []int {
	history, sourceLines := memory.WindowHistory(snap)
	lines := make([]int, len(msgs))
	next := 0
	for i, m := range msgs {
		lines[i] = -1
		if i >= recallAt && i < recallAt+recallLen {
			continue
		}
		if next < len(history) && sameArchiveIdentity(m, history[next]) {
			lines[i] = sourceLines[next]
			next++
		}
	}
	return lines
}

func recallBlockPosition(ts *turnState, msgs []providers.Message) (int, int) {
	ts.mu.RLock()
	span := ts.injectedRecallSpan
	ts.mu.RUnlock()
	if span == nil {
		return -1, 0
	}
	block := span.Messages()
	if len(block) == 0 {
		return -1, 0
	}
	for i := 0; i+len(block) <= len(msgs); i++ {
		if reflect.DeepEqual(msgs[i:i+len(block)], block) {
			return i, len(block)
		}
	}
	return -1, 0
}

func (ts *turnState) protectWindowControls(msgs []providers.Message) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.windowControls = append(ts.windowControls, msgs...)
}

func (ts *turnState) clearWindowControls() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.windowControls = nil
}

func (ts *turnState) protectedWindowMessage(m providers.Message) bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	for _, control := range ts.windowControls {
		if sameArchiveIdentity(control, m) {
			return true
		}
	}
	return false
}
