package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// Spec regression table: nil usage still costs nothing. Exercise the real
// per-call accounting method used by logCompletedCall/debitLLMUsage, not the
// uncalled estimate wrapper. The priced control uses User Story 5's fixture
// arithmetic, so an unpriced/always-zero accounting path cannot pass unnoticed.
// No pre-implementation behavioral RED is claimed for this correction;
// independent GREEN verification and production mutations are deferred to CHECK.
func TestAccountProviderCall_NilUsageAddsNoCost(t *testing.T) {
	cat, err := catalog.NewCatalog([]byte(backendCallCatalog))
	require.NoError(t, err, "load the spec-priced catalog fixture")
	previous := providers.ProviderCatalog()
	providers.SetCatalog(cat)
	t.Cleanup(func() { providers.SetCatalog(previous) })

	for _, tc := range []struct {
		name   string
		seeded bool
	}{
		{name: "fresh turn"},
		{name: "after a priced call", seeded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := &turnState{agentID: "nil-usage-agent", turnID: "nil-usage-turn"}
			wantPending := 1 // the nil-usage call itself
			if tc.seeded {
				usage := &providers.UsageInfo{
					PromptTokens: 1000, CacheReadTokens: 8000, CacheWriteTokens: 2000,
					CompletionTokens: 500, TotalTokens: 11500,
				}
				entry, price := ts.accountProviderCall("anthropic", "cache-model", usage)
				// Spec: .003 + .0024 + .0075 + .0075 = .0204 USD.
				// Only floating-point sum rounding is tolerated; nil uses exact zero.
				require.InDelta(t, 0.0204, entry.Cost, 1e-15, "priced control must debit the catalog's actual nonzero cost")
				require.Equal(t, entry.Cost, price.Cost, "stored and returned priced control agree")
				require.Equal(t, "priced", price.BillingClass, "control must not pass via missing-price zero")
				tokens, cost := ts.GetTurnStats()
				require.Equal(t, int64(11500), tokens, "priced control really updates turn accounting")
				require.Equal(t, entry.Cost, cost, "priced control really updates turn cost")
				wantPending++
			}

			beforeTokens, beforeCost := ts.GetTurnStats()
			beforeRead, beforeWrite := ts.GetTurnCacheStats()
			beforePrompt, beforeOutput := ts.GetTurnIOStats()
			beforeUsage := ts.GetLastUsage()

			entry, price := ts.accountProviderCall("anthropic", "cache-model", nil)
			require.Zero(t, entry.Cost, "missing usage adds exactly no cost to the real stored call")
			require.Zero(t, price.Cost, "missing usage returns exactly no priced cost")
			require.Equal(t, []int{0, 0, 0, 0, 0, 0, 0, 0}, []int{
				entry.Tokens, entry.PromptTokens, entry.CompletionTokens,
				entry.CacheReadTokens, entry.CacheWriteTokens,
				entry.CacheWrite5mTokens, entry.CacheWrite1hTokens, entry.UnpricedTokens,
			}, "a missing usage object cannot invent input, output, cache or unpriced token counts")

			tokens, cost := ts.GetTurnStats()
			require.Equal(t, beforeTokens, tokens, "nil usage must not debit tokens")
			require.Equal(t, beforeCost, cost, "nil usage must neither add cost nor erase a previous priced cost")
			read, write := ts.GetTurnCacheStats()
			require.Equal(t, []int{beforeRead, beforeWrite}, []int{read, write}, "nil usage leaves cache totals unchanged")
			prompt, output := ts.GetTurnIOStats()
			require.Equal(t, []int{beforePrompt, beforeOutput}, []int{prompt, output}, "nil usage leaves input/output totals unchanged")
			require.Equal(t, beforeUsage, ts.GetLastUsage(), "missing usage is not backfilled as a known zero split")

			pending, seen := ts.takeCallAccounting()
			require.True(t, seen, "the real accounting path recorded this call")
			require.Len(t, pending, wantPending, "one accounting entry per actual call")
			require.Equal(t, entry, pending[len(pending)-1], "the pending persisted call carries the same zero-cost accounting")
		})
	}
}
