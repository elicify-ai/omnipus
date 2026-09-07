// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_forcing_test.go covers ADR-081 D3 (two-door forced first move) and D4
// (rubric relocation) at both the evaluateGoalForcing unit level and, for
// the request-shape-level guarantees a unit test cannot observe (what
// runTurn actually SENDS to the provider), a full runTurn drive — the wave-2
// (W2a) regression suite. Traces to:
// docs/internal/architecture/ADR-081-work-first-goal-flow.md (D3/D4/D7),
// docs/internal/specs/work-first-goal-flow-spec.md tests 8/9, C-3/C-4.
package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// noopResumeDispatcher discards the AskUserQuestion resume dispatch — this
// test never answers the card, it only proves the ASK (park) side spends
// the FR-010 budget.
type noopResumeDispatcher struct{}

func (noopResumeDispatcher) DispatchResume(*askuser.PendingSet, string) error { return nil }

// newTestAskUserRegistry wires the REAL askuser.Registry over store, so
// AskUserQuestion's Execute runs its genuine CreatePending success path
// (durable persistence, ParksTurn=true) rather than a hand-rolled fake.
func newTestAskUserRegistry(t *testing.T, store *session.UnifiedStore) *askuser.Registry {
	t.Helper()
	return askuser.NewRegistry(store, noopResumeDispatcher{}, askuser.Options{})
}

// allowGoalToolsPolicy grants the calling agent explicit "allow" on set_goal
// and AskUserQuestion — needed since these minimal test configs carry no
// policy entries at all, which resolves to "deny" fail-closed (CLAUDE.md
// hard constraint 6: no default-policy fallback). Mirrors allowBashPolicy
// (judge_test.go).
func allowGoalToolsPolicy(agentInst *AgentInstance) {
	agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{
			tools.SetGoalToolName:         config.ToolPolicyAllow,
			tools.AskUserQuestionToolName: config.ToolPolicyAllow,
		},
	})
}

// setActiveGoalRecordless writes an active goal with an EMPTY compiled
// record directly onto sid — the D3 base predicate's legal transient state
// (ADR-081 D1), without going through applyGoalCommandPrompt (W1b's own
// lane) at all.
func setActiveGoalRecordless(t *testing.T, store *session.UnifiedStore, sid, goalID, condition string) {
	t.Helper()
	empty := ""
	if err := store.SetMeta(sid, session.MetaPatch{
		GoalID: &goalID, GoalCondition: &condition, GoalCriteriaJSON: &empty,
	}); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
}

// --- test 8: TestGoalTurn_ForcingPredicateAndNarrowing ---------------------

func TestGoalTurn_ForcingPredicateAndNarrowing(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	allowGoalToolsPolicy(agentInst)
	store, sid := newGoalTestSession(t, al, agentInst.ID)

	newTS := func(channel string, senderID string, userInitiated bool) *turnState {
		return &turnState{
			agent:   agentInst,
			channel: channel,
			opts: processOptions{
				TranscriptStore: store, TranscriptSessionID: sid,
				Channel: channel, SenderID: senderID, UserInitiated: userInitiated,
			},
		}
	}

	t.Run("no_active_goal_predicate_false", func(t *testing.T) {
		store2, sid2 := newGoalTestSession(t, al, agentInst.ID)
		ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
			TranscriptStore: store2, TranscriptSessionID: sid2, Channel: "webchat",
		}}
		d := al.evaluateGoalForcing(ts, 1, &mockProvider{}, agentInst.Tools.GetAll())
		if d.rubric || d.layer1 {
			t.Fatalf("no active goal must never force or inject the rubric, got %+v", d)
		}
	})

	t.Run("active_goal_record_already_populated_predicate_false", func(t *testing.T) {
		store3, sid3 := newGoalTestSession(t, al, agentInst.ID)
		compiled := `{"intent":"x","prompt":"x","criteria":[{"id":"c1","kind":"prose","judgment":"boolean","text":"t"}]}`
		cond := "build a game"
		if err := store3.SetMeta(sid3, session.MetaPatch{GoalCondition: &cond, GoalCriteriaJSON: &compiled}); err != nil {
			t.Fatal(err)
		}
		ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
			TranscriptStore: store3, TranscriptSessionID: sid3, Channel: "webchat",
		}}
		d := al.evaluateGoalForcing(ts, 1, &mockProvider{}, agentInst.Tools.GetAll())
		if d.rubric || d.layer1 {
			t.Fatalf("a goal with a populated record must never force or inject the rubric, got %+v", d)
		}
	})

	t.Run("webchat_predicate_holds_forces_exact_pair", func(t *testing.T) {
		setActiveGoalRecordless(t, store, sid, "goal-1", "build a tetris game")
		ts := newTS("webchat", "", true)
		d := al.evaluateGoalForcing(ts, 1, &mockProvider{}, agentInst.Tools.GetAll())
		if !d.rubric || !d.layer1 {
			t.Fatalf("webchat + predicate-holds must force, got %+v", d)
		}
		if len(d.narrowed) != 2 {
			t.Fatalf("narrowed = %d tools, want exactly 2 (set_goal, AskUserQuestion), got %+v", len(d.narrowed), d.narrowed)
		}
		names := map[string]bool{}
		for _, tl := range d.narrowed {
			names[tl.Name()] = true
		}
		if !names[tools.SetGoalToolName] || !names[tools.AskUserQuestionToolName] {
			t.Fatalf("narrowed set must be exactly {set_goal, AskUserQuestion}, got %+v", names)
		}
		if !d.askOffered {
			t.Fatal("askOffered must be true when the budget is unspent")
		}
	})

	t.Run("negative_UserInitiated_false_auto_submit_resume_still_forces", func(t *testing.T) {
		// grill M1: the predicate MUST NOT consult opts.UserInitiated or
		// sender identity — an auto-submitted card resume is still a goal
		// turn.
		ts := newTS("webchat", "", false)
		d := al.evaluateGoalForcing(ts, 1, &mockProvider{}, agentInst.Tools.GetAll())
		if !d.layer1 {
			t.Fatalf("UserInitiated=false (auto-submit resume) must still force, got %+v", d)
		}
	})

	t.Run("negative_nudge_turn_sender_still_forces", func(t *testing.T) {
		// A keeper nudge turn (Sender.CanonicalID == goalLoopFollowUpSenderID,
		// UserInitiated=false) is a goal turn exactly like a fresh activation.
		ts := newTS("webchat", goalLoopFollowUpSenderID, false)
		d := al.evaluateGoalForcing(ts, 1, &mockProvider{}, agentInst.Tools.GetAll())
		if !d.layer1 {
			t.Fatalf("a keeper nudge turn must still force, got %+v", d)
		}
	})

	t.Run("policy_denied_no_forcing", func(t *testing.T) {
		store4, sid4 := newGoalTestSession(t, al, agentInst.ID)
		setActiveGoalRecordless(t, store4, sid4, "goal-4", "a denied goal")
		agentDenied, _ := al.GetRegistry().GetAgent("native-agent")
		agentDenied.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{tools.SetGoalToolName: config.ToolPolicyDeny},
		})
		defer allowGoalToolsPolicy(agentDenied) // restore for subsequent subtests
		policyFiltered, _ := tools.FilterToolsByPolicy(agentDenied.Tools.GetAll(), agentDenied.AgentType, agentDenied.LoadToolPolicy())
		ts := &turnState{agent: agentDenied, channel: "webchat", opts: processOptions{
			TranscriptStore: store4, TranscriptSessionID: sid4, Channel: "webchat",
		}}
		d := al.evaluateGoalForcing(ts, 1, &mockProvider{}, policyFiltered)
		if d.layer1 {
			t.Fatal("set_goal policy-denied must never force")
		}
		if !d.rubric {
			t.Fatal("the base predicate (and therefore the rubric) still holds even when forcing is skipped")
		}
	})

	t.Run("cli_provider_no_forcing", func(t *testing.T) {
		store5, sid5 := newGoalTestSession(t, al, agentInst.ID)
		setActiveGoalRecordless(t, store5, sid5, "goal-5", "a cli-bridged goal")
		ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
			TranscriptStore: store5, TranscriptSessionID: sid5, Channel: "webchat",
		}}
		// NewCodexCliProvider only sets fields at construction — safe to
		// construct in a unit test without ever calling Chat() (which is
		// what would exec the subprocess).
		cli := providers.NewCodexCliProvider(t.TempDir())
		d := al.evaluateGoalForcing(ts, 1, cli, agentInst.Tools.GetAll())
		if d.layer1 {
			t.Fatal("a CLI-bridged provider must never force (FR-009)")
		}
		if !d.rubric {
			t.Fatal("the rubric still applies on a CLI-bridged provider turn")
		}
	})

	t.Run("non_web_channel_no_forcing_rubric_still_holds", func(t *testing.T) {
		store6, sid6 := newGoalTestSession(t, al, agentInst.ID)
		setActiveGoalRecordless(t, store6, sid6, "goal-6", "a telegram goal")
		ts := &turnState{agent: agentInst, channel: "telegram", opts: processOptions{
			TranscriptStore: store6, TranscriptSessionID: sid6, Channel: "telegram",
		}}
		d := al.evaluateGoalForcing(ts, 1, &mockProvider{}, agentInst.Tools.GetAll())
		if d.layer1 {
			t.Fatal("AskUserQuestion is web-only — a channel origin must never force (G-B2)")
		}
		if !d.rubric || d.isWebchat {
			t.Fatalf("rubric must still hold on a channel origin (isWebchat=false), got %+v", d)
		}
	})

	t.Run("budget_spent_only_set_goal_offered", func(t *testing.T) {
		store7, sid7 := newGoalTestSession(t, al, agentInst.ID)
		setActiveGoalRecordless(t, store7, sid7, "goal-7", "a spent-budget goal")
		spent := 1
		if err := store7.SetMeta(sid7, session.MetaPatch{GoalQuestionRoundsUsed: &spent}); err != nil {
			t.Fatal(err)
		}
		ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
			TranscriptStore: store7, TranscriptSessionID: sid7, Channel: "webchat",
		}}
		d := al.evaluateGoalForcing(ts, 1, &mockProvider{}, agentInst.Tools.GetAll())
		if !d.layer1 {
			t.Fatal("set_goal alone must still force")
		}
		if len(d.narrowed) != 1 || d.narrowed[0].Name() != tools.SetGoalToolName {
			t.Fatalf("budget spent: narrowed must be {set_goal} ONLY, got %+v", d.narrowed)
		}
		if d.askOffered {
			t.Fatal("askOffered must be false once the question budget is spent")
		}
	})

	t.Run("iteration_not_1_never_forces_or_injects", func(t *testing.T) {
		store8, sid8 := newGoalTestSession(t, al, agentInst.ID)
		setActiveGoalRecordless(t, store8, sid8, "goal-8", "an iteration-2 goal")
		ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
			TranscriptStore: store8, TranscriptSessionID: sid8, Channel: "webchat",
		}}
		d := al.evaluateGoalForcing(ts, 2, &mockProvider{}, agentInst.Tools.GetAll())
		if d.layer1 || d.rubric {
			t.Fatalf("iteration != 1 must never force or inject the rubric (D3 [G-M7]), got %+v", d)
		}
	})
}

// --- test 9: TestGoalRubricInjection_OncePerGoalTurn -----------------------

func TestGoalRubricInjection_OncePerGoalTurn(t *testing.T) {
	t.Run("no_note_when_predicate_false", func(t *testing.T) {
		if note := buildGoalRubricInjectionNote(false, true); note != "" {
			t.Fatalf("holds=false must produce no note, got %d bytes", len(note))
		}
	})

	t.Run("webchat_note_has_no_channel_addendum", func(t *testing.T) {
		note := buildGoalRubricInjectionNote(true, true)
		if note == "" {
			t.Fatal("holds=true must produce a non-empty note")
		}
		if strings.Contains(note, goalRubricChannelAddendum) {
			t.Fatal("the webchat variant must not carry the channel-only addendum")
		}
	})

	t.Run("channel_note_has_addendum_and_no_double_inject", func(t *testing.T) {
		note := buildGoalRubricInjectionNote(true, false)
		if !strings.Contains(note, "AskUserQuestion is unavailable") {
			t.Fatal("the channel variant must tell the agent to ask conversationally")
		}
		// FR-011: the note must NEVER carry the session-window feed or the
		// workspace/project instructions heading — loop.go already injects
		// those per-turn; double-inject is the bug this extraction fixes.
		for _, forbidden := range []string{
			"AUTHORITATIVE workspace/project instructions",
			"BACKGROUND CONTEXT ONLY",
			"Recent conversation",
		} {
			if strings.Contains(note, forbidden) {
				t.Fatalf("the rubric note must not carry %q (double-inject risk)", forbidden)
			}
		}
	})

	t.Run("inject_at_index_1_noop_on_empty_note", func(t *testing.T) {
		msgs := []providers.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hi"},
		}
		out := injectGoalRubricNote(msgs, "")
		if len(out) != 2 {
			t.Fatalf("an empty note must be a no-op, got %d messages", len(out))
		}
		out = injectGoalRubricNote(msgs, "rubric text")
		if len(out) != 3 || out[1].Role != "system" || out[1].Content != "rubric text" || out[2].Content != "hi" {
			t.Fatalf("the note must insert as a system message at index 1, got %+v", out)
		}
		// The ORIGINAL slice must be untouched (a fresh slice is returned).
		if len(msgs) != 2 {
			t.Fatal("injectGoalRubricNote must not mutate its input slice")
		}
	})

	t.Run("registered_in_midturn_budget", func(t *testing.T) {
		al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")
		store, sid := newGoalTestSession(t, al, agentInst.ID)
		ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
			TranscriptStore: store, TranscriptSessionID: sid, Channel: "webchat",
		}}
		before := al.ephemeralSystemNoteTokens(ts)
		setActiveGoalRecordless(t, store, sid, "goal-budget", "a budget-tracked goal")
		after := al.ephemeralSystemNoteTokens(ts)
		if after <= before {
			t.Fatalf("ephemeralSystemNoteTokens must grow once the goal rubric note applies: before=%d after=%d", before, after)
		}
	})
}

// --- forcedDoorCaptureProvider: full-turn scripted provider ---------------

// forcedDoorCaptureProvider scripts a goal-forced turn end to end: call 1
// returns a tool call (configurable), call 2 (and beyond) captures the
// offered tools/options and returns a plain terminal text response. Mirrors
// steering_test.go's gracefulCaptureProvider precedent.
type forcedDoorCaptureProvider struct {
	mu sync.Mutex

	calls int
	// firstCallToolCalls is what call 1 returns.
	firstCallToolCalls []providers.ToolCall
	// captured[n] is call n+1's (tools, options) — call 1 in captured[0],
	// call 2 in captured[1], etc.
	captured []capturedRequest
	finalMsg string
}

type capturedRequest struct {
	tools   []providers.ToolDefinition
	options map[string]any
}

func (p *forcedDoorCaptureProvider) Chat(
	_ context.Context, _ []providers.Message, toolDefs []providers.ToolDefinition, _ string, options map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.captured = append(p.captured, capturedRequest{tools: toolDefs, options: options})
	if p.calls == 1 && len(p.firstCallToolCalls) > 0 {
		return &providers.LLMResponse{ToolCalls: p.firstCallToolCalls}, nil
	}
	return &providers.LLMResponse{Content: p.finalMsg}, nil
}

func (p *forcedDoorCaptureProvider) GetDefaultModel() string { return "forced-door-capture-mock" }

// SupportsNativeSearch implements the same unexported-interface probe
// pkg/agent/loop.go's useNativeSearch check uses (interface{
// SupportsNativeSearch() bool }). Unconditionally true — it only takes
// effect when a test ALSO sets cfg.Tools.Web.PreferNative, which no
// existing caller of this provider does, so this is inert for every
// pre-existing test and opt-in for review-round-1 finding #6's own test.
func (p *forcedDoorCaptureProvider) SupportsNativeSearch() bool { return true }

func (p *forcedDoorCaptureProvider) requestAt(n int) (capturedRequest, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 0 || n >= len(p.captured) {
		return capturedRequest{}, false
	}
	return p.captured[n], true
}

func toolNamesOf(defs []providers.ToolDefinition) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Function.Name)
	}
	return out
}

// TestGoalTurn_EndToEnd_ForcedDoorAndRestoration drives a REAL runTurn (spec
// C-3): the first request offers EXACTLY the narrowed pair with tool-choice
// required; a successful set_goal call registers the record; the SECOND
// request restores the full policy-filtered surface with no tool-choice
// option at all.
func TestGoalTurn_EndToEnd_ForcedDoorAndRestoration(t *testing.T) {
	provider := &forcedDoorCaptureProvider{
		firstCallToolCalls: []providers.ToolCall{{
			ID: "call_1", Type: "function", Name: tools.SetGoalToolName,
			Function: &providers.FunctionCall{
				Name: tools.SetGoalToolName,
				Arguments: `{"definition":"Build a tetris clone","criteria":[` +
					`{"text":"the game renders and accepts input","judgment":"boolean"}],` +
					`"assessment":{"clarity":"clear"}}`,
			},
		}},
		finalMsg: "working on it now",
	}
	al, _ := newGoalLoopTestLoop(t, provider, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	// Grant a few extra tools beyond the goal pair — CLAUDE.md hard
	// constraint 6 fails everything else closed to deny by default, so
	// without this the request-2 "full surface" assertion below could not
	// distinguish "genuinely restored" from "still just the narrowed pair,
	// coincidentally".
	agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{
			tools.SetGoalToolName:         config.ToolPolicyAllow,
			tools.AskUserQuestionToolName: config.ToolPolicyAllow,
			"read_file":                   config.ToolPolicyAllow,
			"write_file":                  config.ToolPolicyAllow,
			"search_web":                  config.ToolPolicyAllow,
		},
	})
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-e2e-1", "build me a tetris game")

	opts := processOptions{
		SessionKey: "goal-e2e-session", Channel: "webchat", ChatID: "c1",
		UserMessage:     "build me a tetris game",
		TranscriptStore: store, TranscriptSessionID: sid,
		DefaultResponse: "done", UserInitiated: true,
	}
	ts := newTurnState(agentInst, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))

	if _, err := al.runTurn(context.Background(), ts); err != nil {
		t.Fatalf("runTurn: %v", err)
	}

	first, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider was never called")
	}
	firstNames := toolNamesOf(first.tools)
	if len(firstNames) != 2 {
		t.Fatalf("request 1 must offer EXACTLY 2 tools, got %v", firstNames)
	}
	wantFirst := map[string]bool{tools.SetGoalToolName: true, tools.AskUserQuestionToolName: true}
	for _, n := range firstNames {
		if !wantFirst[n] {
			t.Fatalf("request 1 offered an unexpected tool %q, want only {set_goal, AskUserQuestion}, got %v", n, firstNames)
		}
	}
	tc, ok := first.options[providers.OptionKeyToolChoice].(providers.ToolChoice)
	if !ok || tc.Mode != providers.ToolChoiceRequired {
		t.Fatalf("request 1 must carry tool_choice=required, got %#v", first.options[providers.OptionKeyToolChoice])
	}

	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if meta.GoalCriteriaJSON == "" {
		t.Fatal("set_goal's write must have landed on the session meta")
	}
	compiled := loadCompiledGoal(meta.GoalCriteriaJSON)
	if compiled == nil || compiled.Definition != "Build a tetris clone" {
		t.Fatalf("the registered record must carry the agent's definition, got %+v", compiled)
	}

	second, ok := provider.requestAt(1)
	if !ok {
		t.Fatal("expected a second LLM request (the record now exists — the predicate is false)")
	}
	if len(second.tools) <= 2 {
		t.Fatalf("request 2 must restore the FULL policy-filtered surface (> 2 tools), got %d: %v",
			len(second.tools), toolNamesOf(second.tools))
	}
	if _, present := second.options[providers.OptionKeyToolChoice]; present {
		t.Fatal("request 2 must NOT carry a tool-choice option — forcing applies to the first request only")
	}
}

// TestGoalTurn_ForcedRequest_SuppressesNativeSearch is review-round-1
// finding #6: llmOpts["native_search"] used to be set independently of
// Layer-1 forcing, silently adding a THIRD callable "tool" (the provider's
// own built-in search capability) that satisfies tool_choice=required
// without the model ever touching set_goal or AskUserQuestion — defeating
// the "exactly two" narrowed pair Layer 1 promises (D3 [G-m11]). The FORCED
// first request must never carry native_search, even when the provider
// supports it and cfg.Tools.Web.PreferNative is on; the SECOND request
// (forcing lifted, full surface restored) is free to carry it again.
func TestGoalTurn_ForcedRequest_SuppressesNativeSearch(t *testing.T) {
	provider := &forcedDoorCaptureProvider{
		firstCallToolCalls: []providers.ToolCall{{
			ID: "call_1", Type: "function", Name: tools.SetGoalToolName,
			Function: &providers.FunctionCall{
				Name: tools.SetGoalToolName,
				Arguments: `{"definition":"Build a tetris clone","criteria":[` +
					`{"text":"the game renders and accepts input","judgment":"boolean"}],` +
					`"assessment":{"clarity":"clear"}}`,
			},
		}},
		finalMsg: "working on it now",
	}
	al, _ := newGoalLoopTestLoop(t, provider, func(cfg *config.Config) {
		cfg.Tools.Web.PreferNative = true
	})
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{
			tools.SetGoalToolName:         config.ToolPolicyAllow,
			tools.AskUserQuestionToolName: config.ToolPolicyAllow,
			"search_web":                  config.ToolPolicyAllow,
		},
	})
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-native-search-1", "build me a tetris game")

	opts := processOptions{
		SessionKey: "goal-native-search-session", Channel: "webchat", ChatID: "c1",
		UserMessage:     "build me a tetris game",
		TranscriptStore: store, TranscriptSessionID: sid,
		DefaultResponse: "done", UserInitiated: true,
	}
	ts := newTurnState(agentInst, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))

	if _, err := al.runTurn(context.Background(), ts); err != nil {
		t.Fatalf("runTurn: %v", err)
	}

	first, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider was never called")
	}
	tc, ok := first.options[providers.OptionKeyToolChoice].(providers.ToolChoice)
	if !ok || tc.Mode != providers.ToolChoiceRequired {
		t.Fatalf("request 1 must carry tool_choice=required (forcing must genuinely be active for this assertion to mean anything), got %#v",
			first.options[providers.OptionKeyToolChoice])
	}
	if _, present := first.options["native_search"]; present {
		t.Fatal("request 1 (forced) must NOT carry native_search — it would add a third callable " +
			"tool that satisfies tool_choice=required without going through set_goal/AskUserQuestion")
	}

	second, ok := provider.requestAt(1)
	if !ok {
		t.Fatal("expected a second LLM request (the record now exists — the predicate is false)")
	}
	if _, present := second.options[providers.OptionKeyToolChoice]; present {
		t.Fatal("request 2 must NOT carry a tool-choice option — forcing applies to the first request only")
	}
	if _, present := second.options["native_search"]; !present {
		t.Fatal("request 2 (forcing lifted) must carry native_search again — the suppression is scoped to the forced request only")
	}
}

// TestGoalTurn_QuestionDoorBudgetIncrement proves the ask door taken on a
// forced goal turn bumps GoalQuestionRoundsUsed exactly once (FR-010), and
// that a SECOND goal turn on the SAME session — the question budget now
// spent — narrows to {set_goal} alone.
func TestGoalTurn_QuestionDoorBudgetIncrement(t *testing.T) {
	provider := &forcedDoorCaptureProvider{
		firstCallToolCalls: []providers.ToolCall{{
			ID: "call_1", Type: "function", Name: tools.AskUserQuestionToolName,
			Function: &providers.FunctionCall{
				Name: tools.AskUserQuestionToolName,
				Arguments: `{"questions":[{"header":"Scope","question":"Single or multiplayer?",` +
					`"options":[{"label":"Single","description":"one player"},{"label":"Multi","description":"two players"}]}]}`,
			},
		}},
		finalMsg: "unused — the turn parks",
	}
	al, _ := newGoalLoopTestLoop(t, provider, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	allowGoalToolsPolicy(agentInst)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-ask-1", "organize my week")

	// AskUserQuestion needs a live pending-question registry wired, or it
	// fails closed with "no registry" and never reaches ParksTurn — mirror
	// the AskUserQuestionTool's own registry wiring precedent so this test
	// exercises the GENUINE success path.
	reg := newTestAskUserRegistry(t, store)
	al.SetAskUserRegistry(reg)

	opts := processOptions{
		SessionKey: "goal-ask-session", Channel: "webchat", ChatID: "c1",
		UserMessage:     "organize my week",
		TranscriptStore: store, TranscriptSessionID: sid,
		DefaultResponse: "done", UserInitiated: true,
	}
	ts := newTurnState(agentInst, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))

	result, err := al.runTurn(context.Background(), ts)
	if err != nil {
		t.Fatalf("runTurn: %v", err)
	}
	if result.status != TurnEndStatusParked {
		t.Fatalf("a successful AskUserQuestion call must park the turn, got status=%v", result.status)
	}

	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if meta.GoalQuestionRoundsUsed != 1 {
		t.Fatalf("GoalQuestionRoundsUsed = %d, want 1 after the ask door was taken on a forced goal turn", meta.GoalQuestionRoundsUsed)
	}

	// A fresh evaluation on the SAME (now budget-spent) session must narrow
	// to {set_goal} alone.
	ts2 := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
		TranscriptStore: store, TranscriptSessionID: sid, Channel: "webchat",
	}}
	d := al.evaluateGoalForcing(ts2, 1, provider, agentInst.Tools.GetAll())
	if !d.layer1 || len(d.narrowed) != 1 || d.narrowed[0].Name() != tools.SetGoalToolName {
		t.Fatalf("after the question door is spent, forcing must narrow to {set_goal} alone, got %+v", d)
	}
}
