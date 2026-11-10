package systools_test

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// session-core DEL-11 follow-up: usage is charged to the session's immutable
// owner (agent_id). The retired handover field is never written, so a session
// with only agent_id set must still be attributed — and a joined agent listed
// first in agent_ids must not take the owner's tokens.
func TestGetUsage_AttributesToTheOwnerAgent(t *testing.T) {
	now := time.Now().UTC()
	owned := func(id, owner string, joined []string, tokensIn int) *session.UnifiedMeta {
		return &session.UnifiedMeta{SessionMeta: session.SessionMeta{
			ID: id, AgentID: owner, AgentIDs: joined, Status: "active", Channel: "webchat",
			CreatedAt: now, UpdatedAt: now,
			Stats: session.SessionStats{TokensIn: tokensIn, TokensTotal: tokensIn},
		}}
	}
	sessions := []*session.UnifiedMeta{
		owned("ownerless-list", "ray", nil, 100),                 // agent_ids empty: was skipped entirely
		owned("joined-first", "ray", []string{"mia", "ray"}, 40), // was charged to mia
	}
	deps := newTestDepsWithSessions(config.DefaultConfig(), sessions)
	res := systools.NewUsageQueryTool(deps).Execute(context.Background(), map[string]any{"agent_id": "ray", "period": "month"})
	if res.IsError {
		t.Fatalf("error: %s", res.ForLLM)
	}
	total, _ := parseSuccess(t, res.ForLLM)["total"].(map[string]any)
	if got, _ := total["in"].(float64); int(got) != 140 {
		t.Fatalf("ray's usage in = %v, want 140 (both sessions are owned by ray); resp=%s", total["in"], res.ForLLM)
	}
	resMia := systools.NewUsageQueryTool(deps).Execute(context.Background(), map[string]any{"agent_id": "mia", "period": "month"})
	if resMia.IsError {
		return // no usage for mia is also a correct outcome
	}
	if total, _ := parseSuccess(t, resMia.ForLLM)["total"].(map[string]any); total != nil {
		if got, _ := total["in"].(float64); got != 0 {
			t.Fatalf("mia owns no session but was charged %v tokens; resp=%s", got, resMia.ForLLM)
		}
	}
}
