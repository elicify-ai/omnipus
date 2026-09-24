// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Functional proof for ADR-092's D1/D3/D7/D8 enforcement wired in this
// package (lane L4). RegisterTurnPolicyBase is process-global (see
// environment_setup_lifecycle_kernel_test.go's own note), so these tests do
// not run in parallel with each other.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
)

// fakeShellModeResolver always resolves to a fixed mode — the test stands in
// for pkg/agent's ShellPermissionGate, which is exercised separately in
// pkg/agent (loop_policy_shell_gate_test.go).
type fakeShellModeResolver struct{ mode ShellMode }

func (f fakeShellModeResolver) ResolveShellMode(context.Context, string, string) ShellMode {
	return f.mode
}

// fakeShellApprovalRequester stands in for pkg/agent's
// ShellPermissionGate.RequestShellApproval — records every call so a test
// can assert exactly how many times (if any) the interactive dialog would
// have fired, and returns a fixed, configurable decision.
type fakeShellApprovalRequester struct {
	mu sync.Mutex
	// approve is what the human decided: approved at all, or not.
	// recordGrant (review finding #5) is a SEPARATE axis: "Allow" (true)
	// vs "Allow once" (false) — permTestFixture defaults it to approve's
	// own value so every pre-existing test (which never distinguished the
	// two) keeps its old behaviour; a finding #5-specific test overrides it
	// directly to simulate "Allow once".
	approve     bool
	recordGrant bool
	calls       int
	lastArg     map[string]any
}

func (f *fakeShellApprovalRequester) RequestShellApproval(
	_ context.Context, _, _, _, _, _ string, args map[string]any,
) (bool, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastArg = args
	if f.approve {
		return true, "", f.recordGrant
	}
	return false, "denied by test fixture", false
}

func (f *fakeShellApprovalRequester) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// permTestFixture builds an ExecTool with the ADR-092 deps wired to fakes,
// a real turn-policy base registered (so resolveShellMode's FR-008
// kernel-sandbox predicate is satisfied and Auto does not degrade to Ask),
// and a context carrying the agent/session identity enforceShellPermissionMode
// reads.
func permTestFixture(t *testing.T, mode ShellMode, approve bool) (tool *ExecTool, ctx context.Context, requester *fakeShellApprovalRequester, workDir string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Setenv("OMNIPUS_HOME", home)
	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: home, Model: sandbox.FilesystemModelOpen})
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })

	workDir, err = filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	tool, err = NewExecTool(workDir, true)
	require.NoError(t, err)
	tool.shellMode = fakeShellModeResolver{mode: mode}
	requester = &fakeShellApprovalRequester{approve: approve, recordGrant: approve}
	tool.approvalRequester = requester
	tool.approvalGrants = security.NewApprovalGrantStore()

	ctx = WithTranscriptSessionID(WithAgentID(context.Background(), "agent-1"), "session-1")
	// D-03 fix (2026-09-24): mirror pkg/agent/loop_run_turn_tools.go's
	// executeTool, which stamps WithTurnID onto every tool's execCtx
	// immediately alongside WithToolCallID — this fixture stands in for
	// that dispatch path, so it must carry the same fact real production
	// tool execution now does, or the D3/D7/D8 turnID assertions below
	// would fail against a ctx no real call site would ever produce.
	ctx = WithTurnID(ctx, "turn-1")
	return tool, ctx, requester, workDir
}

// --- DoD 1: a command cleared by the pre-flight runs without a prompt under Auto ---

func TestEnforceShellPermissionMode_AutoContainedCommandRunsWithoutPrompt(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)

	perm, result := tool.enforceShellPermissionMode(ctx, "echo hello")
	require.Nil(t, result, "a fully-contained command must not be refused")
	require.NotNil(t, perm)
	assert.Equal(t, ShellModeAuto, perm.mode)
	assert.Equal(t, 0, requester.callCount(), "a command touching nothing outside the work dir must never prompt")
}

// --- DoD 2: a command that leaves the sandbox prompts ---

func TestEnforceShellPermissionMode_AutoEscalatesOutsideWorkDir(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)

	outsideDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	outsideFile := filepath.Join(outsideDir, "out.txt")
	cmd := "echo hi > " + outsideFile

	perm, result := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, result, "an APPROVED escalation must not refuse the command")
	require.NotNil(t, perm)
	assert.Equal(t, 1, requester.callCount(), "a write outside the work dir must escalate exactly once")
	require.Len(t, perm.pathGrants, 1)
	assert.Equal(t, outsideFile, perm.pathGrants[0].Path)
	assert.Equal(t, fspolicy.PathGrantAccessWrite, perm.pathGrants[0].Access)
}

// --- DoD 2b: denial refuses outright (FR-048), never runs un-widened ---

func TestEnforceShellPermissionMode_DeniedEscalationRefusesOutright(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, false)

	outsideDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	cmd := "echo hi > " + filepath.Join(outsideDir, "out.txt")

	perm, result := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, perm, "a denied escalation must not return a usable permission result")
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "not approved")
	assert.Equal(t, 1, requester.callCount())
}

// --- DoD 3: an approval widens the path and the next command reuses it ---

func TestEnforceShellPermissionMode_ApprovalWidensAndSecondCommandReuses(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)

	outsideDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	outsideFile := filepath.Join(outsideDir, "out.txt")
	cmd := "echo hi > " + outsideFile

	_, result := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, result)
	require.Equal(t, 1, requester.callCount())

	// A second command writing the SAME already-granted path must not
	// re-prompt — "the next identical command in the session resolves the
	// widened policy directly" (ADR-092 D7).
	perm2, result2 := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, result2)
	require.NotNil(t, perm2)
	assert.Equal(t, 1, requester.callCount(), "the second command must reuse the recorded grant, not re-prompt")
	require.Len(t, perm2.pathGrants, 1)
}

// --- DoD 4: network denied by default and widened on grant ---

func TestEnforceShellPermissionMode_NetworkDeniedByDefaultThenWidened(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)

	perm, result := tool.enforceShellPermissionMode(ctx, "curl https://example.com")
	require.Nil(t, result)
	require.NotNil(t, perm)
	assert.True(t, perm.networkGranted, "an approved network escalation must widen this call's rendering")
	assert.Equal(t, 1, requester.callCount())

	// Reuse: the same session's next network-flagged command does not re-prompt.
	perm2, result2 := tool.enforceShellPermissionMode(ctx, "curl https://example.com/other")
	require.Nil(t, result2)
	assert.True(t, perm2.networkGranted)
	assert.Equal(t, 1, requester.callCount(), "the D8 grant must be reused, not re-requested")
}

func TestEnforceShellPermissionMode_NetworkDenialLeavesRenderingEmpty(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, false)

	perm, result := tool.enforceShellPermissionMode(ctx, "curl https://example.com")
	require.Nil(t, perm)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Equal(t, 1, requester.callCount())
}

// --- 2026-09-24 founder decision: Auto's D7/D8 pre-flights still govern with
// NO kernel sandbox enforcing — the whole point of the change (Auto no
// longer requires an enforcing kernel sandbox) is that these checks become
// the ONLY thing standing between the agent and the host filesystem/network
// in that case, so they must still fire exactly as they do with a sandbox. ---

// permTestFixtureNoSandbox is permTestFixture with NO kernel policy base
// registered — the no-sandbox counterpart (Windows, a sandbox that failed to
// start, permissive mode).
func permTestFixtureNoSandbox(t *testing.T, mode ShellMode, approve bool) (tool *ExecTool, ctx context.Context, requester *fakeShellApprovalRequester, workDir string) {
	t.Helper()
	sandbox.RegisterTurnPolicyBase(nil)
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })
	require.False(t, sandbox.TurnPolicyBaseInstalled(), "setup: no kernel policy base must be registered")

	workDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	tool, err = NewExecTool(workDir, true)
	require.NoError(t, err)
	tool.shellMode = fakeShellModeResolver{mode: mode}
	requester = &fakeShellApprovalRequester{approve: approve, recordGrant: approve}
	tool.approvalRequester = requester
	tool.approvalGrants = security.NewApprovalGrantStore()

	ctx = WithTranscriptSessionID(WithAgentID(context.Background(), "agent-1"), "session-1")
	// D-03 fix: see permTestFixture's identical comment above.
	ctx = WithTurnID(ctx, "turn-1")
	return tool, ctx, requester, workDir
}

// TestEnforceShellPermissionMode_NoKernelSandbox_ContainedCommandRunsWithoutPrompt
// mirrors TestEnforceShellPermissionMode_AutoContainedCommandRunsWithoutPrompt
// with no kernel sandbox: `echo hello` touches nothing outside the work dir,
// so it must still auto-run with zero approver calls.
func TestEnforceShellPermissionMode_NoKernelSandbox_ContainedCommandRunsWithoutPrompt(t *testing.T) {
	tool, ctx, requester, _ := permTestFixtureNoSandbox(t, ShellModeAuto, true)

	perm, result := tool.enforceShellPermissionMode(ctx, "echo hello")
	require.Nil(t, result, "a fully-contained command must not be refused, sandbox or no sandbox")
	require.NotNil(t, perm)
	assert.Equal(t, ShellModeAuto, perm.mode, "mode must stay Auto with no kernel sandbox enforcing")
	assert.Equal(t, 0, requester.callCount(), "a command touching nothing outside the work dir must never prompt")
}

// TestEnforceShellPermissionMode_NoKernelSandbox_EscalatesOutsideWorkDir is
// the D7 filesystem pre-flight proof with no kernel sandbox: a write outside
// the work dir (e.g. bash `touch /etc/x`-shaped) must still escalate to a
// prompt — the pre-flight is the ONLY check here now, so it must not go
// silent just because the kernel isn't confining the child.
func TestEnforceShellPermissionMode_NoKernelSandbox_EscalatesOutsideWorkDir(t *testing.T) {
	tool, ctx, requester, _ := permTestFixtureNoSandbox(t, ShellModeAuto, true)

	outsideDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	outsideFile := filepath.Join(outsideDir, "out.txt")
	cmd := "echo hi > " + outsideFile

	perm, result := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, result, "an APPROVED escalation must not refuse the command")
	require.NotNil(t, perm)
	assert.Equal(t, 1, requester.callCount(), "a write outside the work dir must escalate exactly once even with no kernel sandbox")
	require.Len(t, perm.pathGrants, 1)
	assert.Equal(t, outsideFile, perm.pathGrants[0].Path)
}

// TestEnforceShellPermissionMode_NoKernelSandbox_DeniedEscalationRefusesOutright
// is FR-048 with no kernel sandbox: a denied D7 escalation must still refuse
// the command outright — with no kernel to fall back on, this refusal is the
// only thing stopping the write.
func TestEnforceShellPermissionMode_NoKernelSandbox_DeniedEscalationRefusesOutright(t *testing.T) {
	tool, ctx, requester, _ := permTestFixtureNoSandbox(t, ShellModeAuto, false)

	outsideDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	cmd := "echo hi > " + filepath.Join(outsideDir, "out.txt")

	perm, result := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, perm, "a denied escalation must not return a usable permission result")
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "not approved")
	assert.Equal(t, 1, requester.callCount())
}

// TestEnforceShellPermissionMode_NoKernelSandbox_NetworkDeniedByDefaultThenWidened
// is the D8 network pre-flight proof with no kernel sandbox: outbound
// network still denies by default and widens only on an approved escalation.
func TestEnforceShellPermissionMode_NoKernelSandbox_NetworkDeniedByDefaultThenWidened(t *testing.T) {
	tool, ctx, requester, _ := permTestFixtureNoSandbox(t, ShellModeAuto, true)

	perm, result := tool.enforceShellPermissionMode(ctx, "curl https://example.com")
	require.Nil(t, result)
	require.NotNil(t, perm)
	assert.True(t, perm.networkGranted, "an approved network escalation must widen this call's rendering even with no kernel sandbox")
	assert.Equal(t, 1, requester.callCount())
}

// TestEnforceShellPermissionMode_NoKernelSandbox_BlindCommandEscalates proves
// FR-020's blind-spot posture (a command the classifier cannot parse) still
// escalates to a prompt with no kernel sandbox — the classifier's fail-closed
// default must not depend on kernel enforcement being present. An unbalanced
// quote defeats BOTH ClassifyPathOperations (D7's own blind spot) and
// ClassifyNetworkNeed's tokenizer (which also fails closed to "flagged" when
// it cannot tokenize a segment, preflight.go::ClassifyNetworkNeed), so this
// escalates through both pre-flight layers independently — two prompts, not
// a silent pass-through of either check.
func TestEnforceShellPermissionMode_NoKernelSandbox_BlindCommandEscalates(t *testing.T) {
	tool, ctx, requester, _ := permTestFixtureNoSandbox(t, ShellModeAuto, true)

	perm, result := tool.enforceShellPermissionMode(ctx, `echo "unbalanced`)
	require.Nil(t, result, "an approved blind-spot escalation must not refuse the command")
	require.NotNil(t, perm)
	assert.Equal(t, 2, requester.callCount(),
		"a command neither pre-flight classifier can parse must escalate through D7 AND D8, sandbox or no sandbox")
}

// applyAutoNetworkPosture is D8's kernel-rendering half — proves the
// mutation this file applies to an already-derived *sandbox.SandboxPolicy
// matches the "empty means deny, DefaultConnectPorts means widened" contract
// lane L3's own TurnPolicyInput.NetworkAutoDeny/NetworkGranted fields
// document (see this file's own doc comment on why the mutation happens
// here rather than through that seam).
func TestApplyAutoNetworkPosture(t *testing.T) {
	p := &sandbox.SandboxPolicy{
		BindPortRules:    []sandbox.NetPortRule{{Port: 5173}},
		ConnectPortRules: []sandbox.NetPortRule{{Port: 443}},
	}
	applyAutoNetworkPosture(p, false)
	assert.Empty(t, p.BindPortRules, "Auto denies bind ports regardless of the network grant")
	assert.Empty(t, p.ConnectPortRules, "Auto denies connect ports by default")

	applyAutoNetworkPosture(p, true)
	assert.Empty(t, p.BindPortRules, "a network grant never covers binding a local listener")
	require.Len(t, p.ConnectPortRules, len(sandbox.DefaultConnectPorts))
	for i, want := range sandbox.DefaultConnectPorts {
		assert.Equal(t, want, p.ConnectPortRules[i].Port)
	}
}

// --- DoD 5: a chained command where one segment is ungranted prompts ---

func TestEnforceShellPermissionMode_ChainedCommandUngrantedSegmentPrompts(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)

	outsideDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	outsideFile := filepath.Join(outsideDir, "out.txt")
	cmd := "echo ok && echo hi > " + outsideFile

	perm, result := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, result)
	require.NotNil(t, perm)
	assert.Equal(t, 1, requester.callCount(), "only the segment that actually needs it escalates")
	require.Len(t, perm.pathGrants, 1)
	assert.Equal(t, outsideFile, perm.pathGrants[0].Path)
}

// --- DoD 6: a look-alike binary is not auto-approved ---

// This exercises shellRuleOptions' own wiring (splitShellSegments,
// shellCommandHeadDetailed — the exact function VALUES this package injects
// into pkg/shellrule) with EXPLICIT, DIFFERING Child/Trusted PATH lists,
// proving the plumbing this lane owns correctly carries D3's resolve-and-
// verify look-alike defence end to end. shellRuleOptions() itself (used by
// evaluateCommandRules, ExecTool's own default) deliberately uses the SAME
// os.Getenv("PATH") for both — a documented simplification (see that
// function's own comment) that this test does not exercise, because doing
// so would require ExecTool to thread the real child exec env's PATH
// through to this point, which the current call ordering (before execEnv is
// composed) does not yet support.
func TestShellRuleOptions_LookAlikeBinaryDoesNotAutoApprove(t *testing.T) {
	trustedDir := t.TempDir()
	realBin := filepath.Join(trustedDir, "mytool")
	require.NoError(t, os.WriteFile(realBin, []byte("#!/bin/sh\necho real\n"), 0o755))
	realResolved, err := filepath.EvalSymlinks(realBin)
	require.NoError(t, err)

	childDir := t.TempDir()
	lookalike := filepath.Join(childDir, "mytool")
	require.NoError(t, os.WriteFile(lookalike, []byte("#!/bin/sh\necho evil\n"), 0o755))

	rules := []shellrule.Rule{{Action: shellrule.ActionAllow, Binary: realResolved}}
	opts := shellrule.Options{
		Platform:     shellrule.PlatformForGOOS(runtime.GOOS),
		Segmenter:    splitShellSegments,
		HeadResolver: shellCommandHeadDetailed,
		ChildPath:    childDir,
		TrustedPath:  trustedDir,
	}
	verdict := shellrule.EvaluateCommand("mytool --help", rules, opts)
	assert.NotEqual(t, shellrule.ActionAllow, verdict.Action,
		"a rule authored against the REAL binary must not auto-approve a same-named look-alike earlier on the child's own PATH")
}

// --- DoD 7: God Mode with an operator deny rule still refuses ---

func TestEnforceShellPermissionMode_GodModeDenyRuleStillRefuses(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeGod, true)

	resolvedTrue, err := shellrule.ResolveBinary("true", os.Getenv("PATH"))
	require.NoError(t, err)
	tool.commandRules = []shellrule.Rule{{Action: shellrule.ActionDeny, Binary: resolvedTrue}}

	perm, result := tool.enforceShellPermissionMode(ctx, "true")
	require.Nil(t, perm)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "operator rule")
	assert.Equal(t, 0, requester.callCount(), "God Mode never prompts — the deny rule refuses outright, with no dialog")
}

// God Mode's ONLY surviving D3 control is deny (D1: "Operator deny rules
// stay in force in God Mode... new behaviour"); an ASK verdict is a no-op
// there (no approvals exist in God Mode) and the command proceeds.
func TestEnforceShellPermissionMode_GodModeAskRuleIsANoOp(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeGod, true)

	resolvedTrue, err := shellrule.ResolveBinary("true", os.Getenv("PATH"))
	require.NoError(t, err)
	tool.commandRules = []shellrule.Rule{{Action: shellrule.ActionAsk, Binary: resolvedTrue}}

	perm, result := tool.enforceShellPermissionMode(ctx, "true")
	require.Nil(t, result, "an ask verdict must not refuse a command in God Mode")
	require.NotNil(t, perm)
	assert.Equal(t, ShellModeGod, perm.mode)
	assert.Equal(t, 0, requester.callCount(), "God Mode never shows a dialog")
}

// --- FR-050: God Mode's path-containment disposition is a hard deny, no
// prompt (guardCommand never receives a grant overlay outside Auto mode) ---

func TestEnforceShellPermissionMode_GodModePathWriteHardDeniedNoPrompt(t *testing.T) {
	tool, ctx, requester, workDir := permTestFixture(t, ShellModeGod, true)

	outsideDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	cmd := "echo hi > " + filepath.Join(outsideDir, "out.txt")

	perm, permResult := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, permResult, "D3/D7/D8 themselves must not refuse — God Mode never runs the D7 pre-flight")
	require.NotNil(t, perm)
	assert.Empty(t, perm.pathGrants, "God Mode must never accumulate a D7 grant overlay")

	guardMsg := tool.guardCommand(ctx, cmd, workDir, perm.grants())
	assert.NotEmpty(t, guardMsg, "guardCommand must hard-deny an outside-workdir write in God Mode, with no overlay to satisfy it")
	assert.Equal(t, 0, requester.callCount(), "God Mode never prompts — guardCommand's own hard deny is silent, not an escalation")
}

// --- Reachability proof (B-1): the D3 ask-rule verdict reaches the SAME
// grant-consultation call site an escalation does, under Auto (where the
// upstream tool-policy ceiling is "allow" and would otherwise never touch
// the grant store at all — the defect this lane's task names explicitly).

func TestEnforceShellPermissionMode_AutoD3AskRuleEscalates(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)

	resolvedTrue, err := shellrule.ResolveBinary("true", os.Getenv("PATH"))
	require.NoError(t, err)
	tool.commandRules = []shellrule.Rule{{Action: shellrule.ActionAsk, Binary: resolvedTrue}}

	perm, result := tool.enforceShellPermissionMode(ctx, "true")
	require.Nil(t, result, "an APPROVED D3 ask rule must not refuse the command")
	require.NotNil(t, perm)
	assert.Equal(t, 1, requester.callCount(), "a D3 ask rule under Auto must reach the approval requester — this is the B-1 fix")
}

// Ask mode: the D3 ask verdict is a no-op (the classic upstream ask-policy
// gate already required approval before Execute was ever called) — this
// lane's new escalation call site must not ALSO prompt a second time.
func TestEnforceShellPermissionMode_AskModeD3AskRuleStillPrompts(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAsk, true)

	resolvedTrue, err := shellrule.ResolveBinary("true", os.Getenv("PATH"))
	require.NoError(t, err)
	tool.commandRules = []shellrule.Rule{{Action: shellrule.ActionAsk, Binary: resolvedTrue}}

	perm, result := tool.enforceShellPermissionMode(ctx, "true")
	require.Nil(t, result)
	require.NotNil(t, perm)
	assert.Equal(t, ShellModeAsk, perm.mode)
	// Review finding #11 (2026-09-23 security fix lane): a D3 ask rule
	// must always produce a prompt in every mode except God Mode — this
	// call site can no longer assume "Ask mode's own upstream gate already
	// asked" and skip itself, because the SAME ShellModeAsk pin also
	// covers a bash tool policy resolved directly to "allow" (bypassing
	// the classic ask-policy gate entirely, which only runs when the
	// ceiling resolves to literally "ask"). A real classic-Ask-mode call
	// that already showed the generic upfront dialog will now show a
	// second, D3-specific one when a rule matches — an accepted, narrow
	// UX trade-off; the alternative (silently skipping this branch) is
	// exactly the finding #11 CRITICAL gap: an "allow"-ceiling bash policy
	// with a matching D3 ask rule got NO prompt anywhere at all.
	assert.Equal(t, 1, requester.callCount(), "a D3 ask rule must reach the approval requester in Ask mode too (finding #11)")
}

// --- Unwired dependencies fail closed, never open ---

func TestResolveShellMode_UnwiredResolverFailsClosedToAsk(t *testing.T) {
	tool, err := NewExecTool(t.TempDir(), true)
	require.NoError(t, err)
	got := tool.resolveShellMode(context.Background())
	assert.Equal(t, ShellModeAsk, got)
}

// TestResolveShellMode_AutoWithNoKernelSandboxStaysAuto [2026-09-24, founder
// decision] supersedes the old FR-008 fallback: Auto no longer requires an
// enforcing kernel sandbox. resolveShellMode must return exactly what the
// resolver said, whether or not sandbox.TurnPolicyBaseInstalled() is true —
// the D7/D8 pre-flights below it are what govern in the no-sandbox case now,
// not a silent downgrade to Ask.
func TestResolveShellMode_AutoWithNoKernelSandboxStaysAuto(t *testing.T) {
	sandbox.RegisterTurnPolicyBase(nil) // explicit: no kernel policy in force
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })
	tool, err := NewExecTool(t.TempDir(), true)
	require.NoError(t, err)
	tool.shellMode = fakeShellModeResolver{mode: ShellModeAuto}
	got := tool.resolveShellMode(context.Background())
	assert.Equal(t, ShellModeAuto, got,
		"Auto applies with no kernel sandbox enforcing — 2026-09-24 founder decision, ADR-092 D1/J13 revised")
}

// D-65: every guard refusal names what tripped it, not a bare "blocked".
// shellRuleDenialMessage carries that obligation for an ADR-092 D3 deny
// verdict — it must name the matched rule's binary and arg_prefix, not just
// report that something was denied.
func TestShellRuleDenialMessage_NamesTheMatchedRule(t *testing.T) {
	resolvedTrue, err := shellrule.ResolveBinary("true", os.Getenv("PATH"))
	require.NoError(t, err)
	rules := []shellrule.Rule{{Action: shellrule.ActionDeny, Binary: resolvedTrue, ArgPrefix: "extra"}}

	verdict := shellrule.EvaluateCommand("true extra args", rules, shellRuleOptions())
	require.Equal(t, shellrule.ActionDeny, verdict.Action, "setup: the rule must actually match")

	msg := shellRuleDenialMessage(verdict)
	assert.Contains(t, msg, resolvedTrue, "the message must name the matched rule's binary")
	assert.Contains(t, msg, "extra", "the message must name the matched rule's arg_prefix")
}
