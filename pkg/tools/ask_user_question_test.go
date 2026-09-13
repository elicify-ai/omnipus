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

func (f *fakeAskRegistry) CancelByUser(cardID, _ string) error {
	f.cancelled = append(f.cancelled, cardID)
	return nil
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

// TestAskUserQuestion_RecommendedIsQuestionScoped guards the doc/schema
// consistency the operator reported (2026-09-06): `recommended`/`default_safe`
// are QUESTION-level properties, never option-level, and the Description must
// say so rather than telling the model to "mark an option recommended" (which
// led it to put the property inside an option, where additionalProperties
// rejects it).
func TestAskUserQuestion_RecommendedIsQuestionScoped(t *testing.T) {
	tool := NewAskUserQuestionTool(nil)

	// Description must not resurrect the misleading option-scoped phrasing.
	desc := tool.Description()
	if strings.Contains(desc, "Mark an option `recommended`") {
		t.Errorf("Description resurrects the misleading option-scoped phrasing that caused the reported bug")
	}
	if !strings.Contains(desc, "QUESTION's `recommended`") {
		t.Errorf("Description must scope `recommended` to the question level; got: %q", desc)
	}

	// Schema: question properties carry recommended/default_safe; option
	// properties carry only label/description.
	qProps := askQuestionItemProps(t, tool.Parameters())
	for _, k := range []string{"recommended", "default_safe"} {
		if _, ok := qProps[k]; !ok {
			t.Errorf("question schema missing %q property", k)
		}
	}
	optItems, _ := qProps["options"].(map[string]any)["items"].(map[string]any)
	optProps, _ := optItems["properties"].(map[string]any)
	for _, forbidden := range []string{"recommended", "default_safe"} {
		if _, ok := optProps[forbidden]; ok {
			t.Errorf("option schema must NOT carry %q (it is question-scoped)", forbidden)
		}
	}
	if _, ok := optProps["label"]; !ok {
		t.Error("option schema must carry label")
	}
}

// askQuestionItemProps returns the per-question schema properties map from the
// tool's Parameters() (properties.questions.items.properties).
func askQuestionItemProps(t *testing.T, params map[string]any) map[string]any {
	t.Helper()
	props, _ := params["properties"].(map[string]any)
	questions, _ := props["questions"].(map[string]any)
	items, _ := questions["items"].(map[string]any)
	qProps, _ := items["properties"].(map[string]any)
	if qProps == nil {
		t.Fatalf("could not navigate to questions.items.properties in Parameters()")
	}
	return qProps
}

// --- NormalizeArgs (fix-wave GX-B, DEFECT 1) ---
//
// The operator's reproduction: the model emitted an option object carrying
// a `recommended` property. The registry's shared validateToolArgs rejected
// the WHOLE call with `unexpected property "recommended"` — and this was
// the FORCED first move of a goal turn, so the failure cascaded into 17
// minutes of ungoverned building. NormalizeArgs (pkg/tools/registry.go's
// argsNormalizer seam) now lifts a truthy option-level `recommended` onto
// the question instead of failing the call, while leaving every other
// unexpected property exactly as strict as before.

// askArgsWithOptionRecommended builds a single-question, two-option args
// map whose FIRST option carries `recommended: recVal` — the exact shape
// the operator's model emitted (misplaced on the option, not the question).
func askArgsWithOptionRecommended(recVal any) map[string]any {
	return map[string]any{
		"questions": []any{
			map[string]any{
				"header":   "Scope",
				"question": "Which scope should this cover?",
				"options": []any{
					map[string]any{"label": "Backend only", "recommended": recVal},
					map[string]any{"label": "Full stack", "description": "SPA + backend"},
				},
			},
		},
	}
}

// firstAskQuestion navigates NormalizeArgs' returned args down to
// questions[0], asserting the shape holds along the way.
func firstAskQuestion(t *testing.T, args map[string]any) map[string]any {
	t.Helper()
	qs, ok := args["questions"].([]any)
	if !ok || len(qs) == 0 {
		t.Fatalf("questions missing or empty after NormalizeArgs: %#v", args["questions"])
	}
	q, ok := qs[0].(map[string]any)
	if !ok {
		t.Fatalf("questions[0] is not an object: %#v", qs[0])
	}
	return q
}

func askQuestionOption(t *testing.T, q map[string]any, idx int) map[string]any {
	t.Helper()
	opts, ok := q["options"].([]any)
	if !ok || idx >= len(opts) {
		t.Fatalf("options missing or too short: %#v", q["options"])
	}
	o, ok := opts[idx].(map[string]any)
	if !ok {
		t.Fatalf("options[%d] is not an object: %#v", idx, opts[idx])
	}
	return o
}

// TestAskUserQuestionNormalizeArgs_LiftsOptionRecommended covers every
// truthy near-miss form the fix wave enumerates: bool true, and the strings
// "true"/"yes"/<the option's own label>.
func TestAskUserQuestionNormalizeArgs_LiftsOptionRecommended(t *testing.T) {
	cases := []struct {
		name   string
		recVal any
	}{
		{"bool_true", true},
		{"string_true", "true"},
		{"string_yes", "yes"},
		{"string_yes_uppercase", "YES"},
		{"string_matches_own_label", "Backend only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := NewAskUserQuestionTool(nil)
			args := askArgsWithOptionRecommended(tc.recVal)

			out := tool.NormalizeArgs(args)

			q := firstAskQuestion(t, out)
			if got, _ := q["recommended"].(string); got != "Backend only" {
				t.Fatalf("question recommended = %v, want lifted label %q", q["recommended"], "Backend only")
			}
			opt0 := askQuestionOption(t, q, 0)
			if _, present := opt0["recommended"]; present {
				t.Fatal("option-level recommended must be dropped after being lifted")
			}
		})
	}
}

// TestAskUserQuestionNormalizeArgs_FalsyOptionRecommendedDropped covers the
// falsy forms (bool false, "", "no") plus an unrecognized string — none are
// lifted; all are silently dropped from the option, with no error.
func TestAskUserQuestionNormalizeArgs_FalsyOptionRecommendedDropped(t *testing.T) {
	cases := []struct {
		name   string
		recVal any
	}{
		{"bool_false", false},
		{"empty_string", ""},
		{"string_no", "no"},
		{"string_no_uppercase", "NO"},
		{"unrecognized_string", "maybe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recVal := tc.recVal
			tool := NewAskUserQuestionTool(nil)
			args := askArgsWithOptionRecommended(recVal)

			out := tool.NormalizeArgs(args)

			q := firstAskQuestion(t, out)
			if got, ok := q["recommended"]; ok && got != "" {
				t.Fatalf("recommended must not be lifted from falsy value %v; got %v", recVal, got)
			}
			opt0 := askQuestionOption(t, q, 0)
			if _, present := opt0["recommended"]; present {
				t.Fatalf("option-level recommended must still be dropped for falsy value %v", recVal)
			}
		})
	}
}

// TestAskUserQuestionNormalizeArgs_QuestionLevelRecommendedWins: a question
// that already carries its OWN `recommended` keeps it — the option-level
// marker is dropped silently, never overriding the question's explicit
// choice.
func TestAskUserQuestionNormalizeArgs_QuestionLevelRecommendedWins(t *testing.T) {
	tool := NewAskUserQuestionTool(nil)
	args := map[string]any{
		"questions": []any{
			map[string]any{
				"header":      "Scope",
				"question":    "Which scope?",
				"recommended": "Full stack",
				"options": []any{
					map[string]any{"label": "Backend only", "recommended": true},
					map[string]any{"label": "Full stack"},
				},
			},
		},
	}

	out := tool.NormalizeArgs(args)

	q := firstAskQuestion(t, out)
	if got, _ := q["recommended"].(string); got != "Full stack" {
		t.Fatalf("question-level recommended must win, got %q", got)
	}
	opt0 := askQuestionOption(t, q, 0)
	if _, present := opt0["recommended"]; present {
		t.Fatal("option-level recommended must still be dropped even when the question already has one")
	}
}

// TestAskUserQuestionNormalizeArgs_MultipleOptionsFirstTruthyWins: when more
// than one option in the same question carries a truthy marker, the FIRST
// is lifted and every later one is dropped (deterministic).
func TestAskUserQuestionNormalizeArgs_MultipleOptionsFirstTruthyWins(t *testing.T) {
	tool := NewAskUserQuestionTool(nil)
	args := map[string]any{
		"questions": []any{
			map[string]any{
				"header":   "Scope",
				"question": "Which scope?",
				"options": []any{
					map[string]any{"label": "Backend only", "recommended": true},
					map[string]any{"label": "Full stack", "recommended": true},
				},
			},
		},
	}

	out := tool.NormalizeArgs(args)

	q := firstAskQuestion(t, out)
	if got, _ := q["recommended"].(string); got != "Backend only" {
		t.Fatalf("the FIRST truthy option must win, got %q", got)
	}
	for i := 0; i < 2; i++ {
		o := askQuestionOption(t, q, i)
		if _, present := o["recommended"]; present {
			t.Fatalf("options[%d].recommended must be dropped", i)
		}
	}
}

// TestAskUserQuestion_RegistryAcceptsMisplacedOptionRecommended is the
// end-to-end reproduction of the operator's exact failure, through the real
// dispatch path (ToolRegistry.ExecuteWithContext): NormalizeArgs runs
// before validateToolArgs, so the call that used to be hard-rejected now
// succeeds and parks the turn with the recommendation correctly attached to
// the question.
func TestAskUserQuestion_RegistryAcceptsMisplacedOptionRecommended(t *testing.T) {
	reg := &fakeAskRegistry{}
	r := NewToolRegistry()
	r.Register(NewAskUserQuestionTool(func() AskUserQuestionRegistry { return reg }))

	args := askArgsWithOptionRecommended(true)
	res := r.ExecuteWithContext(webCtx(), AskUserQuestionToolName, args, "webchat", "chat-1", nil)

	if res.IsError {
		t.Fatalf("a misplaced option-level recommended must be auto-corrected, not rejected: %s", res.ForLLM)
	}
	if !res.ParksTurn {
		t.Fatal("ParksTurn must be set on the success path")
	}
	if len(reg.created) != 1 {
		t.Fatalf("expected exactly one pending set created, got %d", len(reg.created))
	}
	if len(reg.created[0].Questions) == 0 {
		t.Fatal("pending set has no questions")
	}
	if got := reg.created[0].Questions[0].Recommended; got != "Backend only" {
		t.Fatalf("persisted question.Recommended = %q, want %q", got, "Backend only")
	}
}

// TestAskUserQuestion_RegistryStillRejectsUnrelatedUnexpectedProperty proves
// the leniency is narrowly scoped to the literal `recommended` key: any
// OTHER unexpected property on an option is rejected exactly as before,
// through the same real dispatch path.
func TestAskUserQuestion_RegistryStillRejectsUnrelatedUnexpectedProperty(t *testing.T) {
	reg := &fakeAskRegistry{}
	r := NewToolRegistry()
	r.Register(NewAskUserQuestionTool(func() AskUserQuestionRegistry { return reg }))

	args := map[string]any{
		"questions": []any{
			map[string]any{
				"header":   "Scope",
				"question": "Which scope?",
				"options": []any{
					map[string]any{"label": "Backend only", "not_a_real_field": "x"},
					map[string]any{"label": "Full stack"},
				},
			},
		},
	}
	res := r.ExecuteWithContext(webCtx(), AskUserQuestionToolName, args, "webchat", "chat-1", nil)

	if !res.IsError {
		t.Fatal("an unrelated unexpected property must still be rejected")
	}
	if !strings.Contains(res.ForLLM, `unexpected property "not_a_real_field"`) {
		t.Fatalf("want an unexpected-property error naming the field, got %q", res.ForLLM)
	}
	if len(reg.created) != 0 {
		t.Fatal("registry must not be touched when validation fails")
	}
}
