package session

// SetEntryCallAccounting copies only the immutable accounting facts of one
// provider call. Narration, message identity, truncation and ownership stay with
// the producer. A zero call clears a terminal notice's duplicate turn totals.
func SetEntryCallAccounting(entry *TranscriptEntry, call TranscriptEntry) {
	entry.ProviderID = call.ProviderID
	entry.ModelID = call.ModelID
	if call.ModelID != "" {
		entry.Model = call.ModelID
	}
	entry.BillingClass = call.BillingClass
	entry.UnpricedTokens = call.UnpricedTokens
	entry.Tokens = call.Tokens
	entry.Cost = call.Cost
	entry.PromptTokens = call.PromptTokens
	entry.CompletionTokens = call.CompletionTokens
	entry.CacheReadTokens = call.CacheReadTokens
	entry.CacheWriteTokens = call.CacheWriteTokens
	entry.CacheWrite5mTokens = call.CacheWrite5mTokens
	entry.CacheWrite1hTokens = call.CacheWrite1hTokens
}
