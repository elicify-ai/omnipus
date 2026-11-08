package tools

// The start path of send_message (channels-spec-r8 T-4, FR-040–FR-043): an
// ordinary send to a connector that is neither the main's refused default
// form nor a non-main session's own conversation is a START. A start passes
// the T-3 check (StartPolicy, wired by the agent loop to
// pkg/channels.StartAllowed on the live policy source and
// Manager.Availability) and is published with Operation=start and the bare
// chat id. A tool with no start policy wired behaves exactly as before.

import (
	"context"
	"errors"
	"fmt"
)

// The values of SendOrigin.Operation (FR-043; the bus copies them into
// bus.OutboundMessage.Operation).
const (
	// SendOperationChecked: the tool applied its ownership rule to a
	// non-start send.
	SendOperationChecked = "checked"
	// SendOperationStart: the send is a start that passed T-3.
	SendOperationStart = "start"
)

// ErrStartRefused is the sentinel of a start that failed the T-3 check; the
// result's ForLLM carries the exact T-3 text.
var ErrStartRefused = errors.New("send_message_start_refused")

// StartVerdict is a StartPolicy's answer for one start.
type StartVerdict struct {
	Allowed bool
	// Text is the exact T-3 refusal text when Allowed is false.
	Text string
	// BareChatID is the canonical target without its platform prefix when
	// Allowed is true: the chat id the start is published with.
	BareChatID string
}

// StartPolicy is the start rule injected into the tool (FR-041).
type StartPolicy interface {
	// IsConnector reports whether channel names a chat connection rather
	// than webchat, system or an internal channel (T-4 step 3).
	IsConnector(channel string) bool
	// CheckStart runs T-3 for a start by agentID in workspaceID to chatID
	// on channel.
	CheckStart(channel, chatID, workspaceID, agentID string) StartVerdict
}

// SetStartPolicy installs the start rule. Nil (the default) keeps today's
// behaviour: no send is classified as a start (FR-043).
func (t *MessageTool) SetStartPolicy(p StartPolicy) { t.startPolicy = p }

// mainConnectorReplyOnlyText is the refusal of the main's ordinary connector
// form. With a start policy wired a start is possible, so the amended
// FR-042 text names it; unwired, the text stays today's.
func (t *MessageTool) mainConnectorReplyOnlyText() string {
	if t.startPolicy == nil {
		return "in your main conversation a connector is reached only by replying to a request: call " +
			"send_message with reply_to=<request id> and your answer as content. Nothing was sent."
	}
	return "in your main conversation a connector is reached only by replying to a request " +
		"(send_message reply_to=<request id> with your answer as content), or by starting a message with " +
		"both channel and chat_id where your Channels block allows it. Nothing was sent."
}

// isStart applies T-4 steps 3–5 to a send that passed steps 1–2: not a
// connector → unchanged; a non-main session's own conversation →
// unchanged; anything else to a connector → a start. Always false with no
// start policy wired.
func (t *MessageTool) isStart(ctx context.Context, channel, chatID, turnChannel, turnChat string) bool {
	if t.startPolicy == nil || !t.startPolicy.IsConnector(channel) {
		return false
	}
	isMain := t.mainConnectorGuard != nil && t.mainConnectorGuard(ToolTranscriptSessionID(ctx), channel)
	if !isMain && channel == turnChannel && chatID == turnChat {
		return false
	}
	return true
}

// executeStart runs T-3 and, on a pass, publishes the start with
// Operation=start and the bare chat id. A refusal publishes nothing.
func (t *MessageTool) executeStart(channel, chatID, content, workspaceID, agentID string) *ToolResult {
	verdict := t.startPolicy.CheckStart(channel, chatID, workspaceID, agentID)
	if !verdict.Allowed {
		return &ToolResult{ForLLM: verdict.Text, IsError: true, Err: ErrStartRefused}
	}
	origin := SendOrigin{AgentID: agentID, WorkspaceID: workspaceID, Operation: SendOperationStart}
	if err := t.sendCallback(channel, verdict.BareChatID, content, origin); err != nil {
		return &ToolResult{
			ForLLM:  fmt.Sprintf("sending message: %v", err),
			IsError: true,
			Err:     err,
		}
	}
	t.sentInRound.Store(true)
	return &ToolResult{
		ForLLM: fmt.Sprintf("accepted for sending to %s:%s (delivery not confirmed)", channel, verdict.BareChatID),
		Silent: true,
	}
}
