// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// redirectOrdinarySession is /stop-redirect on an ordinary chat (founder
// decision 2026-10-06: it redirects the chat itself). It stops THIS session's
// current turn through the one Stop (session scope), and once that turn has
// stopped it continues the same chat with instruction as the next user
// message: exactly one new turn, through the same path a person's message
// takes. replyChannel/replyChatID name where the chat's replies go; empty
// values come from the session's own metadata.
//
// The wait for the stop runs on its own goroutine, owned by the loop (it
// counts as an active request and ends on shutdown); a redirect that cannot
// be applied is told to the chat, never dropped.
func (al *AgentLoop) redirectOrdinarySession(ctx context.Context, sessionID, instruction, userID, replyChannel, replyChatID string) error {
	sessionID = strings.TrimSpace(sessionID)
	instruction = strings.TrimSpace(instruction)
	switch {
	case sessionID == "":
		return fmt.Errorf("redirect requires a session")
	case strings.TrimSpace(userID) == "":
		return fmt.Errorf("redirect requires an authenticated sender")
	case instruction == "":
		return fmt.Errorf("redirect requires an instruction")
	}
	msg, err := al.ordinaryRedirectMessage(sessionID, instruction, userID, replyChannel, replyChatID)
	if err != nil {
		return err
	}
	res, err := al.StopSession(ctx, StopRequest{
		SessionID: sessionID,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: userID},
		Channel:   msg.Channel,
	})
	if err != nil {
		return fmt.Errorf("stop this chat's current turn: %w", err)
	}
	if res.RootErr != nil {
		return fmt.Errorf("stop this chat's current turn: %w", res.RootErr)
	}
	if len(res.Report.Unreachable) > 0 {
		return fmt.Errorf("stop this chat's current turn: %s", res.Report.Unreachable[0].Reason)
	}
	if !al.beginActiveRequest() {
		return fmt.Errorf("the agent is shutting down; the current turn was stopped but the new instruction was not applied")
	}
	waitCtx := al.inboundRunContext()
	go func() {
		defer al.endActiveRequest()
		al.continueOrdinaryAfterStop(waitCtx, msg)
	}()
	return nil
}

// ordinaryRedirectMessage builds the person's next message for the chat:
// its own channel and chat (from metadata when not given), the person as
// sender, and the chat's own agent.
func (al *AgentLoop) ordinaryRedirectMessage(sessionID, instruction, userID, replyChannel, replyChatID string) (bus.InboundMessage, error) {
	store := al.ResolveSessionStore(sessionID)
	if store == nil {
		return bus.InboundMessage{}, fmt.Errorf("redirect: session %q not found", sessionID)
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		return bus.InboundMessage{}, fmt.Errorf("redirect: read session %q: %w", sessionID, errors.Join(err, errNilMeta(meta)))
	}
	channel := strings.TrimSpace(replyChannel)
	if channel == "" || channel == "web" {
		channel = strings.TrimSpace(meta.Channel)
	}
	if channel == "" {
		channel = "webchat"
	}
	chatID := strings.TrimSpace(replyChatID)
	if chatID == "" {
		chatID = sessionID
	}
	agentID := strings.TrimSpace(meta.ActiveAgentID)
	if lifecycle := al.GetSessionLifecycleStore(); lifecycle != nil {
		if rec, loadErr := lifecycle.Load(sessionID); loadErr == nil && strings.TrimSpace(rec.AgentID) != "" {
			agentID = rec.AgentID
		}
	}
	msg := bus.InboundMessage{
		Channel: channel, ChatID: chatID, SessionID: sessionID, Content: instruction,
		Sender: bus.SenderInfo{CanonicalID: userID}, GatewayUserID: userID, UserInitiated: true,
	}
	if agentID != "" {
		msg.Metadata = map[string]string{"agent_id": agentID}
	}
	return msg, nil
}

func errNilMeta(meta *session.UnifiedMeta) error {
	if meta == nil {
		return errors.New("no session metadata")
	}
	return nil
}

// continueOrdinaryAfterStop waits until the chat's stopped turn has ended,
// then records the instruction as the chat's next user message and runs one
// turn with it. A wait that ends without the turn stopping (deadline,
// shutdown) or a refused recording is reported to the chat.
func (al *AgentLoop) continueOrdinaryAfterStop(ctx context.Context, msg bus.InboundMessage) {
	deadline := time.Now().Add(redirectWaitDeadline)
	poll := time.NewTicker(redirectPollInterval)
	defer poll.Stop()
	for !al.ordinaryTurnStopped(msg.SessionID) {
		if time.Now().After(deadline) {
			al.reportUndeliveredOrdinaryRedirect(msg, fmt.Errorf("the current turn did not stop within %s", redirectWaitDeadline))
			return
		}
		select {
		case <-ctx.Done():
			al.reportUndeliveredOrdinaryRedirect(msg, fmt.Errorf("the agent shut down before the current turn stopped: %w", ctx.Err()))
			return
		case <-poll.C:
		}
	}
	// A web chat's user messages are recorded by the web intake; this
	// message does not pass through it, so it is recorded here. Every other
	// channel's turn records its own user message.
	if msg.Channel == "webchat" {
		store := al.ResolveSessionStore(msg.SessionID)
		if store == nil {
			al.reportUndeliveredOrdinaryRedirect(msg, fmt.Errorf("session %q has no store", msg.SessionID))
			return
		}
		if err := store.AppendTranscriptStrict(msg.SessionID, session.TranscriptEntry{
			ID: "redirect-" + uuid.NewString(), Role: "user", Content: msg.Content,
			AgentID: msg.Metadata["agent_id"], Timestamp: time.Now().UTC(),
		}); err != nil {
			al.reportUndeliveredOrdinaryRedirect(msg, fmt.Errorf("the instruction could not be saved: %w", err))
			return
		}
	}
	route, _, err := al.resolveMessageRoute(msg)
	if err != nil {
		al.reportUndeliveredOrdinaryRedirect(msg, fmt.Errorf("route the instruction: %w", err))
		return
	}
	al.runRevivedOrdinaryTurn(msg, resolveScopeKey(route, msg.SessionKey))
}

// ordinaryTurnStopped reports whether the chat has no live turn and no Stop
// still in flight.
func (al *AgentLoop) ordinaryTurnStopped(sessionID string) bool {
	if al.activeTurnForCancel(sessionID, CancelScope{SessionID: sessionID, TurnOnly: true}) != nil {
		return false
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return true
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return errors.Is(err, session.ErrLifecycleNotFound)
	}
	return !lifecycleInFlightStopFence(rec)
}

// reportUndeliveredOrdinaryRedirect tells the chat that its redirect
// instruction was not applied, and logs it.
func (al *AgentLoop) reportUndeliveredOrdinaryRedirect(msg bus.InboundMessage, cause error) {
	logger.ErrorCF("agent", "redirect: the new instruction was NOT applied to the chat",
		map[string]any{"session_id": msg.SessionID, "error": cause.Error()})
	replyCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := al.bus.PublishOutbound(replyCtx, bus.OutboundMessage{
		Channel: msg.Channel, ChatID: msg.ChatID, SessionID: msg.SessionID,
		Content: fmt.Sprintf("Redirect was not applied: %v. Send the instruction again.", cause),
	}); err != nil {
		logger.ErrorCF("agent", "redirect: the not-applied notice could not be sent",
			map[string]any{"session_id": msg.SessionID, "error": err.Error()})
	}
}
