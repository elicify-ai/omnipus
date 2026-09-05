// Omnipus — AskUserQuestion tool tests (spec Tests 2, 5, 10, 11, 15 backend
// halves): park-time stub + ParksTurn, liveness/caller-scope gates, and the
// one-per-session inner-call error.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/askuser"
)

type fakeAskRegistry struct {
	created   []*askuser.PendingSet
	createErr error
	cancelled []string
}

func (f *fakeAskRegistry) CreatePending(set *askuser.PendingSet) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, set)
	return nil
}

func (f *fakeAskRegistry) PendingForSession(string) (*askuser.PendingSet, bool) { return nil, false }

func (f *fakeAskRegistry) CancelOnSessionStop(key string) bool {
	f.cancelled = append(f.cancelled, key)
	return true
}

func newAskTool(reg AskUserQuestionRegistry) *AskUserQuestionTool {
	return NewAskUserQuestionTool(func() AskUserQuestionRegistry { return reg })
}

func validAskArgs() map[string]any {
	return map[string]any{
		"questions": []any{
			map[string]any{
				"header":   "Scope",
				"question": "Which scope should this cover?",
				"options": []any{
					map[string]any{"label": "Backend only"},
					map[string]any{"label": "Full stack", "description": "SPA + backend"},
				},
				"recommended":  "Backend only",
				"default_safe": true,
			},
			map[string]any{
				"header":       "Deploy",
				"question":     "Deploy where?",
				"multi_select": true,
				"options": []any{
					map[string]any{"label": "Staging"},
					map[string]any{"label": "Prod"},
				},
			},
		},
	}
}

// webCtx builds an owner web-session tool context.
func webCtx() context.Context {
	ctx := context.Background()
	ctx = WithToolContext(ctx, "webchat", "chat-1")
	ctx = WithSessionKey(ctx, "session:session_owner_1")
	ctx = WithTranscriptSessionID(ctx, "session_owner_1")
	ctx = WithAgentID(ctx, "mia")
	ctx = WithSessionOwner(ctx, "alice")
	return ctx
}

func TestAskUserQuestion_ParksWithPendingStub(t *testing.T) {
	reg := &fakeAskRegistry{}
	tool := newAskTool(reg)

	res := tool.Execute(webCtx(), validAskArgs())
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	// ParksTurn semantics — the message_parent(question:true) precedent: the
	// loop ends the turn TurnEndStatusParked on exactly this signal.
	if !res.ParksTurn {
		t.Fatal("ParksTurn must be set on the success path")
	}
	// Park-time pending stub (C-R2-1): {status, card_id, question_count}.
	var stub struct {
		Status        string `json:"status"`
		CardID        string `json:"card_id"`
		QuestionCount int    `json:"question_count"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &stub); err != nil {
		t.Fatalf("stub does not parse: %v (%q)", err, res.ForLLM)
	}
	if stub.Status != "pending" || stub.QuestionCount != 2 || stub.CardID == "" {
		t.Fatalf("bad stub: %+v", stub)
	}
	// The registry received the ctx-derived identity.
	if len(reg.created) != 1 {
		t.Fatalf("want 1 CreatePending call, got %d", len(reg.created))
	}
	set := reg.created[0]
	if set.TranscriptSessionID != "session_owner_1" ||
		set.RoutingSessionKey != "session:session_owner_1" ||
		set.AgentID != "mia" || set.Channel != "webchat" || set.ChatID != "chat-1" ||
		set.Owner != "alice" || set.CardID != stub.CardID {
		t.Fatalf("set fields wrong: %+v", set)
	}
	if len(set.Questions) != 2 || set.Questions[0].Recommended != "Backend only" || !set.Questions[0].DefaultSafe || !set.Questions[1].MultiSelect {
		t.Fatalf("questions not carried faithfully: %+v", set.Questions)
	}
}

func TestAskUserQuestion_ValidationErrorDoesNotPark(t *testing.T) {
	reg := &fakeAskRegistry{}
	tool := newAskTool(reg)
	args := map[string]any{
		"questions": []any{
			map[string]any{
				"header":   "Scope",
				"question": "Which scope?",
				"options":  []any{map[string]any{"label": "Only one"}},
			},
		},
	}
	res := tool.Execute(webCtx(), args)
	if !res.IsError {
		t.Fatal("expected validation error")
	}
	if res.ParksTurn {
		t.Fatal("a failed call must never park the turn")
	}
	if len(reg.created) != 0 {
		t.Fatal("registry must not be touched on validation failure")
	}
}

func TestAskUserQuestion_DelegatedChildRejectedTowardMessageParent(t *testing.T) {
	reg := &fakeAskRegistry{}
	tool := newAskTool(reg)
	ctx := WithDelegationDepth(webCtx(), 1)
	res := tool.Execute(ctx, validAskArgs())
	if !res.IsError || res.ParksTurn {
		t.Fatalf("delegated child must be rejected without park: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "message_parent") {
		t.Fatalf("rejection must name message_parent(question:true): %q", res.ForLLM)
	}
	if len(reg.created) != 0 {
		t.Fatal("registry must not be touched")
	}
}

func TestAskUserQuestion_AutoDenyAskIsNoHumanSurface(t *testing.T) {
	reg := &fakeAskRegistry{}
	tool := newAskTool(reg)
	ctx := WithAutoDenyAsk(webCtx(), true)
	res := tool.Execute(ctx, validAskArgs())
	if !res.IsError || res.ParksTurn {
		t.Fatalf("AutoDenyAsk must reject without park: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "no_human_surface") {
		t.Fatalf("want no_human_surface-class error, got %q", res.ForLLM)
	}
}

func TestAskUserQuestion_ChannelOriginBlockedPermanently(t *testing.T) {
	// US-5 (operator ruling, interview #5): every non-web origin is blocked,
	// permanently — the agent asks conversationally instead. Never a silent
	// park a channel user can't see or answer (EC-10).
	for _, ch := range []string{"telegram", "discord", "whatsapp", ""} {
		reg := &fakeAskRegistry{}
		tool := newAskTool(reg)
		ctx := context.Background()
		ctx = WithToolContext(ctx, ch, "chat-9")
		ctx = WithSessionKey(ctx, "chat:"+ch+":chat-9")
		ctx = WithTranscriptSessionID(ctx, "session_chan_1")
		res := tool.Execute(ctx, validAskArgs())
		if !res.IsError || res.ParksTurn {
			t.Fatalf("channel %q must be blocked without park: %+v", ch, res)
		}
		if !strings.Contains(res.ForLLM, "no_human_surface") || !strings.Contains(res.ForLLM, "conversationally") {
			t.Fatalf("channel %q: blocked error must name the reason and the conversational fallback: %q", ch, res.ForLLM)
		}
		if len(reg.created) != 0 {
			t.Fatalf("channel %q: registry must not be touched", ch)
		}
	}
}

func TestAskUserQuestion_SecondCallErrorsOnePerSession(t *testing.T) {
	// EC-11 / US-6 S6: a turn running while a set pends gets a tool error
	// from its own AskUserQuestion call (one-per-routing-session).
	reg := &fakeAskRegistry{createErr: askuser.ErrAlreadyPending}
	tool := newAskTool(reg)
	res := tool.Execute(webCtx(), validAskArgs())
	if !res.IsError || res.ParksTurn {
		t.Fatalf("second call must error without park: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "already pending") {
		t.Fatalf("want one-per-session error, got %q", res.ForLLM)
	}
}

func TestAskUserQuestion_SaturatedRegistry(t *testing.T) {
	reg := &fakeAskRegistry{createErr: askuser.ErrSaturated}
	tool := newAskTool(reg)
	res := tool.Execute(webCtx(), validAskArgs())
	if !res.IsError || res.ParksTurn {
		t.Fatalf("saturated registry must error without park: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "capacity") {
		t.Fatalf("want capacity error, got %q", res.ForLLM)
	}
}

func TestAskUserQuestion_UnwiredRegistryFailsClosed(t *testing.T) {
	for _, tool := range []*AskUserQuestionTool{
		NewAskUserQuestionTool(nil),
		NewAskUserQuestionTool(func() AskUserQuestionRegistry { return nil }),
	} {
		res := tool.Execute(webCtx(), validAskArgs())
		if !res.IsError || res.ParksTurn {
			t.Fatalf("unwired registry must fail closed without park: %+v", res)
		}
		if !strings.Contains(res.ForLLM, "conversationally") {
			t.Fatalf("fail-closed error should point at the conversational fallback: %q", res.ForLLM)
		}
	}
}

func TestAskUserQuestion_RequiresSessionContext(t *testing.T) {
	reg := &fakeAskRegistry{}
	tool := newAskTool(reg)
	ctx := WithToolContext(context.Background(), "webchat", "chat-1")
	res := tool.Execute(ctx, validAskArgs())
	if !res.IsError || res.ParksTurn {
		t.Fatalf("missing transcript session must error: %+v", res)
	}
}

func TestAskUserQuestion_CatalogMetadata(t *testing.T) {
	tool := NewAskUserQuestionTool(nil)
	if tool.Name() != "AskUserQuestion" {
		t.Fatalf("name: %q", tool.Name())
	}
	if tool.Scope() != ScopeGeneral {
		t.Fatalf("scope: %q", tool.Scope())
	}
	if tool.Category() != CategoryCommunication {
		t.Fatalf("category: %q", tool.Category())
	}
	// The metadata catalog carries it (Constraint #6 coverage universe).
	found := false
	for _, mt := range GeneralBuiltinMetadata() {
		if mt.Name() == "AskUserQuestion" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("AskUserQuestion missing from GeneralBuiltinMetadata")
	}
}
