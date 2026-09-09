// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_forcing_bounded_escape_test.go covers the ADR-081 D3 amendment of
// 2026-09-08: the narrowed first-move door (evaluateGoalForcing, loop.go)
// used to apply to a turn's FIRST LLM request only (iteration==1). That
// missed a third outcome besides "register" and "park" — a narrowed call
// that FAILS (tool-arg validation error, policy denial at execution, or an
// error result) neither registers nor parks, so the base predicate
// (goalTurnRecordState: active goal, empty compiled record) is still true on
// the turn's SECOND request — and the old code handed that request the FULL
// unnarrowed tool surface with the record still empty.
//
// Real, operator-reproduced evidence: a /goal was set at 11:54:48Z. The
// forced first move narrowed iteration 1 to {set_goal, AskUserQuestion} —
// the model chose AskUserQuestion, and its call FAILED schema validation
// ("unexpected property \"recommended\"" inside an option). Because
// evaluateGoalForcing returned an empty decision whenever iteration != 1,
// the turn's SECOND LLM request got the full tool surface back while the
// goal record was still empty; the agent then ran ToolSearch, write_file×5,
// bash, serve_web, browser_navigate for ~17 minutes and only called set_goal
// at 12:12:26Z — the immediate post-turn correction
// (checkGoalLoopAfterTurn, goal_loop.go) never got a chance to run, because
// the turn never ended.
//
// The fix: the door stays narrowed for EVERY LLM request in the turn while
// the predicate holds — not only iteration 1 — bounded by
// goalForcingMaxNarrowAttempts (N=3) so a persistently-failing model cannot
// wedge the turn instead; once the bound is exceeded the door releases
// (full surface, WARN logged) and the existing post-turn nudge ladder (D6c)
// picks it up.
package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// sequencedToolCallProvider scripts a goal turn across MULTIPLE requests:
// scriptedCalls[n] is what request n+1 (0-indexed) returns as tool calls;
// once the script is exhausted (or a nil/empty entry is reached), the
// provider returns finalMsg as a plain terminal text response, ending the
// turn. Unlike narrowedDoorCaptureProvider (goal_first_move_test.go), which
// only scripts the FIRST call, this drives several consecutive narrowed
// requests — exactly the shape the real-world defect needs to reproduce.
type sequencedToolCallProvider struct {
	scriptedCalls [][]providers.ToolCall
	finalMsg      string

	calls    int
	captured []capturedRequest
}

func (p *sequencedToolCallProvider) Chat(
	_ context.Context, _ []providers.Message, toolDefs []providers.ToolDefinition, _ string, options map[string]any,
) (*providers.LLMResponse, error) {
	p.captured = append(p.captured, capturedRequest{tools: toolDefs, options: options})
	idx := p.calls
	p.calls++
	if idx < len(p.scriptedCalls) && len(p.scriptedCalls[idx]) > 0 {
		return &providers.LLMResponse{ToolCalls: p.scriptedCalls[idx]}, nil
	}
	return &providers.LLMResponse{Content: p.finalMsg}, nil
}

func (p *sequencedToolCallProvider) GetDefaultModel() string { return "sequenced-tool-call-mock" }

func (p *sequencedToolCallProvider) requestAt(n int) (capturedRequest, bool) {
	if n < 0 || n >= len(p.captured) {
		return capturedRequest{}, false
	}
	return p.captured[n], true
}

// badSetGoalCall builds a set_goal tool call missing the required
// "definition" argument — Execute (pkg/tools/set_goal.go) rejects it BEFORE
// touching the goal record, so the call fails (IsError) without writing
// anything: the base predicate (record still empty) stays true.
func badSetGoalCall(id string) providers.ToolCall {
	return providers.ToolCall{
		ID: id, Type: "function", Name: tools.SetGoalToolName,
		Function: &providers.FunctionCall{Name: tools.SetGoalToolName, Arguments: `{}`},
	}
}

// goalForcingFullSurfacePolicy grants the agent a few tools beyond the goal
// pair so a "full surface restored" assertion can distinguish genuine
// restoration from "still just the narrowed pair, coincidentally" (mirrors
// TestGoalTurn_EndToEnd_NarrowedDoorAndRestoration's own precedent).
func goalForcingFullSurfacePolicy(agentInst *AgentInstance) {
	agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{
			tools.SetGoalToolName:         config.ToolPolicyAllow,
			tools.AskUserQuestionToolName: config.ToolPolicyAllow,
			"read_file":                   config.ToolPolicyAllow,
			"write_file":                  config.ToolPolicyAllow,
			"search_web":                  config.ToolPolicyAllow,
		},
	})
}

// --- unit-level: narrowing persists while the record stays empty ----------

// TestGoalForcing_NarrowingPersistsAcrossIterations is the direct regression
// test for the 11:54→12:12 defect at the evaluateGoalForcing unit level: as
// long as the goal record stays empty, the narrowed pair must be offered on
// EVERY iteration up to goalForcingMaxNarrowAttempts, not just iteration 1.
func TestGoalForcing_NarrowingPersistsAcrossIterations(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	allowGoalToolsPolicy(agentInst)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-persist-1", "build a tetris game")

	ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
		TranscriptStore: store, TranscriptSessionID: sid, Channel: "webchat",
	}}

	// goalForcingMaxNarrowAttempts is 3 — iterations 1..3 must all narrow
	// (the record is never written in this test, mirroring a model that
	// keeps failing the narrowed pair, exactly like the 11:54→12:12 defect).
	for iter := 1; iter <= goalForcingMaxNarrowAttempts; iter++ {
		d := al.evaluateGoalForcing(ts, iter, agentInst.Tools.GetAll())
		if !d.layer1 || !d.rubric {
			t.Fatalf("iteration %d: record still empty must narrow and inject the rubric, got %+v", iter, d)
		}
		if len(d.narrowed) != 2 {
			t.Fatalf("iteration %d: narrowed must stay exactly {set_goal, AskUserQuestion} (2 tools), got %d: %+v",
				iter, len(d.narrowed), d.narrowed)
		}
		if ts.goalNarrowMisses != iter {
			t.Fatalf("iteration %d: goalNarrowMisses = %d, want %d", iter, ts.goalNarrowMisses, iter)
		}
		if ts.goalNarrowIsEscaped() {
			t.Fatalf("iteration %d: must not have escaped yet (attempts == max, not yet over)", iter)
		}
	}
}

// --- a FAILED narrowed call must not release the door ----------------------

// TestGoalForcing_FailedNarrowedCallDoesNotReleaseDoor drives a REAL runTurn:
// request 1 offers the narrowed pair and the model calls set_goal WITHOUT
// the required "definition" argument — the call fails (IsError), writing
// nothing. Request 2 must STILL be narrowed to the exact pair: a failed
// narrowed call is not the door being taken (design constraint), which is
// exactly the gap the 11:54→12:12 defect exploited under the old
// iteration==1-only gate.
func TestGoalForcing_FailedNarrowedCallDoesNotReleaseDoor(t *testing.T) {
	provider := &sequencedToolCallProvider{
		scriptedCalls: [][]providers.ToolCall{
			{badSetGoalCall("call_1")}, // request 1: fails validation
		},
		finalMsg: "still stuck",
	}
	al, _ := newGoalLoopTestLoop(t, provider, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	allowGoalToolsPolicy(agentInst)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-failed-call-1", "build me a tetris game")

	opts := processOptions{
		SessionKey: "goal-failed-call-session", Channel: "webchat", ChatID: "c1",
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
		t.Fatalf("request 1 must offer exactly the narrowed pair, got %d: %v", len(first.tools), toolNamesOf(first.tools))
	}

	second, ok := provider.requestAt(1)
	if !ok {
		t.Fatal("expected a second LLM request after the failed set_goal call")
	}
	secondNames := toolNamesOf(second.tools)
	if len(secondNames) != 2 {
		t.Fatalf("request 2 must STILL be narrowed to exactly {set_goal, AskUserQuestion} (a failed call did not take the door), got %d: %v",
			len(secondNames), secondNames)
	}
	wantSecond := map[string]bool{tools.SetGoalToolName: true, tools.AskUserQuestionToolName: true}
	for _, n := range secondNames {
		if !wantSecond[n] {
			t.Fatalf("request 2 offered an unexpected tool %q, want only {set_goal, AskUserQuestion}, got %v", n, secondNames)
		}
	}

	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if meta.GoalCriteriaJSON != "" {
		t.Fatalf("the failed call must not have written a record, got %q", meta.GoalCriteriaJSON)
	}
}

// --- a SUCCESSFUL set_goal releases the door immediately --------------------

// TestGoalForcing_SuccessfulSetGoalReleasesImmediately proves that once the
// record is written, the VERY NEXT evaluation (regardless of iteration
// number) sees holds==false and stops narrowing — no escape counter is
// needed for this path because goalTurnRecordState reads persisted state
// fresh on every call.
func TestGoalForcing_SuccessfulSetGoalReleasesImmediately(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	allowGoalToolsPolicy(agentInst)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-success-1", "build a tetris game")

	ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
		TranscriptStore: store, TranscriptSessionID: sid, Channel: "webchat",
	}}

	d1 := al.evaluateGoalForcing(ts, 1, agentInst.Tools.GetAll())
	if !d1.layer1 {
		t.Fatalf("iteration 1: must narrow, got %+v", d1)
	}
	if ts.goalNarrowMisses != 1 {
		t.Fatalf("goalNarrowMisses after iteration 1 = %d, want 1", ts.goalNarrowMisses)
	}

	// Simulate iteration 1's set_goal call succeeding — write the record
	// directly, exactly as SetGoalTool.Execute would on success.
	compiled := `{"intent":"x","prompt":"x","criteria":[{"id":"c1","kind":"prose","judgment":"boolean","text":"t"}]}`
	if err := store.SetMeta(sid, session.MetaPatch{GoalCriteriaJSON: &compiled}); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	d2 := al.evaluateGoalForcing(ts, 2, agentInst.Tools.GetAll())
	if d2.layer1 || d2.rubric {
		t.Fatalf("iteration 2, record now written: must NOT narrow or inject the rubric, got %+v", d2)
	}
	// The escape machinery must never even engage on this path — the record
	// write is what released the door, not the bounded escape.
	if ts.goalNarrowIsEscaped() {
		t.Fatal("the door must release via the record write, not via the bounded escape")
	}
	if ts.goalNarrowMisses != 1 {
		t.Fatalf("goalNarrowMisses must not advance once the record is written, got %d", ts.goalNarrowMisses)
	}
}

// --- a genuinely parked AskUserQuestion card releases the door -------------

// TestGoalForcing_ParkedAskUserQuestionReleasesDoor drives a REAL runTurn
// where the model's first narrowed move is a successful AskUserQuestion
// call. The call PARKS the turn (session.ParksTurn) — runTurn returns
// immediately (TurnEndStatusParked) rather than looping to a second LLM
// request, so the door "releases" trivially: there is no further request in
// THIS turn for it to still be narrowed on. Confirms exactly one provider
// call occurred and the record stays empty (the question, not set_goal, was
// the first move).
func TestGoalForcing_ParkedAskUserQuestionReleasesDoor(t *testing.T) {
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
	setActiveGoalRecordless(t, store, sid, "goal-park-1", "organize my week")

	reg := newTestAskUserRegistry(t, store)
	al.SetAskUserRegistry(reg)

	opts := processOptions{
		SessionKey: "goal-park-session", Channel: "webchat", ChatID: "c1",
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
	if _, ok := provider.requestAt(0); !ok {
		t.Fatal("provider was never called")
	}
	if _, ok := provider.requestAt(1); ok {
		t.Fatal("a parked turn must never issue a second LLM request")
	}
	if ts.goalNarrowMisses != 1 {
		t.Fatalf("exactly one narrowed attempt should have been counted before the park, got %d", ts.goalNarrowMisses)
	}
	if ts.goalNarrowIsEscaped() {
		t.Fatal("a parked turn must never trip the bounded escape")
	}

	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if meta.GoalCriteriaJSON != "" {
		t.Fatal("the question, not set_goal, was the first move — the record must still be empty")
	}
	if meta.GoalQuestionRoundsUsed != 1 {
		t.Fatalf("GoalQuestionRoundsUsed = %d, want 1", meta.GoalQuestionRoundsUsed)
	}
}

// --- the bounded escape fires at N and logs ---------------------------------

// TestGoalForcing_BoundedEscapeFiresAndLogs is the direct test for the fix's
// own circuit breaker: after goalForcingMaxNarrowAttempts (N=3) consecutive
// narrowed requests with the record still empty — the exact shape of the
// real-world 11:54→12:12 defect, replayed indefinitely instead of being
// caught after one miss — the door must release (full surface) and a WARN
// log must record why. Confirms the escape STAYS armed on a later request
// too (it must not silently re-engage narrowing once released).
func TestGoalForcing_BoundedEscapeFiresAndLogs(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	goalForcingFullSurfacePolicy(agentInst)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-escape-1", "an unresolvable goal")

	ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
		TranscriptStore: store, TranscriptSessionID: sid, Channel: "webchat",
	}}

	for iter := 1; iter <= goalForcingMaxNarrowAttempts; iter++ {
		d := al.evaluateGoalForcing(ts, iter, agentInst.Tools.GetAll())
		if !d.layer1 {
			t.Fatalf("iteration %d must still narrow (attempt %d <= max %d), got %+v", iter, iter, goalForcingMaxNarrowAttempts, d)
		}
	}

	readLog := captureLogFile(t, logger.WARN)
	escapeIteration := goalForcingMaxNarrowAttempts + 1
	dEscaped := al.evaluateGoalForcing(ts, escapeIteration, agentInst.Tools.GetAll())
	if dEscaped.layer1 {
		t.Fatalf("iteration %d (attempt %d > max %d) must release the door, got %+v",
			escapeIteration, escapeIteration, goalForcingMaxNarrowAttempts, dEscaped)
	}
	if !dEscaped.rubric {
		t.Fatal("the rubric note must keep nudging even after the escape releases narrowing")
	}
	if len(dEscaped.narrowed) != 0 {
		t.Fatalf("an escaped decision must offer no narrowed slice, got %+v", dEscaped.narrowed)
	}
	if !ts.goalNarrowIsEscaped() {
		t.Fatal("the bounded escape must be armed after exceeding goalForcingMaxNarrowAttempts")
	}
	logged := readLog()
	if !strings.Contains(logged, "bounded escape") {
		t.Fatalf("expected a WARN log recording the bounded escape, got: %s", logged)
	}
	if !strings.Contains(logged, sid) {
		t.Fatalf("the escape log must carry the session_id for diagnosability, got: %s", logged)
	}

	// A LATER request in the same turn must stay released — the escape does
	// not silently re-engage narrowing while the record remains empty.
	dLater := al.evaluateGoalForcing(ts, escapeIteration+1, agentInst.Tools.GetAll())
	if dLater.layer1 {
		t.Fatalf("a later request must stay released once escaped, got %+v", dLater)
	}
}

// TestGoalForcing_BoundedEscapeReleasesFullSurfaceEndToEnd drives a REAL
// runTurn where the model fails the narrowed set_goal call on every
// narrowed request; after goalForcingMaxNarrowAttempts failures, the escape
// must hand the model the FULL policy-filtered tool surface so it is not
// wedged in the narrowed pair for the rest of MaxIterations — this is the
// end-to-end shape of the fix for the 17-minutes-wedged defect (in this
// test, "wedged" would mean every request offers only 2 tools forever;
// the assertion is that this does NOT happen).
func TestGoalForcing_BoundedEscapeReleasesFullSurfaceEndToEnd(t *testing.T) {
	scripted := make([][]providers.ToolCall, 0, goalForcingMaxNarrowAttempts)
	for i := 0; i < goalForcingMaxNarrowAttempts; i++ {
		scripted = append(scripted, []providers.ToolCall{badSetGoalCall("call_bad")})
	}
	provider := &sequencedToolCallProvider{
		scriptedCalls: scripted,
		finalMsg:      "giving up on the tool call, replying in text",
	}
	al, _ := newGoalLoopTestLoop(t, provider, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	goalForcingFullSurfacePolicy(agentInst)
	// MaxIterations must comfortably exceed goalForcingMaxNarrowAttempts + 1
	// so the escaped request is reached instead of hitting the (unrelated)
	// iteration ceiling first.
	agentInst.MaxIterations = goalForcingMaxNarrowAttempts + 5

	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-escape-e2e-1", "an unresolvable goal")

	opts := processOptions{
		SessionKey: "goal-escape-e2e-session", Channel: "webchat", ChatID: "c1",
		UserMessage:     "an unresolvable goal",
		TranscriptStore: store, TranscriptSessionID: sid,
		DefaultResponse: "done", UserInitiated: true,
	}
	ts := newTurnState(agentInst, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))

	if _, err := al.runTurn(context.Background(), ts); err != nil {
		t.Fatalf("runTurn: %v", err)
	}

	for i := 0; i < goalForcingMaxNarrowAttempts; i++ {
		req, reqOK := provider.requestAt(i)
		if !reqOK {
			t.Fatalf("expected request %d to have been made", i+1)
		}
		if len(req.tools) != 2 {
			t.Fatalf("request %d (attempt %d <= max %d) must be narrowed to 2 tools, got %d: %v",
				i+1, i+1, goalForcingMaxNarrowAttempts, len(req.tools), toolNamesOf(req.tools))
		}
	}

	escaped, escOK := provider.requestAt(goalForcingMaxNarrowAttempts)
	if !escOK {
		t.Fatalf("expected an escaped request after %d narrowed attempts", goalForcingMaxNarrowAttempts)
	}
	if len(escaped.tools) <= 2 {
		t.Fatalf("the escaped request must offer the FULL policy-filtered surface (> 2 tools), got %d: %v",
			len(escaped.tools), toolNamesOf(escaped.tools))
	}

	if !ts.goalNarrowIsEscaped() {
		t.Fatal("the turn must have armed the bounded escape")
	}
}

// --- D3's existing rule: policy-denied set_goal means NO narrowing ---------

// TestGoalForcing_PolicyDeniedNeverNarrowsEvenAcrossIterations extends the
// existing single-call "policy_denied_no_narrowing" coverage
// (goal_first_move_test.go) across MULTIPLE evaluations on the same
// turnState: a policy-denied set_goal must never narrow, on iteration 1 or
// any later iteration, and — because it is never narrowed at all — must
// never advance the bounded-escape counter or arm the escape.
func TestGoalForcing_PolicyDeniedNeverNarrowsEvenAcrossIterations(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{tools.SetGoalToolName: config.ToolPolicyDeny},
	})
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-denied-1", "a denied goal")
	policyFiltered, _ := tools.FilterToolsByPolicy(agentInst.Tools.GetAll(), agentInst.AgentType, agentInst.LoadToolPolicy())

	ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
		TranscriptStore: store, TranscriptSessionID: sid, Channel: "webchat",
	}}

	for iter := 1; iter <= goalForcingMaxNarrowAttempts+2; iter++ {
		d := al.evaluateGoalForcing(ts, iter, policyFiltered)
		if d.layer1 {
			t.Fatalf("iteration %d: set_goal policy-denied must never narrow, got %+v", iter, d)
		}
		if !d.rubric {
			t.Fatalf("iteration %d: the base predicate (and therefore the rubric) still holds even when narrowing is skipped", iter)
		}
	}
	if ts.goalNarrowMisses != 0 {
		t.Fatalf("a policy-denied set_goal must never count as a narrowed attempt, got goalNarrowMisses=%d", ts.goalNarrowMisses)
	}
	if ts.goalNarrowIsEscaped() {
		t.Fatal("the bounded escape must never arm when narrowing never happened")
	}
}

// --- a non-goal turn is untouched -------------------------------------------

// TestGoalForcing_NonGoalTurnUntouched confirms the whole D3/D3-amendment
// machinery — narrowing, the rubric note, the bounded-escape counter — never
// engages for a turn with no active goal (or an already-populated record):
// goalTurnRecordState's holds==false short-circuits evaluateGoalForcing
// before any of the new counter logic is reached, on every iteration.
func TestGoalForcing_NonGoalTurnUntouched(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	allowGoalToolsPolicy(agentInst)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	// Deliberately no setActiveGoalRecordless call — this session has no
	// active goal at all.

	ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
		TranscriptStore: store, TranscriptSessionID: sid, Channel: "webchat",
	}}

	for iter := 1; iter <= goalForcingMaxNarrowAttempts+2; iter++ {
		d := al.evaluateGoalForcing(ts, iter, agentInst.Tools.GetAll())
		if d.layer1 || d.rubric {
			t.Fatalf("iteration %d: a non-goal turn must never narrow or inject the rubric, got %+v", iter, d)
		}
	}
	if ts.goalNarrowMisses != 0 {
		t.Fatalf("a non-goal turn must never advance the bounded-escape counter, got goalNarrowMisses=%d", ts.goalNarrowMisses)
	}
	if ts.goalNarrowIsEscaped() {
		t.Fatal("a non-goal turn must never arm the bounded escape")
	}
}
