// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// UAT root-cause reproduction (2026-09-24, Fly UAT tester t5, session
// session_01M390ZHHGPP4M3J6MSKJRM003): a bash command that needs an Auto
// "outside the workspace" escalation never showed an approval card in the
// chat — the tester saw the tool card stuck on "Running…" until they gave up
// and pressed Stop (server audit: shell.preflight_escalation at 06:18:44Z,
// then shell.approval_decision deny "session canceled" at 06:19:54Z, ~70s
// later — the file was never created).
//
// Root cause: every ADR-092 D3/D7/D8 call site added in this file
// (requestRuleApproval, requestPreflightApproval) calls
// ShellApprovalRequester.RequestShellApproval with a LITERAL empty string for
// turnID — not a value read from ctx, because pkg/tools has no such
// accessor (there is no ToolTurnID/WithTurnID in base.go, unlike
// ToolCallID/WithToolCallID) and pkg/agent's dispatch path
// (loop_run_turn_tools.go's executeTool, which builds execCtx) never stamps
// one either. The empty turnID flows unchanged through
// ShellPermissionGate.RequestShellApproval (pkg/agent/loop_policy.go) into
// AgentLoop.CheckGrantOrRequestApproval, into
// policyApproverAdapter.RequestApproval (pkg/gateway/policy_approver.go),
// into approvalRegistryV2.requestApproval, and is stamped onto the resulting
// approvalEntry.TurnID unchanged — there is no fail-closed guard for an empty
// TurnID there (unlike the one that exists for an empty actingSessionID).
// broadcastToolApprovalRequired (pkg/gateway/ws_tool_approval.go) then
// marshals it straight into the wire frame's turn_id field.
//
// contracts/components/schemas/ToolApprovalRequiredFrame.yaml requires
// turn_id at minLength 1 (required, non-empty) — the SPA's generated Zod
// schema (src/lib/api/generated/schemas.ts::ToolApprovalRequiredFrame) encodes
// this as z.string().min(1). src/lib/ws.ts's parseFrameSafe/_parseServerFrame
// validates every inbound frame against that schema and silently DROPS any
// frame that fails it (_recordDropped, never reaches
// useToolApprovalStore.enqueue). A tool_approval_required frame with
// turn_id:"" therefore never reaches the approval queue, so
// ToolApprovalModal.tsx never renders a card — the bash tool call is left
// showing only its "Running…" ToolCallStart state, exactly as tester t5's
// screenshots show, until the operator gives up and cancels the turn.
//
// This is the classic ask-policy path's OWN call site
// (loop_run_turn_tools.go, ts.turnID passed directly, never through this
// package) that supplies a real, non-empty turnID — which is why tester t3's
// tool-level "ask" policy approval card rendered normally in the very same
// minute.
//
// The three tests below prove the defect is in THIS package
// (pkg/tools/shell_permission_mode.go), at its three new
// ShellApprovalRequester.RequestShellApproval call sites, and that the D3
// ask-rule, D7 filesystem pre-flight, and D8 network pre-flight escalations
// all share the exact same bug — they are, in fact, the exact same function
// call (requestPreflightApproval is the single call site for both D7 and
// D8; requestRuleApproval is D3's).
package tools

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/shellrule"
)

// turnIDCapturingApprovalRequester stands in for pkg/agent's
// ShellPermissionGate.RequestShellApproval, recording the turnID argument
// exactly as this package supplied it — nothing more. Deliberately separate
// from fakeShellApprovalRequester (which discards every string argument):
// this fake exists solely to make the turnID this package passes visible to
// an assertion, without touching the shared fixture other tests in this
// package rely on.
type turnIDCapturingApprovalRequester struct {
	approve    bool
	calls      int
	lastTurnID string
}

func (f *turnIDCapturingApprovalRequester) RequestShellApproval(
	_ context.Context, _, _, _, _, turnID string, _ map[string]any,
) (bool, string, bool) {
	f.calls++
	f.lastTurnID = turnID
	if f.approve {
		return true, "", true
	}
	return false, "denied by test fixture", false
}

// turnIDAssertionMsg documents, in the failing test's own output, exactly
// what a non-empty turnID is FOR — so a reader of a failed `go test -v` run
// sees the causal chain without needing this file's own header comment.
const turnIDAssertionMsg = "ADR-092 escalation must carry the calling turn's non-empty turn_id: " +
	"contracts/components/schemas/ToolApprovalRequiredFrame.yaml requires turn_id at minLength 1, " +
	"the SPA's generated Zod schema enforces that at the WS boundary (src/lib/ws.ts parseFrameSafe), " +
	"and a frame that fails validation is silently dropped before it ever reaches " +
	"useToolApprovalStore.enqueue — so an empty turn_id here means the approval card never renders " +
	"and the bash call hangs until the user cancels (reproduces UAT tester t5, session " +
	"session_01M390ZHHGPP4M3J6MSKJRM003, 2026-09-24)"

// TestEnforceShellPermissionMode_D7FilesystemPreflightCarriesNonEmptyTurnID
// reproduces the UAT defect directly: a write outside the work dir under
// Auto mode (t5's `touch /etc/t5-probe`-shaped command) must escalate with a
// non-empty turnID. It currently does not — requestPreflightApproval
// (shell_permission_mode.go) hardcodes turnID as the literal "" in its call
// to RequestShellApproval.
func TestEnforceShellPermissionMode_D7FilesystemPreflightCarriesNonEmptyTurnID(t *testing.T) {
	tool, ctx, _, _ := permTestFixture(t, ShellModeAuto, true)
	capture := &turnIDCapturingApprovalRequester{approve: true}
	tool.approvalRequester = capture

	outsideDir, err := os.MkdirTemp("", "adr092-turnid-fs-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(outsideDir) })
	cmd := "touch " + outsideDir + "/t5-probe"

	perm, result := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, result, "an APPROVED escalation must not refuse the command")
	require.NotNil(t, perm)
	require.Equal(t, 1, capture.calls, "a write outside the work dir must escalate exactly once")
	assert.NotEmpty(t, capture.lastTurnID, turnIDAssertionMsg)
}

// TestEnforceShellPermissionMode_D8NetworkPreflightCarriesNonEmptyTurnID is
// the D8 counterpart: enforceNetworkPreflight also calls
// requestPreflightApproval — the SAME function the D7 test above exercises,
// with the SAME hardcoded "" — proving the fs and network pre-flights share
// one call site and one bug, not two independent defects.
func TestEnforceShellPermissionMode_D8NetworkPreflightCarriesNonEmptyTurnID(t *testing.T) {
	tool, ctx, _, _ := permTestFixture(t, ShellModeAuto, true)
	capture := &turnIDCapturingApprovalRequester{approve: true}
	tool.approvalRequester = capture

	perm, result := tool.enforceShellPermissionMode(ctx, "curl https://example.com")
	require.Nil(t, result)
	require.NotNil(t, perm)
	require.Equal(t, 1, capture.calls)
	assert.NotEmpty(t, capture.lastTurnID, turnIDAssertionMsg)
}

// TestEnforceShellPermissionMode_D3AskRuleCarriesNonEmptyTurnID is the third
// ADR-092 FR-039 call site (requestRuleApproval) — an operator {action: ask}
// command_rule under Auto mode. Same bug, same hardcoded "" (a separate
// literal at a separate call site inside this file, but the identical
// defect).
func TestEnforceShellPermissionMode_D3AskRuleCarriesNonEmptyTurnID(t *testing.T) {
	tool, ctx, _, _ := permTestFixture(t, ShellModeAuto, true)
	capture := &turnIDCapturingApprovalRequester{approve: true}
	tool.approvalRequester = capture

	resolvedTrue, err := shellrule.ResolveBinary("true", os.Getenv("PATH"))
	require.NoError(t, err)
	tool.commandRules = []shellrule.Rule{{Action: shellrule.ActionAsk, Binary: resolvedTrue}}

	perm, result := tool.enforceShellPermissionMode(ctx, "true")
	require.Nil(t, result, "an APPROVED D3 ask rule must not refuse the command")
	require.NotNil(t, perm)
	require.Equal(t, 1, capture.calls)
	assert.NotEmpty(t, capture.lastTurnID, turnIDAssertionMsg)
}
