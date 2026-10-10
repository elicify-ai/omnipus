package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// This file is send_message's two non-ordinary forms (session-core U8,
// C-ADDRESS / C-REPLY, FR-027/028/045):
//
//   - the PEER form names a recipient by the exact {workspace_id, agent_id}
//     pair and delivers a request to that pair's main session;
//   - the REPLY form carries reply_to — the id of a request the acting agent
//     was sent — and returns the answer through the request's source owner.
//
// The tool only validates the shape of the call and hands it to an injected
// router. Admission (policy, membership), the request ledger and the
// source-owner return adapter live behind those seams (pkg/agent), so this
// package never sees a connector credential or a source route.

// ErrReplyForm and ErrPeerForm classify a refused call shape.
var (
	ErrReplyForm = errors.New("send_message_reply_form")
	ErrPeerForm  = errors.New("send_message_peer_form")
)

// PeerRequest is one validated peer send. Sender is the acting agent and
// workspace taken from the turn context — never from tool arguments.
type PeerRequest struct {
	RecipientWorkspaceID string
	RecipientAgentID     string
	Content              string
	Sender               SendOrigin
	// SenderSessionID is the sending turn's transcript session.
	SenderSessionID string
}

// PeerReceipt reports what the router actually admitted.
type PeerReceipt struct {
	// RequestID is the admitted message id: the id the receiver replies to.
	RequestID string
	// SessionID is the receiver's main session the request was admitted to.
	SessionID string
}

// PeerRouter admits a peer request. A nil error means the request was
// admitted (accepted for the receiver's session), not that the receiver acted.
type PeerRouter interface {
	SendPeer(ctx context.Context, req PeerRequest) (PeerReceipt, error)
}

// ReplyRequest is one validated reply. Author is the acting (responding)
// agent; ReplyTo is the request id it answers.
type ReplyRequest struct {
	ReplyTo string
	Content string
	Author  SendOrigin
	// ResponderSessionID is the responding turn's transcript session — the
	// session the request must have been admitted to.
	ResponderSessionID string
}

// ReplyReceipt reports the accepted-for-send reply.
type ReplyReceipt struct {
	// ReplyID is the id of the reply as appended to the source conversation
	// (empty when the source is a connector and the reply only left the bus).
	ReplyID string
	// Destination is a short human description of where the reply went.
	Destination string
}

// ReplyRouter resolves reply_to against the authenticated responding
// session and returns the answer through the source owner's adapter.
type ReplyRouter interface {
	Reply(ctx context.Context, req ReplyRequest) (ReplyReceipt, error)
}

// SetPeerRouter injects the peer admission seam. Nil leaves the peer form
// refused ("not configured") rather than falling back to anything.
func (t *MessageTool) SetPeerRouter(r PeerRouter) { t.peerRouter = r }

// SetReplyRouter injects the reply seam. Nil leaves the reply form refused.
func (t *MessageTool) SetReplyRouter(r ReplyRouter) { t.replyRouter = r }

// ErrMainConnectorReplyOnly marks an ordinary send refused because the turn is
// a main session addressing a connector (reply_to is the only way out).
var ErrMainConnectorReplyOnly = errors.New("send_message_main_connector_reply_only")

// SetMainConnectorGuard installs the main-session test for the ordinary form.
func (t *MessageTool) SetMainConnectorGuard(g func(sessionID, channel string) bool) {
	t.mainConnectorGuard = g
}

func addrRefuse(format string, a ...any) *ToolResult {
	return &ToolResult{ForLLM: fmt.Sprintf(format, a...), IsError: true}
}

func addrRefuseWith(err error, format string, a ...any) *ToolResult {
	return &ToolResult{ForLLM: fmt.Sprintf(format, a...), IsError: true, Err: err}
}

// addrArgString reads an optional string argument. present is true when the key
// exists at all (so a supplied-but-blank value is distinguishable from an
// omitted one); ok is false when the value is present but not a string.
func addrArgString(args map[string]any, key string) (value string, present, ok bool) {
	raw, exists := args[key]
	if !exists || raw == nil {
		return "", false, true
	}
	s, isString := raw.(string)
	if !isString {
		return "", true, false
	}
	return s, true, true
}

// executeAddressedForm handles the reply and peer forms. handled is false
// when the call is an ordinary send and the caller should continue as before.
func (t *MessageTool) executeAddressedForm(ctx context.Context, content string, args map[string]any) (result *ToolResult, handled bool) {
	replyTo, hasReplyTo, replyToOK := addrArgString(args, "reply_to")
	wsArg, hasWS, wsOK := addrArgString(args, "workspace_id")
	agentArg, hasAgent, agentOK := addrArgString(args, "agent_id")
	channel, hasChannel, channelOK := addrArgString(args, "channel")
	chatID, hasChat, chatOK := addrArgString(args, "chat_id")

	if !hasReplyTo && !hasWS && !hasAgent {
		return nil, false
	}
	if !replyToOK || !wsOK || !agentOK || !channelOK || !chatOK {
		return addrRefuse("reply_to, workspace_id, agent_id, channel and chat_id must be strings"), true
	}
	if denied := t.denySteeredAddressing(ctx); denied != nil {
		return denied, true
	}
	if hasReplyTo {
		return t.executeReplyForm(ctx, content, replyTo,
			hasWS || hasAgent, (hasChannel && strings.TrimSpace(channel) != "") || (hasChat && strings.TrimSpace(chatID) != "")), true
	}
	if (hasChannel && strings.TrimSpace(channel) != "") || (hasChat && strings.TrimSpace(chatID) != "") {
		return addrRefuseWith(ErrPeerForm, "a peer send is addressed by workspace_id and agent_id only; "+
			"do not also supply channel or chat_id"), true
	}
	return t.executePeerForm(ctx, content, wsArg, hasWS, agentArg, hasAgent), true
}

func (t *MessageTool) executeReplyForm(ctx context.Context, content, replyTo string, hasRecipient, hasDestination bool) *ToolResult {
	if hasDestination || hasRecipient {
		return addrRefuseWith(ErrReplyForm, "a reply is addressed by reply_to alone; do not also supply "+
			"channel, chat_id, workspace_id or agent_id — the destination comes from the request you are answering")
	}
	replyTo = strings.TrimSpace(replyTo)
	if replyTo == "" {
		return addrRefuseWith(ErrReplyForm, "reply_to is blank: name the request id you are answering. "+
			"Nothing was sent, and no default destination is used")
	}
	if strings.TrimSpace(content) == "" {
		return addrRefuseWith(ErrReplyForm, "a reply needs non-empty content")
	}
	if t.replyRouter == nil {
		return addrRefuseWith(ErrReplyForm, "replying to a request is not configured here; nothing was sent")
	}
	author := SendOrigin{AgentID: ToolAgentID(ctx), WorkspaceID: ToolWorkspaceID(ctx)}
	receipt, err := t.replyRouter.Reply(ctx, ReplyRequest{
		ReplyTo:            replyTo,
		Content:            content,
		Author:             author,
		ResponderSessionID: ToolTranscriptSessionID(ctx),
	})
	if err != nil {
		return &ToolResult{ForLLM: fmt.Sprintf("reply refused, nothing was sent: %v", err), IsError: true, Err: err}
	}
	// N2: an accepted reply counts as this round's send, so a plain closing
	// reply of the same turn is not ALSO sent to the unbound chat's default
	// destination (the unbound per-chat turn would otherwise answer twice).
	t.sentInRound.Store(true)
	return &ToolResult{ForLLM: fmt.Sprintf("Reply to %s accepted for sending (%s)", replyTo, receipt.Destination), Silent: true}
}

func (t *MessageTool) executePeerForm(ctx context.Context, content, wsArg string, hasWS bool, agentArg string, hasAgent bool) *ToolResult {
	workspaceID := strings.TrimSpace(wsArg)
	agentID := strings.TrimSpace(agentArg)
	if !hasAgent || agentID == "" {
		return addrRefuseWith(ErrPeerForm, "a peer send needs a non-empty agent_id (the recipient pair is "+
			"{workspace_id, agent_id}); nothing was sent and the current conversation is not used instead")
	}
	if hasWS && workspaceID == "" {
		return addrRefuseWith(ErrPeerForm, "workspace_id is blank; omit it for your current workspace or name the "+
			"recipient's workspace; nothing was sent")
	}
	if !hasWS {
		workspaceID = ToolWorkspaceID(ctx)
	}
	if workspaceID == "" {
		return addrRefuseWith(ErrPeerForm, "this turn has no workspace, so workspace_id must be named explicitly; nothing was sent")
	}
	if _, err := session.MainSessionID(workspaceID, agentID); err != nil {
		return addrRefuseWith(ErrPeerForm, "invalid recipient pair {workspace_id, agent_id}: %v; nothing was sent", err)
	}
	if strings.TrimSpace(content) == "" {
		return addrRefuseWith(ErrPeerForm, "a peer send needs non-empty content")
	}
	if t.peerRouter == nil {
		return addrRefuseWith(ErrPeerForm, "peer messaging is not configured here; nothing was sent")
	}
	sender := SendOrigin{AgentID: ToolAgentID(ctx), WorkspaceID: ToolWorkspaceID(ctx)}
	receipt, err := t.peerRouter.SendPeer(ctx, PeerRequest{
		RecipientWorkspaceID: workspaceID,
		RecipientAgentID:     agentID,
		Content:              content,
		Sender:               sender,
		SenderSessionID:      ToolTranscriptSessionID(ctx),
	})
	if err != nil {
		return &ToolResult{ForLLM: fmt.Sprintf("peer send refused, nothing was admitted: %v", err), IsError: true, Err: err}
	}
	return &ToolResult{
		ForLLM: fmt.Sprintf("Request %s admitted to %s/%s's main session", receipt.RequestID, workspaceID, agentID),
		Silent: true,
	}
}

// denySteeredAddressing keeps the addressed forms out of delegated sessions:
// a steered session messages only its own conversation (ADR-091 boundary 8),
// and the reply and peer forms both reach a different conversation.
func (t *MessageTool) denySteeredAddressing(ctx context.Context) *ToolResult {
	if t.steerAudience == nil {
		return nil
	}
	sessionID := ToolTranscriptSessionID(ctx)
	if sessionID == "" {
		return nil
	}
	audience, _, err := t.steerAudience.Audience(ctx, sessionID)
	if err != nil {
		audience = steer.AudienceNone
	}
	if audience == steer.AudienceUser {
		return nil
	}
	return &ToolResult{
		ForLLM: "steered_session_own_chat_only: a delegated session may message only its own " +
			"conversation — use message_parent to reach your parent",
		IsError: true,
		Err:     ErrSteeredSessionOwnChatOnly,
	}
}
