package agent

import (
	"context"
	"errors"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

const asyncNotifyAttemptBudget = 3

// asyncNotifyPublishTimeout bounds each individual publish attempt and
// asyncNotifyBackoffUnit scales the wait between attempts (attempt 1 waits one
// unit, attempt 2 waits two). Package vars, not consts, for the same reason as
// judgeSleepFn (judge.go): the real-callback contract tests —
// TestAsyncNotifierB3ProducerContract,
// TestGateFix1FinishedCallbackOriginReadDiagnostics and
// TestGateFix1FinishedCallbackReportsSecondaryNoticeSaveFailure — need three
// REAL delivery attempts through the real finished-tool callback against a
// full inbound queue, which blocks ~18s per callback on the shipped
// 5s/1s/2s values. Those tests shorten both values and restore them via
// t.Cleanup; production never writes them, so the defaults below are the
// shipped behaviour unchanged.
var (
	asyncNotifyPublishTimeout = 5 * time.Second
	asyncNotifyBackoffUnit    = time.Second
)

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
			if err := sleepWithContext(ctx, time.Duration(attempts)*asyncNotifyBackoffUnit); err != nil {
				return attempts, errors.Join(failedErr, err)
			}
		}
		if err := ctx.Err(); err != nil {
			return attempts, errors.Join(failedErr, err)
		}
		publishCtx, cancel := context.WithTimeout(ctx, asyncNotifyPublishTimeout)
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
