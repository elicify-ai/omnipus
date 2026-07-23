// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Design-conformance test gate for ADR-053 v2.2 (DoD-6b / §9.1).
//
// The v2.2 design diagrams ARE the behavioral spec. This file holds the
// integration-level conformance scenarios that prove the running system walks
// each target flow exactly as drawn — "the drawn path is the observed path,"
// node-by-node — for the diagrams whose FULL drawn path is not already covered
// by an existing scoped test. It complements (does not duplicate) the per-node
// scoped tests in goal_triggers_test.go (t0/t1), plan_engine_test.go +
// boot_sweep_test.go + plan_engine_correction_test.go (t2/t3/§5), and
// lint_test.go (g4).
//
// Each TestConformance_* scenario lives in `package agent` so it can drive the
// real plan engine (newTestPlanEngine) and the real session-control plane
// (newAsyncNotifierTestLoop + the live message_parent tool + the bus consumer)
// against fake/spy providers — never a real LLM. The real-LLM end of each
// user-facing flow is the separate e2e-gate concern (Conformance_*_E2E); the
// residue that genuinely cannot be proven at this level is documented on each
// scenario.

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// conformanceCriterion is a single populated acceptance criterion so a member's
// own criteria are unambiguously present (a join/assemble member must be a
// first-class member with its own criteria — g5).
func conformanceCriterion(id string) []task.AcceptanceCriterion {
	return []task.AcceptanceCriterion{{
		ID:     id,
		Kind:   task.KindProse,
		Text:   "done",
		Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "conformance"},
		Status: task.CritPending,
	}}
}

// containsTaskID reports whether id appears in the dispatcher's cumulative
// call list (membership is the deterministic observable for "was this member
// dispatched across the DAG phases").
func containsTaskID(calls []string, id string) bool {
	for _, c := range calls {
		if c == id {
			return true
		}
	}
	return false
}

// markMemberDone transitions a member task to StatusDone via the real store
// (the same path a completed member turn takes), so promoteReadyMembers can
// advance its blocked dependents on the next processPlan.
func markMemberDone(t *testing.T, ts *task.Store, id string) {
	t.Helper()
	done := task.StatusDone
	if _, err := ts.Update(id, task.Patch{Status: &done}); err != nil {
		t.Fatalf("mark %q done: %v", id, err)
	}
}

// TestConformance_g5_ShardAssembleTopology_DAGExecution proves the g5
// "report-with-workbook" worked topology EXECUTES as drawn, not just that it
// passes lint. The lint (g4) only validates the plan STRUCTURE; g5's drawn path
// is a runtime execution contract: a serial shard-schema member → three
// disjoint-shard streams → ONE assemble (join) member that the engine must hold
// gated until every shard is terminal, then dispatch exactly once.
//
// Drawn path asserted node-by-node:
//  1. Lint passes for the topology (re-affirms the g4 lint seam under g5).
//  2. schema dispatches FIRST (the only ready member); shards + assemble held.
//  3. schema done → all THREE shards dispatch in parallel; assemble STILL held
//     (deps incomplete).
//  4. two shards done → assemble STILL held (one shard pending — the DAG gate
//     does not partially open).
//  5. the LAST shard done → assemble dispatches (all deps terminal).
//  6. the assemble member is a first-class join with its OWN criteria.
//
// e2e residue: the assemble member actually consuming the shard files to BUILD
// out/report.xlsx is a real-LLM member-turn concern (the e2e gate); this test
// proves the plan engine walks the drawn DAG dispatch ordering faithfully.
func TestConformance_g5_ShardAssembleTopology_DAGExecution(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "plan-report", "owner")

	// Topology (mirrors lint_test.go's g5 report-workbook dataset): schema is
	// the lone root; three shards depend on schema and write disjoint paths;
	// assemble is an authored join depending on all three shards.
	assemble := &task.Task{
		ID: "assemble", Title: "assemble", WorkspaceID: "ws", PlanID: "plan-report",
		Status: task.StatusBlocked, BlockedBy: []string{"shard-a", "shard-b", "shard-c"},
		WriteSet: []string{"out/report.xlsx"}, IsJoin: true,
		Criteria: conformanceCriterion("assemble-c1"),
	}
	members := []*task.Task{
		{ID: "schema", Title: "schema", WorkspaceID: "ws", PlanID: "plan-report",
			Status: task.StatusNext, WriteSet: []string{"schema/shard.json"},
			Criteria: conformanceCriterion("schema-c1")},
		{ID: "shard-a", Title: "shard-a", WorkspaceID: "ws", PlanID: "plan-report",
			Status: task.StatusBlocked, BlockedBy: []string{"schema"}, WriteSet: []string{"shards/a.csv"},
			Criteria: conformanceCriterion("sa-c1")},
		{ID: "shard-b", Title: "shard-b", WorkspaceID: "ws", PlanID: "plan-report",
			Status: task.StatusBlocked, BlockedBy: []string{"schema"}, WriteSet: []string{"shards/b.csv"},
			Criteria: conformanceCriterion("sb-c1")},
		{ID: "shard-c", Title: "shard-c", WorkspaceID: "ws", PlanID: "plan-report",
			Status: task.StatusBlocked, BlockedBy: []string{"schema"}, WriteSet: []string{"shards/c.csv"},
			Criteria: conformanceCriterion("sc-c1")},
		assemble,
	}
	for _, m := range members {
		mustCreateTask(t, h.tasks, m)
	}

	// (1) The topology must pass plan-lint (g4 seam under the g5 shape).
	planMembers := make([]task.Task, 0, len(members))
	for _, m := range members {
		planMembers = append(planMembers, *m)
	}
	pp, err := h.plans.Get("plan-report")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if lerr := plan.Lint(pp, planMembers); lerr != nil {
		t.Fatalf("(1) g5 shard+assemble topology must pass lint, got: %v", lerr)
	}

	// (2) Only the schema root is ready → it dispatches alone.
	h.pe.processPlan(context.Background(), "plan-report")
	calls := h.disp.callList()
	if !containsTaskID(calls, "schema") {
		t.Fatalf("(2) schema must dispatch first, calls=%v", calls)
	}
	for _, held := range []string{"shard-a", "shard-b", "shard-c", "assemble"} {
		if containsTaskID(calls, held) {
			t.Fatalf("(2) %s must NOT dispatch before schema is done, calls=%v", held, calls)
		}
	}

	// (3) schema done → all three shards dispatch in parallel; assemble held.
	markMemberDone(t, h.tasks, "schema")
	h.pe.processPlan(context.Background(), "plan-report")
	calls = h.disp.callList()
	for _, sh := range []string{"shard-a", "shard-b", "shard-c"} {
		if !containsTaskID(calls, sh) {
			t.Errorf("(3) %s must dispatch once schema is done, calls=%v", sh, calls)
		}
	}
	if containsTaskID(calls, "assemble") {
		t.Fatalf("(3) assemble must NOT dispatch while any shard is in flight, calls=%v", calls)
	}

	// (4) Two shards done is NOT enough — the DAG gate stays closed.
	markMemberDone(t, h.tasks, "shard-a")
	markMemberDone(t, h.tasks, "shard-b")
	h.pe.processPlan(context.Background(), "plan-report")
	if containsTaskID(h.disp.callList(), "assemble") {
		t.Fatalf("(4) assemble must NOT dispatch while shard-c is still pending (DAG gate must not partially open), calls=%v", h.disp.callList())
	}

	// (5) The LAST shard done opens the gate → assemble dispatches exactly once.
	markMemberDone(t, h.tasks, "shard-c")
	h.pe.processPlan(context.Background(), "plan-report")
	calls = h.disp.callList()
	if !containsTaskID(calls, "assemble") {
		t.Fatalf("(5) assemble must dispatch once all shards are terminal, calls=%v", calls)
	}

	// (6) The assemble member is a first-class join with its own criteria.
	got, err := h.tasks.Get("assemble")
	if err != nil {
		t.Fatalf("(6) reload assemble: %v", err)
	}
	if !got.IsJoin {
		t.Error("(6) assemble member must be marked is_join (first-class join member)")
	}
	if len(got.Criteria) == 0 {
		t.Error("(6) assemble member must carry its OWN acceptance criteria (first-class join member, not a bare edge)")
	}
}

// TestConformance_g6_PerChildCeiling_NoisyChildCannotStarveSibling proves the
// D15 per-child ceiling is keyed PER CHILD SESSION, so one noisy child hitting
// its unacked question+blocker ceiling cannot starve a sibling sharing the same
// parent inbox. The existing TestMessageParentTool_PerChildCeiling_* proves a
// SINGLE child is throttled; this g6 conformance scenario proves the ISOLATION
// invariant between siblings that the drawn g6 diagram calls out ("one noisy
// child cannot starve a sibling").
//
// Drawn path: child-noisy floods blockers to the ceiling (21st rejected) →
// child-quiet's blocker is STILL accepted into the same parent inbox.
func TestConformance_g6_PerChildCeiling_NoisyChildCannotStarveSibling(t *testing.T) {
	lc := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	inbox.ChildSendRatePerMinute = 100000 // isolate the per-type CEILING from the unrelated rate cap
	tool := tools.NewMessageParentTool(inbox, lc)

	// Two sibling children of the same parent, distinct SessionIDs.
	for _, sid := range []string{"child-noisy", "child-quiet"} {
		if err := lc.Persist(&session.LifecycleRecord{
			SessionID: sid, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: "parent-delegate",
			ParentDurableKey: "parent-1", WorkspaceID: "ws", AgentID: "worker",
			LaunchProfile: session.LaunchProfileSpecialist,
		}); err != nil {
			t.Fatalf("seed %s: %v", sid, err)
		}
	}

	// child-noisy floods blockers up to the default per-child ceiling (20).
	noisyCtx := tools.WithTranscriptSessionID(context.Background(), "child-noisy")
	for i := 0; i < 20; i++ {
		res := tool.Execute(noisyCtx, map[string]any{
			"kind": "blocker", "text": "noise", "severity": "low",
			"message_id": "noisy-" + string(rune('a'+i)),
		})
		if res.IsError {
			t.Fatalf("child-noisy blocker #%d (within ceiling) failed: %s", i, res.ForLLM)
		}
	}
	// The 21st blocker from the SAME noisy child is rejected — the ceiling holds.
	over := tool.Execute(noisyCtx, map[string]any{
		"kind": "blocker", "text": "one too many", "severity": "low", "message_id": "noisy-overflow",
	})
	if !over.IsError {
		t.Fatal("expected child-noisy's 21st blocker to be rejected by the per-child ceiling (D15)")
	}

	// child-quiet's blocker is STILL accepted — the ceiling is per-child, so the
	// noisy sibling did not consume the quiet sibling's slot.
	quietCtx := tools.WithTranscriptSessionID(context.Background(), "child-quiet")
	quiet := tool.Execute(quietCtx, map[string]any{
		"kind": "blocker", "text": "I need a decision", "severity": "high", "message_id": "quiet-1",
	})
	if quiet.IsError {
		t.Fatalf("child-quiet's blocker must be accepted (sibling isolation / D15), got: %s", quiet.ForLLM)
	}

	// Assert the quiet child's message is durable in the shared parent inbox.
	msgs, _, _, err := inbox.Drain("parent-1", "child-quiet", "", 50)
	if err != nil {
		t.Fatalf("drain quiet: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected child-quiet's 1 blocker in the parent inbox, got %d", len(msgs))
	}
}

// TestConformance_g7_SessionRoundTrip_WarmQuestionRespondHandback proves the
// g7 "full round trip" sequence diagram: a mid-run blocking
// question(wait=true) parks the child with a correlation_id, the parent's
// respond routes WARM to the same generation (no cold restart), and a clean
// handback carries result_so_far/artifacts[]/open_questions[] into the parent
// inbox — the rung-0 evidence surface. The scoped tests prove each EDGE in
// isolation (question→inbox, steer→queue, handback→inbox); this scenario
// proves the BIDIRECTIONAL SEQUENCE as one observed path, which is what the g7
// diagram draws and what no single existing test asserts.
//
// Drawn path asserted node-by-node:
//  1. child question(wait=true) → child parks in needs_input with a
//     correlation_id (same generation — no restart yet), question durable in
//     the parent inbox.
//  2. parent respond(correlation_id) → consumer routes it WARM into the SAME
//     child's steering queue (generation unchanged — no cold restart; a
//     restart would mint a new generation via follow_up/Play).
//  3. child handback(final) → result_so_far / artifacts[] / open_questions[]
//     reach the parent inbox (the rung-0 evidence gate surface the Judge
//     reads).
//
// e2e residue: the needs_input→running state flip on the child's resumed turn
// (the actual tool-boundary resume) is a real-LLM turn-engine concern; here we
// prove the CONTROL PLANE routes the round-trip warm with correlation routing
// and that the handback's evidence fields are durably delivered.
func TestConformance_g7_SessionRoundTrip_WarmQuestionRespondHandback(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)
	enableSessionMessaging(al)

	inbox := session.NewMessageInboxStore(t.TempDir())
	ls := session.NewLifecycleStore(t.TempDir())

	const (
		parentSession = "owner-chat-g7"
		childSession  = "child-g7"
		childAgent    = "child-agent-g7"
		childGen      = 7 // distinctive: a cold restart would mint generation 8
	)
	// Seed the PARENT (wake origin routing) + the CHILD (a delegated child with
	// a recorded parent — the consumer's sec-MAJOR-3 gate requires it before it
	// will inject a steer/respond).
	seedLifecycleRecord(t, ls, &session.LifecycleRecord{
		SessionID: parentSession, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession, AgentID: "parent-agent",
		OriginChannel: "tc", OriginChatID: "c1", ParentDurableKey: parentSession,
	})
	seedLifecycleRecord(t, ls, &session.LifecycleRecord{
		SessionID: childSession, State: session.LifecycleRunning, Generation: childGen,
		OwnerScopeKind: session.OwnerScopeParentSession, AgentID: childAgent,
		OriginChannel: "tc", OriginChatID: "c1", ParentDurableKey: parentSession,
	})
	al.SetSessionMessagingStores(inbox, ls)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	al.StartSessionMessageConsumer(ctx)

	// The default agent now carries a LIVE message_parent tool backed by the
	// real stores — drive the child's upward legs through it.
	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent registered")
	}
	mpAny, ok := agent.Tools.Get("message_parent")
	if !ok {
		t.Fatal("message_parent tool not registered after SetSessionMessagingStores")
	}
	mp, ok := mpAny.(*tools.MessageParentTool)
	if !ok {
		t.Fatalf("message_parent tool is %T, want *tools.MessageParentTool", mpAny)
	}
	childCtx := tools.WithTranscriptSessionID(context.Background(), childSession)

	// (1) Child → parent: a blocking question parks the child in needs_input
	// with a correlation_id, same generation, and durably reaches the inbox.
	qRes := mp.Execute(childCtx, map[string]any{
		"kind": "question", "text": "which spec should I implement against?", "wait": true,
	})
	if qRes.IsError {
		t.Fatalf("(1) question(wait=true) failed: %s", qRes.ForLLM)
	}
	parked, err := ls.Load(childSession)
	if err != nil {
		t.Fatalf("(1) load child: %v", err)
	}
	if parked.State != session.LifecycleNeedsInput {
		t.Fatalf("(1) child state = %q, want needs_input (INV-4/G-6)", parked.State)
	}
	if parked.NeedsInput == nil || parked.NeedsInput.CorrelationID == "" {
		t.Fatal("(1) expected a non-empty correlation_id on the parked needs_input")
	}
	if parked.Generation != childGen {
		t.Fatalf("(1) a question park must NOT mint a new generation: got %d, want %d (warm)", parked.Generation, childGen)
	}
	correlationID := parked.NeedsInput.CorrelationID
	waitFor(t, 2*time.Second, func() bool {
		msgs, _, _, _ := inbox.Drain(parentSession, childSession, "", 10)
		return len(msgs) == 1
	})

	// (2) Parent → child: respond(correlation_id) routes WARM into the SAME
	// child's steering queue via the bus consumer (deliverParentToChild). The
	// generation is unchanged — a respond is a warm injection, NOT a restart.
	var resp generated.SessionMessage
	if err := resp.FromSessionMessageRespond(generated.SessionMessageRespond{
		MessageId: "resp-1", SessionId: childSession, CreatedAt: time.Now(),
		CorrelationId: correlationID, Text: "implement against openapi.yaml",
	}); err != nil {
		t.Fatalf("(2) FromSessionMessageRespond: %v", err)
	}
	if err := msgBus.PublishSessionMessage(context.Background(), bus.SessionMessageEvent{
		TargetSessionID: childSession,
		Message:         resp,
	}); err != nil {
		t.Fatalf("(2) PublishSessionMessage: %v", err)
	}
	// The consumer forms the steering scope "agent:<agentID>:<sessionID>".
	scope := "agent:" + childAgent + ":" + childSession
	waitFor(t, 2*time.Second, func() bool {
		return al.pendingSteeringCountForScope(scope) == 1
	})
	drained := al.dequeueSteeringMessagesForScope(scope)
	if len(drained) != 1 {
		t.Fatalf("(2) expected the respond text queued warm in the child's steering scope, got: %+v", drained)
	}
	if !strings.Contains(drained[0].Content, "openapi.yaml") {
		t.Errorf("(2) respond text not routed warm to the child, got: %q", drained[0].Content)
	}
	warm, err := ls.Load(childSession)
	if err != nil {
		t.Fatalf("(2) reload child: %v", err)
	}
	if warm.Generation != childGen {
		t.Fatalf("(2) a respond must route WARM (no cold restart): generation got %d, want %d — a restart would have minted %d",
			warm.Generation, childGen, childGen+1)
	}

	// (3) Child → parent: a clean handback(final) carries result_so_far +
	// artifacts[] + open_questions[] into the durable parent inbox — the
	// rung-0 evidence surface the plan Judge reads.
	hbRes := mp.Execute(childCtx, map[string]any{
		"kind": "handback", "mode": "final",
		"result_so_far":  "contract-first layer landed; lint green",
		"artifacts":      []any{"pkg/api/generated/openapi_types.gen.go", "contracts/openapi.yaml"},
		"open_questions": []any{"should the respond schema carry a decision matrix?"},
	})
	if hbRes.IsError {
		t.Fatalf("(3) handback failed: %s", hbRes.ForLLM)
	}
	// Drain the parent inbox and isolate the handback (the question is still
	// there too); assert all three evidence fields fed the rung-0 surface.
	waitFor(t, 2*time.Second, func() bool {
		msgs, _, _, _ := inbox.Drain(parentSession, childSession, "", 20)
		for _, m := range msgs {
			if k, _ := m.Discriminator(); k == "handback" {
				return true
			}
		}
		return false
	})
	msgs, _, _, _ := inbox.Drain(parentSession, childSession, "", 20)
	var hb *generated.SessionMessageHandback
	for _, m := range msgs {
		if k, _ := m.Discriminator(); k == "handback" {
			if v, err := m.AsSessionMessageHandback(); err == nil {
				hb = &v
				break
			}
		}
	}
	if hb == nil {
		t.Fatal("(3) expected a handback message in the parent inbox (rung-0 evidence surface)")
	}
	if !strings.Contains(hb.ResultSoFar, "contract-first") {
		t.Errorf("(3) handback result_so_far not delivered to the rung-0 surface, got: %q", hb.ResultSoFar)
	}
	if len(hb.Artifacts) != 2 {
		t.Errorf("(3) handback artifacts[] not delivered, got %d: %v", len(hb.Artifacts), hb.Artifacts)
	}
	if len(hb.OpenQuestions) != 1 {
		t.Errorf("(3) handback open_questions[] not delivered, got %d: %v", len(hb.OpenQuestions), hb.OpenQuestions)
	}
}
