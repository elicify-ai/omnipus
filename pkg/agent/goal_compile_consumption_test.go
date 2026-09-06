// Omnipus — ADR-080 goal-compile CONSUMPTION wave tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_compile_consumption_test.go covers the goal-compile consumption half
// of ADR-080: the Judge scoring criteria UNION dod (§120's "judged-set union
// seam"), the shared statement/criteria/DoD echo on every surface (§122's
// "formatGoalEcho on EVERY surface"), and the judgment tag on every echo
// line (D-TYPES). The frame-emission half (definition/dod riding the
// `queued` GoalStatusFrame) is covered by
// goal_status_criteria_wire_test.go's TestGoalStatus_QueuedEmissionCarriesDefinitionAndDoD.
package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- formatGoalEcho / formatGoalStatementAndCriteria (unit, no harness) ---

// TestFormatGoalEcho_StatementCriteriaJudgmentAndDoDInferredFlag is ADR-080's
// required test 8/§122: formatGoalEcho renders the restated statement (D-
// STATEMENT) BEFORE the criteria, each criterion's judgment tag, and a
// DISTINCT "Definition of Done" block after the criteria with
// `provenance == inferred` items flagged "(inferred — confirm or drop)" —
// while a floor-provenance item stays unflagged.
func TestFormatGoalEcho_StatementCriteriaJudgmentAndDoDInferredFlag(t *testing.T) {
	author := task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "u1"}
	g := &CompiledGoal{
		Intent:     "ship the release",
		Prompt:     "ship the release",
		Definition: "Ship the v2 release to production.",
		Criteria: []task.AcceptanceCriterion{
			{ID: "c1", Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "the changelog is accurate", Author: author},
			{ID: "c2", Kind: task.KindProse, Judgment: task.JudgmentQuantitative, Text: "at least 3 reviewers approved", Author: author},
		},
		DoD: []task.AcceptanceCriterion{
			{
				ID: "d1", Kind: task.KindProse, Judgment: task.JudgmentBoolean, Provenance: task.ProvenanceFloor,
				Text: "no secrets or credentials appear in the output", Author: author,
			},
			{
				ID: "d2", Kind: task.KindProse, Judgment: task.JudgmentArtifact, Provenance: task.ProvenanceInferred,
				Text: "a rollback plan is documented", Author: author,
			},
		},
	}

	echo := formatGoalEcho(g)

	// D-STATEMENT: the compiled restatement leads, not Prompt/Intent.
	if !strings.Contains(echo, "Ship the v2 release to production.") {
		t.Fatalf("echo missing the restated Definition (D-STATEMENT), got:\n%s", echo)
	}
	if strings.Contains(echo, "Goal: ship the release") {
		t.Fatalf("echo used the Prompt/Intent fallback despite a non-empty Definition, got:\n%s", echo)
	}

	// Criteria still present, and the statement precedes them.
	statementIdx := strings.Index(echo, "Ship the v2 release to production.")
	criteriaIdx := strings.Index(echo, "the changelog is accurate")
	if statementIdx < 0 || criteriaIdx < 0 || statementIdx > criteriaIdx {
		t.Fatalf("statement must precede the criteria breakdown, got:\n%s", echo)
	}

	// D-TYPES: every criterion carries a judgment tag now.
	if !strings.Contains(echo, "the changelog is accurate (judged yes/no)") {
		t.Errorf("boolean criterion missing its judgment tag, got:\n%s", echo)
	}
	if !strings.Contains(echo, "at least 3 reviewers approved (judged against a measured value)") {
		t.Errorf("quantitative criterion missing its judgment tag, got:\n%s", echo)
	}

	// D-DOD: a DISTINCT block after the criteria.
	dodHeaderIdx := strings.Index(echo, "Definition of Done")
	if dodHeaderIdx < 0 || dodHeaderIdx < criteriaIdx {
		t.Fatalf("echo missing a DoD block after the criteria, got:\n%s", echo)
	}

	// Floor item: NOT flagged inferred.
	floorIdx := strings.Index(echo, "no secrets or credentials appear in the output")
	if floorIdx < 0 {
		t.Fatalf("echo missing the floor DoD item, got:\n%s", echo)
	}
	floorLineEnd := strings.Index(echo[floorIdx:], "\n")
	floorLine := echo[floorIdx : floorIdx+floorLineEnd]
	if strings.Contains(floorLine, "inferred") {
		t.Errorf("floor-provenance DoD item must NOT be flagged inferred, got line:\n%s", floorLine)
	}

	// Inferred item: flagged for approve/drop.
	if !strings.Contains(echo, "a rollback plan is documented") {
		t.Fatalf("echo missing the inferred DoD item, got:\n%s", echo)
	}
	inferredIdx := strings.Index(echo, "a rollback plan is documented")
	inferredLineEnd := strings.Index(echo[inferredIdx:], "\n")
	inferredLine := echo[inferredIdx : inferredIdx+inferredLineEnd]
	if !strings.Contains(inferredLine, "(inferred — confirm or drop)") {
		t.Errorf("inferred DoD item must be flagged \"(inferred — confirm or drop)\", got line:\n%s", inferredLine)
	}
}

// TestFormatGoalEcho_NoDoDOmitsBlock proves the DoD block is genuinely
// conditional (a CompiledGoal with zero DoD items — e.g. a very early
// deterministic-fallback compile, before any load-time floor backfill —
// renders no "Definition of Done" heading at all, rather than an empty one).
func TestFormatGoalEcho_NoDoDOmitsBlock(t *testing.T) {
	g := &CompiledGoal{
		Intent: "x", Prompt: "x",
		Criteria: []task.AcceptanceCriterion{
			{ID: "c1", Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "x is done",
				Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "u1"}},
		},
	}
	echo := formatGoalEcho(g)
	if strings.Contains(echo, "Definition of Done") {
		t.Fatalf("echo must not render a DoD block when DoD is empty, got:\n%s", echo)
	}
}

// --- compiledGoalCriteriaFor union seam (unit, no harness) ---------------

// TestCompiledGoalCriteriaFor_UnionsCriteriaAndDoD is ADR-080 §120's
// required coverage: the goal-adjudication criteria assembly returns
// Criteria UNION DoD, both fully present and disjoint by ID, whenever a
// compiled ladder carries a DoD.
func TestCompiledGoalCriteriaFor_UnionsCriteriaAndDoD(t *testing.T) {
	author := task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "u1"}
	g := &CompiledGoal{
		Intent: "x", Prompt: "x",
		Criteria: []task.AcceptanceCriterion{
			{ID: "c1", Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "criterion one", Author: author, Status: task.CritPending},
			{ID: "c2", Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "criterion two", Author: author, Status: task.CritPending},
		},
		DoD: []task.AcceptanceCriterion{
			{ID: "d1", Kind: task.KindProse, Judgment: task.JudgmentBoolean, Provenance: task.ProvenanceFloor,
				Text: "dod one", Author: author, Status: task.CritPending},
		},
	}
	raw, err := marshalCompiledGoal(g)
	if err != nil {
		t.Fatalf("marshalCompiledGoal: %v", err)
	}

	got := compiledGoalCriteriaFor(raw, "unused-fallback-condition", "sid")
	if len(got) != 3 {
		t.Fatalf("compiledGoalCriteriaFor returned %d criteria, want 3 (2 criteria + 1 dod): %+v", len(got), got)
	}
	seen := map[string]bool{}
	for _, c := range got {
		seen[c.ID] = true
	}
	for _, id := range []string{"c1", "c2", "d1"} {
		if !seen[id] {
			t.Errorf("union missing criterion id %q, got ids=%v", id, seen)
		}
	}
}

// TestCompiledGoalCriteriaFor_NoDoDReturnsCriteriaOnly proves the union does
// not fabricate anything when the compiled ladder has no DoD at all — the
// zero-DoD branch stays criteria-only (distinct from every OTHER read path
// in this codebase, which backfills a floor DoD via loadCompiledGoal; this
// function's own contract is "union of what loadCompiledGoal returns").
func TestCompiledGoalCriteriaFor_NoDoDReturnsCriteriaOnly(t *testing.T) {
	got := compiledGoalCriteriaFor("", "the fallback condition text", "sid")
	if len(got) != 1 || got[0].Text != "the fallback condition text" {
		t.Fatalf("empty rawJSON must fall back to the single back-compat criterion, got %+v", got)
	}
}

// --- buildGoalPendingNote shares the same DoD/inferred rendering ---------

// TestBuildGoalPendingNote_RendersDoDInferredFlag proves buildGoalPendingNote
// (goal_pending_note.go) shares formatGoalStatementAndCriteria with
// formatGoalEcho — an inferred DoD item is visible to the MODEL's own
// per-turn context, not just the web card (ADR-080 R-C2).
func TestBuildGoalPendingNote_RendersDoDInferredFlag(t *testing.T) {
	al, agentInst, _, store, sid, opts := twoPhaseHarness(t,
		func(_ int, _ []providers.Message) (*providers.LLMResponse, error) {
			return &providers.LLMResponse{Content: `{"assessment":{"clarity":"clear"},` +
				`"definition":"Ship the v2 release to production.",` +
				`"criteria":[{"text":"the changelog is accurate","judgment":"boolean"}],` +
				`"dod":[{"text":"a rollback plan is documented","judgment":"artifact","provenance":"inferred"}]}`}, nil
		}, nil)
	setGoal(t, al, agentInst, opts, "ship the release")

	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if meta.GoalPendingJSON == "" {
		t.Fatal("setup: pending goal required")
	}

	note := buildGoalPendingNote(store, sid)
	if note == "" {
		t.Fatal("buildGoalPendingNote returned empty for a fresh pending goal")
	}
	if !strings.Contains(note, "Ship the v2 release to production.") {
		t.Fatalf("note missing the restated statement, got:\n%s", note)
	}
	if !strings.Contains(note, "Definition of Done") {
		t.Fatalf("note missing the DoD block, got:\n%s", note)
	}
	if !strings.Contains(note, "a rollback plan is documented") ||
		!strings.Contains(note, "(inferred — confirm or drop)") {
		t.Fatalf("note missing the inferred DoD item's flag, got:\n%s", note)
	}
}

// --- Judged-set union seam, end to end (ADR-080 §120 / R-C1) -------------

// unmetDoDJudgeProvider returns a fake Judge LLM provider that reports every
// criterion in compiled.Criteria MET but every item in compiled.DoD UNMET —
// isolating a DoD-only failure (no acceptance criterion is at fault) to
// prove the union actually reaches adjudication and an unmet DoD item alone
// fails the round.
func unmetDoDJudgeProvider(compiled *CompiledGoal, dodReason string) *fakeJudgeProvider {
	var entries []string
	for _, c := range compiled.Criteria {
		entries = append(entries, fmt.Sprintf(`{"id":%q,"met":true,"reason":"criterion met"}`, c.ID))
	}
	for _, d := range compiled.DoD {
		entries = append(entries, fmt.Sprintf(`{"id":%q,"met":false,"reason":%q}`, d.ID, dodReason))
	}
	body := `{"met":false,"criteria":[` + strings.Join(entries, ",") + `]}`
	return &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: body}, nil
	}}
}

// TestGoalAdjudication_UnmetDoDFailsRound is ADR-080's required regression
// test 7a: the goal-adjudication criteria assembly (compiledGoalCriteriaFor)
// feeds Criteria UNION DoD into runVerifierAdjudication; every acceptance
// criterion is MET but the (load-time-backfilled floor) DoD item is UNMET —
// the round must still fail (goal stays active, one round consumed), never
// silently clear on "all real criteria met".
func TestGoalAdjudication_UnmetDoDFailsRound(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal ship the release", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)

	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	compiled := loadCompiledGoal(meta.GoalCriteriaJSON)
	if compiled == nil || len(compiled.Criteria) == 0 {
		t.Fatalf("setup: compile must produce criteria, got GoalCriteriaJSON=%q", meta.GoalCriteriaJSON)
	}
	if len(compiled.DoD) == 0 {
		t.Fatal("setup: the compiled goal must carry at least one DoD item (built-in floor, via load-time backfill)")
	}

	judgeInst.Provider = unmetDoDJudgeProvider(compiled, "secret token found in the diff")

	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, &turnResult{
		finalContent: "[goal:evidence] release shipped\nGOAL_STATUS: met",
	})

	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatalf("GetMeta after adjudication: %v", err)
	}
	if after.GoalCondition == "" {
		t.Fatal("an unmet DoD item must fail the round — the goal must still be ACTIVE, not cleared as done")
	}
	if after.GoalRoundsUsed != 1 {
		t.Fatalf("an unmet-but-rounds-remaining verdict must consume exactly 1 round, got %d", after.GoalRoundsUsed)
	}
	if !strings.Contains(after.GoalLatestReason, "secret token found in the diff") {
		t.Errorf("latest reason must surface the DoD item's own unmet reason, got %q", after.GoalLatestReason)
	}
}
