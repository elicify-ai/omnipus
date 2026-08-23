// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_session_type_test.go covers ADR-052 Wave 2's session-type
// stamping slice (verifier_adjudication.go): a fresh verifier session's
// meta.json carries a "verifier" session type from the moment it is
// created (newVerifierSessionChatID), via a pre-created, explicitly-typed
// UnifiedStore session — NOT a post-hoc patch (MetaPatch carries no Type
// field; UnifiedStore has no such seam). Creation goes through
// session.NewVerifierSession, so the stamped type is the exported
// session.SessionTypeVerifier constant (FR-036).
package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestVerifierSessionType_RealAdjudicationStampsVerifierType is the
// end-to-end proof: a real runVerifierAdjudication call (via
// AgentLoop.JudgeCriteria, exactly how task_executor.go/plan_engine.go/
// goal_loop.go invoke it) leaves behind a persisted session whose Type is
// "verifier" — found by listing the Judge's own session store afterward
// (runVerifierAdjudication does not return the session id directly; its
// contract is verdicts/model/judgeAgentID/unavailable/reason only, per
// FR-011 — see runVerifierAdjudication's own doc comment for why that
// signature could not change).
func TestVerifierSessionType_RealAdjudicationStampsVerifierType(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-type-stamp",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "x")},
		Attempt:         1,
		ClaimText:       "done",
	})
	if result.Unavailable || !result.Verdict.Met {
		t.Fatalf("unexpected result: %+v", result)
	}

	judgeStore := al.GetAgentStore(string(coreagent.IDJudge))
	if judgeStore == nil {
		t.Fatal("judge session store not available")
	}
	sessions, err := judgeStore.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	wantTitle := "Verifier: " + verifierUnitForTask("t-type-stamp")
	var found *session.UnifiedMeta
	for _, s := range sessions {
		if s.Title == wantTitle {
			found = s
			break
		}
	}
	if found == nil {
		titles := make([]string, 0, len(sessions))
		for _, s := range sessions {
			titles = append(titles, s.Title)
		}
		t.Fatalf("no verifier session found with title %q among session titles %v", wantTitle, titles)
	}
	if found.Type != session.SessionTypeVerifier {
		t.Errorf("verifier session Type = %q, want %q", found.Type, session.SessionTypeVerifier)
	}
}

// TestVerifierSessionType_ChatIDIsAPreCreatedSessionNotAnAdHocString
// distinguishes newVerifierSessionChatID's real behavior from Wave 1's
// original ad hoc "verify:"+sessionKey construction: the returned chatID
// must resolve to a REAL, readable session (GetMeta succeeds) — proving it
// really is the pre-created session's own ID, not merely a routing string
// that happens to look similar.
func TestVerifierSessionType_ChatIDIsAPreCreatedSessionNotAnAdHocString(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	chatID := al.newVerifierSessionChatID("agent:judge:verify:test-key", "task:t-precreate")

	judgeStore := al.GetAgentStore(string(coreagent.IDJudge))
	if judgeStore == nil {
		t.Fatal("judge session store not available")
	}
	meta, err := judgeStore.GetMeta(chatID)
	if err != nil {
		t.Fatalf("newVerifierSessionChatID returned %q, which does not resolve to a real session: %v", chatID, err)
	}
	if meta.Type != session.SessionTypeVerifier {
		t.Errorf("pre-created session Type = %q, want %q", meta.Type, session.SessionTypeVerifier)
	}
	if meta.Title != "Verifier: task:t-precreate" {
		t.Errorf("pre-created session Title = %q, want %q", meta.Title, "Verifier: task:t-precreate")
	}
}

// TestVerifierSessionType_FallsBackWhenJudgeNotRegistered proves
// newVerifierSessionChatID degrades gracefully — never blocks adjudication
// — when the Judge's session store cannot be resolved (the Judge is not
// registered at all, e.g. a raw pkg/agent harness that never ran
// coreagent.SeedConfig): it returns the ORIGINAL Wave-1 ad hoc
// "verify:"+sessionKey construction verbatim, unstamped, rather than
// erroring or panicking. Uses a minimal config with NO agents at all
// (mirrors cancel_test.go's newCancelTestAgentLoop harness shape) so
// al.GetAgentStore(string(coreagent.IDJudge)) genuinely returns nil.
func TestVerifierSessionType_FallsBackWhenJudgeNotRegistered(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}},
			// Deliberately no List entries — no Judge, no worker, nothing.
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })

	if store := al.GetAgentStore(string(coreagent.IDJudge)); store != nil {
		t.Fatal("test premise broken: the Judge must NOT be registered in this harness")
	}

	sessKey := "agent:judge:verify:fallback-key"
	got := al.newVerifierSessionChatID(sessKey, "task:t-fallback")
	if want := "verify:" + sessKey; got != want {
		t.Errorf("newVerifierSessionChatID with no Judge registered = %q, want the ad hoc fallback %q", got, want)
	}
}
