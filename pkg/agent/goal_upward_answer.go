package agent

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// goalUpwardAnswerMaxChars is the handback contract's result_so_far limit
// (contracts/components/schemas/MessageParentHandback.yaml, maxLength 50000
// — a JSON Schema maxLength counts characters, not bytes).
const goalUpwardAnswerMaxChars = session.SystemFinalResultMaxChars

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
	return fitGoalAnswerParts(parts)
}

// goalAnswerSeparator joins the claiming turn's messages.
const goalAnswerSeparator = "\n\n"

// fitGoalAnswerParts joins parts when they fit the hand-back limit. When
// they do not, the deliverable is protected: the turn's longest message is
// kept (whole when it fits on its own, otherwise its beginning with the
// truncation marker), then the other messages are added back in their
// original order while they still fit. When anything was left out and the
// marker still fits, the marker ends the text. A message that fits on its
// own is therefore never cut because a short closing remark followed it.
func fitGoalAnswerParts(parts []string) string {
	joined := strings.Join(parts, goalAnswerSeparator)
	if utf8.RuneCountInString(joined) <= goalUpwardAnswerMaxChars {
		return joined
	}
	longest := 0
	for i := range parts {
		if utf8.RuneCountInString(parts[i]) > utf8.RuneCountInString(parts[longest]) {
			longest = i
		}
	}
	if utf8.RuneCountInString(parts[longest]) > goalUpwardAnswerMaxChars {
		return capGoalUpwardAnswer(parts[longest])
	}
	keep := make([]bool, len(parts))
	keep[longest] = true
	used := utf8.RuneCountInString(parts[longest])
	sep := utf8.RuneCountInString(goalAnswerSeparator)
	for i := range parts {
		if i == longest {
			continue
		}
		if n := utf8.RuneCountInString(parts[i]) + sep; used+n <= goalUpwardAnswerMaxChars {
			keep[i] = true
			used += n
		}
	}
	kept := make([]string, 0, len(parts))
	for i := range parts {
		if keep[i] {
			kept = append(kept, parts[i])
		}
	}
	out := strings.Join(kept, goalAnswerSeparator)
	if used+utf8.RuneCountInString(goalUpwardAnswerTruncatedMarker) <= goalUpwardAnswerMaxChars {
		out += goalUpwardAnswerTruncatedMarker
	}
	return out
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

// capGoalUpwardAnswer cuts answer at a character boundary so that, with the
// truncation marker, it stays within the handback contract limit of
// goalUpwardAnswerMaxChars characters.
func capGoalUpwardAnswer(answer string) string {
	if utf8.RuneCountInString(answer) <= goalUpwardAnswerMaxChars {
		return answer
	}
	keep := goalUpwardAnswerMaxChars - utf8.RuneCountInString(goalUpwardAnswerTruncatedMarker)
	cut, n := 0, 0
	for i := range answer {
		if n == keep {
			cut = i
			break
		}
		n++
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

type systemFinalKey struct{}

// withSystemFinal marks ctx as the completion tail publishing a child's
// committed final hand-back. Only this package can set it (unexported key),
// so a child's own message_parent call — which reaches the same Deliver —
// can never claim the system-final size bound; the code path decides, never
// the message id (issue #1306, round 4).
func withSystemFinal(ctx context.Context) context.Context {
	return context.WithValue(ctx, systemFinalKey{}, true)
}

func isSystemFinal(ctx context.Context) bool {
	v, _ := ctx.Value(systemFinalKey{}).(bool)
	return v
}

// capHandbackResult bounds a final hand-back's result text to the contract
// limit with the truncation marker — for every helper, goal or not.
func capHandbackResult(answer string) string {
	return capGoalUpwardAnswer(answer)
}
