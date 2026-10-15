package agent

import (
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/promptcache"
	"github.com/google/uuid"
)

// estimateLLMCallCost uses the exact provider/model catalog pair. A missing
// provider or price is unpriced; subscription tokens never acquire API dollars.
func estimateLLMCallCost(model string, usage *providers.UsageInfo, providerID ...string) float64 {
	if usage == nil {
		return 0
	}
	id := ""
	if len(providerID) > 0 {
		id = providerID[0]
	}
	return priceLLMUsage(id, model, *usage).Cost
}

func priceLLMUsage(providerID, model string, usage providers.UsageInfo) promptcache.PriceResult {
	return promptcache.Price(promptcache.PriceInput{
		ProviderID: providerID,
		Cost:       providers.ProviderCatalog().Resolve(providerID, model).Cost(),
		Usage:      usage,
	})
}

// debitLLMUsage is the single accounting path for delivered and refused usage.
// Provider identity is captured at the attempt boundary, never inferred from a
// later agent configuration. A fallback's produced model wins over the request.
func (al *AgentLoop) debitLLMUsage(ts *turnState, llmModel string, usage *providers.UsageInfo) {
	if ts == nil || usage == nil {
		return
	}
	ts.mu.RLock()
	providerID, producedModel := ts.lastProducedProvider, ts.lastProducedModel
	ts.mu.RUnlock()
	if producedModel != "" {
		llmModel = producedModel
	}
	price := priceLLMUsage(providerID, llmModel, *usage)
	ts.SetLastUsage(usage)
	ts.AddTurnStats(int64(usage.TotalTokens), price.Cost)
	ts.AddTurnCacheStats(usage.CacheReadTokens, usage.CacheWriteTokens)
	ts.AddTurnIOStats(usage.PromptTokens, usage.CompletionTokens)
}

// logCompletedCall runs once at the real provider boundary, including a
// successful response with missing usage. Failed calls do not get this line.
func (rt *agentLoopRunTurn) logCompletedCall(providerID, model string, response *providers.LLMResponse) {
	var usage providers.UsageInfo
	if response.Usage != nil {
		usage = *response.Usage
	}
	price := priceLLMUsage(providerID, model, usage)
	logger.InfoC("agent", promptcache.FormatCallAccounting(promptcache.CallAccounting{
		ProviderID: providerID, ModelID: model, AgentID: rt.ts.agent.ID,
		SessionID: rt.ts.cacheSessionID(), CallID: uuid.NewString(),
		Usage: usage, Cost: price.Cost, BillingClass: price.BillingClass,
		OneHourRule: price.OneHourRule, UnpricedTokens: price.UnpricedTokens,
	}))
}
