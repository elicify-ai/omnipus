// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

// accumulateEntryStats folds one TranscriptEntry's token/cost/tool-call
// contribution into stats in place.
//
// This is the single implementation shared by PartitionStore.AppendMessage
// (daypartition.go), UnifiedStore.AppendTranscript (unified.go), and
// UnifiedStore.AppendTranscriptStrict (unified_api.go). Those three call
// sites carried a byte-identical ~20-line copy of this block until this
// extraction. The duplication was a deliberate but TEMPORARY constraint from
// the ADR-057 U2/U4/U5 coordination wave — each unit owned a different file
// and was told not to edit the others' concurrently-in-flight work ("a name,
// not a second behavior" — unified_api.go's original AppendTranscriptStrict
// doc comment) — not a design property to preserve once that wave finished.
// Each call site still owns its own meta.UpdatedAt assignment immediately
// after calling this helper: daypartition.go uses the entry's resolved
// partition timestamp (which equals entry.Timestamp by the time it gets
// there), the other two use entry.Timestamp directly — the same value in
// practice, kept as separate lines because they are logically the caller's
// concern, not this accumulator's.
//
// Token accounting convention (the provider input/output split). Before
// TranscriptEntry carried PromptTokens/CompletionTokens, TokensOut received
// the entry's whole turn TOTAL and TokensIn was fed only by non-assistant
// entries — which no production caller populates — so every session
// reported tokens_in=0 with the entire volume booked as output, and the
// per-model In/Out pair sat at 0 beside a non-zero Total. Now:
//
//   - hasSplit reports whether the provider's input/output split was
//     recorded on this entry at all (prompt>0 OR completion>0).
//   - When hasSplit, TokensIn/TokensOut (and the per-model In/Out) take the
//     provider's PromptTokens/CompletionTokens DIRECTLY — including when one
//     of the two is genuinely 0 (e.g. prompt=5000, completion=0 for a turn
//     that sent a large prompt but produced no completion tokens before
//     stopping). This is intentional, not a lossy fallback: production
//     callers always populate PromptTokens, CompletionTokens,
//     CacheReadTokens, and CacheWriteTokens together from the SAME provider
//     UsageInfo response (pkg/providers/protocoltypes/types.go's UsageInfo,
//     whose TotalTokens is documented to equal PromptTokens +
//     CompletionTokens + CacheReadTokens + CacheWriteTokens), so a
//     genuinely-zero split component is never "missing" data to recover —
//     any remainder of Total not covered by Prompt+Completion is accounted
//     for by cache, folded in separately below. Falling back to booking the
//     whole Total into TokensOut whenever ONE split component is zero would
//     reintroduce exactly the over-counting bug this convention exists to
//     fix, this time silently, only on turns with a genuinely-zero half of
//     the split. See TestAccumulateEntryStats_HasSplitBoundary for the
//     pinned cases.
//   - When NOT hasSplit (both are 0 — the legacy no-split case: entries
//     written before this convention existed, or a provider that reports
//     only a total), TokensOut falls back to the entry's whole Tokens total,
//     preserving historical behaviour for old transcripts and for any
//     provider path that never reports the split.
//   - CacheReadTokens/CacheWriteTokens are folded in unconditionally,
//     independent of hasSplit, since they are additive components of Total
//     alongside In and Out — never a subset of Out.
func accumulateEntryStats(stats *SessionStats, entry TranscriptEntry) {
	if entry.Role == "assistant" {
		promptTokens, completionTokens := entry.PromptTokens, entry.CompletionTokens
		hasSplit := promptTokens > 0 || completionTokens > 0

		if hasSplit {
			stats.TokensIn += promptTokens
			stats.TokensOut += completionTokens
		} else {
			stats.TokensOut += entry.Tokens
		}
		stats.TokensCacheRead += entry.CacheReadTokens
		stats.TokensCacheWrite += entry.CacheWriteTokens
		if entry.Model != "" {
			if stats.ByModel == nil {
				stats.ByModel = make(map[string]ModelTokens)
			}
			mt := stats.ByModel[entry.Model]
			mt.In += promptTokens
			mt.Out += completionTokens
			mt.CacheRead += entry.CacheReadTokens
			mt.CacheWrite += entry.CacheWriteTokens
			mt.Total += entry.Tokens
			stats.ByModel[entry.Model] = mt
		}
	} else {
		stats.TokensIn += entry.Tokens
	}
	stats.TokensTotal += entry.Tokens
	stats.Cost += entry.Cost
	stats.ToolCalls += len(entry.ToolCalls)
	if entry.Type == "" || entry.Type == EntryTypeMessage {
		stats.MessageCount++
	}
}
