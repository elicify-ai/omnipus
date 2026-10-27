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
//
// One projection, occurrence-scoped on both surfaces. A cancellation record that
// binds to an occurrence is bookkeeping and is EXCLUDED from the model-facing
// view. The occurrence it names proves the declaration it belongs to was
// abandoned — an interrupted batch — so every occurrence that declaration
// issued and no result ever reached is an orphan of the SAME abandoned batch
// and gains its own engine observation: the retained group then validates
// instead of blocking the request. A declaration no cancellation record binds
// to is not abandoned; its unresolved occurrences stay unresolved and the group
// validator refuses them, which is the honest outcome for a history the engine
// has no evidence was interrupted.
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

	// The declarations a bound cancellation proves were abandoned.
	abandoned := map[int]bool{}
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
		if owner < 0 {
			continue
		}
		id := restartCancellationID(msg)
		if !missing[restartCallIdentity{assistantIndex: owner, toolCallID: id}] {
			continue
		}
		abandoned[owner] = true
	}

	// Every occurrence an abandoned declaration issued and no result ever
	// reached is an orphan: it gains its own engine observation, so the
	// retained group validates instead of blocking the request. The
	// observation is engine-authored and ephemeral; it never invents an
	// original response and never rewrites the archive.
	for _, orphan := range findOrphanedToolCalls(scan) {
		if !abandoned[orphan.assistantIndex] {
			continue
		}
		issuer, found := snap.Slot(scanLines[orphan.assistantIndex])
		at, inView := positions[scanLines[orphan.assistantIndex]]
		if !found || !inView {
			continue
		}
		if resolved[session.ToolResultFor{AssistantEntryID: issuer.Addr.EntryID, ToolCallID: orphan.ToolCallID}] {
			continue
		}
		reason := "the abandoned declaration left this occurrence without a recovered original result"
		if missing[restartCallIdentity{assistantIndex: orphan.assistantIndex, toolCallID: orphan.ToolCallID}] {
			reason = "a saved restart cancellation has no recovered original result"
		}
		ob := recoveryObservation{Disposition: tools.FileEffectUnknown, Assistant: issuer.Addr, Reason: reason}
		message, err := ob.message(orphan.ToolCallID)
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
