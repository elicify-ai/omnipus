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

// disposeReplacedPairing keeps the exact write-captured handle for its entire
// lifetime. Exhausting bounded retries is not resource completion: the final
// background wait survives response deadlines, later writes and deletion.
func (al *AgentLoop) disposeReplacedPairing(store *channels.PairingAttempts, p channels.PairingProgress) {
	attempt := p.Attempt
	delays := append([]time.Duration(nil), pairingStopRetryDelays...)
	for i := 0; ; i++ {
		if i > 0 {
			var delay time.Duration
			if i <= len(delays) {
				delay = delays[i-1]
			} else if i > len(delays)+1 {
				delay = time.Minute // persistent errors retry without a busy loop
			}
			time.Sleep(delay)
		}
		ctx := context.Background()
		cancel := func() {}
		if i <= len(delays) {
			ctx, cancel = context.WithTimeout(ctx, pairingStopTimeout)
		}
		err := p.Dispose(ctx)
		cancel()
		done, finished := store.FinishReplacement(attempt.Instance, attempt.AttemptID, attempt.StopGeneration, err)
		if err == nil {
			if finished {
				al.publishPairingTransition(done, channels.PairingStatusError, channels.PairingInterruptedMessage)
			}
			return // only the captured resource's actual completion releases its barrier
		}
		logger.ErrorCF("agent", "Replaced pairing cleanup unfinished; wait for completion before starting another pairing",
			map[string]any{"channel_id": attempt.Instance, "attempt_id": attempt.AttemptID,
				"error": err.Error(), "bounded_retries_exhausted": i >= len(delays),
				"action": "Check the server log and resolve the reported client cleanup problem; Omnipus retains this exact cleanup and keeps waiting. Do not press Retry until cleanup finishes."})
		if i == len(delays) {
			al.publishPairingTransition(attempt, channels.PairingStatusError, channels.PairingStopPendingMessage)
		}
	}
}
