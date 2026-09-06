// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_two_phase_test.go covers ADR-074 D4a's two-phase `/goal` (judgment-first
// spec US-3, FR-006/FR-007, tests 10, 11, 11b, 11c, 12, 13, 14, 14b, 14c, 14d,
// 22) with a stubbed LLM seam: the goal-bearing agent's own Provider is the
// compile seam (goalCompileLLMCall reads agentInst.Provider), so a scripted
// provider double is swapped onto the worker agent exactly the way judge tests
// swap judgeInst.Provider.

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// scriptedCompileProvider is an LLM double for the goal-compile seam: script
// receives the 1-based call number AND the messages, so tests can assert what
// the engine put into the compile context (define-goal injection, repair
// feedback, the clarification answer) and script different responses per call.
type scriptedCompileProvider struct {
	mu     sync.Mutex
	calls  int
	msgs   [][]providers.Message
	script func(call int, messages []providers.Message) (*providers.LLMResponse, error)
}

func (p *scriptedCompileProvider) Chat(
	_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	cp := make([]providers.Message, len(messages))
	copy(cp, messages)
	p.msgs = append(p.msgs, cp)
	p.mu.Unlock()
	return p.script(n, messages)
}

func (p *scriptedCompileProvider) GetDefaultModel() string { return "scripted-compile-model" }

func (p *scriptedCompileProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *scriptedCompileProvider) messagesOfCall(n int) []providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 1 || n > len(p.msgs) {
		return nil
	}
	return p.msgs[n-1]
}

// compileJSON builds a "clear"-branch compile response (ADR-079 D2, ADR-080
// D-STATEMENT/D-TYPES/D-DOD): a definition, each criterion tagged
// judgment:"boolean" (the honestly-subjective default, D-TYPES), and a
// single floor-provenance DoD item (the built-in guarantee that a compile
// never emits an empty dod). Tests that need a specific definition/judgment/
// dod shape call parseGoalCompileResponse or construct the JSON directly
// (see TestParseGoalCompileResponse_SchemaEnforcesINV1D2D-TYPES below).
func compileJSON(criteria ...string) *providers.LLMResponse {
	var sb strings.Builder
	sb.WriteString(`{"assessment":{"clarity":"clear"},"definition":"the goal is compiled","criteria":[`)
	for i, c := range criteria {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"text":"` + c + `","judgment":"boolean"}`)
	}
	sb.WriteString(`],"dod":[{"text":"no secrets or credentials appear in the output",` +
		`"judgment":"boolean","provenance":"floor"}]}`)
	return &providers.LLMResponse{Content: sb.String()}
}

// questionJSON builds an "ambiguous"-branch compile response (ADR-079 D2/D3)
// carrying exactly one clarifying question, shaped as a full askuser.Question
// (header + question text + a valid 2-option answer menu — every real
// AskUserQuestion needs options, MinOptions=2, even though free text is
// always ALSO available). joinClarifyingQuestions collapses a 1-element
// slice to the bare .Question text, so every existing single-question
// substring assertion in this file keeps working unchanged.
func questionJSON(q string) *providers.LLMResponse {
	return &providers.LLMResponse{
		Content: `{"assessment":{"clarity":"ambiguous"},"clarifying_questions":[` +
			`{"header":"Q1","question":"` + q + `","options":[{"label":"Option A"},{"label":"Option B"}]}` +
			`]}`,
	}
}

// twoPhaseHarness builds the loop with a scripted compile provider on the
// goal-bearing agent and returns everything the scenarios below need.
func twoPhaseHarness(
	t *testing.T, script func(call int, messages []providers.Message) (*providers.LLMResponse, error),
	mutateCfg func(*config.Config),
) (*AgentLoop, *AgentInstance, *scriptedCompileProvider, *session.UnifiedStore, string, *processOptions) {
	t.Helper()
	provider := &scriptedCompileProvider{script: script}
	al, _ := newGoalLoopTestLoop(t, provider, mutateCfg)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := &processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	return al, agentInst, provider, store, sid, opts
}

func setGoal(t *testing.T, al *AgentLoop, agentInst *AgentInstance, opts *processOptions, intent string) (bool, bool, string) {
	t.Helper()
	return al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal " + intent, UserInitiated: true}, agentInst, opts)
}

// assertNoKindTokens is spec US-6 S4's negative assertion: `[kind]`
// classification labels are never user-facing content in the echo.
func assertNoKindTokens(t *testing.T, echo string) {
	t.Helper()
	for _, tok := range []string{"[prose]", "[check]", "[behavior]", "[" + string(task.KindProse) + "]"} {
		if strings.Contains(echo, tok) {
			t.Fatalf("echo must not carry %q kind tokens, got:\n%s", tok, echo)
		}
	}
}

// --- Test 10 (US-3 S3): marker-only pinned — zero LLM, immediate ----------

func TestGoalTwoPhase_MarkerOnly_ZeroLLM_ImmediateActivation(t *testing.T) {
	al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return nil, errors.New("marker-only goals must never reach the LLM compile")
		}, nil)
	allowBashPolicy(agentInst) // [tests] compiles a bash-run check criterion

	matched, handled, _ := setGoal(t, al, agentInst, opts, "[tests]")
	if !matched || handled {
		t.Fatalf("marker-only set: matched=%v handled=%v, want matched=true handled=false (same-turn round 1)", matched, handled)
	}
	if provider.callCount() != 0 {
		t.Fatalf("marker-only set made %d LLM calls, want 0 (US-3 S3 pinned)", provider.callCount())
	}
	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if meta.GoalCondition == "" {
		t.Fatal("marker-only goal must activate immediately (no confirm step)")
	}
	if meta.GoalPendingJSON != "" {
		t.Fatal("marker-only goal must not park as pending")
	}
	if opts.UserMessage == "" {
		t.Fatal("marker-only set must rewrite opts.UserMessage for the same-turn round 1")
	}
}

// --- Test 11 (US-3 S1/S6): prose → pending+echo+confirm; admission pre-compile

func TestGoalTwoPhase_Prose_PendingEchoThenConfirmActivates(t *testing.T) {
	al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return compileJSON("the report is saved to report.md", "the summary names all three sources"), nil
		}, nil)

	matched, handled, echo := setGoal(t, al, agentInst, opts, "write the research report")
	if !matched || !handled {
		t.Fatalf("prose set: matched=%v handled=%v, want both true (pending echo answers synchronously)", matched, handled)
	}
	if provider.callCount() != 1 {
		t.Fatalf("prose set made %d LLM calls, want exactly 1", provider.callCount())
	}
	for _, want := range []string{"report.md", "all three sources", ConfirmGoalWord} {
		if !strings.Contains(echo, want) {
			t.Fatalf("echo missing %q:\n%s", want, echo)
		}
	}
	assertNoKindTokens(t, echo)
	if strings.Contains(echo, "quality-bar rewrite was unavailable") {
		t.Fatalf("a successful LLM compile must not carry the fallback note:\n%s", echo)
	}

	mid, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if mid.GoalCondition != "" {
		t.Fatalf("prose goal must NOT activate before confirm, condition=%q", mid.GoalCondition)
	}
	if mid.GoalPendingJSON == "" {
		t.Fatal("prose compile must park as a pending goal")
	}
	if mid.GoalID != "" {
		t.Fatal("no goal-id generation may be minted before confirm (newGoalID contract)")
	}

	// Confirm activates and rewrites the turn into round 1.
	matched, handled, _ = al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal confirm", UserInitiated: true}, agentInst, opts)
	if !matched || handled {
		t.Fatalf("confirm: matched=%v handled=%v, want matched=true handled=false", matched, handled)
	}
	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalCondition == "" || after.GoalID == "" || after.GoalRoundsUsed != 0 {
		t.Fatalf("confirm must activate a fresh generation, got %+v", after)
	}
	activated := loadCompiledGoal(after.GoalCriteriaJSON)
	if activated == nil || len(activated.Criteria) != 2 {
		t.Fatalf("activated ladder must carry the 2 compiled prose criteria, got %+v", activated)
	}
	for _, c := range activated.Criteria {
		if c.Kind != task.KindProse {
			t.Fatalf("LLM-compiled criterion kind = %q, want prose only (INV-1)", c.Kind)
		}
	}
	if opts.UserMessage == "" || !strings.Contains(opts.UserMessage, "write the research report") {
		t.Fatalf("confirm must rewrite opts.UserMessage to the condition, got %q", opts.UserMessage)
	}
}

func TestGoalTwoPhase_AdmissionCheckedBeforeCompile(t *testing.T) {
	al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return compileJSON("anything"), nil
		}, func(cfg *config.Config) {
			cfg.Planning.GlobalActiveLoopCap = 1
		})
	pe := NewPlanEngine(al, plan.New(t.TempDir()), nil, nil)
	pe.RegisterActiveCounter("goal", func() (int, error) { return 1, nil }) // cap full
	al.SetPlanEngine(pe)

	matched, handled, reply := setGoal(t, al, agentInst, opts, "write the research report")
	if !matched || !handled {
		t.Fatalf("matched=%v handled=%v, want both true (refusal answers synchronously)", matched, handled)
	}
	if !strings.Contains(reply, "active loops") {
		t.Fatalf("reply = %q, want the cap-reached refusal", reply)
	}
	if provider.callCount() != 0 {
		t.Fatalf("a refused admission must cost ZERO compile calls (US-3 S6), got %d", provider.callCount())
	}
	meta, _ := store.GetMeta(sid)
	if meta.GoalPendingJSON != "" || meta.GoalCondition != "" {
		t.Fatal("nothing may persist when admission is refused pre-compile")
	}
}

// --- Test 11b (US-3 S2): mixed intent — marker byte-identical through the LLM path

func TestGoalTwoPhase_Mixed_MarkerByteIdenticalThroughLLMPath(t *testing.T) {
	const intent = "ship the feature [check: go vet ./... exit:2] and the docs read well"
	al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return compileJSON("the docs read well for a newcomer"), nil
		}, nil)
	allowBashPolicy(agentInst)

	_, handled, echo := setGoal(t, al, agentInst, opts, intent)
	if !handled {
		t.Fatal("mixed intent must take the pending path")
	}
	if provider.callCount() != 1 {
		t.Fatalf("mixed set made %d LLM calls, want 1", provider.callCount())
	}
	meta, _ := store.GetMeta(sid)
	pending := loadCompiledGoal(meta.GoalPendingJSON)
	if pending == nil {
		t.Fatal("mixed compile must park as pending")
	}

	// The marker criterion must be BYTE-IDENTICAL to the deterministic parse
	// of the same intent (INV-1: the LLM never authors/rewrites technical
	// payloads). Compare against parseIntentMarkers' own output.
	wantCriteria, _, _ := parseIntentMarkers(intent, sid)
	if len(wantCriteria) != 1 || wantCriteria[0].Check == nil {
		t.Fatalf("test setup: deterministic parse should yield 1 check criterion, got %+v", wantCriteria)
	}
	var gotCheck *task.AcceptanceCriterion
	proseCount := 0
	for i := range pending.Criteria {
		switch pending.Criteria[i].Kind {
		case task.KindCheck:
			gotCheck = &pending.Criteria[i]
		case task.KindProse:
			proseCount++
		}
	}
	if gotCheck == nil {
		t.Fatalf("pending ladder lost the marker check criterion: %+v", pending.Criteria)
	}
	if gotCheck.Check.Command != wantCriteria[0].Check.Command ||
		gotCheck.Check.ExpectedExitCode != wantCriteria[0].Check.ExpectedExitCode ||
		gotCheck.Text != wantCriteria[0].Text {
		t.Fatalf("marker criterion not byte-identical to the deterministic parse:\n got %+v\nwant %+v",
			gotCheck, wantCriteria[0])
	}
	if proseCount != 1 {
		t.Fatalf("prose remainder must arrive as exactly the LLM's 1 prose criterion, got %d", proseCount)
	}
	// The verbatim command survives into the echo (FR-113 substance).
	if !strings.Contains(echo, "go vet ./...") {
		t.Fatalf("echo must carry the literal command verbatim:\n%s", echo)
	}
}

// --- Test 11c (INV-1): the compile schema rejects technical kinds ----------

// goodDoD is a minimal valid dod array shared by the "bad" fixtures below so
// each one isolates the ONE schema violation it names, rather than also
// tripping the (separately tested) "dod required" rule.
const goodDoD = `"dod":[{"text":"g","judgment":"boolean","provenance":"floor"}]`

func TestParseGoalCompileResponse_SchemaEnforcesINV1(t *testing.T) {
	bad := []string{
		// INV-1: technical payload keys on a criterion are a hard schema error.
		`{"assessment":{"clarity":"clear"},"definition":"d",` + goodDoD +
			`,"criteria":[{"text":"x","judgment":"boolean","kind":"check"}]}`,
		`{"assessment":{"clarity":"clear"},"definition":"d",` + goodDoD +
			`,"criteria":[{"text":"x","judgment":"boolean","check":{"command":"rm -rf /"}}]}`,
		`{"assessment":{"clarity":"clear"},"definition":"d",` + goodDoD +
			`,"criteria":[{"text":"x","judgment":"boolean","behavior":{"tool":"bash"}}]}`,
		`{"assessment":{"clarity":"clear"},"definition":"d",` + goodDoD + `,"criteria":[{"tool":"bash"}]}`,
		`{"assessment":{"clarity":"clear"},"definition":"d",` + goodDoD +
			`,"criteria":[{"text":"x","judgment":"boolean","command":"true"}]}`,
		`{"assessment":{"clarity":"clear"},"definition":"d",` + goodDoD + `,"criteria":[{"text":""}]}`,
		// both halves / neither half / no JSON at all.
		`{"assessment":{"clarity":"clear"},"definition":"d",` + goodDoD +
			`,"criteria":[{"text":"x","judgment":"boolean"}],"clarifying_questions":["both?"]}`,
		`{}`,
		`no json here at all`,
		// missing/invalid assessment.clarity.
		`{"definition":"d",` + goodDoD + `,"criteria":[{"text":"x","judgment":"boolean"}]}`,
		`{"assessment":{"clarity":"maybe"},"definition":"d",` + goodDoD +
			`,"criteria":[{"text":"x","judgment":"boolean"}]}`,
		// ADR-080 D-TYPES: a criterion missing/invalid judgment is rejected.
		`{"assessment":{"clarity":"clear"},"definition":"d",` + goodDoD + `,"criteria":[{"text":"x"}]}`,
		`{"assessment":{"clarity":"clear"},"definition":"d",` + goodDoD +
			`,"criteria":[{"text":"x","judgment":"maybe"}]}`,
		// ADR-080 D-STATEMENT: clear requires a non-empty definition.
		`{"assessment":{"clarity":"clear"},` + goodDoD + `,"criteria":[{"text":"x","judgment":"boolean"}]}`,
		// ADR-080 D-DOD: clear requires a non-empty dod, each item judgment-
		// and provenance-tagged.
		`{"assessment":{"clarity":"clear"},"definition":"d","criteria":[{"text":"x","judgment":"boolean"}]}`,
		`{"assessment":{"clarity":"clear"},"definition":"d","criteria":[{"text":"x","judgment":"boolean"}],"dod":[]}`,
		`{"assessment":{"clarity":"clear"},"definition":"d","criteria":[{"text":"x","judgment":"boolean"}],` +
			`"dod":[{"text":"g","judgment":"boolean"}]}`, // missing provenance
		`{"assessment":{"clarity":"clear"},"definition":"d","criteria":[{"text":"x","judgment":"boolean"}],` +
			`"dod":[{"text":"g","provenance":"floor"}]}`, // missing judgment
		// ambiguous must not carry the clear-branch fields.
		`{"assessment":{"clarity":"ambiguous"},"clarifying_questions":["q"],` +
			`"criteria":[{"text":"x","judgment":"boolean"}]}`,
		`{"assessment":{"clarity":"ambiguous"}}`, // no questions
	}
	for _, raw := range bad {
		if _, err := parseGoalCompileResponse(raw); err == nil {
			t.Errorf("parseGoalCompileResponse(%q) accepted, want a schema error (INV-1/ADR-079 D2/ADR-080)", raw)
		}
	}

	good, err := parseGoalCompileResponse("Here you go:\n```json\n" +
		`{"assessment":{"clarity":"clear"},"definition":"the doc is finished",` +
		`"criteria":[{"text":"the doc is written","judgment":"boolean"}],` + goodDoD + `}` +
		"\n```")
	if err != nil {
		t.Fatalf("valid fenced compile response rejected: %v", err)
	}
	if good.Clarity != "clear" {
		t.Fatalf("Clarity = %q, want \"clear\"", good.Clarity)
	}
	if good.Definition != "the doc is finished" {
		t.Fatalf("Definition = %q", good.Definition)
	}
	if len(good.Criteria) != 1 || good.Criteria[0].Text != "the doc is written" ||
		good.Criteria[0].Judgment != task.JudgmentBoolean {
		t.Fatalf("unexpected criteria parse: %+v", good.Criteria)
	}
	if len(good.DoD) != 1 || good.DoD[0].Text != "g" ||
		good.DoD[0].Judgment != task.JudgmentBoolean || good.DoD[0].Provenance != task.ProvenanceFloor {
		t.Fatalf("unexpected dod parse: %+v", good.DoD)
	}

	q, err := parseGoalCompileResponse(`{"assessment":{"clarity":"ambiguous"},"clarifying_questions":[` +
		`{"header":"Repo","question":"Which repo?","options":[{"label":"omnipus"},{"label":"other"}]}` +
		`]}`)
	if err != nil {
		t.Fatalf("valid question rejected: %v", err)
	}
	if q.Clarity != "ambiguous" || len(q.ClarifyingQuestions) != 1 ||
		q.ClarifyingQuestions[0].Question != "Which repo?" || q.ClarifyingQuestions[0].Header != "Repo" {
		t.Fatalf("unexpected question parse: %+v", q)
	}
}

// TestParseGoalCompileResponse_AmbiguousQuestionNeedsOptions is ADR-079 D3's
// schema extension: a clarifying question with no options (or too few) is a
// hard schema error — every real askuser.Question needs 2-6 real answer
// options (free text is always ALSO available, never a substitute).
func TestParseGoalCompileResponse_AmbiguousQuestionNeedsOptions(t *testing.T) {
	bad := []string{
		// No options field at all.
		`{"assessment":{"clarity":"ambiguous"},"clarifying_questions":[{"header":"H","question":"q?"}]}`,
		// Only one option (MinOptions=2).
		`{"assessment":{"clarity":"ambiguous"},"clarifying_questions":[` +
			`{"header":"H","question":"q?","options":[{"label":"only one"}]}]}`,
		// Missing header.
		`{"assessment":{"clarity":"ambiguous"},"clarifying_questions":[` +
			`{"question":"q?","options":[{"label":"a"},{"label":"b"}]}]}`,
	}
	for _, raw := range bad {
		if _, err := parseGoalCompileResponse(raw); err == nil {
			t.Errorf("parseGoalCompileResponse(%q) accepted, want a schema error (ADR-079 D3 options requirement)", raw)
		}
	}
}

// --- Test 12 (US-3 S4): LLM failure → observable deterministic fallback ----

func TestGoalTwoPhase_LLMFailure_FallbackPendingObservable(t *testing.T) {
	al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return nil, errors.New("provider down")
		}, nil)

	before := goalCompileFallbacksTotal()
	_, handled, echo := setGoal(t, al, agentInst, opts, "make the tests pass")
	if !handled {
		t.Fatal("failed compile must still answer with the (fallback) pending echo")
	}
	if got := goalCompileFallbacksTotal(); got != before+1 {
		t.Fatalf("fallback counter = %d, want %d (FR-014: one increment per fallback)", got, before+1)
	}
	if !strings.Contains(echo, "quality-bar rewrite was unavailable") {
		t.Fatalf("fallback echo must carry the no-rewrite note (US-3 S4):\n%s", echo)
	}
	assertNoKindTokens(t, echo)
	if provider.callCount() != 1 {
		t.Fatalf("want exactly 1 (failed) compile attempt, got %d", provider.callCount())
	}

	// Every prose path — fallbacks included — still ends at the confirm gate.
	mid, _ := store.GetMeta(sid)
	if mid.GoalCondition != "" || mid.GoalPendingJSON == "" {
		t.Fatalf("fallback must park pending, not activate: %+v", mid)
	}
	matched, handled2, _ := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal confirm", UserInitiated: true}, agentInst, opts)
	if !matched || handled2 {
		t.Fatal("fallback pending must be confirmable")
	}
	after, _ := store.GetMeta(sid)
	if after.GoalCondition != "make the tests pass" {
		t.Fatalf("condition = %q after confirm", after.GoalCondition)
	}
}

// --- Test 13 (US-3 S5): veto → one repair → fallback; plain-language ------

func TestGoalTwoPhase_VetoRepairThenFallback(t *testing.T) {
	al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
		func(call int, _ []providers.Message) (*providers.LLMResponse, error) {
			// Both the compile and its single repair produce a hedging-only
			// prose criterion the D9 gate vetoes (no observable referent).
			return compileJSON("feels good"), nil
		}, nil)

	before := goalCompileFallbacksTotal()
	_, handled, echo := setGoal(t, al, agentInst, opts, "the report is complete and saved")
	if !handled {
		t.Fatal("want the pending echo reply")
	}
	if provider.callCount() != 2 {
		t.Fatalf("want exactly 2 LLM calls (compile + ONE repair, FR-007), got %d", provider.callCount())
	}
	// The repair call carried the gate's rejection reason back to the model.
	repairMsgs := provider.messagesOfCall(2)
	repairText := ""
	for _, m := range repairMsgs {
		repairText += m.Content + "\n"
	}
	if !strings.Contains(repairText, "rejected by the feasibility gate") {
		t.Fatalf("repair call must carry the veto reason, got:\n%s", repairText)
	}
	if got := goalCompileFallbacksTotal(); got != before+1 {
		t.Fatalf("second veto must fall back (counter %d, want %d)", got, before+1)
	}
	if !strings.Contains(echo, "quality-bar rewrite was unavailable") {
		t.Fatalf("fallback echo must carry the note:\n%s", echo)
	}
	// The deterministic fallback compiled the (judgeable) intent → pending.
	mid, _ := store.GetMeta(sid)
	if mid.GoalPendingJSON == "" || mid.GoalCondition != "" {
		t.Fatalf("fallback must end pending+confirm: %+v", mid)
	}
}

func TestGoalTwoPhase_FallbackRejection_PlainLanguageFirst(t *testing.T) {
	// The intent itself is hedging-only, so the deterministic fallback ALSO
	// rejects — the user must get the plain-language rejection, never silence
	// and never marker-syntax-first steering.
	al, agentInst, _, store, sid, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return compileJSON("vibes good"), nil // vetoed both times
		}, nil)

	_, handled, reply := setGoal(t, al, agentInst, opts, "really very good vibes")
	if !handled {
		t.Fatal("rejection must answer synchronously")
	}
	if reply == "" {
		t.Fatal("silent failure is the prohibited outcome (US-3 S5)")
	}
	if !strings.Contains(reply, "Describe what should be TRUE when the goal is done") {
		t.Fatalf("rejection must lead with the plain-language guidance, got:\n%s", reply)
	}
	if idx := strings.Index(reply, "[tests pass]"); idx >= 0 &&
		idx < strings.Index(reply, "Describe what should be TRUE") {
		t.Fatalf("marker syntax must not lead the rejection (plain-language-FIRST):\n%s", reply)
	}
	meta, _ := store.GetMeta(sid)
	if meta.GoalCondition != "" || meta.GoalPendingJSON != "" {
		t.Fatal("nothing may persist on a rejection")
	}
}

// --- Test 14 (US-3 S7/S9): clarification round-trip ------------------------

func TestGoalTwoPhase_Clarification_RoundTrip(t *testing.T) {
	al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
		func(call int, _ []providers.Message) (*providers.LLMResponse, error) {
			if call == 1 {
				return questionJSON("Which repo do you mean?"), nil
			}
			return compileJSON("the omnipus repo README is rewritten"), nil
		}, nil)

	// (1) Compile pauses on its single question.
	_, handled, reply := setGoal(t, al, agentInst, opts, "improve the readme")
	if !handled || !strings.Contains(reply, "Which repo do you mean?") {
		t.Fatalf("set must surface the clarifying question, got handled=%v reply=%q", handled, reply)
	}
	mid, _ := store.GetMeta(sid)
	if mid.GoalClarificationJSON == "" {
		t.Fatal("a question must persist the pending-clarification record")
	}
	if mid.GoalPendingJSON != "" {
		t.Fatal("no pending criteria may exist during a clarification round")
	}

	// (2) `/goal confirm` during clarification → informative redirect, never
	// "No pending goal to confirm".
	_, _, confirmReply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal confirm", UserInitiated: true}, agentInst, opts)
	if !strings.Contains(confirmReply, "Answer the pending question first") {
		t.Fatalf("confirm during clarification = %q", confirmReply)
	}
	if strings.Contains(confirmReply, "No pending goal") {
		t.Fatalf("must never say 'No pending goal' during clarification: %q", confirmReply)
	}

	// (3) Status reports the waiting state (US-3 S10).
	_, _, statusReply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal", UserInitiated: true}, agentInst, opts)
	if !strings.Contains(statusReply, "waiting for your answer") {
		t.Fatalf("status during clarification = %q, want the waiting-for-answer wording", statusReply)
	}

	// (4) The next ORDINARY message is the answer → ONE resumed compile →
	// pending + confirm.
	handled, echo := al.applyGoalPendingReply(context.Background(),
		bus.InboundMessage{Content: "the omnipus repo", UserInitiated: true}, agentInst, opts)
	if !handled {
		t.Fatal("the clarification answer must be intercepted (US-3 S9)")
	}
	if !strings.Contains(echo, "README is rewritten") {
		t.Fatalf("resumed compile must produce the pending echo:\n%s", echo)
	}
	if provider.callCount() != 2 {
		t.Fatalf("want exactly 2 LLM calls (compile + resume), got %d", provider.callCount())
	}
	resumeMsgs := provider.messagesOfCall(2)
	resumeText := ""
	for _, m := range resumeMsgs {
		resumeText += m.Content + "\n"
	}
	for _, want := range []string{"Which repo do you mean?", "the omnipus repo"} {
		if !strings.Contains(resumeText, want) {
			t.Fatalf("resumed compile input missing %q:\n%s", want, resumeText)
		}
	}
	after, _ := store.GetMeta(sid)
	if after.GoalClarificationJSON != "" {
		t.Fatal("the clarification record must clear once answered")
	}
	if after.GoalPendingJSON == "" || after.GoalCondition != "" {
		t.Fatalf("the resumed compile must end pending+confirm: %+v", after)
	}
}

func TestGoalTwoPhase_Clarification_ConfirmWordIsTheAnswer(t *testing.T) {
	al, agentInst, provider, _, _, opts := twoPhaseHarness(t,
		func(call int, _ []providers.Message) (*providers.LLMResponse, error) {
			if call == 1 {
				return questionJSON("Ship to staging or production?"), nil
			}
			return compileJSON("the release is deployed"), nil
		}, nil)

	setGoal(t, al, agentInst, opts, "ship the release")
	// A bare confirm-word during clarification is the ANSWER, not a confirm
	// (nothing is confirmable yet) — it must reach the resumed compile.
	handled, _ := al.applyGoalPendingReply(context.Background(),
		bus.InboundMessage{Content: "yes", UserInitiated: true}, agentInst, opts)
	if !handled {
		t.Fatal("the confirm-word answer must be intercepted as the clarification answer")
	}
	if provider.callCount() != 2 {
		t.Fatalf("want the resumed compile to have run (2 calls), got %d", provider.callCount())
	}
	resumeText := ""
	for _, m := range provider.messagesOfCall(2) {
		resumeText += m.Content + "\n"
	}
	if !strings.Contains(resumeText, "The user answered: yes") {
		t.Fatalf("the word 'yes' must be fed as the ANSWER, not treated as a confirm:\n%s", resumeText)
	}
}

func TestGoalTwoPhase_Clarification_MaxOneRound(t *testing.T) {
	al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
		func(call int, _ []providers.Message) (*providers.LLMResponse, error) {
			return questionJSON("And another question?"), nil // asks every time
		}, nil)

	before := goalCompileFallbacksTotal()
	setGoal(t, al, agentInst, opts, "improve the readme")
	handled, echo := al.applyGoalPendingReply(context.Background(),
		bus.InboundMessage{Content: "the omnipus repo", UserInitiated: true}, agentInst, opts)
	if !handled {
		t.Fatal("answer must be intercepted")
	}
	// A second question is out of budget (max ONE round): deterministic
	// fallback, still ending pending+confirm.
	if got := goalCompileFallbacksTotal(); got != before+1 {
		t.Fatalf("second question must fall back (counter %d, want %d)", got, before+1)
	}
	if !strings.Contains(echo, "quality-bar rewrite was unavailable") {
		t.Fatalf("fallback note missing:\n%s", echo)
	}
	after, _ := store.GetMeta(sid)
	if after.GoalClarificationJSON != "" {
		t.Fatal("the (spent) clarification record must clear")
	}
	if after.GoalPendingJSON == "" {
		t.Fatal("the fallback must still end pending+confirm")
	}
	if provider.callCount() != 2 {
		t.Fatalf("want exactly 2 LLM calls (question + resume-that-asked-again), got %d", provider.callCount())
	}
}

// Regression (US-3 S7): a clarification reply that trims to EMPTY — a
// whitespace-only message, or an attachment-only message whose text content
// is "" — still spends the episode's single question round. The resumed
// compile keys on the PERSISTED question, not the answer text; before the
// fix it keyed on answer != "", so the compiler could re-ask indefinitely
// on empty replies (each new question re-armed the clarification record).
func TestGoalTwoPhase_Clarification_EmptyReplyConsumesRound(t *testing.T) {
	for name, content := range map[string]string{
		"whitespace_only":            "   \n\t ",
		"attachment_only_empty_text": "",
	} {
		t.Run(name, func(t *testing.T) {
			al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
				func(call int, _ []providers.Message) (*providers.LLMResponse, error) {
					return questionJSON("And which branch?"), nil // asks every time
				}, nil)

			before := goalCompileFallbacksTotal()
			setGoal(t, al, agentInst, opts, "improve the readme")
			handled, echo := al.applyGoalPendingReply(context.Background(),
				bus.InboundMessage{Content: content, UserInitiated: true}, agentInst, opts)
			if !handled {
				t.Fatal("the empty reply must still be intercepted as the clarification answer")
			}
			// The round is SPENT: the compiler's second question is out of
			// budget → deterministic fallback, never a second question in chat.
			if strings.Contains(echo, "And which branch?") {
				t.Fatalf("a second clarifying question surfaced after an empty reply (round not consumed):\n%s", echo)
			}
			if got := goalCompileFallbacksTotal(); got != before+1 {
				t.Fatalf("re-ask after the empty-reply resume must fall back (counter %d, want %d)", got, before+1)
			}
			after, _ := store.GetMeta(sid)
			if after.GoalClarificationJSON != "" {
				t.Fatal("the clarification record must clear — no second question round may be armed")
			}
			if after.GoalPendingJSON == "" {
				t.Fatal("the fallback must still end pending+confirm")
			}
			if provider.callCount() != 2 {
				t.Fatalf("want exactly 2 LLM calls (question + empty-answer resume), got %d", provider.callCount())
			}
		})
	}
}

// Companion regression: an empty reply resumes the compile normally when the
// compiler produces criteria — and the resume prompt states explicitly that
// no textual answer arrived instead of rendering a blank answer line.
func TestGoalTwoPhase_Clarification_EmptyReplyStillResumes(t *testing.T) {
	al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
		func(call int, _ []providers.Message) (*providers.LLMResponse, error) {
			if call == 1 {
				return questionJSON("Which repo do you mean?"), nil
			}
			return compileJSON("the README is rewritten"), nil
		}, nil)

	setGoal(t, al, agentInst, opts, "improve the readme")
	handled, echo := al.applyGoalPendingReply(context.Background(),
		bus.InboundMessage{Content: "  ", UserInitiated: true}, agentInst, opts)
	if !handled {
		t.Fatal("the empty reply must be intercepted")
	}
	if !strings.Contains(echo, "README is rewritten") {
		t.Fatalf("the empty-answer resume must still produce the pending echo:\n%s", echo)
	}
	resumeText := ""
	for _, m := range provider.messagesOfCall(2) {
		resumeText += m.Content + "\n"
	}
	if !strings.Contains(resumeText, "sent no textual answer") {
		t.Fatalf("the resume prompt must state the answer was empty, got:\n%s", resumeText)
	}
	after, _ := store.GetMeta(sid)
	if after.GoalClarificationJSON != "" {
		t.Fatal("the clarification record must clear once resumed")
	}
	if after.GoalPendingJSON == "" {
		t.Fatal("the resume must end pending+confirm")
	}
}

func TestGoalTwoPhase_Clarification_ResumeGetsOwnRepair(t *testing.T) {
	al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
		func(call int, _ []providers.Message) (*providers.LLMResponse, error) {
			switch call {
			case 1:
				return questionJSON("Which repo?"), nil
			case 2:
				return compileJSON("feels good"), nil // vetoed → resume's own repair
			default:
				return compileJSON("the README covers installation end to end"), nil
			}
		}, nil)

	setGoal(t, al, agentInst, opts, "improve the readme")
	handled, echo := al.applyGoalPendingReply(context.Background(),
		bus.InboundMessage{Content: "the omnipus repo", UserInitiated: true}, agentInst, opts)
	if !handled {
		t.Fatal("answer must be intercepted")
	}
	// FR-007 episode budget: compile(question) + resume + resume-repair = 3 ≤ 4.
	if provider.callCount() != 3 {
		t.Fatalf("want 3 LLM calls (question, resume, resume's single repair), got %d", provider.callCount())
	}
	if !strings.Contains(echo, "covers installation end to end") {
		t.Fatalf("repaired resume must produce the pending echo:\n%s", echo)
	}
	after, _ := store.GetMeta(sid)
	if after.GoalPendingJSON == "" {
		t.Fatal("want pending after the repaired resume")
	}
	_ = store
	_ = sid
}

// --- Test 14b (US-3 S8, EC-5): amendment pins ------------------------------

func TestGoalTwoPhase_AmendmentPins(t *testing.T) {
	t.Run("active_restate_stays_deterministic", func(t *testing.T) {
		al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
			func(int, []providers.Message) (*providers.LLMResponse, error) {
				return nil, errors.New("active-goal restate must not call the LLM (US-3 S8)")
			}, nil)
		allowBashPolicy(agentInst)
		// Activate marker-only (zero LLM), then restate with prose.
		setGoal(t, al, agentInst, opts, "[tests]")
		_, handled, reply := setGoal(t, al, agentInst, opts, "the docs also read well")
		if !handled || !strings.Contains(reply, "amendment") {
			t.Fatalf("active restate must produce a deterministic amendment echo, got %q", reply)
		}
		if provider.callCount() != 0 {
			t.Fatalf("active restate made %d LLM calls, want 0 (deterministic this phase)", provider.callCount())
		}
		meta, _ := store.GetMeta(sid)
		if meta.GoalPendingJSON == "" {
			t.Fatal("amendment must park as pending")
		}
		_ = sid
	})

	t.Run("restate_over_pending_replaces", func(t *testing.T) {
		al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
			func(call int, _ []providers.Message) (*providers.LLMResponse, error) {
				if call == 1 {
					return compileJSON("first draft is saved"), nil
				}
				return compileJSON("second draft is saved"), nil
			}, nil)
		setGoal(t, al, agentInst, opts, "write draft one")
		setGoal(t, al, agentInst, opts, "write draft two")
		if provider.callCount() != 2 {
			t.Fatalf("want 2 compiles, got %d", provider.callCount())
		}
		meta, _ := store.GetMeta(sid)
		pending := loadCompiledGoal(meta.GoalPendingJSON)
		if pending == nil || pending.Intent != "write draft two" {
			t.Fatalf("restate over pending must REPLACE the pending compile, got %+v", pending)
		}
		if meta.GoalCondition != "" {
			t.Fatal("no activation may happen on a restate")
		}
	})

	t.Run("restate_over_clarification_discards", func(t *testing.T) {
		al, agentInst, _, store, sid, opts := twoPhaseHarness(t,
			func(call int, _ []providers.Message) (*providers.LLMResponse, error) {
				if call == 1 {
					return questionJSON("Which one?"), nil
				}
				return compileJSON("the new goal is done"), nil
			}, nil)
		setGoal(t, al, agentInst, opts, "ambiguous goal")
		mid, _ := store.GetMeta(sid)
		if mid.GoalClarificationJSON == "" {
			t.Fatal("setup: clarification must be pending")
		}
		setGoal(t, al, agentInst, opts, "a completely new goal")
		after, _ := store.GetMeta(sid)
		if after.GoalClarificationJSON != "" {
			t.Fatal("a fresh /goal <intent> must discard the clarification record (R2-10)")
		}
		if after.GoalPendingJSON == "" {
			t.Fatal("the new compile must park as pending")
		}
	})

	t.Run("clear_discards_clarification", func(t *testing.T) {
		al, agentInst, _, store, sid, opts := twoPhaseHarness(t,
			func(int, []providers.Message) (*providers.LLMResponse, error) {
				return questionJSON("Which one?"), nil
			}, nil)
		setGoal(t, al, agentInst, opts, "ambiguous goal")
		_, _, reply := al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal clear", UserInitiated: true}, agentInst, opts)
		if !strings.Contains(reply, "cleared") {
			t.Fatalf("clear reply = %q", reply)
		}
		after, _ := store.GetMeta(sid)
		if after.GoalClarificationJSON != "" {
			t.Fatal("/goal clear must discard the clarification record")
		}
	})
}

// --- Test 14c (US-3 S9): pending-confirm reply taxonomy --------------------

func TestGoalTwoPhase_PendingConfirmReplyTaxonomy(t *testing.T) {
	newPending := func(t *testing.T) (*AgentLoop, *AgentInstance, *session.UnifiedStore, string, *processOptions) {
		al, agentInst, _, store, sid, opts := twoPhaseHarness(t,
			func(int, []providers.Message) (*providers.LLMResponse, error) {
				return compileJSON("the report is saved"), nil
			}, nil)
		setGoal(t, al, agentInst, opts, "write the report")
		meta, _ := store.GetMeta(sid)
		if meta.GoalPendingJSON == "" {
			t.Fatal("setup: pending goal required")
		}
		return al, agentInst, store, sid, opts
	}

	t.Run("bare_confirm_token_activates_into_round1", func(t *testing.T) {
		al, agentInst, store, sid, opts := newPending(t)
		handled, _ := al.applyGoalPendingReply(context.Background(),
			bus.InboundMessage{Content: "confirm", UserInitiated: true}, agentInst, opts)
		if handled {
			t.Fatal("a fresh-pending confirm must NOT answer synchronously — the turn continues into round 1")
		}
		if !strings.Contains(opts.UserMessage, "write the report") {
			t.Fatalf("opts.UserMessage = %q, want the condition (round 1)", opts.UserMessage)
		}
		after, _ := store.GetMeta(sid)
		if after.GoalCondition == "" || after.GoalPendingJSON != "" {
			t.Fatalf("bare confirm must activate: %+v", after)
		}
	})

	t.Run("bare_non_confirm_passes_through_pending_intact", func(t *testing.T) {
		al, agentInst, store, sid, opts := newPending(t)
		originalUserMessage := opts.UserMessage
		handled, reply := al.applyGoalPendingReply(context.Background(),
			bus.InboundMessage{Content: "hey, unrelated question about the weather", UserInitiated: true}, agentInst, opts)
		if handled || reply != "" {
			t.Fatalf("ordinary chat must pass through untouched, got handled=%v reply=%q", handled, reply)
		}
		if opts.UserMessage != originalUserMessage {
			t.Fatal("passthrough must not rewrite the turn")
		}
		after, _ := store.GetMeta(sid)
		if after.GoalPendingJSON == "" || after.GoalCondition != "" {
			t.Fatal("a routine chat message must never silently mutate goal state (US-3 S9)")
		}
	})

	t.Run("multi_token_confirmish_message_is_ordinary_chat", func(t *testing.T) {
		al, agentInst, store, sid, opts := newPending(t)
		handled, _ := al.applyGoalPendingReply(context.Background(),
			bus.InboundMessage{Content: "yes please do that", UserInitiated: true}, agentInst, opts)
		if handled {
			t.Fatal("only an EXACT single confirm token confirms")
		}
		after, _ := store.GetMeta(sid)
		if after.GoalPendingJSON == "" {
			t.Fatal("pending must survive a multi-token near-confirm")
		}
		_ = sid
	})

	t.Run("goal_clear_discards_pending", func(t *testing.T) {
		al, agentInst, store, sid, opts := newPending(t)
		al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal clear", UserInitiated: true}, agentInst, opts)
		after, _ := store.GetMeta(sid)
		if after.GoalPendingJSON != "" || after.GoalCondition != "" {
			t.Fatal("/goal clear must discard the pending goal")
		}
	})
}

// --- Test 14d (US-3 S10): pending lifecycle --------------------------------

func TestGoalTwoPhase_PendingLifecycle(t *testing.T) {
	t.Run("status_during_pending_confirm", func(t *testing.T) {
		al, agentInst, _, _, _, opts := twoPhaseHarness(t,
			func(int, []providers.Message) (*providers.LLMResponse, error) {
				return compileJSON("the report is saved"), nil
			}, nil)
		setGoal(t, al, agentInst, opts, "write the report")
		_, _, status := al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal", UserInitiated: true}, agentInst, opts)
		if !strings.Contains(status, "pending your confirmation") {
			t.Fatalf("status during pending = %q, want the pending wording (never 'No active goal')", status)
		}
		if strings.Contains(status, "No active goal") {
			t.Fatalf("status must not claim no goal exists: %q", status)
		}
	})

	t.Run("idle_sweep_expires_pending_records", func(t *testing.T) {
		al, agentInst, _, store, sid, opts := twoPhaseHarness(t,
			func(int, []providers.Message) (*providers.LLMResponse, error) {
				return compileJSON("the report is saved"), nil
			}, nil)
		setGoal(t, al, agentInst, opts, "write the report")
		mid, _ := store.GetMeta(sid)
		if mid.GoalPendingJSON == "" {
			t.Fatal("setup: pending required")
		}

		var cfg config.PlanningConfig
		days := cfg.EffectiveIdleExpiryDays(nil)
		al.goalIdleExpirySweep(cfg, time.Now().UTC().Add(time.Duration(days+1)*24*time.Hour))

		after, _ := store.GetMeta(sid)
		if after.GoalPendingJSON != "" || after.GoalClarificationJSON != "" {
			t.Fatalf("the idle sweep must expire pending records (US-3 S10): %+v", after)
		}
	})

	t.Run("idle_sweep_expires_clarification_records", func(t *testing.T) {
		al, agentInst, _, store, sid, opts := twoPhaseHarness(t,
			func(int, []providers.Message) (*providers.LLMResponse, error) {
				return questionJSON("Which one?"), nil
			}, nil)
		setGoal(t, al, agentInst, opts, "ambiguous goal")
		mid, _ := store.GetMeta(sid)
		if mid.GoalClarificationJSON == "" {
			t.Fatal("setup: clarification required")
		}
		var cfg config.PlanningConfig
		days := cfg.EffectiveIdleExpiryDays(nil)
		al.goalIdleExpirySweep(cfg, time.Now().UTC().Add(time.Duration(days+1)*24*time.Hour))
		after, _ := store.GetMeta(sid)
		if after.GoalClarificationJSON != "" {
			t.Fatal("the idle sweep must expire clarification records (US-3 S10)")
		}
	})

	t.Run("fresh_pending_not_swept", func(t *testing.T) {
		al, agentInst, _, store, sid, opts := twoPhaseHarness(t,
			func(int, []providers.Message) (*providers.LLMResponse, error) {
				return compileJSON("the report is saved"), nil
			}, nil)
		setGoal(t, al, agentInst, opts, "write the report")
		var cfg config.PlanningConfig
		al.goalIdleExpirySweep(cfg, time.Now().UTC().Add(time.Hour))
		after, _ := store.GetMeta(sid)
		if after.GoalPendingJSON == "" {
			t.Fatal("a fresh pending goal must survive a sweep inside the TTL")
		}
	})
}

// --- Test 22 (EC-4): nil agentInst → fallback, no LLM ----------------------

func TestGoalTwoPhase_NilAgentInst_FallbackNoLLM(t *testing.T) {
	al, _, provider, store, sid, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return nil, errors.New("must not be called")
		}, nil)

	before := goalCompileFallbacksTotal()
	matched, handled, echo := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, nil, opts)
	if !matched || !handled {
		t.Fatalf("matched=%v handled=%v", matched, handled)
	}
	if provider.callCount() != 0 {
		t.Fatalf("nil agentInst must make ZERO LLM calls (EC-4), got %d", provider.callCount())
	}
	if got := goalCompileFallbacksTotal(); got != before+1 {
		t.Fatalf("nil-agentInst fallback must be observable (counter %d, want %d)", got, before+1)
	}
	meta, _ := store.GetMeta(sid)
	if meta.GoalPendingJSON == "" || meta.GoalCondition != "" {
		t.Fatalf("nil-agentInst prose set must still end pending+confirm: %+v", meta)
	}
	if !strings.Contains(echo, "quality-bar rewrite was unavailable") {
		t.Fatalf("fallback note missing:\n%s", echo)
	}
}

// --- Engine-side define-goal injection (US-4 S5 seam, exercised here) ------

func TestGoalTwoPhase_DefineGoalInjectedEngineSide(t *testing.T) {
	al, agentInst, provider, _, _, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return compileJSON("the report is saved"), nil
		}, nil)

	// The harness (newGoalLoopTestLoop) pins OMNIPUS_HOME to a temp dir; seed
	// the define-goal skill file where SeedDefaults would put it.
	skillDir := filepath.Join(config.OmnipusHomeDir(), "skills", "define-goal")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const marker = "QUALITY-BAR-MARKER: outcome-not-activity"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}

	setGoal(t, al, agentInst, opts, "write the research report")
	if provider.callCount() != 1 {
		t.Fatalf("want 1 compile call, got %d", provider.callCount())
	}
	sysText := ""
	for _, m := range provider.messagesOfCall(1) {
		if m.Role == "system" {
			sysText += m.Content
		}
	}
	if !strings.Contains(sysText, marker) {
		t.Fatalf("define-goal content must be injected engine-side into the compile call, got:\n%s", sysText)
	}
}
