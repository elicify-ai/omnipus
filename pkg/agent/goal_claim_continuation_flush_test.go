// #1431 CHECK M-a: pending model answer bytes must survive the continuation
// accumulator's flush BEFORE a tool round. Post-fix coverage commissioned by
// the chief; only model-provider replies are doubled, never the flush/collector.
package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestTaskRun_ContinuationIntoToolRoundPreservesFlushedAnswerExactlyOnce(t *testing.T) {
	// The expected bytes follow from these chosen fragments and the chief's
	// byte-preserving result/conversation contract, not an observed output.
	const prefix, continued, tail = `{"result":"hel`, `lo`, `"}`
	const want = `{"result":"hello"}`
	worker := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: prefix, finishReason: "length"},
		{content: continued, finishReason: "tool_calls", toolCalls: []providers.ToolCall{{
			ID: "flush-boundary-claim", Type: "function", Name: tools.GoalClaimToolName,
			Arguments: map[string]any{"status": "met", "evidence": ""},
		}}},
		{content: tail, finishReason: "stop"},
	}}
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judge := &gap1431Judge{met: true, reason: "the recorded work satisfies the criterion"}
	judgeInst.Provider = judge
	tk := newRunLoopTask(t, al, nil)
	final := runTaskUntilTerminal(t, al, tk.ID, 1)
	al.taskExecutor.wg.Wait()

	assertRound2RecordedEmptyClaim(t, al, final, "")
	if worker.CallCount() != 3 || len(judge.snapshot()) != 1 {
		t.Fatalf("worker/Judge calls=%d/%d, want truncated answer + continuation/tool round + final reply and one MET adjudication", worker.CallCount(), len(judge.snapshot()))
	}
	continuationInstructionSeen := false
	for _, message := range worker.Requests()[1] {
		if message.Role == "user" && message.Content == truncationContinueMessage().Content {
			continuationInstructionSeen = true
		}
	}
	if !continuationInstructionSeen {
		t.Fatal("second provider request lacks the actual automatic-continuation instruction; the pending-continuation tool boundary was not exercised")
	}
	if final.Status != task.StatusDone || final.AttemptCount != 0 {
		t.Errorf("upheld flushed answer status/attempts=%q/%d, want Done/0", final.Status, final.AttemptCount)
	}
	if final.Result != want {
		t.Errorf("task result=%q, want exact flushed+continued answer %q; pending model bytes must not be lost at the tool-round boundary", final.Result, want)
	}
	for _, part := range []string{prefix, continued, tail, want} {
		if count := strings.Count(final.Result, part); count != 1 {
			t.Errorf("task result contains answer bytes %q %d times, want exactly once: %q", part, count, final.Result)
		}
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(final.Result), &decoded); err != nil {
		t.Errorf("flushed task result is not intact JSON: %v (result=%q)", err, final.Result)
	} else if !reflect.DeepEqual(decoded, map[string]string{"result": "hello"}) {
		t.Errorf("flushed JSON=%v, want exactly result=hello", decoded)
	}

	// Read the real persisted conversation independently of task answer
	// assembly. The pending prefix must be on disk BEFORE the claim's tool
	// entry; this is the transition the prior final-text continuation missed.
	entries, err := al.GetSessionStore().ReadTranscript(final.SessionID)
	if err != nil {
		t.Fatalf("read flushed conversation: %v", err)
	}
	prefixEntry, claimEntry := -1, -1
	for i, entry := range entries {
		if entry.Role == "assistant" && entry.Content == prefix {
			prefixEntry = i
		}
		for _, call := range entry.ToolCalls {
			if call.Tool == tools.GoalClaimToolName && call.Status == "success" {
				claimEntry = i
			}
		}
	}
	if prefixEntry < 0 || claimEntry <= prefixEntry {
		t.Errorf("persisted pending prefix/claim positions=%d/%d, want prefix flushed before the successful continuation tool call", prefixEntry, claimEntry)
	}
	conversation := strings.Join(round4PersistedAssistantAnswers(t, al, final.SessionID), "")
	if conversation != want {
		t.Errorf("persisted conversation bytes=%q, want exactly %q once in order", conversation, want)
	}
	if final.Result != conversation {
		t.Errorf("task result=%q differs from persisted conversation=%q after continuation-to-tool flush", final.Result, conversation)
	}
	round4RejectFrameworkAnswer(t, final.Result)
}
