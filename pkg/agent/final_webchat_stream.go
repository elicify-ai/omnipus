package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// ensureFinalWebchatStreamer gives a complete plain-provider answer the same
// durable/live identity as streamed answers. It sends that already-available
// text once before done; no provider request or tool operation is repeated.
func (ts *turnState) ensureFinalWebchatStreamer(messageBus *bus.MessageBus, lookupDeclined bool) error {
	if lookupDeclined || ts.streamerChannel() != "webchat" || ts.transcriptSessionID == "" || messageBus == nil {
		return nil
	}
	ts.mu.RLock()
	active := ts.lastStreamer != nil
	ts.mu.RUnlock()
	if active {
		return nil
	}
	ctx := ts.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	streamer, ok := messageBus.GetStreamer(ctx, ts.streamerChannel(), ts.opts.ChatID, ts.transcriptSessionID)
	if !ok {
		return nil
	}
	ts.setLastStreamer(streamer)
	ts.stampStreamerProducerAgentID(streamer)
	ts.stampStreamerTurnID(streamer)
	ts.stampStreamerGoalID(streamer)
	ts.stampStreamerParentSpawnCallID(streamer)
	if ts.getCurrentMessageID() == "" {
		ts.nextRoundMessageID()
	}
	ts.stampStreamerMessageID(streamer)
	ts.markLastStreamerProducedModel(ts.lastProducedModel)
	return streamer.Update(ctx, ts.finalContent)
}
