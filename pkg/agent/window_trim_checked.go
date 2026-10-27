package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// trimWindowChecked keeps the pre-turn whole-turn policy, using the same exact
// archive snapshot, staged projection and checked metadata commit as mid-turn.
func (al *AgentLoop) trimWindowChecked(ctx context.Context, agent *AgentInstance, transcriptID, key string, force bool) (compressionResult, bool) {
	if agent.budgetChecksExempt() {
		return compressionResult{NothingToTrim: true}, false
	}
	store, ok := agent.Sessions.(session.ContextWindowStore)
	if !ok {
		return compressionResult{Err: fmt.Errorf("context trim: session store does not support atomic context checkpoints")}, false
	}
	modelKey := al.modelContextKey(key)
	snap, err := store.WindowView(ctx, modelKey)
	if err != nil {
		return compressionResult{Err: err}, false
	}
	msgs, lines := recoveryWindowHistory(snap)
	ts := al.getActiveTurnState(key)
	if ts == nil {
		ts = &turnState{agent: agent, sessionKey: key}
	}
	cs := config.DefaultContextSettings()
	if cfg := al.GetConfig(); cfg != nil {
		cs = cfg.Context
	}
	p := &windowCheckpoint{ts: ts, snapshot: snap, slots: storeSlots{store: store, key: modelKey}, state: snap.State.Clone(), lines: lines,
		policy: capPolicyFor(cs, agentContextBudget(agent)), anchor: -1}
	ts.mu.RLock()
	p.notice = ts.windowNotice.clone()
	ts.mu.RUnlock()
	if snap.State.AnchorLine != nil {
		p.anchor = *snap.State.AnchorLine
	}
	p.messages, err = projectMessagesChecked(msgs, func(i int) int { return lines[i] }, snap.State.Projection.Entries, p.projectionContext())
	if err != nil {
		return compressionResult{Err: err}, false
	}
	for i, m := range p.messages {
		if ts.userMessage != "" && m.Role == "user" && m.Content == ts.userMessage && !ts.protectedWindowMessage(m) {
			p.anchor = lines[i]
		}
	}
	budget := agentContextBudget(agent)
	window, _, _ := agent.windowSnapshot()
	shareLimit := toolResultShareLimit(cs, window)
	surface := al.sentToolSurfaceTokens(agent, transcriptID, key)
	span := al.activeRecallSpan(key)
	recallTokens := 0
	if span != nil {
		recallTokens = span.Tokens
	}
	fits := func(m []providers.Message) bool {
		return sumMessageTokens(m)+surface+recallTokens <= budget && toolResultShareTokens(m) <= shareLimit
	}
	if !force && fits(p.messages) {
		return compressionResult{NothingToTrim: true, RemainingMessages: len(msgs)}, false
	}
	beforeBytes, err := retainedPayloadSize(ts, p.messages)
	if err != nil {
		return compressionResult{Err: err}, false
	}
	changed := false
	if span != nil {
		recallTokens = 0
		changed = true
		p.droppedRecall = true
		p.notice.addEvicted(span.FromTurn, span.ToTurn)
	}
	if (!force && !fits(p.messages)) || (force && !changed) {
		changed = p.trimWholeTurns(fits, force) || changed
	}
	for (!force && !fits(p.messages)) || (force && !changed) {
		shortened, shortenErr := p.shortenNext()
		if shortenErr != nil {
			return compressionResult{Err: shortenErr}, false
		}
		if !shortened {
			break // Immutable residue goes to the provider; it is not a local fatal.
		}
		changed = true
		if force {
			bytes, sizeErr := retainedPayloadSize(ts, p.messages)
			if sizeErr != nil {
				return compressionResult{Err: sizeErr}, false
			}
			if bytes >= beforeBytes {
				changed = false // Framing growth must not authorize an unchanged retry.
			}
		}
	}
	if !changed {
		return compressionResult{NothingToTrim: true, RemainingMessages: len(msgs)}, false
	}
	changes, err := al.commitWindowProjections(ctx, p, store)
	if err != nil {
		return compressionResult{Err: err}, false
	}
	al.emitWindowProjectionEvents(ts, changes)
	if p.droppedRecall {
		al.dropRecallSpan(key, "pressure")
	}
	ts.mu.Lock()
	ts.windowNotice = p.notice.clone()
	if p.droppedRecall {
		ts.injectedRecallSpan = nil
		ts.injectedRecallAt, ts.injectedRecallLen = 0, 0
	}
	ts.mu.Unlock()
	dropped := len(msgs) - len(p.messages)
	evictionTotal.Add(1)
	if p.state.Skip > snap.State.Skip {
		skipAdvanceTotal.Add(1)
	}
	al.recordWindowRelief(p, changes, emptyingSitePreTurn)
	logger.InfoCF("agent", "windowTrim: committed context relief", map[string]any{
		"session_key": key, "dropped_messages": dropped, "kept_msgs": len(p.messages),
		"budget": budget, "context_archive_lines": snap.State.Count, "context_eviction_total": evictionTotal.Load(),
	})
	return compressionResult{DroppedMessages: dropped, RemainingMessages: len(p.messages)}, true
}

func (p *windowCheckpoint) trimWholeTurns(fits func([]providers.Message) bool, force bool) bool {
	selected, cut := -1, p.state.Skip
	var kept []providers.Message
	var lines []int
	for _, end := range p.wholeTurnStarts() {
		if end <= 0 || end >= len(p.lines) || p.lines[end] <= p.state.Skip || !p.canTrimPrefix(end) {
			continue
		}
		next, nextLines := p.trimmedSuffix(p.lines[end])
		selected, cut, kept, lines = end, p.lines[end], next, nextLines
		if force || fits(next) {
			break
		}
	}
	if selected < 0 {
		return false
	}
	p.messages, p.lines = kept, lines
	p.notice.addEvicted(turnNumberForArchiveLine(p.archive(), p.state.Skip), turnNumberForArchiveLine(p.archive(), cut-1))
	p.state.Skip = cut
	p.state.AnchorLine = nil
	if p.anchor >= 0 && p.anchor < cut {
		anchor := p.anchor
		p.state.AnchorLine = &anchor
	}
	for key := range p.state.Projection.Entries {
		if key.ArchiveLine < cut {
			delete(p.state.Projection.Entries, key)
			delete(p.state.Projection.SourceRunes, key)
			delete(p.notice.shortened, key)
		}
	}
	for key := range p.state.Projection.TranscriptAddr {
		if key.ArchiveLine < cut {
			delete(p.state.Projection.TranscriptAddr, key)
		}
	}
	return true
}

func (p *windowCheckpoint) canTrimPrefix(end int) bool {
	for i, m := range p.messages[:end] {
		if end < len(p.lines) && p.lines[i] >= p.lines[end] {
			return false // A late repaired result must be evicted with its declaration, never alone.
		}
		// The anchor is a real archive address; the unset sentinel (-1) is not.
		// Only a set anchor can match, so an inserted observation (ordinal -1)
		// never borrows the sentinel's keep/skip meaning.
		if p.anchor >= 0 && p.lines[i] == p.anchor {
			continue
		}
		if p.lines[i] < 0 {
			// An inserted recovery observation is evicted with the declaration it
			// completes. That declaration precedes the observation, so it is
			// inside this prefix too and the observation vetoes nothing here.
			if d := p.observationDeclarationIndex(i); d >= 0 && d < end {
				continue
			}
			return false
		}
		if m.Role == "system" || p.ts.protectedWindowMessage(m) {
			return false
		}
	}
	for _, step := range windowSteps(p.messages) {
		if step.start < end && (!step.complete || step.end > end) {
			return false
		}
	}
	return true
}

// observationDeclarationIndex returns the index of the assistant message that
// declared the tool call an inserted recovery observation at index i completes,
// or -1 when that declaration is not in the view. The projection always inserts
// an observation right after the declaring assistant's group, so the nearest
// preceding assistant IS the declaration; the scan stops there and never
// reaches across a later assistant (a reused tool-call id belongs to neither).
func (p *windowCheckpoint) observationDeclarationIndex(i int) int {
	m := p.messages[i]
	if m.Role != "tool" || m.ToolCallID == "" {
		return -1
	}
	for j := i - 1; j >= 0; j-- {
		d := p.messages[j]
		if d.Role != "assistant" {
			continue
		}
		for _, tc := range d.ToolCalls {
			if tc.ID == m.ToolCallID {
				return j
			}
		}
		return -1
	}
	return -1
}

func (p *windowCheckpoint) trimmedSuffix(cut int) ([]providers.Message, []int) {
	out := make([]providers.Message, 0, len(p.messages))
	lines := make([]int, 0, len(p.lines))
	for i, m := range p.messages {
		switch {
		case p.lines[i] >= cut:
			// Survives the cut by its own archive address.
		case p.anchor >= 0 && p.lines[i] == p.anchor:
			// The protected current user message always stays.
		case p.lines[i] < 0:
			// An inserted recovery observation goes with its declaration: it is
			// dropped when the declaration is cut and kept when the declaration
			// survives. Its own ordinal is -1, so the address comparison above
			// never decides it.
			d := p.observationDeclarationIndex(i)
			if d < 0 || p.lines[d] < cut {
				continue
			}
		default:
			continue
		}
		out = append(out, m)
		lines = append(lines, p.lines[i])
	}
	return out, lines
}
