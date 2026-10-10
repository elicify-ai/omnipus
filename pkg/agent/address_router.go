package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Session-core U8 (C-ADDRESS / C-REPLY, FR-027/028/045): peer requests and
// source-owner replies. The tool (pkg/tools/message_address.go) validates the
// call shape; this file is the server side:
//
//   - SendPeer admits a request to an eligible pair's main session after the
//     RECEIVER's existing send_message policy and the still-a-member check;
//   - AdmitRequest is the one place a request is written: the capture first,
//     then the entry, so a request the model can see is always answerable;
//   - Reply resolves reply_to in the responder's own ledger and returns the
//     answer through the capture's source (conversation append, or the source
//     owner's connector via bus.ReturnRoute and the final egress check).
//
// Trust decisions (security review): see pkg/addressing's package comment
// (T1-T4) plus
//
//	T5  Eligibility (member + chat-target, Admin default-workspace exception)
//	    comes from AddressDeps, not from the model.
//	T6  The answer's author is the responder taken from the turn context
//	    (ToolAgentID/ToolWorkspaceID), never from arguments; it grants nothing.
//	T7  Receiver policy is read from the receiver's own AgentInstance via the
//	    existing two-layer resolver; no new permission map.

// AddressDeps are the gateway-owned facts and delivery the router needs.
type AddressDeps interface {
	// PairEligible reports whether (workspaceID, agentID) currently owns an
	// eligible main: a member (or Admin in the default workspace) that is a
	// chat target. An unreadable workspace is an error, never "eligible".
	PairEligible(workspaceID, agentID string) (bool, error)
	// PublishGuestReply delivers an already-persisted guest reply to the
	// source session's live viewers.
	PublishGuestReply(sessionID string, entry session.TranscriptEntry)
}

// SetAddressDeps installs the gateway seam. Safe to call after boot.
func (al *AgentLoop) SetAddressDeps(d AddressDeps) { al.addressDeps.Store(&d) }

func (al *AgentLoop) loadAddressDeps() AddressDeps {
	if p := al.addressDeps.Load(); p != nil {
		return *p
	}
	return nil
}

// RequestLedger returns the request ledger rooted at the session store, or
// nil when no store is wired.
func (al *AgentLoop) RequestLedger() *addressing.Ledger {
	store := al.GetSessionStore()
	if store == nil {
		return nil
	}
	if l := al.requestLedger.Load(); l != nil && l.dir == store.BaseDir() {
		return l.ledger
	}
	l := &ledgerHolder{dir: store.BaseDir(), ledger: addressing.NewLedger(store.BaseDir())}
	al.requestLedger.Store(l)
	return l.ledger
}

type ledgerHolder struct {
	dir    string
	ledger *addressing.Ledger
}

// AddressRouter implements tools.PeerRouter and tools.ReplyRouter over a live
// AgentLoop. Tools hold this value, not a snapshot, so a reload never leaves
// a stale router behind.
type AddressRouter struct{ al *AgentLoop }

var (
	_ tools.PeerRouter  = AddressRouter{}
	_ tools.ReplyRouter = AddressRouter{}
)

// ErrPeerRefused wraps every refusal that admitted nothing.
var ErrPeerRefused = errors.New("peer request refused")

// RequestAdmission describes a request to admit to a receiver's main session.
type RequestAdmission struct {
	Receiver addressing.Pair
	// Content is the request as its sender wrote it.
	Content string
	Sender  addressing.Sender
	Source  addressing.Source
	// SenderLabel is the human-readable origin shown in the request header.
	SenderLabel string
}

// AdmitRequest records the capture, appends the request to the receiver's main
// session and starts (or joins) the receiver's turn through the ordinary
// intake. It returns the admitted request id (the entry id the store wrote).
func (al *AgentLoop) AdmitRequest(ctx context.Context, a RequestAdmission) (requestID, receiverSessionID string, err error) {
	store := al.GetSessionStore()
	ledger := al.RequestLedger()
	if store == nil || ledger == nil {
		return "", "", fmt.Errorf("%w: session store unavailable", ErrPeerRefused)
	}
	if err := a.Receiver.Validate(); err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrPeerRefused, err)
	}
	meta, err := store.GetOrCreateMainSession(a.Receiver.WorkspaceID, a.Receiver.AgentID)
	if err != nil {
		return "", "", fmt.Errorf("%w: receiver main session: %v", ErrPeerRefused, err)
	}
	receiverSessionID = meta.ID
	requestID = uuid.New().String()

	capture := addressing.Capture{
		RequestID:         requestID,
		ReceiverSessionID: receiverSessionID,
		Receiver:          a.Receiver,
		Sender:            a.Sender,
		Source:            a.Source,
		AdmittedAt:        time.Now().UTC(),
	}
	// Capture first: if the append below fails the capture is discarded; if it
	// were written second, a request the model can see might be unanswerable.
	if err := ledger.Put(capture); err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrPeerRefused, err)
	}
	entry := session.TranscriptEntry{
		ID:        requestID,
		Role:      "user",
		AgentID:   a.Receiver.AgentID,
		Content:   composeRequestText(requestID, a.SenderLabel, a.Content),
		Timestamp: capture.AdmittedAt,
	}
	if err := store.AppendTranscriptStrict(receiverSessionID, entry); err != nil {
		if dErr := ledger.Discard(receiverSessionID, requestID); dErr != nil {
			logger.WarnCF("agent", "request capture could not be discarded after a failed append",
				map[string]any{"request_id": requestID, "error": dErr.Error()})
		}
		return "", "", fmt.Errorf("%w: could not record the request: %v", ErrPeerRefused, err)
	}

	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := al.bus.PublishInbound(pubCtx, bus.InboundMessage{
		Channel:           "webchat",
		ChatID:            "request:" + requestID,
		Sender:            bus.SenderInfo{CanonicalID: "request:" + requestID},
		Content:           entry.Content,
		SessionID:         receiverSessionID,
		TranscriptEntryID: requestID,
		Metadata:          map[string]string{"agent_id": a.Receiver.AgentID, "workspace_id": a.Receiver.WorkspaceID},
		// Not UserInitiated / OperatorPrompt: a request is not a live human
		// action on the receiver's session (fail closed for /goal, /loop and
		// the browser wheel).
	}); err != nil {
		// The request is durable in the receiver's main and answerable; only
		// the turn start failed. Report it rather than hide it.
		return requestID, receiverSessionID, fmt.Errorf("request %s was recorded but the receiver's turn could not be started: %w", requestID, err)
	}
	return requestID, receiverSessionID, nil
}

// composeRequestText is the server-composed envelope the receiver reads. The
// body is untrusted text; the header is the only part the server vouches for
// and the ledger, not this text, is what authorizes an answer.
func composeRequestText(requestID, senderLabel, body string) string {
	if strings.TrimSpace(senderLabel) == "" {
		senderLabel = "a sender"
	}
	return fmt.Sprintf("Request %s from %s. To answer it, call send_message with reply_to=%q and your answer as content; "+
		"a plain reply in this conversation does not reach the sender.\n\n%s", requestID, senderLabel, requestID, body)
}

// SendPeer implements tools.PeerRouter.
func (r AddressRouter) SendPeer(ctx context.Context, req tools.PeerRequest) (tools.PeerReceipt, error) {
	al := r.al
	receiver := addressing.Pair{WorkspaceID: req.RecipientWorkspaceID, AgentID: req.RecipientAgentID}
	sender := addressing.Pair{WorkspaceID: req.Sender.WorkspaceID, AgentID: req.Sender.AgentID}
	if sender.IsZero() || req.SenderSessionID == "" {
		return tools.PeerReceipt{}, fmt.Errorf("%w: the sending turn has no agent or session identity", ErrPeerRefused)
	}
	if receiver == sender {
		return tools.PeerReceipt{}, fmt.Errorf("%w: an agent cannot send a request to itself", ErrPeerRefused)
	}
	if err := al.CheckPeerAdmission(ctx, receiver, req.SenderSessionID); err != nil {
		return tools.PeerReceipt{}, err
	}
	id, sid, err := al.AdmitRequest(ctx, RequestAdmission{
		Receiver:    receiver,
		Content:     req.Content,
		Sender:      addressing.Sender{Agent: sender},
		SenderLabel: fmt.Sprintf("agent %s in workspace %s", sender.AgentID, sender.WorkspaceID),
		Source: addressing.Source{
			Kind:      addressing.SourceConversation,
			Owner:     sender,
			SessionID: req.SenderSessionID,
		},
	})
	if err != nil {
		return tools.PeerReceipt{RequestID: id, SessionID: sid}, err
	}
	return tools.PeerReceipt{RequestID: id, SessionID: sid}, nil
}

// CheckPeerAdmission is the whole admission rule for addressing a peer pair,
// shared by the send_message tool and the web @recipient path so neither can
// drift: the pair is valid, owns an eligible main NOW (member check; Admin's
// default-workspace exception lives in AddressDeps), and the RECEIVER's
// send_message policy admits it. It writes nothing.
func (al *AgentLoop) CheckPeerAdmission(ctx context.Context, receiver addressing.Pair, senderSessionID string) error {
	deps := al.loadAddressDeps()
	if deps == nil {
		return fmt.Errorf("%w: peer messaging is not available", ErrPeerRefused)
	}
	if err := receiver.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrPeerRefused, err)
	}
	ok, err := deps.PairEligible(receiver.WorkspaceID, receiver.AgentID)
	if err != nil {
		return fmt.Errorf("%w: membership could not be read: %v", ErrPeerRefused, err)
	}
	if !ok {
		return fmt.Errorf("%w: %s/%s is not an eligible main (not a current member, a worker, or a system agent)",
			ErrPeerRefused, receiver.WorkspaceID, receiver.AgentID)
	}
	return al.admitByReceiverPolicy(ctx, receiver, senderSessionID)
}

// admitByReceiverPolicy applies the RECEIVER's existing send_message policy
// (two-layer resolver) before anything is written: deny refuses, ask uses the
// existing approval path, allow admits (FR-045, F9).
func (al *AgentLoop) admitByReceiverPolicy(ctx context.Context, receiver addressing.Pair, senderSessionID string) error {
	reg := al.GetRegistry()
	if reg == nil {
		return fmt.Errorf("%w: agent registry unavailable", ErrPeerRefused)
	}
	inst, ok := reg.GetAgent(receiver.AgentID)
	if !ok || inst == nil {
		return fmt.Errorf("%w: agent %q is not available", ErrPeerRefused, receiver.AgentID)
	}
	cfg := inst.LoadToolPolicy()
	if cfg == nil {
		return fmt.Errorf("%w: the receiver's tool policy is unreadable", ErrPeerRefused)
	}
	switch tools.ResolveEffectivePolicy(cfg, "send_message") {
	case string(config.ToolPolicyAllow):
		return nil
	case string(config.ToolPolicyAsk):
		approved, reason, _ := al.CheckGrantOrRequestApproval(ctx, senderSessionID, receiver.AgentID,
			"send_message", "peer-"+uuid.New().String(), "",
			map[string]any{"recipient_workspace_id": receiver.WorkspaceID, "recipient_agent_id": receiver.AgentID})
		if !approved {
			return fmt.Errorf("%w: the receiver's send_message policy is ask and it was not approved (%s)", ErrPeerRefused, reason)
		}
		return nil
	default:
		return fmt.Errorf("%w: the receiver's send_message policy denies requests", ErrPeerRefused)
	}
}

// Reply implements tools.ReplyRouter.
func (r AddressRouter) Reply(ctx context.Context, req tools.ReplyRequest) (tools.ReplyReceipt, error) {
	al := r.al
	ledger := al.RequestLedger()
	if ledger == nil {
		return tools.ReplyReceipt{}, errors.New("session store unavailable")
	}
	responder := addressing.Pair{WorkspaceID: req.Author.WorkspaceID, AgentID: req.Author.AgentID}
	if responder.IsZero() || req.ResponderSessionID == "" {
		return tools.ReplyReceipt{}, errors.New("this turn has no agent or session identity to answer from")
	}
	capture, err := ledger.Resolve(req.ResponderSessionID, responder, req.ReplyTo)
	if err != nil {
		return tools.ReplyReceipt{}, replyRefusal(err)
	}
	switch capture.Source.Kind {
	case addressing.SourceConnector:
		return al.replyViaConnector(ctx, capture, responder, req)
	case addressing.SourceConversation:
		return al.replyIntoConversation(capture, responder, req)
	default:
		return tools.ReplyReceipt{}, fmt.Errorf("%w", addressing.ErrUnusableCorrelation)
	}
}

func replyRefusal(err error) error {
	switch {
	case errors.Is(err, addressing.ErrUnknownRequest):
		return errors.New("no such request is waiting for an answer from you in this conversation")
	case errors.Is(err, addressing.ErrDiscarded):
		return errors.New("that request was discarded before it was delivered and can no longer be answered")
	default:
		return err
	}
}

// replyViaConnector hands the answer to the source owner's existing bus/worker
// path. The route carries the owner captured at admission; pkg/channels
// re-verifies it at the final send boundary (nothing here grants the author a
// connector).
func (al *AgentLoop) replyViaConnector(ctx context.Context, c addressing.Capture, responder addressing.Pair, req tools.ReplyRequest) (tools.ReplyReceipt, error) {
	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	msg := bus.OutboundMessage{
		Channel:          c.Source.InstanceID,
		ChatID:           c.Source.ChatID,
		SessionID:        c.Source.SessionID,
		Content:          req.Content,
		ReplyToMessageID: c.Source.PlatformMessageID,
		// The author is the responder; OwnershipChecked stays false because
		// the author owns nothing here — the Return route is what authorizes.
		AgentID:     responder.AgentID,
		WorkspaceID: responder.WorkspaceID,
		Return: &bus.ReturnRoute{
			RequestID:        c.RequestID,
			SourceSessionID:  c.Source.SessionID,
			OwnerWorkspaceID: c.Source.Owner.WorkspaceID,
			OwnerAgentID:     c.Source.Owner.AgentID,
		},
	}
	if err := al.bus.PublishOutbound(pubCtx, msg); err != nil {
		return tools.ReplyReceipt{}, fmt.Errorf("the reply could not be queued: %w", err)
	}
	return tools.ReplyReceipt{Destination: "the original conversation on " + c.Source.InstanceID}, nil
}

// replyIntoConversation appends the answer to the source session, authored by
// the responder (the guest), and publishes it live. A peer-agent source is
// then woken through the ordinary intake so it sees the answer.
func (al *AgentLoop) replyIntoConversation(c addressing.Capture, responder addressing.Pair, req tools.ReplyRequest) (tools.ReplyReceipt, error) {
	store := al.GetSessionStore()
	if store == nil {
		return tools.ReplyReceipt{}, errors.New("session store unavailable")
	}
	// Final binding check for a conversation source: the source session must
	// still exist and still belong to the captured owner.
	meta, err := store.GetMeta(c.Source.SessionID)
	if err != nil || meta == nil {
		return tools.ReplyReceipt{}, errors.New("the conversation this request came from is no longer available")
	}
	if !c.Source.Owner.IsZero() && meta.AgentID != c.Source.Owner.AgentID {
		return tools.ReplyReceipt{}, errors.New("the conversation this request came from no longer belongs to the agent that sent it")
	}
	entry := session.TranscriptEntry{
		ID:               uuid.New().String(),
		Role:             "assistant",
		AgentID:          responder.AgentID,
		Content:          req.Content,
		Timestamp:        time.Now().UTC(),
		ReplyToMessageID: c.RequestID,
	}
	if err := store.AppendTranscriptStrict(c.Source.SessionID, entry); err != nil {
		return tools.ReplyReceipt{}, fmt.Errorf("the reply could not be saved: %w", err)
	}
	if deps := al.loadAddressDeps(); deps != nil {
		deps.PublishGuestReply(c.Source.SessionID, entry)
	}
	if !c.Sender.Agent.IsZero() {
		al.wakeRequestSender(c, entry)
	}
	return tools.ReplyReceipt{ReplyID: entry.ID, Destination: "the conversation it came from"}, nil
}

// wakeRequestSender starts (or joins) the requesting agent's turn so it sees
// the answer. It goes through the ordinary intake, so an already-running turn
// takes it at its next safe boundary and an idle session starts exactly one.
func (al *AgentLoop) wakeRequestSender(c addressing.Capture, answer session.TranscriptEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := al.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:   "webchat",
		ChatID:    "answer:" + answer.ID,
		Sender:    bus.SenderInfo{CanonicalID: "answer:" + answer.ID},
		Content:   fmt.Sprintf("%s answered request %s (see the answer above).", answer.AgentID, c.RequestID),
		SessionID: c.Source.SessionID,
		Metadata:  map[string]string{"agent_id": c.Sender.Agent.AgentID, "workspace_id": c.Sender.Agent.WorkspaceID},
	})
	if err != nil {
		logger.WarnCF("agent", "could not wake the requesting agent after an answer",
			map[string]any{"request_id": c.RequestID, "error": err.Error()})
	}
}

// DiscardRequest marks an admitted request as discarded before delivery (Stop
// before the receiver consumed it), so it can no longer be answered. A request
// with no capture is a no-op.
func (al *AgentLoop) DiscardRequest(receiverSessionID, requestID string) error {
	ledger := al.RequestLedger()
	if ledger == nil {
		return errors.New("session store unavailable")
	}
	return ledger.Discard(receiverSessionID, requestID)
}

// NewAddressRouter returns the router tools hold. Exported for wiring tests.
func (al *AgentLoop) NewAddressRouter() AddressRouter { return AddressRouter{al: al} }
