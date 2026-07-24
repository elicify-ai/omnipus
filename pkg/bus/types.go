package bus

// PeerKind classifies the routing peer type for a message.
type PeerKind string

const (
	// PeerDirect is a one-on-one direct message conversation.
	PeerDirect PeerKind = "direct"
	// PeerGroup is a multi-user group chat.
	PeerGroup PeerKind = "group"
	// PeerChannel is a broadcast channel (e.g. Slack channel, IRC channel).
	PeerChannel PeerKind = "channel"
)

// Peer identifies the routing peer for a message (direct, group, channel, etc.)
type Peer struct {
	Kind PeerKind `json:"kind"` // PeerDirect | PeerGroup | PeerChannel | ""
	ID   string   `json:"id"`
	// InstanceID is the channel-instance key this peer belongs to
	// (ADR-019 FR-4b). Optional; channels that know their instance set it
	// directly instead of smuggling it through InboundMessage.Metadata.
	InstanceID string `json:"instance_id,omitempty"`
}

// SenderInfo provides structured sender identity information.
type SenderInfo struct {
	Platform    string `json:"platform,omitempty"`     // "telegram", "discord", "slack", ...
	PlatformID  string `json:"platform_id,omitempty"`  // raw platform ID, e.g. "123456"
	CanonicalID string `json:"canonical_id,omitempty"` // "platform:id" format
	Username    string `json:"username,omitempty"`     // username (e.g. @alice)
	DisplayName string `json:"display_name,omitempty"` // display name
	// InstanceID is the channel-instance key the sender is associated with
	// (ADR-019 FR-4b). Optional; populated by channels that know their
	// instance instead of smuggling it through InboundMessage.Metadata.
	InstanceID string `json:"instance_id,omitempty"`
}

type InboundMessage struct {
	Channel string `json:"channel"`
	// Sender carries the structured sender identity. Use Sender.CanonicalID
	// (e.g. "telegram:123") for routing keys and audit logs; use
	// Sender.DisplayName / Sender.Username for human-facing UI.
	Sender     SenderInfo `json:"sender"`
	ChatID     string     `json:"chat_id"`
	Content    string     `json:"content"`
	Media      []string   `json:"media,omitempty"`
	Peer       Peer       `json:"peer"`                  // routing peer
	MessageID  string     `json:"message_id,omitempty"`  // platform message ID
	MediaScope string     `json:"media_scope,omitempty"` // media lifecycle scope
	SessionKey string     `json:"session_key"`
	// SessionID is the transcript-store session ID this message belongs to.
	// Populated by the gateway from the WS frame.SessionID on every message
	// (the gateway mints a new id when the SPA sends one without it). Used
	// by routing, handoff override, and per-agent history keying so two
	// concurrent sessions in the same browser remain isolated.
	SessionID string `json:"session_id,omitempty"`
	// InstanceID is the channel-instance key this message arrived on
	// (ADR-019 FR-4b / NFR-1). Channels that know their instance set this
	// directly; inboundInstanceID prefers it over Metadata for the
	// transition off the metadata-map smuggling pattern.
	InstanceID string `json:"instance_id,omitempty"`
	// GatewayUserID is the WS-authenticated gateway principal that initiated
	// this turn (FR-017 audit attribution). It is set ONLY by the gateway
	// webchat WS path (pkg/gateway/websocket.go, where wc.userID is known,
	// e.g. "cli" or an admin username) and is read by the agent loop into
	// processOptions.UserID → audit.Entry.User. It is a DEDICATED carrier,
	// deliberately separate from Sender.Username: production channels
	// (Telegram, Discord, IRC, Matrix, Google Chat, WeiXin) populate
	// Sender.Username with the PLATFORM handle (e.g. "@alice"), which is NOT
	// a gateway principal and must never be stamped as audit User. Because
	// channel/task/scheduled inbound messages never set this field, those
	// turns leave audit.Entry.User empty structurally — not by a runtime
	// guard. Empty under dev-mode bypass / legacy env-token auth (wc.userID
	// is unset there); the audit stamp then stays empty rather than guessing.
	GatewayUserID string `json:"gateway_user_id,omitempty"`
	// AsyncOriginAgentID is the agent whose turn produced this background
	// result. Set ONLY by AsyncNotifier.Notify (pkg/agent/async_notifier.go,
	// FIX 5d) when publishing a synthetic "system" channel message carrying an
	// async tool/delegate result (AsyncNotifyEvent.AgentID). A DEDICATED
	// carrier, following the same pattern as GatewayUserID above: without it,
	// the consumer (processSystemMessage) has no way to know which agent
	// actually produced the work and must guess via GetDefaultAgent() —
	// the confirmed, exact cause of a live "Worker vs Jim" speaker-attribution
	// flip when an async result from a non-default agent lands. Channel/
	// task/scheduled inbound messages never set this field, so it is empty
	// there structurally — not by a runtime guard.
	AsyncOriginAgentID string `json:"async_origin_agent_id,omitempty"`
	// AsyncTranscriptSessionID is the transcript-store session ID the
	// originating turn was persisting to (turnState.transcriptSessionID). Set
	// ONLY by AsyncNotifier.Notify (FIX 5d), mirroring AsyncOriginAgentID
	// above. processSystemMessage resolves the owning store via
	// AgentLoop.ResolveSessionStore and threads it into the reconstructed
	// turn's TranscriptSessionID/TranscriptStore — the same "run a turn that
	// must land in a specific, pre-existing session" pattern
	// AgentLoop.ProcessScheduled and spawnSubTurn already use. Without this,
	// persistence of the reconstructed turn depended ENTIRELY on a live
	// WebSocket connection still being open (via the gateway's fragile
	// chatID→sessionID GetStreamer lookup) — if that connection had already
	// closed by the time the async result arrived, the result was silently,
	// permanently lost (never written to transcript.jsonl, unrecoverable even
	// by reopening the conversation).
	AsyncTranscriptSessionID string            `json:"async_transcript_session_id,omitempty"`
	Metadata                 map[string]string `json:"metadata,omitempty"`
}

type OutboundMessage struct {
	Channel string `json:"channel"`
	ChatID  string `json:"chat_id"`
	// SessionID is the transcript-store session this message belongs to.
	// Populated by the agent loop from the originating turn so channels
	// (and the SPA) can route the frame to the right session bucket.
	SessionID string `json:"session_id,omitempty"`
	// InstanceID is the channel-instance key this message is bound for
	// (ADR-019 FR-4b / NFR-1). Optional; channels that know their instance
	// set it directly instead of relying on Channel alone.
	InstanceID       string `json:"instance_id,omitempty"`
	Content          string `json:"content"`
	ReplyToMessageID string `json:"reply_to_message_id,omitempty"`
}

// MediaPart describes a single media attachment to send.
type MediaPart struct {
	Type        string `json:"type"`                   // "image" | "audio" | "video" | "file"
	Ref         string `json:"ref"`                    // media store ref, e.g. "media://abc123"
	Caption     string `json:"caption,omitempty"`      // optional caption text
	Filename    string `json:"filename,omitempty"`     // original filename hint
	ContentType string `json:"content_type,omitempty"` // MIME type hint
}

// OutboundMediaMessage carries media attachments from Agent to channels via the bus.
type OutboundMediaMessage struct {
	Channel     string      `json:"channel"`
	ChatID      string      `json:"chat_id"`
	SessionID   string      `json:"session_id,omitempty"`
	WorkspaceID string      `json:"workspace_id,omitempty"`
	Parts       []MediaPart `json:"parts"`
}
