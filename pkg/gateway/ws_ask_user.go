// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_ask_user.go — the gateway half of AskUserQuestion (askuserquestion-
// tool-spec v3 §3, ADR-074 D4b; W9b): bridges pkg/askuser's narrow seams to
// the wire.
//
//   - askUserCardSink (askuser.CardSink) → broadcasts an ask_user_question
//     frame (generated.AskUserQuestionFrame) to every connected WS client at
//     park time and on answered/cancelled transitions.
//   - askUserResumeDispatcher (askuser.ResumeDispatcher) → publishes the
//     §0.2 correlated user-role answers message into the owner session's
//     turn machinery via the message bus (the same PublishInbound path the
//     WS chat handler uses).
//   - handleAskUserAnswer → the inbound ask_user_answer frame handler:
//     Registry.Submit (server-validated, first-valid-wins) or CancelByUser.
//   - session_state's pending_asks snapshot is built in ws_tool_approval.go's
//     emitSessionState from Registry.PendingAll via toAskUserCard here.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
)

// toAskUserCard converts a pkg/askuser.PendingSet into the wire card shape.
func toAskUserCard(set *askuser.PendingSet) generated.AskUserQuestionCard {
	card := generated.AskUserQuestionCard{
		CardId:    set.CardID,
		SessionId: set.TranscriptSessionID,
		AgentId:   set.AgentID,
		Status:    string(set.Status),
		CreatedAt: set.CreatedAt.UTC().Format(time.RFC3339),
	}

	// Elements are grown zero-valued and assigned field-by-field through
	// pointers into the generated slices (the setGoalStatusCriteria pattern)
	// — no locally-declared struct types, so the generated shapes stay the
	// only wire types in play (check-no-handwritten-wire-types).
	card.Questions = slices.Grow(card.Questions, len(set.Questions))[:len(set.Questions)]
	for i := range set.Questions {
		q := &set.Questions[i]
		dst := &card.Questions[i]
		dst.Header = q.Header
		dst.Question = q.Question
		dst.Options = slices.Grow(dst.Options, len(q.Options))[:len(q.Options)]
		for j := range q.Options {
			o := &q.Options[j]
			od := &dst.Options[j]
			od.Label = o.Label
			if o.Description != "" {
				desc := o.Description
				od.Description = &desc
			}
		}
		if q.MultiSelect {
			v := true
			dst.MultiSelect = &v
		}
		if q.Recommended != "" {
			rec := q.Recommended
			dst.Recommended = &rec
		}
		if q.Context != "" {
			ctxText := q.Context
			dst.Context = &ctxText
		}
	}

	if len(set.Answers) > 0 {
		card.Answers = slices.Grow(card.Answers, len(set.Answers))[:len(set.Answers)]
		for i := range set.Answers {
			a := &set.Answers[i]
			wa := &card.Answers[i]
			wa.Header = a.Header
			wa.Question = a.QuestionText
			if a.FreeText != nil {
				ft := *a.FreeText
				wa.FreeText = &ft
			}
			if len(a.Selected) > 0 {
				wa.Selected = append([]string(nil), a.Selected...)
			}
		}
	}
	return card
}

// broadcastAskUserCard sends an ask_user_question frame to every connected
// WS client (single-user model — mirrors broadcastToolApprovalRequired).
// Best-effort: a disconnected client re-hydrates from session_state's
// pending_asks snapshot on reconnect.
func (h *WSHandler) broadcastAskUserCard(card generated.AskUserQuestionCard) {
	frame := generated.AskUserQuestionFrame{
		Type: string(generated.WsFrameTypeAskUserQuestion),
		Card: card,
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		slog.Error("ws: marshal ask_user_question", "card_id", card.CardId, "error", err)
		return
	}
	// This caller reports one aggregate lifecycle outcome, so suppress the
	// shared helper's per-connection warning. dropCount counts connections
	// that were already closed or closing (#823: a live connection's queue
	// never drops; it reconnects and re-hydrates from session_state).
	fanoutCount, dropCount := h.broadcastRaw(raw, "")
	attrs := []any{
		"card_id", card.CardId,
		"status", card.Status,
		"fanout_count", fanoutCount,
		"enqueued_count", fanoutCount - dropCount,
		"drop_count", dropCount,
	}
	if dropCount > 0 {
		slog.Warn("ws: ask_user_question broadcast", attrs...)
		return
	}
	slog.Info("ws: ask_user_question broadcast", attrs...)
}

// askUserCardSink adapts the registry's CardSink seam onto the WS broadcast.
type askUserCardSink struct {
	h *WSHandler
}

func (s *askUserCardSink) EmitCard(set *askuser.PendingSet) {
	if s.h == nil || set == nil {
		return
	}
	s.h.broadcastAskUserCard(toAskUserCard(set))
}

// askUserResumeDispatcher publishes the §0.2 resume message as a
// user-role bus.InboundMessage into the owner session — the same
// PublishInbound path the WS chat handler drives ordinary turns through.
type askUserResumeDispatcher struct {
	msgBus *bus.MessageBus
}

func (d *askUserResumeDispatcher) DispatchResume(set *askuser.PendingSet, resumeText string) error {
	if d.msgBus == nil {
		err := errors.New("askuser: resume dispatcher has no message bus")
		slog.Warn("ws: ask_user resume dispatch completed",
			"card_id", set.CardID,
			"answer_count", len(set.Answers),
			"outcome", "stranded",
			"reason", "no_message_bus")
		return err
	}
	msg := bus.InboundMessage{
		Channel:       set.Channel,
		ChatID:        set.ChatID,
		SessionID:     set.TranscriptSessionID,
		Content:       resumeText,
		UserInitiated: true,
		Sender: bus.SenderInfo{
			CanonicalID: "webchat_user",
		},
		GatewayUserID: set.Owner,
	}
	if set.AgentID != "" {
		msg.Metadata = map[string]string{"agent_id": set.AgentID}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := d.msgBus.PublishInbound(ctx, msg)
	if err == nil {
		slog.Info("ws: ask_user resume dispatch completed",
			"card_id", set.CardID,
			"answer_count", len(set.Answers),
			"outcome", "delivered",
			"delivery_stage", "message_bus")
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		slog.Warn("ws: ask_user resume dispatch completed",
			"card_id", set.CardID,
			"answer_count", len(set.Answers),
			"outcome", "timed_out")
		return err
	}
	if errors.Is(err, bus.ErrBusClosed) {
		slog.Warn("ws: ask_user resume dispatch completed",
			"card_id", set.CardID,
			"answer_count", len(set.Answers),
			"outcome", "stranded",
			"reason", "bus_closed")
		return err
	}
	slog.Warn("ws: ask_user resume dispatch completed",
		"card_id", set.CardID,
		"answer_count", len(set.Answers),
		"outcome", "stranded",
		"reason", "publish_failed",
		"error_type", fmt.Sprintf("%T", err))
	return err
}

func askUserAnswerRejectionReason(err error) string {
	switch {
	case errors.Is(err, askuser.ErrNoPending):
		return "no_pending"
	case errors.Is(err, askuser.ErrSessionMismatch):
		return "session_mismatch"
	case errors.Is(err, askuser.ErrNotOwner):
		return "not_owner"
	case errors.Is(err, askuser.ErrAlreadyResolved):
		return "already_resolved"
	default:
		return "invalid_answer"
	}
}

func askUserResumeFailureOutcome(err error) (outcome, reason string) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timed_out", "message_bus_timeout"
	case errors.Is(err, bus.ErrBusClosed):
		return "stranded", "bus_closed"
	default:
		return "stranded", "resume_dispatch_failed"
	}
}

// handleAskUserAnswer bridges an inbound ask_user_answer frame to the
// registry: cancel:true → CancelByUser; otherwise Submit (server-validated,
// first-valid-wins). A validation rejection is surfaced back to the submitting
// client as an error frame and leaves the card pending. A failure to resume
// after acceptance is also surfaced, but the already-consumed card remains
// terminal; its log record distinguishes that outcome from a rejection.
func (h *WSHandler) handleAskUserAnswer(wc *wsConn, f generated.AskUserAnswerFrame) {
	reg := h.askUserReg
	if reg == nil {
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "ask_user_answer: no pending-question registry on this deployment",
		})
		return
	}

	var err error
	if f.Cancel != nil && *f.Cancel {
		err = reg.CancelByUser(f.CardId, f.SessionId)
	} else {
		answers := make([]askuser.SubmittedAnswer, 0, len(f.Answers))
		for i := range f.Answers {
			a := &f.Answers[i]
			sa := askuser.SubmittedAnswer{Header: a.Header}
			if a.FreeText != nil {
				ft := *a.FreeText
				sa.FreeText = &ft
			}
			if len(a.Selected) > 0 {
				sa.Selected = append([]string(nil), a.Selected...)
			}
			answers = append(answers, sa)
		}
		err = reg.Submit(f.CardId, f.SessionId, wc.userID, answers)
	}
	if err != nil {
		if errors.Is(err, askuser.ErrResumeDispatch) {
			outcome, reason := askUserResumeFailureOutcome(err)
			slog.Warn("ws: ask_user_answer accepted; resume failed",
				"card_id", f.CardId,
				"session_id", f.SessionId,
				"outcome", outcome,
				"reason", reason,
				"error", err) // the real cause, for the operator; the client frame stays fixed text
			sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: "ask_user_answer: your answer was accepted but the paused turn could not resume. Details are in the server log.",
			})
			return
		}
		slog.Info("ws: ask_user_answer rejected",
			"card_id", f.CardId,
			"session_id", f.SessionId,
			"reason", askUserAnswerRejectionReason(err))
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "ask_user_answer: " + err.Error(),
		})
	}
}
