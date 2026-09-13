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
	"encoding/json"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// seedActiveGoalRecord creates and activates a pkg/goal.Store record owned
// by session sid (ADR-086 GOAL-FR-002 owner_kind: session) — this wave's
// own test scaffolding standing in for the activation wiring
// (Store.Create + Goal.Activate) that goal_loop.go's /goal command path
// will perform once wave E8/E12 re-points it (joint delivery plan §5's
// E4 → E8 chain for this file: this wave owns ONLY WriteRecord/
// ReadGoalState, not activation). A nil dod defaults to a single
// floor-provenance item so goal.New's DoD-non-empty invariant (D11, schema
// minItems: 1) is always satisfied; nil criteria leaves the record in the
// ADR-081 D1 legal-transient "active, no criteria registered yet" state,
// mirroring setActiveGoalRecordless's (goal_first_move_test.go, wave E12)
// old session-meta-only seeding for that same state.
func seedActiveGoalRecord(t *testing.T, sid, prompt string, criteria, dod []task.AcceptanceCriterion) *goal.Goal {
	t.Helper()
	if len(dod) == 0 {
		dod = []task.AcceptanceCriterion{{
			Kind: task.KindProse, Judgment: task.JudgmentBoolean, Provenance: task.ProvenanceFloor,
			Text: "no secrets are leaked", Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "test-seed"},
		}}
	}
	gstore := goal.NewStore(config.OmnipusHomeDir())
	g, err := goal.New(generated.GoalOwnerKindSession, sid, generated.ChatCompiled, prompt, "", criteria, dod, 10, time.Now().UTC())
	if err != nil {
		t.Fatalf("seedActiveGoalRecord: New: %v", err)
	}
	if createErr := gstore.Create(g); createErr != nil {
		t.Fatalf("seedActiveGoalRecord: Create: %v", createErr)
	}
	updated, err := gstore.Update(g.GoalID, func(cur *goal.Goal) error {
		return cur.Activate(sid, time.Now().UTC())
	})
	if err != nil {
		t.Fatalf("seedActiveGoalRecord: Activate: %v", err)
	}
	return updated
}

// --- GoalRecordAccess (D2) --------------------------------------------------

func TestGoalRecordAccess_ReadWrite(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")

	access := agentLoopGoalRecordAccess{al: al}

	t.Run("read_no_active_goal", func(t *testing.T) {
		_, sid := newGoalTestSession(t, al, agentInst.ID)
		goalID, cond, rec, err := access.ReadGoalState(sid)
		if err != nil {
			t.Fatal(err)
		}
		if goalID != "" || cond != "" || rec != "" {
			t.Fatalf("a fresh session must read empty goal id/condition/record, got %q/%q/%q", goalID, cond, rec)
		}
	})

	// Each subtest below mints its OWN session: a pkg/goal.Store session-owned
	// goal has no one-active-per-owner uniqueness constraint the way a
	// task-owned one does (GetActiveByOwner errors loudly on more than one
	// active match for the same owner — see predicate.go's own doc comment),
	// so two subtests seeding against the SAME sid would collide.

	t.Run("read_active_goal_empty_record", func(t *testing.T) {
		_, sid := newGoalTestSession(t, al, agentInst.ID)
		seeded := seedActiveGoalRecord(t, sid, "build a game", nil, nil)
		goalID, cond, rec, err := access.ReadGoalState(sid)
		if err != nil {
			t.Fatal(err)
		}
		if goalID != seeded.GoalID {
			t.Fatalf("goal id = %q, want the minted id %q (Store.Create mints it, GOAL-FR-002)", goalID, seeded.GoalID)
		}
		if cond != "build a game" {
			t.Fatalf("condition = %q, want the active condition (Goal.Prompt)", cond)
		}
		if rec != "" {
			t.Fatalf("record must still read empty (D1's transient state — no criteria registered yet), got %q", rec)
		}
	})

	t.Run("write_persists_and_readable_back", func(t *testing.T) {
		_, sid := newGoalTestSession(t, al, agentInst.ID)
		seedActiveGoalRecord(t, sid, "build a game", nil, nil)
		recordJSON := `{"intent":"i","prompt":"p","definition":"Build a tetris clone",` +
			`"criteria":[{"id":"c1","kind":"prose","judgment":"boolean","text":"it renders","author":{"kind":"agent","id":"tester"}}],` +
			`"dod":[{"id":"d1","kind":"prose","judgment":"boolean","provenance":"floor","text":"no secrets","author":{"kind":"agent","id":"tester"}}]}`
		if err := access.WriteRecord(sid, recordJSON); err != nil {
			t.Fatalf("WriteRecord: %v", err)
		}
		_, _, rec, err := access.ReadGoalState(sid)
		if err != nil {
			t.Fatal(err)
		}
		// GOAL-FR-003: the STORE persists typed lists, never a serialised
		// string — a byte-identical round-trip is no longer the contract
		// (the store re-marshals from its own typed fields). Assert the
		// values that matter instead.
		var got goalSeamRecord
		if uErr := json.Unmarshal([]byte(rec), &got); uErr != nil {
			t.Fatalf("ReadGoalState returned an unparseable record: %v (%q)", uErr, rec)
		}
		if got.Definition != "Build a tetris clone" {
			t.Fatalf("definition = %q, want %q", got.Definition, "Build a tetris clone")
		}
		if len(got.Criteria) != 1 || got.Criteria[0].Text != "it renders" {
			t.Fatalf("criteria read back = %+v, want one criterion with text %q", got.Criteria, "it renders")
		}
		if len(got.DoD) != 1 || got.DoD[0].Text != "no secrets" {
			t.Fatalf("dod read back = %+v, want one dod item with text %q", got.DoD, "no secrets")
		}
	})

	t.Run("read_unknown_session_errors", func(t *testing.T) {
		if _, _, _, err := access.ReadGoalState("no-such-session"); err == nil {
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
	_, sid := newGoalTestSession(t, al, agentInst.ID)
	// ADR-086 (wave S6): ONE active record per session, and this is it.
	// The setActiveGoalRecordless call that used to sit above this line is
	// gone. It was written when the two halves were independent — it seeded
	// SESSION META for afterGoalRecordWrite's own read, while
	// seedActiveGoalRecord seeded the pkg/goal record for WriteRecord. S6
	// deleted the session-meta half of the goal entirely, so
	// setActiveGoalRecordless now CREATES AND ACTIVATES A REAL RECORD of its
	// own — and the pair left this session owning TWO active goals, which
	// activeGoalForSession reports at Warn and resolves in favour of the
	// OLDER one (the recordless one), so every assertion below then read a
	// record WriteRecord had never touched. Same failure and same fix as
	// goal_terminal_transition_test.go's seedCriteriaOntoActiveGoal.
	seeded := seedActiveGoalRecord(t, sid, "build a game", nil, nil)

	// Seed a nonzero push streak directly on the store record — WriteRecord
	// must reset it (GOAL-FR-004's relocated FR-014b counter).
	goalStore := goal.NewStore(config.OmnipusHomeDir())
	if _, err := goalStore.Update(seeded.GoalID, func(cur *goal.Goal) error {
		cur.ZeroOutputPushes = 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := goalStore.Get(seeded.GoalID)
	if err != nil {
		t.Fatal(err)
	}
	if before.ZeroOutputPushes != 2 {
		t.Fatalf("seed failed: ZeroOutputPushes = %d, want 2", before.ZeroOutputPushes)
	}
	beforeActivity := before.LastActivityAt
	time.Sleep(2 * time.Millisecond) // guarantee a distinguishable LastActivityAt bump below

	collector, cleanup := newEventCollector(t, al)
	defer cleanup()

	recordJSON := `{"intent":"i","prompt":"p","definition":"Build a tetris clone",` +
		`"criteria":[{"id":"c1","kind":"prose","judgment":"boolean","text":"it renders and accepts input","author":{"kind":"agent","id":"tester"}}],` +
		`"dod":[{"id":"d1","kind":"prose","judgment":"boolean","provenance":"floor","text":"no secrets","author":{"kind":"agent","id":"tester"}}]}`
	access := agentLoopGoalRecordAccess{al: al}
	if writeErr := access.WriteRecord(sid, recordJSON); writeErr != nil {
		t.Fatalf("WriteRecord: %v", writeErr)
	}

	after, err := goalStore.Get(seeded.GoalID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ZeroOutputPushes != 0 {
		t.Fatalf("ZeroOutputPushes = %d, want reset to 0 on a successful write", after.ZeroOutputPushes)
	}
	if !after.LastActivityAt.After(beforeActivity) {
		t.Fatalf("LastActivityAt must be bumped by a successful write, got before=%v after=%v", beforeActivity, after.LastActivityAt)
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
		_, sid := newGoalTestSession(t, al, agentInst.ID)
		// One active record per session (ADR-086) — see
		// TestGoalRecordAccess_WriteRecord_SideEffects' own note above for why
		// the setActiveGoalRecordless call that used to pair with this one is
		// gone.
		seeded := seedActiveGoalRecord(t, sid, "build a game", nil, nil)
		al.recordGoalRouting(sid, seeded.GoalID, "telegram", "chat-99", "sk-1", agentInst.ID)

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
		_, sid := newGoalTestSession(t, al, agentInst.ID)
		seeded := seedActiveGoalRecord(t, sid, "build a game", nil, nil)
		al.recordGoalRouting(sid, seeded.GoalID, "webchat", "c1", "sk-2", agentInst.ID)

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
		_, sid := newGoalTestSession(t, al, agentInst.ID)
		seedActiveGoalRecord(t, sid, "build a game", nil, nil)
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

// --- Marker-path activation/restate route through the SAME post-write path
// set_goal uses (review-round-1 finding #8) -------------------------------

// TestGoalMarkerActivation_EmitsCriteriaCarryingFrame proves a fresh
// marker-only `/goal [tests pass]` activation (applyGoalCommandPrompt's
// marker-compile branch, goal_loop.go) emits its goal_status frame through
// afterGoalRecordWrite — carrying the compiled criteria/dod ladder — rather
// than the bare emitGoalStatusFrame this call site used before the fix
// (which carried no criteria at all, unlike set_goal's own writes).
func TestGoalMarkerActivation_EmitsCriteriaCarryingFrame(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	// The [tests pass] marker synthesizes a KindCheck criterion, which the
	// compile-time feasibility gate rejects unless bash is policy-reachable
	// (FR-111/FR-112) — this test's own concern is frame emission, not
	// feasibility, so grant it explicitly (allowBashPolicy, judge_test.go).
	allowBashPolicy(agentInst)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}

	collector, cleanup := newEventCollector(t, al)
	defer cleanup()

	// A goalless session's fresh /goal with ONLY a marker (no prose) takes
	// the deterministic marker-compile branch (goalIntentNeedsLLMCompile
	// returns false), never the D1 instant-activation branch — the branch
	// that carries a real compiled criteria ladder from the moment it
	// activates.
	matched, handled, _ := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal [tests pass]", UserInitiated: true}, agentInst, &opts)
	if !matched || handled {
		t.Fatalf("marker activation: matched=%v handled=%v, want matched=true handled=false", matched, handled)
	}
	if goalRecordCompiledJSON(goalRecordForSession(t, sid)) == "" {
		t.Fatal("precondition: a marker-only activation must compile a non-empty record immediately")
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
		t.Fatal("marker activation must emit a goal_status frame")
	}
	last := payloads[len(payloads)-1]
	if last.State != goalPillActive {
		t.Fatalf("frame state = %q, want %q", last.State, goalPillActive)
	}
	if len(last.Criteria) == 0 {
		t.Fatal("finding #8: the marker-activation frame must carry the compiled criteria ladder, got none")
	}
	if len(last.DoD) == 0 {
		t.Fatal("finding #8: the marker-activation frame must carry the DoD (the built-in floor, absent an explicit one), got none")
	}
}

// TestGoalMarkerActivation_ChannelOrigin_GetsOneFormattedEcho proves FR-020
// now reaches the marker-activation path too: a channel-routed marker
// `/goal` gets exactly one formatted record echo, same as a set_goal-authored
// write — the bare emitGoalStatusFrame this call site used before the fix
// never triggered a channel echo at all.
func TestGoalMarkerActivation_ChannelOrigin_GetsOneFormattedEcho(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	allowBashPolicy(agentInst) // see the sibling test's comment for why
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "telegram", ChatID: "chat-77", SessionKey: "sk-telegram", UserInitiated: true,
	}

	matched, handled, _ := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal [tests pass]", UserInitiated: true}, agentInst, &opts)
	if !matched || handled {
		t.Fatalf("marker activation: matched=%v handled=%v, want matched=true handled=false", matched, handled)
	}
	if goalRecordCompiledJSON(goalRecordForSession(t, sid)) == "" {
		t.Fatal("precondition: a marker-only activation must compile a non-empty record immediately")
	}

	select {
	case msg := <-al.bus.OutboundChan():
		if msg.Channel != "telegram" || msg.ChatID != "chat-77" {
			t.Fatalf("echo routed to %s/%s, want telegram/chat-77", msg.Channel, msg.ChatID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("finding #8: a channel-origin marker activation must get exactly one formatted echo, got none")
	}
	select {
	case msg := <-al.bus.OutboundChan():
		t.Fatalf("expected exactly ONE echo, got a second: %+v", msg)
	case <-time.After(200 * time.Millisecond):
	}
}

// --- EmitGoalStatusRehydrate (WS reattach rehydration, item 14) ------------

// TestEmitGoalStatusRehydrate_RegisteredGoal_DeliversRecordCarryingFrame is
// item 14's own test: on a session carrying an active goal with a
// REGISTERED record, EmitGoalStatusRehydrate emits exactly one goal_status
// frame carrying the definition/criteria/dod ladder — proving the gateway's
// WS attach path (pkg/gateway/websocket.go's handleAttachSession, which
// calls this once per attach) has a real rehydration path for the record
// card an SPA reload otherwise loses (goal_status is a pure live push,
// never a persisted transcript entry).
func TestEmitGoalStatusRehydrate_RegisteredGoal_DeliversRecordCarryingFrame(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-rehydrate-1", "build a game")

	// ADR-086: the registered record is the goal's OWN typed Criteria/DoD
	// (GOAL-FR-003), not the retired GoalCriteriaJSON session-meta string.
	if _, uerr := goal.NewStore(config.OmnipusHomeDir()).Update("goal-rehydrate-1", func(cur *goal.Goal) error {
		cur.Definition = "Build a tetris clone"
		if serr := cur.SetCriteria([]task.AcceptanceCriterion{{
			ID: "c1", Kind: task.KindProse, Judgment: task.JudgmentBoolean,
			Text:   "it renders and accepts input",
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "tester"},
		}}, time.Now().UTC()); serr != nil {
			return serr
		}
		return cur.SetDoD([]task.AcceptanceCriterion{{
			ID: "d1", Kind: task.KindProse, Judgment: task.JudgmentBoolean,
			Provenance: task.ProvenanceFloor, Text: "no secrets",
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "tester"},
		}}, time.Now().UTC())
	}); uerr != nil {
		t.Fatal(uerr)
	}

	// Simulate the SPA reload: a fresh WS connection attaches (represented
	// here by starting a fresh event collector — no earlier live emission
	// exists in its buffer) and the gateway calls EmitGoalStatusRehydrate
	// exactly once, mirroring handleAttachSession's own call site.
	collector, cleanup := newEventCollector(t, al)
	defer cleanup()

	if ok := al.EmitGoalStatusRehydrate(sid); !ok {
		t.Fatal("EmitGoalStatusRehydrate must report true for an active goal with a registered record")
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
	if len(payloads) != 1 {
		t.Fatalf("EmitGoalStatusRehydrate must emit exactly ONE goal_status frame, got %d", len(payloads))
	}
	got := payloads[0]
	if got.State != goalPillActive {
		t.Fatalf("rehydrate frame state = %q, want %q", got.State, goalPillActive)
	}
	if got.Definition != "Build a tetris clone" {
		t.Fatalf("rehydrate frame definition = %q, want the record's definition", got.Definition)
	}
	if len(got.Criteria) != 1 || len(got.DoD) != 1 {
		t.Fatalf("rehydrate frame must carry the record's criteria/dod, got criteria=%d dod=%d", len(got.Criteria), len(got.DoD))
	}
}

// TestEmitGoalStatusRehydrate_NoRecord_StillReemitsActivationFrame proves
// the D1 legal-transient empty-record state (active goal, GoalCriteriaJSON
// still empty — the working agent hasn't called set_goal yet) is NOT a
// no-op (2026-09-08 fix, frontend wave GX-C): a reload landing inside this
// window still gets a criteria-less `active` goal_status frame — the SAME
// shape activateInstantGoal itself emits at activation — so the SPA's
// goal-acknowledgement line (chat.ts's case 'goal_status') and goal-aware
// thinking indicator survive a reload that happens before any record has
// been written. Before this fix EmitGoalStatusRehydrate returned false here
// and nothing at all reached a reattaching connection during exactly the
// window the operator's "17 minutes of a silent spinner" bug report
// covers.
func TestEmitGoalStatusRehydrate_NoRecord_StillReemitsActivationFrame(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-rehydrate-2", "build a game") // GoalCriteriaJSON left empty

	collector, cleanup := newEventCollector(t, al)
	defer cleanup()

	if ok := al.EmitGoalStatusRehydrate(sid); !ok {
		t.Fatal("EmitGoalStatusRehydrate must report true for an active goal even with no record registered yet")
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
	if len(payloads) != 1 {
		t.Fatalf("EmitGoalStatusRehydrate must emit exactly ONE goal_status frame, got %d", len(payloads))
	}
	got := payloads[0]
	if got.State != goalPillActive {
		t.Fatalf("rehydrate frame state = %q, want %q", got.State, goalPillActive)
	}
	if got.GoalID != "goal-rehydrate-2" {
		t.Fatalf("rehydrate frame goal_id = %q, want %q", got.GoalID, "goal-rehydrate-2")
	}
	if len(got.Criteria) != 0 || len(got.DoD) != 0 {
		t.Fatalf("rehydrate frame for an unregistered record must carry NO criteria/dod, got criteria=%d dod=%d", len(got.Criteria), len(got.DoD))
	}
}

// TestEmitGoalStatusRehydrate_NoActiveGoal_IsANoOp proves a goalless (or
// cleared/terminal) session is correctly a no-op — GoalCondition == "" is
// both states, so this also proves terminal goals never get a stray
// rehydrate frame.
func TestEmitGoalStatusRehydrate_NoActiveGoal_IsANoOp(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	_, sid := newGoalTestSession(t, al, agentInst.ID) // no active goal at all

	if ok := al.EmitGoalStatusRehydrate(sid); ok {
		t.Fatal("EmitGoalStatusRehydrate must be a no-op (false) for a session with no active goal")
	}
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
	_, sid := newGoalTestSession(t, al, agentInst.ID)
	// One active record per session (ADR-086) — see
	// TestGoalRecordAccess_WriteRecord_SideEffects' own note for the full
	// reason the paired setActiveGoalRecordless call is gone.
	seeded := seedActiveGoalRecord(t, sid, "build a game", nil, nil)

	tl, ok := agentInst.Tools.Get(tools.SetGoalToolName)
	if !ok {
		t.Fatal("set_goal must be registered on every agent")
	}
	ctx := tools.WithAgentID(context.Background(), agentInst.ID)
	ctx = tools.WithTranscriptSessionID(ctx, sid)
	res := tl.Execute(ctx, map[string]any{
		"definition": "Build a tetris clone",
		"criteria":   []any{map[string]any{"text": "it renders", "judgment": "boolean"}},
		// An EXPLICIT dod, deliberately: set_goal.go's own floor-DoD
		// fallback (setGoalFloorDoD, used whenever dod is omitted) mints
		// items carrying the exact "goal-dod-floor-" id prefix
		// pkg/goal.IsReservedCriterionID refuses to persist on ANY goal
		// record (GOAL-FR-007) — a real cross-wave collision this seam
		// re-point surfaces (see this wave's report), not something this
		// test is exercising. Supplying our own dod here sidesteps it so
		// this test proves what it says it proves: the seam is really
		// wired, not the floor-DoD/reserved-id interaction.
		"dod": []any{map[string]any{"text": "no unrelated secrets are exposed", "judgment": "boolean", "provenance": "stated"}},
	})
	if res.IsError {
		t.Fatalf("set_goal execution failed — the access seam is not really wired: %s", res.ForLLM)
	}

	goalStore := goal.NewStore(config.OmnipusHomeDir())
	updated, err := goalStore.Get(seeded.GoalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Criteria) == 0 {
		t.Fatal("the write must have landed on the real pkg/goal.Store record — the metadata-only catalog instance would have refused with a nil-store error")
	}
}
