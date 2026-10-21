package agent

import (
	"bytes"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// SFH F7 remainder: exercise the real outer goal-check/deferred completion
// path, not only judgeDispatchNeedsOperator. Count adjudications and waits;
// a completed helper must carry its unverified work and the named settings fix.
func TestRouting1340_Round2OuterGoalStopsPermanentJudge(t *testing.T) {
	for _, name := range []string{"bad_model", "missing_named_client", "expired_sign_in"} {
		t.Run(name, func(t *testing.T) {
			missingRoute := name == "missing_named_client"
			waits := e7RecordJudgeBackoffWaits(t)
			model := "bad-model"
			if missingRoute {
				model = "z-ai/glm-5.2"
				t.Setenv("ROUTING1340_R2_MISSING", "")
			}
			mutate := func(cfg *config.Config) {
				for i := range cfg.Agents.List {
					if cfg.Agents.List[i].ID == string(coreagent.IDJudge) {
						cfg.Agents.List[i].Model = &config.AgentModelConfig{Primary: model}
						if missingRoute {
							cfg.Agents.List[i].Model.Provider = "openrouter"
						}
					}
				}
				if missingRoute {
					cfg.Providers = []*config.ModelConfig{{Provider: "openrouter", Model: model, APIKeyRef: "ROUTING1340_R2_MISSING"}}
				}
			}
			al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, mutate)
			lifecycle := session.NewLifecycleStore(t.TempDir())
			inbox := session.NewMessageInboxStore(t.TempDir())
			al.SetSessionMessagingStores(inbox, lifecycle)
			wireSteerCompletionDeps(t, al)
			parent, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
			if err != nil {
				t.Fatal(err)
			}
			rec := stampG5ExitedExecution(t, al, launchGoalBearingChild947(t, al, parent.ID, "routing1340-r2-operator"))
			judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
				if name == "expired_sign_in" {
					return nil, providers.ErrProviderNeedsSignIn
				}
				return nil, &providers.FailoverError{Reason: providers.FailoverBadModel, Provider: "openrouter", Model: model, Status: 400, Wrapped: &common.ProviderError{Status: 400, Body: `{"error":{"code":"model_not_found","message":"sk-proj-round2CANARY"}}`}}
			}}
			judgeInst.Provider = judge
			var adjudications atomic.Int64
			al.eventBus.SetSyncTap(func(e Event) {
				if e.Kind == EventKindTurnStart && e.Meta.AgentID == string(coreagent.IDJudge) {
					adjudications.Add(1)
				}
			})
			ts, err := al.reconstructSteeredTurn(rec, nil)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan string, 1)
			oldDone := goalDeferredAdjudicationDoneFn
			goalDeferredAdjudicationDoneFn = func(id string) { done <- id }
			t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })
			result := turnResult{finalContent: "[goal:evidence] R2-WORK-RESULT the report is ready\nGOAL_STATUS: met"}
			al.finishSteeredGoalTurn(ts, rec, &result, nil)
			select {
			case got := <-done:
				if got != rec.SessionID {
					t.Fatalf("completed session=%q", got)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("outer goal adjudication did not finish")
			}
			wantCalls := 1
			if missingRoute {
				wantCalls = 0
			}
			if got := judge.callCount(); got != wantCalls {
				t.Errorf("Judge provider calls=%d, want%d; permanent fault repeated or unavailable route was called", got, wantCalls)
			}
			if got := adjudications.Load(); got != 1 {
				t.Errorf("outer goal adjudications=%d, want1", got)
			}
			if got := waits.count(); got != 0 {
				t.Errorf("permanent Judge fault requested%d retry waits, want0", got)
			}
			child, err := lifecycle.Load(rec.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if child.State != session.LifecycleFailed || child.FinalDelivery == nil {
				t.Fatalf("helper was left unfinished: %+v", child)
			}
			messages, _, _, err := inbox.Drain(parent.ID, rec.SessionID, "", 10)
			if err != nil || len(messages) != 1 {
				t.Fatalf("parent messages=%d error=%v, want one final conclusion", len(messages), err)
			}
			conclusion, err := messages[0].AsSessionMessageError()
			if err != nil || !conclusion.Fatal {
				t.Fatalf("final parent message is not a fatal error: %+v error=%v", conclusion, err)
			}
			wantParts := []string{"Not verified", "openrouter", model, "settings", "R2-WORK-RESULT the report is ready"}
			if name == "expired_sign_in" {
				wantParts = []string{"Not verified", "sign-in expired", "sign in again under Settings", "R2-WORK-RESULT the report is ready"}
			}
			for _, want := range wantParts {
				if !strings.Contains(conclusion.Text, want) || !strings.Contains(child.FailedReason, want) {
					t.Errorf("durable conclusion missing%q: message=%q failed_reason=%q", want, conclusion.Text, child.FailedReason)
				}
			}
			if strings.Contains(conclusion.Text, "sk-proj") || strings.Contains(child.FailedReason, "sk-proj") {
				t.Fatal("provider response canary entered trusted conclusion")
			}
			raw, err := messages[0].MarshalJSON()
			if err != nil || !bytes.Equal(raw, child.FinalDelivery.Payload) {
				t.Errorf("parent message differs from committed outbox: %v", err)
			}
			goalRecord, err := resolveGoalRecordStore().Get(rec.GoalRef)
			if err != nil {
				t.Fatal(err)
			}
			if !goalRecord.IsActive() || goalRecord.Round != 0 || goalRecord.LatestVerdict != nil {
				t.Fatalf("unavailable Judge consumed a round or ended the goal: %+v", goalRecord)
			}
		})
	}
}
