// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"errors"
	"fmt"
	"os"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
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
// the durable record. Retried consumption is idempotent: an entry already
// present under the instruction's ID is not appended again. Every failure is
// returned so the caller restores the unmarked items instead of consuming
// them.
func (al *AgentLoop) recordAcceptedSteeredInstruction(wake steeringWake, msg providers.Message) error {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return nil
	}
	rec, err := lifecycle.Load(wake.transcriptSessionID)
	switch {
	case errors.Is(err, session.ErrLifecycleNotFound):
		return nil
	case err != nil:
		return fmt.Errorf("load lifecycle of %q: %w", wake.transcriptSessionID, err)
	case rec.SteeredBy == nil:
		return nil
	}
	store := al.ResolveSessionStore(wake.transcriptSessionID)
	if store == nil {
		return fmt.Errorf("no transcript store for session %q", wake.transcriptSessionID)
	}
	entryID := steeredInstructionEntryID(wake.messageID)
	entries, err := store.ReadTranscript(wake.transcriptSessionID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read transcript of %q: %w", wake.transcriptSessionID, err)
	}
	for _, entry := range entries {
		if entry.ID == entryID {
			return nil
		}
	}
	return store.AppendTranscriptStrict(wake.transcriptSessionID, session.TranscriptEntry{
		ID:      entryID,
		Role:    "user",
		Content: msg.Content,
		AgentID: wake.agentID,
	})
}
