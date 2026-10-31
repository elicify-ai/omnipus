package agent

import (
	"context"
	"errors"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

const asyncNotifyAttemptBudget = 3

// publishAsyncNotify retries the one prepared handoff, never the work. A caller
// deadline bounds each child; a child timeout alone does not end a live caller.
func (n *asyncNotifierImpl) publishAsyncNotify(ctx context.Context, event AsyncNotifyEvent, noticeID string, message bus.InboundMessage) (int, error) {
	attempts := 0
	var lastErr error
	var failedErr error
	for attempts < asyncNotifyAttemptBudget {
		if err := ctx.Err(); err != nil {
			return attempts, errors.Join(failedErr, err)
		}
		if attempts > 0 {
			if err := sleepWithContext(ctx, time.Duration(attempts)*time.Second); err != nil {
				return attempts, errors.Join(failedErr, err)
			}
		}
		if err := ctx.Err(); err != nil {
			return attempts, errors.Join(failedErr, err)
		}
		publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		attempts++
		lastErr = n.loop.bus.PublishInbound(publishCtx, message)
		cancel()
		if lastErr == nil {
			return attempts, nil // an accepted send wins even if the caller just ended
		}
		failedErr = errors.Join(failedErr, lastErr)
		fields := asyncNotifyDiagnosticFields(event, noticeID)
		fields["attempt"] = attempts
		fields["attempt_budget"] = asyncNotifyAttemptBudget
		fields["status"] = "final"
		canRetry := attempts < asyncNotifyAttemptBudget && ctx.Err() == nil
		if canRetry {
			fields["status"] = "retry"
		}
		logger.WarnCF("agent", "Async notification publish failed", fields)
		if !canRetry {
			return attempts, errors.Join(failedErr, ctx.Err())
		}
	}
	return attempts, failedErr
}
