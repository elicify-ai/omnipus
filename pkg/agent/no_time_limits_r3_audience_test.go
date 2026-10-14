package agent

// PR #1311 G6 / architect R2-A3: missing journal or lost modern edge PLUS
// retained parent metadata is a damaged child, never proof of a human audience.
// The classification, audience resolver, launcher and conclusion wrapper are
// real; only the provider is unavailable. Control: a true root needs no journal.
// CHECK: bypass the classifier; guess parentless on absence; drop the history.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func r3DamagedConclusionAudience(t *testing.T, missingJournal bool) {
	noJudgeWait(t)
	h := newR3IdleHarness(t)
	root := h.newSession(t, "", "audience-root", false)
	child := h.newSession(t, root.SessionID, "audience-child", true)
	ls, store := h.al.GetSessionLifecycleStore(), h.al.GetSessionStore()
	classifier := NewSteerRecordClassifier(ls, store)
	resolver := NewSteerAudienceResolver(classifier)
	if class, err := classifier.Classify(context.Background(), child.SessionID); err != nil || class != steer.ClassSteered {
		t.Fatalf("SETUP real Launch must classify as steered before damage: class=%q err=%v", class, err)
	}
	metaBefore, beforeErr := store.GetMeta(child.SessionID)
	if beforeErr != nil || metaBefore.ParentSessionID != root.SessionID {
		t.Fatalf("SETUP Launch must have persisted the real parent: meta=%+v err=%v", metaBefore, beforeErr)
	}
	if missingJournal {
		if err := os.Remove(filepath.Join(ls.Dir(), child.SessionID+".jsonl")); err != nil {
			t.Fatalf("SETUP remove journal: %v", err)
		}
		if _, err := ls.Load(child.SessionID); !errors.Is(err, session.ErrLifecycleNotFound) {
			t.Fatalf("SETUP expected ErrLifecycleNotFound: %v", err)
		}
	} else {
		if err := ls.Mutate(child.SessionID, func(rec *session.LifecycleRecord) error {
			rec.SteeredBy = nil // remove ONLY the edge; keep the modern Origin and metadata
			return nil
		}); err != nil {
			t.Fatalf("SETUP remove modern steering edge: %v", err)
		}
	}
	metaAfter, afterErr := store.GetMeta(child.SessionID)
	if afterErr != nil || metaAfter.ParentSessionID != metaBefore.ParentSessionID || metaAfter.Type != metaBefore.Type {
		t.Fatalf("SETUP parent metadata was not preserved: before=%+v after=%+v err=%v", metaBefore, metaAfter, afterErr)
	}
	if audience, class, err := resolver.Audience(context.Background(), child.SessionID); err != nil ||
		audience != steer.AudienceNone || class != steer.ClassDamagedChild {
		t.Fatalf("SETUP canonical audience/class = %q/%q err=%v, want none/damaged_child", audience, class, err)
	}

	steered, audienceErr := h.al.goalConclusionAudience(child.SessionID)
	if steered || audienceErr == nil {
		t.Errorf("G6 damaged child was guessed parentless: steered=%v err=%v, want an undeliverable audience error", steered, audienceErr)
	}
	judgeInst, _ := h.al.GetRegistry().GetAgent("judge")
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, errors.New("simulated judge outage")
	}}
	agent, _ := h.al.GetRegistry().GetAgent("native-agent")
	events, stop := recordAgentEvents(t, h.al)
	const evidence = "RESULT-R3-DAMAGED: the helper produced the report"
	h.al.dispatchDeferredGoalAdjudication(&goalDeferredAdjudicationWork{
		agentInst: agent, sessionID: child.SessionID, claimText: evidence,
	})
	stop()
	entries, err := store.ReadTranscript(child.SessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(child): %v", err)
	}
	keptUndeliverable := false
	for _, entry := range entries {
		if strings.Contains(entry.Content, "Not verified") && strings.Contains(entry.Content, evidence) &&
			strings.Contains(entry.Content, "could not be delivered") {
			keptUndeliverable = true
		}
	}
	if !keptUndeliverable {
		t.Error("G6 damaged child's result was not preserved as an explicitly undeliverable conclusion")
	}
	for _, frame := range goalStatusPayloadsFor(events, child.SessionID) {
		if frame.State == goalPillJudgeUnavailable && strings.Contains(frame.LatestReason, "Not verified") {
			t.Errorf("G6 damaged child reached the HUMAN conclusion/status arm: %+v", frame)
		}
	}
	payloads := r3ConclusionErrorPayloads(events)
	if len(payloads) == 0 {
		t.Error("G6 damaged audience emitted no judge_conclusion_failed notice")
	}
	for _, p := range payloads {
		if got := WireLLMError(p).Code; got != r3ConclusionFailedCode {
			t.Errorf("G6 damaged audience notice code=%q, want judge_conclusion_failed", got)
		}
	}
}

func r3OrdinaryRootConclusionAudience(t *testing.T) {
	noJudgeWait(t)
	al, agent, store, sid := newChannelGoal(t, "webchat", "r3-root-chat")
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), session.NewLifecycleStore(t.TempDir()))
	wireSteerCompletionDeps(t, al)
	if _, err := al.GetSessionLifecycleStore().Load(sid); !errors.Is(err, session.ErrLifecycleNotFound) {
		t.Fatalf("CONTROL ordinary root unexpectedly has a journal: %v", err)
	}
	resolver := NewSteerAudienceResolver(NewSteerRecordClassifier(al.GetSessionLifecycleStore(), store))
	if audience, class, err := resolver.Audience(context.Background(), sid); err != nil ||
		audience != steer.AudienceUser || class != steer.ClassOrdinaryRoot {
		t.Fatalf("CONTROL canonical ordinary root = %q/%q err=%v", audience, class, err)
	}
	drainOutbound(al.bus)
	al.dispatchDeferredGoalAdjudication(&goalDeferredAdjudicationWork{
		agentInst: agent, sessionID: sid, claimText: "RESULT-R3-ROOT: report completed",
	})
	texts := []string{}
	for _, msg := range drainOutbound(al.bus) {
		if msg.Channel == "webchat" && msg.ChatID == "r3-root-chat" && msg.SessionID == sid {
			texts = append(texts, msg.Content)
		}
	}
	r3AssertResultBearingConclusion(t, texts, "RESULT-R3-ROOT: report completed", "G6 genuine ordinary root human delivery")
}

func TestR3ConclusionAudience_DamagedChildIsUndeliverableButOrdinaryRootIsHuman(t *testing.T) {
	t.Run("missing_journal_with_parent_metadata", func(t *testing.T) { r3DamagedConclusionAudience(t, true) })
	t.Run("lost_modern_edge_with_parent_metadata", func(t *testing.T) { r3DamagedConclusionAudience(t, false) })
	t.Run("ordinary_root_without_journal", r3OrdinaryRootConclusionAudience)
}
