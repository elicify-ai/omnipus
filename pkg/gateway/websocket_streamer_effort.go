// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// websocket_streamer_effort.go — the live done frame's effort fields
// (effort-levels spec FR-024, FR-058, S-15, S-75): derived from the per-call
// accounting entries the agent loop hands the streamer, never reconstructed
// from configuration.

import (
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// turnEffortRecord is the answering (last) provider call's effort record, or
// nil when the turn made no call or the call carried none.
func turnEffortRecord(calls []session.TranscriptEntry) *generated.EffortRecord {
	if len(calls) == 0 {
		return nil
	}
	return calls[len(calls)-1].Effort
}

// turnReasoningSum is the turn sum of the reasoning-token counts the calls
// reported: nil when no call reported one, 0 when they reported zero.
func turnReasoningSum(calls []session.TranscriptEntry) *int {
	var sum *int
	for _, c := range calls {
		if c.ReasoningTokens == nil {
			continue
		}
		n := *c.ReasoningTokens
		if sum != nil {
			n += *sum
		}
		sum = &n
	}
	return sum
}
