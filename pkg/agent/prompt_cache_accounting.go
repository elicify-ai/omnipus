package agent

import (
	"errors"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/providers/promptcache"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/google/uuid"
)

func priceLLMUsage(providerID, model string, usage providers.UsageInfo) promptcache.PriceResult {
	return promptcache.Price(promptcache.PriceInput{
		ProviderID: providerID,
		Cost:       providers.ProviderCatalog().Resolve(providerID, model).Cost(),
		Usage:      usage,
	})
}

// turnCallAccounting is protected by turnState.mu. Each entry is priced once
// at the actual provider boundary and later joined to its sanitized narration.
type turnCallAccounting struct {
	pending        []session.TranscriptEntry
	seen           bool
	err            error
	continuationID string
}

// accountProviderAttempt owns accounting at the real call boundary, before a
// retry/fallback can replace the failed candidate's identity or hide its usage.
// Recovery/classification must not debit this same attempt a second time.
func (rt *agentLoopRunTurn) accountProviderAttempt(providerID, model string, response *providers.LLMResponse, err error) {
	if err == nil {
		if response != nil {
			rt.logCompletedCall(providerID, model, response)
		}
		return
	}
	var usage *providers.UsageInfo
	var streamErr *common.StreamProviderError
	var argumentsErr *common.ToolArgumentsError
	if errors.As(err, &streamErr) {
		usage = streamErr.Usage
	} else if errors.As(err, &argumentsErr) {
		usage = argumentsErr.Usage
	}
	if usage != nil {
		rt.ts.accountProviderCall(providerID, model, usage)
	}
}

func (ts *turnState) accountProviderCall(providerID, model string, usage *providers.UsageInfo) (session.TranscriptEntry, promptcache.PriceResult) {
	var u providers.UsageInfo
	if usage != nil {
		u = *usage
	}
	price := priceLLMUsage(providerID, model, u)
	if usage == nil && price.BillingClass != promptcache.BillingClassSubscription {
		price.BillingClass = promptcache.BillingClassUnpriced
	}
	entry := session.TranscriptEntry{
		ID: uuid.NewString(), Role: "assistant", AgentID: ts.resolveActiveAgentID(), TurnID: ts.turnID, GoalID: ts.goalID,
		Timestamp: time.Now().UTC(), ProviderID: providerID, Model: model, ModelID: model,
		BillingClass: price.BillingClass, UnpricedTokens: price.UnpricedTokens,
		Tokens: u.TotalTokens, Cost: price.Cost, PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens,
		CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens,
		CacheWrite5mTokens: u.CacheWrite5mTokens, CacheWrite1hTokens: u.CacheWrite1hTokens,
	}
	if usage != nil {
		ts.SetLastUsage(usage)
		ts.AddTurnStats(int64(u.TotalTokens), price.Cost)
		ts.AddTurnCacheStats(u.CacheReadTokens, u.CacheWriteTokens)
		ts.AddTurnIOStats(u.PromptTokens, u.CompletionTokens)
		// The group-chat allocation (§6.14) reads the same call through the
		// one accounting path: the installed room pricer, else this price.
		ts.opts.roomCost.addCall(ts.turnID, model, usage, price)
	}
	ts.mu.Lock()
	ts.callAccounting.pending = append(ts.callAccounting.pending, entry)
	ts.callAccounting.seen = true
	ts.mu.Unlock()
	return entry, price
}

// logCompletedCall captures price/classification once for both log and storage.
// No provider payload string is retained. Failed calls use the typed debit path.
func (rt *agentLoopRunTurn) logCompletedCall(providerID, model string, response *providers.LLMResponse) {
	entry, price := rt.ts.accountProviderCall(providerID, model, response.Usage)
	var usage providers.UsageInfo
	if response.Usage != nil {
		usage = *response.Usage
	}
	if response.Content == "" && !isTruncatedFinishReason(response.FinishReason) && !rt.ts.abandoned.Load() && rt.ts.transcriptStore != nil && rt.ts.transcriptSessionID != "" {
		// No-text calls own an invisible accounting entry immediately. A later
		// synthetic terminal notice must not inherit their provider attribution.
		if err := rt.ts.persistLatestCallAccounting(); err != nil {
			rt.ts.setCallAccountingError(err)
		}
	}
	logger.InfoC("agent", promptcache.FormatCallAccounting(promptcache.CallAccounting{
		ProviderID: providerID, ModelID: model, AgentID: rt.ts.agent.ID,
		SessionID: rt.ts.cacheSessionID(), CallID: entry.ID, Usage: usage, Cost: entry.Cost,
		BillingClass: entry.BillingClass, OneHourRule: price.OneHourRule, UnpricedTokens: entry.UnpricedTokens,
	}))
}

func (ts *turnState) pendingCallAccounting() bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return len(ts.callAccounting.pending) > 0
}

func (ts *turnState) takeCallAccounting() ([]session.TranscriptEntry, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	pending := ts.callAccounting.pending
	ts.callAccounting.pending = nil
	return pending, ts.callAccounting.seen
}

func (ts *turnState) callAccountingError() error {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.callAccounting.err
}

func (ts *turnState) setCallAccountingError(err error) {
	if err == nil {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.callAccounting.err == nil {
		ts.callAccounting.err = err
	}
}

func (ts *turnState) persistAssistantAccounting(entry session.TranscriptEntry) error {
	if err := ts.callAccountingError(); err != nil {
		return err
	}
	pending, seen := ts.takeCallAccounting()
	if seen {
		// An already-accounted narration followed by a synthetic terminal
		// notice must not debit the whole turn again.
		session.SetEntryCallAccounting(&entry, session.TranscriptEntry{})
	}
	if len(pending) > 0 {
		for _, older := range pending[:len(pending)-1] {
			if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, older); err != nil {
				ts.setCallAccountingError(err)
				return err
			}
		}
		session.SetEntryCallAccounting(&entry, pending[len(pending)-1])
	}
	if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, entry); err != nil {
		ts.setCallAccountingError(err)
		return err
	}
	return nil
}

// persistLatestCallAccounting writes only the completed no-text call. Earlier
// continuation calls keep their accounting until their own narration settles.
func (ts *turnState) persistLatestCallAccounting() error {
	if err := ts.callAccountingError(); err != nil {
		return err
	}
	ts.mu.Lock()
	n := len(ts.callAccounting.pending)
	if n != 1 {
		// A saved continuation must settle first, in its original order.
		ts.mu.Unlock()
		return nil
	}
	entry := ts.callAccounting.pending[0]
	ts.callAccounting.pending = nil
	ts.mu.Unlock()
	if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, entry); err != nil {
		ts.setCallAccountingError(err)
		return err
	}
	return nil
}

func (ts *turnState) markContinuationCallAccounting() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if n := len(ts.callAccounting.pending); n > 0 {
		ts.callAccounting.continuationID = ts.callAccounting.pending[n-1].ID
	}
}

// holdFollowingCallAccounting leaves only the calls that produced the saved
// continuation prefix pending while that prefix is written. The following
// round keeps its own accounting for its later narration or empty-call entry.
func (ts *turnState) holdFollowingCallAccounting() func() {
	ts.mu.Lock()
	end := 0
	for i, entry := range ts.callAccounting.pending {
		if entry.ID == ts.callAccounting.continuationID {
			end = i + 1
			break
		}
	}
	following := append([]session.TranscriptEntry(nil), ts.callAccounting.pending[end:]...)
	ts.callAccounting.pending = ts.callAccounting.pending[:end]
	ts.callAccounting.continuationID = ""
	ts.mu.Unlock()
	return func() {
		ts.mu.Lock()
		ts.callAccounting.pending = append(ts.callAccounting.pending, following...)
		ts.mu.Unlock()
	}
}

func (ts *turnState) flushPendingCallAccounting() error {
	if !ts.pendingCallAccounting() || ts.transcriptStore == nil || ts.transcriptSessionID == "" {
		return nil
	}
	return ts.persistAssistantAccounting(session.TranscriptEntry{ID: uuid.NewString(), Role: "assistant", AgentID: ts.agent.ID, TurnID: ts.turnID, GoalID: ts.goalID, Timestamp: time.Now().UTC()})
}
