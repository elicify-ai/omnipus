// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_activation_test.go covers ADR-081 D1 (instant activation), D5/FR-001
// (restate/steering), and D9/FR-022 (confirm-gate deletion) at the
// applyGoalCommandPrompt unit level — the wave-1b (W1b) regression suite.
// Traces to: docs/internal/architecture/ADR-081-work-first-goal-flow.md,
// docs/internal/specs/work-first-goal-flow-spec.md tests 4-6, 19.
package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// noCallProvider is a scripted LLM provider that fails the test the instant
// Chat is invoked — the direct proof for C-1 ("the activation path issues
// ZERO LLM calls before the first working request") beyond a mere call-count
// check: a stray call fails AT the call site, not just in a later assertion.
type noCallProvider struct {
	t     *testing.T
	calls int
}

func (p *noCallProvider) Chat(
	context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	p.t.Errorf("unexpected LLM call #%d — the goal activation path must make ZERO calls before the first working request (C-1)", p.calls)
	return &providers.LLMResponse{Content: "unexpected"}, nil
}

func (p *noCallProvider) GetDefaultModel() string { return "no-call-model" }

// TestGoalActivation_InstantProsePath proves ADR-081 D1/US-1 (test 4):
// `/goal <prose>` on a goalless session activates the record immediately —
// GoalID minted, GoalCondition set to the raw intent, GoalCriteriaJSON
// EMPTY (the agent authors it later via set_goal), the ADMISSION GATE runs
// exactly once and BEFORE state is written (FR-003 — collapsing the retired
// two-step courtesy-then-authoritative gate into one; a status-frame
// snapshot read of the SAME live count right after the write, via
// activeLoopsSnapshot, is a separate, pre-existing, non-gating use of the
// same Admit method and is accounted for explicitly below, not conflated
// with the gate), zero LLM calls happen (C-1), and a restate on the now-
// active goal keeps the SAME GoalID (FR-001) without re-Admitting.
func TestGoalActivation_InstantProsePath(t *testing.T) {
	provider := &noCallProvider{t: t}
	al, _ := newGoalLoopTestLoop(t, provider, nil)

	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}

	var admitCalls int
	var gateSawActiveGoal bool // true only if the FIRST Admit call ran AFTER state was written (a FR-003 violation)
	planStore := plan.New(t.TempDir())
	pe := NewPlanEngine(al, planStore, nil, nil)
	pe.RegisterActiveCounter("goal", func() (int, error) {
		admitCalls++
		if admitCalls == 1 {
			m, _ := store.GetMeta(sid)
			gateSawActiveGoal = m != nil && m.GoalCondition != ""
		}
		return 0, nil
	})
	al.SetPlanEngine(pe)

	matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal build me a tetris game", UserInitiated: true}, agentInst, &opts)
	if !matched || handled {
		t.Fatalf("matched=%v handled=%v, want matched=true handled=false (instant activation continues to the LLM)", matched, handled)
	}
	if reply != "" {
		t.Fatalf("instant activation must not answer synchronously, got reply %q", reply)
	}
	if opts.UserMessage != "build me a tetris game" {
		t.Fatalf("opts.UserMessage = %q, want the raw intent", opts.UserMessage)
	}

	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if meta.GoalID == "" {
		t.Fatal("instant activation must mint a GoalID")
	}
	if meta.GoalCondition != "build me a tetris game" {
		t.Fatalf("GoalCondition = %q, want the raw intent", meta.GoalCondition)
	}
	if meta.GoalCriteriaJSON != "" {
		t.Fatalf("GoalCriteriaJSON must start EMPTY (D3's legal transient state), got %q", meta.GoalCriteriaJSON)
	}
	if meta.GoalRoundsUsed != 0 {
		t.Fatalf("GoalRoundsUsed = %d, want 0", meta.GoalRoundsUsed)
	}
	if meta.GoalMaxRounds != config.DefaultGoalMaxRounds {
		t.Fatalf("GoalMaxRounds = %d, want the default", meta.GoalMaxRounds)
	}
	if meta.GoalStartedAt == "" || meta.GoalLastActivityAt == "" {
		t.Fatal("GoalStartedAt/GoalLastActivityAt must be stamped on activation")
	}
	if gateSawActiveGoal {
		t.Fatal("the admission gate's Admit call must run BEFORE state is written (FR-003), but the goal was already active")
	}
	if admitCalls != 2 {
		t.Fatalf("want exactly 2 Admit invocations for one activation (1 gating check before the write + "+
			"1 status-frame snapshot read after — see this test's doc comment), got %d", admitCalls)
	}
	if provider.calls != 0 {
		t.Fatalf("instant activation must make ZERO LLM calls (C-1), got %d", provider.calls)
	}

	// A prose restate on the now-active goal: rewrites the prompt, does NOT
	// mint a new GoalID, does NOT re-Admit at all (no status frame is
	// emitted for a prose restate either), and still makes zero LLM calls.
	firstID := meta.GoalID
	admitCalls = 0
	matched, handled, reply = al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal actually make it snake instead", UserInitiated: true}, agentInst, &opts)
	if !matched || handled {
		t.Fatalf("restate: matched=%v handled=%v, want matched=true handled=false", matched, handled)
	}
	if reply != "" {
		t.Fatalf("restate must not answer synchronously, got reply %q", reply)
	}
	if opts.UserMessage != "actually make it snake instead" {
		t.Fatalf("restate opts.UserMessage = %q, want the new intent", opts.UserMessage)
	}
	afterRestate, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if afterRestate.GoalID != firstID {
		t.Fatalf("restate must NOT mint a new GoalID (FR-001), got %q want %q", afterRestate.GoalID, firstID)
	}
	if admitCalls != 0 {
		t.Fatalf("a restate must not re-Admit (only a fresh activation admits), got %d calls", admitCalls)
	}
	if provider.calls != 0 {
		t.Fatalf("restate must also make ZERO LLM calls, got %d", provider.calls)
	}
}

// TestGoalActivation_InstantProsePath_CapRefusal proves US-1/A3 (test 4):
// the active-loop cap refuses cleanly, once, at submission — nothing
// half-starts.
func TestGoalActivation_InstantProsePath_CapRefusal(t *testing.T) {
	provider := &noCallProvider{t: t}
	al, _ := newGoalLoopTestLoop(t, provider, func(cfg *config.Config) {
		cfg.Planning.GlobalActiveLoopCap = 1
	})
	planStore := plan.New(t.TempDir())
	pe := NewPlanEngine(al, planStore, nil, nil)
	pe.RegisterActiveCounter("goal", func() (int, error) { return 1, nil }) // cap already full
	al.SetPlanEngine(pe)

	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}

	matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal a second goal", UserInitiated: true}, agentInst, &opts)
	if !matched || !handled {
		t.Fatalf("cap refusal: matched=%v handled=%v, want both true (refusal answers synchronously)", matched, handled)
	}
	if !strings.Contains(reply, "active loops") {
		t.Fatalf("cap refusal reply = %q, want a cap-reached message", reply)
	}
	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if meta.GoalCondition != "" {
		t.Fatal("no goal state may be created when admission is refused")
	}
	if provider.calls != 0 {
		t.Fatalf("cap refusal must make ZERO LLM calls, got %d", provider.calls)
	}
}

// TestGoalActivation_MarkerPathPinned proves FR-002 (test 5): a marker-only
// `/goal` on a goalless session stays byte-identical — deterministic
// compile, immediate activation, criteria populated ALREADY (unlike the
// prose path), zero LLM calls.
func TestGoalActivation_MarkerPathPinned(t *testing.T) {
	provider := &noCallProvider{t: t}
	al, _ := newGoalLoopTestLoop(t, provider, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	allowBashPolicy(agentInst)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}

	matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal [tests pass]", UserInitiated: true}, agentInst, &opts)
	if !matched || handled {
		t.Fatalf("marker path: matched=%v handled=%v, want matched=true handled=false", matched, handled)
	}
	if reply != "" {
		t.Fatalf("marker path must not answer synchronously, got reply %q", reply)
	}
	if opts.UserMessage == "" {
		t.Fatal("marker path must rewrite opts.UserMessage into round 1")
	}

	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if meta.GoalID == "" {
		t.Fatal("marker activation must mint a GoalID")
	}
	if meta.GoalCriteriaJSON == "" {
		t.Fatal("marker path activates with criteria ALREADY compiled (unlike the prose path's empty start)")
	}
	compiled := loadCompiledGoal(meta.GoalCriteriaJSON)
	if compiled == nil || len(compiled.Criteria) == 0 {
		t.Fatalf("marker path must compile real criteria, got %+v", compiled)
	}
	if compiled.Criteria[0].Kind != "check" {
		t.Fatalf("want a KindCheck criterion from [tests pass], got %+v", compiled.Criteria[0])
	}
	if provider.calls != 0 {
		t.Fatalf("marker path must make ZERO LLM calls, got %d", provider.calls)
	}
}

// TestGoalRestate_ActiveGoal proves US-5 (test 6): a prose restate on an
// active goal rewrites the working prompt AND patches the durable
// GoalCondition to the new intent (review-round-1 finding #9 — keeper
// prompts, `/goal status`, and the D7 fallback compile all cite
// GoalCondition, so leaving it stale would have them cite the SUPERSEDED
// pre-restate intent forever) without touching the compiled
// GoalCriteriaJSON record or minting a new GoalID; a marker-only restate
// updates the record deterministically (zero LLM calls); and the
// feasibility veto still applies on a vetoed marker restate (fail-closed —
// the record is untouched).
func TestGoalRestate_ActiveGoal(t *testing.T) {
	t.Run("prose_restate_rewrites_prompt_and_condition_leaves_compiled_record_untouched", func(t *testing.T) {
		provider := &noCallProvider{t: t}
		al, _ := newGoalLoopTestLoop(t, provider, nil)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")
		store, sid := newGoalTestSession(t, al, agentInst.ID)
		opts := processOptions{
			TranscriptStore: store, TranscriptSessionID: sid,
			Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
		}
		al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal build a game", UserInitiated: true}, agentInst, &opts)
		before, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}

		matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal actually make it multiplayer", UserInitiated: true}, agentInst, &opts)
		if !matched || handled {
			t.Fatalf("matched=%v handled=%v, want matched=true handled=false", matched, handled)
		}
		if reply != "" {
			t.Fatalf("prose restate must not answer synchronously, got %q", reply)
		}
		if opts.UserMessage != "actually make it multiplayer" {
			t.Fatalf("opts.UserMessage = %q", opts.UserMessage)
		}
		after, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}
		if after.GoalID != before.GoalID {
			t.Fatalf("prose restate must not mint a new GoalID, got %q want %q", after.GoalID, before.GoalID)
		}
		if after.GoalCondition != "actually make it multiplayer" {
			t.Fatalf("finding #9: prose restate must patch the durable GoalCondition to the new intent, "+
				"got %q want %q", after.GoalCondition, "actually make it multiplayer")
		}
		if after.GoalCriteriaJSON != before.GoalCriteriaJSON {
			t.Fatalf("prose restate must not touch the COMPILED record (the agent updates it via set_goal), got %q want %q",
				after.GoalCriteriaJSON, before.GoalCriteriaJSON)
		}
		if provider.calls != 0 {
			t.Fatalf("prose restate must make ZERO LLM calls, got %d", provider.calls)
		}
	})

	t.Run("marker_restate_updates_record_deterministically", func(t *testing.T) {
		provider := &noCallProvider{t: t}
		al, _ := newGoalLoopTestLoop(t, provider, nil)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")
		agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{"search_web": config.ToolPolicyAllow},
		})
		store, sid := newGoalTestSession(t, al, agentInst.ID)
		opts := processOptions{
			TranscriptStore: store, TranscriptSessionID: sid,
			Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
		}
		al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal [search: 2]", UserInitiated: true}, agentInst, &opts)
		before, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}

		matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal [search: 5]", UserInitiated: true}, agentInst, &opts)
		if !matched || !handled {
			t.Fatalf("marker restate: matched=%v handled=%v, want both true (answers synchronously)", matched, handled)
		}
		if reply == "" {
			t.Fatal("marker restate must reply with the updated goal summary")
		}
		after, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}
		if after.GoalID != before.GoalID {
			t.Fatalf("marker restate must not mint a new GoalID, got %q want %q", after.GoalID, before.GoalID)
		}
		if after.GoalCriteriaJSON == before.GoalCriteriaJSON {
			t.Fatal("marker restate must update the compiled criteria")
		}
		compiled := loadCompiledGoal(after.GoalCriteriaJSON)
		if compiled == nil || len(compiled.Criteria) == 0 || compiled.Criteria[0].Behavior == nil ||
			compiled.Criteria[0].Behavior.EffectiveMinCount() != 5 {
			t.Fatalf("marker restate must land the NEW min_count=5, got %+v", compiled)
		}
		if provider.calls != 0 {
			t.Fatalf("marker restate must make ZERO LLM calls, got %d", provider.calls)
		}
	})

	t.Run("marker_restate_feasibility_veto_leaves_record_unchanged", func(t *testing.T) {
		al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")
		agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{"search_web": config.ToolPolicyAllow},
		})
		store, sid := newGoalTestSession(t, al, agentInst.ID)
		opts := processOptions{
			TranscriptStore: store, TranscriptSessionID: sid,
			Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
		}
		al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal [search: 2]", UserInitiated: true}, agentInst, &opts)
		before, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}

		// bash (hence [tests pass]) is NOT granted — default-deny fail-closed
		// (CLAUDE.md hard constraint 6) — so the feasibility veto must reject
		// this restate outright, leaving the record untouched.
		matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal [tests pass]", UserInitiated: true}, agentInst, &opts)
		if !matched || !handled {
			t.Fatalf("vetoed restate: matched=%v handled=%v, want both true (rejection answers synchronously)", matched, handled)
		}
		if !strings.Contains(reply, "rejected") {
			t.Fatalf("vetoed restate reply = %q, want a rejection message", reply)
		}
		after, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}
		if after.GoalCriteriaJSON != before.GoalCriteriaJSON {
			t.Fatal("a vetoed restate must NOT change the persisted record (fail-closed, FR-111/D9)")
		}
		if after.GoalID != before.GoalID {
			t.Fatal("a vetoed restate must not touch the GoalID either")
		}
	})
}

// TestConfirmInert proves ADR-081 D9/FR-022/US-9 (test 19): bare "confirm"
// is ordinary chat (nothing intercepts it — the confirm-gate hook is
// deleted in full); `/goal confirm` is an informative no-op, never
// activating a goal literally named "confirm" (grill B3); and `/goal
// status` (FR-029) shows the record summary once one exists, with none of
// the retired pending-draft/confirm phrasing.
func TestConfirmInert(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}

	t.Run("bare_confirm_is_ordinary_chat", func(t *testing.T) {
		matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "confirm", UserInitiated: true}, agentInst, &opts)
		if matched || handled || reply != "" {
			t.Fatalf("bare 'confirm' must not match the /goal hook at all, got matched=%v handled=%v reply=%q",
				matched, handled, reply)
		}
		meta, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}
		if meta.GoalCondition != "" {
			t.Fatal("bare 'confirm' must not create or change any goal state")
		}
	})

	t.Run("goal_confirm_command_is_informative_noop", func(t *testing.T) {
		matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal confirm", UserInitiated: true}, agentInst, &opts)
		if !matched || !handled {
			t.Fatalf("/goal confirm: matched=%v handled=%v, want both true (informative notice, no LLM call)", matched, handled)
		}
		if !strings.Contains(reply, "activate immediately") {
			t.Fatalf("/goal confirm reply = %q, want the instant-activation notice", reply)
		}
		meta, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}
		if meta.GoalCondition != "" {
			t.Fatal("/goal confirm must NEVER activate a goal literally named 'confirm' (grill B3)")
		}
	})

	t.Run("goal_status_shows_record_summary_no_pending_phrasing", func(t *testing.T) {
		al2, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
		agentInst2, _ := al2.GetRegistry().GetAgent("native-agent")
		allowBashPolicy(agentInst2)
		store2, sid2 := newGoalTestSession(t, al2, agentInst2.ID)
		opts2 := processOptions{
			TranscriptStore: store2, TranscriptSessionID: sid2,
			Channel: "webchat", ChatID: "c2", SessionKey: "sk2", UserInitiated: true,
		}
		al2.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal [tests pass]", UserInitiated: true}, agentInst2, &opts2)

		matched, handled, reply := al2.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal", UserInitiated: true}, agentInst2, &opts2)
		if !matched || !handled {
			t.Fatalf("/goal status: matched=%v handled=%v", matched, handled)
		}
		for _, want := range []string{"Rounds: 0/", "Done when", "all tests pass"} {
			if !strings.Contains(reply, want) {
				t.Fatalf("status reply = %q, want to contain %q", reply, want)
			}
		}
		for _, unwanted := range []string{"pending your confirmation", "waiting for your answer", "Reply **confirm**"} {
			if strings.Contains(reply, unwanted) {
				t.Fatalf("status reply must not contain retired pending-draft/confirm phrasing %q, got %q", unwanted, reply)
			}
		}
	})
}
