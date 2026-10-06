// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// steeredInstructionEntryID is the transcript entry ID of an accepted steered
// instruction. It is derived from the wake's durable message identity, so one
// accepted instruction has exactly one transcript identity however many times
// its consumption is retried.
func steeredInstructionEntryID(messageID string) string {
	return "instruction-" + messageID
}

// recordAcceptedSteeredInstruction makes an accepted, wake-bearing instruction
// durable in the receiving STEERED session's transcript. It runs before the
// consumed marker is written (consumeDequeuedSteeringResult), so a marker is
// never the only trace of an instruction: boot recovery and a replayed wake
// treat the marker as "consumed", and the sub-agent control-plane design
// (D4 "Delivery") requires the exact injected text to be durably present in
// the transcript before that receipt and the queue-slot release.
//
// A session with no steering edge (an ordinary chat) is deliberately not
// touched: its transcript renders to a person, and its inbox entry is already
// the durable record. A session whose lifecycle record is missing but whose
// metadata names a parent is a genuine child whose record was lost (I-8
// row 4, damaged_child), never an ordinary chat: that is a visible error.
// The append is once-only under the session's own store lock, so retried or
// concurrent consumption of one instruction writes exactly one entry. Every
// failure is returned so the caller restores the unmarked items instead of
// consuming them.
func (al *AgentLoop) recordAcceptedSteeredInstruction(wake steeringWake, msg providers.Message) error {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return nil
	}
	store := al.ResolveSessionStore(wake.transcriptSessionID)
	rec, err := lifecycle.Load(wake.transcriptSessionID)
	switch {
	case errors.Is(err, session.ErrLifecycleNotFound):
		if store == nil {
			return fmt.Errorf("no transcript store for session %q", wake.transcriptSessionID)
		}
		class, classifyErr := NewSteerRecordClassifier(lifecycle, store).Classify(context.Background(), wake.transcriptSessionID)
		if classifyErr != nil {
			return fmt.Errorf("classify session %q without a lifecycle record: %w", wake.transcriptSessionID, classifyErr)
		}
		if class == steer.ClassOrdinaryRoot {
			return nil
		}
		return fmt.Errorf("session %q is a %s: its lifecycle record is missing, so the instruction was not injected", wake.transcriptSessionID, class)
	case err != nil:
		return fmt.Errorf("load lifecycle of %q: %w", wake.transcriptSessionID, err)
	case rec.SteeredBy == nil:
		return nil
	}
	if store == nil {
		return fmt.Errorf("no transcript store for session %q", wake.transcriptSessionID)
	}
	_, err = store.AppendTranscriptOnce(wake.transcriptSessionID, session.TranscriptEntry{
		ID:      steeredInstructionEntryID(wake.messageID),
		Role:    "user",
		Content: msg.Content,
		AgentID: wake.agentID,
	})
	return err
}
