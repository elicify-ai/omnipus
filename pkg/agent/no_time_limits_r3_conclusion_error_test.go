package agent

// PR #1311 / RED dispatch G5, architect R2-3 decision: all conclusion
// handling failures use judge_conclusion_failed, curated catalogue text,
// retryable=false, attribution=ambiguous, without a provider-message flag.
// The independent exact-copy oracle is acceptance data under tests/fixtures,
// not a hand-copy in production code or a value learned from a test run.
// CHECK: restore CodeUnknown at one producer; leak raw text; mark retryable.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
)

const r3ConclusionFailedCode = "judge_conclusion_failed"

func r3ConclusionCatalogueCopy(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "no-time-limits-r3", "judge-conclusion-failed-copy.txt"))
	if err != nil {
		t.Fatalf("read agreed R2-3 acceptance copy: %v", err)
	}
	return strings.TrimSuffix(string(body), "\n")
}

func r3ConclusionErrorPayloads(events *eventCollector) []ErrorPayload {
	events.mu.Lock()
	defer events.mu.Unlock()
	var out []ErrorPayload
	for _, event := range events.events {
		if p, ok := event.Payload.(ErrorPayload); ok && p.Stage == "goal_conclusion" {
			out = append(out, p)
		}
	}
	return out
}

func r3AssertConclusionFailureWire(t *testing.T, events *eventCollector, sid, path string) {
	t.Helper()
	payloads := r3ConclusionErrorPayloads(events)
	if len(payloads) == 0 {
		t.Fatalf("G5 %s: no goal_conclusion failure was emitted", path)
	}
	wantCopy := r3ConclusionCatalogueCopy(t)
	for _, p := range payloads {
		wire := WireLLMError(p) // REAL assembly point, not a hand-made wire fixture
		if p.Code != r3ConclusionFailedCode || wire.Code != r3ConclusionFailedCode {
			t.Errorf("G5 %s: payload/wire code=%q/%q, want judge_conclusion_failed (not unknown)", path, p.Code, wire.Code)
		}
		if p.Message != wantCopy || wire.Message != wantCopy {
			t.Errorf("G5 %s: payload/wire Message must be the exact agreed catalogue sentence, got %q / %q", path, p.Message, wire.Message)
		}
		if p.SessionID != sid || wire.Retryable || (wire.ProviderMessage != nil && *wire.ProviderMessage) {
			t.Errorf("G5 %s: wrong session/retry/trust shape: payload=%+v wire=%+v", path, p, wire)
		}
	}
	if got := generated.LLMErrorUserAttributions[r3ConclusionFailedCode]; got != generated.LLMErrorAttributionAmbiguous {
		t.Errorf("G5 %s: catalogue attribution=%q, want ambiguous", path, got)
	}
}

func r3CorruptGoalDirectory(t *testing.T) {
	t.Helper()
	dir := resolveGoalRecordStore().Dir()
	if err := os.Rename(dir, dir+"-saved"); err != nil {
		t.Fatalf("SETUP save goal directory: %v", err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("SETUP block real goal reads: %v", err)
	}
	if _, err := activeGoalForSession("instrument-positive-control"); err == nil || !strings.Contains(err.Error(), "goal: list") {
		t.Fatalf("SETUP real goal-directory read fault was not observed: %v", err)
	}
}

func TestR3ConclusionFailure_SaveReadAndUpdateUseTypedSafeWireNotice(t *testing.T) {
	for _, failure := range []string{"transcript_append", "goal_read", "goal_update"} {
		t.Run(failure, func(t *testing.T) {
			oldRegistry := currentVerifierSessionRegistry()
			t.Cleanup(func() { SetVerifierSessionRegistry(oldRegistry) })
			g := setUpGoalClaimAwaitingJudge(t, &fakeJudgeProvider{})
			original := appendGoalConclusionFn
			t.Cleanup(func() { appendGoalConclusionFn = original })
			appendCalls := 0
			appendGoalConclusionFn = func(al *AgentLoop, store *session.UnifiedStore, sid, aid, text string) error {
				appendCalls++
				if failure == "transcript_append" {
					return errors.New("disk full") // process-edge filesystem-write failure only
				}
				if err := original(al, store, sid, aid, text); err != nil {
					return err
				}
				if failure == "goal_read" {
					r3CorruptGoalDirectory(t)
				} else {
					// The record is readable but its stored reference is damaged.
					// A real Update must fail, not a mocked Update returning a value.
					gs := resolveGoalRecordStore()
					rec := mustGoalRecord(t, g.goalID)
					rec.GoalID = "r3-damaged-goal-reference"
					body, err := json.Marshal(rec)
					if err != nil {
						t.Fatalf("SETUP marshal damaged reference: %v", err)
					}
					if writeErr := os.WriteFile(filepath.Join(gs.Dir(), g.goalID+".json"), body, 0o600); writeErr != nil {
						t.Fatalf("SETUP damage goal reference: %v", writeErr)
					}
					cur, err := activeGoalForSession(sid)
					if err != nil || cur == nil || cur.GoalID != rec.GoalID {
						t.Fatalf("SETUP goal must still READ successfully: rec=%+v err=%v", cur, err)
					}
					if _, err := gs.Update(cur.GoalID, func(*goal.Goal) error { return nil }); err == nil {
						t.Fatal("SETUP damaged reference must fail a real goal Update")
					}
				}
				return nil
			}
			g.al.concludeInteractiveGoalNotVerified(g.store, g.work,
				"Not verified: the Judge could not finish. Work result: all tests green")
			g.stopEvents()
			r3AssertConclusionFailureWire(t, g.events, g.sid, failure)
			if failure == "transcript_append" && appendCalls != 2 {
				t.Errorf("G5 bounded save attempts=%d, want 2", appendCalls)
			}
		})
	}
}

func TestR3ConclusionFailure_UnreadableAudienceUsesTypedSafeWireNotice(t *testing.T) {
	noJudgeWait(t)
	al, _, _, sid := newChannelGoal(t, "webchat", "r3-audience-chat")
	// Keep an actual lifecycle store; an operational audience read failure is
	// not the genuinely parentless-by-construction control.
	dir := filepath.Join(config.OmnipusHomeDir(), "r3-audience")
	ls := session.NewLifecycleStore(dir)
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), ls)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("SETUP lifecycle directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte("{unreadable journal\n"), 0o600); err != nil {
		t.Fatalf("SETUP unreadable lifecycle: %v", err)
	}
	if _, err := ls.Load(sid); err == nil || errors.Is(err, session.ErrLifecycleNotFound) {
		t.Fatalf("SETUP expected operational read failure, not absence: %v", err)
	}
	events, stop := recordAgentEvents(t, al)
	steered, audienceErr := al.goalConclusionAudience(sid)
	if audienceErr == nil || steered {
		t.Fatalf("SETUP expected unavailable audience, got steered=%v err=%v", steered, audienceErr)
	}
	agent, _ := al.GetRegistry().GetAgent("native-agent")
	al.reportUndeliverableGoalConclusion(al.GetSessionStore(), &goalDeferredAdjudicationWork{
		agentInst: agent, sessionID: sid, claimText: "RESULT-R3-AUDIENCE: report completed",
	}, "Not verified: RESULT-R3-AUDIENCE: report completed", audienceErr)
	stop()
	r3AssertConclusionFailureWire(t, events, sid, "audience_read")
}

func TestR3ConclusionFailure_RouteReadMissingBusAndPublishUseTypedSafeWireNotice(t *testing.T) {
	for _, failure := range []string{"route_read", "missing_route", "missing_bus", "publish_failed"} {
		t.Run(failure, func(t *testing.T) {
			al, _, _, sid := newChannelGoal(t, "webchat", "r3-route-chat")
			events, stop := recordAgentEvents(t, al)
			switch failure {
			case "route_read", "missing_route":
				state := goalTriggers()
				state.mu.Lock()
				delete(state.routing, sid) // remove only the process's route cache
				state.mu.Unlock()
				if failure == "route_read" {
					r3CorruptGoalDirectory(t)
				} else {
					gid := goalRecordForSession(t, sid).GoalID
					if _, err := resolveGoalRecordStore().Update(gid, func(g *goal.Goal) error {
						g.RouteChannel, g.RouteChatID = "", ""
						return nil
					}); err != nil {
						t.Fatalf("SETUP remove persisted route: %v", err)
					}
				}
			case "missing_bus":
				original := al.bus
				al.bus = nil
				t.Cleanup(func() { al.bus = original })
			case "publish_failed":
				al.bus.Close() // the real transport rejects publication with ErrBusClosed
				if err := al.bus.PublishOutbound(context.Background(), bus.OutboundMessage{SessionID: sid, Channel: "webchat", ChatID: "r3-route-chat", Content: "transport probe"}); err == nil {
					t.Fatal("SETUP a closed real bus accepted publication")
				}
			}
			al.deliverGoalConclusionToRoute(sid, "Not verified: RESULT-R3-ROUTE: report completed")
			stop()
			r3AssertConclusionFailureWire(t, events, sid, failure)
		})
	}
}
