package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// goalAdjudicationOutcome is internal retry metadata, not a wire format. Only
// an operator-only outcome's safe, Omnipus-authored reason is carried into a
// conclusion. Transient provider text must not be promoted into trusted copy.
type goalAdjudicationOutcome struct {
	met           bool
	unavailable   bool
	needsOperator bool
	reason        string
}

// runGoalAdjudication retains the established bool interface for callers that
// only need bookkeeping. The outer retry loop consumes the detailed outcome.
// It passes recordedClaim=false: this legacy entry takes the caller's
// claimText at face value, so an empty one stays a refused, claimless call
// (JUDGE-FR-095; ADR-084 §13 keeps the tripwire for exactly this shape).
func (al *AgentLoop) runGoalAdjudication(ctx context.Context, agent *AgentInstance, workspaceID, sessionID string, store *session.UnifiedStore, rec *goal.Goal, claimText string, deliverSteer func(string)) (met, unavailable bool) {
	result := al.runGoalAdjudicationOutcome(ctx, agent, workspaceID, sessionID, store, rec, claimText, false, deliverSteer)
	return result.met, result.unavailable
}
