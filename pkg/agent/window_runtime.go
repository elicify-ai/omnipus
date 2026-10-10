package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// windowProducer names the producer of a model-window append so the archive
// records who wrote it: the membership and the trusted source are chosen here,
// by the call site, never defaulted from the message role (Decision A).
type windowProducer int

const (
	// windowProducerAssistant is the model's own turn output.
	windowProducerAssistant windowProducer = iota
	// windowProducerUser is the person's (or relayed) input admitted to the
	// model at the turn's start or injected as steering.
	windowProducerUser
)

func (p windowProducer) modelAppend(msg providers.Message, agentID string) session.ModelAppend {
	in := session.ModelAppend{Message: msg, ViewMembership: session.ViewMembershipModel, AgentID: agentID}
	if p == windowProducerAssistant {
		in.Source = session.EntrySource{Kind: "agent"}
	} else {
		in.Source = session.EntrySource{Kind: "user"}
	}
	return in
}

// appendWindowMessage admits structural and control messages through the same
// checked archive path as results, before they enter the ongoing request. It
// returns the exact slot the archive assigned, so the turn keeps the address of
// an assistant message and hands it to that step's tool results.
func (ts *turnState) appendWindowMessage(msg providers.Message, producer windowProducer) (session.ModelSlot, error) {
	if ts.opts.NoHistory {
		return session.ModelSlot{}, nil
	}
	if err := ts.contextWindowError(); err != nil {
		return session.ModelSlot{}, err
	}
	store, ok := ts.agent.Sessions.(session.ContextWindowStore)
	if !ok {
		return session.ModelSlot{}, fmt.Errorf("context admission: session store does not support atomic context checkpoints")
	}
	ctx := ts.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	slot, _, err := store.AppendModelMessage(ctx, ts.sessionKey, producer.modelAppend(msg, ts.agent.ID))
	if err != nil {
		ts.setContextWindowError(err)
		return session.ModelSlot{}, fmt.Errorf("context admission: append %s message: %w", msg.Role, err)
	}
	if msg.Role == "assistant" {
		ts.recordCallIssuers(msg, slot.Addr)
	}
	return slot, nil
}

// recordCallIssuers remembers which archive record declared each of msg's tool
// calls.
func (ts *turnState) recordCallIssuers(msg providers.Message, addr session.ArchiveAddress) {
	if len(msg.ToolCalls) == 0 {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.callIssuers == nil {
		ts.callIssuers = make(map[string]session.ArchiveAddress, len(msg.ToolCalls))
	}
	for _, tc := range msg.ToolCalls {
		if tc.ID != "" {
			ts.callIssuers[tc.ID] = addr
		}
	}
}

// callIssuer returns the address of the assistant record that issued callID in
// this turn.
func (ts *turnState) callIssuer(callID string) (session.ArchiveAddress, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	addr, ok := ts.callIssuers[callID]
	return addr, ok
}

// retainLiveWindow installs only the surviving live-message subsequence, not
// request-only notes or hook additions. Content changes use the same staged
// messages as the outgoing request, preserving resolved media and exact cuts.
func retainLiveWindow(live, request []providers.Message) []providers.Message {
	out := make([]providers.Message, 0, len(live))
	next := 0
	for i, m := range request {
		if i == 0 && m.Role == "system" && len(live) > 0 && live[0].Role == "system" {
			out = append(out, m)
			next = 1
			continue
		}
		for j := next; j < len(live); j++ {
			if sameArchiveIdentity(live[j], m) {
				out = append(out, m)
				next = j + 1
				break
			}
		}
	}
	return out
}

// checkpointRequest is the send-seam checkpoint: the callLLM closure inside
// callLLMWithRetries is the one real choke-point every actual provider-send
// attempt funnels through (first send, retries, PDF fallback, media
// downgrade, empty-response retry), so it is the only production caller —
// one post-assembly pre-send measurement per actual attempt (MAJ-CW-004).
// Neither outgoing nor ongoing messages change if persistence fails.
// countOverflow gates contextResidueOverflowsTotal: production always passes
// true; a direct harness drive that must not count the overflow passes false.
func (rq *agentLoopRunTurnRequest) checkpointRequest(countOverflow bool) error {
	rt := rq.ri.rf.rt
	if err := rt.ts.contextWindowError(); err != nil {
		return err
	}
	if err := validateWindowGroups(rq.ri.rf.callMessages); err != nil {
		return err
	}
	if err := rt.ts.validateWindowControls(rq.ri.rf.callMessages); err != nil {
		return err
	}
	candidate, changed, err := rt.al.checkpointWindow(rt.turnCtx, rt.ts, rq.ri.rf.callMessages, rq.ri.rf.providerToolDefs, false)
	if err != nil {
		return err
	}
	live := retainLiveWindow(rq.ri.messages, candidate)
	if !rt.ts.opts.NoHistory && !rt.ts.agent.budgetChecksExempt() {
		cs := config.DefaultContextSettings()
		if cfg := rt.al.GetConfig(); cfg != nil {
			cs = cfg.Context
		}
		budget := agentContextBudget(rt.ts.agent)
		window, _, _ := rt.ts.agent.windowSnapshot()
		shareLimit := toolResultShareLimit(cs, window)
		// Only request-only additions caused this residue when the surviving
		// live window fits both bounds. Share-only live pressure is not notes.
		if countOverflow &&
			(requestTokens(candidate, rq.ri.rf.providerToolDefs) > budget || toolResultShareTokens(candidate) > shareLimit) &&
			requestTokens(live, rq.ri.rf.providerToolDefs) <= budget && toolResultShareTokens(live) <= shareLimit {
			contextResidueOverflowsTotal.Add(1)
		}
	}
	if changed {
		rq.ri.messages = live
	}
	rq.ri.rf.callMessages = candidate
	return nil
}

func (ts *turnState) validateWindowControls(msgs []providers.Message) error {
	ts.mu.RLock()
	controls := append([]providers.Message(nil), ts.windowControls...)
	ts.mu.RUnlock()
	next := 0
	for _, control := range controls {
		found := false
		for next < len(msgs) {
			m := msgs[next]
			next++
			if sameArchiveIdentity(control, m) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("context request: unconsumed control is missing or reordered")
		}
	}
	return nil
}

// A successful real provider call proves send; dial failures and context
// rejections keep protection across retries without redefining receipts.
func (ts *turnState) completeWindowRequest() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.windowControls = nil
	ts.windowNotice = contextReliefNotice{}
}
