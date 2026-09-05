// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_ask_user.go — the gateway half of AskUserQuestion (askuserquestion-
// tool-spec v3 §3, ADR-074 D4b; W9b): bridges pkg/askuser's narrow seams to
// the wire.
//
//   - askUserCardSink (askuser.CardSink) → broadcasts an ask_user_question
//     frame (generated.AskUserQuestionFrame) to every connected WS client at
//     park time, on default-safe auto-resolutions, and on terminal
//     transitions.
//   - askUserResumeDispatcher (askuser.ResumeDispatcher) → publishes the
//     §0.2 correlated user-role answers message into the owner session's
//     turn machinery via the message bus (the same PublishInbound path the
//     WS chat handler uses).
//   - askUserAuditSink (askuser.AuditSink) → audit.EventAskUserAutoDefault
//     per 30-minute auto-resolution.
//   - handleAskUserAnswer → the inbound ask_user_answer frame handler:
//     Registry.Submit (server-validated, first-valid-wins) or CancelByUser.
//   - session_state's pending_asks snapshot is built in ws_tool_approval.go's
//     emitSessionState from Registry.PendingAll via toAskUserCard here.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
)

// toAskUserCard converts a pkg/askuser.PendingSet into the wire card shape.
// defaultSafeDelay is the registry's effective timer (30 minutes in
// production) — default_safe_at is materialized here so the SPA countdown
// needs no knowledge of the server constant.
func toAskUserCard(set *askuser.PendingSet, defaultSafeDelay time.Duration) generated.AskUserQuestionCard {
	card := generated.AskUserQuestionCard{
		CardId:    set.CardID,
		SessionId: set.TranscriptSessionID,
		AgentId:   set.AgentID,
		Status:    string(set.Status),
		CreatedAt: set.CreatedAt.UTC().Format(time.RFC3339),
	}

	hasDefaultSafe := false
	for i := range set.Questions {
		q := &set.Questions[i]
		if q.DefaultSafe {
			hasDefaultSafe = true
		}
		wq := struct {
			Context     *string `json:"context,omitempty"`
			DefaultSafe *bool   `json:"default_safe,omitempty"`
			Header      string  `json:"header"`
			MultiSelect *bool   `json:"multi_select,omitempty"`
			Options     []struct {
				Description *string `json:"description,omitempty"`
				Label       string  `json:"label"`
			} `json:"options"`
			Question    string  `json:"question"`
			Recommended *string `json:"recommended,omitempty"`
		}{Header: q.Header, Question: q.Question}
		for j := range q.Options {
			o := q.Options[j]
			wo := struct {
				Description *string `json:"description,omitempty"`
				Label       string  `json:"label"`
			}{Label: o.Label}
			if o.Description != "" {
				desc := o.Description
				wo.Description = &desc
			}
			wq.Options = append(wq.Options, wo)
		}
		if q.MultiSelect {
			v := true
			wq.MultiSelect = &v
		}
		if q.DefaultSafe {
			v := true
			wq.DefaultSafe = &v
		}
		if q.Recommended != "" {
			rec := q.Recommended
			wq.Recommended = &rec
		}
		if q.Context != "" {
			ctxText := q.Context
			wq.Context = &ctxText
		}
		card.Questions = append(card.Questions, wq)
	}

	if hasDefaultSafe && set.Status == askuser.StatusPending {
		at := set.CreatedAt.Add(defaultSafeDelay).UTC().Format(time.RFC3339)
		card.DefaultSafeAt = &at
	}
	for header := range set.AutoResolved {
		card.AutoResolved = append(card.AutoResolved, header)
	}
	for i := range set.Answers {
		a := &set.Answers[i]
		wa := struct {
			AutoDefault bool     `json:"auto_default"`
			FreeText    *string  `json:"free_text,omitempty"`
			Header      string   `json:"header"`
			Question    string   `json:"question"`
			Selected    []string `json:"selected,omitempty"`
		}{AutoDefault: a.AutoDefault, Header: a.Header, Question: a.QuestionText}
		if a.FreeText != nil {
			ft := *a.FreeText
			wa.FreeText = &ft
		}
		if len(a.Selected) > 0 {
			wa.Selected = append([]string(nil), a.Selected...)
		}
		card.Answers = append(card.Answers, wa)
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
		slog.Error("ws: marshal ask_user_question", "error", err)
		return
	}
	h.mu.Lock()
	conns := make([]*wsConn, 0, len(h.sessions))
	for _, wc := range h.sessions {
		conns = append(conns, wc)
	}
	h.mu.Unlock()
	for _, wc := range conns {
		select {
		case wc.sendCh <- raw:
		default:
			slog.Warn("ws: ask_user_question dropped — send buffer full",
				"card_id", card.CardId)
			wc.droppedFrames.Add(1)
		}
	}
}

// askUserCardSink adapts the registry's CardSink seam onto the WS broadcast.
// delayFn is bound to the live registry's EffectiveDefaultSafeDelay after
// construction (the registry needs the sink at construction time — the two
// are wired in setupAndStartServices).
type askUserCardSink struct {
	h       *WSHandler
	delayFn func() time.Duration
}

func (s *askUserCardSink) EmitCard(set *askuser.PendingSet) {
	if s.h == nil || set == nil {
		return
	}
	delay := askuser.DefaultSafeDelay
	if s.delayFn != nil {
		delay = s.delayFn()
	}
	s.h.broadcastAskUserCard(toAskUserCard(set, delay))
}

// askUserResumeDispatcher publishes the §0.2 resume message as a
// user-role bus.InboundMessage into the owner session — the same
// PublishInbound path the WS chat handler drives ordinary turns through.
type askUserResumeDispatcher struct {
	msgBus *bus.MessageBus
}

// resumeIsUserInitiated is the origin heuristic for the resume turn: a
// resume driven by a human submission/cancel is user-initiated; the server's
// all-default auto-submit (US-3 S3 — nobody was there) is not. Every
// auto-submitted answer carries AutoDefault, so "any non-auto answer" ⇔ a
// human acted; a cancelled set (no answers) reaches the dispatcher only via
// the card's Cancel button (session Stop skips the resume entirely), which
// is likewise a human action.
func resumeIsUserInitiated(set *askuser.PendingSet) bool {
	if set.Status == askuser.StatusCancelled {
		return true
	}
	for i := range set.Answers {
		if !set.Answers[i].AutoDefault {
			return true
		}
	}
	return false
}

func (d *askUserResumeDispatcher) DispatchResume(set *askuser.PendingSet, resumeText string) error {
	if d.msgBus == nil {
		return errors.New("askuser: resume dispatcher has no message bus")
	}
	userInitiated := resumeIsUserInitiated(set)
	msg := bus.InboundMessage{
		Channel:       set.Channel,
		ChatID:        set.ChatID,
		SessionID:     set.TranscriptSessionID,
		Content:       resumeText,
		UserInitiated: userInitiated,
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
	return d.msgBus.PublishInbound(ctx, msg)
}

// askUserAuditSink records one audit entry per default-safe auto-resolution
// (spec §4 / US-3 S2).
type askUserAuditSink struct {
	al *agent.AgentLoop
}

func (s *askUserAuditSink) RecordAutoDefault(cardID, sessionID, header, label string) {
	if s.al == nil {
		return
	}
	audit.Emit(context.Background(), s.al.AuditLogger(), audit.EventAskUserAutoDefault,
		audit.SeverityInfo, map[string]any{
			"card_id":    cardID,
			"session_id": sessionID,
			"header":     header,
			"label":      label,
		})
}

// handleAskUserAnswer bridges an inbound ask_user_answer frame to the
// registry: cancel:true → CancelByUser; otherwise Submit (server-validated,
// first-valid-wins). Every rejection is surfaced back to the submitting
// client as an error frame — never a silent drop (the card stays pending
// and the user must see why their Answer did nothing).
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
			if a.AutoDefault != nil {
				sa.AutoDefault = *a.AutoDefault
			}
			answers = append(answers, sa)
		}
		err = reg.Submit(f.CardId, f.SessionId, wc.userID, answers)
	}
	if err != nil {
		slog.Warn("ws: ask_user_answer rejected",
			"card_id", f.CardId, "session_id", f.SessionId, "error", err)
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "ask_user_answer: " + err.Error(),
		})
	}
}
