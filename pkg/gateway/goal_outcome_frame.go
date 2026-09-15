// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// goalOutcomeFrame builds the goal_outcome WS frame (founder decision
// 2026-09-14) from the persisted GoalOutcome. It is the ONE conversion both
// senders use — the live push (websocket.go's EventKindGoalOutcome case) and
// the replay of the `system_subtype: goal_outcome` transcript entry
// (replay.go) — so the two frames for one ending can never differ.
//
// messageID is the transcript entry's own id, which is what lets the SPA keep
// a single thread line across the live push, a replay and a cold REST load.
// GoalOutcomeFrameOutcome is the AsyncAPI hand-synced copy of GoalOutcome
// (its ending and ended_at are plain strings); ended_at keeps the persisted
// timestamp's full precision so it matches the cold-loaded entry exactly.
func goalOutcomeFrame(sessionID, messageID string, o generated.GoalOutcome) generated.GoalOutcomeFrame {
	out := generated.GoalOutcomeFrameOutcome{
		GoalId:     o.GoalId,
		GoalText:   o.GoalText,
		Ending:     string(o.Ending),
		RoundsUsed: o.RoundsUsed,
		MaxRounds:  o.MaxRounds,
		EndedAt:    o.EndedAt.UTC().Format(time.RFC3339Nano),
	}
	if o.JudgeReason != nil {
		reason := *o.JudgeReason
		out.JudgeReason = &reason
	}
	if o.CriteriaTotal != nil {
		total := *o.CriteriaTotal
		out.CriteriaTotal = &total
	}
	return generated.GoalOutcomeFrame{
		Type:      string(generated.WsFrameTypeGoalOutcome),
		SessionId: sessionID,
		MessageId: messageID,
		Outcome:   out,
	}
}
