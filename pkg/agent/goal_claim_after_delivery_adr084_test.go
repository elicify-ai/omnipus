// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_claim_after_delivery_adr084_test.go carries ADR-084 revision 9
// D13/JUDGE-FR-098's required oracle (wave E13, joint delivery plan §3): the
// claim-triggered adjudication MUST be dispatched strictly AFTER the
// operator's answer has been published, off the critical path. The oracle
// is OBSERVED ordering through injected seams (a captured publish timestamp
// vs. a captured Judge-call timestamp), never a source-order read — a
// conditionally-deferred implementation could still pass a naive
// "the call appears later in the file" check, which the Explicit
// Non-Behaviors this delivery targets forbid.
package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// claimingWorkerProvider is a minimal worker LLM provider that answers with
// a fixed final message and NO tool calls — the simplest way to drive a
// real runTurn straight to a `met`+evidence marker completion without any
// tool-policy scaffolding.
type claimingWorkerProvider struct{ content string }

func (p *claimingWorkerProvider) Chat(
	context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: p.content, ToolCalls: []providers.ToolCall{}}, nil
}

func (p *claimingWorkerProvider) GetDefaultModel() string { return "test-model" }

// TestAdjudicationDispatchedAfterOutboundPublish proves JUDGE-FR-098: the
// observed sequence is PublishOutbound(finalContent) THEN the verifier
// dispatch — runAgentLoop returns without waiting for the adjudication, and
// the adjudication's own context outlives the turn's.
func TestAdjudicationDispatchedAfterOutboundPublish(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, judgeInst := newGoalLoopTestLoop(t,
		&claimingWorkerProvider{content: "[goal:evidence] all green\nGOAL_STATUS: met"}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	// setGoalRoundsArmedRecorded's "goal-condition"-id criterion matches the
	// fixed verdict JSON shape below (goal_triggers_test.go).
	setGoalRoundsArmedRecorded(t, store, sid, "goal after-delivery ordering", 0, time.Now())

	var mu sync.Mutex
	var publishedAt, judgeCalledAt time.Time
	judgeDone := make(chan struct{})
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		mu.Lock()
		judgeCalledAt = time.Now()
		mu.Unlock()
		close(judgeDone)
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"goal-condition","met":true,"reason":"ok"}]}`,
		}, nil
	}}

	publishSeen := make(chan struct{})
	go func() {
		<-al.bus.OutboundChan()
		mu.Lock()
		publishedAt = time.Now()
		mu.Unlock()
		close(publishSeen)
	}()

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
		SendResponse: true, UserMessage: "please check",
	}

	if _, err := al.runAgentLoop(context.Background(), agentInst, opts); err != nil {
		t.Fatalf("runAgentLoop: %v", err)
	}

	select {
	case <-publishSeen:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the outbound publish")
	}
	select {
	case <-judgeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the deferred adjudication to dispatch — runAgentLoop must dispatch it in a goroutine after publish")
	}

	mu.Lock()
	pub, judge := publishedAt, judgeCalledAt
	mu.Unlock()
	if pub.IsZero() || judge.IsZero() {
		t.Fatal("both the publish and the judge call must have been observed")
	}
	if !judge.After(pub) {
		t.Fatalf("JUDGE-FR-098: judge called at %v, want strictly after the outbound publish at %v", judge, pub)
	}
}
