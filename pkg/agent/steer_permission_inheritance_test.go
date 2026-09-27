// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// steer_permission_inheritance_test.go: functional proof for Q13 A
// (2026-09-24) — reconnecting ADR-092's per-chat permission hand-over
// (FR-005/FR-058) to the real delegation launch path,
// steer_launcher.go::SteerLauncher.inheritDelegatePermissions.
//
// Before that wiring existed, AgentLoop.inheritSessionPermissions
// (loop_policy.go) and ApprovalGrantStore.InheritFrom (pkg/security/
// approvalgrants.go) were both correct and both unit-tested in isolation
// (inherit_session_permissions_test.go, approvalgrants_adr092_test.go) but
// had ZERO production callers — subturn.go, the pre-ADR-091 mechanism that
// used to call inheritSessionPermissions, was deleted and nothing in the
// new SteerLauncher.Launch/Dispatch path replaced the call. Every test here
// drives a REAL delegated child through Launch+Dispatch — never calling
// inheritSessionPermissions by hand — so a regression that breaks only the
// WIRING (reintroducing exactly this defect) fails these tests even though
// it would leave the isolated unit tests green.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// steerInheritanceFixture builds a real AgentLoop wired for delegation
// (SteerLauncher.Launch/Dispatch) from a parent chat run by testDefaultAgentID
// to a delegate target agent "worker" (no AutoApproveDisabled of its own —
// each test sets only what it needs on top). Global Auto-approve is set to
// globalAutoApprove. "worker" gets a single RUNS-classified stub tool,
// knowledge_edit (ADR-092 D9's J11: "Knowledge-writing tools Run"), on Ask,
// scripted to be called exactly once.
func steerInheritanceFixture(t *testing.T, globalAutoApprove bool) (al *AgentLoop, launcher *SteerLauncher, parentSessionID string, approver *autoRecordingApprover, stub *autoStubTool) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	provider := testutil.NewScenario().WithToolCalls([]providers.ToolCall{
		autoToolCall("inherit-ke", "knowledge_edit", `{}`),
	}).WithText("done")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: testDefaultAgentID, Home: home},
				{ID: "worker", Home: home},
			},
		},
	}
	cfg.Sandbox.AutoApprove = globalAutoApprove
	al = mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(func() { al.Close() })
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
	al.SetSessionMessagingStores(inbox, lifecycle)

	stubs := installAutoStubs(t, al, "worker", []string{"knowledge_edit"})
	stub = stubs["knowledge_edit"]

	approver = &autoRecordingApprover{approve: false}
	al.SetToolApprover(approver)

	parentSessionID = newTestSteeringSession(t, al, "")
	launcher = NewSteerLauncher(al)
	return al, launcher, parentSessionID, approver, stub
}

// launchWorkerDelegate launches and dispatches a real delegation to "worker"
// from parentSessionID through the production SteerLauncher, waits for its
// one scripted knowledge_edit call to resolve (a prompt or a run), and waits
// for the child's turn to finish.
func launchWorkerDelegate(
	t *testing.T, al *AgentLoop, launcher *SteerLauncher, parentSessionID string,
	approver *autoRecordingApprover, stub *autoStubTool, callID string,
) string {
	t.Helper()
	launch, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentSessionID,
		TargetAgentID:     "worker",
		Task:              "edit the knowledge base",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	require.NoError(t, err, "Launch(worker delegate)")
	if _, dispatchErr := launcher.Dispatch(context.Background(), launch.SessionID, launch.Generation); dispatchErr != nil {
		t.Fatalf("Dispatch(worker delegate): %v", dispatchErr)
	}
	waitFor(t, 10*time.Second, func() bool {
		return stub.calls.Load() > 0 || approver.countFor("knowledge_edit") > 0
	})
	if ts := al.getActiveTurnState(launch.SessionID); ts != nil {
		select {
		case <-ts.Finished():
		case <-time.After(10 * time.Second):
			t.Fatal("the worker delegate's turn did not finish within 10s of its tool call resolving")
		}
	}
	return launch.SessionID
}

// TestSteerLauncher_PermissionInheritance_TheHole is the exact scenario
// named in loop.go's approvalGrants doc comment as the consequence of the
// missing wiring: global Auto-approve is ON, but the PARENT chat turned
// Auto OFF for itself. Before SteerLauncher.inheritDelegatePermissions
// existed, a delegate never saw the parent's per-chat modifier at all and
// fell straight through to the global default (Auto ON), running the
// worker's RUNS tool unprompted — looser than the human's own per-chat
// choice. With the hand-over reconnected, the child inherits the parent's
// OFF modifier and prompts.
func TestSteerLauncher_PermissionInheritance_TheHole(t *testing.T) {
	al, launcher, parentSessionID, approver, stub := steerInheritanceFixture(t, true) // global Auto ON
	al.SessionModes().Set(parentSessionID, false)                                     // this chat turned Auto OFF for itself

	launchWorkerDelegate(t, al, launcher, parentSessionID, approver, stub, "call-hole")

	require.Equal(t, 1, approver.countFor("knowledge_edit"),
		"the parent chat's Auto-OFF modifier must inherit to the delegate and force a prompt, "+
			"even though the global default is Auto ON — this is the hole named in loop.go's own doc comment")
	require.Zero(t, stub.calls.Load(), "the tool must not run before the (denied) prompt resolves")
}

// TestSteerLauncher_PermissionInheritance_ParentChatOnRunsUnprompted is the
// positive control symmetric to the hole above: global Auto is OFF, but the
// PARENT chat turned Auto ON for itself. The delegate must inherit that ON
// modifier and run its RUNS tool with no prompt — proving the hand-over
// loosens, not only tightens, exactly as FR-005 ("delegate resolves the
// tightest of (parent modifier, own override)") requires when the delegate
// carries no override of its own.
func TestSteerLauncher_PermissionInheritance_ParentChatOnRunsUnprompted(t *testing.T) {
	al, launcher, parentSessionID, approver, stub := steerInheritanceFixture(t, false) // global Auto OFF
	al.SessionModes().Set(parentSessionID, true)                                       // this chat turned Auto ON for itself

	launchWorkerDelegate(t, al, launcher, parentSessionID, approver, stub, "call-loosen")

	require.Zero(t, approver.countFor("knowledge_edit"),
		"the parent chat's Auto-ON modifier must inherit to the delegate and skip the prompt")
	require.Equal(t, int32(1), stub.calls.Load(), "the RUNS tool must run under the inherited Auto-ON modifier")
}

// TestSteerLauncher_PermissionInheritance_StandingGrantHonoured proves the
// other half of the hand-over: ApprovalGrantStore.InheritFrom, not only the
// SessionModeStore modifier. A parent's earlier "Always Allow" grant for
// this EXACT call — recorded, in production, by the gateway's tool-approval
// REST handler (rest_tool_registry.go's approvalGrantRecorder) after a human
// clicks Always Allow — must be honoured by a delegate spawned afterward,
// with no second prompt. Global Auto stays off throughout and no per-chat
// modifier is set, so only the standing grant can be responsible for the
// delegate's call running unprompted.
func TestSteerLauncher_PermissionInheritance_StandingGrantHonoured(t *testing.T) {
	al, launcher, parentSessionID, approver, stub := steerInheritanceFixture(t, false) // global Auto OFF, plain Ask everywhere

	require.True(t, al.ApprovalGrants().Record(parentSessionID, testDefaultAgentID, "knowledge_edit", map[string]any{}),
		"recording the parent's Always Allow grant under its own session+agent key")

	launchWorkerDelegate(t, al, launcher, parentSessionID, approver, stub, "call-grant")

	require.Zero(t, approver.countFor("knowledge_edit"),
		"the delegate must inherit the parent's standing grant and skip the prompt")
	require.Equal(t, int32(1), stub.calls.Load(), "the tool must run once the inherited grant settles the call")
}
