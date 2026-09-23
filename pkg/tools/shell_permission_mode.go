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

// ShellModeResolver resolves the ADR-092 D1 three-level, tighten-only mode
// merge (global -> per-agent -> per-chat) governing one bash call. Injected
// via ExecToolDeps.ShellMode, exactly like ExecPolicyAuditor is injected for
// the (now-retired) exec allowlist — a dependency edge pkg/agent satisfies,
// never a direct import. Implemented by pkg/agent's ShellPermissionGate
// (loop_policy.go), which composes agent.GlobalShellMode /
// agent.AgentShellModeOverride / agent.SessionModeStore.Get through
// agent.ResolveEffectiveShellMode — the mode-resolution CONTRACT that
// function's own doc comment describes lane L4 as calling.
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
type ShellApprovalRequester interface {
	RequestShellApproval(ctx context.Context, sessionID, agentID, toolName, toolCallID, turnID string, args map[string]any) (approved bool, denialReason string)
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
	if t.approvalRequester == nil {
		t.emitApprovalDecision(ctx, sessionID, agentID, command, "rule_ask", false, "no_approver_configured", false)
		return false, "no_approver_configured"
	}
	args := map[string]any{
		"command":     command,
		"adr092_kind": "rule_ask",
	}
	approved, reason := t.approvalRequester.RequestShellApproval(ctx, sessionID, agentID, t.Name(), toolCallID, "", args)
	// willRecordGrant=false: an approved D3 ask-rule verdict records no
	// grant today (RecordPrefixGrant has no caller yet — ADR-092 lane L4's
	// own scope, see pkg/gateway/rest_tool_registry.go's "prefix-scoped
	// recording separately" note) — so this is always "allow-once" from
	// this call site's own point of view, even though a fresh D3 rule
	// evaluation may separately match an EXISTING prefix grant on a later
	// call (requestRuleApproval's allCovered branch above, which never
	// reaches this line).
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

// requestPreflightApproval is FR-039's third new CheckGrantOrRequestApproval
// call site: a D7 or D8 pre-flight verdict needs more than the sandbox's
// current grant already covers.
func (t *ExecTool) requestPreflightApproval(ctx context.Context, sessionID, agentID, toolCallID, command, kind, note string) (bool, string) {
	if t.approvalRequester == nil {
		return false, "no_approver_configured"
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
// form EvaluateFSPreflight expects. Mirrors checkPathSegment's own resolve-
// then-fall-back-to-nearest-existing-ancestor shape (shell_path_guard.go)
// rather than reimplementing it: a not-yet-created write target still
// resolves through its existing parent, and an unresolvable path is judged
// on its lexical (cleaned) form rather than aborting.
func resolvePreflightPath(raw string) string {
	clean := filepath.Clean(raw)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return resolved
	}
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
		approved, reason := t.requestPreflightApproval(ctx, sessionID, agentID, toolCallID, command,
			"fs_preflight_blind", "the command could not be parsed for filesystem references")
		// willRecordGrant=false: a blind-spot approval is single-call only —
		// no PathGrant is recorded (there is no resolved {path, access} to
		// record one for), so the next call re-evaluates from scratch.
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
		approved, reason := t.requestPreflightApproval(ctx, sessionID, agentID, toolCallID, command, "fs_preflight",
			fmt.Sprintf("%s needs %s access the sandbox does not currently grant: %s", resolved, accessLabel(op.Access), verdict.PolicyRule))
		// willRecordGrant=true: an approved fs_preflight escalation always
		// records a PathGrant immediately below (RecordPathGrant itself now
		// emits the FR-032(c) shell.grant_recorded event — see
		// pkg/security/approvalgrants.go).
		t.emitApprovalDecision(ctx, sessionID, agentID, command, "fs_preflight", approved, reason, true)
		if !approved {
			return nil, ErrorResult(preflightDenialMessage("filesystem", reason))
		}
		grant := fspolicy.PathGrant{Path: resolved, Access: op.Access}
		t.approvalGrants.RecordPathGrant(sessionID, agentID, grant)
		policy.PathGrants = append(policy.PathGrants, grant)
	}
	return t.approvalGrants.PathGrantsFor(sessionID, agentID), nil
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
	approved, reason := t.requestPreflightApproval(ctx, sessionID, agentID, toolCallID, command, "network_preflight",
		"this command needs outbound network access the sandbox does not currently grant")
	// willRecordGrant=true: an approved network_preflight escalation always
	// records the network grant immediately below (RecordNetworkGrant itself
	// now emits the FR-032(c) shell.grant_recorded event).
	t.emitApprovalDecision(ctx, sessionID, agentID, command, "network_preflight", approved, reason, true)
	if !approved {
		return false, ErrorResult(preflightDenialMessage("network", reason))
	}
	t.approvalGrants.RecordNetworkGrant(sessionID, agentID)
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
	if verdict.Action == shellrule.ActionAsk && mode == ShellModeAuto {
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
// ok is false when args carries no usable "command" string, or the command's
// head cannot be confidently resolved (a blind spot per FR-020) — the
// caller MUST treat that as "no prefix grant can apply," never as a match.
func bashPrefixMatchInputs(args map[string]any) (resolvedBinary string, argWords []string, runInBackground bool, ok bool) {
	command, _ := args["command"].(string)
	if command == "" {
		return "", nil, false, false
	}
	runInBackground = getBoolArg(args, "run_in_background")

	head, fromExpansion, _ := shellCommandHeadDetailed(command)
	if fromExpansion || head == "" {
		return "", nil, runInBackground, false
	}
	path := os.Getenv("PATH")
	resolved, err := shellrule.ResolveBinary(head, path)
	if err != nil {
		return "", nil, runInBackground, false
	}
	words, tokOK := tokenizeShellWords(command)
	if !tokOK {
		return "", nil, runInBackground, false
	}
	_, args2 := resolveShellHead(words)
	return resolved, args2, runInBackground, true
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
