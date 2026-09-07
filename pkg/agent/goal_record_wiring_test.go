// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_record_wiring_test.go covers ADR-081 D2 (set_goal's three late-bound
// seams — GoalRecordAccess/DiffFn/FeasibilityFn), D5 (write-side frame
// emission + channel echo), and D7 (the Judge-model fast lane) — the wave-2
// (W2a) regression suite for goal_record_wiring.go. Traces to:
// docs/internal/architecture/ADR-081-work-first-goal-flow.md (D2/D5/D7),
// docs/internal/specs/work-first-goal-flow-spec.md tests 15/16/17(emit side).
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- GoalRecordAccess (D2) --------------------------------------------------

func TestGoalRecordAccess_ReadWrite(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)

	access := agentLoopGoalRecordAccess{al: al}

	t.Run("read_no_active_goal", func(t *testing.T) {
		cond, rec, err := access.ReadGoalState(sid)
		if err != nil {
			t.Fatal(err)
		}
		if cond != "" || rec != "" {
			t.Fatalf("a fresh session must read empty condition/record, got %q/%q", cond, rec)
		}
	})

	t.Run("read_active_goal_empty_record", func(t *testing.T) {
		setActiveGoalRecordless(t, store, sid, "goal-r1", "build a game")
		cond, rec, err := access.ReadGoalState(sid)
		if err != nil {
			t.Fatal(err)
		}
		if cond != "build a game" {
			t.Fatalf("condition = %q, want the active condition", cond)
		}
		if rec != "" {
			t.Fatalf("record must still read empty (D1's transient state), got %q", rec)
		}
	})

	t.Run("write_persists_and_readable_back", func(t *testing.T) {
		recordJSON := `{"intent":"i","prompt":"p","definition":"Build a tetris clone",` +
			`"criteria":[{"id":"c1","kind":"prose","judgment":"boolean","text":"it renders","author":{"kind":"agent","id":"tester"}}],` +
			`"dod":[{"id":"d1","kind":"prose","judgment":"boolean","provenance":"floor","text":"no secrets","author":{"kind":"agent","id":"tester"}}]}`
		if err := access.WriteRecord(sid, recordJSON); err != nil {
			t.Fatalf("WriteRecord: %v", err)
		}
		_, rec, err := access.ReadGoalState(sid)
		if err != nil {
			t.Fatal(err)
		}
		if rec != recordJSON {
			t.Fatalf("the record read back must be byte-identical to what was written, got %q", rec)
		}
	})

	t.Run("read_unknown_session_errors", func(t *testing.T) {
		if _, _, err := access.ReadGoalState("no-such-session"); err == nil {
			t.Fatal("an unresolvable session must return an error, not a silent empty read")
		}
	})

	t.Run("write_unknown_session_errors", func(t *testing.T) {
		if err := access.WriteRecord("no-such-session", "{}"); err == nil {
			t.Fatal("an unresolvable session must return an error, not a silent no-op")
		}
	})
}

// TestGoalRecordAccess_WriteRecord_SideEffects proves ADR-081 D5/FR-019: a
// successful WriteRecord bumps activity, RESETS GoalZeroOutputPushes to 0
// (FR-014b's reset rule), and emits the goal_status frame in state ACTIVE
// with definition/criteria/dod populated from the freshly-written record.
func TestGoalRecordAccess_WriteRecord_SideEffects(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-side-1", "build a game")

	// Seed a nonzero push streak — WriteRecord must reset it.
	pushes := 2
	if err := store.SetMeta(sid, session.MetaPatch{GoalZeroOutputPushes: &pushes}); err != nil {
		t.Fatal(err)
	}
	beforeMeta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if beforeMeta.GoalZeroOutputPushes != 2 {
		t.Fatalf("seed failed: GoalZeroOutputPushes = %d, want 2", beforeMeta.GoalZeroOutputPushes)
	}

	collector, cleanup := newEventCollector(t, al)
	defer cleanup()

	recordJSON := `{"intent":"i","prompt":"p","definition":"Build a tetris clone",` +
		`"criteria":[{"id":"c1","kind":"prose","judgment":"boolean","text":"it renders and accepts input","author":{"kind":"agent","id":"tester"}}],` +
		`"dod":[{"id":"d1","kind":"prose","judgment":"boolean","provenance":"floor","text":"no secrets","author":{"kind":"agent","id":"tester"}}]}`
	access := agentLoopGoalRecordAccess{al: al}
	if err := access.WriteRecord(sid, recordJSON); err != nil {
		t.Fatalf("WriteRecord: %v", err)
	}

	afterMeta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if afterMeta.GoalZeroOutputPushes != 0 {
		t.Fatalf("GoalZeroOutputPushes = %d, want reset to 0 on a successful write", afterMeta.GoalZeroOutputPushes)
	}
	if afterMeta.GoalLastActivityAt == "" || afterMeta.GoalLastActivityAt == beforeMeta.GoalLastActivityAt {
		t.Fatal("GoalLastActivityAt must be bumped by a successful write")
	}

	// Give the async event-bus delivery a moment (SubscribeEvents fans out
	// via a channel + goroutine in newEventCollector).
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
		t.Fatal("WriteRecord must emit a goal_status frame")
	}
	last := payloads[len(payloads)-1]
	if last.State != goalPillActive {
		t.Fatalf("frame state = %q, want %q — queued must never be emitted from this call site", last.State, goalPillActive)
	}
	if last.Definition != "Build a tetris clone" {
		t.Fatalf("frame definition = %q, want the record's definition", last.Definition)
	}
	if len(last.Criteria) != 1 || len(last.DoD) != 1 {
		t.Fatalf("frame must carry the record's criteria/dod, got criteria=%d dod=%d", len(last.Criteria), len(last.DoD))
	}
	for _, p := range payloads {
		if p.State == goalPillQueued {
			t.Fatal("the queued state must NEVER be emitted from afterGoalRecordWrite (ADR-081 D5/D9)")
		}
	}
}

// TestGoalRecordAccess_ChannelEcho proves FR-020: a channel-routed goal
// receives exactly one formatted record echo per write; a web-routed (or
// unrouted) goal receives none.
func TestGoalRecordAccess_ChannelEcho(t *testing.T) {
	recordJSON := `{"intent":"i","prompt":"p","definition":"Build a tetris clone",` +
		`"criteria":[{"id":"c1","kind":"prose","judgment":"boolean","text":"it renders","author":{"kind":"agent","id":"tester"}}]}`

	t.Run("channel_routed_gets_exactly_one_echo", func(t *testing.T) {
		al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")
		store, sid := newGoalTestSession(t, al, agentInst.ID)
		setActiveGoalRecordless(t, store, sid, "goal-echo-1", "build a game")
		al.recordGoalRouting(sid, "telegram", "chat-99", "sk-1", agentInst.ID)

		access := agentLoopGoalRecordAccess{al: al}
		if err := access.WriteRecord(sid, recordJSON); err != nil {
			t.Fatalf("WriteRecord: %v", err)
		}

		select {
		case msg := <-al.bus.OutboundChan():
			if msg.Channel != "telegram" || msg.ChatID != "chat-99" {
				t.Fatalf("echo routed to %s/%s, want telegram/chat-99", msg.Channel, msg.ChatID)
			}
			if !strings.Contains(msg.Content, "Build a tetris clone") {
				t.Fatalf("echo content = %q, want it to carry the record's definition", msg.Content)
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

	t.Run("web_routed_gets_no_echo", func(t *testing.T) {
		al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")
		store, sid := newGoalTestSession(t, al, agentInst.ID)
		setActiveGoalRecordless(t, store, sid, "goal-echo-2", "build a game")
		al.recordGoalRouting(sid, "webchat", "c1", "sk-2", agentInst.ID)

		access := agentLoopGoalRecordAccess{al: al}
		if err := access.WriteRecord(sid, recordJSON); err != nil {
			t.Fatalf("WriteRecord: %v", err)
		}
		select {
		case msg := <-al.bus.OutboundChan():
			t.Fatalf("a web-routed goal must receive NO text echo (the frame is the surface), got %+v", msg)
		case <-time.After(300 * time.Millisecond):
		}
	})

	t.Run("unrouted_gets_no_echo", func(t *testing.T) {
		al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")
		store, sid := newGoalTestSession(t, al, agentInst.ID)
		setActiveGoalRecordless(t, store, sid, "goal-echo-3", "build a game")
		// No recordGoalRouting call — routeFor returns the zero value.

		access := agentLoopGoalRecordAccess{al: al}
		if err := access.WriteRecord(sid, recordJSON); err != nil {
			t.Fatalf("WriteRecord: %v", err)
		}
		select {
		case msg := <-al.bus.OutboundChan():
			t.Fatalf("an unrouted goal must publish NO echo, got %+v", msg)
		case <-time.After(300 * time.Millisecond):
		}
	})
}

// --- DiffFn (D2 mode:update) -------------------------------------------------

func TestGoalRecordDiffAdapter(t *testing.T) {
	oldRecord := `{"intent":"i","prompt":"p","definition":"Build a game",` +
		`"criteria":[{"id":"c1","kind":"prose","judgment":"boolean","text":"it renders","author":{"kind":"agent","id":"tester"}}]}`
	newRecord := `{"intent":"i","prompt":"p","definition":"Build a multiplayer game",` +
		`"criteria":[{"id":"c1","kind":"prose","judgment":"boolean","text":"it renders","author":{"kind":"agent","id":"tester"}},` +
		`{"id":"c2","kind":"prose","judgment":"boolean","text":"two players can join","author":{"kind":"agent","id":"tester"}}]}`

	summary := goalRecordDiffAdapter(oldRecord, newRecord)
	if !strings.Contains(summary, "+1 added") {
		t.Fatalf("diff summary = %q, want it to report 1 added criterion", summary)
	}
	// Matches set_goal.go's own unwired-seam fallback (localDiffSummary)
	// wording exactly, per formatGoalAmendmentSummary's doc comment.
	wantPrefix := "criteria: +1 added, ~0 changed, -0 dropped; dod:"
	if !strings.HasPrefix(summary, wantPrefix) {
		t.Fatalf("diff summary = %q, want it to start with %q", summary, wantPrefix)
	}
}

// --- FeasibilityFn (D2) ------------------------------------------------------

func TestGoalRecordFeasibilityFn(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")

	t.Run("prose_criterion_always_passes_policy_checks", func(t *testing.T) {
		ctx := tools.WithAgentID(context.Background(), agentInst.ID)
		criteria := []task.AcceptanceCriterion{{
			Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "the game renders correctly",
		}}
		if err := al.goalRecordFeasibilityFn(ctx, criteria); err != nil {
			t.Fatalf("a well-formed prose criterion must pass, got %v", err)
		}
	})

	t.Run("unjudgeable_prose_rejected", func(t *testing.T) {
		ctx := tools.WithAgentID(context.Background(), agentInst.ID)
		criteria := []task.AcceptanceCriterion{{
			Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "   ",
		}}
		if err := al.goalRecordFeasibilityFn(ctx, criteria); err == nil {
			t.Fatal("an empty/unjudgeable criterion must be rejected")
		}
	})

	t.Run("behavior_criterion_denied_by_policy_rejected", func(t *testing.T) {
		// native-agent carries no explicit policy entries in this minimal
		// test config — every tool resolves "deny" fail-closed (CLAUDE.md
		// hard constraint 6), so a behavior criterion naming any tool must
		// be rejected as infeasible.
		ctx := tools.WithAgentID(context.Background(), agentInst.ID)
		minCount := 1
		criteria := []task.AcceptanceCriterion{{
			Kind: task.KindBehavior, Judgment: task.JudgmentQuantitative, Text: "search at least once",
			Behavior: &task.CriterionBehavior{Tool: "search_web", MinCount: &minCount},
		}}
		if err := al.goalRecordFeasibilityFn(ctx, criteria); err == nil {
			t.Fatal("a behavior criterion whose tool is policy-denied must be rejected (FR-111/D9)")
		}
	})

	t.Run("unresolvable_agent_fails_closed", func(t *testing.T) {
		// No tools.WithAgentID at all — the seam must fail CLOSED, matching
		// the compile path's own default (agentFeasibilityContext{}, nil
		// agentInst → EffectiveToolPolicy "deny"/BashReachable false).
		minCount := 1
		criteria := []task.AcceptanceCriterion{{
			Kind: task.KindBehavior, Judgment: task.JudgmentQuantitative, Text: "search at least once",
			Behavior: &task.CriterionBehavior{Tool: "search_web", MinCount: &minCount},
		}}
		if err := al.goalRecordFeasibilityFn(context.Background(), criteria); err == nil {
			t.Fatal("an unresolvable calling agent must fail closed (deny), not silently pass")
		}
	})
}

// --- D7 fast lane: TestFallbackCompile_JudgeModelResolution (test 15) ------

// scriptedCompileProvider scripts a single-shot compile response and
// captures the model string it was called with.
type scriptedCompileProvider struct {
	content   string
	calls     int
	lastModel string
}

func (p *scriptedCompileProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	p.lastModel = model
	return &providers.LLMResponse{Content: p.content}, nil
}

func (p *scriptedCompileProvider) GetDefaultModel() string { return "scripted-compile-mock" }

func TestFallbackCompile_JudgeModelResolution(t *testing.T) {
	t.Run("judge_model_used_not_the_chat_agent", func(t *testing.T) {
		chatProvider := &noCallProvider{t: t}
		al, judgeInst := newGoalLoopTestLoop(t, chatProvider, nil)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")

		judgeProvider := &scriptedCompileProvider{
			content: `{"assessment":{"clarity":"clear"},"definition":"Organize the week",` +
				`"criteria":[{"text":"a plan exists","judgment":"boolean"}],` +
				`"dod":[{"text":"no secrets leak","judgment":"boolean","provenance":"floor"}]}`,
		}
		judgeInst.Provider = judgeProvider
		judgeInst.Model = "judge-fast-model"

		outcome := al.compileGoalIntentLLM(context.Background(), agentInst, nil,
			"organize my week", "sess-d7-1", "", "", false, "")
		if outcome.UsedFallback {
			t.Fatalf("expected the real Judge-backed compile to succeed, got fallback (reason=%q)", outcome.FallbackReason)
		}
		if outcome.Result.Goal == nil || outcome.Result.Goal.Definition != "Organize the week" {
			t.Fatalf("expected the Judge-compiled definition to land, got %+v", outcome.Result)
		}
		if judgeProvider.calls != 1 {
			t.Fatalf("Judge provider calls = %d, want 1", judgeProvider.calls)
		}
		if judgeProvider.lastModel != "judge-fast-model" {
			t.Fatalf("model sent to the provider = %q, want the Judge agent's OWN model, never the chat agent's", judgeProvider.lastModel)
		}
		if chatProvider.calls != 0 {
			t.Fatalf("the chat (goal-bearing) agent's provider must NEVER be called for the compile, got %d calls", chatProvider.calls)
		}
	})

	t.Run("judge_no_provider_degrades_to_deterministic_parser_with_warn", func(t *testing.T) {
		chatProvider := &noCallProvider{t: t}
		al, judgeInst := newGoalLoopTestLoop(t, chatProvider, nil)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")
		judgeInst.Provider = nil

		outcome := al.compileGoalIntentLLM(context.Background(), agentInst, nil,
			"organize my week", "sess-d7-2", "", "", false, "")
		if !outcome.UsedFallback {
			t.Fatal("a Judge agent with no provider must degrade to the deterministic parser")
		}
		if !strings.Contains(outcome.FallbackReason, "judge") {
			t.Fatalf("fallback reason = %q, want it to name the Judge-provider degradation", outcome.FallbackReason)
		}
	})
}

// --- test 16 (emit side): TestGoalStatusFrame_ActiveCarriesRecord ----------

// TestGoalStatusFrame_ActiveCarriesRecord proves FR-019's emission rule at
// afterGoalRecordWrite's own call site: the active-state frame always
// carries criteria/dod, definition only when the record actually has one
// (a marker-shaped record legitimately does not — round-2 B-3), and the
// queued state is never emitted from here.
func TestGoalStatusFrame_ActiveCarriesRecord(t *testing.T) {
	t.Run("full_record_carries_definition_criteria_dod", func(t *testing.T) {
		al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")
		store, sid := newGoalTestSession(t, al, agentInst.ID)
		setActiveGoalRecordless(t, store, sid, "goal-frame-1", "build a game")
		collector, cleanup := newEventCollector(t, al)
		defer cleanup()

		recordJSON := `{"intent":"i","prompt":"p","definition":"Build a tetris clone",` +
			`"criteria":[{"id":"c1","kind":"prose","judgment":"boolean","text":"it renders","author":{"kind":"agent","id":"tester"}}],` +
			`"dod":[{"id":"d1","kind":"prose","judgment":"boolean","provenance":"floor","text":"no secrets","author":{"kind":"agent","id":"tester"}}]}`
		al.afterGoalRecordWrite(sid, recordJSON, "no diff")

		p := waitForGoalStatusPayload(t, collector, sid)
		if p.State != goalPillActive {
			t.Fatalf("state = %q, want active", p.State)
		}
		if p.Definition != "Build a tetris clone" {
			t.Fatalf("definition = %q, want the record's definition", p.Definition)
		}
		if len(p.Criteria) != 1 || len(p.DoD) != 1 {
			t.Fatalf("criteria/dod must be populated, got %d/%d", len(p.Criteria), len(p.DoD))
		}
	})

	t.Run("marker_shaped_record_definition_absent", func(t *testing.T) {
		al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")
		store, sid := newGoalTestSession(t, al, agentInst.ID)
		setActiveGoalRecordless(t, store, sid, "goal-frame-2", "[tests pass]")
		collector, cleanup := newEventCollector(t, al)
		defer cleanup()

		// A marker-path record legitimately carries NO definition — the
		// existing Prompt/Intent fallback (round-2 B-3, FR-019).
		markerRecord := `{"intent":"[tests pass]","prompt":"","criteria":[` +
			`{"id":"c1","kind":"check","judgment":"boolean","text":"tests pass",` +
			`"check":{"command":"go test ./...","expected_exit_code":0},` +
			`"author":{"kind":"agent","id":"tester"}}]}`
		al.afterGoalRecordWrite(sid, markerRecord, "no diff")

		p := waitForGoalStatusPayload(t, collector, sid)
		if p.State != goalPillActive {
			t.Fatalf("state = %q, want active", p.State)
		}
		if p.Definition != "" {
			t.Fatalf("definition = %q, want absent on a marker-shaped record", p.Definition)
		}
		if len(p.Criteria) != 1 {
			t.Fatalf("criteria must still be populated, got %d", len(p.Criteria))
		}
	})
}

// waitForGoalStatusPayload polls the collector for sid's most recent
// goal_status payload, failing the test after a bounded wait — the event
// bus fan-out (newEventCollector) is asynchronous.
func waitForGoalStatusPayload(t *testing.T, c *eventCollector, sid string) GoalStatusChangedPayload {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if payloads := goalStatusPayloadsFor(c, sid); len(payloads) > 0 {
			return payloads[len(payloads)-1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no goal_status payload observed for session %q within the deadline", sid)
	return GoalStatusChangedPayload{}
}

// --- wireGoalToolsForAgent registration -------------------------------------

// TestWireGoalToolsForAgent_RegistersRealSeams proves set_goal is registered
// per-agent with a LIVE (non-nil) access/diff/feasibility seam — not the
// wave-1 metadata-only NewSetGoalTool(nil) instance from
// general_builtin_catalog.go.
func TestWireGoalToolsForAgent_RegistersRealSeams(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	allowGoalToolsPolicy(agentInst)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-wire-1", "build a game")

	tl, ok := agentInst.Tools.Get(tools.SetGoalToolName)
	if !ok {
		t.Fatal("set_goal must be registered on every agent")
	}
	ctx := tools.WithAgentID(context.Background(), agentInst.ID)
	ctx = tools.WithTranscriptSessionID(ctx, sid)
	res := tl.Execute(ctx, map[string]any{
		"definition": "Build a tetris clone",
		"criteria":   []any{map[string]any{"text": "it renders", "judgment": "boolean"}},
	})
	if res.IsError {
		t.Fatalf("set_goal execution failed — the access seam is not really wired: %s", res.ForLLM)
	}

	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if meta.GoalCriteriaJSON == "" {
		t.Fatal("the write must have landed on the real session store — the metadata-only catalog instance would have refused with a nil-store error")
	}
}
