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
}

// SenderInfo provides structured sender identity information.
type SenderInfo struct {
	Platform    string `json:"platform,omitempty"`     // "telegram", "discord", "slack", ...
	PlatformID  string `json:"platform_id,omitempty"`  // raw platform ID, e.g. "123456"
	CanonicalID string `json:"canonical_id,omitempty"` // "platform:id" format
	Username    string `json:"username,omitempty"`     // username (e.g. @alice)
	DisplayName string `json:"display_name,omitempty"` // display name
}

type InboundMessage struct {
	Channel string `json:"channel"`
	// Deprecated: use Sender.CanonicalID instead. Retained for backward compatibility.
	SenderID   string     `json:"sender_id"`
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
	SessionID string            `json:"session_id,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type OutboundMessage struct {
	Channel string `json:"channel"`
	ChatID  string `json:"chat_id"`
	// SessionID is the transcript-store session this message belongs to.
	// Populated by the agent loop from the originating turn so channels
	// (and the SPA) can route the frame to the right session bucket.
	SessionID        string `json:"session_id,omitempty"`
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
	Channel   string      `json:"channel"`
	ChatID    string      `json:"chat_id"`
	SessionID string      `json:"session_id,omitempty"`
	Parts     []MediaPart `json:"parts"`
}
