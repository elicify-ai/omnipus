package agent

import (
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/memory"
)

// Count and log only committed projections. Repeated mark-only checks are not
// new emptying work, and failed persistence produces neither metrics nor frames.
func (al *AgentLoop) recordWindowRelief(p *windowCheckpoint, changes []windowProjectionChange, site string) {
	emptied := 0
	for _, c := range changes {
		if c.state == memory.ProjectionEmptied && p.snapshot.State.Projection.Entries[c.key] != memory.ProjectionEmptied {
			emptied++
		}
	}
	contextEmptiesTotal.Add(int64(emptied))
	logger.InfoCF("agent", "committed context-window relief", map[string]any{
		"session_key": p.ts.sessionKey, "agent_id": p.ts.agent.ID, "site": site,
		"results_shortened": len(changes), "results_emptied": emptied,
		"skip_before": p.snapshot.State.Skip, "skip_after": p.state.Skip,
		"share_after": toolResultShareTokens(p.messages), "context_empties_total": contextEmptiesTotal.Load(),
	})
}
