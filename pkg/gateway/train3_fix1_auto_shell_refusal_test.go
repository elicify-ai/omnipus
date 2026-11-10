package gateway

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gate round 1, silent-failure SF-03: with Auto on, a task run's bash call
// whose own escalation (network / filesystem pre-flight, or the no-kernel-
// sandbox gate) needs approval is refused inside the shell tool. That refusal
// must be the same task/plan refusal as every other one (amendment R4): the
// fixed permanent model message, the fixed transcript row and the task-run
// audit reason — not the cron-flavoured "headless/scheduled run" text.
func TestTrain3Fix1_AutoShellEscalationInATaskIsTheTaskPlanRefusal(t *testing.T) {
	call := l11263Call("approval-shell", "bash", map[string]any{"command": "curl -s https://example.com/train3-fix1"})
	worker := l11263Script(call, l11263Claim())
	h := newL11263Harness(t, true, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker})
	created := h.createTask(t, "")
	h.startBoard(t, created)
	final := h.awaitTask(t, created.Id)
	h.requireTaskOrigin(t, final.SessionID)

	result, ok := worker.result("approval-shell")
	require.True(t, ok, "the worker receives an immediate result")
	var denial map[string]any
	require.NoError(t, json.Unmarshal([]byte(result), &denial), "a structured refusal, not free text: %s", result)
	assert.Equal(t, map[string]any{
		"error": "permission_denied", "tool": "bash", "reason": l11263Reason,
		"permanent": true, "message": l11263Model,
	}, denial)

	entries, err := h.api.agentLoop.GetSessionStore().ReadTranscript(final.SessionID)
	require.NoError(t, err)
	var calls []session.ToolCall
	for _, entry := range entries {
		for _, c := range entry.ToolCalls {
			if string(c.ID) == "approval-shell" {
				calls = append(calls, c)
			}
		}
	}
	require.Len(t, calls, 1, "one settled visible row")
	assert.Equal(t, "denied", calls[0].Status)
	assert.Equal(t, map[string]any{"error": true, "text": l11263Transcript, "reason": l11263Reason, "permanent": true}, calls[0].Result)
	_, known := agent.ClassifyDenial(l11263Reason)
	require.True(t, known)
	require.Equal(t, []string{final.SessionID}, l1AskDeniedSessionIDs(t, h, "bash"),
		"the canonical task-run ask.denied audit row is written for the acting session")
}
