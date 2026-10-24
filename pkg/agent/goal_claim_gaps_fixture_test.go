// #1431 gap fixtures. Only language-model provider replies are doubled;
// claim tools, Judge dispatch, stores and outcome application remain real.
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

type gap1431Chat struct {
	al    *AgentLoop
	agent *AgentInstance
	judge *AgentInstance
	store *session.UnifiedStore
	sid   string
	gid   string
	opts  processOptions
}

func newGap1431Chat(t *testing.T) *gap1431Chat {
	t.Helper()
	resetGoalTriggerStateForTest()
	al, judge := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agent, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent is not registered")
	}
	store, sid := newGoalTestSession(t, al, agent.ID)
	const condition = "report.md records the supplier's acceptance of the requested clause"
	gid := armGoalRecord(t, sid, condition, recordedGoalCriteria(condition), 0, time.Now().UTC())
	al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agent.ID)
	return &gap1431Chat{
		al: al, agent: agent, judge: judge, store: store, sid: sid, gid: gid,
		opts: processOptions{TranscriptStore: store, TranscriptSessionID: sid,
			Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true},
	}
}

func (g *gap1431Chat) emptyClaim(t *testing.T, id, answer string) *goalDeferredAdjudicationWork {
	t.Helper()
	claim, ok := g.agent.Tools.Get(tools.GoalClaimToolName)
	if !ok {
		t.Fatal("goal_claim is not registered on the real worker")
	}
	wiringClaimMetAndPersist(t, claim, g.store, g.sid, id, "")
	if err := g.store.AppendTranscriptStrict(g.sid, session.TranscriptEntry{
		ID: id + "-answer", Type: session.EntryTypeMessage, Role: "assistant",
		Content: answer, Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("persist worker answer: %v", err)
	}
	turn := &turnResult{finalContent: answer}
	g.al.checkGoalLoopAfterTurn(context.Background(), g.agent, g.opts, turn)
	if turn.goalDeferredAdjudication == nil {
		t.Fatal("ADR-084 section 13: a recorded empty-evidence met claim must schedule adjudication")
	}
	if turn.goalDeferredAdjudication.claimText != "" {
		t.Fatalf("ClaimText = %q, want the empty evidence, not the worker answer", turn.goalDeferredAdjudication.claimText)
	}
	return turn.goalDeferredAdjudication
}

// gap1431Judge records the actual provider requests and supplies independently
// scripted verdicts. beforeReply can observe durable state while each real
// adjudication is in progress, before that verdict is applied.
type gap1431Judge struct {
	mu          sync.Mutex
	requests    [][]providers.Message
	met         bool
	reason      string
	beforeReply func(int)
}

func (j *gap1431Judge) Chat(
	_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	j.mu.Lock()
	j.requests = append(j.requests, append([]providers.Message(nil), messages...))
	n := len(j.requests)
	j.mu.Unlock()
	if j.beforeReply != nil {
		j.beforeReply(n)
	}
	var prompt string
	for _, m := range messages {
		if m.Role == "user" && !m.Volatile {
			prompt = m.Content
		}
	}
	criteria := parseJudgeCriteriaBlock(prompt)
	if len(criteria) == 0 {
		return nil, fmt.Errorf("gap fixture: Judge request has no prose criteria")
	}
	items := make([]string, 0, len(criteria))
	for _, c := range criteria {
		items = append(items, fmt.Sprintf(`{"id":%q,"met":%t,"reason":%q}`, c.ID, j.met, j.reason))
	}
	return &providers.LLMResponse{Content: fmt.Sprintf(`{"met":%t,"criteria":[%s]}`, j.met, strings.Join(items, ","))}, nil
}

func (j *gap1431Judge) GetDefaultModel() string { return "fake-judge-model" }

func (j *gap1431Judge) snapshot() [][]providers.Message {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([][]providers.Message, len(j.requests))
	for i, messages := range j.requests {
		out[i] = append([]providers.Message(nil), messages...)
	}
	return out
}

// The canary is never in the criteria, claim, narration or Judge reply.
// Inspect the actual adjudication request, not a direct renderer invocation.
func requireGap1431ResultInJudgeRequest(t *testing.T, judge *gap1431Judge, decisive string) {
	t.Helper()
	requests := judge.snapshot()
	if len(requests) != 1 {
		t.Fatalf("Judge requests = %d, want exactly one upheld adjudication", len(requests))
	}
	var prompt string
	for _, m := range requests[0] {
		if m.Role == "user" && !m.Volatile && len(parseJudgeCriteriaBlock(m.Content)) > 0 {
			prompt = m.Content
		}
	}
	if !strings.Contains(prompt, decisive) {
		t.Errorf("ADR-084 section 13 MAJ-004: actual Judge request lost the result-only value %q:\n%s", decisive, prompt)
	}
	if !strings.Contains(prompt, "result=") {
		t.Errorf("actual Judge request lacks the tier-one result segment:\n%s", prompt)
	}
	if !strings.Contains(prompt, "(the worker reported no summary text)") {
		t.Errorf("actual Judge request replaced empty ClaimText with other text:\n%s", prompt)
	}
}

// A real task turn first reads a file, then submits present-empty evidence,
// then returns an answer distinct from the result-only canary in that file.
type gap1431ResultWorker struct {
	mu     sync.Mutex
	calls  int
	path   string
	answer string
}

func (w *gap1431ResultWorker) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	switch w.calls {
	case 1:
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
			ID: "gap1431-read", Type: "function", Name: "read_file",
			Arguments: map[string]any{"path": w.path},
		}}}, nil
	case 2:
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
			ID: "gap1431-claim", Type: "function", Name: tools.GoalClaimToolName,
			Arguments: map[string]any{"status": "met", "evidence": ""},
		}}}, nil
	case 3:
		return &providers.LLMResponse{Content: w.answer}, nil
	default:
		return nil, fmt.Errorf("gap fixture: unexpected worker provider call %d after its completed turn", w.calls)
	}
}

func (w *gap1431ResultWorker) GetDefaultModel() string { return "claiming-worker-model" }

func (w *gap1431ResultWorker) callCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}
