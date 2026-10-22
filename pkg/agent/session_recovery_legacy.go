package agent

import (
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// projectLegacyRecoveryMarkers gives an already-durable cancellation its truthful
// provider representation: Unknown, never an original return. This read projection
// does not modify the archive. Native recovery deliberately disables it until it
// has checked proof, persisted any hold and appended its own checked observation.
func projectLegacyRecoveryMarkers(snap session.WindowView, history []providers.Message, lines []int,
	scan []providers.Message, scanLines []int, omitted map[int]bool, missing map[restartCallIdentity]bool,
) ([]providers.Message, []int) {
	positions := map[int]int{}
	for i, line := range lines {
		positions[line] = i
	}
	resolved := map[session.ToolResultFor]bool{}
	for _, slot := range snap.Live {
		if slot.Message.Role == "tool" && slot.ToolResultFor != nil {
			resolved[*slot.ToolResultFor] = true
		}
	}
	omitLines := map[int]bool{}
	insert := map[int][]providers.Message{}
	emitted := map[restartCallIdentity]bool{}
	owner := -1
	for i, msg := range scan {
		if msg.Role == "user" || msg.Role == "assistant" {
			owner = -1
			if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
				owner = i
			}
		}
		if !omitted[i] {
			continue
		}
		omitLines[scanLines[i]] = true
		id := restartCancellationID(msg)
		key := restartCallIdentity{assistantIndex: owner, toolCallID: id}
		if owner < 0 || !missing[key] || emitted[key] {
			continue
		}
		issuer, found := snap.Slot(scanLines[owner])
		at, inView := positions[scanLines[owner]]
		if !found || !inView {
			continue
		}
		if resolved[session.ToolResultFor{AssistantEntryID: issuer.Addr.EntryID, ToolCallID: id}] {
			continue
		}
		ob := recoveryObservation{Disposition: tools.FileEffectUnknown, Assistant: issuer.Addr,
			Reason: "a saved restart cancellation has no recovered original result"}
		message, err := ob.message(id)
		if err != nil {
			// Preserve the incomplete original view: its group validator refuses
			// compute rather than manufacturing an unencodable observation.
			return history, lines
		}
		end := at + 1
		for end < len(history) && history[end].Role == "tool" {
			end++
		}
		insert[end] = append(insert[end], message)
		emitted[key] = true
	}
	out := make([]providers.Message, 0, len(history))
	ordinals := make([]int, 0, len(lines))
	for i := 0; i <= len(history); i++ {
		for _, message := range insert[i] {
			out, ordinals = append(out, message), append(ordinals, -1)
		}
		if i < len(history) && !omitLines[lines[i]] {
			out, ordinals = append(out, history[i]), append(ordinals, lines[i])
		}
	}
	return bindRecoveryToolResults(snap, out, ordinals)
}
