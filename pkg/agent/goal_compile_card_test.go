// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_compile_card_test.go covers ADR-079 D3: delivering the goal-compile
// clarifying question via the shipped AskUserQuestion card on web owner
// sessions, with the plain-chat fallback preserved everywhere else. Reuses
// the twoPhaseHarness/setGoal/questionJSON fixtures from
// goal_two_phase_test.go.

package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// fakeGoalAskRegistry is a scriptable tools.AskUserQuestionRegistry double:
// CreatePending records a clone of every accepted set and can be scripted to
// return an error (once) for the fallback-condition tests.
type fakeGoalAskRegistry struct {
	mu      sync.Mutex
	created []*askuser.PendingSet
	nextErr error
}

func (f *fakeGoalAskRegistry) CreatePending(set *askuser.PendingSet) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.nextErr != nil {
		err := f.nextErr
		f.nextErr = nil
		return err
	}
	f.created = append(f.created, set.Clone())
	return nil
}

func (f *fakeGoalAskRegistry) PendingForSession(string) (*askuser.PendingSet, bool) {
	return nil, false
}

func (f *fakeGoalAskRegistry) CancelOnSessionStop(string) bool { return false }

func (f *fakeGoalAskRegistry) lastCreated() *askuser.PendingSet {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.created) == 0 {
		return nil
	}
	return f.created[len(f.created)-1]
}

func (f *fakeGoalAskRegistry) createCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created)
}

// structuredQuestionJSON builds an ambiguous-branch compile response with a
// FULL askuser.Question shape (header, question, 2 options, recommended,
// and — deliberately, to prove M2's stripping — default_safe:true), so
// tests can assert the web card path actually maps the compile's structured
// output onto the registry, and that default_safe never survives to the
// PendingSet.
func structuredQuestionJSON(header, question string) *providers.LLMResponse {
	return &providers.LLMResponse{
		Content: `{"assessment":{"clarity":"ambiguous"},"clarifying_questions":[` +
			`{"header":"` + header + `","question":"` + question + `",` +
			`"options":[{"label":"omnipus repo"},{"label":"other repo"}],` +
			`"recommended":"omnipus repo","default_safe":true}` +
			`]}`,
	}
}

// --- Web ambiguous -> CreatePending card, no default_safe ------------------

func TestGoalCompileCard_WebAmbiguous_CreatesCardStripsDefaultSafe(t *testing.T) {
	al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
		func(call int, _ []providers.Message) (*providers.LLMResponse, error) {
			return structuredQuestionJSON("Repo", "Which repo do you mean?"), nil
		}, nil)
	reg := &fakeGoalAskRegistry{}
	al.SetAskUserRegistry(reg)

	matched, handled, reply := setGoal(t, al, agentInst, opts, "improve the readme")
	if !matched || !handled {
		t.Fatalf("matched=%v handled=%v, want both true", matched, handled)
	}
	if provider.callCount() != 1 {
		t.Fatalf("want exactly 1 compile call, got %d", provider.callCount())
	}

	// The card path answers with a reply that does NOT invite a typed
	// answer (D3 point 3) and does not itself restate the question text as
	// something to type back.
	if strings.Contains(reply, "Answer in chat") {
		t.Fatalf("card-path reply must not invite a typed chat answer, got %q", reply)
	}
	if !strings.Contains(reply, "card") {
		t.Fatalf("card-path reply should point the user at the card, got %q", reply)
	}

	created := reg.lastCreated()
	if created == nil {
		t.Fatal("CreatePending must have been called for a webchat session with a wired registry")
	}
	if created.CardID == "" {
		t.Fatal("PendingSet.CardID must be minted")
	}
	if created.TranscriptSessionID != sid {
		t.Fatalf("TranscriptSessionID = %q, want %q", created.TranscriptSessionID, sid)
	}
	if created.Channel != "webchat" || created.ChatID != opts.ChatID {
		t.Fatalf("Channel/ChatID not carried from opts: %+v", created)
	}
	if created.AgentID != agentInst.ID {
		t.Fatalf("AgentID = %q, want %q", created.AgentID, agentInst.ID)
	}
	if len(created.Questions) != 1 || created.Questions[0].Header != "Repo" ||
		created.Questions[0].Question != "Which repo do you mean?" {
		t.Fatalf("unexpected questions on the created set: %+v", created.Questions)
	}
	// M2 (ADR-079 D3 regrill): default_safe is FORBIDDEN on every
	// goal-clarify question — even though the compile emitted
	// default_safe:true, the card-built set must never carry it.
	if created.Questions[0].DefaultSafe {
		t.Fatal("default_safe must be stripped from every goal-clarify question (M2)")
	}
	if created.Questions[0].Recommended != "omnipus repo" {
		t.Fatalf("recommended should survive (only default_safe is stripped), got %+v", created.Questions[0])
	}

	// The clarification record persists additively-extended with the
	// card's identity, and no plain-chat fallback state exists.
	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	clar := loadGoalClarification(meta.GoalClarificationJSON)
	if clar == nil {
		t.Fatal("a clarification record must persist")
	}
	if clar.CardID != created.CardID {
		t.Fatalf("record CardID = %q, want %q", clar.CardID, created.CardID)
	}
	if len(clar.Questions) != 1 || clar.Questions[0].Header != "Repo" {
		t.Fatalf("record Questions echo missing/wrong: %+v", clar.Questions)
	}
	if meta.GoalPendingJSON != "" {
		t.Fatal("a question supersedes any earlier pending compile")
	}
	if meta.GoalCondition != "" {
		t.Fatal("the card path must not activate a goal")
	}
}

// --- Fallback conditions (ADR-079 D3): all keep today's plain-chat path ----

func TestGoalCompileCard_ChannelOrigin_FallsBackToPlainChat(t *testing.T) {
	al, agentInst, _, store, sid, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return structuredQuestionJSON("Repo", "Which repo do you mean?"), nil
		}, nil)
	reg := &fakeGoalAskRegistry{}
	al.SetAskUserRegistry(reg) // wired — but the channel gate must still win (US-5)
	opts.Channel = "telegram"

	_, handled, reply := setGoal(t, al, agentInst, opts, "improve the readme")
	if !handled {
		t.Fatal("channel-origin clarify must still answer synchronously")
	}
	if reg.createCount() != 0 {
		t.Fatal("no card may be created on a non-webchat origin (US-5, permanent)")
	}
	if !strings.Contains(reply, "Which repo do you mean?") || !strings.Contains(reply, "Answer in chat") {
		t.Fatalf("channel fallback must be today's plain-chat question, got %q", reply)
	}
	meta, _ := store.GetMeta(sid)
	clar := loadGoalClarification(meta.GoalClarificationJSON)
	if clar == nil || clar.CardID != "" {
		t.Fatalf("channel fallback record must carry no CardID, got %+v", clar)
	}
}

func TestGoalCompileCard_NilRegistry_FallsBackToPlainChat(t *testing.T) {
	al, agentInst, _, store, sid, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return structuredQuestionJSON("Repo", "Which repo do you mean?"), nil
		}, nil)
	// No SetAskUserRegistry call: registry stays nil.

	_, handled, reply := setGoal(t, al, agentInst, opts, "improve the readme")
	if !handled || !strings.Contains(reply, "Which repo do you mean?") {
		t.Fatalf("unwired-registry fallback must be plain-chat, got handled=%v reply=%q", handled, reply)
	}
	meta, _ := store.GetMeta(sid)
	clar := loadGoalClarification(meta.GoalClarificationJSON)
	if clar == nil || clar.CardID != "" {
		t.Fatalf("unwired-registry fallback record must carry no CardID, got %+v", clar)
	}
}

func TestGoalCompileCard_ErrAlreadyPending_FallsBackToPlainChat(t *testing.T) {
	al, agentInst, _, store, sid, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return structuredQuestionJSON("Repo", "Which repo do you mean?"), nil
		}, nil)
	reg := &fakeGoalAskRegistry{nextErr: askuser.ErrAlreadyPending}
	al.SetAskUserRegistry(reg)

	_, handled, reply := setGoal(t, al, agentInst, opts, "improve the readme")
	if !handled || !strings.Contains(reply, "Which repo do you mean?") {
		t.Fatalf("ErrAlreadyPending must fall back to plain-chat, got handled=%v reply=%q", handled, reply)
	}
	if reg.createCount() != 0 {
		t.Fatal("a losing CreatePending call must not be recorded as created")
	}
	meta, _ := store.GetMeta(sid)
	clar := loadGoalClarification(meta.GoalClarificationJSON)
	if clar == nil || clar.CardID != "" {
		t.Fatalf("ErrAlreadyPending fallback record must carry no CardID, got %+v", clar)
	}
}

func TestGoalCompileCard_UnexpectedCreatePendingError_SurfacesInternalError(t *testing.T) {
	al, agentInst, _, _, _, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return structuredQuestionJSON("Repo", "Which repo do you mean?"), nil
		}, nil)
	reg := &fakeGoalAskRegistry{nextErr: errors.New("boom: registry storage fault")}
	al.SetAskUserRegistry(reg)

	_, handled, reply := setGoal(t, al, agentInst, opts, "improve the readme")
	if !handled {
		t.Fatal("an unexpected internal error must still answer synchronously")
	}
	if !strings.Contains(reply, "internal error") {
		t.Fatalf("an unexpected CreatePending error must surface loudly, got %q", reply)
	}
	// This must NOT silently degrade to the plain-chat question text — an
	// unknown error is a bug, not a verified fallback condition.
	if strings.Contains(reply, "Which repo do you mean?") {
		t.Fatalf("an unexpected error must not silently fall back to plain chat, got %q", reply)
	}
}

// --- Resume: CardID keying (regrill C1) -------------------------------------

// cardTestHarness sets up a webchat session with a wired fake registry and
// drives the initial ambiguous compile through to a created card. Returns
// everything a resume-path test needs.
func cardTestHarness(
	t *testing.T, resumeScript func(call int, messages []providers.Message) (*providers.LLMResponse, error),
) (al *AgentLoop, agentInst *AgentInstance, provider *scriptedCompileProvider, store *session.UnifiedStore, sid string, opts *processOptions, reg *fakeGoalAskRegistry, cardID string) {
	t.Helper()
	al, agentInst, provider, store, sid, opts = twoPhaseHarness(t,
		func(call int, messages []providers.Message) (*providers.LLMResponse, error) {
			if call == 1 {
				return structuredQuestionJSON("Repo", "Which repo do you mean?"), nil
			}
			return resumeScript(call, messages)
		}, nil)
	reg = &fakeGoalAskRegistry{}
	al.SetAskUserRegistry(reg)

	_, handled, _ := setGoal(t, al, agentInst, opts, "improve the readme")
	if !handled {
		t.Fatal("setup: initial ambiguous compile must answer synchronously")
	}
	created := reg.lastCreated()
	if created == nil {
		t.Fatal("setup: the card must have been created")
	}
	return al, agentInst, provider, store, sid, opts, reg, created.CardID
}

func TestGoalCompileCard_Resume_MatchingCardIDResumesCompile(t *testing.T) {
	al, agentInst, provider, store, sid, opts, _, cardID := cardTestHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return compileJSON("the omnipus repo README is rewritten"), nil
		})

	freeText := "the omnipus repo"
	resumeSet := &askuser.PendingSet{
		CardID: cardID,
		Status: askuser.StatusAnswered,
		Answers: []askuser.Answer{
			{Header: "Repo", QuestionText: "Which repo do you mean?", FreeText: &freeText},
		},
	}
	resumeMsg, err := askuser.ResumeMessage(resumeSet)
	if err != nil {
		t.Fatal(err)
	}

	handled, echo := al.applyGoalPendingReply(context.Background(),
		bus.InboundMessage{Content: resumeMsg, UserInitiated: true}, agentInst, opts)
	if !handled {
		t.Fatal("the matching card_id answers message must be intercepted and resume the compile")
	}
	if !strings.Contains(echo, "README is rewritten") {
		t.Fatalf("resumed compile must produce the pending echo:\n%s", echo)
	}
	if provider.callCount() != 2 {
		t.Fatalf("want exactly 2 LLM calls (initial + resume), got %d", provider.callCount())
	}
	resumeText := ""
	for _, m := range provider.messagesOfCall(2) {
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

func TestGoalCompileCard_Resume_StrayMessagePassesThroughCardSurvives(t *testing.T) {
	al, agentInst, provider, store, sid, opts, _, cardID := cardTestHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return nil, errors.New("must not be called — a stray message must never resume the compile")
		})

	t.Run("unrelated_bare_message", func(t *testing.T) {
		handled, reply := al.applyGoalPendingReply(context.Background(),
			bus.InboundMessage{Content: "hey, what's the weather like", UserInitiated: true}, agentInst, opts)
		if handled || reply != "" {
			t.Fatalf("a stray unrelated message must pass through untouched, got handled=%v reply=%q", handled, reply)
		}
	})

	t.Run("resume_message_for_a_different_card", func(t *testing.T) {
		foreignMsg, err := askuser.ResumeMessage(&askuser.PendingSet{
			CardID: "ask_some_other_card_entirely",
			Status: askuser.StatusAnswered,
		})
		if err != nil {
			t.Fatal(err)
		}
		handled, reply := al.applyGoalPendingReply(context.Background(),
			bus.InboundMessage{Content: foreignMsg, UserInitiated: true}, agentInst, opts)
		if handled || reply != "" {
			t.Fatalf("a non-matching card_id resume message must pass through untouched, got handled=%v reply=%q", handled, reply)
		}
	})

	if provider.callCount() != 1 {
		t.Fatalf("neither stray message may trigger a resumed compile call, got %d calls", provider.callCount())
	}
	// The card and clarification record survive both stray messages.
	meta, _ := store.GetMeta(sid)
	clar := loadGoalClarification(meta.GoalClarificationJSON)
	if clar == nil || clar.CardID != cardID {
		t.Fatalf("the clarification record (and its CardID) must survive stray messages, got %+v", clar)
	}
}

func TestGoalCompileCard_Resume_CancelledDiscardsLikeGoalClear(t *testing.T) {
	al, agentInst, provider, store, sid, opts, _, cardID := cardTestHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return nil, errors.New("must not be called — a cancel must never resume the compile")
		})

	cancelMsg, err := askuser.ResumeMessage(&askuser.PendingSet{CardID: cardID, Status: askuser.StatusCancelled})
	if err != nil {
		t.Fatal(err)
	}
	handled, reply := al.applyGoalPendingReply(context.Background(),
		bus.InboundMessage{Content: cancelMsg, UserInitiated: true}, agentInst, opts)
	if !handled {
		t.Fatal("a cancel resume message must be intercepted")
	}
	if !strings.Contains(reply, "cleared") {
		t.Fatalf("cancel must discard the draft like /goal clear, got %q", reply)
	}
	if provider.callCount() != 1 {
		t.Fatalf("cancel must not trigger a resumed compile call, got %d calls", provider.callCount())
	}
	meta, _ := store.GetMeta(sid)
	if meta.GoalClarificationJSON != "" || meta.GoalPendingJSON != "" || meta.GoalCondition != "" {
		t.Fatalf("cancel must fully discard the draft, got %+v", meta)
	}
}

// --- Goal-loop gate: a pending clarification never trips it ----------------

// TestGoalCompileCard_GoalLoopGate_PendingClarificationNeverAdvances confirms
// item 5 of ADR-079 D3: while a (web-card or plain-chat) clarification is
// pending, no active goal exists yet (GoalCondition == ""), so
// checkGoalLoopAfterTurn's existing fast-path gate already no-ops — this is
// not new behavior, just a regression pin that the card path does not
// somehow trip it.
func TestGoalCompileCard_GoalLoopGate_PendingClarificationNeverAdvances(t *testing.T) {
	al, agentInst, _, store, sid, opts, _, _ := cardTestHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return nil, errors.New("must not be called by this test")
		})

	before, _ := store.GetMeta(sid)
	if before.GoalCondition != "" {
		t.Fatal("setup: no active goal may exist while a clarification is pending")
	}

	result := &turnResult{finalContent: "GOAL_STATUS: met [goal:evidence]"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, *opts, result)

	after, _ := store.GetMeta(sid)
	if after.GoalRoundsUsed != 0 || after.GoalCondition != "" {
		t.Fatalf("a pending clarification must never let the goal-loop gate advance a round: %+v", after)
	}
}
