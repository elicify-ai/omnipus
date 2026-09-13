// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_first_move_test.go covers ADR-081 D3 (two-door narrowed first move,
// as amended 2026-09-07 — see the D3 AMENDMENT block in the ADR) and D4
// (rubric relocation + the tool-calling first-move instruction) at both the
// evaluateGoalForcing unit level and, for the request-shape-level
// guarantees a unit test cannot observe (what runTurn actually SENDS to the
// provider), a full runTurn drive — the wave-2 (W2a) regression suite,
// updated for the D3 amendment. Traces to:
// docs/internal/architecture/ADR-081-work-first-goal-flow.md (D3
// AMENDMENT/D4/D7), docs/internal/specs/work-first-goal-flow-spec.md tests
// 8/9, C-3/C-4.
//
// Renamed from goal_forcing_test.go: provider tool-choice forcing is
// deleted (Z.AI 400 evidence — a forced tool choice broke the operator's
// primary provider family). What survives is tool-surface NARROWING (this
// file's core coverage) plus the immediate post-turn correction that now
// carries the determinism guarantee (pkg/agent/goal_loop_test.go's
// TestGoalLoop_PostTurnCorrection_* tests, since that correction lives in
// checkGoalLoopAfterTurn, not here).
package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
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
// hard constraint 6: no default-policy fallback).
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
func setActiveGoalRecordless(t *testing.T, _ *session.UnifiedStore, sid, goalID, condition string) {
	t.Helper()
	// ADR-086 (wave S6): the session-meta half of this fixture
	// (GoalID/GoalCondition/GoalCriteriaJSON) is GONE — the goal is its own
	// entity, and the REAL, active pkg/goal record built below is now the
	// whole fixture rather than a pairing alongside session meta
	// (GOAL-FR-009/FR-010, wave E12). A real set_goal call's WriteRecord
	// path (goal_record_wiring.go, wave E4) resolves the goal via
	// GetActiveByOwner, so this record is what every reader finds. Criteria
	// stays empty to mirror this function's own "recordless" contract (D3's
	// legal transient state, this function's own name); dod is the same
	// built-in floor a real activation always gets.
	gstore := goal.NewStore(config.OmnipusHomeDir())
	if existing, gerr := gstore.GetActiveByOwner(generated.GoalOwnerKindSession, sid); gerr == nil && existing != nil {
		return // already paired — a caller that sets this fixture twice for one session
	}
	g, nerr := goal.New(generated.GoalOwnerKindSession, sid, generated.ChatCompiled,
		condition, "", nil, newFloorDoD(), config.DefaultGoalMaxRounds, time.Now().UTC())
	if nerr != nil {
		t.Fatalf("setActiveGoalRecordless: goal.New: %v", nerr)
	}
	g.GoalID = goalID
	if cerr := gstore.Create(g); cerr != nil {
		t.Fatalf("setActiveGoalRecordless: Create: %v", cerr)
	}
	if _, aerr := gstore.Update(g.GoalID, func(cur *goal.Goal) error {
		return cur.Activate(sid, time.Now().UTC())
	}); aerr != nil {
		t.Fatalf("setActiveGoalRecordless: Activate: %v", aerr)
	}
}

// mustActiveGoalRecord fetches sid's active pkg/goal record. wave R7C
// (joint delivery plan §3): the ADR-086 re-point target for assertions that
// used to read a set_goal write off session meta's GoalCriteriaJSON — that
// dual write is retired (DD-6; goal_record_wiring.go::WriteRecord writes
// pkg/goal.Store only), so a test proving "the write landed" must now read
// the record it actually landed on. Shared across this wave's whole
// write-set (goal_first_move_test.go, goal_flow_integration_test.go,
// goal_terminal_transition_test.go, conformance_design_test.go) — same
// package, so one definition suffices.
func mustActiveGoalRecord(t *testing.T, sid string) *goal.Goal {
	t.Helper()
	gstore := goal.NewStore(config.OmnipusHomeDir())
	g, err := gstore.GetActiveByOwner(generated.GoalOwnerKindSession, sid)
	if err != nil {
		t.Fatalf("mustActiveGoalRecord(%q): GetActiveByOwner: %v", sid, err)
	}
	return g
}

// --- test 8: TestGoalTurn_NarrowingPredicateAndSurface ---------------------

func TestGoalTurn_NarrowingPredicateAndSurface(t *testing.T) {
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
		d := al.evaluateGoalForcing(ts, 1, agentInst.Tools.GetAll())
		if d.rubric || d.layer1 {
			t.Fatalf("no active goal must never narrow the surface or inject the rubric, got %+v", d)
		}
	})

	t.Run("active_goal_record_already_populated_predicate_false", func(t *testing.T) {
		store3, sid3 := newGoalTestSession(t, al, agentInst.ID)
		// ADR-086: a POPULATED record is an active goal record with a
		// non-empty criteria ladder, not the retired GoalCriteriaJSON string.
		armGoalRecord(t, sid3, "build a game", recordedGoalCriteria("t"), 0, time.Now())
		ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
			TranscriptStore: store3, TranscriptSessionID: sid3, Channel: "webchat",
		}}
		d := al.evaluateGoalForcing(ts, 1, agentInst.Tools.GetAll())
		if d.rubric || d.layer1 {
			t.Fatalf("a goal with a populated record must never narrow the surface or inject the rubric, got %+v", d)
		}
	})

	t.Run("webchat_predicate_holds_narrows_to_exact_pair", func(t *testing.T) {
		setActiveGoalRecordless(t, store, sid, "goal-1", "build a tetris game")
		ts := newTS("webchat", "", true)
		d := al.evaluateGoalForcing(ts, 1, agentInst.Tools.GetAll())
		if !d.rubric || !d.layer1 {
			t.Fatalf("webchat + predicate-holds must narrow, got %+v", d)
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

	t.Run("negative_UserInitiated_false_auto_submit_resume_still_narrows", func(t *testing.T) {
		// grill M1: the predicate MUST NOT consult opts.UserInitiated or
		// sender identity — an auto-submitted card resume is still a goal
		// turn.
		ts := newTS("webchat", "", false)
		d := al.evaluateGoalForcing(ts, 1, agentInst.Tools.GetAll())
		if !d.layer1 {
			t.Fatalf("UserInitiated=false (auto-submit resume) must still narrow, got %+v", d)
		}
	})

	t.Run("negative_nudge_turn_sender_still_narrows", func(t *testing.T) {
		// A keeper nudge turn (Sender.CanonicalID == goalLoopFollowUpSenderID,
		// UserInitiated=false) is a goal turn exactly like a fresh activation.
		ts := newTS("webchat", goalLoopFollowUpSenderID, false)
		d := al.evaluateGoalForcing(ts, 1, agentInst.Tools.GetAll())
		if !d.layer1 {
			t.Fatalf("a keeper nudge turn must still narrow, got %+v", d)
		}
	})

	t.Run("policy_denied_no_narrowing", func(t *testing.T) {
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
		d := al.evaluateGoalForcing(ts, 1, policyFiltered)
		if d.layer1 {
			t.Fatal("set_goal policy-denied must never narrow")
		}
		if !d.rubric {
			t.Fatal("the base predicate (and therefore the rubric) still holds even when narrowing is skipped")
		}
	})

	t.Run("non_web_channel_narrows_to_set_goal_alone", func(t *testing.T) {
		// D3 AMENDMENT (2026-09-07): narrowing is provider/channel-agnostic
		// now that tool-choice forcing is gone — a channel origin no longer
		// skips narrowing entirely, it just excludes the permanently
		// web-only AskUserQuestion door, degrading the pair to {set_goal}
		// alone (never the empty set).
		store6, sid6 := newGoalTestSession(t, al, agentInst.ID)
		setActiveGoalRecordless(t, store6, sid6, "goal-6", "a telegram goal")
		ts := &turnState{agent: agentInst, channel: "telegram", opts: processOptions{
			TranscriptStore: store6, TranscriptSessionID: sid6, Channel: "telegram",
		}}
		d := al.evaluateGoalForcing(ts, 1, agentInst.Tools.GetAll())
		if !d.layer1 {
			t.Fatal("a channel origin must still narrow (G-B2 gates the ASK door only, not narrowing itself)")
		}
		if len(d.narrowed) != 1 || d.narrowed[0].Name() != tools.SetGoalToolName {
			t.Fatalf("a channel origin must narrow to {set_goal} alone, got %+v", d.narrowed)
		}
		if d.askOffered {
			t.Fatal("AskUserQuestion is web-only — a channel origin must never offer it")
		}
		if !d.rubric || d.isWebchat {
			t.Fatalf("rubric must still hold on a channel origin (isWebchat=false), got %+v", d)
		}
	})

	t.Run("budget_spent_only_set_goal_offered", func(t *testing.T) {
		store7, sid7 := newGoalTestSession(t, al, agentInst.ID)
		setActiveGoalRecordless(t, store7, sid7, "goal-7", "a spent-budget goal")
		// ADR-086 GOAL-FR-004: the question-round budget lives on the goal
		// record, not on session meta.
		if _, uerr := goal.NewStore(config.OmnipusHomeDir()).Update("goal-7", func(cur *goal.Goal) error {
			cur.QuestionRoundsUsed = 1
			return nil
		}); uerr != nil {
			t.Fatal(uerr)
		}
		ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
			TranscriptStore: store7, TranscriptSessionID: sid7, Channel: "webchat",
		}}
		d := al.evaluateGoalForcing(ts, 1, agentInst.Tools.GetAll())
		if !d.layer1 {
			t.Fatal("set_goal alone must still narrow")
		}
		if len(d.narrowed) != 1 || d.narrowed[0].Name() != tools.SetGoalToolName {
			t.Fatalf("budget spent: narrowed must be {set_goal} ONLY, got %+v", d.narrowed)
		}
		if d.askOffered {
			t.Fatal("askOffered must be false once the question budget is spent")
		}
	})

	t.Run("iteration_not_1_still_narrows_while_record_empty", func(t *testing.T) {
		// D3 AMENDMENT (2026-09-08): the old [G-M7] rule — narrow on
		// iteration 1 ONLY — is retired. A narrowed call that FAILS (tool-arg
		// validation error, policy denial at execution, or an error result)
		// neither registers nor parks, so the base predicate is still true on
		// iteration 2, and the door must stay narrowed (see
		// TestGoalForcing_NarrowingPersistsAcrossIterations for the full
		// multi-iteration table covering the real-world defect this fixes).
		store8, sid8 := newGoalTestSession(t, al, agentInst.ID)
		setActiveGoalRecordless(t, store8, sid8, "goal-8", "an iteration-2 goal")
		ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
			TranscriptStore: store8, TranscriptSessionID: sid8, Channel: "webchat",
		}}
		d := al.evaluateGoalForcing(ts, 2, agentInst.Tools.GetAll())
		if !d.layer1 || !d.rubric {
			t.Fatalf("iteration != 1 with the record still empty must still narrow and inject the rubric, got %+v", d)
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

	t.Run("tool_calling_note_instructs_the_first_move_not_raw_json", func(t *testing.T) {
		// D3 amendment / D4: the turn-scoped injection is read by a REAL
		// tool-calling agent — it must instruct set_goal/AskUserQuestion,
		// never the D7 fallback compile's raw-JSON-only contract (that
		// framing would tell a tool-calling agent to answer in prose
		// instead of calling a tool).
		note := buildGoalRubricInjectionNote(true, true)
		if !strings.Contains(note, "Your FIRST action this turn must be a tool call") {
			t.Fatal("the injected note must explicitly instruct a tool call as the first move")
		}
		if !strings.Contains(note, "call set_goal now") {
			t.Fatal("the injected note must name set_goal as the registration door")
		}
		if strings.Contains(note, "Respond with ONLY a JSON object") {
			t.Fatal("the injected note must NOT carry the raw-JSON-only framing — that instruction is for the D7 fallback compile call only, and would contradict tool-calling")
		}
	})

	t.Run("raw_compile_note_still_uses_the_json_only_contract", func(t *testing.T) {
		// The D7 fallback compile (buildGoalCompileMessages) is a genuine
		// no-tools LLM call — it must keep its original raw-JSON contract
		// byte-for-byte; only the turn-scoped injection changed shape.
		note := buildGoalRubricNote(false)
		if !strings.Contains(note, "Respond with ONLY a JSON object") {
			t.Fatal("the D7 fallback compile's rubric text must keep its raw-JSON-only contract")
		}
		if strings.Contains(note, "Your FIRST action this turn must be a tool call") {
			t.Fatal("the D7 fallback compile has no tools available — it must never carry the tool-calling instruction")
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

// --- narrowedDoorCaptureProvider: full-turn scripted provider -------------

// narrowedDoorCaptureProvider scripts a goal turn end to end: call 1 returns
// a tool call (configurable), call 2 (and beyond) captures the offered
// tools/options and returns a plain terminal text response. Mirrors
// steering_test.go's gracefulCaptureProvider precedent.
type narrowedDoorCaptureProvider struct {
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

func (p *narrowedDoorCaptureProvider) Chat(
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

func (p *narrowedDoorCaptureProvider) GetDefaultModel() string { return "narrowed-door-capture-mock" }

// SupportsNativeSearch implements the same unexported-interface probe
// pkg/agent/loop.go's useNativeSearch check uses (interface{
// SupportsNativeSearch() bool }). Unconditionally true — it only takes
// effect when a test ALSO sets cfg.Tools.Web.PreferNative, which no
// existing caller of this provider does, so this is inert for every
// pre-existing test and opt-in for review-round-1 finding #6's own test.
func (p *narrowedDoorCaptureProvider) SupportsNativeSearch() bool { return true }

func (p *narrowedDoorCaptureProvider) requestAt(n int) (capturedRequest, bool) {
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

// TestGoalTurn_EndToEnd_NarrowedDoorAndRestoration drives a REAL runTurn
// (spec C-3): the first request offers EXACTLY the narrowed pair — with NO
// tool-choice option (D3 amendment: forcing is deleted); a successful
// set_goal call registers the record; the SECOND request restores the full
// policy-filtered surface, also with no tool-choice option.
func TestGoalTurn_EndToEnd_NarrowedDoorAndRestoration(t *testing.T) {
	provider := &narrowedDoorCaptureProvider{
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
	if _, present := first.options["tool_choice"]; present {
		t.Fatalf("request 1 must NOT carry a tool-choice option — forcing is deleted (D3 amendment), got %#v", first.options["tool_choice"])
	}

	// wave R7C (Group 1): the dual write to session meta is retired
	// (DD-6) — WriteRecord lands on pkg/goal.Store only, so the proof the
	// write landed reads the goal record, not session meta.
	rec := mustActiveGoalRecord(t, sid)
	if len(rec.Criteria) == 0 {
		t.Fatal("set_goal's write must have landed on the pkg/goal record")
	}
	if rec.Definition != "Build a tetris clone" {
		t.Fatalf("the registered record must carry the agent's definition, got %+v", rec)
	}

	second, ok := provider.requestAt(1)
	if !ok {
		t.Fatal("expected a second LLM request (the record now exists — the predicate is false)")
	}
	if len(second.tools) <= 2 {
		t.Fatalf("request 2 must restore the FULL policy-filtered surface (> 2 tools), got %d: %v",
			len(second.tools), toolNamesOf(second.tools))
	}
	if _, present := second.options["tool_choice"]; present {
		t.Fatal("request 2 must NOT carry a tool-choice option")
	}
}

// TestGoalTurn_NarrowedRequest_SuppressesNativeSearch is review-round-1
// finding #6, kept under the D3 amendment: llmOpts["native_search"] used to
// be set independently of narrowing, which silently adds a THIRD callable
// "tool" (the provider's own built-in search) — defeating the "exactly two"
// narrowed pair's promise, independent of whether anything forces the model
// to touch either tool. The NARROWED first request must never carry
// native_search, even when the provider supports it and
// cfg.Tools.Web.PreferNative is on; the SECOND request (narrowing lifted,
// full surface restored) is free to carry it again.
func TestGoalTurn_NarrowedRequest_SuppressesNativeSearch(t *testing.T) {
	provider := &narrowedDoorCaptureProvider{
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
	if len(first.tools) != 2 {
		t.Fatalf("request 1 must be genuinely narrowed (2 tools) for this assertion to mean anything, got %d: %v",
			len(first.tools), toolNamesOf(first.tools))
	}
	if _, present := first.options["native_search"]; present {
		t.Fatal("request 1 (narrowed) must NOT carry native_search — it would add a third callable " +
			"tool outside {set_goal, AskUserQuestion}")
	}

	second, ok := provider.requestAt(1)
	if !ok {
		t.Fatal("expected a second LLM request (the record now exists — the predicate is false)")
	}
	if _, present := second.options["tool_choice"]; present {
		t.Fatal("request 2 must NOT carry a tool-choice option")
	}
	if _, present := second.options["native_search"]; !present {
		t.Fatal("request 2 (narrowing lifted) must carry native_search again — the suppression is scoped to the narrowed request only")
	}
}

// TestGoalTurn_QuestionDoorBudgetIncrement proves the ask door taken on a
// narrowed goal turn bumps GoalQuestionRoundsUsed exactly once (FR-010), and
// that a SECOND goal turn on the SAME session — the question budget now
// spent — narrows to {set_goal} alone.
func TestGoalTurn_QuestionDoorBudgetIncrement(t *testing.T) {
	provider := &narrowedDoorCaptureProvider{
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

	meta := mustActiveGoalRecord(t, sid)
	if meta.QuestionRoundsUsed != 1 {
		t.Fatalf("goal record QuestionRoundsUsed = %d, want 1 after the ask door was taken on a narrowed goal turn", meta.QuestionRoundsUsed)
	}

	// A fresh evaluation on the SAME (now budget-spent) session must narrow
	// to {set_goal} alone.
	ts2 := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
		TranscriptStore: store, TranscriptSessionID: sid, Channel: "webchat",
	}}
	d := al.evaluateGoalForcing(ts2, 1, agentInst.Tools.GetAll())
	if !d.layer1 || len(d.narrowed) != 1 || d.narrowed[0].Name() != tools.SetGoalToolName {
		t.Fatalf("after the question door is spent, narrowing must degrade to {set_goal} alone, got %+v", d)
	}
}

// TestGoalTurn_NoRequestEverCarriesToolChoice is a regression guard against
// reintroducing provider tool-choice forcing (D3 AMENDMENT, 2026-09-07):
// EVERY request across a full narrowed-then-restored turn lifecycle must be
// free of a "tool_choice" option key, on every provider, on every origin —
// asserted against the literal wire key rather than the (deleted)
// providers.ToolChoice* Go symbols, so this test keeps compiling and keeps
// meaning something even after pkg/providers drops those symbols entirely.
func TestGoalTurn_NoRequestEverCarriesToolChoice(t *testing.T) {
	provider := &narrowedDoorCaptureProvider{
		firstCallToolCalls: []providers.ToolCall{{
			ID: "call_1", Type: "function", Name: tools.SetGoalToolName,
			Function: &providers.FunctionCall{
				Name: tools.SetGoalToolName,
				Arguments: `{"definition":"Publish the launch checklist","criteria":[` +
					`{"text":"every launch task has an owner","judgment":"boolean"}],` +
					`"assessment":{"clarity":"clear"}}`,
			},
		}},
		finalMsg: "registered — working on it",
	}
	al, _ := newGoalLoopTestLoop(t, provider, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	allowGoalToolsPolicy(agentInst)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-no-tool-choice-1", "publish the launch checklist")

	opts := processOptions{
		SessionKey: "goal-no-tool-choice-session", Channel: "webchat", ChatID: "c1",
		UserMessage:     "publish the launch checklist",
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
	second, ok := provider.requestAt(1)
	if !ok {
		t.Fatal("expected a second LLM request")
	}
	for i, req := range []capturedRequest{first, second} {
		if _, present := req.options["tool_choice"]; present {
			t.Fatalf("request %d carries a %q option key — provider tool-choice forcing must never reappear (D3 amendment)",
				i+1, "tool_choice")
		}
	}
}
