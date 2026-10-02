package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// windowCheckpoint is a private staged candidate. No caller-owned slice or
// turn bookkeeping changes until the metadata transaction succeeds.
type windowCheckpoint struct {
	ts                  *turnState
	snapshot            memory.WindowSnapshot
	state               memory.WindowState
	messages            []providers.Message
	lines               []int
	policy              resultCapPolicy
	recallAt, recallLen int
	droppedRecall       bool
	anchor              int
	breadcrumb          string
	notice              contextReliefNotice
}

func (ts *turnState) contextWindowError() error {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.windowError
}

func (ts *turnState) setContextWindowError(err error) {
	if err == nil {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.windowError == nil {
		ts.windowError = err
	}
}

func newWindowCheckpoint(ctx context.Context, ts *turnState, msgs []providers.Message, cs config.ContextSettings) (*windowCheckpoint, session.ContextWindowStore, error) {
	if err := ts.contextWindowError(); err != nil {
		return nil, nil, err
	}
	store, ok := ts.agent.Sessions.(session.ContextWindowStore)
	if !ok {
		return nil, nil, fmt.Errorf("context checkpoint: session store does not support atomic context checkpoints")
	}
	snap, err := store.SnapshotWindow(ctx, ts.sessionKey)
	if err != nil {
		return nil, nil, err
	}
	p := &windowCheckpoint{ts: ts, snapshot: snap, state: snap.State.Clone(), policy: capPolicyFor(cs, agentContextBudget(ts.agent)), anchor: -1}
	ts.mu.RLock()
	p.notice = ts.windowNotice.clone()
	ts.mu.RUnlock()
	p.breadcrumb = breadcrumbForWindow(snap, snap.State.Skip)
	p.recallAt, p.recallLen = recallBlockPosition(ts, msgs)
	p.lines = mapWindowMessages(snap, msgs, p.recallAt, p.recallLen)
	p.messages, err = projectMessagesChecked(msgs, func(i int) int { return p.lines[i] }, p.state.Projection.Entries, p.projectionContext())
	if err != nil {
		return nil, nil, err
	}
	if p.state.AnchorLine != nil {
		p.anchor = *p.state.AnchorLine
	}
	// A newly initiating user line replaces a prior turn's anchor only when
	// its original archive identity is actually present in this request.
	for i, m := range p.messages {
		if m.Role == "user" && m.Content == ts.userMessage && p.lines[i] >= 0 && !ts.protectedWindowMessage(m) {
			p.anchor = p.lines[i]
		}
	}
	return p, store, nil
}

func (p *windowCheckpoint) projectionContext() projectionContext {
	return projectionContext{policy: p.policy, archive: p.snapshot.Archive, sourceRunes: p.state.Projection.SourceRunes}
}

func (p *windowCheckpoint) dropRecall() bool {
	if p.recallLen == 0 {
		return false
	}
	a, b := p.recallAt, p.recallAt+p.recallLen
	p.messages = append(append([]providers.Message(nil), p.messages[:a]...), p.messages[b:]...)
	p.lines = append(append([]int(nil), p.lines[:a]...), p.lines[b:]...)
	p.ts.mu.RLock()
	span := p.ts.injectedRecallSpan
	p.ts.mu.RUnlock()
	if span != nil {
		p.notice.addEvicted(span.FromTurn, span.ToTurn)
	}
	p.droppedRecall = true
	p.recallAt, p.recallLen = -1, 0
	return true
}

func retainedPayloadSize(ts *turnState, msgs []providers.Message) (int, error) {
	// No timestamp is part of Message. Normalization matches the send seam;
	// required anchor, instruction, breadcrumb and mark bytes remain.
	data, err := json.Marshal(normalizeWindowMessages(ts, stripContextReliefNotice(msgs)))
	return len(data), err
}

// checkpointWindowOptions carries the per-invocation checkpoint variations.
type checkpointWindowOptions struct {
	estimateNotes bool
}

// checkpointWindowOption tweaks one checkpointWindow invocation.
type checkpointWindowOption func(*checkpointWindowOptions)

// estimateEphemeralNotes charges the ephemeral system-note estimate (C1) on
// top of the measured messages when deciding whether the trigger fired and
// whether the staged relief reached its target. Only the admitted-result
// checkpoints pass it: their candidate slice does not yet contain the
// request-only notes, so the estimate is the only way the trigger can see
// them. The send-seam checkpoints (checkpointRequest, the forced overflow
// retry) measure already-assembled payloads that carry the real notes and
// must not add the estimate — that would double-count.
func estimateEphemeralNotes() checkpointWindowOption {
	return func(o *checkpointWindowOptions) { o.estimateNotes = true }
}

func (al *AgentLoop) checkpointWindow(ctx context.Context, ts *turnState, messages []providers.Message, toolDefs []providers.ToolDefinition, force bool, opts ...checkpointWindowOption) ([]providers.Message, bool, error) {
	var co checkpointWindowOptions
	for _, opt := range opts {
		opt(&co)
	}
	if err := ctx.Err(); err != nil {
		return messages, false, err
	}
	if err := ts.contextWindowError(); err != nil {
		return messages, false, err
	}
	if ts.opts.NoHistory || ts.agent.budgetChecksExempt() {
		return messages, false, nil
	}
	ts.mu.RLock()
	pendingNotice := ts.windowNotice.clone()
	ts.mu.RUnlock()
	measured := injectContextReliefNotice(messages, pendingNotice)
	cs := config.DefaultContextSettings()
	if cfg := al.GetConfig(); cfg != nil {
		cs = cfg.Context
	}
	budget := agentContextBudget(ts.agent)
	window, _, _ := ts.agent.windowSnapshot()
	shareLimit := toolResultShareLimit(cs, window)
	midTurnChecksTotal.Add(1)
	// C1: at the admitted-result checkpoints the request-only notes are not
	// in the slice yet; charge their estimate so a note-inflated total still
	// fires relief. Zero on the send-seam paths (notes already assembled).
	noteTokens := 0
	if co.estimateNotes {
		noteTokens = al.ephemeralSystemNoteTokens(ts)
	}
	totalFired := requestTokens(measured, toolDefs)+noteTokens > budget
	shareFired := toolResultShareTokens(measured) > shareLimit
	if !force && !totalFired && !shareFired {
		return measured, false, nil
	}
	p, store, err := newWindowCheckpoint(ctx, ts, measured, cs)
	if err != nil {
		return messages, false, err
	}
	beforeBytes, err := retainedPayloadSize(ts, messages)
	if err != nil {
		return messages, false, err
	}
	progress := false
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return messages, false, ctxErr
		}
		if force && progress {
			bytes, sizeErr := retainedPayloadSize(ts, p.messages)
			if sizeErr != nil {
				return messages, false, sizeErr
			}
			if bytes < beforeBytes {
				break
			}
		}
		if !force && p.atTarget(toolDefs, budget, shareLimit, noteTokens, totalFired, shareFired) {
			break
		}
		changed := p.dropRecall()
		if !changed {
			changed = p.slideOldest()
		}
		if !changed {
			changed, err = p.shortenNext()
		}
		if err != nil {
			return messages, false, err
		}
		if !changed {
			if force {
				return messages, false, nil
			}
			break // Immutable residue is sent; never a local size-only failure.
		}
		progress = true
		p.rebuildBreadcrumb()
		p.refreshNotice()
	}
	if !progress {
		return messages, false, nil
	}
	if force {
		if groupErr := validateWindowGroups(p.messages); groupErr != nil {
			return messages, false, groupErr
		}
	}
	changes, err := al.commitWindowProjections(ctx, p, store)
	if err != nil {
		return messages, false, err
	}
	al.emitWindowProjectionEvents(ts, changes)
	al.recordWindowRelief(p, changes, emptyingSiteMidTurn)
	if p.state.Skip > p.snapshot.State.Skip {
		skipAdvanceTotal.Add(1)
		evictionTotal.Add(1)
	} else if p.droppedRecall {
		evictionTotal.Add(1)
	}
	ts.mu.Lock()
	ts.windowNotice = p.notice.clone()
	ts.mu.Unlock()
	if p.droppedRecall {
		al.dropRecallSpan(ts.sessionKey, "pressure")
		ts.mu.Lock()
		ts.injectedRecallSpan = nil
		ts.injectedRecallAt, ts.injectedRecallLen = 0, 0
		ts.mu.Unlock()
	}
	return p.messages, true, nil
}

func (p *windowCheckpoint) atTarget(defs []providers.ToolDefinition, budget, shareLimit, noteTokens int, totalFired, shareFired bool) bool {
	// The note estimate rides on every total-shaped comparison here too (C1):
	// the ephemeral notes are un-emptiable, so relief stops only when the
	// measured remainder PLUS the notes is at or under the 4/5 hysteresis.
	total, share := requestTokens(p.messages, defs)+noteTokens, toolResultShareTokens(p.messages)
	if total > budget || share > shareLimit {
		return false
	}
	return (!totalFired || total <= budget*4/5) && (!shareFired || share <= shareLimit*4/5)
}
