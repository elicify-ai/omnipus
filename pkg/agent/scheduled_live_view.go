// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import "github.com/elicify-ai/omnipus/pkg/session"

// scheduled_live_view.go — #1442: a scheduled or heartbeat run's prompt and
// answer reach the viewers already bound to its session, live, without a
// reload. Two halves, both read only by the live view:
//
//   - publishScheduledPrompt emits EventKindUserMessagePersisted for the
//     prompt ProcessScheduled has just appended to the transcript; the
//     gateway's session-hub sync tap turns it into one user_message frame.
//   - streamerChannel lets the turn's streamer lookups use the webchat
//     session hub for a WebchatLiveStream turn although no channel owns it.

// publishScheduledPrompt publishes a scheduled/heartbeat prompt that is
// ALREADY persisted (entry is the appended transcript entry). The caller
// must call it strictly after a successful append, so the live frame can
// never precede its durable copy (the interactive path's publish-after-append
// rule), and before the turn runs, so the frame is numbered ahead of every
// answer token. Interactive turns never call it: their echo is the gateway's
// publishUserMessage, so no turn is echoed twice.
func (al *AgentLoop) publishScheduledPrompt(ownerAgentID, sessionKey, sessionID string, entry session.TranscriptEntry) {
	al.emitEvent(EventKindUserMessagePersisted,
		EventMeta{AgentID: ownerAgentID, SessionKey: sessionKey, Source: "ProcessScheduled"},
		UserMessagePersistedPayload{SessionID: sessionID, Entry: entry})
}

// streamerChannel is the channel identity this turn's live-streamer lookups
// use: loop_run_turn_attempt.go's bus.GetStreamer call,
// ensureFinalWebchatStreamer, and the iteration-limit notice's terminal
// streamer. Normally the turn's own channel; a WebchatLiveStream turn (a
// scheduled/heartbeat run whose session renders in the web UI) streams
// through the webchat session hub although no channel owns it. Deliberately
// NOT ts.channel: channel-keyed side effects (goal routing records, connector
// intake/egress, tool-media delivery, response sends) keep seeing the run as
// channel-less, exactly as before #1442.
func (ts *turnState) streamerChannel() string {
	if ts.opts.WebchatLiveStream {
		return "webchat"
	}
	return ts.opts.Channel
}
