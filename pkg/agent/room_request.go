// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// room_request.go: the agent side of group-chat rooms (omnipus-system-agent
// US-17, §6.11, FR-200, FR-214–FR-217, FR-251, FR-252).
//
// A room is not a session and no agent works inside it. Each room message is
// fanned out by the room service (pkg/rooms, wired by the gateway) as one
// request per addressed member, admitted here into the member's MAIN session:
//
//   - AdmitRoomRequest is the changed admission: the caller supplies the
//     request id (the room's stable delivery id, FR-216), the request never
//     joins a running turn (no-join intake, FR-214), and a receiver whose
//     send_message policy is Ask gets the approval card in ITS OWN main chat,
//     labelled "for group <name>" (FR-251) — a room has no sender session.
//   - A committed capture for the delivery id is the receiver fact a retry
//     reconciles against: it is reported as already admitted and nothing is
//     appended or woken again (FR-216). The receiver append goes through the
//     named RoomReceiverWriter seam, so a tester can fault-inject it.
//   - Before a room request is dequeued into a turn the room is asked again
//     under its lock (RoomRuntime.BeginRoomConsume, the pause fence of
//     FR-217); a cancelled request is discarded, never run.
//   - The answer (answer_request on a room capture) returns through the room
//     reply branch (RoomRuntime.DeliverRoomAnswer, FR-215): appended to the
//     room, mirrored once into the responder's main chat, mentions fanned
//     out; no agent owner is woken.

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/promptcache"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Metadata keys a room request's owner wake carries (the no-join marker and
// the room it belongs to).
const (
	roomRequestMetadataKey = "room_request"
	roomIDMetadataKey      = "room_id"
)

// Room admission refusals. Every one of them admitted nothing.
var (
	// ErrRoomRequestRefused: the member's send_message policy denies requests,
	// or the person denied (or let time out) the member's approval card.
	ErrRoomRequestRefused = errors.New("room request refused")
	// ErrRoomReceiverUnavailable: the member is no longer an eligible main
	// (removed, deleted, a worker or system agent), or its conversation could
	// not be prepared.
	ErrRoomReceiverUnavailable = errors.New("room member unavailable")
	// ErrRoomRequestCancelled: the room cancelled the request (pause, member
	// removal, room deletion) before it was admitted.
	ErrRoomRequestCancelled = errors.New("room request cancelled")
	// ErrRoomStorage: a server-side storage failure; nothing was admitted.
	ErrRoomStorage = errors.New("room request storage failure")
)

// RoomConsumeDecision is the room's answer at the dequeue fence (FR-217).
type RoomConsumeDecision int

const (
	// RoomConsumeRun: the request is current and now "working"; run it.
	RoomConsumeRun RoomConsumeDecision = iota
	// RoomConsumeCancel: the room cancelled it (or it is settled); discard
	// it, never run it.
	RoomConsumeCancel
	// RoomConsumeDefer: the room could not decide or record now, or a turn
	// already took the request: neither run nor discard it. It stays
	// undelivered in the member's ledger and is offered again on the
	// member's next turn or a Retry.
	RoomConsumeDefer
)

// RoomFence is the full dequeue fence a room service may also provide (the
// gateway's does): it tells "cancelled" from "could not decide now", and can
// release a consume whose request never reached the turn. A RoomRuntime
// without it is treated as answering run / cancel only.
type RoomFence interface {
	DecideRoomConsume(roomID, requestID, sessionID string) RoomConsumeDecision
	RoomConsumeReleased(roomID, requestID string) error
}

// RoomRuntime is the room service as the agent loop sees it. The gateway
// installs it (SetRoomRuntime); every call is made without agent locks held.
type RoomRuntime interface {
	// BeginRoomConsume is the dequeue fence: under the room lock it reports
	// whether the request may run now (still current, room and member still
	// there) and, if so, marks it consumed ("working"). A runtime that is
	// also a RoomFence is asked through DecideRoomConsume instead.
	BeginRoomConsume(roomID, requestID, sessionID string) bool
	// RoomConsumeHeld reports that a consumed request met a stopped member:
	// it is held again and delivered when the member runs.
	RoomConsumeHeld(roomID, requestID string)
	// RoomConsumeEnded reports that the turn consuming the request ended with
	// turnErr (nil: ended without error; the answer, if any, already arrived).
	RoomConsumeEnded(roomID, requestID string, turnErr error) (discardCapture bool, completionErr error)
	// RoomReminderTurnEnded reports that the one unanswered-request reminder
	// turn of sessionID ended: room requests still unanswered there failed.
	RoomReminderTurnEnded(sessionID string) error
	// RoomTurnCost reports the cost of a turn that consumed a room request.
	RoomTurnCost(report RoomTurnCost)
	// DeliverRoomAnswer is the room reply branch (FR-215).
	DeliverRoomAnswer(ctx context.Context, answer RoomAnswer) (tools.ReplyReceipt, error)
}

// RoomTurnCost is one consumed room request's turn, for §6.14 allocation.
type RoomTurnCost struct {
	RoomID, RequestID, SessionID, TurnID string
	// CostUSD is the turn's billed cost; Priced is false when the turn
	// reported no cost figure at all.
	CostUSD float64
	Priced  bool
	// Inputs is the number of inputs the turn consumed by its end (the room
	// request itself plus every message steered into it). RoomInputs is how
	// many of them came from this room (several held requests may be inputs
	// at the start of a reviving turn; they never join an already-running turn).
	Inputs, RoomInputs int
}

// RoomAnswer is an answer_request call on a room capture.
type RoomAnswer struct {
	RoomID, RequestID string
	Responder         addressing.Pair
	// ResponderSessionID is the member's main session the request was
	// admitted to (where the answer is mirrored).
	ResponderSessionID string
	Content            string
}

// RoomReceiverWriter is the named seam on the room receiver-commit path
// (FR-216, BDD-17.32): the member-main appends of a room request and of the
// answer mirror go through it. Production uses the session store's
// AppendTranscriptOnce (the append and its duplicate check run under one
// session hold); a test injects a writer that faults at the cut it wants.
type RoomReceiverWriter interface {
	AppendTranscriptOnce(sessionID string, entry session.TranscriptEntry) (bool, error)
}

// SetRoomRuntime installs the room service. Nil removes it.
func (al *AgentLoop) SetRoomRuntime(rt RoomRuntime) {
	if rt == nil {
		al.roomRuntime.Store(nil)
		return
	}
	al.roomRuntime.Store(&rt)
}

func (al *AgentLoop) loadRoomRuntime() RoomRuntime {
	if p := al.roomRuntime.Load(); p != nil {
		return *p
	}
	return nil
}

// SetRoomReceiverWriter replaces the receiver-commit writer (the FR-216 fault
// seam). Nil restores the session store.
func (al *AgentLoop) SetRoomReceiverWriter(w RoomReceiverWriter) {
	if w == nil {
		al.roomWriter.Store(nil)
		return
	}
	al.roomWriter.Store(&w)
}

func (al *AgentLoop) roomReceiverWriter() (RoomReceiverWriter, error) {
	if p := al.roomWriter.Load(); p != nil {
		return *p, nil
	}
	store := al.GetSessionStore()
	if store == nil {
		return nil, errors.New("session store unavailable")
	}
	return store, nil
}

// RoomRequestAdmission is one (room message, member) delivery to admit.
type RoomRequestAdmission struct {
	Receiver addressing.Pair
	// DeliveryID is the room's stable delivery id; it becomes the receiver's
	// request id, capture id and transcript entry id (FR-216).
	DeliveryID string
	RoomID     string
	RoomName   string
	Sender     addressing.Sender
	// SenderLabel names the sender in the request header ("the user",
	// "agent Jim").
	SenderLabel string
	// DisplayText is what the member's main chat shows as the collapsed entry
	// (its first line is the room message, FR-202). ModelText is the body the
	// member's model reads (room context + message), wrapped here in the
	// server-composed request header.
	DisplayText string
	ModelText   string
	// OnAwaitingApproval runs just before the member's approval card is
	// raised (the room shows awaiting_approval).
	OnAwaitingApproval func()
	// BeforeCommit runs after the final authorization and before any receiver
	// write; an error aborts the admission with nothing written.
	BeforeCommit func() error
}

// RoomAdmission is the outcome of a successful AdmitRoomRequest.
type RoomAdmission struct {
	ReceiverSessionID string
	// Held: the member's main is stopped; the request waits until it runs.
	Held bool
	// AlreadyAdmitted: a capture for the delivery id already existed (a
	// retry after a crash); nothing was written or woken by this call.
	AlreadyAdmitted bool
}

// AdmitRoomRequest admits one room request into the member's main session.
func (al *AgentLoop) AdmitRoomRequest(ctx context.Context, a RoomRequestAdmission) (RoomAdmission, error) {
	store := al.GetSessionStore()
	ledger := al.RequestLedger()
	if store == nil || ledger == nil {
		return RoomAdmission{}, fmt.Errorf("%w: session store unavailable", ErrRoomStorage)
	}
	if err := a.validate(); err != nil {
		return RoomAdmission{}, err
	}
	if err := al.roomReceiverEligible(a.Receiver); err != nil {
		return RoomAdmission{}, err
	}
	meta, err := store.GetOrCreateMainSession(a.Receiver.WorkspaceID, a.Receiver.AgentID)
	if err != nil {
		return RoomAdmission{}, roomStorageRefusal("the member's main chat could not be prepared", err)
	}
	mainID := meta.ID
	// FR-216: the receiver's facts come first. A committed capture for this
	// delivery id is an admission already made: report it, write nothing.
	if prior, found, gErr := ledger.Get(mainID, a.DeliveryID); gErr != nil {
		return RoomAdmission{}, roomStorageRefusal("the member's request record could not be read", gErr)
	} else if found {
		if prior.Discarded {
			return RoomAdmission{}, fmt.Errorf("%w: the request was discarded", ErrRoomRequestCancelled)
		}
		return al.rewakeRoomRequest(ctx, prior, mainID), nil
	}
	if err := al.authorizeRoomRequest(ctx, a, mainID); err != nil {
		return RoomAdmission{}, err
	}
	if a.BeforeCommit != nil {
		if err := a.BeforeCommit(); err != nil {
			return RoomAdmission{}, err
		}
	}
	return al.commitRoomRequest(ctx, a, mainID)
}

// rewakeRoomRequest handles a retry of a request the member already has
// (FR-216): nothing is written again. A request no turn has taken yet is
// woken again unless its member is stopped (then it is held and delivered
// when the member runs) or a wake for it is already on its way; a duplicate
// wake is harmless - the dequeue fence runs a request once.
func (al *AgentLoop) rewakeRoomRequest(ctx context.Context, prior addressing.Capture, mainID string) RoomAdmission {
	out := RoomAdmission{ReceiverSessionID: mainID, AlreadyAdmitted: true}
	if prior.Delivered {
		return out
	}
	if al.receiverStopped(mainID) {
		out.Held = true
		return out
	}
	if !al.claimWakeSlot(prior.RequestID) {
		return out // its wake is already on its way
	}
	al.pendingRequests.Store(prior.RequestID, mainID)
	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := al.publishRoomRequestWake(pubCtx, prior, true); err != nil {
		al.heldRepublished.Delete(prior.RequestID)
		logger.ErrorCF("agent", "a retried room request could not be woken; it stays queued for the member's next turn or the next Retry",
			map[string]any{"request_id": prior.RequestID, "session_id": mainID, "error": err.Error()})
	}
	return out
}

func (a RoomRequestAdmission) validate() error {
	if err := a.Receiver.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrRoomReceiverUnavailable, err)
	}
	if strings.TrimSpace(a.DeliveryID) == "" || len(a.DeliveryID) > addressing.MaxComponentLen ||
		strings.ContainsAny(a.DeliveryID, `/\`) {
		return fmt.Errorf("%w: invalid delivery id", ErrRoomStorage)
	}
	if strings.TrimSpace(a.RoomID) == "" {
		return fmt.Errorf("%w: missing room id", ErrRoomStorage)
	}
	return nil
}

// roomReceiverEligible re-reads the member's eligibility now (member of the
// workspace, a chat target).
func (al *AgentLoop) roomReceiverEligible(receiver addressing.Pair) error {
	deps := al.loadAddressDeps()
	if deps == nil {
		return fmt.Errorf("%w: messaging is not available", ErrRoomReceiverUnavailable)
	}
	ok, err := deps.PairEligible(receiver.WorkspaceID, receiver.AgentID)
	if err != nil {
		return roomStorageRefusal("the member's workspace membership could not be checked", err)
	}
	if !ok {
		return fmt.Errorf("%w: %s is not a current member of the workspace", ErrRoomReceiverUnavailable, receiver.AgentID)
	}
	return nil
}

// authorizeRoomRequest applies the member's existing send_message policy:
// allow admits, deny refuses, ask raises the card in the member's main chat
// (FR-251) and re-reads eligibility and policy once the person decided.
func (al *AgentLoop) authorizeRoomRequest(ctx context.Context, a RoomRequestAdmission, mainID string) error {
	policy, err := al.roomReceiverPolicy(a.Receiver)
	if err != nil {
		return err
	}
	switch policy {
	case string(config.ToolPolicyAllow):
		return nil
	case string(config.ToolPolicyAsk):
	default:
		return fmt.Errorf("%w: the member's send_message policy denies requests", ErrRoomRequestRefused)
	}
	if a.OnAwaitingApproval != nil {
		a.OnAwaitingApproval()
	}
	approved, reason := al.requestRoomApproval(ctx, a, mainID)
	if !approved {
		if reason == "cancel" || ctx.Err() != nil {
			return fmt.Errorf("%w: the approval was cancelled", ErrRoomRequestCancelled)
		}
		return fmt.Errorf("%w: the approval was not given (%s)", ErrRoomRequestRefused, reason)
	}
	// The decision can take as long as a person needs: membership and policy
	// are read again before anything is written.
	if err = al.roomReceiverEligible(a.Receiver); err != nil {
		return err
	}
	policy, err = al.roomReceiverPolicy(a.Receiver)
	if err != nil {
		return err
	}
	if policy != string(config.ToolPolicyAllow) && policy != string(config.ToolPolicyAsk) {
		return fmt.Errorf("%w: the member's send_message policy denies requests", ErrRoomRequestRefused)
	}
	return nil
}

func (al *AgentLoop) roomReceiverPolicy(receiver addressing.Pair) (string, error) {
	policy, err := al.receiverSendPolicy(receiver)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrRoomReceiverUnavailable, err)
	}
	return policy, nil
}

// requestRoomApproval raises the room's pre-admission card in the member's
// main chat: request id = delivery id, room_ref, origin label "for group
// <name>" (FR-251). A standing grant settles it without a card.
func (al *AgentLoop) requestRoomApproval(ctx context.Context, a RoomRequestAdmission, mainID string) (bool, string) {
	args := map[string]any{"room_id": a.RoomID, "room_name": a.RoomName, "from": a.SenderLabel}
	if al.checkStandingGrant(mainID, a.Receiver.AgentID, "send_message", args) {
		return true, ""
	}
	approved, reason, _ := al.loadToolApprover().RequestApproval(ctx, PolicyApprovalReq{
		ToolCallID: a.DeliveryID,
		ToolName:   "send_message",
		Args:       args,
		AgentID:    a.Receiver.AgentID,
		SessionID:  mainID,
		// The card belongs to no turn of the member (the request is not
		// admitted yet); the wire contract needs a non-empty turn id.
		TurnID:           "room-" + a.DeliveryID,
		RoomRef:          &generated.RoomRef{RoomId: a.RoomID, RoomName: a.RoomName, RequestId: a.DeliveryID, Role: "request"},
		OriginLabel:      "for group " + a.RoomName,
		RoomPreAdmission: true,
	})
	return approved, reason
}

// commitRoomRequest writes the capture, then the member's transcript entry,
// then publishes the no-join wake.
func (al *AgentLoop) commitRoomRequest(ctx context.Context, a RoomRequestAdmission, mainID string) (RoomAdmission, error) {
	ledger := al.RequestLedger()
	writer, err := al.roomReceiverWriter()
	if err != nil {
		return RoomAdmission{}, roomStorageRefusal("the member's main chat is not available", err)
	}
	held := al.receiverStopped(mainID) && !al.newHumanRoomCanRestart(a, mainID)
	now := time.Now().UTC()
	humanRestartBoot := uint64(0)
	if authenticatedHumanRoomSender(a.Sender) {
		humanRestartBoot = al.bootEpochFor()
	}
	capture := addressing.Capture{
		RequestID:         a.DeliveryID,
		HumanRestartBoot:  humanRestartBoot,
		ReceiverSessionID: mainID,
		Receiver:          a.Receiver,
		Sender:            a.Sender,
		Source:            addressing.Source{Kind: addressing.SourceRoom, RoomID: a.RoomID, RoomName: a.RoomName},
		AdmittedAt:        now,
		Text:              composeRoomRequestText(a.DeliveryID, a.SenderLabel, a.RoomName, a.ModelText),
		Held:              held,
	}
	entry := session.TranscriptEntry{
		ID:          a.DeliveryID,
		Role:        "user",
		AgentID:     a.Receiver.AgentID,
		Content:     a.DisplayText,
		Timestamp:   now,
		Participant: participantFromSender(a.Sender, al.agentDisplayName),
		RoomRef:     &generated.RoomRef{RoomId: a.RoomID, RoomName: a.RoomName, RequestId: a.DeliveryID, Role: "request"},
	}
	// The receiver append comes FIRST (WU-15 ruling 06:00Z): a failed append
	// leaves no capture at all - the member shows "unavailable", no turn runs,
	// and a Retry admits it cleanly. The append is idempotent by entry id, so
	// a Retry after a failed capture write below appends nothing twice.
	wrote, err := writer.AppendTranscriptOnce(mainID, entry)
	if err != nil {
		return RoomAdmission{}, roomStorageRefusal("the request could not be recorded in the member's main chat", err)
	}
	if err = ledger.Put(capture); err != nil {
		return RoomAdmission{}, roomStorageRefusal("the request could not be recorded", err)
	}
	al.pendingRequests.Store(a.DeliveryID, mainID)
	if wrote {
		al.publishUserEntry(mainID, entry)
	}
	if held {
		// A stopped member: the request waits in its ledger and is delivered
		// when the member runs again (FR-252). No wake.
		return RoomAdmission{ReceiverSessionID: mainID, Held: true}, nil
	}
	al.claimWakeSlot(a.DeliveryID)
	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := al.publishRoomRequestWake(pubCtx, capture, true); err != nil {
		// The request is recorded and queued, but its turn did not start. It
		// is NOT "held" (that means the member is stopped): it stays queued,
		// is given to the member's next turn, and a Retry wakes it again.
		al.heldRepublished.Delete(a.DeliveryID)
		logger.ErrorCF("agent", "room request recorded but the member's turn could not be started; it stays queued for the member's next turn or a Retry",
			map[string]any{"request_id": a.DeliveryID, "session_id": mainID, "error": err.Error()})
	}
	return RoomAdmission{ReceiverSessionID: mainID}, nil
}

// publishRoomRequestWake is the owner wake of a room request: the ordinary
// request wake plus the room id and, for the admission wake, the no-join
// marker (FR-214: it never joins a running turn). A held request is not
// published: it is handed to the turn that revives its member, at that
// turn's start (deliverHeldRoomRequest).
func (al *AgentLoop) publishRoomRequestWake(ctx context.Context, c addressing.Capture, noJoin bool) error {
	extra := map[string]string{roomIDMetadataKey: c.Source.RoomID}
	if noJoin {
		extra[roomRequestMetadataKey] = "1"
	}
	al.roomRequests.Store(c.RequestID, c.Source.RoomID)
	return al.publishRequestWake(ctx, c.Receiver, c.RequestID, c.ReceiverSessionID, c.Text, extra)
}

// deliverHeldRoomRequest hands a room request that waited (its member was
// stopped, or its wake never ran) to the turn of its member that is being
// admitted now - before that turn's first model call, so it is one of the
// turn's starting inputs, never steered into a turn already under way
// (FR-214, ruling A 2026-10-15; BDD-17.30 counts it in that turn's cost). It
// passes the room fence first; the turn's end settles it (settleRoomInputs).
func (al *AgentLoop) deliverHeldRoomRequest(sessionID string, c addressing.Capture, meters ...*roomCostMeter) {
	msg := roomWakeMessage(c)
	al.roomRequests.Store(c.RequestID, c.Source.RoomID)
	g, proceed := al.gateRoomRequest(msg)
	if !proceed {
		return
	}
	if err := al.enqueueHeldRoomInput(msg, meters); err != nil {
		if undoErr := releaseRoomConsume(g.rt, g.roomID, g.requestID); undoErr != nil {
			logger.ErrorCF("agent", "a waiting room request did not reach the turn and its queue reset is pending storage recovery",
				map[string]any{"session_id": sessionID, "request_id": c.RequestID, "error": err.Error(), "undo_error": undoErr.Error()})
			return
		}
		logger.ErrorCF("agent", "a waiting room request did not reach the turn; it is queued again",
			map[string]any{"session_id": sessionID, "request_id": c.RequestID, "error": err.Error()})
		return
	}
	for _, meter := range meters {
		meter.ownInput(c.RequestID, c.Source.RoomID)
	}
	al.markRequestDelivered(sessionID, c.RequestID)
}

// roomWakeMessage is the inbound form of a room capture (the same message
// publishRoomRequestWake sends, without the no-join marker).
func roomWakeMessage(c addressing.Capture) bus.InboundMessage {
	return bus.InboundMessage{
		Channel:           "webchat",
		ChatID:            "request:" + c.RequestID,
		Sender:            bus.SenderInfo{CanonicalID: "request:" + c.RequestID},
		Content:           c.Text,
		SessionID:         c.ReceiverSessionID,
		TranscriptEntryID: c.RequestID,
		Metadata: map[string]string{
			"agent_id": c.Receiver.AgentID, "workspace_id": c.Receiver.WorkspaceID,
			ownerWakeMetadataKey: "1", roomIDMetadataKey: c.Source.RoomID,
		},
	}
}

// composeRoomRequestText is the server-composed envelope the member's model
// reads. The body (room context and message) is untrusted text written by
// the person and the other members.
func composeRoomRequestText(requestID, senderLabel, roomName, body string) string {
	if strings.TrimSpace(senderLabel) == "" {
		senderLabel = "a member"
	}
	return fmt.Sprintf("Request %s from %s in the group chat %q. To answer it, call the answer_request tool with "+
		"request_id=%q and your answer as text; your answer is posted in the group. A plain reply in this "+
		"conversation does not reach the group.\n\n%s", requestID, senderLabel, roomName, requestID, body)
}

func roomStorageRefusal(what string, cause error) error {
	logger.ErrorCF("agent", "room request: server-side failure", map[string]any{"what": what, "error": cause.Error()})
	return fmt.Errorf("%w: %s; details are in the server log", ErrRoomStorage, what)
}

// isRoomRequestWake reports whether msg is a room request's owner wake.
func isRoomRequestWake(msg bus.InboundMessage) bool {
	return msg.Metadata[roomIDMetadataKey] != "" && msg.Metadata[ownerWakeMetadataKey] != ""
}

// isNoJoinRoomWake reports whether msg is a room request's admission wake,
// which never joins a running turn (FR-214).
func isNoJoinRoomWake(msg bus.InboundMessage) bool {
	return isRoomRequestWake(msg) && msg.Metadata[roomRequestMetadataKey] != ""
}

// replyIntoRoom is the Reply branch for a room capture (FR-215).
func (al *AgentLoop) replyIntoRoom(ctx context.Context, c addressing.Capture, responder addressing.Pair, req tools.ReplyRequest) (tools.ReplyReceipt, error) {
	rt := al.loadRoomRuntime()
	if rt == nil {
		return tools.ReplyReceipt{}, errors.New("group chats are not available; the answer was not posted")
	}
	return rt.DeliverRoomAnswer(ctx, RoomAnswer{
		RoomID: c.Source.RoomID, RequestID: c.RequestID, Responder: responder,
		ResponderSessionID: c.ReceiverSessionID, Content: req.Content,
	})
}

// MirrorRoomAnswer writes the member's room answer once into its main chat as
// a chat-only entry carrying room_ref role "answer" (FR-202, FR-215) and shows
// it live. mirrorID is derived by the room from the answer, so a repeated
// call after a crash writes nothing twice.
func (al *AgentLoop) MirrorRoomAnswer(mainSessionID, mirrorID string, author addressing.Pair, ref generated.RoomRef, content string) error {
	writer, err := al.roomReceiverWriter()
	if err != nil {
		return err
	}
	entry := session.TranscriptEntry{
		ID: mirrorID, Role: "assistant", AgentID: author.AgentID, Content: content,
		Timestamp: time.Now().UTC(), RoomRef: &ref,
	}
	wrote, err := writer.AppendTranscriptOnce(mainSessionID, entry)
	if err != nil {
		return err
	}
	if wrote {
		if deps := al.loadAddressDeps(); deps != nil {
			deps.PublishGuestReply(mainSessionID, entry)
		}
	}
	return nil
}

// roomGate carries one dequeued room request through its turn.
type roomGate struct {
	rt                RoomRuntime
	roomID, requestID string
}

// gateRoomRequest is the FR-217 dequeue fence. It returns proceed=false when
// the room refused the request; the request is then discarded, never run.
func (al *AgentLoop) gateRoomRequest(msg bus.InboundMessage) (*roomGate, bool) {
	if !isRoomRequestWake(msg) {
		return nil, true
	}
	rt := al.loadRoomRuntime()
	if rt == nil {
		// No room service: nothing can say the request was cancelled, and a
		// request that cannot reach its room must not run unseen.
		logger.ErrorCF("agent", "room request dropped: the room service is not wired",
			map[string]any{"session_id": msg.SessionID, "request_id": msg.TranscriptEntryID})
		return nil, false
	}
	g := &roomGate{rt: rt, roomID: msg.Metadata[roomIDMetadataKey], requestID: msg.TranscriptEntryID}
	al.roomRequests.Store(g.requestID, g.roomID)
	if al.receiverStopped(msg.SessionID) && !al.roomWakeCanRestart(msg) {
		// A wake that meets a stopped member is dropped; the request stays
		// held in the member's ledger and is delivered when the member runs
		// again (FR-252). It never shows "working" in between.
		al.heldRepublished.Delete(g.requestID)
		logger.InfoCF("agent", "room request stays held: its member is stopped",
			map[string]any{"session_id": msg.SessionID, "request_id": g.requestID, "room_id": g.roomID})
		return nil, false
	}
	switch decideRoomConsume(rt, g.roomID, g.requestID, msg.SessionID) {
	case RoomConsumeRun:
		return g, true
	case RoomConsumeDefer:
		// Not run and not discarded: the request stays undelivered in the
		// member's ledger and is offered again on its next turn or a Retry
		// (the room logged why it deferred).
		al.heldRepublished.Delete(g.requestID)
		logger.WarnCF("agent", "room request is not runnable now; its capture is retained for later delivery or answer repair",
			map[string]any{"session_id": msg.SessionID, "request_id": g.requestID, "room_id": g.roomID})
		return nil, false
	default:
		al.heldRepublished.Delete(g.requestID)
		al.roomRequests.Delete(g.requestID)
		if err := al.DiscardRequest(msg.SessionID, g.requestID); err != nil {
			logger.ErrorCF("agent", "a cancelled room request could not be discarded; it is refused again on its next delivery",
				map[string]any{"session_id": msg.SessionID, "request_id": g.requestID, "error": err.Error()})
		}
		logger.InfoCF("agent", "room request not run: the room cancelled it",
			map[string]any{"session_id": msg.SessionID, "request_id": g.requestID, "room_id": g.roomID})
		return nil, false
	}
}

func decideRoomConsume(rt RoomRuntime, roomID, requestID, sessionID string) RoomConsumeDecision {
	if fence, ok := rt.(RoomFence); ok {
		return fence.DecideRoomConsume(roomID, requestID, sessionID)
	}
	if rt.BeginRoomConsume(roomID, requestID, sessionID) {
		return RoomConsumeRun
	}
	return RoomConsumeCancel
}

func releaseRoomConsume(rt RoomRuntime, roomID, requestID string) error {
	if fence, ok := rt.(RoomFence); ok {
		return fence.RoomConsumeReleased(roomID, requestID)
	}
	return errors.New("room runtime cannot release a failed handoff")
}

// prepared reports a failed turn admission to the room: a stopped member
// holds the request again, anything else ends it.
func (g *roomGate) prepared(err error) error {
	if g == nil || err == nil {
		return nil
	}
	if errors.Is(err, errOwnerWakeRetained) {
		g.rt.RoomConsumeHeld(g.roomID, g.requestID)
		return nil
	}
	_, completionErr := g.rt.RoomConsumeEnded(g.roomID, g.requestID, err)
	return completionErr
}

// newTurnRoomMeter is the cost meter of one ordinary turn: one input (the
// message that started it), a room input when that message is a room
// request; steered inputs are added as the turn consumes them.
func (al *AgentLoop) newTurnRoomMeter(msg bus.InboundMessage) *roomCostMeter {
	m := &roomCostMeter{al: al, sessionID: msg.SessionID, inputs: 1, rooms: map[string]int{}}
	if isRoomRequestWake(msg) {
		m.rooms[msg.Metadata[roomIDMetadataKey]]++
		m.ownInput(msg.TranscriptEntryID, msg.Metadata[roomIDMetadataKey])
	}
	return m
}

// reportRoomCost reports, at the end of a turn, its cost share to every room
// whose requests it consumed (§6.14).
func (al *AgentLoop) reportRoomCost(m *roomCostMeter, sessionID string) {
	rt := al.loadRoomRuntime()
	if rt == nil || m == nil {
		return
	}
	turnID, cost, unpriced, inputs, rooms := m.snapshot()
	if turnID == "" || len(rooms) == 0 {
		return // no model call, or no room input: nothing to allocate
	}
	for roomID, n := range rooms {
		rt.RoomTurnCost(RoomTurnCost{
			RoomID: roomID, SessionID: sessionID, TurnID: turnID,
			CostUSD: cost, Priced: !unpriced, Inputs: inputs, RoomInputs: n,
		})
	}
}

// settleRoomInputs runs when a turn ended: a turn that failed fails (or, if
// it was cancelled, interrupts) every room request it owns, including the
// starting message. This includes a hard abort with a public nil error. A
// successful turn leaves outstanding requests to their answers and the one
// reminder whose end fails them.
func (al *AgentLoop) settleRoomInputs(m *roomCostMeter, turnErr error) error {
	rt := al.loadRoomRuntime()
	if rt == nil || m == nil || turnErr == nil {
		return nil
	}
	m.mu.Lock()
	joined := make(map[string]string, len(m.joined))
	for req, room := range m.joined {
		joined[req] = room
	}
	m.mu.Unlock()
	al.dropRoomSteering(joined)
	var result error
	for req, room := range joined {
		result = errors.Join(result, al.completeRoomInput(rt, room, req, m.sessionID, turnErr))
	}
	return result
}

// roomTurnEnded runs when a worker turn's processMessage returned: a failed
// room-request turn fails its request; the end of a reminder turn fails the
// room requests still unanswered in that session.
func (al *AgentLoop) roomTurnEnded(msg bus.InboundMessage, err error) error {
	rt := al.loadRoomRuntime()
	if rt == nil {
		return nil
	}
	var result error
	if isRoomRequestWake(msg) && err != nil {
		result = al.completeRoomInput(rt, msg.Metadata[roomIDMetadataKey], msg.TranscriptEntryID, msg.SessionID, err)
	}
	if msg.Metadata[requestReminderMetadataKey] != "" {
		result = errors.Join(result, rt.RoomReminderTurnEnded(msg.SessionID))
	}
	return result
}

// steerRoomRequest is the fence for a held room request re-published into a
// running turn (trySteerIntoLiveTurn): it reports whether the request may be
// given to that turn. A refused request is discarded.
func (al *AgentLoop) steerRoomRequest(msg bus.InboundMessage) bool {
	_, proceed := al.gateRoomRequest(msg)
	return proceed
}

// RoomCallPricer is the named deterministic pricing seam of the room cost
// (§6.14, BDD-17.30): when installed, the cost the room allocation reads for
// each model call comes from it instead of the built-in estimate, and the
// call counts as priced. It changes nothing else: the turn's own stats, the
// done frame and the transcript keep the catalog price. Nil (production)
// uses the catalog price of the call, priced unless the catalog classes it
// unpriced (§6.14 "unknown price").
type RoomCallPricer func(model string, usage providers.UsageInfo) float64

// SetRoomCallPricer installs (or, with nil, removes) the room pricing seam.
func (al *AgentLoop) SetRoomCallPricer(p RoomCallPricer) {
	if p == nil {
		al.roomPricer.Store(nil)
		return
	}
	al.roomPricer.Store(&p)
}

// roomCallCost is the cost of one model call as the room allocation reads it,
// and whether its price is known: the installed pricer, or else the call's own
// catalog price (pkg/agent/prompt_cache_accounting.go::accountProviderCall).
func (al *AgentLoop) roomCallCost(model string, usage *providers.UsageInfo, price promptcache.PriceResult) (float64, bool) {
	if p := al.roomPricer.Load(); p != nil && usage != nil {
		return (*p)(model, *usage), true
	}
	return price.Cost, roomPriceKnown(price)
}

// roomPriceKnown reports whether a call's catalog price is known as a whole,
// as the room allocation needs it (§6.14: a turn with an unknown price adds
// nothing and marks "plus unpriced turns"). A fully unpriced call is not, and
// neither is a MIXED call: the caching accounting keeps it "priced" while it
// carries unpriced tokens, and its dollars cover only the priced portions
// (gate round 3, architect R3-N1). A subscription call is a known zero.
func roomPriceKnown(price promptcache.PriceResult) bool {
	return price.BillingClass != promptcache.BillingClassUnpriced && price.UnpricedTokens == 0
}

// roomCostMeter accumulates the cost and consumed inputs of one ordinary turn
// and which of those inputs were room requests (by room). Methods are
// nil-safe so the turn code can call them unconditionally.
type roomCostMeter struct {
	al        *AgentLoop
	sessionID string
	scope     string
	mu        sync.Mutex
	turnID    string
	cost      float64
	unpriced  bool
	inputs    int
	rooms     map[string]int
	// joined are all room requests owned by this turn, including a starting
	// room message (request id -> room id); settlement is independent of cost.
	joined   map[string]string
	finished bool
}

// addCall debits one accounted provider call to the room allocation (§6.14):
// the loop's installed room pricer when there is one, else the call's own
// catalog price. Called from the one accounting path
// (prompt_cache_accounting.go::accountProviderCall); nil-safe like the rest.
func (m *roomCostMeter) addCall(turnID, model string, usage *providers.UsageInfo, price promptcache.PriceResult) {
	if m == nil {
		return
	}
	cost, priced := price.Cost, roomPriceKnown(price)
	if m.al != nil {
		cost, priced = m.al.roomCallCost(model, usage, price)
	}
	m.addCost(turnID, cost, priced)
}

func (m *roomCostMeter) addCost(turnID string, cost float64, priced bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.turnID == "" {
		m.turnID = turnID
	}
	if priced {
		m.cost += cost
		m.mu.Unlock()
		return
	}
	first := !m.unpriced
	m.unpriced = true
	m.mu.Unlock()
	if first && m.al != nil {
		// An unknown price is known the moment the call is debited: the rooms
		// show "plus unpriced turns" now. The turn adds no money (§6.14), so
		// the report at its end changes nothing (each turn counts once).
		m.al.reportRoomCost(m, m.sessionID)
	}
}

// roomSteerCorrelationPrefix marks the steering correlation id of a room
// request that joined a running turn (enqueueSteeringFromMessage).
const roomSteerCorrelationPrefix = "room-request:"

func roomSteerCorrelation(requestID string) string { return roomSteerCorrelationPrefix + requestID }

// addInputs counts n inputs the turn just consumed; ids are their steering
// correlation ids, used to find the room requests among them.
func (m *roomCostMeter) addInputs(ids []string, n int) {
	if m == nil || n <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputs += n
	if m.al == nil {
		return
	}
	for _, id := range ids {
		requestID, isRoom := strings.CutPrefix(id, roomSteerCorrelationPrefix)
		if !isRoom {
			continue
		}
		if roomID, ok := m.al.roomRequests.Load(requestID); ok {
			if r, isString := roomID.(string); isString && r != "" {
				m.rooms[r]++
				if m.joined == nil {
					m.joined = map[string]string{}
				}
				m.joined[requestID] = r
			}
		}
	}
}

func (m *roomCostMeter) snapshot() (turnID string, cost float64, unpriced bool, inputs int, rooms map[string]int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int, len(m.rooms))
	for k, v := range m.rooms {
		out[k] = v
	}
	return m.turnID, m.cost, m.unpriced, m.inputs, out
}
