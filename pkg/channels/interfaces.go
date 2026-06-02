package channels

import (
	"context"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/commands"
)

// TypingCapable — channels that can show a typing/thinking indicator.
// StartTyping begins the indicator and returns a stop function.
// The stop function MUST be idempotent and safe to call multiple times.
type TypingCapable interface {
	StartTyping(ctx context.Context, chatID string) (stop func(), err error)
}

// MessageEditor — channels that can edit an existing message.
// messageID is always string; channels convert platform-specific types internally.
type MessageEditor interface {
	EditMessage(ctx context.Context, chatID string, messageID string, content string) error
}

// MessageDeleter — channels that can delete a message by ID.
type MessageDeleter interface {
	DeleteMessage(ctx context.Context, chatID string, messageID string) error
}

// ReactionCapable — channels that can add a reaction (e.g. 👀) to an inbound message.
// ReactToMessage adds a reaction and returns an undo function to remove it.
// The undo function MUST be idempotent and safe to call multiple times.
type ReactionCapable interface {
	ReactToMessage(ctx context.Context, chatID, messageID string) (undo func(), err error)
}

// PlaceholderCapable — channels that can send a placeholder message
// (e.g. "Thinking... 💭") that will later be edited to the actual response.
// The channel MUST also implement MessageEditor for the placeholder to be useful.
// SendPlaceholder returns the platform message ID of the placeholder so that
// Manager.preSend can later edit it via MessageEditor.EditMessage.
type PlaceholderCapable interface {
	SendPlaceholder(ctx context.Context, chatID string) (messageID string, err error)
}

// StreamingCapable — channels that can show partial LLM output in real-time.
// The channel SHOULD gracefully degrade if the platform rejects streaming
// (e.g. Telegram bot without forum mode). In that case, Update becomes a no-op
// and Finalize still delivers the final message.
type StreamingCapable interface {
	BeginStream(ctx context.Context, chatID string) (Streamer, error)
}

// Streamer is defined in pkg/bus to avoid circular imports.
// This alias keeps channel implementations using channels.Streamer unchanged.
type Streamer = bus.Streamer

// PlaceholderRecorder is injected into channels by Manager.
// Channels call these methods on inbound to register typing/placeholder state.
// Manager uses the registered state on outbound to stop typing and edit placeholders.
type PlaceholderRecorder interface {
	RecordPlaceholder(channel, chatID, placeholderID string)
	RecordTypingStop(channel, chatID string, stop func())
	RecordReactionUndo(channel, chatID string, undo func())
}

// CommandRegistrarCapable is implemented by channels that can register
// command menus with their upstream platform (e.g. Telegram BotCommand).
// Channels that do not support platform-level command menus can ignore it.
type CommandRegistrarCapable interface {
	RegisterCommands(ctx context.Context, defs []commands.Definition) error
}

// PairingStatus is a linked-device pairing lifecycle state. The values mirror
// the WhatsAppPairingFrame.status contract enum exactly; a typed constant set
// keeps the channel→gateway→SPA boundary from drifting by a stray string literal
// (#283). The full set is asserted against the wire contract by a drift test in
// pkg/gateway.
type PairingStatus string

const (
	// PairingStatusWaiting is reserved for a pre-QR connecting state. It is not
	// currently emitted; the SPA renders its initial prompt from the absence of
	// any pairing state.
	PairingStatusWaiting PairingStatus = "waiting"
	// PairingStatusCode means a QR code is available to scan (qr is set).
	PairingStatusCode PairingStatus = "code"
	// PairingStatusLinked means the device paired successfully.
	PairingStatusLinked PairingStatus = "linked"
	// PairingStatusTimeout means the displayed QR expired; a fresh one follows.
	PairingStatusTimeout PairingStatus = "timeout"
	// PairingStatusError means pairing failed; message carries the reason.
	PairingStatusError PairingStatus = "error"
)

// AllPairingStatuses is the authoritative set of pairing statuses, kept next to
// the constants so adding one here is the single edit a new status needs. The
// pkg/gateway drift test asserts this set equals the contract enum.
var AllPairingStatuses = []PairingStatus{
	PairingStatusWaiting,
	PairingStatusCode,
	PairingStatusLinked,
	PairingStatusTimeout,
	PairingStatusError,
}

// PairingObservable is implemented by channels that produce linked-device
// pairing updates (a QR code to scan + status transitions) and can report them
// to an observer. The gateway wires the observer at boot so the SPA can render
// the QR in-browser instead of requiring the gateway terminal (#283).
//
// qr is non-empty only when status == PairingStatusCode.
type PairingObservable interface {
	SetPairingObserver(fn func(status PairingStatus, qr, message string))
}
