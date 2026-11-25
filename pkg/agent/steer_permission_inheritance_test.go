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
//
// C-26 (Skills & Tools redesign, founder R2-Q1, 2026-11-22) retires the GRANT
// half of this hand-over. An allowance is keyed by the agent alone, so a
// self-helper looks it up with no copy at spawn and a delegation to another
// agent inherits nothing (rows Key and Covers; code-path row
// ApprovalGrantStore.InheritFrom, "copies no grants"). The per-chat
// Auto-approve modifier half is unchanged (the first two tests below). The
// grant tests at the end of this file were migrated accordingly; each one's
// own comment names its old test and the row that ordered the change.

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
// to a delegate target agent "worker" (each test sets only what it needs on
// top). Global Auto-approve is set to
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
	// D2 admission requires the genuine current boot epoch before Dispatch.
	mintGenuineBootEpochForLoop(t, al)
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

// TestSteerLauncher_PermissionInheritance_StandingGrantNotCopiedToAnotherAgent
// replaces TestSteerLauncher_PermissionInheritance_StandingGrantHonoured.
//
// FLIPPED by C-26 (row Covers): the old test expected a parent chat's standing
// grant for mia to be honoured by a delegate of ANOTHER agent ("worker") with
// no second prompt, because SteerLauncher.inheritDelegatePermissions copied it
// through ApprovalGrantStore.InheritFrom. C-26 deletes that copy. Row Covers:
// "A helper of another agent, or a delegation to another agent, is not covered
// and asks as usual"; code-path row ApprovalGrantStore.InheritFrom: "copies no
// grants (self-helpers share the key; other agents get nothing)"; security
// point 5: "a delegation to another agent no longer inherits the delegating
// chat's grants"; test row 60: "a delegation to another agent asks". A correct
// GREEN fails the old test.
//
// KEPT: the real Launch+Dispatch wiring; global Auto off and no per-chat
// modifier, so only the allowance question can decide whether the delegate
// prompts; the same tool and the same empty arguments; and the instrument that
// the allowance really exists for the parent's own agent, so a prompt below can
// never be explained by a missing record. The scripted approver denies, so the
// tool must not run behind an answer nobody gave. The old "delegate honours a
// standing grant" check lives on in its only C-26-valid form, the next test.
func TestSteerLauncher_PermissionInheritance_StandingGrantNotCopiedToAnotherAgent(t *testing.T) {
	al, launcher, parentSessionID, approver, stub := steerInheritanceFixture(t, false) // global Auto OFF, plain Ask everywhere
	args := map[string]any{}

	require.True(t, al.ApprovalGrants().Record(parentSessionID, testDefaultAgentID, "knowledge_edit", args),
		"recording the parent agent's standing allowance")
	require.True(t, al.ApprovalGrants().IsAllowed(parentSessionID, testDefaultAgentID, "knowledge_edit", args),
		"instrument: the parent's own agent holds the allowance")

	launchWorkerDelegate(t, al, launcher, parentSessionID, approver, stub, "call-grant")

	require.Equal(t, 1, approver.countFor("knowledge_edit"),
		"C-26 Covers: a delegation to ANOTHER agent is not covered by the parent agent's allowance and asks as usual")
	require.Zero(t, stub.calls.Load(), "the tool must not run before the (denied) prompt resolves")
}

// TestSteerLauncher_PermissionInheritance_SelfDelegateSharesTheAgentAllowance
// is the positive half of C-26's Covers row ("helpers the agent starts for
// itself are covered", test row 60: "a self-helper shares the allowance ... no
// grant copy at spawn"), driven through the real Launch+Dispatch path. The
// delegating chat belongs to "worker" and so does the delegate, so the
// delegation is a self-helper, and the scripted approver would deny any prompt.
//
// It carries what the old StandingGrantHonoured test guarded (a delegate
// honours a standing allowance, with no second prompt and the tool running once)
// in the only form C-26 still allows, and adds the case the old copy could not
// serve.
//
//   - "recorded in the delegating chat": a control. Today's InheritFrom copy and
//     C-26's shared key both settle it, so it passes before and after.
//   - "recorded in another chat of the same agent": C-26 row Key ("The session id
//     is no longer part of the key") and row Covers ("every session of that
//     agent"). No spawn copy can reach an allowance recorded in a chat that is
//     not the delegating one, so today's delegate asks; C-26's delegate does not.
func TestSteerLauncher_PermissionInheritance_SelfDelegateSharesTheAgentAllowance(t *testing.T) {
	for _, tc := range []struct {
		name           string
		recordInParent bool
		why            string
	}{
		{
			name:           "recorded in the delegating chat",
			recordInParent: true,
			why:            "a self-helper must honour the allowance its own agent already holds, with no prompt",
		},
		{
			name:           "recorded in another chat of the same agent",
			recordInParent: false,
			why: "C-26 Key and Covers: the allowance belongs to the agent, so a self-helper holds it " +
				"although it was recorded in a chat that is not the delegating one and nothing was copied",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			al, launcher, _, approver, stub := steerInheritanceFixture(t, false) // global Auto OFF, plain Ask everywhere
			args := map[string]any{}

			// The delegating chat is owned by "worker", which is also the delegation's
			// target: a self-helper.
			delegatingChat := newTestSteeringSessionOwnedBy(t, al, "", "worker")
			otherWorkerChat := newTestSteeringSessionOwnedBy(t, al, "", "worker")
			recordedIn := otherWorkerChat
			if tc.recordInParent {
				recordedIn = delegatingChat
			}
			require.NotEqual(t, delegatingChat, otherWorkerChat, "fixture defect: the two worker chats must be distinct")

			require.True(t, al.ApprovalGrants().Record(recordedIn, "worker", "knowledge_edit", args),
				"recording the worker agent's standing allowance")
			require.True(t, al.ApprovalGrants().IsAllowed(recordedIn, "worker", "knowledge_edit", args),
				"instrument: the store sees the record before any delegation")

			launchWorkerDelegate(t, al, launcher, delegatingChat, approver, stub, "call-self")

			require.Zero(t, approver.countFor("knowledge_edit"), tc.why)
			require.Equal(t, int32(1), stub.calls.Load(), "the tool must run once the agent's allowance settles the call")
		})
	}
}
