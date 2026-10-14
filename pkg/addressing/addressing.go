// Package addressing holds the server-owned record of an admitted request and
// the rule that decides who may answer it (session-core U8, C-ADDRESS /
// C-REPLY, FR-027/028/045).
//
// A request is a message admitted to a RECEIVER's session (a peer send, an
// @request from a conversation, or an inbound connector message routed to a
// guest). At admission the server writes a Capture beside the receiver's
// session: the receiver pair, the authenticated sender, and the SOURCE route
// the answer must return through. The model never sees or supplies the source
// route; it supplies only the request id (reply_to). The capture is looked up
// by that id in the RESPONDING agent's own session, so a forged, foreign or
// stale id finds nothing.
//
// Trust decisions (security review points):
//
//	T1  The ledger is written only by the admission code in pkg/agent, from
//	    values the server resolved (authenticated principal, bound instance
//	    owner, the id the store actually appended) — never from tool arguments.
//	T2  Resolve matches the capture against the responder's session AND pair;
//	    a request addressed to another agent or session is "unknown".
//	T3  A capture records the source owner as it was at admission. Whether it
//	    still holds is NOT decided here: delivery re-checks at the final egress
//	    boundary (pkg/channels.checkReturnRoute) and for a conversation source
//	    in pkg/agent before the append.
//	T4  Nothing here grants the answering agent a connector, a credential or a
//	    delegation edge; it only selects the return route.
package addressing

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxComponentLen is the bound on a workspace or agent id (existing contract).
const MaxComponentLen = 128

// Pair is a {workspace_id, agent_id} address.
type Pair struct {
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
}

// IsZero reports an unset pair.
func (p Pair) IsZero() bool { return p.WorkspaceID == "" && p.AgentID == "" }

// Validate refuses an incomplete or oversize pair.
func (p Pair) Validate() error {
	for name, v := range map[string]string{"workspace_id": p.WorkspaceID, "agent_id": p.AgentID} {
		if strings.TrimSpace(v) == "" || v != strings.TrimSpace(v) {
			return fmt.Errorf("addressing: %s is empty or has surrounding whitespace", name)
		}
		if len(v) > MaxComponentLen {
			return fmt.Errorf("addressing: %s is %d bytes, over the %d-byte limit", name, len(v), MaxComponentLen)
		}
	}
	return nil
}

// SourceKind says where an answer returns to.
type SourceKind string

const (
	// SourceConversation returns into the source session's own conversation
	// (web chat, or a peer agent's session): the answer is appended there,
	// authored by the answering agent.
	SourceConversation SourceKind = "conversation"
	// SourceConnector returns through the source owner's channel instance, to
	// the original chat/thread.
	SourceConnector SourceKind = "connector"
	// SourceRoom returns into a group chat (room, omnipus-system-agent US-17).
	// A room is not a session: there is no source session and no owner to
	// wake; the answer is appended to the room by the room reply branch
	// (FR-166, FR-215) and the capture carries the room id instead.
	SourceRoom SourceKind = "room"
)

// Sender is the authenticated origin of the request. Fields are filled only
// from authenticated facts; an empty principal stays empty (anonymous/shared
// auth modes are preserved, never invented into a human).
type Sender struct {
	// Principal is the gateway-authenticated principal for web sources.
	Principal string `json:"principal,omitempty"`
	// Platform identity for connector sources.
	Platform    string `json:"platform,omitempty"`
	PlatformID  string `json:"platform_id,omitempty"`
	CanonicalID string `json:"canonical_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	// Agent is set when a peer agent sent the request.
	Agent Pair `json:"agent,omitempty"`
}

// Source is the server-owned return route of a request.
type Source struct {
	Kind SourceKind `json:"kind"`
	// Owner is the pair that owned the source (the session owner, or the
	// bound instance owner at admission). Zero for an unbound instance.
	Owner Pair `json:"owner"`
	// SessionID is the source conversation. Empty for a room source.
	SessionID string `json:"session_id"`
	// RoomID and RoomName identify the group chat of a SourceRoom request
	// (RoomName as it was at admission, for display only).
	RoomID   string `json:"room_id,omitempty"`
	RoomName string `json:"room_name,omitempty"`
	// Connector correlation (SourceConnector only).
	InstanceID        string `json:"instance_id,omitempty"`
	ChatID            string `json:"chat_id,omitempty"`
	PlatformMessageID string `json:"platform_message_id,omitempty"`
	PeerKind          string `json:"peer_kind,omitempty"`
	PeerID            string `json:"peer_id,omitempty"`
}

// Capture is one admitted, answerable request.
type Capture struct {
	// RequestID is the admitted message id — the id the receiver replies to.
	RequestID string `json:"request_id"`
	// ReceiverSessionID is the session the request was admitted to.
	ReceiverSessionID string    `json:"receiver_session_id"`
	Receiver          Pair      `json:"receiver"`
	Sender            Sender    `json:"sender"`
	Source            Source    `json:"source"`
	AdmittedAt        time.Time `json:"admitted_at"`
	// Discarded is set when the request was discarded before delivery (Stop
	// before delivery); a discarded request can no longer be answered.
	Discarded bool `json:"discarded,omitempty"`
	// Delivered is set once a running turn of the receiver has been given the
	// request. A request admitted while the receiver is stopped stays
	// undelivered (held) until the receiver runs again.
	Delivered bool `json:"delivered,omitempty"`
	// Text is the request exactly as the receiver's model reads it. A request
	// held for a stopped receiver is delivered from here when the receiver runs
	// again; the transcript entry alone does not reach the model.
	Text string `json:"text,omitempty"`
	// Held is set when the request was admitted while the receiver was stopped.
	// Only a held request moves its sender's row on when it is finally delivered.
	Held bool `json:"held,omitempty"`
	// SenderMessageID is the sender's own message id (the web client_message_id)
	// for a request a person sent from a chat, so delivery of a held request can
	// move that message's row on. Empty for an agent sender or a connector.
	SenderMessageID string `json:"sender_message_id,omitempty"`
}

// Validate checks a capture is complete enough to route an answer.
func (c Capture) Validate() error {
	if strings.TrimSpace(c.RequestID) == "" {
		return errors.New("addressing: capture has no request id")
	}
	if strings.TrimSpace(c.ReceiverSessionID) == "" {
		return errors.New("addressing: capture has no receiver session")
	}
	if err := c.Receiver.Validate(); err != nil {
		return fmt.Errorf("addressing: receiver: %w", err)
	}
	if c.Source.Kind == SourceRoom {
		if strings.TrimSpace(c.Source.RoomID) == "" {
			return errors.New("addressing: room source needs a room id")
		}
		return nil
	}
	if strings.TrimSpace(c.Source.SessionID) == "" {
		return errors.New("addressing: capture has no source session")
	}
	switch c.Source.Kind {
	case SourceConversation:
	case SourceConnector:
		if strings.TrimSpace(c.Source.InstanceID) == "" || strings.TrimSpace(c.Source.ChatID) == "" {
			return errors.New("addressing: connector source needs an instance and a chat")
		}
	default:
		return fmt.Errorf("addressing: unknown source kind %q", c.Source.Kind)
	}
	if !c.Source.Owner.IsZero() {
		if err := c.Source.Owner.Validate(); err != nil {
			return fmt.Errorf("addressing: source owner: %w", err)
		}
	}
	return nil
}

// Refusals returned by Resolve. All mean "nothing may be sent".
var (
	// ErrUnknownRequest: no such request in the responding session (also the
	// answer for a forged, foreign-session or wrong-receiver id, so an id from
	// elsewhere reveals nothing).
	ErrUnknownRequest = errors.New("addressing: unknown request")
	// ErrDiscarded: the request was discarded before delivery.
	ErrDiscarded = errors.New("addressing: request was discarded")
	// ErrUnusableCorrelation: the capture has no usable return route.
	ErrUnusableCorrelation = errors.New("addressing: the request has no usable return correlation")
)
