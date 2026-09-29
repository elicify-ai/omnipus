// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestWireSteerDeps_BootHookDescendantTerminalDrivesReevaluation is the
// gateway-level regression for the DescendantTerminal wiring gap (fix-890
// squad-lead contract D, pr-test-analyzer finding): deleting the
// `DescendantTerminal: stg.agentLoop.ResumeDeferredGoalAfterDescendantTerminal`
// assignment inside wireSteerDeps's BootHook closure (gateway_boot.go) left
// every existing test green, because TestWireSteerDeps_InstallsEveryProductionDependency
// only inspects the setters wireSteerDeps calls directly — the BootHook
// closure's own locally-constructed *agent.SteerBootRecovery is never stored
// on any object a test can reach afterward.
//
// This drives the REAL composition root end to end: a real wireSteerDeps(),
// a real BootHook closure, a real SteerBootRecovery.Run — through the exact
// production entrypoint gateway.go calls at boot. It proves the wiring
// matters by observing its ONE externally-visible effect: a descendant that
// finished before a crash (already terminal, no live in-process hook ever
// ran for it) makes its still-active OWNER receive its Q2=B "fresh-claim"
// re-evaluation turn on the next boot (gateway_boot.go's own doc comment,
// "Q2=B (#984 follow-up)"). Without the wiring, ResumeDeferredGoalAfterDescendantTerminal
// is never called for the descendant and no re-evaluation is ever dispatched
// — this test times out waiting for it instead of observing it.
//
// The owner is launched as an ORDINARY ROOT (no SteeringSessionID) rather
// than itself a delegated child, deliberately: SteerBootRecovery.Run's own
// switch statement (boot_sweep.go) leaves `steer.ClassOrdinaryRoot` sessions
// untouched ("existing root boot recovery remains authoritative") — only
// `steer.ClassSteered` sessions go through recoverSteered's interrupt path.
// A steered owner would itself be marked failed/interrupted by the SAME
// Run() call (Q7=A, already covered by TestGoalQ2B_BootDescendantFirstCannotResumePendingOwner
// in pkg/agent), which would defeat this test's own premise (the owner must
// still be ACTIVE when the descendant's catch-up fires). An ordinary-root
// owner is exactly the shape gateway_boot.go's comment describes: a
// goal-bearing session that delegated work and is otherwise untouched by
// boot.
func TestWireSteerDeps_BootHookDescendantTerminalDrivesReevaluation(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096,
			},
			List: []config.AgentConfig{{ID: "native-agent", Name: "Native Agent", Type: config.AgentTypeWorker, Home: t.TempDir()}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)

	launcher := agent.NewSteerLauncher(al)
	ctx := context.Background()

	// The goal-bearing OWNER: an ordinary root carrying the goal that must
	// survive boot untouched (see the function doc comment for why root,
	// not steered).
	ownerRes, err := launcher.Launch(ctx, steer.LaunchRequest{
		TargetAgentID: "native-agent",
		Task:          "own the goal the descendant-terminal wiring test resumes",
		// OriginKindChat, not Delegate: launchSessionType maps Delegate to
		// session.SessionTypeDelegate, which the classifier's Row 5 treats
		// as "meta names a parent" — an ordinary root with SteeredBy==nil
		// but a delegate-typed meta classifies as damaged_child, not
		// ordinary_root, and gets refused at boot instead of left alone
		// (steer_classify.go). Chat is the genuine root-launch kind.
		Origin: steer.Origin{Kind: steer.OriginKindChat},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the delegated work is complete"}},
			DoD:      []steer.Criterion{{Text: "the evidence is sufficient"}},
		},
	})
	if err != nil {
		t.Fatalf("Launch(owner): %v", err)
	}
	ownerRec, err := lifecycle.Load(ownerRes.SessionID)
	if err != nil {
		t.Fatalf("Load(owner): %v", err)
	}
	if ownerRec.GoalRef == "" {
		t.Fatal("arrange: owner launch did not mint a goal record (GoalRef empty)")
	}

	// The DESCENDANT: steered off the owner, already terminal — the "crashed
	// after its own lifecycle write, before the live hook ran" shape the boot
	// catch-up exists for.
	descendantRes, err := launcher.Launch(ctx, steer.LaunchRequest{
		SteeringSessionID: ownerRes.SessionID,
		TargetAgentID:     "native-agent",
		Task:              "the delegated work the owner is waiting on",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "gw-q2b-descendant"},
	})
	if err != nil {
		t.Fatalf("Launch(descendant): %v", err)
	}
	if err := lifecycle.Mutate(descendantRes.SessionID, func(rec *session.LifecycleRecord) error {
		rec.State = session.LifecycleCompleted
		return nil
	}); err != nil {
		t.Fatalf("mark descendant terminal: %v", err)
	}

	// Durable evidence of a met claim with no verdict yet: the exact shape
	// goalRestoreWaitingCompletion reconstructs the waiting_descendants phase
	// from after a restart (goal_child_completion.go), and RouteChannel/
	// RouteChatID so the re-evaluation dispatch has somewhere to go without
	// needing the process-local routing map a fresh boot never rehydrates.
	gstore := goal.NewStore(config.OmnipusHomeDir())
	const evidence = "all delegated work appears complete"
	if _, err := gstore.Update(ownerRec.GoalRef, func(cur *goal.Goal) error {
		cur.RouteChannel = "webchat"
		cur.RouteChatID = "gw-q2b-owner-chat"
		return cur.RecordClaim(generated.GoalLatestClaimStatusMet, evidence, time.Now().UTC())
	}); err != nil {
		t.Fatalf("record met claim on owner's goal: %v", err)
	}

	stg := &setupAndStartServicesState{
		ctx:             ctx,
		agentLoop:       al,
		lifecycleStore:  lifecycle,
		runningServices: &services{},
	}
	SetGatewaySteerAudienceDeps(nil, nil)
	t.Cleanup(func() { SetGatewaySteerAudienceDeps(nil, nil) })
	stg.wireSteerDeps()

	if stg.runningServices.SteerDeps.BootHook == nil {
		t.Fatal("wireSteerDeps left BootHook nil")
	}
	if err := stg.runningServices.SteerDeps.BootHook(ctx); err != nil {
		t.Fatalf("BootHook: %v", err)
	}

	// Nothing consumes msgBus.InboundChan() in this test (AgentLoop.Run was
	// never started), so every publish the boot sweep makes lands here. The
	// descendant's OWN ordinary repair/replay (recoverSteered's ADR-091
	// terminal-message delivery to its owner) also publishes a message —
	// harmless and expected — so this drains for the SPECIFIC one the
	// goal-completion re-evaluation dispatch sends (sender
	// "system:goal_loop", goalLoopFollowUpSenderID in goal_loop.go) rather
	// than assuming the first message on the channel is it. Absent the
	// DescendantTerminal wiring, ResumeDeferredGoalAfterDescendantTerminal
	// is never invoked for the descendant and this message never arrives —
	// the drain exhausts its deadline instead of finding it.
	const reevaluationSender = "system:goal_loop"
	deadline := time.After(10 * time.Second)
	var found bus.InboundMessage
	var ok bool
drain:
	for {
		select {
		case msg := <-msgBus.InboundChan():
			if msg.Sender.CanonicalID == reevaluationSender {
				found, ok = msg, true
				break drain
			}
		case <-deadline:
			break drain
		}
	}
	if !ok {
		t.Fatal("BUG: no goal re-evaluation was dispatched to the owner after the descendant's boot catch-up — DescendantTerminal was not wired through to SteerBootRecovery")
	}
	if found.AsyncTranscriptSessionID != ownerRes.SessionID {
		t.Fatalf("re-evaluation dispatch targeted session %q, want the owner %q", found.AsyncTranscriptSessionID, ownerRes.SessionID)
	}
	if found.Channel != "system" {
		t.Fatalf("re-evaluation dispatch published on channel %q, want %q", found.Channel, "system")
	}

	after, err := gstore.Get(ownerRec.GoalRef)
	if err != nil {
		t.Fatalf("Get(owner goal after boot): %v", err)
	}
	if after.State != generated.GoalStateActive {
		t.Fatalf("owner goal state after the boot catch-up = %q, want active (the owner must survive boot untouched)", after.State)
	}
}
