// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// FR-043 production reachability: a steer to a LIVE external-CLI (3P) child is
// DELIVERED (interrupt + native-conversation resume) through the production
// steering sink's optional capability, instead of the named not_steerable
// refusal — and when the sink cannot deliver (or there is no live conversation)
// the named refusal stays, so an instruction is never silently dropped.

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// cliDelivererSink is fakeSteeringSink PLUS the 3P delivery capability, standing
// in for the production delegateSteeringSink (which embeds *agent.AgentLoop and
// therefore exposes DeliverExternalCLIInstruction). Its embedded fakeSteeringSink
// supplies EnqueueSteeringMessage so it satisfies DelegateSteeringSink too.
type cliDelivererSink struct {
	fakeSteeringSink
	deliverErr error
	sessions   []string
	texts      []string
}

func (s *cliDelivererSink) DeliverExternalCLIInstruction(_ context.Context, sessionID, agentID string, msg providers.Message, correlationID string) (string, error) {
	if s.deliverErr != nil {
		return "", s.deliverErr
	}
	s.sessions = append(s.sessions, sessionID)
	s.texts = append(s.texts, msg.Content)
	return "corr-delivered", nil
}

// FR-043: with a sink that can deliver, a steer to a 3P child is DELIVERED (not
// refused), and the instruction reaches the delivery capability.
func TestDelegateSteer_3PChild_DeliversViaCapability(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	sink := &cliDelivererSink{}
	tool.SetSteeringSink(sink)
	seedRedirectChild(t, lc, "child-3p-live", session.LifecycleRunning, true)

	res := tool.Execute(WithTranscriptSessionID(context.Background(), "parent-1"), map[string]any{
		"action": "steer", "session_id": "child-3p-live", "text": "deliver S",
	})
	if res.IsError {
		t.Fatalf("FR-043: steer to a live 3P child must be delivered, got refusal: %s", res.ForLLM)
	}
	if len(sink.texts) != 1 || sink.texts[0] != "deliver S" {
		t.Fatalf("FR-043: delivered instruction = %#v, want exactly [\"deliver S\"]", sink.texts)
	}
	if len(sink.sessions) != 1 || sink.sessions[0] != "child-3p-live" {
		t.Fatalf("FR-043: delivered to session %#v, want [\"child-3p-live\"]", sink.sessions)
	}
	if !strings.Contains(res.ForLLM, "same conversation") {
		t.Errorf("delivery result must say the instruction reaches the same conversation, got: %s", res.ForLLM)
	}
}

// FR-043 / BDD-05.6: when the delivery capability cannot reach a live
// conversation, the steer refuses VISIBLY (named not_steerable, carrying the
// real cause) — never a silent success and never a dropped instruction.
func TestDelegateSteer_3PChild_DeliveryFailureIsVisible(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	sink := &cliDelivererSink{deliverErr: context.DeadlineExceeded}
	tool.SetSteeringSink(sink)
	seedRedirectChild(t, lc, "child-3p-nolive", session.LifecycleRunning, true)

	res := tool.Execute(WithTranscriptSessionID(context.Background(), "parent-1"), map[string]any{
		"action": "steer", "session_id": "child-3p-nolive", "text": "deliver S",
	})
	if !res.IsError {
		t.Fatalf("BDD-05.6: a 3P steer that cannot be delivered must fail visibly, got success: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "not_steerable") {
		t.Errorf("the refusal must be the named not_steerable result, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, context.DeadlineExceeded.Error()) {
		t.Errorf("the refusal must carry the truthful delivery cause, got: %s", res.ForLLM)
	}
	if len(sink.texts) != 0 {
		t.Errorf("a failed delivery must deliver nothing, got %#v", sink.texts)
	}
}
