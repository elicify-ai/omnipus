// #1431 round-2 provider-edge fixtures. The real task turn, claim registry,
// transcript, Judge pipeline and completion writer are not mocked.
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Unlike claimingWorker, this fixture deliberately preserves a truly empty
// final provider reply. firstAnswer is emitted ALONGSIDE the real tool call.
type round2ClaimWorker struct {
	mu          sync.Mutex
	calls       int
	firstAnswer string
	finalAnswer string
}

func (w *round2ClaimWorker) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	switch w.calls {
	case 1:
		return &providers.LLMResponse{Content: w.firstAnswer, ToolCalls: []providers.ToolCall{{
			ID: "round2-real-claim", Type: "function", Name: tools.GoalClaimToolName,
			Arguments: map[string]any{"status": "met", "evidence": ""},
		}}}, nil
	case 2, 3:
		// The engine retries an empty/whitespace response once. Preserve
		// that provider result rather than turning recovery into a fixture error.
		return &providers.LLMResponse{Content: w.finalAnswer}, nil
	default:
		return nil, fmt.Errorf("round-2 fixture: unexpected provider call %d after the completed claiming turn", w.calls)
	}
}

func (w *round2ClaimWorker) GetDefaultModel() string { return "claiming-worker-model" }

func (w *round2ClaimWorker) callCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

// Positive controls prove a real successful empty-evidence claim and a real
// upheld adjudication, so a default failure cannot make the negative tests pass.
func assertRound2RecordedEmptyClaim(t *testing.T, al *AgentLoop, final *task.Task, wantEarlierAnswer string) {
	t.Helper()
	entries, err := al.GetSessionStore().ReadTranscript(final.SessionID)
	if err != nil {
		t.Fatalf("read claiming turn: %v", err)
	}
	claims, verdicts, earlierAnswers := 0, 0, 0
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Content == wantEarlierAnswer && wantEarlierAnswer != "" {
			earlierAnswers++
		}
		if entry.Type == session.EntryTypeJudgeVerdict {
			verdicts++
		}
		for _, call := range entry.ToolCalls {
			if call.Tool == tools.GoalClaimToolName && call.Status == "success" {
				claims++
			}
		}
	}
	if claims != 1 || verdicts != 1 {
		t.Errorf("successful goal_claim/Judge verdict entries=%d/%d, want exactly 1/1", claims, verdicts)
	}
	if wantEarlierAnswer != "" && earlierAnswers != 1 {
		t.Errorf("persisted earlier real assistant answers=%d, want exactly one %q", earlierAnswers, wantEarlierAnswer)
	}
	rec := taskGoalRecordOf(t, final.ID)
	if rec.LatestClaim == nil || rec.LatestClaim.Status != generated.GoalLatestClaimStatusMet || rec.LatestClaim.Evidence != "" {
		t.Errorf("LatestClaim=%+v, want met with present-empty evidence kept separate from answer", rec.LatestClaim)
	}
	if rec.LatestVerdict == nil || !rec.LatestVerdict.Met || rec.Round != 1 {
		t.Errorf("durable verdict/round=%+v/%d, want actual MET at round 1", rec.LatestVerdict, rec.Round)
	}
}

func assertRound2ConclusionSeparatesAnswerAndEvidence(t *testing.T, al *AgentLoop, final *task.Task, evidence, answer string) {
	t.Helper()
	al.taskExecutor.wg.Wait()
	if evidence == answer || evidence == "" || answer == "" {
		t.Fatal("fixture must provide distinct, non-empty evidence and actual answer")
	}
	const marker = "The work result, unverified:\n"
	_, workResult, found := strings.Cut(final.Result, marker)
	if !found || workResult != answer {
		t.Errorf("unverified work result=%q (section present=%t), want exact ACTUAL worker answer %q", workResult, found, answer)
	}
	if strings.Contains(final.Result, evidence) {
		t.Errorf("claim evidence %q substituted or leaked into result %q", evidence, final.Result)
	}
	// The no-Judge exit can precede recordRunClaim. Observe the successful
	// claim's actual transcript input instead of inventing a durable claim.
	entries, err := al.GetSessionStore().ReadTranscript(final.SessionID)
	if err != nil {
		t.Fatalf("read claim input: %v", err)
	}
	claims := 0
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if call.Tool == tools.GoalClaimToolName && call.Status == "success" {
				claims++
				if call.Parameters["evidence"] != evidence {
					t.Errorf("recorded claim evidence=%#v, want distinct input %q", call.Parameters["evidence"], evidence)
				}
			}
		}
	}
	if claims != 1 {
		t.Errorf("successful distinct-evidence claim calls=%d, want one", claims)
	}
}
