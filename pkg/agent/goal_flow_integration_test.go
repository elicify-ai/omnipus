// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_flow_integration_test.go is the wave-5 CROSS-AREA integration suite
// for ADR-081 (work-first goal flow): it drives full, multi-turn scripted-
// provider lifecycles through the REAL runTurn/processMessage/
// processSystemMessage machinery, gluing together the per-area coverage
// already proven in goal_first_move_test.go (D3/D4, renamed from
// goal_forcing_test.go on the D3 amendment — tool-choice forcing is
// deleted; narrowing survives), goal_keeper_repairs_test.go (D6), and
// goal_record_wiring_test.go (D2/D5/D7). Traces to:
// docs/internal/architecture/ADR-081-work-first-goal-flow.md,
// docs/internal/specs/work-first-goal-flow-spec.md tests 21/22/23.
package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ============================================================================
// Shared scripted-provider / dispatcher scaffolding
// ============================================================================

// e2eCapturedCall is one full LLM request an e2eScriptedProvider observed —
// messages ARE captured (unlike goal_first_move_test.go's capturedRequest),
// since this suite needs to inspect a tool_result's content (e.g. the
// set_goal(mode:update) diff echoed back to the model on the following
// request).
type e2eCapturedCall struct {
	messages []providers.Message
	tools    []providers.ToolDefinition
	options  map[string]any
}

// e2eScriptedProvider scripts a sequence of LLM responses by 1-based call
// number, mirroring goal_first_move_test.go's narrowedDoorCaptureProvider
// but extended to span MULTIPLE runTurn/processSystemMessage invocations across
// one test — the work-first lifecycle is not one round-loop, it is several
// separate turns dispatched on the same session over the life of a goal.
// Calls beyond len(scripted) return `fallback` as a plain terminal response
// (never an error) so an unexpected extra round degrades to an observable
// assertion failure downstream rather than a panic.
type e2eScriptedProvider struct {
	mu       sync.Mutex
	calls    int
	scripted []*providers.LLMResponse
	captured []e2eCapturedCall
	fallback string
}

func (p *e2eScriptedProvider) Chat(
	_ context.Context, msgs []providers.Message, toolDefs []providers.ToolDefinition, _ string, options map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.captured = append(p.captured, e2eCapturedCall{messages: msgs, tools: toolDefs, options: options})
	idx := p.calls - 1
	if idx < len(p.scripted) && p.scripted[idx] != nil {
		return p.scripted[idx], nil
	}
	return &providers.LLMResponse{Content: p.fallback}, nil
}

func (p *e2eScriptedProvider) GetDefaultModel() string { return "e2e-scripted-mock" }

func (p *e2eScriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *e2eScriptedProvider) callAt(n int) (e2eCapturedCall, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 0 || n >= len(p.captured) {
		return e2eCapturedCall{}, false
	}
	return p.captured[n], true
}

// e2eToolCallResponse builds a single-tool-call LLMResponse (the scripted
// model "takes a door").
func e2eToolCallResponse(callID, toolName, argsJSON string) *providers.LLMResponse {
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: callID, Type: "function", Name: toolName,
		Function: &providers.FunctionCall{Name: toolName, Arguments: argsJSON},
	}}}
}

// e2eTextResponse builds a plain terminal-text LLMResponse.
func e2eTextResponse(text string) *providers.LLMResponse {
	return &providers.LLMResponse{Content: text}
}

// e2eAnyMessageContains reports whether any message in msgs carries substr
// in its content — used to prove a set_goal(mode:update) diff summary
// actually reached the model on the following request (D2).
func e2eAnyMessageContains(msgs []providers.Message, substr string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, substr) {
			return true
		}
	}
	return false
}

// e2eResumeDispatcher drives a REAL resume turn synchronously via
// al.runTurn — NOT al.processMessage/the bus, because this suite's test
// agent ("native-agent") is registered config.AgentTypeWorker (the
// newGoalLoopTestLoop/judge_test.go harness convention every other goal
// integration test in this package relies on), and processMessage's
// routing layer refuses to route an explicit agent_id at a worker (workers
// are delegation-only, never a chat target) — exactly the pattern
// TestGoalTurn_EndToEnd_ForcedDoorAndRestoration (goal_forcing_test.go)
// already established for driving a turn directly. Production's real
// dispatcher (pkg/gateway/ws_ask_user.go's askUserResumeDispatcher) DOES go
// through the bus — this is the test-harness equivalent of that same
// contract: build the resume as an ordinary continuation turn on the same
// session. Counts invocations so the S-32 stale-card subtest can assert
// DispatchResume was NEVER called for a superseded card.
type e2eResumeDispatcher struct {
	mu      sync.Mutex
	calls   int
	lastErr error

	al         *AgentLoop
	agentInst  *AgentInstance
	store      *session.UnifiedStore
	sid        string
	channel    string
	chatID     string
	sessionKey string
}

func (d *e2eResumeDispatcher) DispatchResume(set *askuser.PendingSet, resumeText string) error {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()

	// resumeIsUserInitiated's exact heuristic (pkg/gateway/ws_ask_user.go):
	// a cancelled set, or any non-auto-default answer, is a human action.
	// FR-007 forbids the forcing predicate from consulting this at all — it
	// is captured here purely for realism/observability, not correctness.
	userInitiated := set.Status == askuser.StatusCancelled
	if !userInitiated {
		for i := range set.Answers {
			if !set.Answers[i].AutoDefault {
				userInitiated = true
				break
			}
		}
	}
	opts := processOptions{
		TranscriptStore: d.store, TranscriptSessionID: d.sid,
		Channel: d.channel, ChatID: d.chatID, SessionKey: d.sessionKey,
		UserMessage: resumeText, UserInitiated: userInitiated,
		DefaultResponse: "done",
	}
	ts := newTurnState(d.agentInst, opts, d.al.newTurnEventScope(d.agentInst.ID, opts.SessionKey))
	_, err := d.al.runTurn(context.Background(), ts)
	d.mu.Lock()
	d.lastErr = err
	d.mu.Unlock()
	return err
}

func (d *e2eResumeDispatcher) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func (d *e2eResumeDispatcher) errResult() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastErr
}

// ============================================================================
// Test 21 — TestGoalClarify_WebCardRoundtrip (S-09/S-11/S-12/S-32)
// ============================================================================

// TestGoalClarify_WebCardRoundtrip drives the web clarify roundtrip end to
// end through the REAL AskUserQuestion registry + a scripted provider: a
// vague `/goal` on a web session takes the ask door, parks, and — whether
// answered by a human (Submit) or by the server's own default-safe
// auto-submit timer — resumes into a SECOND forced request (budget now
// spent) whose set_goal call registers the record, with NO confirmation
// step anywhere in the flow. A third subtest proves the stale-card
// supersession rule (S-32/FR-028): a fresh `/goal` activation cancels an
// orphaned parked card WITHOUT dispatching its resume, and the card's late
// Submit returns askuser.ErrNoPending.
func TestGoalClarify_WebCardRoundtrip(t *testing.T) {
	t.Run("human_answer_roundtrip", func(t *testing.T) {
		resetGoalTriggerStateForTest()
		provider := &e2eScriptedProvider{
			scripted: []*providers.LLMResponse{
				e2eToolCallResponse("call_ask", tools.AskUserQuestionToolName,
					`{"questions":[{"header":"Scope","question":"Single or multiplayer?",`+
						`"options":[{"label":"Single","description":"one player"},{"label":"Multi","description":"two players"}]}]}`),
				e2eToolCallResponse("call_reg", tools.SetGoalToolName,
					`{"definition":"Build a single-player tetris clone","criteria":[`+
						`{"text":"the game renders and accepts keyboard input","judgment":"boolean"}],`+
						`"assessment":{"clarity":"clear"}}`),
				e2eTextResponse("starting work now"),
			},
			fallback: "unexpected extra call",
		}
		al, _ := newGoalLoopTestLoop(t, provider, nil)
		agentInst, ok := al.GetRegistry().GetAgent("native-agent")
		if !ok {
			t.Fatal("native-agent not registered")
		}
		allowGoalToolsPolicy(agentInst)
		store, sid := newGoalTestSession(t, al, agentInst.ID)

		dispatcher := &e2eResumeDispatcher{
			al: al, agentInst: agentInst, store: store, sid: sid,
			channel: "webchat", chatID: "c1", sessionKey: "sk-clarify-human",
		}
		reg := askuser.NewRegistry(store, dispatcher, askuser.Options{})
		al.SetAskUserRegistry(reg)

		opts := processOptions{
			TranscriptStore: store, TranscriptSessionID: sid,
			Channel: "webchat", ChatID: "c1", SessionKey: "sk-clarify-human", UserInitiated: true,
			DefaultResponse: "done",
		}
		matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal organize my week", UserInitiated: true}, agentInst, &opts)
		if !matched || handled || reply != "" {
			t.Fatalf("activation: matched=%v handled=%v reply=%q, want matched=true handled=false reply=\"\"", matched, handled, reply)
		}

		ts := newTurnState(agentInst, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))
		result, err := al.runTurn(context.Background(), ts)
		if err != nil {
			t.Fatalf("runTurn: %v", err)
		}
		if result.status != TurnEndStatusParked {
			t.Fatalf("the AskUserQuestion door must park the turn, got status=%v", result.status)
		}

		first, ok := provider.callAt(0)
		if !ok {
			t.Fatal("provider was never called")
		}
		if len(first.tools) != 2 {
			t.Fatalf("the forced first request must offer exactly 2 tools, got %d: %v", len(first.tools), toolNamesOf(first.tools))
		}

		meta, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}
		if meta.GoalQuestionRoundsUsed != 1 {
			t.Fatalf("GoalQuestionRoundsUsed = %d, want 1 after the ask door was taken", meta.GoalQuestionRoundsUsed)
		}
		if meta.GoalCriteriaJSON != "" {
			t.Fatal("the record must still be empty while the card is parked")
		}

		pending, ok := reg.PendingForSession(sid)
		if !ok {
			t.Fatal("expected a pending card after the AskUserQuestion door")
		}
		cardID := pending.CardID

		collector, cleanup := newEventCollector(t, al)
		defer cleanup()

		if submitErr := reg.Submit(cardID, sid, "", []askuser.SubmittedAnswer{
			{Header: "Scope", Selected: []string{"Single"}},
		}); submitErr != nil {
			t.Fatalf("Submit: %v", submitErr)
		}
		if got := dispatcher.callCount(); got != 1 {
			t.Fatalf("DispatchResume calls = %d, want exactly 1", got)
		}
		if resumeErr := dispatcher.errResult(); resumeErr != nil {
			t.Fatalf("the resume turn itself failed: %v", resumeErr)
		}

		second, ok := provider.callAt(1)
		if !ok {
			t.Fatal("expected a second LLM call on the resume turn — the predicate must still hold")
		}
		// FR-010: the question budget is now spent — the predicate still
		// holds (record still empty) so narrowing still applies, to
		// {set_goal} alone.
		if len(second.tools) != 1 || second.tools[0].Function.Name != tools.SetGoalToolName {
			t.Fatalf("resume request must narrow to {set_goal} alone (budget spent), got %v", toolNamesOf(second.tools))
		}
		// D3 amendment (2026-09-07): no request EVER carries a tool-choice
		// option — provider tool-choice forcing is deleted. Determinism
		// comes from the immediate post-turn correction, not the request.
		if _, present := second.options["tool_choice"]; present {
			t.Fatal("no request may ever carry a tool-choice option — forcing is deleted (D3 amendment)")
		}

		afterMeta, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}
		if afterMeta.GoalCriteriaJSON == "" {
			t.Fatal("the resumed turn's set_goal call must have registered the record")
		}
		compiled := loadCompiledGoal(afterMeta.GoalCriteriaJSON)
		if compiled == nil || compiled.Definition != "Build a single-player tetris clone" {
			t.Fatalf("the registered record must reflect the agent's post-answer definition, got %+v", compiled)
		}

		deadline := time.Now().Add(2 * time.Second)
		var payloads []GoalStatusChangedPayload
		for time.Now().Before(deadline) {
			payloads = goalStatusPayloadsFor(collector, sid)
			if len(payloads) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if len(payloads) == 0 {
			t.Fatal("registering the record must emit an active goal_status frame — no confirmation step exists anywhere in this flow")
		}
		last := payloads[len(payloads)-1]
		if last.State != goalPillActive {
			t.Fatalf("frame state = %q, want %q — the queued/confirm state must never be emitted", last.State, goalPillActive)
		}
		if last.Definition != "Build a single-player tetris clone" {
			t.Fatalf("frame definition = %q, want the registered definition", last.Definition)
		}
	})

	t.Run("auto_submit_resume_behaves_identically", func(t *testing.T) {
		resetGoalTriggerStateForTest()
		provider := &e2eScriptedProvider{
			scripted: []*providers.LLMResponse{
				e2eToolCallResponse("call_ask", tools.AskUserQuestionToolName,
					`{"questions":[{"header":"Scope","question":"Single or multiplayer?","default_safe":true,"recommended":"Single",`+
						`"options":[{"label":"Single","description":"one player"},{"label":"Multi","description":"two players"}]}]}`),
				e2eToolCallResponse("call_reg", tools.SetGoalToolName,
					`{"definition":"Build a single-player tetris clone (default assumptions)","criteria":[`+
						`{"text":"the game renders and accepts keyboard input","judgment":"boolean"}],`+
						`"assessment":{"clarity":"ambiguous","assumptions":["nobody answered in time — defaulting to single-player"]}}`),
				e2eTextResponse("starting work now"),
			},
			fallback: "unexpected extra call",
		}
		al, _ := newGoalLoopTestLoop(t, provider, nil)
		agentInst, ok := al.GetRegistry().GetAgent("native-agent")
		if !ok {
			t.Fatal("native-agent not registered")
		}
		allowGoalToolsPolicy(agentInst)
		store, sid := newGoalTestSession(t, al, agentInst.ID)

		dispatcher := &e2eResumeDispatcher{
			al: al, agentInst: agentInst, store: store, sid: sid,
			channel: "webchat", chatID: "c1", sessionKey: "sk-clarify-auto",
		}
		reg := askuser.NewRegistry(store, dispatcher, askuser.Options{DefaultSafeDelay: 20 * time.Millisecond})
		al.SetAskUserRegistry(reg)

		opts := processOptions{
			TranscriptStore: store, TranscriptSessionID: sid,
			Channel: "webchat", ChatID: "c1", SessionKey: "sk-clarify-auto", UserInitiated: true,
			DefaultResponse: "done",
		}
		matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal organize my week", UserInitiated: true}, agentInst, &opts)
		if !matched || handled || reply != "" {
			t.Fatalf("activation: matched=%v handled=%v reply=%q, want matched=true handled=false reply=\"\"", matched, handled, reply)
		}

		ts := newTurnState(agentInst, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))
		result, err := al.runTurn(context.Background(), ts)
		if err != nil {
			t.Fatalf("runTurn: %v", err)
		}
		if result.status != TurnEndStatusParked {
			t.Fatalf("the AskUserQuestion door must park the turn, got status=%v", result.status)
		}

		// Block until the default-safe timer's server auto-submit fires AND
		// its (synchronous, inside the callback) resume dispatch fully
		// completes — reg.Quiesce blocks on exactly that WaitGroup.
		reg.Quiesce()

		if got := dispatcher.callCount(); got != 1 {
			t.Fatalf("DispatchResume calls = %d, want exactly 1 (the server auto-submit)", got)
		}
		if resumeErr := dispatcher.errResult(); resumeErr != nil {
			t.Fatalf("the auto-submit resume turn itself failed: %v", resumeErr)
		}

		afterMeta, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}
		if afterMeta.GoalQuestionRoundsUsed != 1 {
			t.Fatalf("GoalQuestionRoundsUsed = %d, want 1 — the auto-submit resume must consume the budget exactly like a human answer (S-12)", afterMeta.GoalQuestionRoundsUsed)
		}
		if afterMeta.GoalCriteriaJSON == "" {
			t.Fatal("the auto-submit resume's set_goal call must have registered the record")
		}
		compiled := loadCompiledGoal(afterMeta.GoalCriteriaJSON)
		if compiled == nil || compiled.Definition != "Build a single-player tetris clone (default assumptions)" {
			t.Fatalf("the registered record must reflect the agent's post-resume definition, got %+v", compiled)
		}

		second, ok := provider.callAt(1)
		if !ok {
			t.Fatal("expected a second LLM call on the auto-submit resume turn")
		}
		if len(second.tools) != 1 || second.tools[0].Function.Name != tools.SetGoalToolName {
			t.Fatalf("resume request must narrow to {set_goal} alone (budget spent), got %v", toolNamesOf(second.tools))
		}
	})

	t.Run("stale_card_superseded_by_new_goal", func(t *testing.T) {
		resetGoalTriggerStateForTest()
		provider := &e2eScriptedProvider{
			scripted: []*providers.LLMResponse{
				e2eToolCallResponse("call_ask", tools.AskUserQuestionToolName,
					`{"questions":[{"header":"Scope","question":"Single or multiplayer?",`+
						`"options":[{"label":"Single"},{"label":"Multi"}]}]}`),
			},
			fallback: "unexpected — the stale card's resume must never dispatch a turn",
		}
		al, _ := newGoalLoopTestLoop(t, provider, nil)
		agentInst, ok := al.GetRegistry().GetAgent("native-agent")
		if !ok {
			t.Fatal("native-agent not registered")
		}
		allowGoalToolsPolicy(agentInst)
		store, sid := newGoalTestSession(t, al, agentInst.ID)

		dispatcher := &e2eResumeDispatcher{
			al: al, agentInst: agentInst, store: store, sid: sid,
			channel: "webchat", chatID: "c1", sessionKey: "sk-stale",
		}
		reg := askuser.NewRegistry(store, dispatcher, askuser.Options{})
		al.SetAskUserRegistry(reg)

		opts := processOptions{
			TranscriptStore: store, TranscriptSessionID: sid,
			Channel: "webchat", ChatID: "c1", SessionKey: "sk-stale", UserInitiated: true,
			DefaultResponse: "done",
		}

		// Goal A activates, asks, and parks — card A pending.
		matched, handled, _ := al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal build a game", UserInitiated: true}, agentInst, &opts)
		if !matched || handled {
			t.Fatal("goal A's activation must continue into the turn")
		}
		ts := newTurnState(agentInst, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))
		result, err := al.runTurn(context.Background(), ts)
		if err != nil {
			t.Fatalf("runTurn (goal A): %v", err)
		}
		if result.status != TurnEndStatusParked {
			t.Fatalf("goal A's AskUserQuestion door must park the turn, got %v", result.status)
		}
		pending, ok := reg.PendingForSession(sid)
		if !ok {
			t.Fatal("expected goal A's card to be pending")
		}
		staleCardID := pending.CardID
		goalAMeta, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}
		staleGoalID := goalAMeta.GoalID
		if staleGoalID == "" {
			t.Fatal("goal A must have minted a GoalID")
		}

		// Simulate goal A having ended out-of-band while its card is still
		// genuinely parked — the ORPHANED-card precondition
		// cancelOrphanedClarifyCard's own doc comment names as its reason
		// for existing (its two call sites are new-goal activation and
		// `/goal clear`, both of which supersede a still-active goal's
		// card; this directly manipulates state to isolate that ONE call
		// site — new-goal activation — from the other).
		empty := ""
		if setMetaErr := store.SetMeta(sid, session.MetaPatch{
			GoalID: &empty, GoalCondition: &empty, GoalCriteriaJSON: &empty,
		}); setMetaErr != nil {
			t.Fatal(setMetaErr)
		}
		if _, ok := reg.PendingForSession(sid); !ok {
			t.Fatal("sanity: the stale card must still be genuinely pending before supersession")
		}

		matched2, handled2, _ := al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal build a completely different app", UserInitiated: true}, agentInst, &opts)
		if !matched2 || handled2 {
			t.Fatal("the new /goal must activate and continue into the turn")
		}

		if got := dispatcher.callCount(); got != 0 {
			t.Fatalf("DispatchResume must NEVER be called for the superseded card, got %d calls", got)
		}
		if _, ok := reg.PendingForSession(sid); ok {
			t.Fatal("the stale card must be gone once the new goal activates")
		}
		if submitErr := reg.Submit(staleCardID, sid, "", []askuser.SubmittedAnswer{
			{Header: "Scope", Selected: []string{"Single"}},
		}); !errors.Is(submitErr, askuser.ErrNoPending) {
			t.Fatalf("a late Submit on the cancelled stale card must return ErrNoPending, got %v", submitErr)
		}

		newMeta, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}
		if newMeta.GoalCondition != "build a completely different app" {
			t.Fatalf("the new goal must be genuinely active, got condition=%q", newMeta.GoalCondition)
		}
		if newMeta.GoalID == "" || newMeta.GoalID == staleGoalID {
			t.Fatalf("the new goal must mint a FRESH GoalID distinct from the superseded one, got %q (stale was %q)", newMeta.GoalID, staleGoalID)
		}
	})
}

// ============================================================================
// Test 22 — TestGoalFlow_EndToEnd_Web (S-01/S-04/S-17/S-22)
// ============================================================================

// TestGoalFlow_EndToEnd_Web drives a single scripted-provider webchat
// session through the FULL ADR-081 lifecycle in one test (spec test 22):
// instant activation with zero LLM calls before the first working request
// (C-1) -> the forced first request registers the record via set_goal ->
// free work continues -> an ordinary steering message updates the record
// via set_goal(mode:update) with an observable diff -> the keeper forces an
// idle cycle on an UNMET-judging Judge provider -> the continuation push is
// dispatched and ACCEPTED (activity bumps, idleSettling clears) -> a SECOND
// idle cycle fires (C-7, the un-wedge invariant).
func TestGoalFlow_EndToEnd_Web(t *testing.T) {
	resetGoalTriggerStateForTest()

	const goalText = "build me a tiny tetris game"
	provider := &e2eScriptedProvider{
		scripted: []*providers.LLMResponse{
			// call 1 (activation turn, round 1): forced pair -> registers.
			e2eToolCallResponse("call_reg", tools.SetGoalToolName,
				`{"definition":"Build a tiny tetris game","criteria":[`+
					`{"text":"the game renders and accepts keyboard input","judgment":"boolean"}],`+
					`"assessment":{"clarity":"clear"}}`),
			// call 2 (activation turn, round 2): full surface restored -> free work continues.
			e2eTextResponse("Starting on the tetris board now."),
			// call 3 (steering turn, round 1): predicate false, no forcing -> steers via mode:update.
			e2eToolCallResponse("call_update", tools.SetGoalToolName,
				`{"mode":"update","definition":"Build a tiny dark-themed tetris game","criteria":[`+
					`{"text":"the game renders and accepts keyboard input","judgment":"boolean"},`+
					`{"text":"the color scheme is dark-only","judgment":"boolean"}],`+
					`"assessment":{"clarity":"clear"}}`),
			// call 4 (steering turn, round 2): free work continues.
			e2eTextResponse("Applied — going dark-themed only."),
			// call 5: the idle-steer continuation-push follow-up turn the
			// keeper dispatches on the unmet verdict.
			e2eTextResponse("Continuing to work on the unmet items."),
		},
		fallback: "unexpected extra call",
	}
	al, judgeInst := newGoalLoopTestLoop(t, provider, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	// Grant a couple of extra tools beyond the goal pair so the "full
	// surface restored" assertion (request 2) can distinguish "genuinely
	// restored" from "still just the narrowed pair, coincidentally" — the
	// same rationale TestGoalTurn_EndToEnd_ForcedDoorAndRestoration uses.
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

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk-e2e-web", UserInitiated: true,
		DefaultResponse: "done",
	}

	// --- Activation: C-1, zero LLM calls before the first working request. ---
	matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal " + goalText, UserInitiated: true}, agentInst, &opts)
	if !matched || handled || reply != "" {
		t.Fatalf("activation: matched=%v handled=%v reply=%q, want matched=true handled=false reply=\"\"", matched, handled, reply)
	}
	if opts.UserMessage != goalText {
		t.Fatalf("opts.UserMessage = %q, want the raw intent", opts.UserMessage)
	}
	if got := provider.callCount(); got != 0 {
		t.Fatalf("C-1: activation must make ZERO LLM calls before the first working request, got %d", got)
	}

	// --- Turn 1: the forced first request registers, then free work continues. ---
	ts1 := newTurnState(agentInst, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))
	result1, err := al.runTurn(context.Background(), ts1)
	if err != nil {
		t.Fatalf("runTurn (activation): %v", err)
	}
	if result1.status == TurnEndStatusParked {
		t.Fatal("a confident registration must not park the turn")
	}

	first, ok := provider.callAt(0)
	if !ok {
		t.Fatal("provider was never called")
	}
	if len(first.tools) != 2 {
		t.Fatalf("request 1 must offer EXACTLY 2 tools (the narrowed pair), got %d: %v",
			len(first.tools), toolNamesOf(first.tools))
	}
	// D3 amendment (2026-09-07): narrowing the tool surface is the whole
	// mechanism now — no request ever carries a tool-choice option.
	if _, present := first.options["tool_choice"]; present {
		t.Fatal("request 1 must NOT carry a tool-choice option — forcing is deleted (D3 amendment)")
	}

	meta1, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if meta1.GoalCriteriaJSON == "" {
		t.Fatal("turn 1's set_goal call must have registered the record")
	}
	compiled1 := loadCompiledGoal(meta1.GoalCriteriaJSON)
	if compiled1 == nil || compiled1.Definition != "Build a tiny tetris game" || len(compiled1.Criteria) != 1 {
		t.Fatalf("the registered record must reflect the agent's first-move definition, got %+v", compiled1)
	}

	second, ok := provider.callAt(1)
	if !ok {
		t.Fatal("expected a second LLM call restoring the full surface")
	}
	if len(second.tools) <= 2 {
		t.Fatalf("request 2 must restore the FULL policy-filtered surface (> 2 tools), got %d: %v",
			len(second.tools), toolNamesOf(second.tools))
	}
	if _, present := second.options["tool_choice"]; present {
		t.Fatal("request 2 must NOT carry a tool-choice option — forcing is deleted (D3 amendment)")
	}

	// --- Turn 2: an ordinary steering message updates the record via set_goal(mode:update). ---
	opts.UserMessage = "actually make it dark-themed only"
	ts2 := newTurnState(agentInst, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))
	result2, err := al.runTurn(context.Background(), ts2)
	if err != nil {
		t.Fatalf("runTurn (steering): %v", err)
	}
	if result2.status == TurnEndStatusParked {
		t.Fatal("steering must not park the turn")
	}

	third, ok := provider.callAt(2)
	if !ok {
		t.Fatal("expected a third LLM call for the steering turn")
	}
	if _, present := third.options["tool_choice"]; present {
		t.Fatal("the steering turn's first request must NOT carry a tool-choice option — the record already exists (predicate false), and no request ever carries one anyway (D3 amendment)")
	}

	meta2, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	compiled2 := loadCompiledGoal(meta2.GoalCriteriaJSON)
	if compiled2 == nil || compiled2.Definition != "Build a tiny dark-themed tetris game" {
		t.Fatalf("steering must have updated the record's definition, got %+v", compiled2)
	}
	if len(compiled2.Criteria) != 2 {
		t.Fatalf("steering must have ADDED a criterion (dark theme), got %d criteria: %+v", len(compiled2.Criteria), compiled2.Criteria)
	}
	// set_goal(mode:update)'s tool_result carries a diff summary (D2) —
	// prove it reached the model on the request that follows.
	fourth, ok := provider.callAt(3)
	if !ok {
		t.Fatal("expected a fourth LLM call ending the steering turn")
	}
	if !e2eAnyMessageContains(fourth.messages, "added") {
		t.Fatal("set_goal(mode:update)'s tool_result must carry an observable diff (D2) reporting what was added")
	}

	// --- Keeper: force an idle cycle with an UNMET-judging Judge provider. ---
	// Prime the FR-014b zero-output-triple watermark + a prior transcript
	// entry so idle settlement adjudicates NORMALLY on the very first check
	// (goalZeroOutputTripleHolds' own degenerate-baseline rule would
	// otherwise treat a fresh watermark as "nothing to compare against yet"
	// and dispatch a bounded continue-push instead of judging) — this
	// test's concern is the un-wedge invariant (C-7), not the FR-014b push
	// ladder, which has its own dedicated coverage in
	// goal_keeper_repairs_test.go.
	primeGoalZeroOutputTripleFalse(t, store, sid, agentInst.ID)
	cp := unmetJudgeProvider("dark theme not yet verified")
	judgeInst.Provider = cp

	// Rewind GoalLastActivityAt (the quiet-window precondition) into the
	// past and settle with a REAL time.Now() — NOT an artificially
	// future-shifted `now`. goalZeroOutputTripleHolds stamps its own
	// watermark to whatever `now` this call receives; a future-shifted
	// `now` would set the watermark AHEAD of the real wall-clock timestamps
	// the intervening turns actually write, making every later transcript
	// entry look like it happened BEFORE the watermark and wrongly keep
	// tripping the FR-014b bounded-push branch instead of real
	// adjudication. Real `now` keeps the watermark and real transcript
	// timestamps in the same, correctly-ordered clock.
	rewindGoalLastActivity(t, store, sid)
	al.goalQuietWindowSettle(time.Now()) // cycle 1
	if got := cp.callCount(); got != 1 {
		t.Fatalf("cycle 1: Judge calls = %d, want 1", got)
	}
	afterCycle1, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if afterCycle1.GoalRoundsUsed != 1 {
		t.Fatalf("cycle 1: rounds_used = %d, want 1 (a real verdict round was consumed)", afterCycle1.GoalRoundsUsed)
	}

	var steerMsg bus.InboundMessage
	select {
	case steerMsg = <-al.bus.InboundChan():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the idle-steer continuation push to be published")
	}
	if steerMsg.Sender.CanonicalID != goalLoopFollowUpSenderID {
		t.Fatalf("the continuation push must be stamped with the goal-loop sender, got %q", steerMsg.Sender.CanonicalID)
	}

	if _, processErr := al.processSystemMessage(context.Background(), steerMsg); processErr != nil {
		t.Fatalf("processSystemMessage (continuation push): %v", processErr)
	}
	if al.goalIsIdleSettling(sid) {
		t.Fatal("the continuation push must be ACCEPTED — activity must bump and idleSettling must clear (the un-wedge, D6b)")
	}

	// --- A SECOND idle cycle must be able to fire (C-7). ---
	//
	// review-round-1 finding #4(a): the continuation-push turn processed
	// above ran the scripted provider's plain TEXT response ("Continuing to
	// work on the unmet items.") — no tool calls, so no adjudicable output
	// (session.EntryTypeToolCall entry) landed between cycle 1's and cycle
	// 2's watermarks. The zero-output triple therefore correctly reads
	// "still zero output" (a bare acknowledgment is not real work), and
	// cycle 2 dispatches its OWN bounded continue-push rather than a second
	// Judge call — this is the CORRECT behavior, not a wedge: the un-wedge
	// invariant itself was already proven above (idleSettling cleared after
	// the steer turn was accepted). This assertion proves the SECOND cycle
	// genuinely fires (the push counter advances) rather than silently doing
	// nothing.
	rewindGoalLastActivity(t, store, sid)
	al.goalQuietWindowSettle(time.Now()) // cycle 2
	if got := cp.callCount(); got != 1 {
		t.Fatalf("cycle 2: Judge calls = %d, want 1 (unchanged — a bare-text continuation reply is not adjudicable output)", got)
	}
	afterCycle2, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if afterCycle2.GoalZeroOutputPushes != 1 {
		t.Fatalf("cycle 2: GoalZeroOutputPushes = %d, want 1 (a second full idle cycle must complete — no wedge)",
			afterCycle2.GoalZeroOutputPushes)
	}
}

// rewindGoalLastActivity pushes sid's GoalLastActivityAt into the past so
// the idle quiet-window precondition is satisfied for a goalQuietWindowSettle
// call driven with a REAL time.Now() (see its call sites' doc comment for
// why a future-shifted `now` is the wrong tool here).
func rewindGoalLastActivity(t *testing.T, store *session.UnifiedStore, sid string) {
	t.Helper()
	past := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	if err := store.SetMeta(sid, session.MetaPatch{GoalLastActivityAt: &past}); err != nil {
		t.Fatal(err)
	}
}

// ============================================================================
// Test 23 — TestGoalFlow_EndToEnd_Channel (S-11/S-31)
// ============================================================================

// TestGoalFlow_EndToEnd_Channel proves the channel-origin half of the
// work-first flow (spec test 23): no forcing, no card — a rubric-only turn
// — and the formatted record text goes out on the outbound bus to the
// goal's routed channel exactly once per successful set_goal write
// (S-31/C-11's channel half). A second subtest proves a channel-origin
// conversational question is nothing but ordinary chat (S-11) — no forcing,
// no park, no interception.
func TestGoalFlow_EndToEnd_Channel(t *testing.T) {
	t.Run("register_via_set_goal_echoes_once_on_the_routed_channel", func(t *testing.T) {
		resetGoalTriggerStateForTest()
		provider := &e2eScriptedProvider{
			scripted: []*providers.LLMResponse{
				e2eToolCallResponse("call_reg", tools.SetGoalToolName,
					`{"definition":"Publish the launch checklist","criteria":[`+
						`{"text":"every launch task has an owner","judgment":"boolean"}],`+
						`"assessment":{"clarity":"clear"}}`),
				e2eTextResponse("Registered — working on it."),
			},
			fallback: "unexpected extra call",
		}
		al, _ := newGoalLoopTestLoop(t, provider, nil)
		agentInst, ok := al.GetRegistry().GetAgent("native-agent")
		if !ok {
			t.Fatal("native-agent not registered")
		}
		allowGoalToolsPolicy(agentInst)
		store, sid := newGoalTestSession(t, al, agentInst.ID)

		opts := processOptions{
			TranscriptStore: store, TranscriptSessionID: sid,
			Channel: "telegram", ChatID: "chat-99", SessionKey: "sk-channel-1", UserInitiated: true,
			DefaultResponse: "done",
		}
		matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal organize the launch checklist", UserInitiated: true}, agentInst, &opts)
		if !matched || handled || reply != "" {
			t.Fatalf("activation: matched=%v handled=%v reply=%q, want matched=true handled=false reply=\"\"", matched, handled, reply)
		}

		ts := newTurnState(agentInst, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))
		if _, err := al.runTurn(context.Background(), ts); err != nil {
			t.Fatalf("runTurn: %v", err)
		}

		first, ok := provider.callAt(0)
		if !ok {
			t.Fatal("provider was never called")
		}
		// D3 amendment (2026-09-07): narrowing now applies on channel
		// origins too — with AskUserQuestion permanently web-only (G-B2),
		// the narrowed pair degrades to {set_goal} alone. No request ever
		// carries a tool-choice option (forcing is deleted).
		if len(first.tools) != 1 || first.tools[0].Function.Name != tools.SetGoalToolName {
			t.Fatalf("a channel-origin goal turn must narrow to {set_goal} alone, got %v", toolNamesOf(first.tools))
		}
		if _, present := first.options["tool_choice"]; present {
			t.Fatal("a channel-origin goal turn must NEVER carry a tool-choice option")
		}

		meta, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}
		if meta.GoalCriteriaJSON == "" {
			t.Fatal("set_goal's write must have landed on the session meta")
		}

		select {
		case msg := <-al.bus.OutboundChan():
			if msg.Channel != "telegram" || msg.ChatID != "chat-99" {
				t.Fatalf("echo routed to %s/%s, want telegram/chat-99", msg.Channel, msg.ChatID)
			}
			if !strings.Contains(msg.Content, "Publish the launch checklist") {
				t.Fatalf("echo content = %q, want it to carry the registered definition", msg.Content)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("expected exactly one channel echo, got none")
		}
		select {
		case msg := <-al.bus.OutboundChan():
			t.Fatalf("expected exactly ONE echo, got a second: %+v", msg)
		case <-time.After(200 * time.Millisecond):
		}
	})

	t.Run("conversational_question_is_just_chat", func(t *testing.T) {
		resetGoalTriggerStateForTest()
		provider := &e2eScriptedProvider{
			scripted: []*providers.LLMResponse{
				e2eTextResponse("Should the checklist cover marketing tasks too, or engineering only?"),
			},
			fallback: "unexpected extra call",
		}
		al, _ := newGoalLoopTestLoop(t, provider, nil)
		agentInst, ok := al.GetRegistry().GetAgent("native-agent")
		if !ok {
			t.Fatal("native-agent not registered")
		}
		allowGoalToolsPolicy(agentInst)
		store, sid := newGoalTestSession(t, al, agentInst.ID)

		opts := processOptions{
			TranscriptStore: store, TranscriptSessionID: sid,
			Channel: "telegram", ChatID: "chat-77", SessionKey: "sk-channel-2", UserInitiated: true,
			DefaultResponse: "done",
		}
		matched, handled, _ := al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal organize my launch", UserInitiated: true}, agentInst, &opts)
		if !matched || handled {
			t.Fatal("activation must continue into the turn")
		}

		ts := newTurnState(agentInst, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))
		result, err := al.runTurn(context.Background(), ts)
		if err != nil {
			t.Fatalf("runTurn: %v", err)
		}
		if result.status == TurnEndStatusParked {
			t.Fatal("a channel-origin turn must NEVER park on a question — AskUserQuestion is web-only")
		}
		if !strings.Contains(result.finalContent, "marketing tasks") {
			t.Fatalf("the agent's conversational question must pass through as ordinary chat, got %q", result.finalContent)
		}

		meta, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}
		if meta.GoalCriteriaJSON != "" {
			t.Fatal("no record was registered — the goal must still be recordless")
		}

		first, ok := provider.callAt(0)
		if !ok {
			t.Fatal("provider was never called")
		}
		if len(first.tools) != 1 || first.tools[0].Function.Name != tools.SetGoalToolName {
			t.Fatalf("a channel-origin goal turn must narrow to {set_goal} alone, got %v", toolNamesOf(first.tools))
		}
		if _, present := first.options["tool_choice"]; present {
			t.Fatal("a channel-origin turn must never carry a tool-choice option")
		}
	})
}
