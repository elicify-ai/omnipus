package agent

import (
	"errors"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// enqueueGoalReminderPause runs on the shared keeper, so backpressure must never
// wait for a consumer that may already have stopped. A successful enqueue is
// remembered only in the existing process-local goal state. A rejected enqueue
// leaves that marker unset: the next paused-goal observation retries the same
// durable notice without counting another reminder or duplicating its chat row.
func (al *AgentLoop) enqueueGoalReminderPause(sessionID, goalID, noticeID, channel, chatID, content string) error {
	work := goalReminderWorkFor(goalID)
	work.mu.Lock()
	defer work.mu.Unlock()
	if work.pauseNoticeEnqueued == noticeID {
		return nil
	}
	if !al.bus.TryPublishOutbound(bus.OutboundMessage{
		Channel: channel, ChatID: chatID, SessionID: sessionID, Content: content,
	}) {
		return errors.New("goal reminder pause: outbound queue is full or closed; notice remains pending")
	}
	work.pauseNoticeEnqueued = noticeID
	return nil
}
