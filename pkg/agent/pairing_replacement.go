// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"time"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// disposeReplacedPairing uses the handle captured at the successful write,
// never Manager.GetChannel(name). Settings/delete cannot release an earlier
// explicit Stop's barrier, and a delayed cleanup cannot act on its successor.
func (al *AgentLoop) disposeReplacedPairing(store *channels.PairingAttempts, p channels.PairingProgress) {
	attempt := p.Attempt
	for i := 0; ; i++ {
		if i > 0 {
			time.Sleep(pairingStopRetryDelays[i-1])
		}
		ctx, cancel := context.WithTimeout(context.Background(), pairingStopTimeout)
		err := p.Dispose(ctx)
		cancel()
		store.FinishReplacement(attempt.Instance, attempt.AttemptID, attempt.StopGeneration, err)
		if err == nil {
			return // the exact captured resource joined; no successor lookup
		}
		logger.ErrorCF("agent", "Replaced pairing teardown did not finish; its resource keeps the stopping exclusion",
			map[string]any{"channel_id": attempt.Instance, "attempt_id": attempt.AttemptID, "error": err.Error()})
		if i >= len(pairingStopRetryDelays) {
			return
		}
	}
}
