package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/constants"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Session-core U8 (C-ADDRESS, FR-028, ADR D7): input from a BOUND connector
// instance flows into the bound pair's MAIN session as a captured request, so
// every sender can be answered on its own chat/thread with send_message
// reply_to. Unbound instances keep today's per-chat channel sessions.
//
// Trust decisions (security review):
//
//	A1-1  Bound is decided from the operator's config only (the instance's
//	      workspace binding plus an agent-kind identity), the same test as
//	      resolveMessageRoute's boundInstance. Nothing in the message selects it.
//	A1-2  The capture's sender comes only from msg.Sender (authenticated by the
//	      channel adapter); its owner pair comes only from the instance config;
//	      its return route (instance, chat, platform message id, peer) comes
//	      only from the inbound envelope. Message text is never read for any of
//	      them.
//	A1-3  Eligibility (FR-003) and the user-message bound are checked before any
//	      write; a hidden pair never gets a main created for it.
//	A1-4  The capture is written before the transcript entry and discarded if
//	      the append fails, so a request the model can see is always answerable
//	      and a refused one leaves nothing behind.
//	A1-5  The model sees a server-composed envelope (request id, sender label,
//	      reply instruction); the transcript keeps the raw text. The body stays
//	      untrusted text: the ledger, not the envelope, authorizes an answer.
//	A1-6  Accepted limitation (#1206): all chats of a bound instance share the
//	      bound pair's main context and control.

// boundConnectorPair returns the {workspace, agent} pair a connector message
// is bound to, and false for an unbound instance (or anything that is not a
// connector message). It reads configuration only.
func (al *AgentLoop) boundConnectorPair(msg bus.InboundMessage) (addressing.Pair, string, bool) {
	if msg.SessionID != "" || msg.Channel == "" || msg.Channel == "webchat" || msg.Channel == "system" ||
		constants.IsInternalChannel(msg.Channel) {
		return addressing.Pair{}, "", false
	}
	instanceID := inboundInstanceID(msg)
	cfg := al.GetConfig()
	if cfg == nil {
		return addressing.Pair{}, "", false
	}
	inst, ok := cfg.Channels[instanceID]
	if !ok || strings.TrimSpace(inst.WorkspaceID) == "" {
		return addressing.Pair{}, "", false
	}
	identity := al.resolveInboundIdentity(instanceID)
	if identity == nil || strings.ToLower(strings.TrimSpace(identity.Kind)) != "agent" || strings.TrimSpace(identity.ID) == "" {
		return addressing.Pair{}, "", false
	}
	return addressing.Pair{WorkspaceID: inst.WorkspaceID, AgentID: strings.TrimSpace(identity.ID)}, instanceID, true
}

// admitBoundConnectorInput is the one intake seam for bound connector input
// (architect order, U8 open point (a)). It returns ("", nil) when the message is
// not bound-connector input (unchanged) or was admitted (msg now addressed to
// the pair's main with its envelope), and a user-facing refusal when it was
// refused (nothing written, no turn).
func (al *AgentLoop) admitBoundConnectorInput(msg *bus.InboundMessage) (refusal string, err error) {
	if msg == nil {
		return "", nil
	}
	// 1. Bound?
	pair, _, bound := al.boundConnectorPair(*msg)
	if !bound {
		return "", nil
	}
	const unroutable = "This conversation can't be handled right now."
	// 2. Eligibility (FR-003): never create a main for a hidden pair.
	deps := al.loadAddressDeps()
	if deps == nil {
		return unroutable, fmt.Errorf("connector intake: address deps are not wired")
	}
	eligible, eErr := deps.PairEligible(pair.WorkspaceID, pair.AgentID)
	if eErr != nil || !eligible {
		return unroutable, fmt.Errorf("connector intake: %s/%s has no eligible main (%v)", pair.WorkspaceID, pair.AgentID, eErr)
	}
	// 3. Bounds before any write.
	if reply, over := al.refuseOversizedUserMessage(*msg); over {
		return reply, nil
	}
	store := al.GetSessionStore()
	ledger := al.RequestLedger()
	if store == nil || ledger == nil {
		return unroutable, fmt.Errorf("connector intake: session store unavailable")
	}
	// 4. The pair's main.
	meta, mErr := store.GetOrCreateMainSession(pair.WorkspaceID, pair.AgentID)
	if mErr != nil {
		return unroutable, fmt.Errorf("connector intake: main session: %w", mErr)
	}
	// 5. Capture, then append (discard the capture if the append fails).
	instanceKey := msg.InstanceID
	if strings.TrimSpace(instanceKey) == "" {
		instanceKey = msg.Channel
	}
	requestID := uuid.New().String()
	capture := addressing.Capture{
		RequestID:         requestID,
		ReceiverSessionID: meta.ID,
		Receiver:          pair,
		Sender: addressing.Sender{
			Platform: msg.Sender.Platform, PlatformID: msg.Sender.PlatformID,
			CanonicalID: msg.Sender.CanonicalID, DisplayName: senderDisplayName(msg.Sender),
		},
		Source: addressing.Source{
			Kind: addressing.SourceConnector, Owner: pair, SessionID: meta.ID,
			InstanceID: instanceKey, ChatID: msg.ChatID, PlatformMessageID: msg.MessageID,
			PeerKind: string(msg.Peer.Kind), PeerID: msg.Peer.ID,
		},
		AdmittedAt: time.Now().UTC(),
	}
	if vErr := capture.Validate(); vErr != nil {
		return unroutable, fmt.Errorf("connector intake: %w", vErr)
	}
	if pErr := ledger.Put(capture); pErr != nil {
		return unroutable, fmt.Errorf("connector intake: capture: %w", pErr)
	}
	raw := msg.Content
	entry := session.TranscriptEntry{
		ID: requestID, Role: "user", AgentID: pair.AgentID, Content: raw, Timestamp: capture.AdmittedAt,
		// Left-side label (F15): from the adapter-authenticated sender only.
		Participant: participantFromSender(capture.Sender, al.agentDisplayName),
	}
	if aErr := store.AppendTranscriptStrict(meta.ID, entry); aErr != nil {
		if dErr := ledger.Discard(meta.ID, requestID); dErr != nil {
			logger.WarnCF("agent", "connector request capture could not be discarded after a failed append",
				map[string]any{"request_id": requestID, "error": dErr.Error()})
		}
		return unroutable, fmt.Errorf("connector intake: could not record the message: %w", aErr)
	}
	al.publishUserEntry(meta.ID, entry)
	// 6. The model's envelope; the route and the sender stay server-side.
	msg.TranscriptEntryID = requestID
	msg.SessionID = meta.ID
	msg.Content = composeRequestText(requestID, connectorSenderLabel(*msg, instanceKey), raw)
	if msg.Metadata == nil {
		msg.Metadata = map[string]string{}
	}
	// 4 (cont.) agent_id makes resolveMessageRoute take the explicit path the
	// web uses, so the main has exactly ONE worker scope (FR-009).
	msg.Metadata["agent_id"] = pair.AgentID
	msg.Metadata["workspace_id"] = pair.WorkspaceID
	return "", nil
}

// connectorSenderLabel is the server-made origin line, e.g.
// "Alice (@alice) on telegram.eu". Display fields are the adapter's, shown as
// data in the header only.
func connectorSenderLabel(msg bus.InboundMessage, instanceKey string) string {
	name := strings.TrimSpace(msg.Sender.DisplayName)
	user := strings.TrimSpace(msg.Sender.Username)
	switch {
	case name != "" && user != "":
		name = fmt.Sprintf("%s (@%s)", name, strings.TrimPrefix(user, "@"))
	case name == "" && user != "":
		name = "@" + strings.TrimPrefix(user, "@")
	case name == "":
		name = "a sender"
	}
	return fmt.Sprintf("%s on %s", name, instanceKey)
}

// refuseConnectorInput tells the source chat why its message was refused. It is
// a system-origin reply (no agent identity, no return route), so it passes the
// ordinary egress unchanged.
func (al *AgentLoop) refuseConnectorInput(ctx context.Context, msg bus.InboundMessage, refusal string) {
	dest := msg.InstanceID
	if strings.TrimSpace(dest) == "" {
		dest = msg.Channel
	}
	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := al.bus.PublishOutbound(pubCtx, bus.OutboundMessage{Channel: dest, ChatID: msg.ChatID, Content: refusal}); err != nil {
		logger.WarnCF("agent", "could not tell a connector chat its message was refused",
			map[string]any{"channel": dest, "chat_id": msg.ChatID, "error": err.Error()})
	}
}

// senderDisplayName is the label source for a connector sender: the adapter's
// display name, else "@username", else the platform id. Display only.
func senderDisplayName(s bus.SenderInfo) string {
	if n := strings.TrimSpace(s.DisplayName); n != "" {
		return n
	}
	if u := strings.TrimSpace(s.Username); u != "" {
		return "@" + strings.TrimPrefix(u, "@")
	}
	return strings.TrimSpace(s.PlatformID)
}
