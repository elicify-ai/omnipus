// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file implements founder decision A (2026-09-24, "like Claude Code
// default"): when NO kernel sandbox is enforcing
// (sandbox.TurnPolicyBaseInstalled() is false), Auto mode must not auto-run
// an arbitrary bash command just because the tool-policy ceiling resolved to
// "allow". Without Landlock/seccomp confining the child, the D7/D8
// pre-flights and the text-based guards are the ONLY checks on what a
// command does (see resolveShellMode's own 2026-09-24 doc-comment note) —
// so, on top of those, a command that is neither on a fixed, hand-reviewed
// read-only allowlist nor fully covered by an operator D3 allow rule now
// asks before it runs, exactly the posture a general-purpose coding agent
// with no kernel sandbox should default to.
//
// With a kernel sandbox enforcing, this file changes NOTHING: every check
// here is gated on !sandbox.TurnPolicyBaseInstalled().
package tools

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// noSandboxExtraReadOnlyCommands extends shell_path_guard.go's own
// readOnlyShellCommands (ADR-068's read-classifier allowlist — the SAME
// severe membership criterion: a binary with NO flag, in any common
// implementation, that writes to a filesystem path named on its own command
// line) with commands that criterion also cleanly admits but ADR-068 never
// needed to list: `echo`/`pwd` print, never open, anything; `which` only
// searches PATH; `true`/`false`/`:` are POSIX no-op builtins that ignore
// every argument and touch nothing; `test`/`[` only stat a path to report a
// boolean (never open for write, never create); `exit` takes only a numeric
// status. None of the six can write a file or reach the network by
// themselves — exactly this list's membership bar — which is why a chain
// like `true; echo $?` (2026-09-24 fix: the exact idiom
// conformance-design-chat-e2e.spec.ts's t0 goal-claim steer asks a worker to
// run) belongs on the no-prompt fast path with no kernel sandbox enforcing.
// This is the ONE place this addition lives — the no-sandbox gate below
// reads readOnlyShellCommands directly rather than forking a second copy of
// it.
var noSandboxExtraReadOnlyCommands = map[string]bool{
	"echo": true, "pwd": true, "which": true,
	"true": true, "false": true, ":": true, "test": true, "[": true, "exit": true,
}

// noSandboxReadOnlyGitSubcommands are the `git` subcommands founder
// decision A names by name as read-only: status, log, diff, show. Any other
// git subcommand (or no subcommand at all) is not on the no-sandbox
// allowlist.
var noSandboxReadOnlyGitSubcommands = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true,
}

// isNoSandboxReadOnlySegment reports whether ONE segment is eligible for
// founder decision A(1)(a)'s no-prompt fast path: a literal, un-normalised,
// non-wrapper head found on the read-only allowlist above, with no
// redirection, command/process substitution, subshell/grouping, or leading
// `VAR=value` assignment anywhere in the segment.
//
// shellrule.IsSimpleSegment (reused here via shellRuleSimpleSegment, the
// same helper the D4 prefix-grant check and D3's FullyAllowed fast path
// already share — ADR-092 D3's "no new parser" principle applied to this
// gate too) is exactly "no redirection to a file, no command substitution":
// its metachar scan ("()<>`") disqualifies any `>`/`<` redirection and any
// `$(...)`/backtick/subshell shape in one check, and its POSIX leading-
// assignment check closes `LD_PRELOAD=evil.so cat file` the same way FR-040
// already does elsewhere.
//
// "No interpreter" needs no separate check: bash/sh/zsh/eval/python*/…
// are not, and never will be, on readOnlyShellCommands — that allowlist's
// own membership criterion (shell_path_guard.go) already excludes every
// interpreter by construction ("every interpreter... arbitrary file access
// by construction").
//
// A segment that fails to TOKENIZE at all (an unbalanced quote) is refused
// outright, before any allowlist match — shellRuleSimpleSegment's metachar
// scan does not look for quote balance (`"` is not in
// simpleSegmentMetachars), so `echo "unbalanced` reads as the literal head
// "echo" with no disqualifying character present and would otherwise pass
// straight through this allowlist. That is exactly the FR-020 blind spot
// D7/D8 fail closed on (ClassifyPathOperations/ClassifyNetworkNeed both use
// this same tokenizer and both refuse to classify it) — this fast path must
// fail closed the same way, not silently declare an unparseable command
// read-only because its unparseable head happens to spell "echo".
func isNoSandboxReadOnlySegment(seg string) bool {
	if _, ok := tokenizeShellWords(seg); !ok {
		return false
	}
	if !shellRuleSimpleSegment(seg) {
		return false
	}
	head, fromExpansion, normalised := shellCommandHeadDetailed(seg)
	if fromExpansion || normalised || head == "" {
		// FR-040's own posture, applied to this allowlist too: a command
		// name built from an expansion, or reachable only via case-folding
		// or a stripped directory prefix, must never satisfy an allowlist
		// match — that is exactly the look-alike shape FR-040 exists to
		// refuse.
		return false
	}
	if readOnlyShellCommands[head] || noSandboxExtraReadOnlyCommands[head] {
		return true
	}
	if head == "git" {
		args := segmentArgWords(seg)
		return len(args) > 0 && noSandboxReadOnlyGitSubcommands[args[0]]
	}
	return false
}

// commandIsNoSandboxReadOnly reports whether EVERY segment of command
// clears isNoSandboxReadOnlySegment — founder decision A(1)(a): "Chained
// segments must ALL be on the list." A command that fails to segment into
// anything (should not happen — even an empty string segments to one empty
// segment, which isNoSandboxReadOnlySegment refuses via its empty-head
// check) is conservatively NOT read-only.
func commandIsNoSandboxReadOnly(command string) bool {
	segments := splitShellSegments(command)
	if len(segments) == 0 {
		return false
	}
	for _, seg := range segments {
		if !isNoSandboxReadOnlySegment(seg) {
			return false
		}
	}
	return true
}

// commandTriggersExistingPreflight is a non-prompting DRY RUN of D7
// (enforceFSPreflight) and D8 (enforceNetworkPreflight)'s own escalation
// predicate: would either pre-flight need to ask (or refuse outright) for
// command on its own, independent of founder decision A's new gate?
//
// This makes the new no-sandbox gate MUTUALLY EXCLUSIVE with D7/D8 rather
// than layered on top of them: when either would already act on its own —
// with a more specific, path/host-named card, or FR-037's outright refusal
// — this gate stays silent and lets that call site run for real afterward,
// exactly as it did before this founder decision (every existing
// TestEnforceShellPermissionMode_NoKernelSandbox_* proof of that still
// holds). It ONLY fires the new no_sandbox_ask prompt for the gap those two
// checks do not cover at all: e.g. a WRITE via a RELATIVE path (`touch
// notes/x`, `rm -rf x`, `echo hi > notes/y`) — D7's classifier is
// absolute-path-only (ClassifyPathOperations's own scope) — or a command
// with no filesystem/network signature the classifiers recognise at all
// (`cd ..`, `bash -c "echo"`). "ask exactly once" (test requirement) is
// achieved by this exclusivity, not by suppressing either pre-flight's own
// prompt.
//
// Mirrors enforceFSPreflight/enforceNetworkPreflight's OWN evaluation
// exactly (same functions, same inputs) rather than a separate heuristic,
// so this can never disagree with what those two will actually do
// immediately afterward.
func (t *ExecTool) commandTriggersExistingPreflight(ctx context.Context, command, sessionID, agentID string) bool {
	ops, ok := ClassifyPathOperations(command)
	if !ok {
		// FR-020 blind spot: enforceFSPreflight's own fs_preflight_blind
		// branch will ask (or auto-deny headless) for this on its own.
		return true
	}
	if len(ops) > 0 {
		readConfined := ReadConfined(ctx)
		existing := t.approvalGrants.PathGrantsFor(sessionID, agentID)
		if policy, ferr := ResolveTurnFSPolicy(ctx, t.workingDir, t.restrictToWorkspace, existing); ferr == nil {
			for _, op := range ops {
				resolved := resolvePreflightPath(op.Path)
				v := EvaluateFSPreflight(policy, resolved, op.Access, readConfined)
				if v.Refused || v.NeedsEscalation() {
					return true
				}
			}
		}
		// A ResolveTurnFSPolicy error here is deliberately NOT treated as
		// "triggers preflight": enforceFSPreflight would hit the identical
		// error immediately afterward and return it as a genuine ToolResult
		// error — this dry run only needs to answer "will a PROMPT happen
		// on its own", and an outright error is not a prompt this gate
		// would otherwise duplicate.
	}
	granted := t.approvalGrants.HasNetworkGrant(sessionID, agentID)
	approvedHosts := t.approvalGrants.NetworkHostsFor(sessionID, agentID)
	return EvaluateNetworkPreflight(command, granted, approvedHosts).NeedsEscalation()
}

// noSandboxAskNote is founder decision A(2)'s exact operator-facing
// explanation, carried on the approval request so the dialog states WHY it
// is asking rather than merely that it is.
const noSandboxAskNote = "No kernel sandbox is enforcing, so shell commands that could change files or reach the network ask first."

// requestNoSandboxApproval is founder decision A's new Auto-mode escalation:
// reached only when NO kernel sandbox is enforcing, the command did not
// fully clear the D3 operator allow-rule fast path (FullyAllowed), and it is
// not on the fixed read-only allowlist (commandIsNoSandboxReadOnly).
// Mirrors requestPreflightApproval's own shape (headless auto-deny per D-08,
// FR-032(b)/(d) audit pair) — a new, dedicated adr092_kind ("no_sandbox_ask")
// rather than overloading "fs_preflight"/"network_preflight", since this gate
// is neither.
func (t *ExecTool) requestNoSandboxApproval(ctx context.Context, sessionID, agentID, toolCallID, command string) (approved bool, reason string) {
	t.emitPreflightEscalation(ctx, sessionID, agentID, command, audit.ShellPreflightNoSandbox,
		"no kernel sandbox is enforcing, and the command is neither on the read-only allowlist nor fully covered by an operator allow rule",
		"approval to run a command that could change files or reach the network with no kernel confinement")
	if ToolAutoDenyAsk(ctx) {
		// D-08: an unattended/scheduled run has nobody to answer this
		// dialog, so it is auto-denied rather than stalled — the same
		// headless posture every other ADR-092 approval call site in this
		// package already applies.
		t.emitApprovalDecision(ctx, sessionID, agentID, command, "no_sandbox_ask", false, headlessShellDenyReason, false)
		return false, headlessShellDenyReason
	}
	if t.approvalRequester == nil {
		t.emitApprovalDecision(ctx, sessionID, agentID, command, "no_sandbox_ask", false, "no_approver_configured", false)
		return false, "no_approver_configured"
	}
	args := map[string]any{
		"command":     command,
		"adr092_kind": "no_sandbox_ask",
		"note":        noSandboxAskNote,
	}
	approved, reason, _ = t.approvalRequester.RequestShellApproval(ctx, sessionID, agentID, t.Name(), toolCallID, ToolTurnID(ctx), args)
	// willRecordGrant=false: this gate has no standing-grant concept of its
	// own (unlike D7/D8's path/network widening) — every command is
	// re-evaluated against the allowlist/allow-rule fast path on its own
	// next call.
	t.emitApprovalDecision(ctx, sessionID, agentID, command, "no_sandbox_ask", approved, reason, false)
	return approved, reason
}

// noSandboxDenialMessage explains a refused founder-decision-A escalation —
// mirrors preflightDenialMessage's shape for the two existing D7/D8 kinds.
func noSandboxDenialMessage(reason string) string {
	return fmt.Sprintf(
		"Command blocked: no kernel sandbox is enforcing, and this command needed approval it did not receive (%s). "+
			"The command did not run.", reason)
}
