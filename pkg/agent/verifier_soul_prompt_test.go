// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_soul_prompt_test.go closes the sign-off gap item 5(b): every
// existing verifier-soul test (verifier_adjudication_test.go's
// TestVerifierSoul_* suite) checks what ensureVerifierSoul WROTE to disk
// (via judgeRubricFromConfig, which itself re-reads SOUL.md), but none of
// them prove that an operator's SOUL.md edit actually REACHES the live LLM
// request a real verifier turn sends — i.e. that the Judge's system prompt
// is built from the file on disk at turn time, not from a compiled-in
// constant read directly. A refactor that swapped judgeRubricFromConfig's
// disk read for `return coreagent.JudgeDefaultRubric` inline (or that pinned
// a stale ContextBuilder cache) would still pass every existing test in this
// package while silently ignoring every operator soul edit forever. This
// file drives one full runVerifierAdjudication turn end-to-end and asserts
// the edited soul text is present in the ACTUAL LLM request payload the
// provider receives.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// soulCapturingProvider is a providers.LLMProvider double that captures the
// full messages slice of every Chat call (unlike fakeJudgeProvider,
// judge_test.go, which discards messages — it only needed ctx). Defined
// locally in this file rather than added to fakeJudgeProvider so this fix
// wave does not touch judge_test.go (owned by a different wave).
type soulCapturingProvider struct {
	mu       sync.Mutex
	messages []providers.Message
}

func (p *soulCapturingProvider) Chat(
	_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.messages = messages
	p.mu.Unlock()
	return &providers.LLMResponse{
		Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
	}, nil
}

func (p *soulCapturingProvider) GetDefaultModel() string { return "fake-soul-capture-model" }

// capturedMessages returns the messages slice from the most recent Chat call.
func (p *soulCapturingProvider) capturedMessages() []providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.messages
}

// allMessageContent concatenates every captured message's Content, so a test
// can assert on substring presence without depending on WHICH message index
// carries the system prompt (an internal detail of ContextBuilder's
// composition this test should not need to pin).
func (p *soulCapturingProvider) allMessageContent() string {
	var sb strings.Builder
	for _, m := range p.capturedMessages() {
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// TestRunVerifierAdjudication_OperatorSoulEditReachesVerifierPrompt is the
// positive, end-to-end proof: an operator-authored SOUL.md (written to the
// Judge's workspace BEFORE any verifier turn runs, so ensureVerifierSoul's
// "never overwrite existing non-empty content" rule preserves it verbatim —
// see verifier_adjudication.go's ensureVerifierSoul doc comment) must appear
// in the real LLM request a live verifier turn sends, proving the whole
// chain — ensureVerifierSoul -> SOUL.md on disk -> LoadAgentDefinition ->
// ContextBuilder.BuildSystemPrompt -> the actual provider.Chat call — is
// unbroken end to end, not just the file-write half.
func TestRunVerifierAdjudication_OperatorSoulEditReachesVerifierPrompt(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	const operatorSoulMarker = "OPERATOR-CUSTOM-VERIFICATION-STANDARD-7f3c9a: reject any claim lacking a citation"
	if err := os.MkdirAll(judgeInst.Home, 0o755); err != nil {
		t.Fatalf("MkdirAll judge home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(judgeInst.Home, "SOUL.md"), []byte(operatorSoulMarker), 0o644); err != nil {
		t.Fatalf("seeding operator soul edit: %v", err)
	}

	capture := &soulCapturingProvider{}
	judgeInst.Provider = capture

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-soul-prompt",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "the work cites its sources")},
		Attempt:         1,
		ClaimText:       "done, with citations",
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if result.Verdict == nil || !result.Verdict.Met {
		t.Fatalf("expected a real, met verdict, got %+v", result)
	}

	messages := capture.capturedMessages()
	if len(messages) == 0 {
		t.Fatal("the verifier's LLM call must have received a non-empty messages slice")
	}

	content := capture.allMessageContent()
	if !strings.Contains(content, operatorSoulMarker) {
		t.Errorf(
			"the operator's SOUL.md edit must reach the verifier turn's real LLM request; "+
				"marker %q not found in any of the %d captured message(s):\n%s",
			operatorSoulMarker, len(messages), content,
		)
	}
}

// TestRunVerifierAdjudication_DefaultSoulReachesVerifierPromptWhenNoOperatorEdit
// is the complementary control: on a fresh Judge with NO pre-existing
// SOUL.md, ensureVerifierSoul lazily seeds coreagent.JudgeDefaultRubric
// (ADR-052 FR-038), and that default text — not a hardcoded literal read
// directly from the compiled constant, but the FILE it was just written to —
// must likewise reach the real LLM request. Together with the positive test
// above, this pins BOTH the "operator edit survives" and "default seed
// actually flows through disk" halves of the chain the sign-off gap named:
// "a refactor reading judgeDefaultRubric directly instead of SOUL.md would
// silently ignore operator edits" — this test would still pass under such a
// refactor (the default text matches either way), but its sibling above
// would not, which is exactly the point of pairing them.
func TestRunVerifierAdjudication_DefaultSoulReachesVerifierPromptWhenNoOperatorEdit(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	if got := judgeRubricFromConfig(judgeInst); got != "" {
		t.Fatalf("a fresh judgeHome must have no SOUL.md content yet, got %q", got)
	}

	capture := &soulCapturingProvider{}
	judgeInst.Provider = capture

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-soul-prompt-default",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "x")},
		Attempt:         1,
		ClaimText:       "done",
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if result.Verdict == nil || !result.Verdict.Met {
		t.Fatalf("expected a real, met verdict, got %+v", result)
	}

	// A short, distinctive substring of coreagent.JudgeDefaultRubric — not
	// the whole constant, which would over-couple this test to unrelated
	// wording tweaks in the rubric text.
	const defaultRubricMarker = "Judge — an impartial acceptance-criteria evaluator"
	content := capture.allMessageContent()
	if !strings.Contains(content, defaultRubricMarker) {
		t.Errorf(
			"the lazily-seeded default rubric must reach the verifier turn's real LLM request via the "+
				"materialized SOUL.md; marker %q not found in:\n%s",
			defaultRubricMarker, content,
		)
	}

	// And it must have actually been written to disk — not merely injected
	// in-memory some other way.
	if got := judgeRubricFromConfig(judgeInst); !strings.Contains(got, defaultRubricMarker) {
		t.Errorf("SOUL.md on disk must contain the default rubric marker, got %q", got)
	}
}
