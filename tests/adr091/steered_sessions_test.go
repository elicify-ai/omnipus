package adr091_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type e2eHarness struct {
	tree       *testutil.Tree
	sessions   *session.UnifiedStore
	lifecycle  *session.LifecycleStore
	audience   steer.AudienceResolver
	deliverer  steer.UpwardDeliverer
	canceller  steer.Canceller
	classifier steer.RecordClassifier
	launcher   steer.SessionLauncher
}

func newE2EHarness(t *testing.T) *e2eHarness {
	t.Helper()
	home := t.TempDir()
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{
			Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "scripted-model"}, MaxTokens: 4096,
		},
		List: []config.AgentConfig{
			{ID: "mia", Name: "ADR-091 fixture agent", Type: config.AgentTypeCustom, Home: filepath.Join(home, "agents", "mia")},
			{ID: "adr091-fixture-task-agent", Name: "ADR-091 task fixture agent", Type: config.AgentTypeCustom, Home: filepath.Join(home, "agents", "task")},
		},
	}}
	al, err := agent.NewAgentLoop(cfg, msgBus, testutil.NewScenario())
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	t.Cleanup(al.Close)
	sessions := al.GetSessionStore()
	if sessions == nil {
		t.Fatal("NewAgentLoop did not construct the shared session store")
	}
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "inbox"))
	al.SetSessionMessagingStores(inbox, lifecycle)
	classifier := agent.NewSteerRecordClassifier(lifecycle, sessions)
	audience := agent.NewSteerAudienceResolver(classifier)
	deliverer := agent.NewSteerUpwardDeliverer()
	al.SetSteerAudienceDeps(audience, steer.NopBoundaryObserver{}, deliverer)
	launcher := agent.NewSteerLauncher(al)
	canceller := agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel)
	cancelPersister := al.StartSubagentSpawnPersister(context.Background())
	t.Cleanup(cancelPersister)
	deps := steer.Deps{
		Canceller: canceller, Deliverer: deliverer, Classifier: classifier,
		LifecycleStore: lifecycle, SessionStore: sessions,
		BootHook: func(context.Context) error { return nil },
	}
	return &e2eHarness{
		tree: testutil.DelegationTree(t, deps, 3), sessions: sessions,
		lifecycle: lifecycle, audience: audience, deliverer: deliverer,
		canceller: canceller, classifier: classifier, launcher: launcher,
	}
}

func TestE2E_ThreeLevelDelegation_NoLeak(t *testing.T) {
	h := newE2EHarness(t)
	recorder := testutil.RecordingOutbound(t)
	children := []testutil.TreeNode{h.tree.A, h.tree.B, h.tree.C}

	if err := h.sessions.AppendTranscriptStrict(h.tree.C.SessionID, session.TranscriptEntry{
		ID: "child-control", Type: session.EntryTypeToolCall, Role: "tool",
		Content: "intentional tool failure", Status: "error", AgentID: h.tree.C.AgentID,
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("write child transcript control: %v", err)
	}
	recorder.Record(steer.BoundarySyncToolText, h.tree.C.SessionID, h.tree.C.SessionID, "tool_error")

	for phase := 0; phase < 2; phase++ { // first entry, then reconstructed re-entry
		for _, child := range children {
			for _, boundary := range steer.Boundaries {
				audience, class, err := h.audience.Audience(context.Background(), child.SessionID)
				if err != nil {
					t.Fatalf("phase %d audience(%s, %s): %v", phase, child.Name, boundary, err)
				}
				if class != steer.ClassSteered {
					t.Fatalf("phase %d class(%s) = %q, want steered", phase, child.Name, class)
				}
				recorder.Observe(boundary, child.SessionID, audience)
				if audience == steer.AudienceUser {
					recorder.Record(boundary, child.SessionID, h.tree.Root.SessionID, "leak")
				}
			}
		}
	}
	for _, boundary := range steer.Boundaries {
		recorder.AssertBoundaryInvoked(boundary)
	}
	recorder.AssertReceived(h.tree.C.SessionID, "tool_error")
	recorder.AssertNothingTo(h.tree.Root.SessionID)
}

func TestE2E_StopReachesReenteredChild(t *testing.T) {
	h := newE2EHarness(t)
	if class, err := h.classifier.Classify(context.Background(), h.tree.C.SessionID); err != nil || class != steer.ClassSteered {
		t.Fatalf("re-entered child classification = %q, %v; want steered", class, err)
	}
	report, err := h.tree.Stop(h.tree.Root.SessionID)
	if err != nil {
		t.Fatalf("Stop(root): %v", err)
	}
	for _, node := range h.tree.Nodes {
		if !slices.Contains(report.Reached, node.SessionID) {
			t.Fatalf("Stop(root) reached %v, want re-entered node %s (%s)", report.Reached, node.Name, node.SessionID)
		}
	}
}

func TestE2E_StopSurvivesRestart(t *testing.T) {
	h := newE2EHarness(t)
	if _, err := h.tree.Stop(h.tree.Root.SessionID); err != nil {
		t.Fatalf("Stop(root): %v", err)
	}
	stopped, err := h.lifecycle.Load(h.tree.B.SessionID)
	if err != nil {
		t.Fatalf("load B after Stop: %v", err)
	}
	if stopped.Stop == nil || stopped.Stop.Generation != stopped.Generation {
		t.Fatalf("B stop marker = %+v at generation %d, want current-generation marker", stopped.Stop, stopped.Generation)
	}
	oldGeneration := stopped.Generation
	if err := h.tree.Crash(); err != nil {
		t.Fatalf("Crash: %v", err)
	}
	if err := h.tree.Reboot(context.Background()); err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	reopened, err := h.tree.Deps().LifecycleStore.Load(h.tree.B.SessionID)
	if err != nil {
		t.Fatalf("load B after reboot: %v", err)
	}
	if reopened.Stop == nil || reopened.Stop.Generation != oldGeneration {
		t.Fatalf("B stop marker after reboot = %+v, want generation %d", reopened.Stop, oldGeneration)
	}
	newGeneration, err := h.tree.Revive(h.tree.B.SessionID)
	if err != nil {
		t.Fatalf("Revive(B): %v", err)
	}
	if newGeneration <= oldGeneration {
		t.Fatalf("Revive(B) generation = %d, want > %d", newGeneration, oldGeneration)
	}
}

func TestE2E_CompletionWakesPerChild(t *testing.T) {
	h := newE2EHarness(t)
	for _, parent := range []testutil.TreeNode{h.tree.A, h.tree.B} {
		delivery, err := h.tree.Reenter(parent.SessionID)
		if err != nil {
			t.Fatalf("completion handback to %s: %v", parent.Name, err)
		}
		if delivery.MessageID == "" {
			t.Fatalf("completion handback to %s has empty message id", parent.Name)
		}
		if delivery.Outcome != steer.DeliveryWoke && delivery.Outcome != steer.DeliveryQueuedIntoLiveTurn {
			t.Fatalf("completion handback to %s outcome = %q, want woke or queued_into_live_turn", parent.Name, delivery.Outcome)
		}
	}
}

func TestE2E_Restart_ParentToldOnce(t *testing.T) {
	h := newE2EHarness(t)
	first, err := h.tree.QueueWake(h.tree.B.SessionID, h.tree.B.Generation)
	if err != nil {
		t.Fatalf("persist terminal handback before crash: %v", err)
	}
	if first.MessageID == "" {
		t.Fatal("first terminal delivery has empty message id")
	}
	if err := h.tree.Crash(); err != nil {
		t.Fatalf("Crash: %v", err)
	}
	if err := h.tree.Reboot(context.Background()); err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	second, err := h.tree.QueueWake(h.tree.B.SessionID, h.tree.B.Generation)
	if err != nil {
		t.Fatalf("repair terminal handback after reboot: %v", err)
	}
	if second.MessageID != first.MessageID {
		t.Fatalf("terminal delivery id changed across restart: before=%q after=%q", first.MessageID, second.MessageID)
	}
}

func TestE2E_TaskChildInSidePanel(t *testing.T) {
	h := newE2EHarness(t)
	taskChild := seedTaskChild(t, h, h.tree.Root)
	message := progressMessage(t, taskChild, h.tree.Root.SessionID)
	delivery, err := h.deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: taskChild.SessionID, Outcome: steer.OutcomeProgress, Message: message,
	})
	if err != nil {
		t.Fatalf("deliver task-child progress: %v", err)
	}
	if delivery.MessageID == "" {
		t.Fatal("task-child progress delivery has empty message id")
	}

	entries, err := h.sessions.ReadTranscript(h.tree.Root.SessionID)
	if err != nil {
		t.Fatalf("read parent transcript: %v", err)
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal parent transcript: %v", err)
	}
	for _, frameType := range []string{"subagent_start", "subagent_state", "subagent_message"} {
		if !strings.Contains(string(raw), frameType) {
			t.Fatalf("parent transcript lacks persisted %s event for task child; replay cannot restore the side-panel row", frameType)
		}
	}
}

func seedTaskChild(t *testing.T, h *e2eHarness, parent testutil.TreeNode) testutil.TreeNode {
	t.Helper()
	const agentID = "adr091-fixture-task-agent"
	result, err := h.launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parent.SessionID,
		TargetAgentID:     agentID,
		Label:             "ADR-091 task child",
		Task:              "Exercise the task-origin side-panel path",
		Origin:            steer.Origin{Kind: steer.OriginKindTask, CallID: "create-task-call", TaskID: "task-record"},
	})
	if err != nil {
		t.Fatalf("launch task child: %v", err)
	}
	node := testutil.TreeNode{
		Name: "task", SessionID: result.SessionID, AgentID: agentID,
		WorkspaceID: parent.WorkspaceID, Generation: result.Generation,
	}
	return node
}

func progressMessage(t *testing.T, child testutil.TreeNode, parentID string) generated.SessionMessage {
	t.Helper()
	gen := child.Generation
	var message generated.SessionMessage
	if err := message.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId: fmt.Sprintf("%s:%d:progress", child.SessionID, child.Generation),
		SessionId: child.SessionID, ParentSessionId: &parentID,
		CreatedAt: time.Now().UTC(), Generation: &gen, Depth: 1,
		Direction:      generated.SessionMessageProgressDirection("child_to_parent"),
		SenderIdentity: child.AgentID, Text: "task child is working", UntrustedOrigin: true,
	}); err != nil {
		t.Fatalf("encode task child progress: %v", err)
	}
	return message
}
