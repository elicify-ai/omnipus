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
	// UserInitiated is a fail-closed origin signal for the Planning & Goals
	// epic's origin gating (ADR-049 Gap #8/r2, spec Part B FR-075/SD-B6/R6):
	// true ONLY when this message originates from a genuine, live human
	// action — the gateway webchat WS `message` handler (pkg/gateway/
	// websocket.go) or a channel adapter's inbound dispatch of a real
	// platform sender (pkg/channels/base.go HandleMessage). Every other
	// producer of an InboundMessage (async-notifier synthesized system
	// messages, followUps/steering re-injection, ProcessDirect/
	// ProcessDirectWithChannel) MUST leave this false — it is NOT set
	// automatically, so a future producer that forgets to set it fails
	// closed (cannot start a /goal or /loop) rather than failing open.
	// Scheduled/cron runs (exec.ProcessScheduled) and task/sub-turn runs
	// (processTaskDirect/spawnSubTurn) never construct an InboundMessage at
	// all — they call runAgentLoop directly with their own processOptions,
	// which also defaults UserInitiated to false. This field is internal
	// agent<->channel plumbing (pkg/bus is not part of the gateway/SPA wire
	// boundary, Constraint #8) — not a wire type.
	UserInitiated bool `json:"user_initiated,omitempty"`
}

type OutboundMessage struct {
	Channel string `json:"channel"`
	ChatID  string `json:"chat_id"`
	// SessionID is the transcript-store session this message belongs to.
	// Populated by the agent loop from the originating turn so channels
	// (and the SPA) can route the frame to the right session bucket.
	SessionID string `json:"session_id,omitempty"`
	// AgentID is the acting agent (tools.ToolAgentID(ctx)) for an
	// AGENT-ORIGINATED send only — i.e. one that reached the bus via the
	// send_message tool (ADR-065 / channel-agent-ownership-spec.md FR-6).
	// Left empty by the ~19 system producers (streamed replies, notifyDrop
	// backpressure, schedule delivery, device notifications, ...): they are
	// not model-addressable and are exempt from the FR-6 dispatch-time
	// ownership re-check by definition of this field being unset. Do not
	// set this for a system-originated send.
	AgentID string `json:"agent_id,omitempty"`
	// WorkspaceID is the turn's workspace (tools.ToolWorkspaceID(ctx)) for
	// an AGENT-ORIGINATED send only, paired with AgentID above — together
	// they are the (workspace, agent) ownership pair FR-6's dispatch-time
	// check validates against the target instance's
	// ChannelInstanceConfig.WorkspaceID + Identity. Left empty by the same
	// system producers that leave AgentID empty.
	WorkspaceID      string `json:"workspace_id,omitempty"`
	Content          string `json:"content"`
	ReplyToMessageID string `json:"reply_to_message_id,omitempty"`

	// OwnershipChecked records that the send tool already applied ADR-065's
	// ownership rule to this message. In-process only — never on the wire.
	//
	// It exists because the tool layer decides with MORE information than
	// dispatch has. send_message knows the turn's own conversation and lets an
	// agent reply into it even when the instance belongs to someone else —
	// which is exactly what keeps hand-off and delegation working, since a
	// delegate answers inside its parent's conversation under its own
	// identity. Dispatch sees only (instance, agent, workspace), so re-deriving
	// the same verdict there produces FALSE REFUSALS on ordinary delegation.
	//
	// So dispatch does not re-decide; it checks that a decision was made. A
	// message carrying an AgentID but not this flag reached the bus without
	// passing the tool's check, which is precisely the "someone adds a second
	// route later" regression FR-7 exists to catch.
	OwnershipChecked bool `json:"-"`
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
	Channel   string `json:"channel"`
	ChatID    string `json:"chat_id"`
	SessionID string `json:"session_id,omitempty"`
	// WorkspaceID was already carried here pre-ADR-065 (FIX 1, see
	// pkg/agent/media_workspace_id_test.go) so channel media sends resolve
	// refs via the turn's actual workspace rather than the private/global
	// room. ADR-065 / FR-6 gives it a second job for AGENT-ORIGINATED
	// sends: paired with AgentID below, it is the same (workspace, agent)
	// ownership pair OutboundMessage carries, so send_file's dispatch-time
	// audit trail is consistent with send_message's rather than a silent
	// gap in observability. Still left empty for any non-agent-originated
	// media send.
	WorkspaceID string `json:"workspace_id,omitempty"`
	// AgentID is the acting agent (tools.ToolAgentID(ctx)) for an
	// AGENT-ORIGINATED media send only, mirroring OutboundMessage.AgentID
	// above. Today's sole producer (pkg/agent/loop.go's send_file
	// tool-media delivery block) is always agent-originated, so this is
	// always set in practice — the "empty = system-originated, exempt from
	// the FR-6 ownership re-check" convention is documented here anyway so
	// a future non-agent producer (there is none today) fails safe rather
	// than needing this comment rewritten first.
	AgentID string      `json:"agent_id,omitempty"`
	Parts   []MediaPart `json:"parts"`
}
