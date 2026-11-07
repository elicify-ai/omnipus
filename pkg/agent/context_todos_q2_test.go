package agent

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Expectations are the approved Q2 text and the explicit numbered-rule move,
// not prompt output copied from the implementation.
func TestContextBuilderSetTodosRuleAtThree(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	t.Cleanup(cb.Memory().Close)
	prompt := cb.BuildSystemPrompt()
	lines := q2OperatingRules(t, prompt)
	approved := q2ApprovedTodoRule(t)
	if len(lines) != 71 {
		t.Fatalf("default operating rule count = %d, want 71", len(lines))
	}
	if lines[1] != "2. Prompt text and skill assignment grant no tools, access, or delegation authority." {
		t.Errorf("authority rule changed: %q", lines[1])
	}
	if lines[2] != approved {
		t.Errorf("rule at position 3 = %q, want exact approved %q", lines[2], approved)
	}
	if lines[3] != "4. Perform actions with the appropriate tool." {
		t.Errorf("old rule3 was not shifted to4: %q", lines[3])
	}
	if lines[13] != "14. Use the memory guide to choose correct recall sources and appropriate saved-note audiences; if the guide is unavailable, do not invent its contents." {
		t.Errorf("conditional memory rule must be14: %q", lines[13])
	}
	if lines[18] != "19. Claim continuing background work only when a real job is running or a supported schedule exists." {
		t.Errorf("old rule18 was not shifted to19: %q", lines[18])
	}
	if lines[19] != "20. Use appropriate Markdown headings, bullets, numbered lists, or tables; keep short answers simple without unnecessary formatting." {
		t.Errorf("canonical rule20 changed: %q", lines[19])
	}
	if lines[70] != "71. Do not generalize a narrow passing test into universal readiness." {
		t.Errorf("canonical final rule changed: %q", lines[70])
	}
	q2AssertConsecutive(t, lines)
	if strings.Count(prompt, approved) != 1 {
		t.Errorf("approved checklist rule must occur exactly once")
	}
	if strings.Contains(prompt, "For every request, keep your own todo list with set_todos and mark the item you are working on.") {
		t.Error("superseded rule19 remains")
	}
}

func TestContextBuilderSetTodosRuleGating(t *testing.T) {
	for _, todos := range []bool{true, false} {
		for _, memory := range []bool{true, false} {
			t.Run(fmt.Sprintf("todos_%t_memory_%t", todos, memory), func(t *testing.T) {
				cb := NewContextBuilder(t.TempDir()).WithMemoryEnabled(memory)
				t.Cleanup(cb.Memory().Close)
				setter, ok := any(cb).(interface {
					WithSetTodosEnabled(enabled bool) *ContextBuilder
				})
				if !ok {
					t.Fatal("ContextBuilder must expose the construction-time set_todos flag gate")
				}
				if got := setter.WithSetTodosEnabled(todos); got != cb {
					t.Fatal("flag setter must retain the same builder")
				}
				for _, prompt := range []string{cb.getIdentity(), cb.getWorkspaceInfo()} {
					lines := q2OperatingRules(t, prompt)
					wantCount := 69
					if todos {
						wantCount++
					}
					if memory {
						wantCount++
					}
					if len(lines) != wantCount {
						t.Fatalf("rule count = %d, want %d", len(lines), wantCount)
					}
					q2AssertConsecutive(t, lines)
					approved := q2ApprovedTodoRule(t)
					if todos {
						if lines[2] != approved {
							t.Errorf("enabled rule3 = %q, want %q", lines[2], approved)
						}
					} else {
						if strings.Contains(prompt, "set_todos") {
							t.Error("disabled checklist guidance still names set_todos")
						}
						if lines[2] != "3. Perform actions with the appropriate tool." {
							t.Errorf("disabled rule leaves a gap: %q", lines[2])
						}
					}
					if strings.Contains(prompt, "<memory_guide>") != memory {
						t.Error("checklist gating changed memory-guide presence")
					}
					if !memory && strings.Contains(prompt, "Use the memory guide to choose correct recall sources") {
						t.Error("disabled memory rule remains")
					}
					wantLast := fmt.Sprintf("%d. Do not generalize a narrow passing test into universal readiness.", wantCount)
					if lines[len(lines)-1] != wantLast {
						t.Errorf("last rule = %q, want %q", lines[len(lines)-1], wantLast)
					}
				}
			})
		}
	}
}

func q2ApprovedTodoRule(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/todo_rule_q2.txt")
	if err != nil {
		t.Fatalf("read approved Q2 fixture: %v", err)
	}
	return strings.TrimSuffix(string(data), "\n")
}

func q2OperatingRules(t *testing.T, prompt string) []string {
	t.Helper()
	_, after, ok := strings.Cut(prompt, "<operating_rules>\n")
	if !ok {
		t.Fatal("operating-rules opening marker missing")
	}
	body, _, ok := strings.Cut(after, "\n</operating_rules>")
	if !ok {
		t.Fatal("operating-rules closing marker missing")
	}
	return regexp.MustCompile(`(?m)^\d+\. .+$`).FindAllString(body, -1)
}

func q2AssertConsecutive(t *testing.T, lines []string) {
	t.Helper()
	for i, line := range lines {
		if !strings.HasPrefix(line, fmt.Sprintf("%d. ", i+1)) {
			t.Errorf("operating rule position %d has wrong number: %q", i+1, line)
		}
	}
}
