// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"time"
)

// These tests cover the defect where a real 10-minute session recorded
// tokens_in=0 with 575,539 tokens booked entirely as output, and a per-model
// breakdown whose in/out sat at 0 beside a non-zero total. Cost attribution
// was computed from that split, so it could not have been right.

func assistantEntryWithSplit(model string, prompt, completion, total int) TranscriptEntry {
	return TranscriptEntry{
		ID:               "e-" + model,
		Role:             "assistant",
		Content:          "hi",
		Timestamp:        time.Now().UTC(),
		Model:            model,
		Tokens:           total,
		PromptTokens:     prompt,
		CompletionTokens: completion,
	}
}
