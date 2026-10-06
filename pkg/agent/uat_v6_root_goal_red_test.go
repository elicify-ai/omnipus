package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Oracle: dispatch V6 and validator/report-v.md::V6 (2026-10-06).
// A root has no parent. Its met verdict belongs to its own goal_outcome,
// not an attempted upward delivery or an unacknowledged recovery message.
const qa3RootGoalIntent = "report exactly ROOT-GOAL-COMPLETE in this chat"
const qa3RootGoalCriterion = "the answer reports exactly ROOT-GOAL-COMPLETE"
const qa3RootGoalDoD = "the answer is available in the owning chat"
const qa3RootGoalAnswer = "ROOT-GOAL-COMPLETE"

func TestQAV6RootGoal_MetStaysInOwnChatWithoutUpwardFailure(t *testing.T) {
	resetGoalTriggerStateForTest()
	worker := &e2eScriptedProvider{
		scripted: []*providers.LLMResponse{
			e2eToolCallResponse("qa3-v6-register", "set_goal", fmt.Sprintf(
				`{"definition":%q,"criteria":[{"text":%q,"judgment":"boolean"}],`+
					`"dod":[{"text":%q,"judgment":"boolean","provenance":"stated"}],"assessment":{"clarity":"clear"}}`,
				qa3RootGoalIntent, qa3RootGoalCriterion, qa3RootGoalDoD)),
			e2eToolCallResponse("qa3-v6-claim", "goal_claim", fmt.Sprintf(`{"status":"met","evidence":%q}`, qa3RootGoalAnswer)),
			e2eTextResponse(qa3RootGoalAnswer),
		},
		fallback: "UNEXPECTED ROOT TURN",
	}
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) {
		cfg.Agents.Defaults.Home = config.OmnipusHomeDir()
		cfg.Agents.Defaults.DefaultAgentID = "native-agent"
		cfg.Agents.Defaults.MaxTokens = 4096
		cfg.Agents.Defaults.MaxToolIterations = 6
		cfg.Agents.List[0].Type = "" // Ordinary chat agent, not a delegation-only worker.
		cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
	})
	home := config.OmnipusHomeDir()
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "session_messages")), session.NewLifecycleStore(filepath.Join(home, "session_lifecycle")))
	wireSteerCompletionDeps(t, al)
	boot := session.NewBootEpochStore(home)
	if epoch, err := boot.Mint(); err != nil || epoch == 0 {
		t.Fatalf("SETUP: real boot mint = %d, error=%v", epoch, err)
	}
	al.SetBootEpochStore(boot)
	judge := &b6ScriptedJudge{metFromCall: 1}
	judgeInst.Provider = judge
	inst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("BLOCKED: ordinary goal agent not registered — required by V6")
	}
	for _, name := range []string{"set_goal", "goal_claim"} {
		if _, ok := inst.Tools.Get(name); !ok {
			t.Fatalf("BLOCKED: %s not registered — required by V6", name)
		}
	}
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", inst.ID)
	if err != nil {
		t.Fatalf("SETUP: real root chat: %v", err)
	}
	readLog := qa3CaptureGoalLog(t)
	logger.WarnCF("agent", "qa3-v6-log-instrument-control", map[string]any{"session_id": meta.ID})
	if !strings.Contains(readLog(), "qa3-v6-log-instrument-control") {
		t.Fatal("SETUP: warning capture cannot see a known warning")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	msg := bus.InboundMessage{Channel: "webchat", ChatID: meta.ID, SessionID: meta.ID,
		Sender: bus.SenderInfo{CanonicalID: "qa3-root-owner"}, GatewayUserID: "qa3-root-owner",
		UserInitiated: true, Content: "/goal " + qa3RootGoalIntent}
	opts := processOptions{TranscriptStore: store, TranscriptSessionID: meta.ID,
		Channel: msg.Channel, ChatID: msg.ChatID, SessionKey: "webchat:" + meta.ID,
		SenderID: msg.Sender.CanonicalID, UserInitiated: true, DefaultResponse: "done"}
	matched, handled, reply := al.applyGoalCommandPrompt(ctx, msg, inst, &opts)
	if !matched || handled || reply != "" || opts.UserMessage != qa3RootGoalIntent {
		t.Fatalf("SETUP: real /goal activation = matched:%v handled:%v reply:%q input:%q", matched, handled, reply, opts.UserMessage)
	}
	g := mustActiveGoalRecord(t, meta.ID)
	qa3ProveRootVerdictInboxInstrument(t, al, meta.ID, g.GoalID)
	// Same ordinary admission and turn constructor as runInboundTurnWithRevival.
	// Drive the deferred dispatch synchronously AFTER the real answer publication,
	// joining the whole Judge/tail without setting goalDeferredAdjudicationDoneFn
	// or any other production hook. No stores, tools, dispatchers or tails are faked.
	prepared, err := al.prepareOrdinaryExecution(ctx, msg, opts)
	if err != nil || prepared.execution == nil {
		t.Fatalf("SETUP: real ordinary root admission = %+v, error=%v", prepared, err)
	}
	opts.executionDisposition = prepared.execution
	ts, err := al.newTurnStateForAdmission(inst, opts)
	if err != nil {
		t.Fatalf("SETUP: real admitted root turn: %v", err)
	}
	root := rootReopenedRecord(t, al, meta.ID)
	if root.SteeredBy != nil || root.ExecutionID == nil || root.ExecutionID.RunID == "" || root.State != session.LifecycleRunning {
		t.Fatalf("SETUP: must be a genuinely admitted root with NO steering parent: %+v", root)
	}
	result, runErr := al.runTurn(ctx, ts)
	prepared.execution.recordTurnOutcome(runErr)
	if runErr != nil || result.finalContent != qa3RootGoalAnswer {
		t.Fatalf("SETUP: real root goal turn = (%q, %v), want exact scripted answer", result.finalContent, runErr)
	}
	al.checkGoalLoopAfterTurn(ctx, inst, opts, &result)
	if result.goalDeferredAdjudication == nil || result.goalDeferredAdjudication.claimText != qa3RootGoalAnswer {
		t.Fatalf("SETUP: real tool claim did not dispatch its evidence: %+v", result.goalDeferredAdjudication)
	}
	if err := al.bus.PublishOutbound(ctx, bus.OutboundMessage{Channel: msg.Channel, ChatID: msg.ChatID, SessionID: meta.ID, Content: result.finalContent}); err != nil {
		t.Fatalf("SETUP: publish root's actual answer before adjudication: %v", err)
	}
	if err := al.finishExecutionDisposition(prepared.execution); err != nil {
		t.Fatalf("SETUP: settle the producing ordinary admission: %v", err)
	}
	al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)
	settled := mustGoalRecord(t, g.GoalID)
	if settled.State != generated.GoalStateMet || settled.Round != 1 || settled.LatestVerdict == nil || !settled.LatestVerdict.Met || settled.LatestClaim == nil || settled.LatestClaim.Evidence != qa3RootGoalAnswer {
		t.Fatalf("V6: real claim/Judge did not settle this root goal met in one round: %+v", settled)
	}
	if judge.callCount() != 1 || !reflect.DeepEqual(judge.askedOnCall(1), []string{qa3RootGoalCriterion, qa3RootGoalDoD}) {
		t.Errorf("V6: Judge calls=%d criteria=%v; want one real round for the submitted criterion and DoD", judge.callCount(), judge.askedOnCall(1))
	}
	outcome := requireOneGoalOutcome(t, store, meta.ID, g.GoalID)
	if outcome.GoalOutcome.Ending != generated.GoalOutcomeEndingMet || outcome.GoalOutcome.GoalText != qa3RootGoalIntent || outcome.GoalOutcome.RoundsUsed != 1 || outcome.GoalOutcome.CriteriaTotal == nil || *outcome.GoalOutcome.CriteriaTotal != 2 {
		t.Errorf("V6: root's own lasting goal_outcome=%+v; want met, original intent, one round, two submitted items", outcome.GoalOutcome)
	}
	for _, forbidden := range []string{"goal-status upward delivery failed", "met verdict fallback wake failed"} {
		if strings.Contains(readLog(), forbidden) {
			t.Errorf("V6: root with NO steering parent logged %q; want no upward-delivery or fallback-wake attempt", forbidden)
		}
	}
	qa3AssertNoPendingRootVerdict(t, al, meta.ID)
	// All production dispatch/tail calls above have returned: drain discrete
	// bus messages rather than sleeping to infer absence of an extra turn.
	for {
		select {
		case wake := <-al.bus.InboundChan():
			t.Errorf("V6: met root queued an unsolicited extra turn: %+v", wake)
			if _, err := al.processSystemMessage(ctx, wake); err != nil {
				t.Errorf("V6: unsolicited wake could not be consumed: %v", err)
			}
		default:
			if got := worker.callCount(); got != 3 { // set_goal, goal_claim, final — one turn.
				t.Errorf("V6: root model calls=%d, want exactly 3 in its single goal turn, no extra parent turn", got)
			}
			return
		}
	}
}

func qa3CaptureGoalLog(t *testing.T) func() string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "root-goal.log")
	previous := logger.GetLevel()
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(path); err != nil {
		t.Fatalf("SETUP: capture real application logs: %v", err)
	}
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(previous) })
	return func() string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read root goal log: %v", err)
		}
		return string(data)
	}
}

func qa3AssertNoPendingRootVerdict(t *testing.T, al *AgentLoop, rootID string) {
	t.Helper()
	if pending := qa3PendingRootVerdicts(t, al, rootID); len(pending) != 0 {
		t.Errorf("V6: root verdict remains unacknowledged for boot recovery: %v; want zero pending goal_status entries", pending)
	}
}

func qa3PendingRootVerdicts(t *testing.T, al *AgentLoop, rootID string) []string {
	t.Helper()
	var pending []string
	inboxDir := filepath.Join(al.GetConfig().Agents.Defaults.Home, "session_messages")
	files, err := os.ReadDir(inboxDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil // No inbox files means no stored recovery verdict anywhere.
	}
	if err != nil {
		t.Fatalf("inspect every real inbox owner for root verdict recovery: %v", err)
	}
	reopened := session.NewMessageInboxStore(inboxDir)
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".jsonl") {
			continue
		}
		owner := strings.TrimSuffix(file.Name(), ".jsonl")
		msgs, _, more, err := reopened.Drain(owner, rootID, "", session.DefaultInboxUnackedMax)
		if err != nil {
			t.Fatalf("read pending root messages for inbox %q: %v", owner, err)
		}
		if more {
			t.Fatalf("root produced more pending messages than this bounded test can inspect in inbox %q", owner)
		}
		for _, message := range msgs {
			class, err := session.ClassifySessionMessage(message)
			if err != nil {
				t.Fatalf("classify persisted root inbox message: %v", err)
			}
			if class.Kind == "goal_status" {
				status, err := message.AsSessionMessageGoalStatus()
				if err != nil {
					t.Fatalf("decode root verdict: %v", err)
				}
				pending = append(pending, owner+":"+status.MessageId)
			}
		}
	}
	return pending
}

// Prove the SAME all-owner scan sees a real stored unacknowledged goal_status,
// and stops seeing it only after a real durable Ack. This is an instrument
// control, not a mocked goal verdict: the Judge still runs independently.
func qa3ProveRootVerdictInboxInstrument(t *testing.T, al *AgentLoop, rootID, goalID string) {
	t.Helper()
	const controlID = "qa3-v6-inbox-instrument-control"
	var message generated.SessionMessage
	if err := message.FromSessionMessageGoalStatus(generated.SessionMessageGoalStatus{
		Kind: generated.SessionMessageGoalStatusKindGoalStatus, MessageId: controlID,
		SessionId: rootID, SenderIdentity: "judge", CreatedAt: time.Now().UTC(), Depth: 1,
		GoalId: goalID, Condition: generated.SessionMessageGoalStatusConditionMet,
		Direction: generated.SessionMessageGoalStatusDirectionSessionToParent,
	}); err != nil {
		t.Fatalf("SETUP: encode inbox instrument control: %v", err)
	}
	if _, err := al.GetMessageInboxStore().Append(rootID, message); err != nil {
		t.Fatalf("SETUP: persist inbox instrument control: %v", err)
	}
	if got := qa3PendingRootVerdicts(t, al, rootID); !reflect.DeepEqual(got, []string{rootID + ":" + controlID}) {
		t.Fatalf("SETUP: root verdict scan missed injected pending control: %v", got)
	}
	if err := al.GetMessageInboxStore().Ack(rootID, []string{controlID}); err != nil {
		t.Fatalf("SETUP: acknowledge inbox instrument control: %v", err)
	}
	if got := qa3PendingRootVerdicts(t, al, rootID); len(got) != 0 {
		t.Fatalf("SETUP: root verdict scan retained acknowledged control: %v", got)
	}
}
