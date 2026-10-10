package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/constants"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Session-core U8 (FR-028, architect A2): in a MAIN session a connector is
// reached ONLY by send_message reply_to=<request id>. A main absorbs input from
// several senders, so neither a plain reply nor streaming may be sent to "the
// connector" - that would fall back to the last or first sender. The default
// send is refused once, visibly, and the agent is prompted once to answer each
// sender explicitly.
//
// Trust decisions (security review):
//
//	A2-1  The refusal sends nothing to any connector; the plain text stays in the
//	      main's transcript (web viewers see the main anyway - #1206).
//	A2-2  The prompt is a server-composed, non-human inbound on the webchat
//	      channel, so it can neither revive a stopped main (not UserInitiated)
//	      nor re-trigger itself: its own plain reply goes to webchat, where the
//	      rule does not apply.
//	A2-3  Unbound instances and non-main sessions are untouched.

// connectorDefaultSendRefusals counts refused default sends.
var connectorDefaultSendRefusals atomic.Int64

// isConnectorChannel reports whether channel names a real connector instance
// (not the web, the engine's system channel or an internal channel).
func isConnectorChannel(channel string) bool {
	c := strings.TrimSpace(channel)
	return c != "" && c != "webchat" && c != "system" && !constants.IsInternalChannel(c)
}

// mainConnectorTurn reports whether a turn on (channel, sessionID) runs in a
// main session fed by a connector - the only case the reply_to-only rule covers.
func (al *AgentLoop) mainConnectorTurn(channel, sessionID string) bool {
	if !isConnectorChannel(channel) || sessionID == "" {
		return false
	}
	store := al.GetSessionStore()
	if store == nil {
		return false
	}
	meta, err := store.GetMeta(sessionID)
	return err == nil && meta != nil && meta.Type == session.SessionTypeMain
}

// sessionErrorPublisher is the optional gateway seam that shows an error frame to
// the viewers of a session. A fake or absent publisher only loses the frame.
type sessionErrorPublisher interface {
	PublishSessionError(sessionID, message string)
}

// refuseMainConnectorDefaultSend refuses the default send of a main's connector
// turn: nothing goes to the connector, the main's viewers see an error frame and
// the agent is prompted once.
func (al *AgentLoop) refuseMainConnectorDefaultSend(ctx context.Context, channel, sessionID string, ag *AgentInstance) {
	connectorDefaultSendRefusals.Add(1)
	text := fmt.Sprintf("Not sent to %s: answer each sender with send_message reply_to=<request id>.", channel)
	logger.InfoCF("agent", "main connector default send refused",
		map[string]any{"channel": channel, "session_id": sessionID})
	if deps := al.loadAddressDeps(); deps != nil {
		if p, ok := deps.(sessionErrorPublisher); ok {
			p.PublishSessionError(sessionID, text)
		}
	}
	agentID := ""
	if ag != nil {
		agentID = ag.ID
	}
	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := al.bus.PublishInbound(pubCtx, bus.InboundMessage{
		Channel:   "webchat",
		ChatID:    "reply-refusal:" + sessionID,
		Sender:    bus.SenderInfo{CanonicalID: "reply-refusal:" + sessionID},
		Content:   text + " Your last reply was not delivered to anyone outside this conversation.",
		SessionID: sessionID,
		Metadata:  map[string]string{"agent_id": agentID, "reply_refusal_note": "1"},
		// Not UserInitiated: it must never revive a stopped main.
	}); err != nil {
		logger.WarnCF("agent", "could not prompt the agent to address its senders",
			map[string]any{"session_id": sessionID, "error": err.Error()})
	}
}

// returnRefusalNotices counts refusals made visible to the responding side.
var returnRefusalNotices atomic.Int64

// ReportReturnRefusal makes a refused captured return visible (BDD-08.9, N1): the
// final egress check refused to send an answer because the instance was rebound,
// unbound or deleted since the request arrived. The responder's main shows one
// error frame; nothing else is sent anywhere. The text is curated - no route,
// instance or owner detail.
func (al *AgentLoop) ReportReturnRefusal(msg bus.OutboundMessage, _ error) {
	if msg.Return == nil || msg.Return.SourceSessionID == "" {
		return
	}
	returnRefusalNotices.Add(1)
	deps := al.loadAddressDeps()
	if deps == nil {
		return
	}
	if p, ok := deps.(sessionErrorPublisher); ok {
		p.PublishSessionError(msg.Return.SourceSessionID,
			"An answer was not delivered: the chat it was for is no longer connected to this agent.")
	}
}
