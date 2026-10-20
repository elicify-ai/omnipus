package gateway

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gate round 1 SF-03 follow-up (qa-lead gap round, brief train3-red-gaps-r1):
// cd75aa920 proved the D8 NETWORK pre-flight escalation inside a task is the
// canonical task/plan refusal. The other two ADR-092 Auto escalation call
// sites a task run can reach were untested:
//   - the no-kernel-sandbox gate (founder decision A, 2026-09-24), and
//   - an operator D3 {action: ask} command rule (ADR-092 D3).
//
// Both must produce the SAME canonical trio as
// TestTrain3Fix1_AutoShellEscalationInATaskIsTheTaskPlanRefusal (amendment
// §1.2 R4): the fixed permanent model message, the one settled denied
// transcript row, and the tool.policy.ask.denied audit row naming the ACTING
// session — never the cron-flavoured headless/scheduled text.
//
// The no-sandbox probe uses "touch notes/x": a relative-path write outside
// the read-only allowlist, with no absolute path for D7 and no network need
// for D8. The RULE probe instead uses "echo rule-ask-control": read-only and
// safe to execute without the operator rule, so deleting the rule branch
// cannot pass by falling through to an indistinguishable no-sandbox refusal
// (round-3 CHECK F4). Auto's loop-level rule pin never pre-settles the ask;
// the shell tool's own rule branch must supply the canonical task refusal.
// The l11263 harness never runs a real sandbox apply, so the no-sandbox gate
// remains active; the rule probe deliberately clears it.

// requireCanonicalTaskPlanBashRefusal asserts the full canonical task/plan
// refusal for one refused bash call: the structured model-visible denial, the
// settled transcript row, the denial classification, and the audit row. The
// expected values are the amendment R4 literals (l11263Reason/l11263Model/
// l11263Transcript), never read off the implementation.
func requireCanonicalTaskPlanBashRefusal(t *testing.T, h *l11263Harness, worker *l11263Provider, callID, sessionID string) {
	t.Helper()
	result, ok := worker.result(callID)
	require.True(t, ok, "the worker receives an immediate result")
	var denial map[string]any
	require.NoError(t, json.Unmarshal([]byte(result), &denial), "a structured refusal, not free text: %s", result)
	assert.Equal(t, map[string]any{
		"error": "permission_denied", "tool": "bash", "reason": l11263Reason,
		"permanent": true, "message": l11263Model,
	}, denial)

	entries, err := h.api.agentLoop.GetSessionStore().ReadTranscript(sessionID)
	require.NoError(t, err)
	var calls []session.ToolCall
	for _, entry := range entries {
		for _, c := range entry.ToolCalls {
			if string(c.ID) == callID {
				calls = append(calls, c)
			}
		}
	}
	require.Len(t, calls, 1, "one settled visible row")
	assert.Equal(t, "denied", calls[0].Status)
	assert.Equal(t, map[string]any{"error": true, "text": l11263Transcript, "reason": l11263Reason, "permanent": true}, calls[0].Result)
	_, known := agent.ClassifyDenial(l11263Reason)
	require.True(t, known)
	require.Equal(t, []string{sessionID}, l1AskDeniedSessionIDs(t, h, "bash"),
		"the canonical task-run ask.denied audit row is written for the acting session")
}

// TestTrain3Fix1_AutoShellNoSandboxEscalationInATaskIsTheTaskPlanRefusal
// drives the no-kernel-sandbox gate's task-run denial: Auto mode, no kernel
// base registered, a command neither read-only nor pre-flight-visible asks
// through requestNoSandboxApproval — and a task run has nobody to answer, so
// the refusal is the canonical task/plan refusal.
func TestTrain3Fix1_AutoShellNoSandboxEscalationInATaskIsTheTaskPlanRefusal(t *testing.T) {
	call := l11263Call("nosandbox-shell", "bash", map[string]any{"command": "touch notes/x"})
	worker := l11263Script(call, l11263Claim())
	h := newL11263Harness(t, true, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker})
	created := h.createTask(t, "")
	h.startBoard(t, created)
	final := h.awaitTask(t, created.Id)
	h.requireTaskOrigin(t, final.SessionID)
	requireCanonicalTaskPlanBashRefusal(t, h, worker, "nosandbox-shell", final.SessionID)
}

// TestTrain3Fix1_AutoShellRuleAskEscalationInATaskIsTheTaskPlanRefusal drives
// the operator D3 ask-rule denial: an {action: ask} rule matching the command
// escalates through requestRuleApproval under Auto, and inside a task that
// ask is auto-denied with the same canonical task/plan refusal.
func TestTrain3Fix1_AutoShellRuleAskEscalationInATaskIsTheTaskPlanRefusal(t *testing.T) {
	call := l11263Call("ruleask-shell", "bash", map[string]any{"command": "echo rule-ask-control"})
	worker := l11263Script(call, l11263Claim())
	h := newL11263Harness(t, true, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker},
		func(cfg *config.Config) {
			cfg.Sandbox.CommandRules = []shellrule.Rule{{Binary: "echo", Action: shellrule.ActionAsk}}
		})
	created := h.createTask(t, "")
	h.startBoard(t, created)
	final := h.awaitTask(t, created.Id)
	h.requireTaskOrigin(t, final.SessionID)
	requireCanonicalTaskPlanBashRefusal(t, h, worker, "ruleask-shell", final.SessionID)
}
