// #1431 ROUND 4, chief T4/T5: automatic continuation preserves exact bytes;
// DISTINCT assistant responses remain ordered, once each, without duplicates.
// Only model-provider replies are doubled. Task, claim, continuation, stores,
// Judge dispatch and completion application remain real.
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

func TestTaskRun_UpheldClaimContinuationPreservesExactAnswerBytes(t *testing.T) {
	// Exact fragments and oracle are specified in the chief's T4 brief;
	// the split is inside a JSON string, where invented newlines are invalid.
	const prefix, suffix = `{"result":"hel`, `lo"}`
	const want = `{"result":"hello"}`
	worker := &truncationScriptedProvider{steps: []truncationScriptStep{
		{finishReason: "tool_calls", toolCalls: []providers.ToolCall{{
			ID: "round4-claim", Type: "function", Name: tools.GoalClaimToolName,
			Arguments: map[string]any{"status": "met", "evidence": ""},
		}}},
		{content: prefix, finishReason: "length"},
		{content: suffix, finishReason: "stop"},
	}}
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judge := &gap1431Judge{met: true, reason: "the recorded work satisfies the criterion"}
	judgeInst.Provider = judge
	tk := newRunLoopTask(t, al, nil)
	final := runTaskUntilTerminal(t, al, tk.ID, 1)
	al.taskExecutor.wg.Wait()

	assertRound2RecordedEmptyClaim(t, al, final, "")
	if worker.CallCount() != 3 || len(judge.snapshot()) != 1 {
		t.Fatalf("worker/Judge calls=%d/%d, want claim + truncated response + automatic continuation and one MET adjudication", worker.CallCount(), len(judge.snapshot()))
	}
	continued := false
	for _, message := range worker.Requests()[2] {
		if message.Role == "user" && message.Content == truncationContinueMessage().Content {
			continued = true
		}
	}
	if !continued {
		t.Fatal("third provider request lacks the actual automatic-continuation instruction; cannot prove a continuation-boundary regression")
	}
	if final.Status != task.StatusDone || final.AttemptCount != 0 {
		t.Errorf("upheld continued answer status/attempts=%q/%d, want Done/0", final.Status, final.AttemptCount)
	}
	if final.Result != want {
		t.Errorf("task result=%q, want EXACT byte-concatenated answer %q, with no invented continuation separator", final.Result, want)
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(final.Result), &decoded); err != nil {
		t.Errorf("task reported Done with a corrupted JSON answer: %v (result=%q)", err, final.Result)
	} else if !reflect.DeepEqual(decoded, map[string]string{"result": "hello"}) {
		t.Errorf("JSON result=%v, want exactly result=hello", decoded)
	}
	conversation := round4PersistedAssistantAnswers(t, al, final.SessionID)
	if !reflect.DeepEqual(conversation, []string{want}) {
		t.Errorf("persisted conversation answers=%q, want exactly one logical answer %q", conversation, want)
	}
	if len(conversation) == 1 && final.Result != conversation[0] {
		t.Errorf("task result=%q differs from persisted conversation=%q", final.Result, conversation[0])
	}
	round4RejectFrameworkAnswer(t, final.Result)
}

func TestTaskRun_UpheldClaimPreservesTwoDistinctAnswerPartsOnceInOrder(t *testing.T) {
	const sectionA = "SECTION-A-1431: supplier Alpha accepted the revised delivery date."
	const sectionB = "SECTION-B-1431: supplier Beta accepted the revised delivery date."
	worker := &round2ClaimWorker{firstAnswer: sectionA, finalAnswer: sectionB}
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judge := &gap1431Judge{met: true, reason: "the recorded work satisfies the criterion"}
	judgeInst.Provider = judge
	tk := newRunLoopTask(t, al, nil)
	final := runTaskUntilTerminal(t, al, tk.ID, 1)
	al.taskExecutor.wg.Wait()

	assertRound2RecordedEmptyClaim(t, al, final, sectionA)
	if worker.callCount() != 2 || len(judge.snapshot()) != 1 {
		t.Fatalf("worker/Judge calls=%d/%d, want exactly two distinct model replies and one MET adjudication", worker.callCount(), len(judge.snapshot()))
	}
	if final.Status != task.StatusDone || final.AttemptCount != 0 {
		t.Errorf("upheld two-part answer status/attempts=%q/%d, want Done/0", final.Status, final.AttemptCount)
	}
	// T5 deliberately does NOT pin incidental separator formatting between
	// distinct responses. Require complete, unaltered parts exactly once.
	for _, part := range []string{sectionA, sectionB} {
		if count := strings.Count(final.Result, part); count != 1 {
			t.Errorf("task result contains section %q %d times, want exactly once: %q", part, count, final.Result)
		}
	}
	if a, b := strings.Index(final.Result, sectionA), strings.Index(final.Result, sectionB); a < 0 || b <= a {
		t.Errorf("task result lost or reordered distinct sections: A index=%d, B index=%d, result=%q", a, b, final.Result)
	}
	conversation := round4PersistedAssistantAnswers(t, al, final.SessionID)
	if !reflect.DeepEqual(conversation, []string{sectionA, sectionB}) {
		t.Errorf("persisted assistant messages=%q, want [section A, section B], each once and no combined duplicate entry", conversation)
	}
	for _, message := range conversation {
		if strings.Contains(message, sectionA) && strings.Contains(message, sectionB) {
			t.Errorf("combined duplicate assistant entry was persisted: %q", message)
		}
	}
	round4RejectFrameworkAnswer(t, final.Result)
}

func round4PersistedAssistantAnswers(t *testing.T, al *AgentLoop, sid string) []string {
	t.Helper()
	entries, err := al.GetSessionStore().ReadTranscript(sid)
	if err != nil {
		t.Fatalf("read persisted conversation: %v", err)
	}
	var answers []string
	for _, entry := range entries {
		// A continuation instruction or framework notice must not have
		// acquired any durable conversation role, not merely assistant.
		round4RejectFrameworkAnswer(t, entry.Content)
		if entry.Role == "assistant" && strings.TrimSpace(entry.Content) != "" {
			answers = append(answers, entry.Content)
		}
	}
	return answers
}

func round4RejectFrameworkAnswer(t *testing.T, text string) {
	t.Helper()
	for _, notice := range []string{defaultResponse, toolLimitResponse, truncationContinueMessage().Content} {
		if strings.Contains(text, notice) {
			t.Errorf("framework/continuation notice was persisted as deliverable text: %q", text)
		}
	}
}
