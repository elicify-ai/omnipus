package agent

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// goalUpwardAnswerMaxBytes is the handback contract's result_so_far limit
// (contracts/components/schemas/MessageParentHandback.yaml, maxLength 50000).
const goalUpwardAnswerMaxBytes = 50000

// goalUpwardAnswerTruncatedMarker ends an answer cut to the handback limit.
const goalUpwardAnswerTruncatedMarker = " … [truncated]"

// goalTurnAnswer is the helper's work result for the parent (issue #1306,
// UAT R1): ALL assistant text the claiming turn produced, in order —
// including text written before the goal_claim call, which is where a model
// usually puts the deliverable before claiming. The Judge still judges the
// narrower claim text (JUDGE-FR-094); this is only what travels upward.
//
// The turn's entries are found by their TurnID in the session transcript.
// GOAL_STATUS control lines are removed (they are control prose, not the
// worker's answer). When the transcript cannot be read or holds no text for
// the turn, the turn's own final content is used; "" means the turn produced
// no text at all, and the caller falls back to the claim evidence.
func goalTurnAnswer(store *session.UnifiedStore, sessionID, turnID, finalContent string) string {
	var parts []string
	if store != nil && sessionID != "" && turnID != "" {
		if entries, err := store.ReadTranscript(sessionID); err == nil {
			for i := range entries {
				e := &entries[i]
				if e.TurnID != turnID || e.Role != "assistant" ||
					(e.Type != "" && e.Type != session.EntryTypeMessage) {
					continue
				}
				if text := stripGoalStatusLines(e.Content); text != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	if len(parts) == 0 {
		if text := stripGoalStatusLines(finalContent); text != "" {
			parts = append(parts, text)
		}
	}
	return capGoalUpwardAnswer(strings.Join(parts, "\n\n"))
}

// stripGoalStatusLines removes GOAL_STATUS marker lines and trims the rest.
func stripGoalStatusLines(text string) string {
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), goalStatusLabel+":") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// capGoalUpwardAnswer cuts answer on a UTF-8 boundary so that, with the
// truncation marker, it stays within the handback contract limit.
func capGoalUpwardAnswer(answer string) string {
	if len(answer) <= goalUpwardAnswerMaxBytes {
		return answer
	}
	cut := goalUpwardAnswerMaxBytes - len(goalUpwardAnswerTruncatedMarker)
	for cut > 0 && !utf8.RuneStart(answer[cut]) {
		cut--
	}
	return answer[:cut] + goalUpwardAnswerTruncatedMarker
}

type goalAnswerKey struct{}

// withGoalAnswer carries the claiming turn's upward answer into an
// adjudication (runGoalAdjudication keeps its signature; the answer rides
// the context like the claiming execution does).
func withGoalAnswer(ctx context.Context, answer string) context.Context {
	if strings.TrimSpace(answer) == "" {
		return ctx
	}
	return context.WithValue(ctx, goalAnswerKey{}, answer)
}

func goalAnswerFromContext(ctx context.Context) string {
	answer, _ := ctx.Value(goalAnswerKey{}).(string)
	return answer
}

// upwardAnswer is what the completion tail hands the parent: the claiming
// turn's answer, or the claim text when the turn produced no text.
func (ag *agentLoopRunGoalAdjudication) upwardAnswer() string {
	if strings.TrimSpace(ag.answer) != "" {
		return ag.answer
	}
	return ag.claimText
}

// upwardAnswer is the work result a Judge conclusion carries for this
// deferred claim: the claiming turn's answer, or the claim text when the
// turn produced no text (or the work was built without one).
func (work *goalDeferredAdjudicationWork) upwardAnswer() string {
	if strings.TrimSpace(work.answer) != "" {
		return work.answer
	}
	return work.claimText
}
