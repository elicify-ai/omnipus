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
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
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

// mustFindRevisionRecord looks up revisionID in planID's write-ahead intent
// log and returns its record — the DURABLE proof that AppendCorrection's
// revision entry was actually committed (G-11: "append + SUPERSEDE +
// TARGETED-RETRY each record a revision entry"; INV-6/N-8: all-or-nothing),
// not merely echoed back in the in-memory CorrectionResult. Requires a
// harness built via newCorrectionHarness (intent log wired). Fails the test
// if the record is missing or stuck short of IntentDone — a record that
// never reached "done" means the per-file apply never finished, which is
// exactly the half-applied state the transactional commit (CommitCorrection:
// AppendIntent -> MarkCommitted -> apply -> MarkDone) exists to prevent.
func mustFindRevisionRecord(t *testing.T, h *planEngineHarness, planID, revisionID string) plan.IntentRecord {
	t.Helper()
	if h.pe.intentLog == nil {
		t.Fatal("mustFindRevisionRecord: no intent log wired on this harness (use newCorrectionHarness)")
	}
	records, err := h.pe.intentLog.List(planID)
	if err != nil {
		t.Fatalf("mustFindRevisionRecord: intentLog.List(%q): %v", planID, err)
	}
	for _, rec := range records {
		if rec.IntentID != revisionID {
			continue
		}
		if rec.Status != plan.IntentDone {
			t.Fatalf("mustFindRevisionRecord: revision %q for plan %q is %q, want done "+
				"(all-or-nothing, INV-6/N-8)", revisionID, planID, rec.Status)
		}
		return rec
	}
	t.Fatalf("mustFindRevisionRecord: no committed revision entry %q found for plan %q — "+
		"the correction verb did not durably record (G-11)", revisionID, planID)
	return plan.IntentRecord{}
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
	tool.SetSessionMessagingEnabled(func() bool { return true })

	// Two sibling children of the same parent, distinct SessionIDs.
	for _, sid := range []string{"child-noisy", "child-quiet"} {
		if err := lc.Persist(&session.LifecycleRecord{
			SessionID: sid, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: "parent-delegate",
			ParentDurableKey: "parent-1", WorkspaceID: "ws", AgentID: "worker",
		}); err != nil {
			t.Fatalf("seed %s: %v", sid, err)
		}
	}

	// child-noisy floods blockers up to the default per-child ceiling (20).
	// message_parent.go resolves the durable LifecycleRecord under
	// tools.ToolDelegateSessionID(ctx) (the child's OWN ADR-053 session_id —
	// #576), not ToolTranscriptSessionID; set both to the same seeded
	// SessionID, mirroring pkg/tools/message_parent_test.go's withChildContext.
	noisyCtx := tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), "child-noisy"), "child-noisy")
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
	quietCtx := tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), "child-quiet"), "child-quiet")
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
	// message_parent.go resolves the durable LifecycleRecord under
	// tools.ToolDelegateSessionID(ctx) (the child's OWN ADR-053 session_id —
	// #576), not ToolTranscriptSessionID; set both to the same seeded
	// SessionID, mirroring pkg/tools/message_parent_test.go's withChildContext.
	childCtx := tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), childSession), childSession)

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
	if buildErr := resp.FromSessionMessageRespond(generated.SessionMessageRespond{
		MessageId: "resp-1", SessionId: childSession, CreatedAt: time.Now(),
		CorrelationId: correlationID, Text: "implement against openapi.yaml",
	}); buildErr != nil {
		t.Fatalf("(2) FromSessionMessageRespond: %v", buildErr)
	}
	if publishErr := msgBus.PublishSessionMessage(context.Background(), bus.SessionMessageEvent{
		TargetSessionID: childSession,
		Message:         resp,
	}); publishErr != nil {
		t.Fatalf("(2) PublishSessionMessage: %v", publishErr)
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
	msgs, _, _, _ := inbox.Drain(parentSession, childSession, "", 20) //nolint:dogsled // Only messages are relevant from Drain's four returns.
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

// metJudgeProviderForCompiled returns a fake Judge LLM provider that reports
// every criterion in the REAL compiled goal ladder MET — echoing each
// criterion's own ID so the engine's per-criterion AND (finalizeVerdict)
// resolves to an overall MET verdict and the goal clears. Used by the t0
// conformance scenario to drive a faithful met verdict against the compiled
// Phase-2 criteria ladder (not the legacy "goal-condition" back-compat path
// the unit tests exercise by clearing GoalCriteriaJSON).
//
// ADR-080 D-DOD's judged-set union seam (compiledGoalCriteriaFor,
// goal_compile.go): adjudication now feeds Criteria UNION DoD to the Judge,
// so compiled.DoD's own item(s) (at minimum the built-in floor DoD —
// loadCompiledGoal backfills it whenever a persisted goal carries none, which
// is exactly what happens here: the t0 scenario's deterministic-fallback
// compile — mockProvider's Chat is not JSON, so goalCompileIntentLLM falls
// back to compileGoalIntent, which sets no DoD — gets the floor DoD
// backfilled the moment GoalCriteriaJSON round-trips through loadCompiledGoal)
// ride the SAME per-criterion verdict list this stub returns — omitting them
// would leave the floor DoD unjudgeable and the overall verdict unmet.
func metJudgeProviderForCompiled(t *testing.T, compiled *CompiledGoal, reason string) *fakeJudgeProvider {
	t.Helper()
	var entries []string
	if compiled != nil {
		for _, c := range compiled.Criteria {
			entries = append(entries, fmt.Sprintf(`{"id":%q,"met":true,"reason":%q}`, c.ID, reason))
		}
		for _, d := range compiled.DoD {
			entries = append(entries, fmt.Sprintf(`{"id":%q,"met":true,"reason":%q}`, d.ID, reason))
		}
	}
	body := `{"met":true,"criteria":[` + strings.Join(entries, ",") + `]}`
	return &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: body}, nil
	}}
}

// goalPillStates drains the event collector and returns the ordered list of
// goal_status frame states (GoalStatusChangedPayload.State) observed for sid —
// the durable pill walk the t0 diagram draws ("active → judging → done").
func goalPillStates(c *eventCollector, sid string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, e := range c.events {
		if e.Kind != EventKindGoalStatusChanged {
			continue
		}
		p, ok := e.Payload.(GoalStatusChangedPayload)
		if !ok || p.SessionID != sid {
			continue
		}
		out = append(out, p.State)
	}
	return out
}

// TestConformance_t0_ChatGoal_Design proves the t0 "chat goal" diagram EXECUTES
// as drawn, not just that its edges pass in isolation. The scoped tests in
// goal_triggers_test.go (G-1..G-5) prove each EDGE (claim→Judge, idle, bounce,
// pause); this t0 scenario walks the FULL drawn sequence as one observed path:
// set /goal → SMART compile (GoalCriteriaJSON ladder populated) → conversational
// confirm in chat (goal_status active pill) → a NON-claim question turn PAUSES
// without a verdict/round (waiting_on_user) → user reply resumes → claim with
// evidence → Judge verdict (judging pill) → met → done (goal cleared); and /goal
// clear cancels an in-flight verifier session.
//
// Drawn path asserted node-by-node:
//  1. /goal set compiles a SMART criteria ladder (GoalCriteriaJSON non-empty)
//     and the /goal command IS the chat confirmation — a goal_status frame
//     with state=active is emitted (FR-113).
//  2. A worker turn ending GOAL_STATUS: waiting_on_user PAUSES — no verdict,
//     no round consumed (G-5), pill=waiting_on_user.
//  3. A genuine user reply clears the pause and re-arms (G-5 resume).
//  4. A claim ([goal:evidence] + GOAL_STATUS: met) invokes the Judge EXACTLY
//     once (G-1), pill=judging is emitted BEFORE dispatch, and a met verdict
//     clears the goal (done).
//  5. The pill walk is active → waiting_on_user → judging → done (terminal
//     success per ADR-053 R§8.10 8-value enum — no "cleared" literal).
//  6. /goal clear cancels an in-flight verifier session registered for this
//     goal (FR-037/N-12): the registry entry is removed.
//
// e2e residue: the real-LLM worker turn (vs the scripted turnResult here) is
// the real-LLM/UI gate (Conformance_t0_E2E); this proves the control plane
// walks the drawn path faithfully.
//
// Traces to: ADR-053 §9.1, design diagram t0 (chat goal)
func TestConformance_t0_ChatGoal_Design(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	pe := NewPlanEngine(al, plan.New(t.TempDir()), nil, nil)
	al.SetPlanEngine(pe)
	t.Cleanup(pe.Stop)

	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	coll, collDone := newEventCollector(t, al)
	defer collDone()

	// (1) /goal set compiles a SMART ladder (GoalCriteriaJSON non-empty after
	// confirm) and emits the confirm-in-chat surface. ADR-074 D4a: a PROSE
	// intent parks as a pending goal (pill=queued) and activates on the
	// explicit confirm (pill=active).
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal land the contract-first layer", UserInitiated: true},
		agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	meta, _ := store.GetMeta(sid)
	if meta.GoalCondition == "" {
		t.Fatal("(1) /goal set must persist the goal condition")
	}
	if meta.GoalCriteriaJSON == "" {
		t.Fatal("(1) /goal set must run the SMART compile (GoalCriteriaJSON non-empty) — t0 SMART-compile node")
	}
	compiled := loadCompiledGoal(meta.GoalCriteriaJSON)
	if compiled == nil || len(compiled.Criteria) == 0 {
		t.Fatalf("(1) SMART compile must produce a criteria ladder, got GoalCriteriaJSON=%q", meta.GoalCriteriaJSON)
	}
	// The Judge is swapped AFTER compile so its verdict echoes the REAL
	// compiled criterion IDs (not the legacy "goal-condition" back-compat).
	judgeInst.Provider = metJudgeProviderForCompiled(t, compiled, "contract-first layer landed")

	// (2) A non-claim question turn PAUSES — no verdict, no round, pill=waiting.
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, &turnResult{
		finalContent: "Which schema version should I target?\nGOAL_STATUS: waiting_on_user",
	})
	fakeJudge2, ok := judgeInst.Provider.(*fakeJudgeProvider)
	if !ok {
		t.Fatalf("unexpected type %T for judgeInst.Provider, want *fakeJudgeProvider", judgeInst.Provider)
	}
	if fakeJudge2.callCount() != 0 {
		t.Fatal("(2) a waiting_on_user turn must NOT invoke the Judge (G-5: no verdict)")
	}
	after2, _ := store.GetMeta(sid)
	if after2.GoalRoundsUsed != 0 {
		t.Fatalf("(2) a waiting_on_user turn consumed a round (%d), want 0 (G-5)", after2.GoalRoundsUsed)
	}
	if !al.goalIsWaitingOnUser(sid) {
		t.Fatal("(2) goal must be paused in waiting_on_user after the question turn (G-5)")
	}

	// (3) A genuine user reply clears the pause and re-arms (G-5 resume).
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, &turnResult{
		finalContent: "Target openapi v1 — go ahead.",
	})
	if al.goalIsWaitingOnUser(sid) {
		t.Fatal("(3) user reply must clear the waiting_on_user pause (G-5 resume)")
	}

	// (4) A claim WITH evidence invokes the Judge EXACTLY once and clears the goal.
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, &turnResult{
		finalContent: "[goal:evidence] generated types + lint green\nGOAL_STATUS: met",
	})
	fakeJudge4, ok := judgeInst.Provider.(*fakeJudgeProvider)
	if !ok {
		t.Fatalf("unexpected type %T for judgeInst.Provider, want *fakeJudgeProvider", judgeInst.Provider)
	}
	if got := fakeJudge4.callCount(); got != 1 {
		t.Fatalf("(4) the claim must invoke the Judge exactly once (G-1), got %d", got)
	}
	after4, _ := store.GetMeta(sid)
	if after4.GoalCondition != "" {
		t.Fatalf("(4) a met verdict must clear the goal (done), still: %q", after4.GoalCondition)
	}

	// (5) The pill walk is active → waiting_on_user → judging → done.
	//     Wait for the terminal "done" frame (the met verdict's clearGoal
	//     emits it synchronously, but the event-collector goroutine drains the
	//     bus asynchronously — wait for the frame rather than a frame COUNT).
	waitFor(t, 2*time.Second, func() bool {
		for _, s := range goalPillStates(coll, sid) {
			if s == goalPillDone {
				return true
			}
		}
		return false
	})
	pills := goalPillStates(coll, sid)
	// Drop any trailing re-arm active frames; keep the first occurrence of each
	// drawn node in order.
	var walk []string
	for _, p := range pills {
		if len(walk) == 0 || walk[len(walk)-1] != p {
			walk = append(walk, p)
		}
	}
	// ADR-074 D4a prepends the pending step: queued (compiled, awaiting the
	// user's confirmation) precedes active.
	wantWalk := []string{goalPillQueued, goalPillActive, goalPillWaitingOnUser, goalPillActive, goalPillJudging, goalPillDone}
	if !equalStringSlices(walk, wantWalk) {
		t.Fatalf("(5) pill walk = %v, want %v (queued→active→waiting_on_user→active(resume)→judging→done)", walk, wantWalk)
	}

	// (6) /goal clear cancels an in-flight verifier session registered for this
	// goal (FR-037/N-12). Set a fresh goal, register a verifier session, clear.
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal a second goal", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	verifierUnit := verifierUnitForGoal(sid)
	pe.VerifierRegistry().Register(verifierUnit, "verifier-t0-inflight")
	if _, ok := pe.VerifierRegistry().Lookup(verifierUnit); !ok {
		t.Fatal("(6) setup: verifier entry must register before clear")
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal clear", UserInitiated: true}, agentInst, &opts)
	if _, ok := pe.VerifierRegistry().Lookup(verifierUnit); ok {
		t.Fatal("(6) /goal clear must cancel + unregister the in-flight verifier session (FR-037)")
	}
	after6, _ := store.GetMeta(sid)
	if after6.GoalCondition != "" {
		t.Fatalf("(6) /goal clear must clear the goal, still: %q", after6.GoalCondition)
	}
}

// equalStringSlices reports whether two string slices are element-wise equal.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// lintMember builds a plan member for the g4 lint conformance scenario. It
// mirrors pkg/plan/lint_test.go's `member` helper (package plan) but lives here
// in package agent so the g4 scenario can call plan.Lint without a cross-
// package test helper. Each member carries its own acceptance criterion so a
// failure is unambiguously attributable to Lint, never an empty-criteria gap.
func lintMember(id string, blockedBy, writeSet []string) task.Task {
	return task.Task{
		ID:        id,
		Title:     "member " + id,
		BlockedBy: blockedBy,
		WriteSet:  writeSet,
		Criteria:  conformanceCriterion(id + "-c1"),
	}
}

// TestConformance_t1_StandaloneTask_Design proves the t1 "standalone task"
// diagram EXECUTES as drawn. The scoped evidence_gate_test.go tests prove each
// EDGE (bare claim rejected, consecutive→attempt, evidence→verifier, Stop);
// this t1 scenario walks the FULL drawn sequence as one observed path: ▶ Run
// (claim next→in_progress) → worker claim → evidence-gate (1st bare claim →
// free steer, no attempt; 2nd → consumes an attempt) → claim with evidence →
// Judge met → done; then ■ Stop cancels the in-flight turn+verifier sessions.
//
// Drawn path asserted node-by-node:
//  1. ▶ Run: a StatusNext task is claimed into in_progress (the dispatch).
//  2. 1st bare claim (TASK_STATUS: success, no [goal:evidence]) → rejected
//     pre-Judge with a teaching steer, AttemptCount stays 0 (G-4 free bounce).
//  3. re-claim → 2nd bare claim → consumes a real attempt (AttemptCount 1).
//  4. claim WITH evidence → verifier dispatched → met verdict → StatusDone.
//  5. ■ Stop on a second in_progress task cancels its worker + verifier
//     sessions (RequestCancelForSession) and marks the task failed/cancelled.
//
// e2e residue: the real-LLM worker turn (vs the scripted finishTaskRun resp)
// is the real-LLM gate (Conformance_t1_E2E); this proves the evidence-gate +
// Stop control plane walks the drawn path faithfully.
//
// Traces to: ADR-053 §9.1, design diagram t1 (standalone task)
func TestConformance_t1_StandaloneTask_Design(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	taskStore := GetTaskStore(al)
	sessStore := al.GetAgentStore("native-agent")
	if sessStore == nil {
		t.Fatal("native-agent session store not available")
	}
	taskMeta, err := sessStore.NewSession(session.SessionTypeTask, "system", "native-agent")
	if err != nil {
		t.Fatalf("create task session: %v", err)
	}
	taskSessionID := taskMeta.ID

	tk := &task.Task{
		ID: "t1-standalone", AgentID: "native-agent", WorkspaceID: "test-ws",
		Title: "t1 standalone task", Status: task.StatusNext,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "must do X")},
	}
	if createErr := taskStore.Create(tk); createErr != nil {
		t.Fatalf("create task: %v", createErr)
	}

	// A met-verdict judge that echoes the criterion id (the verifier reaches it
	// only on a claim WITH evidence — node 4).
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}}

	// (1) ▶ Run: claim the next task into in_progress (the dispatch).
	if _, claimErr := taskStore.ClaimForRun(tk.ID, time.Now()); claimErr != nil {
		t.Fatalf("(1) Run/claim: %v", claimErr)
	}
	current, _ := taskStore.Get(tk.ID)
	if current.Status != task.StatusInProgress {
		t.Fatalf("(1) after Run: status = %q, want in_progress", current.Status)
	}

	// (2) 1st bare claim → evidence-gate rejects pre-Judge, free steer, attempt 0.
	bare := "done.\nTASK_STATUS: success\n"
	if redis := al.taskExecutor.finishTaskRun(context.Background(), current, taskSessionID, bare, nil, "", nil); redis == "" {
		t.Fatal("(2) 1st bare claim must be re-prompted (free steer), got empty redispatch")
	}
	after2, _ := taskStore.Get(tk.ID)
	if after2.AttemptCount != 0 {
		t.Fatalf("(2) 1st bare claim consumed an attempt (%d), want 0 (G-4 free bounce)", after2.AttemptCount)
	}
	fakeJudgeB2, ok := judgeInst.Provider.(*fakeJudgeProvider)
	if !ok {
		t.Fatalf("unexpected type %T for judgeInst.Provider, want *fakeJudgeProvider", judgeInst.Provider)
	}
	if fakeJudgeB2.callCount() != 0 {
		t.Fatal("(2) 1st bare claim must NOT reach the Judge (evidence-gate rejects pre-Judge)")
	}

	// (3) re-claim → 2nd bare claim → consumes a real attempt (AttemptCount 1).
	if _, claimErr3 := taskStore.ClaimForRun(tk.ID, time.Now()); claimErr3 != nil {
		t.Fatalf("(3) re-claim: %v", claimErr3)
	}
	current, _ = taskStore.Get(tk.ID)
	if redis := al.taskExecutor.finishTaskRun(context.Background(), current, taskSessionID, bare, nil, "", nil); redis == "" {
		t.Fatal("(3) 2nd bare claim must be re-prompted too")
	}
	after3, _ := taskStore.Get(tk.ID)
	if after3.AttemptCount != 1 {
		t.Fatalf("(3) 2nd bare claim: attempt_count = %d, want 1 (G-4: 2nd costs an attempt)", after3.AttemptCount)
	}
	fakeJudgeB3, ok := judgeInst.Provider.(*fakeJudgeProvider)
	if !ok {
		t.Fatalf("unexpected type %T for judgeInst.Provider, want *fakeJudgeProvider", judgeInst.Provider)
	}
	if fakeJudgeB3.callCount() != 0 {
		t.Fatal("(3) 2nd bare claim must still NOT reach the Judge (no evidence to judge)")
	}

	// (4) claim WITH evidence → verifier dispatched → met → done.
	if _, claimErr4 := taskStore.ClaimForRun(tk.ID, time.Now()); claimErr4 != nil {
		t.Fatalf("(4) re-claim: %v", claimErr4)
	}
	current, _ = taskStore.Get(tk.ID)
	withEvidence := "verified output matches c1.\n[goal:evidence] compared to acceptance criterion c1, matches\nTASK_STATUS: success\n"
	if redis := al.taskExecutor.finishTaskRun(context.Background(), current, taskSessionID, withEvidence, nil, "", nil); redis != "" {
		t.Fatalf("(4) a met claim must NOT re-dispatch, got redispatch=%q", redis)
	}
	fakeJudgeB4, ok := judgeInst.Provider.(*fakeJudgeProvider)
	if !ok {
		t.Fatalf("unexpected type %T for judgeInst.Provider, want *fakeJudgeProvider", judgeInst.Provider)
	}
	if fakeJudgeB4.callCount() != 1 {
		t.Fatalf("(4) a claim WITH evidence must reach the Judge exactly once, got %d", fakeJudgeB4.callCount())
	}
	after4, _ := taskStore.Get(tk.ID)
	if after4.Status != task.StatusDone {
		t.Fatalf("(4) a met verdict must transition the task to done, got %q", after4.Status)
	}

	// (5) ■ Stop cancels the in-flight turn + verifier sessions on a second
	// in_progress task (the Stop node of the t1 diagram). A fake canceller
	// records the RequestCancelForSession calls; the verifier registry entry
	// for the task is registered so Stop's fan-out reaches it.
	h := newTestPlanEngine(t)
	canceller := &fakeSessionCanceller{}
	h.pe.canceller = canceller
	stopTk := mustCreateTask(t, h.tasks, &task.Task{
		ID: "t1-stop", Title: "t1 stop target", WorkspaceID: "ws",
		Status: task.StatusInProgress, SessionID: "sess-worker-t1",
	})
	h.pe.VerifierRegistry().Register(verifierUnitForTask(stopTk.ID), "verifier-t1")
	stopped, err := h.pe.StopTask(context.Background(), stopTk.ID, "tester", "web")
	if err != nil {
		t.Fatalf("(5) StopTask: %v", err)
	}
	if stopped.Status != task.StatusFailed || !isCancelledMember(stopped) {
		t.Fatalf("(5) Stop must fail+cancel the task, got status=%q result=%q", stopped.Status, stopped.Result)
	}
	if !canceller.contains("sess-worker-t1") || !canceller.contains("verifier-t1") {
		t.Fatalf("(5) Stop must cancel the worker AND verifier sessions, calls=%v", canceller.callList())
	}
}

// TestConformance_t2_PlanLifecycle_Design proves the t2 "plan lifecycle"
// diagram EXECUTES as drawn. The scoped plan_engine_test.go (judge
// met/unmet/rounds) + plan_engine_correction_test.go (Play, no-per-member)
// prove each EDGE; this t2 scenario walks the FULL drawn sequence as one
// observed path: Execute (approved→running) → gated approve (lint) → members
// dispatch per DAG → all-terminal → plan Judge → unmet → awaiting-owner-
// correction gate HOLDS (no round burned on unchanged state — the F2 proof)
// → owner appends → re-judge → done; plus Play resumes a cancelled member
// from last git commit (D13) and there is NO per-member start/cancel/resume
// (D7).
//
// Drawn path asserted node-by-node:
//  1. Execute: an approved plan auto-ticks to running (the Execute node).
//  2. gated approve: the plan's member topology passes plan-lint (approve).
//  3. members dispatch per DAG: the root member dispatches; a blocked
//     dependent is held until its dependency is done, then dispatches.
//  4. all-terminal → plan Judge → unmet → parks at awaiting_supervision
//     (one round consumed).
//  5. F2: idle ticks over the UNCHANGED all-terminal state burn NO further
//     round (the gate holds — no re-judge of unchanged state).
//  6. owner appends a correction (new terminal member) → signature changes →
//     re-judge → met → done.
//  7. D13: Play on a stopped plan mints a new generation and resumes a
//     cancelled member from its last boundary commit.
//  8. D7: no per-member start/cancel/resume exists (whole-plan Stop/Play only).
//
// e2e residue: the real per-member worker turns + the real git commit the
// Judge reads are the real-LLM/git gate; this proves the plan-engine control
// plane walks the drawn path faithfully.
//
// Traces to: ADR-053 §9.1, design diagram t2 (plan lifecycle)
func TestConformance_t2_PlanLifecycle_Design(t *testing.T) {
	h := newCorrectionHarness(t)
	ctx := context.Background()

	// (1) Execute: an approved plan auto-ticks to running.
	pp := &plan.Plan{
		ID: "plan-t2", Title: "plan-t2", WorkspaceID: "ws", OwnerAgentID: "owner",
		State: plan.StateApproved, DoD: []task.AcceptanceCriterion{planProseCriterion("DoD is met")},
	}
	mustCreatePlan(t, h.plans, pp)
	// (2) gated approve: a DAG topology (root → blocked dependent) passes lint.
	root := &task.Task{ID: "t2-root", Title: "root", WorkspaceID: "ws", PlanID: "plan-t2",
		Status: task.StatusNext, WriteSet: []string{"src/root.go"}, Criteria: conformanceCriterion("t2-root-c1")}
	dep := &task.Task{ID: "t2-dep", Title: "dep", WorkspaceID: "ws", PlanID: "plan-t2",
		Status: task.StatusBlocked, BlockedBy: []string{"t2-root"}, WriteSet: []string{"src/dep.go"},
		Criteria: conformanceCriterion("t2-dep-c1")}
	for _, m := range []*task.Task{root, dep} {
		mustCreateTask(t, h.tasks, m)
	}
	members := []task.Task{*root, *dep}
	if lerr := plan.Lint(pp, members); lerr != nil {
		t.Fatalf("(2) gated approve: plan-lint must pass the DAG topology, got: %v", lerr)
	}

	h.pe.Tick(ctx)
	got, _ := h.plans.Get("plan-t2")
	if got.State != plan.StateRunning {
		t.Fatalf("(1) Execute: state = %q, want running", got.State)
	}

	// (3) members dispatch per DAG: root dispatches; dependent is HELD.
	h.pe.processPlan(ctx, "plan-t2")
	calls := h.disp.callList()
	if !containsTaskID(calls, "t2-root") {
		t.Fatalf("(3) root must dispatch first, calls=%v", calls)
	}
	if containsTaskID(calls, "t2-dep") {
		t.Fatalf("(3) dependent must NOT dispatch before root is done (DAG gate), calls=%v", calls)
	}
	// root done → dependent dispatches.
	markMemberDone(t, h.tasks, "t2-root")
	h.pe.processPlan(ctx, "plan-t2")
	if !containsTaskID(h.disp.callList(), "t2-dep") {
		t.Fatal("(3) dependent must dispatch once root is done (DAG ordering)")
	}
	// all-terminal: dependent done → the plan is ready to judge.
	markMemberDone(t, h.tasks, "t2-dep")

	// (4) plan Judge → unmet → awaiting_supervision (one round).
	h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
			Met: false, PerCriterion: []task.CriterionVerdict{
				{CriterionID: in.Criteria[0].ID, Met: false, Reason: "not yet"},
			},
		}}
	}
	h.pe.processPlan(ctx, "plan-t2")
	h.pe.judgeWG.Wait()
	parked, _ := h.plans.Get("plan-t2")
	if parked.State != plan.StateRunning || parked.JudgeRounds != 1 {
		t.Fatalf("(4) after unmet: state=%q rounds=%d, want running/1", parked.State, parked.JudgeRounds)
	}
	if parked.EffectivePlanPhase() != plan.PhaseAwaitingSupervision {
		t.Fatalf("(4) plan_phase = %q, want awaiting_supervision", parked.EffectivePlanPhase())
	}

	// (5) F2: idle ticks over the UNCHANGED all-terminal state burn NO round.
	const idleTicks = 3
	for i := 0; i < idleTicks; i++ {
		h.pe.processPlan(ctx, "plan-t2")
		h.pe.judgeWG.Wait()
	}
	after5, _ := h.plans.Get("plan-t2")
	if after5.JudgeRounds != 1 {
		t.Fatalf("(5) F2: %d unchanged idle ticks burned rounds to %d, want 1 (no re-judge of unchanged state)",
			idleTicks, after5.JudgeRounds)
	}
	if h.judge.callCount() != 1 {
		t.Fatalf("(5) F2: judge called %d times after unchanged idle ticks, want 1", h.judge.callCount())
	}

	// (6) owner appends a correction (a NEW member with work to do) → it
	//     dispatches, completes → the all-terminal signature changes → re-judge
	//     → met → done. (AppendCorrection's honest-exit check requires the
	//     correction to make progress; a StatusNext tail member does.)
	h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{Met: true, PerCriterion: []task.CriterionVerdict{
			{CriterionID: in.Criteria[0].ID, Met: true, Reason: "now met"},
		}}}
	}
	corrRes, err := h.pe.AppendCorrection(ctx, "plan-t2", supervisorCaller(), CorrectionRequest{
		Verb: CorrectionAppend, FalsifiedAssumption: "assumed the first attempt was enough",
		TailMembers: []task.Task{{ID: "t2-tail", Title: "tail", WorkspaceID: "ws", Status: task.StatusNext,
			WriteSet: []string{"src/tail.go"}, Criteria: conformanceCriterion("t2-tail-c1")}},
		Reason: "owner correction to address the unmet DoD",
	})
	if err != nil {
		t.Fatalf("(6) AppendCorrection: %v", err)
	}
	if corrRes.HonestExit {
		t.Fatal("(6) the correction must make progress (a new ready member), not honest-exit")
	}
	// G-11: append records a revision entry — durable proof via the intent
	// log, not merely the in-memory CorrectionResult echo.
	t2Rec := mustFindRevisionRecord(t, h, "plan-t2", corrRes.RevisionID)
	if t2Rec.Revision.Verb != CorrectionAppend {
		t.Fatalf("(6) G-11: recorded revision verb = %q, want append", t2Rec.Revision.Verb)
	}
	if t2Rec.Revision.PlanID != "plan-t2" {
		t.Fatalf("(6) G-11: recorded revision plan_id = %q, want plan-t2", t2Rec.Revision.PlanID)
	}
	if corrRes.RevisionEntry.Verb != CorrectionAppend {
		t.Fatalf("(6) CorrectionResult.RevisionEntry.Verb = %q, want append", corrRes.RevisionEntry.Verb)
	}
	// AppendCorrection dispatched the ready tail member (next→in_progress);
	// complete it so the DAG is all-terminal again with a CHANGED signature.
	markMemberDone(t, h.tasks, "t2-tail")
	h.pe.processPlan(ctx, "plan-t2")
	h.pe.judgeWG.Wait()
	done, _ := h.plans.Get("plan-t2")
	if done.State != plan.StateDone {
		t.Fatalf("(6) after owner correction + re-judge: state=%q failed_reason=%q, want done",
			done.State, done.FailedReason)
	}
	if h.judge.callCount() != 2 {
		t.Fatalf("(6) re-judge after correction: judge calls = %d, want 2 (gate reopened on changed state)", h.judge.callCount())
	}

	// (7) D13: Play on a stopped plan mints a new generation and resumes a
	// cancelled member from its last boundary commit (resolved via the
	// commit resolver). Stop the t2 plan first (it's done — Stop needs failed,
	// so drive a fresh failed plan for the Play node).
	playPlan := &plan.Plan{
		ID: "plan-t2-play", Title: "plan-t2-play", WorkspaceID: "ws", OwnerAgentID: "owner",
		State: plan.StateFailed, FailedReason: plan.FailedReasonStoppedByUser, JudgeRounds: 2,
		DoD: []task.AcceptanceCriterion{planProseCriterion("DoD is met")},
	}
	mustCreatePlan(t, h.plans, playPlan)
	mustCreateTask(t, h.tasks, &task.Task{
		ID: "t2-play-failed", Title: "failed-with-commit", PlanID: "plan-t2-play", WorkspaceID: "ws",
		Status: task.StatusFailed,
	})
	h.pe.SetCommitResolver(&fakeCommitResolver{hashes: map[string]string{"t2-play-failed": "feedface"}})
	played, err := h.pe.PlayPlan(ctx, "plan-t2-play")
	if err != nil {
		t.Fatalf("(7) PlayPlan: %v", err)
	}
	if played.NewGeneration != 1 {
		t.Errorf("(7) D13: Play must mint a new generation, got %d", played.NewGeneration)
	}
	resumed, _ := h.tasks.Get("t2-play-failed")
	if resumed.Status != task.StatusNext {
		t.Errorf("(7) D13: cancelled member must be reset to next by Play, got %q", resumed.Status)
	}
	if resumed.ResumeFromCommit != "feedface" {
		t.Errorf("(7) D13: member must resume from its last boundary commit, got %q want \"feedface\"", resumed.ResumeFromCommit)
	}
	played0, _ := h.plans.Get("plan-t2-play")
	if played0.JudgeRounds != 0 {
		t.Errorf("(7) D13: Play must zero JudgeRounds, got %d", played0.JudgeRounds)
	}

	// (8) D7: no per-member start/cancel/resume exists — only whole-plan
	// Stop/Play (and StopTask for standalone tasks, not member lifecycle). The
	// compile-time guarantee is asserted in TestNoPerMemberControls; here we
	// re-affirm the whole-plan Stop is the ONLY plan-level control by driving it.
	if _, err := h.pe.StopPlan(ctx, "plan-t2-play", "tester", "test"); err != nil {
		t.Fatalf("(8) D7: whole-plan StopPlan: %v", err)
	}
	stoppedPlan, _ := h.plans.Get("plan-t2-play")
	if stoppedPlan.State != plan.StateFailed {
		t.Errorf("(8) D7: StopPlan must fail the whole plan, got %q", stoppedPlan.State)
	}
}

// TestConformance_t3_PlanningReplanning_Design proves the t3 "planning &
// re-planning" diagram EXECUTES as drawn. The scoped plan_engine_correction_test.go
// tests prove each EDGE (supersede, targeted-retry, transactional append); this
// t3 scenario walks the FULL drawn sequence as one observed path: intent →
// owner plans (members) → execute → unmet-all-done (awaiting correction) →
// owner re-plans → supersede a done member (D4) AND targeted-retry a frozen-
// transient failed member (D4) → transactional append (kill mid-append →
// pre-append DAG) → done.
//
// Each correction verb requires the plan to be durably at
// awaiting_supervision, and AppendCorrection itself resets the phase to dispatching (so
// the engine re-dispatches/re-judges). The two D4 verbs are therefore driven
// on two freshly-seeded awaiting plans (supersede on plan-t3-sup, targeted-
// retry on plan-t3-retry) — the same edge-faithful pattern the scoped
// correction tests use — then the transactional-append and re-judge→done nodes
// close the drawn path.
//
// Drawn path asserted node-by-node:
//  1. owner plans: a running plan is driven to awaiting_supervision
//     (unmet-all-done).
//  2. owner re-plans via SUPERSEDE: the done member is marked ignored-by-Judge
//     (immutable record), and the auto-reset resets the other failed member.
//  3. TARGETED-RETRY: a frozen-transient failed member is reset ALONE, WITHOUT
//     auto-resetting other failed members and WITHOUT touching the done member.
//  4. transactional append: an uncommitted intent is discarded on boot replay
//     (pre-append DAG); a committed-but-not-done intent is replayed forward
//     (post-append DAG) — kill mid-append leaves the pre-append DAG intact.
//  5. the re-planned DAG re-judges to done once the corrected members land.
//
// e2e residue: the real owner-LLM planning turn (vs the scripted
// AppendCorrection calls) is the real-LLM gate; this proves the correction
// control plane walks the drawn path faithfully.
//
// Traces to: ADR-053 §9.1, design diagram t3 (planning & re-planning)
func TestConformance_t3_PlanningReplanning_Design(t *testing.T) {
	h := newCorrectionHarness(t)
	ctx := context.Background()

	// (1) owner plans: a running plan driven to awaiting_supervision
	//     (unmet-all-done). Two awaiting plans are seeded — one for the
	//     SUPERSEDE verb, one for the TARGETED-RETRY verb — because each
	//     AppendCorrection resets the phase to dispatching.
	// t3-done carries a distinctive (deliberately WRONG) claim so the
	// supersede step below can prove its outcome is withheld from the Judge's
	// evidence — the actual observable property — rather than merely
	// flagged in an in-memory map (see buildPlanClaimText).
	const t3WrongClaim = "WRONG-CLAIM-T3-DONE-OUTCOME"
	t3Done := doneMember("t3-done")
	t3Done.Result = t3WrongClaim
	mustSeedAwaitingCorrection(t, h, "plan-t3-sup",
		t3Done, failedMember("t3-other-failed"))
	mustSeedAwaitingCorrection(t, h, "plan-t3-retry",
		doneMember("t3-done-r"), failedMember("t3-frozen"))
	for _, pid := range []string{"plan-t3-sup", "plan-t3-retry"} {
		p, _ := h.plans.Get(pid)
		if p.EffectivePlanPhase() != plan.PhaseAwaitingSupervision {
			t.Fatalf("(1) plan %q must be at awaiting_supervision (unmet-all-done), got %q",
				pid, p.EffectivePlanPhase())
		}
	}

	// (2) owner re-plans via SUPERSEDE: the done member is ignored-by-Judge
	//     (record stays immutable/done), and the auto-reset resets the other
	//     failed member t3-other-failed (supersede triggers auto-reset).
	supCorrRes, err := h.pe.AppendCorrection(ctx, "plan-t3-sup", supervisorCaller(), CorrectionRequest{
		Verb: CorrectionSupersede, SupersededMemberID: "t3-done",
		FalsifiedAssumption: "assumed the done member's outcome was correct",
		Reason:              "supersede the done member — its result is wrong",
		// FR-030: a supersede must be PAIRED with replacement work. Bare
		// discounting is rejected by the engine. The replacement carries its
		// own acceptance criteria for the same reason (ADR-055 fix wave,
		// finding 4): t3-done has no criteria of its own, so
		// RequireCriteriaInheritance is vacuous here, and replacement work the
		// Judge cannot adjudicate would be a bare discount in all but name.
		TailMembers: []task.Task{{
			ID: "t3-done-replacement", Title: "redo the superseded work",
			WorkspaceID: "ws", Status: task.StatusNext,
			Criteria: []task.AcceptanceCriterion{planProseCriterion("the superseded work is redone correctly")},
		}},
	})
	if err != nil {
		t.Fatalf("(2) AppendCorrection supersede: %v", err)
	}
	superRecord, _ := h.tasks.Get("t3-done")
	if superRecord.Status != task.StatusDone {
		t.Errorf("(2) D4 supersede: the done member's record must stay immutable (done), got %q", superRecord.Status)
	}
	if !h.pe.isMemberSuperseded("plan-t3-sup", "t3-done") {
		t.Error("(2) D4 supersede: the done member must be tracked as ignored-by-Judge")
	}
	autoReset, _ := h.tasks.Get("t3-other-failed")
	if autoReset.Status == task.StatusFailed {
		t.Errorf("(2) supersede's auto-reset must reset the other failed member, still failed")
	}
	// G-11: supersede records a revision entry (durable — the intent log, not
	// just the in-memory map isMemberSuperseded checks above).
	supRec := mustFindRevisionRecord(t, h, "plan-t3-sup", supCorrRes.RevisionID)
	if supRec.Revision.Verb != CorrectionSupersede {
		t.Fatalf("(2) G-11: recorded revision verb = %q, want supersede", supRec.Revision.Verb)
	}
	if supRec.Revision.SupersededMemberID != "t3-done" {
		t.Fatalf("(2) G-11: recorded revision superseded_member_id = %q, want t3-done", supRec.Revision.SupersededMemberID)
	}
	// D4 / "reaches the judge as such": isMemberSuperseded above only proves a
	// map entry was written — ADR-055 fix-wave finding 3 documents that this
	// exact shape (asserting the map, not the evidence) is what let supersede
	// ship inert end to end. Prove the OBSERVABLE property instead by calling
	// the REAL production function the next judge round actually feeds
	// (runPlanJudgeRound: ClaimText = buildPlanClaimText(tasks, superseded))
	// with the real post-correction task list and the real superseded set.
	supTasks, lerr := h.tasks.List(task.Filter{PlanID: "plan-t3-sup"})
	if lerr != nil {
		t.Fatalf("(2) list plan-t3-sup tasks: %v", lerr)
	}
	claimText := buildPlanClaimText(supTasks, h.pe.supersededMemberSet("plan-t3-sup"))
	if strings.Contains(claimText, t3WrongClaim) {
		t.Fatalf("(2) the superseded member's WRONG outcome reached the Judge's claim text verbatim — "+
			"supersede changed nothing the Judge sees.\nClaimText was:\n%s", claimText)
	}
	if !strings.Contains(claimText, "SUPERSEDED") || !strings.Contains(claimText, "t3-done") {
		t.Fatalf("(2) the superseded member is not marked withheld in the claim text the Judge would "+
			"receive.\nClaimText was:\n%s", claimText)
	}

	// (3) TARGETED-RETRY (on the second awaiting plan): reset the frozen-
	//     transient failed member ALONE — the done member stays frozen/done and
	//     targeted-retry does NOT auto-reset other failed members (D4).
	retryCorrRes, err := h.pe.AppendCorrection(ctx, "plan-t3-retry", supervisorCaller(), CorrectionRequest{
		Verb: CorrectionTargetedRetry, RetriedMemberID: "t3-frozen",
		FalsifiedAssumption: "assumed the transient failure was permanent",
		Reason:              "targeted-retry the frozen-transient member",
	})
	if err != nil {
		t.Fatalf("(3) AppendCorrection targeted_retry: %v", err)
	}
	retried, _ := h.tasks.Get("t3-frozen")
	if retried.Status == task.StatusFailed {
		t.Errorf("(3) D4 targeted-retry: the frozen-transient member must be reset, still failed")
	}
	// the done member on the retry plan is STILL frozen/done (D4).
	stillDone, _ := h.tasks.Get("t3-done-r")
	if stillDone.Status != task.StatusDone {
		t.Errorf("(3) D4 targeted-retry: the done member must stay frozen (done), got %q", stillDone.Status)
	}
	// G-11: targeted-retry records a revision entry.
	retryRec := mustFindRevisionRecord(t, h, "plan-t3-retry", retryCorrRes.RevisionID)
	if retryRec.Revision.Verb != CorrectionTargetedRetry {
		t.Fatalf("(3) G-11: recorded revision verb = %q, want targeted_retry", retryRec.Revision.Verb)
	}
	if retryRec.Revision.RetriedMemberID != "t3-frozen" {
		t.Fatalf("(3) G-11: recorded revision retried_member_id = %q, want t3-frozen", retryRec.Revision.RetriedMemberID)
	}

	// (4) transactional append: kill mid-append → pre-append DAG. An
	//     uncommitted intent is discarded on boot replay (its tail member does
	//     NOT exist); a committed-but-not-done intent is replayed forward (its
	//     tail member DOES exist). This is the t3 "transactional append" node.
	dir := t.TempDir()
	il, ilErr := plan.NewIntentLog(filepath.Join(dir, "plan_intents"), nil)
	if ilErr != nil {
		t.Fatalf("(4) NewIntentLog: %v", ilErr)
	}
	// uncommitted intent (crash before MarkCommitted) — must be DISCARDED.
	recUncommitted := plan.IntentRecord{
		IntentID: "rev-t3-uncommitted", PlanID: "plan-t3",
		Members: []task.Task{{ID: "t3-uncommitted", Title: "u", WorkspaceID: "ws", PlanID: "plan-t3"}},
		Revision: plan.RevisionEntry{RevisionID: "rev-t3-uncommitted", PlanID: "plan-t3",
			Verb: plan.RevisionAppend, Generation: 0},
	}
	if appendErr := il.AppendIntent(recUncommitted); appendErr != nil {
		t.Fatalf("(4) AppendIntent uncommitted: %v", appendErr)
	}
	// committed-but-not-done (crash after MarkCommitted, before MarkDone) — replayed FORWARD.
	recCommitted := plan.IntentRecord{
		IntentID: "rev-t3-committed", PlanID: "plan-t3",
		Members: []task.Task{{ID: "t3-committed", Title: "c", WorkspaceID: "ws", PlanID: "plan-t3"}},
		Revision: plan.RevisionEntry{RevisionID: "rev-t3-committed", PlanID: "plan-t3",
			Verb: plan.RevisionAppend, Generation: 0},
	}
	if appendErr := il.AppendIntent(recCommitted); appendErr != nil {
		t.Fatalf("(4) AppendIntent committed: %v", appendErr)
	}
	if commitErr := il.MarkCommitted("plan-t3", "rev-t3-committed"); commitErr != nil {
		t.Fatalf("(4) MarkCommitted: %v", commitErr)
	}
	// boot replay (the kill-mid-append recovery).
	ts := task.New(filepath.Join(dir, "tasks"))
	var applied []string
	res, err := il.ReplayAtBoot("plan-t3", func(rec plan.IntentRecord) error {
		for _, m := range rec.Members {
			applied = append(applied, m.ID)
			_ = ts.Create(&m)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("(4) ReplayAtBoot: %v", err)
	}
	if res.Discarded != 1 || res.Replayed != 1 {
		t.Fatalf("(4) transactional append: Discarded=%d Replayed=%d, want 1/1 (kill mid-append → pre-append DAG)",
			res.Discarded, res.Replayed)
	}
	if len(applied) != 1 || applied[0] != "t3-committed" {
		t.Fatalf("(4) only the committed intent's member must be applied (pre-append DAG), applied=%v", applied)
	}
	if _, err := ts.Get("t3-uncommitted"); err == nil {
		t.Error("(4) the uncommitted (killed-mid-append) member must NOT exist — pre-append DAG")
	}
	if _, err := ts.Get("t3-committed"); err != nil {
		t.Error("(4) the committed-but-not-done member must exist — post-append DAG (replayed forward)")
	}

	// (5) the re-planned DAG re-judges to done once the corrected members land.
	//     On plan-t3-retry, the targeted-retry reset t3-frozen (failed→next→
	//     in_progress via the dispatcher); mark it done so the all-terminal
	//     signature CHANGES (frozen was failed, now done), then a met Judge
	//     resolves the plan to done.
	markMemberDone(t, h.tasks, "t3-frozen")
	h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{Met: true, PerCriterion: []task.CriterionVerdict{
			{CriterionID: in.Criteria[0].ID, Met: true, Reason: "re-planned and met"},
		}}}
	}
	h.pe.processPlan(ctx, "plan-t3-retry")
	h.pe.judgeWG.Wait()
	final, _ := h.plans.Get("plan-t3-retry")
	if final.State != plan.StateDone {
		t.Fatalf("(5) after re-plan + met re-judge: state=%q, want done", final.State)
	}
}

// TestConformance_g4_ParallelStreamsLint_Design proves the g4 "parallel streams
// (lint)" diagram EXECUTES as drawn. The scoped pkg/plan/lint_test.go tests
// prove each EDGE (disjoint pass, overlap reject, prefix-aware, exploratory
// exempt, join-less reject); this g4 scenario walks the FULL drawn sequence as
// one observed path through the lint seam: disjoint write-sets → lint passes
// (the approve gate opens); overlapping → lint REJECTS at approve
// (ErrValidation); exploratory members are exempt (own git worktree, D10 — no
// write_set, never flagged); join-less convergence is rejected; and a real
// merge-time conflict surfaces as a plan-correction event
// (NewMergeConflictEvent → CorrectionKindMergeConflict).
//
// Drawn path asserted node-by-node:
//  1. disjoint parallel write-sets → lint passes (approve gate opens).
//  2. overlapping write-sets → lint rejects at approve (LintOverlap,
//     ErrValidation) — the approve is blocked.
//  3. exploratory members (empty write_set) are exempt — they declare no
//     footprint (D10: own worktree, conflict is a runtime merge concern).
//  4. a convergence of ≥2 parallel predecessors with no authored join member
//     → lint rejects (LintJoinless).
//  5. a real runtime merge conflict surfaces as a plan-correction event
//     (NewMergeConflictEvent → CorrectionKindMergeConflict) — never silent.
//
// e2e residue: the real go-git merge that produces the conflict (vs the
// typed NewMergeConflictEvent emission point) is the real-git gate; this
// proves the lint + correction-event control plane walks the drawn path.
//
// Traces to: ADR-053 §9.1, design diagram g4 (parallel streams / lint)
func TestConformance_g4_ParallelStreamsLint_Design(t *testing.T) {
	// (1) disjoint parallel write-sets → lint passes (approve gate opens).
	p := &plan.Plan{ID: "plan-g4"}
	disjoint := []task.Task{
		lintMember("stream-a", nil, []string{"pkg/alpha.go"}),
		lintMember("stream-b", nil, []string{"pkg/beta.go"}),
	}
	if lerr := plan.Lint(p, disjoint); lerr != nil {
		t.Fatalf("(1) disjoint parallel streams must pass lint (approve opens), got: %v", lerr)
	}

	// (2) overlapping write-sets → lint REJECTS at approve (ErrValidation).
	overlap := []task.Task{
		lintMember("stream-a", nil, []string{"pkg/shared.go"}),
		lintMember("stream-b", nil, []string{"pkg/shared.go"}),
	}
	lerr := plan.Lint(p, overlap)
	if lerr == nil {
		t.Fatal("(2) overlapping write-sets must be rejected at approve (g4)")
	}
	if !errors.Is(lerr, plan.ErrValidation) {
		t.Fatalf("(2) lint rejection must wrap ErrValidation, got: %v", lerr)
	}
	if lerr.Violations[0].Kind != plan.LintOverlap {
		t.Fatalf("(2) violation kind = %q, want LintOverlap", lerr.Violations[0].Kind)
	}
	// The violation's Kind is the correction-event discriminator (write_set_overlap
	// → CorrectionKindWriteSetOverlap, logged via logCorrectionEvent — "surfaced,
	// never silent"). Assert the public payload fields the CorrectionEvent would
	// carry: both offending member IDs + the overlapping path.
	v := lerr.Violations[0]
	if len(v.MemberIDs) != 2 || v.MemberIDs[0] != "stream-a" || v.MemberIDs[1] != "stream-b" {
		t.Fatalf("(2) overlap violation must name both offending members, got %v", v.MemberIDs)
	}
	if len(v.Paths) == 0 || v.Paths[0] != "pkg/shared.go" {
		t.Fatalf("(2) overlap violation must name the conflicting path, got %v", v.Paths)
	}

	// (3) exploratory members (empty write_set) are exempt (D10: own worktree).
	exploratory := []task.Task{
		lintMember("explore-a", nil, nil),
		lintMember("explore-b", nil, nil),
	}
	if lerr := plan.Lint(p, exploratory); lerr != nil {
		t.Fatalf("(3) exploratory members (no write_set) must be exempt from the overlap check (D10), got: %v", lerr)
	}

	// (4) join-less convergence → rejected (an authored join member is required).
	joinless := []task.Task{
		lintMember("p1", nil, []string{"shards/1.csv"}),
		lintMember("p2", nil, []string{"shards/2.csv"}),
		lintMember("merge", []string{"p1", "p2"}, nil), // converges p1+p2, IsJoin=false.
	}
	lerrJoin := plan.Lint(p, joinless)
	if lerrJoin == nil {
		t.Fatal("(4) a join-less convergence must be rejected (authored join member required)")
	}
	foundJoinless := false
	for _, v := range lerrJoin.Violations {
		if v.Kind == plan.LintJoinless {
			foundJoinless = true
		}
	}
	if !foundJoinless {
		t.Fatalf("(4) expected a LintJoinless violation, got: %+v", lerrJoin.Violations)
	}

	// (5) a real runtime merge conflict surfaces as a plan-correction event
	//     (NewMergeConflictEvent → CorrectionKindMergeConflict) — never silent.
	mergeEv := plan.NewMergeConflictEvent(p.ID, []string{"explore-a", "explore-b"},
		[]string{"pkg/conflict.go"}, "same-file collision at the join")
	if mergeEv.Kind != plan.CorrectionKindMergeConflict {
		t.Fatalf("(5) NewMergeConflictEvent kind = %q, want merge_conflict (plan-correction event)", mergeEv.Kind)
	}
	if mergeEv.PlanID != p.ID || len(mergeEv.MemberIDs) != 2 {
		t.Errorf("(5) merge-correction event must name the plan + colliding members, got %+v", mergeEv)
	}
}

// TestConformance_bootsweep_Design proves the §5 "boot sweep" diagram EXECUTES
// as drawn. The scoped boot_sweep_test.go tests prove each EDGE (non-terminal→
// failed, needs_input reconstructability, awaiting-correction exemption, N-15
// re-baseline); this bootsweep scenario walks the FULL drawn sequence as one
// observed path: kill -9 mid-plan (persisted non-terminal sessions) → every
// non-terminal session with no live turn → failed(interrupted) within budget,
// carrying last checkpoint + undelivered messages → session.failed hook fires
// → plan re-judges/re-dispatches (the awaiting-correction owner is PRESERVED,
// not swept → no wedge, CRIT-1) → an in-flight goal predating the upgrade is
// re-baselined (N-15), not swept.
//
// Drawn path asserted node-by-node:
//  1. kill -9 mid-plan: a running session (with checkpoint + undelivered
//     messages), a queued session, a terminal (completed) session, a paused
//     awaiting-correction owner, and a stale-version goal are persisted.
//  2. boot sweep → the running + queued non-terminal sessions become
//     failed(interrupted) within budget, carrying checkpoint + undelivered;
//     the terminal session is untouched.
//  3. session.failed hook fires for each swept session (→ plan re-judges/
//     re-dispatches downstream).
//  4. CRIT-1 no-wedge: the paused awaiting-correction owner is PRESERVED
//     (exemption b) — not swept, so the plan can recover (no wedge).
//  5. N-15: the in-flight goal predating the upgrade is re-baselined (preserved
//     as running), NOT swept — one sweep, two triggers.
//
// e2e residue: the actual kill -9 + process restart (vs the in-process sweep
// over persisted records) is the real-process gate; this proves the boot-sweep
// control plane walks the drawn path faithfully.
//
// Traces to: ADR-053 §9.1, design diagram §5 (boot sweep)
func TestConformance_bootsweep_Design(t *testing.T) {
	h := newBootSweepHarness(t)

	// (1) kill -9 mid-plan: persist the cross-section of stranded sessions.
	//     A running session with a checkpoint + undelivered messages.
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "bs-running", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "agent-1",
		OwnerScopeKind:        session.OwnerScopeHuman,
		LastCheckpointRef:     "ckpt-bs",
		UndeliveredMessageIDs: []string{"bs-msg-1", "bs-msg-2"},
		CreatedAt:             time.Now().Add(-1 * time.Hour),
	})
	// A queued session — also non-terminal, also swept.
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "bs-queued", Generation: 1, State: session.LifecycleQueued,
		WorkspaceID: "ws", AgentID: "agent-1",
		OwnerScopeKind: session.OwnerScopeHuman,
	})
	// A terminal session — MUST be left alone.
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "bs-done", Generation: 1, State: session.LifecycleCompleted,
		WorkspaceID: "ws", AgentID: "agent-1",
		OwnerScopeKind: session.OwnerScopeHuman,
	})
	// CRIT-1: a paused awaiting-correction owner (exemption b) — preserved.
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "plan-bs", Title: "plan-bs", WorkspaceID: "ws", OwnerAgentID: "owner",
		State: plan.StateRunning, PlanPhase: plan.PhaseAwaitingSupervision,
		LastUnmetTerminalSignature: "sig-bs",
	})
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "bs-owner", Generation: 1, State: session.LifecyclePaused,
		WorkspaceID: "ws", AgentID: "owner",
		OwnerScopeKind: session.OwnerScopeHuman, OwnsPlanID: "plan-bs",
	})
	// N-15: an in-flight goal predating the upgrade (stale semantics version).
	h.pe.currentSemanticsVersionOverride = 3
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "bs-stale-goal", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "agent-1",
		OwnerScopeKind: session.OwnerScopeHuman, GoalRef: "goal-bs",
	})
	h.pe.SetGoalSemanticsVersioner(func(sid string) int {
		if sid == "bs-stale-goal" {
			return 1 // predates the current build (3)
		}
		return 3
	})

	// (2)+(3) boot sweep → non-terminal sessions swept to failed(interrupted)
	// within budget, checkpoint + undelivered carried; session.failed hook fires.
	var failedHooked []string
	h.pe.SetSessionFailedHook(func(sid, reason string) {
		if reason != failedReasonInterrupted {
			t.Errorf("(3) hook reason = %q, want %q", reason, failedReasonInterrupted)
		}
		failedHooked = append(failedHooked, sid)
	})
	res := h.pe.runBootSweep(context.Background())

	if len(res.SweptToFailed) != 2 {
		t.Fatalf("(2) SweptToFailed = %v, want exactly 2 (running+queued; terminal excluded)", res.SweptToFailed)
	}
	if len(failedHooked) != 2 {
		t.Fatalf("(3) session.failed hook fired %d times, want 2 (→ plan re-judges/re-dispatches)", len(failedHooked))
	}
	swept, _ := h.ls.Load("bs-running")
	if swept.State != session.LifecycleFailed || swept.FailedReason != failedReasonInterrupted {
		t.Fatalf("(2) bs-running state=%q reason=%q, want failed/interrupted", swept.State, swept.FailedReason)
	}
	if swept.LastCheckpointRef != "ckpt-bs" || len(swept.UndeliveredMessageIDs) != 2 {
		t.Errorf("(2) swept record must carry checkpoint + undelivered, got ckpt=%q undelivered=%v",
			swept.LastCheckpointRef, swept.UndeliveredMessageIDs)
	}
	// the terminal session is untouched.
	done, _ := h.ls.Load("bs-done")
	if done.State != session.LifecycleCompleted {
		t.Errorf("(2) terminal session changed state: %q (must be untouched)", done.State)
	}

	// (4) CRIT-1 no-wedge: the awaiting-correction owner is PRESERVED, not swept.
	if len(res.PreservedAwaitingCorrection) != 1 || res.PreservedAwaitingCorrection[0] != "bs-owner" {
		t.Fatalf("(4) CRIT-1: PreservedAwaitingCorrection = %v, want [bs-owner] (no wedge)", res.PreservedAwaitingCorrection)
	}
	owner, _ := h.ls.Load("bs-owner")
	if owner.State != session.LifecyclePaused {
		t.Errorf("(4) CRIT-1: owner swept to %q (must stay paused — exemption b, no wedge)", owner.State)
	}

	// (5) N-15: the stale-version goal is re-baselined (preserved as running),
	//     NOT swept — one sweep, two triggers.
	if len(res.RebaselinedGoals) != 1 || res.RebaselinedGoals[0] != "bs-stale-goal" {
		t.Fatalf("(5) N-15: RebaselinedGoals = %v, want [bs-stale-goal] (in-flight goal re-baselined, not swept)",
			res.RebaselinedGoals)
	}
	staleGoal, _ := h.ls.Load("bs-stale-goal")
	if staleGoal.State != session.LifecycleRunning {
		t.Errorf("(5) N-15: re-baselined goal state = %q, want running (preserved, not swept)", staleGoal.State)
	}
}
