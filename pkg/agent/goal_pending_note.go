// Omnipus — ADR-078 D2: per-turn pending-goal context injection (v0.1.1)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// buildGoalPendingNote (ADR-078 D2) renders the ephemeral per-turn system
// note that keeps the model aware of a goal that is compiled and awaiting
// the user's confirmation. Without this note, a non-confirm reply rode
// applyGoalPendingReply's blind `return false, ""` fall-through
// (goal_loop.go, see that function's doc comment and ADR-078 D3) into an
// ordinary LLM turn that had no idea a goal was pending — the model
// answered about the PRIOR conversation, with the pending goal and its
// confirm/amend affordance completely invisible to it (ADR-078 symptom 2).
//
// Gating (ADR-078 D2/D4 — get this exact):
//
//   - meta.GoalPendingJSON == "" → nothing pending, no note.
//   - meta.GoalCondition != "" → an ACTIVE goal's own pending amendment
//     (proposeGoalAmendment sets GoalPendingJSON while GoalCondition is
//     still set). Injecting "awaiting confirmation, not yet active" here
//     would contradict the goal that is, in fact, already running. Amendment
//     windows are explicitly out of scope for this note (ADR-078 D4).
//   - meta.GoalClarificationJSON != "" → a pending CLARIFYING QUESTION has
//     its own conversational surface (the question itself) and its own
//     reply routing (applyGoalPendingReply's clarification branch, which
//     runs BEFORE the confirm branch). Double-injecting here would confuse
//     the model about whether it is answering a clarifying question or
//     reviewing a compiled goal awaiting confirmation.
//
// Rebuilt fresh every turn from session meta and NEVER persisted to
// history — identical lifecycle to buildScratchpadNote /
// buildWorkspaceInstructionsNote: it appears exactly on the turns where a
// fresh goal is pending and vanishes the instant the goal activates or is
// cleared, with zero on-disk state of its own.
//
// Returns "" when store/sessionID are unusable, when the gates above
// exclude injection, or when the pending JSON does not parse into a
// CompiledGoal with at least one criterion (loadCompiledGoal already logs
// that corruption case loudly; this function just treats it as "nothing to
// inject" rather than failing the turn).
func buildGoalPendingNote(store *session.UnifiedStore, sessionID string) string {
	if store == nil || sessionID == "" {
		return ""
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		return ""
	}
	if strings.TrimSpace(meta.GoalPendingJSON) == "" {
		return ""
	}
	if meta.GoalCondition != "" {
		// Active-goal amendment window — out of scope (ADR-078 D4).
		return ""
	}
	if strings.TrimSpace(meta.GoalClarificationJSON) != "" {
		// A pending clarifying question owns the conversational surface.
		return ""
	}
	pending := loadCompiledGoal(meta.GoalPendingJSON)
	if pending == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# Pending goal (awaiting user confirmation)\n\n")
	sb.WriteString("A goal has been compiled from the user's intent and is awaiting their confirmation — " +
		"it is NOT yet active.\n\n")
	sb.WriteString("Goal: ")
	if pending.Prompt != "" {
		sb.WriteString(pending.Prompt)
	} else {
		sb.WriteString(pending.Intent)
	}
	sb.WriteString("\n\nDone when (a reviewer will verify each of these):\n")
	for i, c := range pending.Criteria {
		fmt.Fprintf(&sb, "  %d. %s\n", i+1, criterionEchoLine(c))
	}
	sb.WriteString("\nHow to treat the user's current message while this goal is pending:\n")
	sb.WriteString("- If it expresses confirmation intent (e.g. \"yes\", \"go ahead\", \"sounds good\"), do NOT activate " +
		"the goal yourself — activation is deterministic and happens only on an exact reply. Tell the user to reply \"" +
		ConfirmGoalWord + "\" or click Confirm on the goal card.\n")
	sb.WriteString("- If it asks to change the goal, answer helpfully but do not assume the change has been applied — " +
		"tell the user to restate it with `/goal <new intent>` (or click Amend).\n")
	sb.WriteString("- Otherwise (a question, or something unrelated), answer it normally with this pending goal in view. " +
		"The goal stays pending until the user confirms, amends, or clears it (`/goal clear`).\n")
	return sb.String()
}

// injectGoalPendingNote inserts the ADR-078 D2 pending-goal note as a
// "system" role message at index 1 of msgs — immediately after the system
// prompt at index 0 — mirroring injectWorkspaceInstructions' insertion
// contract (workspace_instructions.go). Returns msgs unchanged when
// note == "" or len(msgs) == 0, matching every other ephemeral-note
// injector's empty-note no-op contract.
func injectGoalPendingNote(msgs []providers.Message, note string) []providers.Message {
	if note == "" || len(msgs) == 0 {
		return msgs
	}
	out := make([]providers.Message, 0, len(msgs)+1)
	out = append(out, msgs[0])
	out = append(out, providers.Message{Role: "system", Content: note})
	out = append(out, msgs[1:]...)
	return out
}
