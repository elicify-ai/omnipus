// #1431 founder Q91: an empty-response retry must clean residual tool markup
// before accepting a task answer. Genuine retry prose must survive unchanged.
package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestTaskRun_EmptyResponseRetryRejectsMarkupAndKeepsRealAnswer(t *testing.T) {
	// The pure closing-tag residue is the exact Q91 input (also captured
	// verbatim in the existing orphan-markup UAT fixture). No structured tool
	// call or real answer accompanies the retry response.
	const residue = "</arg_key><arg_value>count.txt</arg_value></tool_call>"
	const realAnswer = "REPORT-Q91: count.txt contains exactly three lines."
	for _, tc := range []struct {
		name         string
		retryContent string
		wantStatus   task.Status
		wantResult   string
	}{
		{"markup_only_is_not_an_answer", residue, task.StatusFailed, "empty_answer: the task produced no result"},
		{"real_retry_answer_is_preserved", realAnswer, task.StatusDone, realAnswer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worker := &truncationScriptedProvider{steps: []truncationScriptStep{
				{finishReason: "tool_calls", toolCalls: []providers.ToolCall{{
					ID: "q91-claim", Type: "function", Name: tools.GoalClaimToolName,
					Arguments: map[string]any{"status": "met", "evidence": ""},
				}}},
				{content: "", finishReason: "stop"},
				{content: tc.retryContent, finishReason: "stop"},
			}}
			al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
			judge := &gap1431Judge{met: true, reason: "the recorded work satisfies the criterion"}
			judgeInst.Provider = judge
			events, stop := recordAgentEvents(t, al)
			tk := newRunLoopTask(t, al, nil)
			final := runTaskUntilTerminal(t, al, tk.ID, 1)
			al.taskExecutor.wg.Wait()
			stop()

			assertRound2RecordedEmptyClaim(t, al, final, "")
			if worker.CallCount() != 3 || len(judge.snapshot()) != 1 {
				t.Fatalf("worker/Judge calls=%d/%d, want claim + empty response + inner retry and one MET adjudication", worker.CallCount(), len(judge.snapshot()))
			}
			retries := 0
			events.mu.Lock()
			for _, event := range events.events {
				if event.Kind == EventKindEmptyResponseRetry {
					retries++
				}
			}
			events.mu.Unlock()
			if retries != 1 {
				t.Errorf("actual empty-response retry events=%d, want exactly one; a direct response is not the Q91 scenario", retries)
			}
			if final.Status != tc.wantStatus || final.Result != tc.wantResult {
				t.Errorf("Q91 retry result status/content=%q/%q, want %q/%q; residual markup is not a deliverable, real retry text must survive", final.Status, final.Result, tc.wantStatus, tc.wantResult)
			}
			if final.AttemptCount != 0 {
				t.Errorf("upheld retry claim spent %d failed task attempts, want zero", final.AttemptCount)
			}
			for _, tag := range []string{"<tool_call>", "</tool_call>", "<arg_key>", "</arg_key>", "<arg_value>", "</arg_value>"} {
				if strings.Contains(final.Result, tag) {
					t.Errorf("task result accepted tool-protocol residue %q: %q", tag, final.Result)
				}
			}
		})
	}
}
