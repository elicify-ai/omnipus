// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// external_live_view.go — the live view of a steered external-CLI helper turn
// (#1895).
//
// A native helper's text reaches its open chat through the webchat streamer:
// token frames while the text arrives and one done frame when the turn ends
// (turn_stream.go::finalizeStreamer). The external-CLI body wrote the same text
// to the transcript only, so a helper chat showed the answer and the failure
// only after a reload. These helpers give the external body the native shape:
// each CLI text segment is sent live under the id of the row it is saved as,
// and the body ends the turn with exactly one done (failed when the run
// failed), after one error frame for a failure nothing has announced yet.
package agent

import (
	"context"
	"errors"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// recordExternalOutput saves one external-CLI text segment as its own
// assistant row. For a steered helper turn (externalLiveOutput) the segment is
// first sent live under a freshly minted round id, and the row is saved under
// that same id, so a reload never shows the text twice. The round is then
// closed, so later rows of the run (a diff, a permission record, an error
// marker) never reuse the text's id.
func (ts *turnState) recordExternalOutput(al *AgentLoop, text, model string) {
	if !ts.externalLiveOutput {
		ts.appendIntermediateAssistantTranscript(text, model)
		return
	}
	ts.nextRoundMessageID()
	if streamer, ok := ts.acquireExternalStreamer(al); ok {
		ts.markLastStreamerProducedModel(model)
		if err := streamer.Update(ts.liveViewContext(), text); err != nil {
			// The row below is still saved; the client catches up by id.
			logger.DebugCF("agent", "External CLI live text update error (client may have disconnected)",
				map[string]any{"session_id": ts.transcriptSessionID, "error": err.Error()})
		}
	}
	ts.appendIntermediateAssistantTranscript(text, model)
	ts.closeExternalOutputRound()
}

// closeExternalOutputRound ends the current external text round: the next
// intermediate row mints its own id instead of reusing the text's.
func (ts *turnState) closeExternalOutputRound() {
	ts.mu.Lock()
	ts.currentMessageID = ""
	ts.mu.Unlock()
}

// acquireExternalStreamer obtains the turn's webchat streamer for the current
// round, stamped exactly like the native streaming call site does
// (loop_run_turn_attempt.go), and records it as the turn's last streamer.
func (ts *turnState) acquireExternalStreamer(al *AgentLoop) (bus.Streamer, bool) {
	if al == nil || al.bus == nil || ts.streamerChannel() != "webchat" || ts.transcriptSessionID == "" {
		return nil, false
	}
	streamer, ok := al.bus.GetStreamer(ts.liveViewContext(), ts.streamerChannel(), ts.opts.ChatID, ts.transcriptSessionID)
	if !ok {
		return nil, false
	}
	ts.stampStreamerProducerAgentID(streamer)
	ts.stampStreamerTurnID(streamer)
	ts.stampStreamerParentSpawnCallID(streamer)
	ts.stampStreamerGoalID(streamer)
	ts.stampStreamerMessageID(streamer)
	ts.setLastStreamer(streamer)
	return streamer, true
}

// liveViewContext is the turn's own context, or Background when none is set.
// A canceled run context must not stop the done frame of the turn it ended.
func (ts *turnState) liveViewContext() context.Context {
	if ts.ctx != nil {
		return ts.ctx
	}
	return context.Background()
}

// finishExternalLiveView ends a steered external-CLI turn's live view: one
// error frame for a failure not yet announced, then exactly one done (marked
// failed when the run failed), with every text row already saved so the
// streamer writes nothing. A Stop or a steer interrupt (context.Canceled) is
// not a failure and gets no error frame, as on the native path. It returns
// the streamer's own finalize error, if any.
func (al *AgentLoop) finishExternalLiveView(ctx context.Context, ts *turnState, runErr error) error {
	failed := runErr != nil && !errors.Is(runErr, context.Canceled)
	if failed && !ts.externalErrorAnnounced.Load() {
		al.emitExternalErrorFrame(ts, "external_cli", TranslateTurnError(runErr))
	}
	if failed {
		ts.markTurnFailed()
	}
	ts.mu.RLock()
	active := ts.lastStreamer != nil
	ts.mu.RUnlock()
	if !active {
		// No text was streamed: the done carries no message id, since no
		// assistant row of this turn would match it.
		if _, ok := ts.acquireExternalStreamer(al); !ok {
			return nil
		}
	}
	ts.markLastStreamerTranscriptPersisted()
	return ts.finalizeStreamer(ctx)
}

// emitExternalErrorFrame emits the typed error frame for an external-CLI exit
// and records that this turn's failure was announced. Like the native
// emitErrorEvent it addresses the run's OWN session (u9ToolExecSessionIDs) and
// honours the typed-error audience; for a steered helper that is the helper's
// chat, where its viewers are subscribed, not the cascade root's (#1895,
// ADR-091 D1). ChatID falls back to the turn's chat id when the options carry
// none, as the external exits always did.
func (al *AgentLoop) emitExternalErrorFrame(ts *turnState, stage string, llm LLMError) {
	ts.externalErrorAnnounced.Store(true)
	sessionID := u9ToolExecSessionIDs(ts)
	if al.audienceFor(context.Background(), steer.BoundaryTypedErrorFrame, sessionID) == steer.AudienceNone {
		return
	}
	chatID := ts.opts.ChatID
	if chatID == "" {
		chatID = ts.chatID
	}
	al.emitEvent(EventKindError, ts.eventMeta("runTurn", "turn.error"), ErrorPayload{
		Stage:     stage,
		ChatID:    chatID,
		SessionID: sessionID,
		Code:      string(llm.Code),
		Message:   llm.Message,
	})
}
