// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// midturn_budget.go — the shared admitted-result/final-request checkpoint.
// Relative bounds and whole-step relief follow ADR-066 MAJ-CW-001–007/012.
package agent

import (
	"context"
	"math"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// midTurnChecksTotal counts mid-turn window-check evaluations (B-33: N tool
// results → N checks). Exported for test assertions via
// MidTurnBudgetChecksTotal.
var midTurnChecksTotal atomic.Int64

// MidTurnBudgetChecksTotal returns the number of mid-turn D6 checks that
// evaluated their trigger conditions (exempt/NoHistory turns do not count).
func MidTurnBudgetChecksTotal() int64 { return midTurnChecksTotal.Load() }

// contextResidueOverflowsTotal counts final assembled-request checkpoints
// whose remaining request-only additions exceed a bound after relief, while
// the surviving live window fits both budget and tool-result share bounds.
// This exported counter is diagnostic only: immutable residue is still sent.
var contextResidueOverflowsTotal atomic.Int64

// ContextResidueOverflowsTotal returns the count above.
func ContextResidueOverflowsTotal() int64 { return contextResidueOverflowsTotal.Load() }

// toolResultShareLimit measures the combined result allowance against the
// resolved model window W, not the smaller request budget B (MAJ-CW-007).
func toolResultShareLimit(cs config.ContextSettings, window int) int {
	f := cs.ToolResultShareFraction
	if math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 || f > 1 {
		f = config.DefaultToolResultShareFraction
	}
	return max(1, int(math.Floor(f*float64(window))))
}

// ephemeralSystemNoteTokens estimates request-only notes for the pre-turn
// entry check: scratchpad, workspace instructions, web-rendering guidance and
// the goal rubric. The final request checkpoint measures the actual assembled
// messages after note injection and hooks; it does not use this estimate.
// The compressed manifest is already charged by sentToolSurfaceTokens at
// the pre-turn site, so including it here would double-count its cost.
func (al *AgentLoop) ephemeralSystemNoteTokens(ts *turnState) int {
	if ts == nil || ts.agent == nil {
		return 0
	}
	tokens := 0
	add := func(note string) {
		if note != "" {
			tokens += estimateMessageTokens(providers.Message{Role: "system", Content: note})
		}
	}
	add(al.buildScratchpadNote(ts.agent.ID, ts.opts.TranscriptSessionID))
	add(buildWorkspaceInstructionsNote(ts.opts.WorkspaceID))
	add(buildWebRenderingNote(ts.channel))
	// ADR-088 D4 (spec FR-011): the goal rubric note. The ADR-078 D2
	// buildGoalPendingNote entry that used to sit here is gone in full
	// (pkg/agent/goal_pending_note.go, deleted — ADR-088 D9); this is its
	// replacement in the SAME per-turn note enumeration. See
	// goalRubricNoteForBudget's own doc comment for why this deliberately
	// does not gate on "iteration==1 only" the way the real injection call
	// site (runTurn) does — a conservative over-estimate on later
	// iterations, never an under-estimate.
	add(al.goalRubricNoteForBudget(ts))
	return tokens
}

// manifestNoteTokens estimates the compressed manifest with the same tool
// universe, loaded-tools lookup and builder as sentToolSurfaceTokens. The
// pre-turn site already charges that cost; the final request checkpoint
// charges the actual assembled note instead of this separate estimate.
func (al *AgentLoop) manifestNoteTokens(ts *turnState, cfg *config.Config) int {
	if ts == nil || ts.agent == nil || ts.agent.Tools == nil || cfg == nil || !cfg.Tools.Manifest.Compressed {
		return 0
	}
	bucket := ts.manifestBucket()
	loaded := al.sessionLoadedTools(bucket)
	note := tools.BuildCompressedManifest(ts.agent.Tools.GetAll(), loaded)
	if note == "" {
		return 0
	}
	return estimateMessageTokens(providers.Message{Role: "system", Content: note})
}

// midTurnWindowCheck installs the committed ongoing window after each
// admitted result. The shared checkpoint counts the pending model-only
// notice, but that notice is installed only in the final provider request,
// not in the live history. Immutable oversized residue is not a local error.
func (al *AgentLoop) midTurnWindowCheck(ts *turnState, messages []providers.Message, toolDefs []providers.ToolDefinition) ([]providers.Message, error) {
	ctx := ts.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	out, _, err := al.checkpointWindow(ctx, ts, messages, toolDefs, false)
	if err != nil {
		return messages, err
	}
	return stripContextReliefNotice(out), nil
}
