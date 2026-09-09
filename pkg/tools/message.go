package tools

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
)

// SendOrigin identifies the agent and workspace a send came from, so an
// outbound message carries its provenance instead of arriving anonymously
// (ADR-065 spec FR-6). Empty for system-originated sends.
type SendOrigin struct {
	AgentID     string
	WorkspaceID string
	// OwnershipChecked marks that ADR-065's rule was applied to this send.
	// Dispatch checks for it rather than re-deriving the verdict — see
	// bus.OutboundMessage.OwnershipChecked for why re-deriving is wrong.
	OwnershipChecked bool
}

type SendCallback func(channel, chatID, content string, origin SendOrigin) error

type MessageTool struct {
	BaseTool
	sendCallback SendCallback
	ownership    ChannelOwnership
	sentInRound  atomic.Bool // Tracks whether a message was sent in the current processing round
}

func NewMessageTool() *MessageTool {
	return &MessageTool{}
}

// SetChannelOwnership injects the resolver used to decide whether the acting
// agent may send through the requested channel (ADR-065).
//
// Nil is a legitimate state and means "ownership is unknowable here". The tool
// then falls back to the turn's own conversation and refuses any OTHER target,
// rather than pretending to have checked something it could not.
func (t *MessageTool) SetChannelOwnership(o ChannelOwnership) { t.ownership = o }

func (t *MessageTool) Name() string {
	return "send_message"
}

func (t *MessageTool) Description() string {
	return "Send a message on a chat channel. You do NOT need this to answer normally — your reply is " +
		"delivered automatically at the end of your turn. Calling this REPLACES your automatic reply for " +
		"this round (it will not be sent in addition to whatever you say afterward); use it only for " +
		"proactive or out-of-band messages — e.g. sending an update mid-turn, or messaging a different " +
		"channel/chat than the one you're responding in — not as your primary way to respond."
}

func (t *MessageTool) Scope() ToolScope       { return ScopeGeneral }
func (t *MessageTool) Category() ToolCategory { return CategoryCommunication }

func (t *MessageTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "The message content to send",
			},
			"channel": map[string]any{
				"type":        "string",
				"description": "Optional: target channel (telegram, whatsapp, etc.)",
			},
			"chat_id": map[string]any{
				"type":        "string",
				"description": "Optional: target chat/user ID",
			},
		},
		"required": []string{"content"},
	}
}

// ResetSentInRound resets the per-round send tracker.
// Called by the agent loop at the start of each inbound message processing round.
func (t *MessageTool) ResetSentInRound() {
	t.sentInRound.Store(false)
}

// HasSentInRound returns true if the message tool sent a message during the current round.
func (t *MessageTool) HasSentInRound() bool {
	return t.sentInRound.Load()
}

func (t *MessageTool) SetSendCallback(callback SendCallback) {
	t.sendCallback = callback
}

func (t *MessageTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	content, ok := args["content"].(string)
	if !ok {
		return &ToolResult{ForLLM: "content is required", IsError: true}
	}

	// The ACTING agent — never the session's original agent and never a
	// parent. After a hand-off this is the handed-to agent, and inside a
	// delegated sub-turn it is the delegate (ADR-065 spec FR-2a).
	actingAgent := ToolAgentID(ctx)
	workspaceID := ToolWorkspaceID(ctx)

	turnChannel, turnChat := ToolChannel(ctx), ToolChatID(ctx)
	channel, _ := args["channel"].(string)
	chatID, _ := args["chat_id"].(string)

	if channel == "" {
		channel = turnChannel
	}
	// Default the recipient from the turn whenever the target IS the turn's
	// own conversation — including when the agent named that channel
	// explicitly. Nesting this inside the "no channel named" branch meant
	// send_message(content, channel:"telegram") mid-Telegram-turn had no
	// chat id and was then told its turn had no conversation, which was both
	// unhelpful and untrue.
	if chatID == "" && channel == turnChannel {
		chatID = turnChat
	}

	// No channel named and no conversation to reply into: resolve the acting
	// agent's OWN instance for this workspace (spec FR-2).
	if channel == "" {
		resolved, errResult := t.resolveOwnChannel(workspaceID, actingAgent)
		if errResult != nil {
			return errResult
		}
		channel = resolved
	}

	if denied := t.denyUnownedTarget(channel, chatID, turnChannel, turnChat, workspaceID, actingAgent); denied != nil {
		return denied
	}

	if channel == "" {
		return &ToolResult{ForLLM: "No target channel specified", IsError: true}
	}
	if chatID == "" {
		// Resolving the agent's OWN instance (FR-2) yields a channel but no
		// conversation within it — an instance is not a chat. The generic
		// "no target" message used to swallow this and report the wrong
		// reason, which made the whole proactive path look broken rather than
		// under-specified.
		return &ToolResult{
			ForLLM: fmt.Sprintf("no recipient on %q: name the chat_id you mean. (Only the "+
				"turn's own conversation supplies one automatically.)", channel),
			IsError: true,
		}
	}

	if t.sendCallback == nil {
		return &ToolResult{ForLLM: "Message sending not configured", IsError: true}
	}

	// Ownership has now been decided for this message. Dispatch verifies that
	// a decision was made rather than re-making it with less context
	// (ADR-065 FR-7).
	origin := SendOrigin{AgentID: actingAgent, WorkspaceID: workspaceID, OwnershipChecked: true}
	if err := t.sendCallback(channel, chatID, content, origin); err != nil {
		return &ToolResult{
			ForLLM:  fmt.Sprintf("sending message: %v", err),
			IsError: true,
			Err:     err,
		}
	}

	t.sentInRound.Store(true)
	// Silent: user already received the message directly
	return &ToolResult{
		ForLLM: fmt.Sprintf("Message sent to %s:%s", channel, chatID),
		Silent: true,
	}
}

// resolveOwnChannel picks the acting agent's own instance when the turn has no
// conversation to reply into — a heartbeat, a scheduled run, background work
// (ADR-065 spec FR-2).
//
// It refuses rather than guessing when the agent owns several. That mirrors
// EmailTransports.resolve, and it is what the operator decided: the agent
// picks, because only it knows in context whether something belongs on Slack
// or on Telegram. Choosing for it would put a message somewhere nobody
// intended and log a warning nobody reads.
func (t *MessageTool) resolveOwnChannel(workspaceID, actingAgent string) (string, *ToolResult) {
	if t.ownership == nil {
		return "", &ToolResult{
			ForLLM: "no channel to send on: this turn has no conversation, and channel " +
				"ownership is not available here to resolve one",
			IsError: true,
		}
	}
	if workspaceID == "" {
		return "", &ToolResult{
			ForLLM: "no channel to send on: this turn is not bound to a workspace, so there " +
				"is no workspace whose channels you could use",
			IsError: true,
		}
	}
	owned := t.ownership.OwnedBy(workspaceID, actingAgent)
	switch len(owned) {
	case 0:
		return "", &ToolResult{
			ForLLM: fmt.Sprintf("no channel to send on: you own no channel in this workspace (%s). "+
				"An operator assigns a channel to an agent in that channel's Configure panel.", workspaceID),
			IsError: true,
		}
	case 1:
		return owned[0], nil
	default:
		return "", &ToolResult{
			ForLLM: fmt.Sprintf("you own more than one channel here (%s) — name the one you mean "+
				"in the channel argument", strings.Join(owned, ", ")),
			IsError: true,
		}
	}
}

// denyUnownedTarget is the enforcement point (ADR-065 spec FR-1, FR-4).
//
// A channel instance falls into exactly one of three cases, and conflating
// them is what made the first drafts of the spec incoherent:
//
//  1. CONFIGURED and workspace-BOUND — owned by one (workspace, agent) pair.
//     Only that pair may send through it. Team membership on the same
//     workspace confers nothing, exactly as it confers no access to another
//     agent's inbox.
//
//  2. CONFIGURED and UNBOUND — the operator's "No workspace (global default
//     routing)" choice. Nobody owns it, so nothing is enforced and behaviour
//     is unchanged (spec FR-9). This spec does not force binding; it makes
//     binding mean something outbound as well as inbound.
//
//  3. NOT CONFIGURED AT ALL — webchat, registered synthetically with no
//     ChannelInstanceConfig, plus any id resolving to nothing. Treated as
//     case 2: unowned, unenforced, unchanged.
//
// The spec claimed webchat was "protected by FR-1's validation". It is not,
// and cannot be: with no ownership record there is nothing for ownership to
// check. That sentence described protection that does not exist and has been
// corrected. Webchat is SHARED by operator decision — every agent may use it,
// it is not separately configured, and this spec changes nothing about it.
func (t *MessageTool) denyUnownedTarget(channel, chatID, turnChannel, turnChat, workspaceID, actingAgent string) *ToolResult {
	if channel == "" {
		return nil
	}
	// The turn's own conversation is always reachable: it was established by
	// the inbound path, and hand-off already decided who may answer it.
	if channel == turnChannel && (chatID == turnChat || chatID == "") {
		return nil
	}
	if t.ownership == nil {
		return &ToolResult{
			ForLLM: fmt.Sprintf("cannot send on %q: it is not this turn's conversation, and "+
				"channel ownership is not available here to authorise it", channel),
			IsError: true,
		}
	}

	ownerWS, ownerAgent, bound := t.ownership.OwnerOf(channel)
	if !bound {
		// Not workspace-bound, so nobody owns it and nothing is enforced.
		// This covers BOTH an instance the operator configured and left
		// unbound ("No workspace (global default routing)", spec FR-9) and
		// webchat, which is registered synthetically with no
		// ChannelInstanceConfig at all.
		//
		// OPERATOR DECISION: webchat is shared — every agent may use it, it is
		// not separately configured, and this spec changes nothing about it.
		// An earlier draft of this function refused any unconfigured target
		// that was not the turn's own conversation, which would have
		// restricted webchat; that is explicitly not wanted.
		return nil
	}
	if ownerWS == workspaceID && ownerAgent == actingAgent {
		return nil
	}
	owned := t.ownership.OwnedBy(workspaceID, actingAgent)
	yours := "you own no channel in this workspace"
	if len(owned) > 0 {
		yours = "yours here: " + strings.Join(owned, ", ")
	}
	return &ToolResult{
		ForLLM: fmt.Sprintf("cannot send on %q: it belongs to another agent (%s) — %s",
			channel, ownerAgent, yours),
		IsError: true,
	}
}
