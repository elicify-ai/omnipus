package agent

// PR #1311 ROUND 4 G10. One REAL parent turn polls one actual child through
// the registered delegate tool. A provider script alternates status/peek/inbox
// without new information; the existing eight-call stop must not be evaded by
// changing the read action. Fresh progress is a distinct control, not a timer.
// CHECK: split the stale count by action; count timestamp-only differences as
// progress; stop a poll that really sees new information.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

type r4PollProvider struct {
	script *testutil.ScenarioProvider
	before func(int) error
}

func (p *r4PollProvider) Chat(ctx context.Context, messages []providers.Message, definitions []providers.ToolDefinition,
	model string, options map[string]any) (*providers.LLMResponse, error) {
	if p.before != nil {
		if err := p.before(p.script.CallCount() + 1); err != nil {
			return nil, err
		}
	}
	return p.script.Chat(ctx, messages, definitions, model, options)
}

func (p *r4PollProvider) GetDefaultModel() string { return p.script.GetDefaultModel() }

func r4FreezeDelegateClock(t *testing.T, h *r4DelegateHarness) {
	t.Helper()
	tool, found := h.agent.Tools.Get("delegate")
	if !found {
		t.Fatal("SETUP delegate must be registered")
	}
	delegate, typed := tool.(*tools.DelegateTool)
	if !typed {
		t.Fatalf("SETUP registered delegate type=%T, want *tools.DelegateTool", tool)
	}
	// The clock is ONLY a noise control. Nothing asserts elapsed wall time.
	fixed := time.Now().UTC()
	delegate.SetClock(func() time.Time { return fixed })
}

func r4AppendFreshProgress(h *r4DelegateHarness, child steer.LaunchResult, step int) error {
	var message generated.SessionMessage
	parent := h.parentID
	if err := message.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId: fmt.Sprintf("r4-progress-%d", step), SessionId: child.SessionID, ParentSessionId: &parent,
		Generation: &child.Generation, Depth: 1, CreatedAt: time.Now().UTC(), SenderIdentity: h.agent.ID,
		Direction: generated.SessionMessageProgressDirectionChildToParent, UntrustedOrigin: true,
		Text: fmt.Sprintf("NEW-INFORMATION-R4: completed analysis section %d", step),
	}); err != nil {
		return fmt.Errorf("encode genuine new progress: %w", err)
	}
	if _, err := h.al.GetMessageInboxStore().Append(h.parentID, message); err != nil {
		return fmt.Errorf("append genuine new progress: %w", err)
	}
	return nil
}

func r4RunParentPolls(t *testing.T, actions []string, newInformation bool) (string, *testutil.ScenarioProvider) {
	t.Helper()
	p := &r4PollProvider{script: testutil.NewScenario()}
	h := newR4DelegateHarness(t, p)
	child := h.launch(t, false)
	r4FreezeDelegateClock(t, h)
	for i, action := range actions {
		args := fmt.Sprintf(`{"action":%q,"session_id":%q}`, action, child.SessionID)
		call := providers.FunctionCall{Name: "delegate", Arguments: args}
		p.script.WithToolCalls([]providers.ToolCall{{ID: fmt.Sprintf("r4-read-%d", i+1), Function: &call}})
	}
	p.script.WithText("SCRIPT-FINISHED-R4: polling is complete.")
	if newInformation {
		p.before = func(step int) error {
			if step <= len(actions) {
				return r4AppendFreshProgress(h, child, step)
			}
			return nil
		}
	}
	// The loop's real task/direct entry pins this pre-existing parent session;
	// all reads therefore pass actual child ownership and schema validation.
	final, err := h.al.processTaskDirect(context.Background(), h.agent.ID,
		"Inspect this helper and act on its new information; do not poll unchanged state indefinitely.",
		"r4-parent-poll", h.parentID)
	if err != nil {
		t.Fatalf("SETUP real parent turn refused: %v", err)
	}
	return final, p.script
}

func r4RequireRepeatStop(t *testing.T, final string, script *testutil.ScenarioProvider, why string) {
	t.Helper()
	// ROUND 4 G10 explicitly reuses the eight-call guard. Keep that count
	// independent of the implementation constants so a weakened bound fails.
	if script.CallCount() != 8 {
		t.Errorf("G10 %s: provider rounds=%d, want 8 (no further model call after eight unchanged observations)", why, script.CallCount())
	}
	if !strings.HasPrefix(final, "I stopped this turn:") || !strings.Contains(final, "delegate") {
		t.Errorf("G10 %s: existing repeated-call stop response did not trigger: final=%q", why, final)
	}
}

func TestR4DelegatePolling_AlternatingUnchangedReadsCannotEvadeTheRepeatStop(t *testing.T) {
	t.Run("status_peek_inbox_same_child_no_new_information", func(t *testing.T) {
		// Three full eight-call budgets give the broken guard every chance to
		// trip, yet stay below the fixture's 64-round iteration limit.
		actions := make([]string, 24)
		verbs := []string{"status", "peek", "inbox"}
		for i := range actions {
			actions[i] = verbs[i%len(verbs)]
		}
		final, script := r4RunParentPolls(t, actions, false)
		r4RequireRepeatStop(t, final, script, "alternating read actions")
	})
	t.Run("eight_identical_status_reads_still_stop", func(t *testing.T) {
		actions := make([]string, 12) // more than eight, but below the iteration cap
		for i := range actions {
			actions[i] = "status"
		}
		final, script := r4RunParentPolls(t, actions, false)
		r4RequireRepeatStop(t, final, script, "identical status control")
	})
	t.Run("new_information_does_not_trigger_the_guard", func(t *testing.T) {
		actions := make([]string, 12)
		for i := range actions {
			actions[i] = "status"
		}
		final, script := r4RunParentPolls(t, actions, true)
		if final != "SCRIPT-FINISHED-R4: polling is complete." || script.CallCount() != len(actions)+1 {
			t.Errorf("G10 fresh-information control was stopped despite real new progress: final=%q calls=%d, want normal completion after twelve distinct updates", final, script.CallCount())
		}
		seen := make(map[string]bool)
		for _, request := range script.AllRequests() {
			for _, message := range request {
				if message.Role == "tool" && strings.Contains(message.Content, "NEW-INFORMATION-R4") {
					seen[message.Content] = true
				}
			}
		}
		if len(seen) < len(actions) {
			t.Errorf("G10 fresh-information control saw only %d distinct real delegate results, want %d", len(seen), len(actions))
		}
	})
}
