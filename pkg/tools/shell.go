// The unified `bash` tool (ADR-036, bash-tool-spec.md).
//
// `bash` replaces the three previously-separate shell tools (`exec`,
// `workspace_shell`, `workspace_shell_bg`) with ONE tool, ONE policy surface,
// and ONE hardening path. See docs/internal/architecture/ADR-036-consolidate-
// shell-and-subagent-tools.md and docs/internal/specs/bash-tool-spec.md for
// the full rationale and FR/BDD traceability. Highlights:
//
//   - Registration is universal (every agent), governed exclusively by
//     ToolPolicyCfg — the old experimental.workspace_shell_enabled gate is
//     retired (FR-B8).
//   - `cwd` is relative-to-workspace ONLY; there is no absolute-path escape
//     hatch (FR-B2/FR-B13). The guard resolves symlinks before the
//     containment check, so a workspace-internal symlink pointing outside the
//     workspace cannot be used to escape it.
//   - The hardcoded deny-pattern baseline (rm -rf /, master.key/
//     credentials.json literal guards, curl-pipe-to-shell, fork bomb, ...)
//     applies unconditionally — no policy verdict or operator configuration
//     can disable it (FR-B4). It is layered with an operator-extensible
//     custom-pattern mechanism (global + per-agent), which IS opt-in/off by
//     default.
//   - `pkg/policy.Evaluator.EvaluateExec` (the binary allowlist, SEC-05)
//     applies identically to foreground and background calls (FR-B5).
//   - Every non-god-mode invocation routes through `sandbox.ResolveLimits` +
//     `sandbox.ApplyChildHardening`/`sandbox.Run` (ADR-035 §7) — there is no
//     longer a separate "sandbox off but not god mode" state; the fixed
//     kernel-sandbox boundary is universal except when the global god-mode
//     override (agent.GodModeActive) is active (FR-B6).
//   - Audit-log write failures fail CLOSED — the call is refused rather than
//     silently proceeding unaudited (FR-B7).
//   - PTY / interactive sessions (send-keys, write) and free-form background
//     port-exposure (the old workspace_shell_bg capability) are DROPPED
//     entirely — accepted capability reductions per ADR-036 §3.1. The
//     `web_serve` tool covers the legitimate dev-server use case.

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/policy"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

// ExecPolicyAuditor evaluates a bash command against the policy engine and
// audit-logs the decision. Implemented by *policy.PolicyAuditor. Defined as an
// interface so tests can supply lightweight mocks and so this package does not
// need to directly import the audit package through this dependency edge.
//
// Contract: implementations MUST audit-log every decision (allow AND deny) as
// a side effect of EvaluateExec. Returning a decision without logging violates
// the SEC-15/ADR-002 §W-3 contract. This is not expressible in the signature
// but is part of the type's invariant — test doubles must honor it.
type ExecPolicyAuditor interface {
	EvaluateExec(agentID, command string) policy.Decision
}

// ExecToolDeps bundles the ADR-035/ADR-036 dependencies for the bash tool.
// All fields are optional — a nil PolicyAuditor disables binary allowlist
// enforcement (useful when the policy layer is not configured).
//
// Note: the interactive approval layer (SEC-08, "ask" prompts) and the
// allow/ask/deny TOOL POLICY gate are handled upstream of this tool entirely
// (HookManager.ApproveTool / the compositor's EffectiveToolPolicy resolution)
// — a "deny" verdict means Execute is never called at all, so bash does not
// re-implement that check. What DOES live here is the narrower, automated
// binary allowlist (SEC-05) and the deny-pattern/sandbox layers that apply
// regardless of the policy verdict.
type ExecToolDeps struct {
	// PolicyAuditor enforces the binary allowlist (SEC-05) and audit-logs the
	// decision. Nil disables the check (default-permissive).
	PolicyAuditor ExecPolicyAuditor

	// GodMode reflects agent.GodModeActive(cfg), resolved ONCE at wiring time.
	// When true, ApplyChildHardening/sandbox.Run are skipped entirely and the
	// command runs with full host latitude (see runUnconstrained).
	GodMode bool

	// Proxy is the process-wide kernel-sandbox egress proxy (SSRF
	// protection). May be nil (no HTTP_PROXY injection on the sandbox-on
	// path); ignored entirely under GodMode.
	Proxy *sandbox.EgressProxy

	// AuditFailClosed: when true, an audit-log write failure aborts execution
	// (FR-B7) rather than proceeding unaudited.
	AuditFailClosed bool

	// GlobalShellDenyPatterns is the operator-global, OPT-IN deny-pattern
	// extension list (config.Sandbox.ShellDenyPatterns). Layered ON TOP of
	// (never a substitute for) the hardcoded baseline, and only consulted
	// when AgentShellPolicy.EnableDenyPatterns is true.
	GlobalShellDenyPatterns []string

	// AgentShellPolicy is the per-agent shell policy (AgentConfig.ShellPolicy).
	// Nil means no per-agent custom patterns and EnableDenyPatterns=false
	// (operator-extensible layer off; the hardcoded baseline still applies).
	AgentShellPolicy *config.AgentShellPolicy
}

var (
	globalSessionManager = NewSessionManager()
	sessionManagerMu     sync.RWMutex
)

// marshalErrorFallback builds a safe minimal JSON payload for the rare case
// where json.Marshal(resp) itself fails on one of the ExecResponse
// constructions below. Uses encoding/json rather than fmt.Sprintf's %s,
// which interpolated marshalErr.Error() — an arbitrary, unescaped string —
// directly into a JSON string literal: exactly the #618 hand-built-JSON
// defect class (Go-string/verb substitution instead of JSON quoting) fixed
// elsewhere in this codebase (pkg/tools/result.go's marshalWithinBudget
// producers). json.Marshal of a map[string]string cannot itself fail for any
// valid Go string — encoding/json replaces invalid UTF-8 rather than
// rejecting it — so the inner error branch is unreachable in practice; it
// exists only so this helper never panics or returns invalid JSON.
func marshalErrorFallback(marshalErr error) []byte {
	data, err := json.Marshal(map[string]string{
		"error": "failed to serialize response: " + marshalErr.Error(),
	})
	if err != nil {
		return []byte(`{"error":"failed to serialize response"}`)
	}
	return data
}

func getSessionManager() *SessionManager {
	sessionManagerMu.RLock()
	defer sessionManagerMu.RUnlock()
	return globalSessionManager
}

// ExecTool implements the `bash` builtin tool (registered name: "bash";
// the Go type retains its historical "Exec" name to minimize unrelated churn
// across ~20 call sites — see bash-tool-spec.md Assumptions: "the exact final
// home of the merged implementation ... is an implementer's naming choice").
type ExecTool struct {
	BaseTool

	workingDir          string
	restrictToWorkspace bool
	allowedPathPatterns []*regexp.Regexp

	// denyPatterns is the hardcoded baseline (defaultDenyPatterns).
	// Unconditional: FR-B4 forbids disabling this via policy or config.
	denyPatterns []*regexp.Regexp

	// operatorDenyPatterns is the OPT-IN, operator-extensible layer (global +
	// per-agent custom patterns), only consulted when
	// enableOperatorDenyPatterns is true. Layered on top of denyPatterns,
	// never a substitute for it.
	operatorDenyPatterns       []*regexp.Regexp
	enableOperatorDenyPatterns bool

	sessionManager *SessionManager

	// policyAuditor enforces the binary allowlist (SEC-05) uniformly for
	// foreground and background calls (FR-B5).
	policyAuditor ExecPolicyAuditor

	// godMode / proxy: see ExecToolDeps. Resolved once at wiring time.
	godMode bool
	proxy   *sandbox.EgressProxy

	// auditLogger + auditFailClosed implement FR-B7 (fail-closed on audit
	// write failure).
	auditLogger     *audit.Logger
	auditFailClosed bool

	// killAuditFn is a pre-built audit callback for process-kill failures,
	// constructed once at SetAuditLogger time so every kill site (foreground
	// timeout, background timeout, explicit kill action) shares one closure.
	// Nil when auditLogger is nil.
	killAuditFn func(pid int, killErr error, caller string)
}

// GodModeForTest exposes the resolved god-mode flag for white-box testing.
// Kept as a production method because pkg/agent tests require cross-package
// access (mirrors the pre-consolidation WorkspaceShellTool.GodModeForTest).
func (t *ExecTool) GodModeForTest() bool { return t.godMode }

// SetAuditLogger injects an audit.Logger into the ExecTool so exec/deny
// decisions and kill failures are recorded. Satisfies the auditLoggerAware
// contract used by the ToolRegistry. Calling this on a nil ExecTool is a no-op.
func (t *ExecTool) SetAuditLogger(l *audit.Logger) {
	if t == nil {
		return
	}
	t.auditLogger = l
	if l == nil {
		t.killAuditFn = nil
		return
	}
	al := l
	t.killAuditFn = func(pid int, killErr error, caller string) {
		audit.EmitEntry(al, &audit.Entry{
			Event:    audit.EventProcessKillFailed,
			Decision: audit.DecisionError,
			Details: map[string]any{
				"pid":    pid,
				"error":  killErr.Error(),
				"caller": caller,
			},
		})
	}
}

var (
	// defaultDenyPatterns is the hardcoded deny-pattern baseline, ported
	// verbatim from the pre-consolidation `exec` tool (FR-B4). Applies
	// UNCONDITIONALLY — no policy verdict or operator configuration disables
	// this list.
	//
	// The secrets-subtree literal guards (v0.2 #155 item 8) are appended below
	// via secretGuardPatterns rather than hand-copied here — see that var's doc
	// comment for why hand-copying is exactly the bug this replaces.
	defaultDenyPatterns = append([]*regexp.Regexp{
		regexp.MustCompile(`\brm\s+-[rf]{1,2}\b`),
		regexp.MustCompile(`\bdel\s+/[fq]\b`),
		regexp.MustCompile(`\brmdir\s+/s\b`),
		// Match disk wiping commands (must be followed by space/args)
		regexp.MustCompile(
			`\b(format|mkfs|diskpart)\b\s`,
		),
		regexp.MustCompile(`\bdd\s+if=`),
		// Block writes to block devices (all common naming schemes).
		regexp.MustCompile(
			`>\s*/dev/(sd[a-z]|hd[a-z]|vd[a-z]|xvd[a-z]|nvme\d|mmcblk\d|loop\d|dm-\d|md\d|sr\d|nbd\d)`,
		),
		regexp.MustCompile(`\b(shutdown|reboot|poweroff)\b`),
		// Fork-bomb guard. Widened in v0.2 #155 item 5 to match every
		// documented bypass shape:
		//   `: ( ) { :|:& };:`        (whitespace anywhere)
		//   `b(){b|b};b`              (disguised with arbitrary identifier)
		//   `:(){ :|:& \n };:`        (newline inside braces)
		// RLIMIT_NPROC in hardened_exec_linux.go is the kernel-layer
		// backstop for any shape that still slips through.
		regexp.MustCompile(`(?s)([A-Za-z_]\w*|:)\s*\(\s*\)\s*\{[^{}]*[|&][^{}]*\}\s*;\s*([A-Za-z_]\w*|:)`),
		// NOTE: the blanket `\$\([^)]+\)` rule that used to sit here —
		// "reject ANY command substitution" — was removed. It blocked benign
		// substitutions (`$(seq 1 5)`, `$(date)`, `$(pwd)`), making bounded
		// `for` loops unusable, and it made the four `$(cat|curl|wget|which `
		// rules below it unreachable. Command substitutions are now judged
		// STRUCTURALLY by substitutionGuard (shell_subst_guard.go), which is
		// applied on this same unconditional baseline path in guardCommand and
		// preserves every dangerous shape the blanket rule caught. Do not
		// reinstate a blanket rule here without reading that file's threat
		// notes first.
		regexp.MustCompile(`\$\{[^}]+\}`),
		regexp.MustCompile("`[^`]+`"),
		regexp.MustCompile(`\|\s*sh\b`),
		regexp.MustCompile(`\|\s*bash\b`),
		regexp.MustCompile(`;\s*rm\s+-[rf]`),
		regexp.MustCompile(`&&\s*rm\s+-[rf]`),
		regexp.MustCompile(`\|\|\s*rm\s+-[rf]`),
		// REMOVED (ADR-068 §3, 2026-08-23): `regexp.MustCompile("<<\\s*EOF")`.
		// Do NOT restore it. applyDenyPatterns LOWERCASES the command before
		// matching (lowerASCII, shell_guard.go:32), so an uppercase `EOF`
		// literal could never match anything: the rule was unreachable from the
		// day it was written and blocked exactly zero commands over its whole
		// life. Deleting it therefore changes no runtime behaviour.
		//
		// Making it fire instead would have been a REGRESSION dressed up as a
		// fix — heredocs (`cat > notes.md << EOF … EOF`) are an ordinary,
		// currently-working way for an agent to write a file, and the ADR-068
		// investigation confirmed the deny layer allows every heredoc write
		// shape UAT defect 003 reported as blocked (those were blocked by the
		// path-containment scan below, not here).
		//
		// Two tests pin this: TestDefaultDenyPatterns_HeredocsArePermitted
		// asserts heredocs stay allowed, and
		// TestDefaultDenyPatterns_ContainNoUppercaseOnlyLiterals asserts no
		// pattern in this list can contain an uppercase-only literal again —
		// the whole bug class, not just this one instance (shell_guard_test.go).
		// The four substitution rules below are now ALSO covered by
		// substitutionGuard's R2 (which additionally handles `$(/bin/cat …)`,
		// `$(FOO=1 curl …)` and mid-pipeline positions). They are retained
		// verbatim as literal, cheap redundancy: if the structural scanner ever
		// regresses, these still fire.
		regexp.MustCompile(`\$\(\s*cat\s+`),
		regexp.MustCompile(`\$\(\s*curl\s+`),
		regexp.MustCompile(`\$\(\s*wget\s+`),
		regexp.MustCompile(`\$\(\s*which\s+`),
		regexp.MustCompile(`\bsudo\b`),
		regexp.MustCompile(`\bchmod\s+[0-7]{3,4}\b`),
		regexp.MustCompile(`\bchown\b`),
		regexp.MustCompile(`\bpkill\b`),
		regexp.MustCompile(`\bkillall\b`),
		regexp.MustCompile(`\bkill\b`),
		regexp.MustCompile(`\bcurl\b.*\|\s*(sh|bash)`),
		regexp.MustCompile(`\bwget\b.*\|\s*(sh|bash)`),
		regexp.MustCompile(`\bnpm\s+install\s+-g\b`),
		regexp.MustCompile(`\bpip\s+install\s+--user\b`),
		regexp.MustCompile(`\bapt\s+(install|remove|purge)\b`),
		regexp.MustCompile(`\byum\s+(install|remove)\b`),
		regexp.MustCompile(`\bdnf\s+(install|remove)\b`),
		regexp.MustCompile(`\bdocker\s+run\b`),
		regexp.MustCompile(`\bdocker\s+exec\b`),
		regexp.MustCompile(`\bgit\s+push\b`),
		regexp.MustCompile(`\bgit\s+force\b`),
		regexp.MustCompile(`\bssh\b.*@`),
		regexp.MustCompile(`\beval\b`),
		regexp.MustCompile(`\bsource\s+.*\.sh\b`),
		regexp.MustCompile(`<\([^)]*\)`),
		regexp.MustCompile(`>\([^)]*\)`),
	}, secretGuardPatterns...)

	// secretGuardPatterns is the v0.2 #155 item 8 secrets-subtree literal-text
	// backstop (option B), generated FROM fspolicy.SecretEntriesAlways rather
	// than hand-copied.
	//
	// It used to be two hardcoded lines here — `\bmaster\.key\b` and
	// `\bcredentials\.json\b` — written when the secret set had exactly those
	// two entries. The set has since grown to five (config.json, cli.token,
	// entities joined master.key and credentials.json; see
	// fspolicy.SecretEntriesAlways), and grew again since (auth.json, backups).
	// The hand-copied pair never gained any of them: this guard is a backstop
	// over a boundary the kernel sandbox already enforces, so its silent
	// drift was invisible in every test that exercises the kernel deny
	// instead. A backstop that covers 2 of N entries and looks like it covers
	// all of them is worse than no backstop, because a reviewer reads
	// "secrets-subtree path-guard" and stops checking.
	//
	// Generating the list closes that class of drift structurally — there is
	// no second copy to fall behind. TestSecretGuardPatterns_CoverEverySecretEntryAlways
	// (shell_secret_guard_test.go) is the regression: it fails the moment
	// SecretEntriesAlways gains an entry this can't already reach, which is
	// possible only if this generation is ever replaced with a literal list
	// again.
	//
	// Scoped to SecretEntriesAlways, not the combined SecretEntriesRelative:
	// the per-turn half (agents/, workspaces/) is made of ordinary English
	// words an agent legitimately types constantly ("list the workspaces",
	// "check the agents dir"), and the own-tree exception that makes reaching
	// them sometimes correct (fspolicy.DeniedPathsFor) is inherently
	// contextual — a static text guard has no turn to evaluate that against.
	// The five ALWAYS names are never legitimate in ANY turn, which is what
	// makes a context-free literal match safe for them and not for the rest.
	secretGuardPatterns = buildSecretGuardPatterns()
)

// buildSecretGuardPatterns compiles one case-insensitive-by-construction
// (applyDenyPatterns lowercases the command before matching) word-boundary
// regex per fspolicy.SecretEntriesAlways entry.
func buildSecretGuardPatterns() []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(fspolicy.SecretEntriesAlways))
	for _, name := range fspolicy.SecretEntriesAlways {
		out = append(out, regexp.MustCompile(`\b`+regexp.QuoteMeta(strings.ToLower(name))+`\b`))
	}
	return out
}

var (
	// absolutePathPattern matches absolute file paths in commands (Unix and Windows).
	// absolutePathPattern extracts absolute-path candidates from raw command
	// TEXT so guardCommand can reject references outside the workspace.
	//
	// Three properties are load-bearing:
	//
	//  1. The path BODY stops at shell metacharacters (; | & ( ) < > , { } [ ]
	//     ! * ` $ ~ \), not just at whitespace and quotes. The original class
	//     was `[^\s"']+`, so the ubiquitous idiom `2>/dev/null;` extracted the
	//     candidate `/dev/null;` — WITH the semicolon — which does not match
	//     the safePaths key "/dev/null". The exemption silently missed and the
	//     whole command was rejected, including every innocent fragment
	//     chained beside it. The set was later widened (this revision) to
	//     also stop at brace/bracket/glob punctuation so multi-path shell
	//     shapes like `{/etc/shadow,/etc/passwd}` or `cat[/etc/shadow]` split
	//     into the individual candidates they textually contain instead of
	//     gluing trailing punctuation onto (or past) the real path.
	//
	//  2. The match must begin at a TOKEN BOUNDARY, captured in group 1.
	//     Plain single-character boundaries (whitespace, quote, = : ; , { }
	//     [ ] ! * ` $ ~ \ and the shell metacharacters above) cover most
	//     cases. Without this restriction the pattern matched the first `/`
	//     found ANYWHERE, including one in the middle of a relative path:
	//     `-o build/app.min.js` yielded the fabricated candidate
	//     `/app.min.js`, which resolves to the filesystem root and was
	//     rejected as outside the workspace — even though the real argument
	//     was a relative path inside it.
	//
	//     Two shapes need MORE than a single boundary character because no
	//     single character sits directly before the leading `/`:
	//
	//       - Attached short flags: `-o/etc/passwd`, `-I/etc`, `-C/etc`. The
	//         candidate has the exact same textual shape as the legitimate
	//         relative path in `-o build/app.min.js` — a `/` following a
	//         `-flag` token — so the two must be told apart by what comes
	//         right after the flag, not by the flag itself. This pattern
	//         additionally matches `(?:^|\s)-[A-Za-z]` immediately followed
	//         by the candidate body, i.e. the flag itself must sit at a
	//         token start (start of command or preceded by whitespace) AND
	//         the very next character after the flag letter must be `/`. A
	//         relative arg (`-o build/…`) never satisfies the second part
	//         (there is a space, not a `/`, right after `-o`), and a
	//         hyphenated relative path segment (`build-x/output`) never
	//         satisfies the first part (the `-x` is not at a token start —
	//         `d` precedes it, not whitespace) — so neither is affected.
	//       - Variable-expansion prefixes: `$HOME/.ssh/id_rsa`,
	//         `${HOME}/.ssh/id_rsa`. This pattern additionally matches
	//         `(?:^|\s)\$\{?[A-Za-z_][A-Za-z0-9_]*\}?` (a token-start shell
	//         variable reference) immediately followed by the candidate
	//         body, so a resolved-at-runtime path prefix does not hide an
	//         absolute suffix from this compile-time text scan.
	//
	//  3. The boundary set and the excluded body set are almost — but
	//     deliberately NOT — the same metacharacters: `:` and `=` are
	//     boundary-only, never excluded from the body. `=` bodies are
	//     unrestricted by design (unchanged from the original version).
	//     `:` is unrestricted so that a colon-joined path LIST (`PATH=`
	//     assignments, `-I a:b`-style compiler/linker flags) is captured as
	//     ONE candidate rather than split into several fragments that each
	//     independently look like a bare absolute path outside the
	//     workspace. guardCommand recognizes that specific shape
	//     (colonPathListPattern) and evaluates EACH `:`-separated segment
	//     against the same workspace-boundary check applied to a bare
	//     candidate (safePaths / allowedPathPatterns exemption, then
	//     containment) — see guardCommand's colon-list check. A list is
	//     allowed only when every segment is inside the workspace or
	//     exempt; a list containing any out-of-workspace segment is
	//     blocked, same as a single bare candidate would be.
	//
	// Worked examples:
	//   - `which node 2>/dev/null; echo done` -> candidate "/dev/null", exempt
	//     via safePaths (rule 1).
	//   - `curl -sL -o build/app.min.js https://…` -> no candidate at all
	//     for `build/app.min.js` (rule 2, general case).
	//   - `curl -o/etc/passwd https://x` -> candidate "/etc/passwd", blocked
	//     (rule 2, attached-flag case) — contrast with the previous example,
	//     where the space after `-o` prevents the flag alternative from
	//     firing.
	//   - `PATH=/usr/bin:/usr/local/bin make` -> candidate
	//     "/usr/bin:/usr/local/bin" (ONE match, per rule 3); guardCommand's
	//     colon-list check splits it into "/usr/bin" and "/usr/local/bin"
	//     and evaluates each — both are outside the workspace, so the
	//     command is BLOCKED (consistent with what a bare `/usr/bin`
	//     candidate would do).
	//
	// Callers MUST read group 1, not the whole match, since the match also
	// consumes the leading boundary text (which may be more than one
	// character — see rule 2's attached-flag and variable-prefix cases).
	//
	// The attached-flag alternative is `-[A-Za-z]+`, not `-[A-Za-z]`. With
	// exactly one letter, `-o/etc/passwd` was caught but a COMBINED short flag
	// with an attached path was not: in `tar -cf/etc/passwd` the character
	// before `/` is `f`, which is neither a single flag letter after `-` nor a
	// boundary character, so no candidate was extracted at all and the
	// workspace-boundary check never ran on that path. Same for `cc -Wl/etc/x`.
	// Defence-in-depth rather than the primary control — the kernel sandbox is
	// that — but a guard that misses the combined form misses the form people
	// actually type.
	absolutePathPattern = regexp.MustCompile(
		`(?:^|[\s"'=:;,{}\[\]!*` + "`" + `$~\\|&()<>]|(?:^|\s)-[A-Za-z]+|(?:^|\s)\$\{?[A-Za-z_][A-Za-z0-9_]*\}?)` +
			`([A-Za-z]:\\[^\\\s"';,{}\[\]!*` + "`" + `$~|&()<>]+` +
			`|/[^\s"';,{}\[\]!*` + "`" + `$~\\|&()<>]+)`,
	)

	// colonPathListPattern recognizes a colon-joined list of two or more
	// absolute Unix paths — e.g. "/usr/bin:/usr/local/bin" — the shape
	// produced by `PATH=` assignments and by multi-path compiler/linker
	// flags (`-I a:b`). absolutePathPattern's Unix body does not exclude
	// `:` (see that pattern's rule 3), so a colon-joined list is captured
	// as a single raw candidate rather than fragmented at each colon.
	// guardCommand matches that raw candidate against this pattern and, on
	// a match, SPLITS it on `:` and evaluates each segment independently
	// against the same workspace-boundary check applied to a bare
	// candidate (safePaths exemption, allowedPathPatterns exemption, then
	// containment relative to cwd). A list is allowed only when every
	// segment clears that check; a list containing any out-of-workspace,
	// non-exempt segment is blocked. This is deliberately narrow — every
	// segment must independently look like an absolute path (start with
	// `/`, contain none of the same excluded punctuation) — so it does not
	// exempt a single path with a stray colon suffix appended to it
	// (`/etc/passwd:evil` has a second segment that does not start with
	// `/`, so it does not match and is still evaluated as a single,
	// literal candidate).
	colonPathListPattern = regexp.MustCompile(
		`^/[^\s"';,{}\[\]!*` + "`" + `$~\\:]+` +
			`(?::/[^\s"';,{}\[\]!*` + "`" + `$~\\:]+)+$`,
	)

	// safePaths are kernel pseudo-devices that are always safe to reference in
	// commands, regardless of workspace restriction. They contain no user data
	// and cannot cause destructive writes.
	safePaths = map[string]bool{
		"/dev/null":    true,
		"/dev/zero":    true,
		"/dev/random":  true,
		"/dev/urandom": true,
		"/dev/stdin":   true,
		"/dev/stdout":  true,
		"/dev/stderr":  true,
	}
)

// NewExecTool constructs a minimal bash tool with no deps injected (test /
// metadata-only use).
func NewExecTool(workingDir string, restrict bool, allowPaths ...[]*regexp.Regexp) (*ExecTool, error) {
	return NewExecToolWithConfig(workingDir, restrict, nil, allowPaths...)
}

// NewExecToolWithDeps constructs an ExecTool with the full ADR-036 dependency
// set (policy auditor, god-mode, egress proxy, audit-fail-closed, deny
// patterns). Callers that do not need these should use NewExecToolWithConfig.
func NewExecToolWithDeps(
	workingDir string,
	restrict bool,
	cfg *config.Config,
	deps ExecToolDeps,
	allowPaths ...[]*regexp.Regexp,
) (*ExecTool, error) {
	tool, err := NewExecToolWithConfig(workingDir, restrict, cfg, allowPaths...)
	if err != nil {
		return nil, err
	}
	tool.policyAuditor = deps.PolicyAuditor
	tool.godMode = deps.GodMode
	tool.proxy = deps.Proxy
	tool.auditFailClosed = deps.AuditFailClosed

	tool.operatorDenyPatterns = compileDenyPatterns(deps.GlobalShellDenyPatterns, "global")
	if deps.AgentShellPolicy != nil {
		tool.enableOperatorDenyPatterns = deps.AgentShellPolicy.EnableDenyPatterns
		tool.operatorDenyPatterns = append(
			tool.operatorDenyPatterns,
			compileDenyPatterns(deps.AgentShellPolicy.CustomDenyPatterns, "agent")...,
		)
	}
	return tool, nil
}

// NewExecToolWithConfig constructs a bash tool without the ADR-036 deps
// (policy auditor / god-mode / proxy / operator deny patterns default to
// off/nil). Used for early registration (before the AgentLoop's dependencies
// are ready — see pkg/agent/instance.go) and for metadata-only catalog
// instances (pkg/tools/general_builtin_catalog.go). cfg is accepted for
// signature stability with existing call sites; the ADR-036 config-derived
// deps are supplied later via NewExecToolWithDeps.
func NewExecToolWithConfig(
	workingDir string,
	restrict bool,
	_ *config.Config,
	allowPaths ...[]*regexp.Regexp,
) (*ExecTool, error) {
	var allowedPathPatterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		allowedPathPatterns = allowPaths[0]
	}

	return &ExecTool{
		workingDir:          workingDir,
		restrictToWorkspace: restrict,
		allowedPathPatterns: allowedPathPatterns,
		denyPatterns:        defaultDenyPatterns,
		sessionManager:      getSessionManager(),
	}, nil
}

func (t *ExecTool) Name() string { return "bash" }

func (t *ExecTool) Scope() ToolScope       { return ScopeCore }
func (t *ExecTool) Category() ToolCategory { return CategoryShell }

func (t *ExecTool) Description() string {
	return "Execute a shell command (foreground or backgrounded) in the workspace and get its output.\n" +
		"sh -c on Linux/macOS, powershell on Windows. Set run_in_background=true for long-running commands " +
		"(returns a session_id immediately); use action=poll/read/kill with that session_id to check on it, " +
		"read incremental output, or terminate it. cwd is relative to the workspace only (no absolute paths, " +
		"no '..' escapes). timeout_seconds defaults to 300 and must be between 1 and 3600; enforced identically " +
		"in the foreground and in the background — a background session times out on its own after " +
		"timeout_seconds elapses, and is otherwise stopped only by an explicit kill action or an explicit " +
		"session cancel. Output is truncated beyond a size cap — a SUCCEEDING command keeps up to 64,000 " +
		"characters, a FAILING one only 10,000 (the failure cap is smaller, so a large error command's output " +
		"is cut harder than a successful one's); redirect to a file and read it with read_file/offset when you " +
		"need all of it. Commands are screened by a safety guard (deny patterns, a binary allowlist, and a " +
		"path-use check) before they run — writing outside your workspace requires a mount first (see " +
		"list_mounts / request_mount); a \"blocked by safety guard\" or \"blocked by exec allowlist\" error means " +
		"the guard refused the command, not that it failed to run."
}

func (t *ExecTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "Shell command to execute (required for action=run)",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Optional human-readable description of what this command does (documentation only)",
			},
			"cwd": map[string]any{
				"type":        "string",
				"description": "Working directory relative to the workspace (e.g. 'my-project'). Absolute paths and '..' escapes are rejected. Defaults to the workspace root.",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Wall-clock timeout in seconds, applied identically in the foreground and background. Default 300. Must be between 1 and 3600.",
			},
			"run_in_background": map[string]any{
				"type":        "boolean",
				"description": "Run the command in the background and return immediately with a session_id (default false).",
			},
			"persistent": map[string]any{
				"type":        "boolean",
				"description": "Reserved for a future long-lived session mode. Only meaningful together with run_in_background=true (default false).",
			},
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"run", "poll", "read", "kill"},
				"description": "run (default): execute command. poll/read/kill: manage a background session_id.",
			},
			"session_id": map[string]any{
				"type":        "string",
				"description": "Background session id (required for action=poll/read/kill)",
			},
		},
	}
}

// --- Execute / AsyncExecutor ------------------------------------------------

var _ AsyncExecutor = (*ExecTool)(nil)

func (t *ExecTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	return t.execute(ctx, args, nil)
}

// ExecuteAsync implements AsyncExecutor. cb is only ever invoked for a
// run_in_background=true call: it is captured by the background-completion
// goroutine and fired exactly once — on natural completion, failure, timeout,
// or kill (FR-B9) — regardless of how long after this call returns that
// happens. Every other action (foreground run, poll, read, kill) never
// invokes cb; it is simply unused for those calls.
func (t *ExecTool) ExecuteAsync(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	return t.execute(ctx, args, cb)
}

func (t *ExecTool) execute(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	action, _ := args["action"].(string)
	if action == "" {
		action = "run"
	}

	switch action {
	case "run":
		return t.executeRun(ctx, args, cb)
	case "poll":
		return t.executePoll(ctx, args)
	case "read":
		return t.executeRead(ctx, args)
	case "kill":
		return t.executeKill(ctx, args)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s", action))
	}
}

func getBoolArg(args map[string]any, key string) bool {
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

const (
	defaultTimeoutSeconds = int32(300)
	minTimeoutSeconds     = int32(1)
	maxTimeoutSeconds     = int32(3600)
)

// resolveTimeoutSeconds implements FR-B15: a timeout_seconds value outside
// [1, 3600] is REJECTED as invalid input, never clamped or silently ignored.
// Absent/nil defaults to 300 (the documented default).
func resolveTimeoutSeconds(args map[string]any) (int32, error) {
	raw, ok := args["timeout_seconds"]
	if !ok || raw == nil {
		return defaultTimeoutSeconds, nil
	}
	var v int64
	switch n := raw.(type) {
	case float64:
		v = int64(n)
	case int:
		v = int64(n)
	case int32:
		v = int64(n)
	case int64:
		v = n
	default:
		return 0, fmt.Errorf("timeout_seconds must be a number")
	}
	if v < int64(minTimeoutSeconds) || v > int64(maxTimeoutSeconds) {
		return 0, fmt.Errorf(
			"timeout_seconds must be between %d and %d (got %d)",
			minTimeoutSeconds, maxTimeoutSeconds, v,
		)
	}
	return int32(v), nil
}

func (t *ExecTool) executeRun(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	command, ok := args["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return ErrorResult("command is required and must be a non-empty string")
	}
	command = strings.TrimSpace(command)

	runInBackground := getBoolArg(args, "run_in_background")
	persistent := getBoolArg(args, "persistent")
	if persistent && !runInBackground {
		return ErrorResult("persistent requires run_in_background to also be true")
	}

	timeoutSeconds, err := resolveTimeoutSeconds(args)
	if err != nil {
		return ErrorResult(err.Error())
	}

	// Resolve the base working directory for this call. When the turn
	// re-roots to a workspace dir (the agent is a member of that Workspace's
	// CoreTeam), the base becomes workspaces/<id>/ for this turn only;
	// otherwise it is the fixed agent dir. All cwd guards below are evaluated
	// relative to baseDir.
	baseDir := t.workingDir
	if d := TurnWorkspaceDir(ctx); d != "" {
		baseDir = d
	}

	cwd, cwdErr := t.resolveCWD(ctx, args, baseDir)
	if cwdErr != nil {
		t.emitAudit(ctx, command, "", audit.DecisionDeny)
		return ErrorResult(cwdErr.Error())
	}

	// FR-B4: hardcoded deny-pattern baseline (unconditional) + the opt-in
	// operator-extensible layer + the legacy command-text absolute-path scan.
	if guardErr := t.guardCommand(ctx, command, cwd); guardErr != "" {
		t.emitAudit(ctx, command, cwd, audit.DecisionDeny)
		return ErrorResult(guardErr)
	}

	// FR-B5: binary allowlist (SEC-05), applied uniformly to foreground and
	// background — this check runs BEFORE the foreground/background branch
	// below, so both paths are covered by the same call site.
	if t.policyAuditor != nil {
		agentID := ToolAgentID(ctx)
		decision := t.policyAuditor.EvaluateExec(agentID, command)
		if !decision.Allowed {
			t.emitAudit(ctx, command, cwd, audit.DecisionDeny)
			return ErrorResult(fmt.Sprintf("Command blocked by exec allowlist: %s", decision.PolicyRule))
		}
	}

	// FR-B7: audit-log write failure fails CLOSED.
	if auditResult := t.emitAuditOrDeny(ctx, command, cwd); auditResult != nil {
		return auditResult
	}

	// FR-B6: route every non-god-mode invocation through sandbox.ResolveLimits.
	// P3 (ADR-046): EffectiveFSPolicy feeds the per-child Landlock ruleset
	// here — sandbox.ResolveLimits is where the fresh, per-call ruleset
	// (FR-023: deny -> working dir + libs + /tmp; ask -> +approved; allow ->
	// per the P3 spike's carve-out decision) will be computed and applied via
	// the non-latched apply path, once the P3 kernel-sandbox rearchitecture
	// lands. Today this remains the pre-ADR-046 god-mode/no-god-mode split.
	lim, limErr := sandbox.ResolveLimits(t.godMode, baseDir, t.proxy, timeoutSeconds)
	if limErr != nil {
		return ErrorResult(fmt.Sprintf("sandbox limits error: %v", limErr))
	}
	lim.WorkspaceDir = cwd

	// ADR-063 FR-3.5: carry THIS TURN's filesystem policy to the kernel, so the
	// child bash spawns is confined the same way the app-layer path resolver
	// confines this same turn's read_file/write_file.
	//
	// Without this the child inherited the BOOT profile, which grants
	// $OMNIPUS_HOME as one tree — so `bash` from any agent could read and write
	// every other agent's home and every workspace record, while the app layer
	// denied exactly those paths. That divergence was demonstrated against real
	// children, not inferred.
	//
	// God mode is excluded deliberately: it is an explicit operator opt-out of
	// confinement, and runForeground routes it to runUnconstrained anyway.
	if !t.godMode {
		kernelPolicy, kpErr := t.turnKernelPolicy(ctx)
		if kpErr != nil {
			t.emitAudit(ctx, command, cwd, audit.DecisionDeny)
			return ErrorResult(fmt.Sprintf("sandbox policy error: %v", kpErr))
		}
		lim.KernelPolicy = kernelPolicy
	}

	if runInBackground {
		ownerSessionID := ToolTranscriptSessionID(ctx)
		return t.runBackground(ctx, command, cwd, timeoutSeconds, lim, ownerSessionID, cb)
	}
	return t.runForeground(ctx, command, lim, timeoutSeconds)
}

// turnKernelPolicy derives the per-turn kernel policy for this bash call from
// the SAME authored fspolicy.FSPolicy that every path-taking tool resolves
// (ResolveTurnFSPolicy), so the kernel and the app layer are answering "what
// may this turn touch" from one input rather than two.
//
// Returns (nil, nil) when no kernel policy is in force — sandbox off, or a
// platform that degraded to application-level enforcement. The spawn then uses
// whatever the boot profile is, exactly as before.
//
// Returns an error, rather than a nil policy, when a policy IS in force but
// could not be derived. Falling back on failure would hand the child the boot
// profile, which is the WIDER of the two — a derivation bug would then quietly
// restore the very cross-agent reach this exists to remove.
func (t *ExecTool) turnKernelPolicy(ctx context.Context) (*sandbox.SandboxPolicy, error) {
	if !sandbox.TurnPolicyBaseInstalled() {
		return nil, nil
	}
	// restrict is t.restrictToWorkspace, not the hardcoded true that cwd
	// resolution uses: cwd confinement is a separate, deliberately stricter
	// decision (see resolveCWD's note 3), while this is the turn's real posture.
	// Scope does not change the derived rules anyway — post-ADR-062 it governs
	// writes through the work dir, which is identical either way (FR-2.5).
	authored, err := ResolveTurnFSPolicy(ctx, t.workingDir, t.restrictToWorkspace)
	if err != nil {
		return nil, fmt.Errorf("resolve turn filesystem policy: %w", err)
	}
	return sandbox.KernelPolicyForTurn(authored)
}

// --- cwd resolution (FR-B2/FR-B13) -----------------------------------------

// resolveCWD resolves the optional cwd argument to an absolute path under
// baseDir. Absolute paths are rejected outright (no escape hatch); relative
// paths are resolved via ResolvePath (resolvepath.go, ADR-046's mandatory
// chokepoint), which also follows symlinks and anchors confinement on the
// realpath before the containment check — a workspace-internal symlink
// pointing outside the workspace is rejected the same way an absolute path
// is (FR-B13). Empty cwd defaults to baseDir.
//
// SEC-05/FR-B2 threat model: t.allowedPathPatterns is the operator-configured
// cross-tool allowlist that read_file/write_file/list_directory legitimately
// honor (e.g. the shared media/attachments temp dir — see
// buildAllowReadPatterns/mediaTempDirPattern in pkg/agent/instance.go). That
// directory is shared across ALL agents and ALL sessions, so it is NOT a safe
// place for bash to chdir into. bash's cwd must never consult that allowlist
// at all (mirrors the pre-consolidation workspace_shell.go, which rejected
// filepath.IsAbs(rawCWD) outright and passed nil — not the real patterns —
// into the legacy validator). Concretely:
//  1. Any absolute path is rejected unconditionally, with no allowlist-based
//     exception, before ResolvePath is ever called.
//  2. Below calls the PLAIN ResolvePath (never ResolvePathAllowingPatterns),
//     which has no patterns parameter at all — so a relative cwd that
//     traverses (e.g. "../../media") out to the shared media dir cannot
//     short-circuit past the containment check either; it is judged purely
//     on the effective working directory's confinement, same as any other
//     outside-workspace target.
//  3. The scope is forced to fspolicy.FSScopeConfined for this call
//     regardless of the exec tool's own restrictToWorkspace setting —
//     matching the pre-ADR-046 behavior, which always passed
//     restrict=true (hardcoded) into the legacy validator for cwd
//     resolution specifically, never t.restrictToWorkspace. bash's OWN
//     host-filesystem reach (when unrestricted) is governed entirely by
//     sandbox.ResolveLimits/god-mode below, not by widening cwd's escape
//     hatch.
func (t *ExecTool) resolveCWD(ctx context.Context, args map[string]any, baseDir string) (string, error) {
	rawCWD, _ := args["cwd"].(string)
	rawCWD = strings.TrimSpace(rawCWD)

	if filepath.IsAbs(rawCWD) {
		return "", fmt.Errorf("path escapes workspace: absolute path not allowed (use a relative path)")
	}

	policyForCWD, err := fspolicy.EffectiveFSPolicy(
		ctx, baseDir, "", true, config.OmnipusHomeDir(), ToolAgentID(ctx), ToolWorkspaceID(ctx),
	)
	if err != nil {
		return "", fmt.Errorf("workspace dir not resolvable: %w", err)
	}

	handle, err := ResolvePath(ctx, policyForCWD, "bash", "", FSOpExec, rawCWD)
	if err != nil {
		return "", fmt.Errorf("path escapes workspace: %w", err)
	}
	defer handle.Close()

	realPath, err := handle.RealPath()
	if err != nil {
		return "", fmt.Errorf("failed to resolve cwd: %w", err)
	}
	return realPath, nil
}

// --- deny-pattern guard ------------------------------------------------------

// guardCommand applies, in order: (1) the hardcoded baseline (FR-B4,
// unconditional) — both its regex half (defaultDenyPatterns) and its structural
// half (substitutionGuard) — (2) the opt-in operator-extensible layer,
// and (3) a legacy defense-in-depth scan for absolute paths referenced in the
// command TEXT (independent of the cwd parameter guard above), gated on
// restrictToWorkspace exactly as the pre-consolidation exec tool did.
//
// ADR-068 (2026-08-23) named step 3 the system's THIRD file-access rule layer
// and corrected the record about what it does: it had been enforcing the
// pre-ADR-062 CONFINED model for reads as well as writes, which is why
// `bash cat ~/notes.txt` was refused while ADR-063 documented it as allowed.
// Under the founder's ruling (ADR-068 §2.1 option A) this layer now
// distinguishes the two: a path reference outside the working directory that is
// PROVABLY a read is allowed; anything else — every write, and every reference
// this scanner cannot prove is a read — still requires a workspace mount,
// exactly as before. The proof lives in pathUseClassifier below, and it is
// deliberately allowlist-shaped: see its doc comment for why "not provably a
// read" must mean "treat as a write", never the other way round.
func (t *ExecTool) guardCommand(ctx context.Context, command, cwd string) string {
	if msg := applyDenyPatterns(command, t.denyPatterns, nil); msg != "" {
		return msg
	}
	// FR-B4, structural half: command substitutions are judged by what they
	// run and where they sit, not by their mere presence. Unconditional and
	// not disableable, exactly like the regex baseline above.
	if msg := substitutionGuard(command); msg != "" {
		return msg
	}
	if t.enableOperatorDenyPatterns {
		if msg := applyDenyPatterns(command, t.operatorDenyPatterns, nil); msg != "" {
			return msg
		}
	}

	if !t.restrictToWorkspace {
		return ""
	}

	cmd := strings.TrimSpace(command)
	if strings.Contains(cmd, "..\\") || strings.Contains(cmd, "../") {
		return "Command blocked by safety guard (path traversal detected)"
	}

	cwdPath, err := filepath.Abs(cwd)
	if err != nil {
		return "cannot resolve working directory"
	}

	// ADR-063 alignment: the command-TEXT scan below must honor the SAME
	// workspace mounts that the kernel policy (turnKernelPolicy ->
	// ResolveTurnFSPolicy) and the app-layer path resolver (matchedAllowedRoot)
	// already grant write to. Without this, an absolute path under a mounted
	// folder is rejected here with "no mount covers it" before the kernel —
	// which WOULD allow it — ever runs, so the guard's own message described a
	// mount check the function never performed. Resolve mount roots from that
	// one authoritative source. On resolve error, fall back to NO mount
	// exemption: the guard then stays exactly as strict as before, and (unlike
	// the kernel path, which fails closed by ABORTING the spawn) it never aborts
	// a command over a transient mount-resolution hiccup.
	//
	// The KERNEL sandbox remains the authoritative boundary, not this text scan.
	// This guard is deliberately COARSER than the kernel for the mount exemption:
	// it grants read+write+exec under a mount root where the kernel grants only
	// read+write; it matches lexically (filepath.Abs) where the kernel enforces
	// on realpaths (a symlink inside a mount pointing out is passed here, denied
	// by the kernel); and it does NOT subtract the per-turn secret set, so a
	// mount that overlaps $OMNIPUS_HOME could name a secret path here that the
	// kernel still denies. On Linux 5.13+ the kernel blocks every one of those
	// deltas. On a fallback platform (no Landlock) bash is governed by the boot
	// profile, which already grants $OMNIPUS_HOME broadly — this scan was never a
	// secret boundary there. (Subtracting KernelDeniedPathsFor here to close the
	// gap needs a home/work-dir path-form match this call site can't cheaply
	// guarantee; it is tracked as follow-up, not attempted inline.)
	var mountRoots []string

	// ADR-068 read exemption vs the turn's secret set (code review, 2026-08-23,
	// second pass). Opening reads outside the working directory must NOT open
	// the secret set with them. secretGuardPatterns (the literal text backstop)
	// covers only fspolicy.SecretEntriesAlways — config.json, master.key,
	// cli.token, entities — and deliberately NOT the PER-TURN roots `agents/`
	// and `workspaces/`. Those were out of reach here only as a side effect of
	// blocking every outside-WorkDir path, so granting reads removed the sole
	// app-layer barrier: another agent's SOUL.md and another workspace's work
	// tree became readable from bash wherever the kernel layer is absent or off
	// (a non-Landlock host, or sandbox.mode=off) — the exact regression
	// pkg/sandbox/derive_from_fspolicy.go:160 records as previously fixed.
	//
	// IsCarveOut is the right primitive here, and the two obvious alternatives
	// are both wrong:
	//   - DeniedPathsFor re-admits a whole per-turn ROOT, so during a workspace
	//     turn every OTHER workspace is re-admitted with the caller's own. The
	//     comment at derive_from_fspolicy.go:160 warns against exactly this.
	//   - KernelDeniedPathsFor is correct but enumerates the sibling set from
	//     disk on every call — thousands of entries, per bash command, matched
	//     lexically. Wrong shape for a per-command text guard.
	// IsCarveOut answers per PATH with no listing, applies the own-tree
	// exception per-root, and resolves by filesystem identity rather than by
	// string prefix — which is also what makes this immune to the
	// lexical-vs-realpath mismatch that made the first attempt at this fix
	// inert on macOS (/var vs /private/var).
	//
	// It is ALSO the same primitive the app-layer file tools call, so `bash cat
	// X` and `read_file X` now agree about the secret set — the file-access
	// spec's R-4 ("a rule must not depend on which tool asks"), which ADR-068
	// §1.1 found was never true on the bash path.
	//
	// FAIL CLOSED: when the policy cannot be resolved there is no way to tell
	// the caller's own tree from anyone else's, so the read exemption is
	// withheld entirely (readPolicyOK=false) and the guard behaves exactly as
	// it did before ADR-068. Never widen on an error path.
	var (
		turnPolicy   fspolicy.FSPolicy
		readPolicyOK bool
	)
	if authored, ferr := ResolveTurnFSPolicy(ctx, t.workingDir, t.restrictToWorkspace); ferr == nil {
		mountRoots = authored.AllowedRoots
		turnPolicy = authored
		readPolicyOK = true
	} else {
		// The fail-closed direction is right, but silence is not: with no
		// policy the agent loses BOTH its mounts and the ADR-068 read
		// exemption, so an operator sees writes to a folder they demonstrably
		// mounted refused with "request a mount for that folder" — advice for
		// a thing they already did. Log it so the degraded mode is
		// diagnosable instead of looking like the mount never existed
		// (code review round 3).
		logger.WarnCF("tools", "bash guard: turn filesystem policy unresolved; mounts and the ADR-068 read exemption are both withheld for this command",
			map[string]any{"working_dir": t.workingDir, "error": ferr.Error()})
	}

	// Web URL schemes whose path components (starting with //) should be
	// exempt from workspace sandbox checks. file: is intentionally excluded
	// so file:// URIs are still validated against the workspace boundary.
	webSchemes := []string{"http:", "https:", "ftp:", "ftps:", "sftp:", "ssh:", "git:"}

	// ADR-068 §1: read/write classification for this command. Built ONCE — it
	// scans the command for quoting once and answers per-candidate questions
	// from that single pass. A command it cannot parse confidently classifies
	// every candidate as a write, i.e. behaves exactly like the pre-ADR-068
	// guard.
	classifier := newPathUseClassifier(cmd)

	// Group 1 is the path itself; the full match also consumes the leading
	// boundary character, so indices 2:4 (not 0:2) are what we want. Reading
	// the whole match here would re-introduce the leading space/operator into
	// every candidate and break both filepath.Abs and the safePaths lookup.
	matchIndices := absolutePathPattern.FindAllStringSubmatchIndex(cmd, -1)
	for _, loc := range matchIndices {
		start, end := loc[2], loc[3]
		if start < 0 || end < 0 {
			continue
		}
		raw := cmd[start:end]

		// EXPANSION-DERIVED CANDIDATE (code review round 3, 2026-08-24).
		//
		// absolutePathPattern treats `~` and a token-start `$VAR` as BOUNDARY
		// characters and captures only the suffix after them, so
		// `$HOME/.omnipus/agents/other/SOUL.md` reaches this loop as the
		// candidate `/.omnipus/agents/other/SOUL.md` — a path that names no
		// real file. Judging that phantom is meaningless: it is outside the
		// work dir, no mount covers it, and IsCarveOut cannot match it because
		// the carve-out roots are anchored at the real $OMNIPUS_HOME. The shell
		// then expands the variable and touches the REAL file.
		//
		// Measured before this guard: `cat $HOME/.omnipus/agents/victim/SOUL.md`
		// and the `~` spelling were both ALLOWED while the literal absolute
		// path was correctly blocked — the ADR-068 read exemption reachable
		// through a different spelling of the same file.
		//
		// The boundary the regex consumed sits between loc[0] and loc[2], so
		// the fact is available here and nowhere downstream. A candidate built
		// this way is UNRESOLVABLE by a text scan: fail closed. It earns
		// neither the read exemption nor the inside-the-work-dir pass, because
		// the suffix can be made to land inside the work dir while the real
		// target is anywhere on the host (`printf x > $HOME<abs-cwd>/pwned`).
		expansionDerived := false
		if loc[0] >= 0 && loc[0] < start {
			if prefix, resolved, isExpansion := expandCandidatePrefix(cmd[loc[0]:start]); isExpansion {
				if resolved {
					// Judge the REAL path. `cat ~/notes.txt` stays an ordinary
					// outside-the-work-dir read (allowed), while
					// `cat ~/.omnipus/agents/other/SOUL.md` now resolves onto
					// the carve-out and is refused — the same verdict its
					// literal absolute spelling already got.
					raw = filepath.Join(prefix, raw)
				} else {
					expansionDerived = true
				}
			}
		}

		// The classification is a property of WHERE the candidate sits in the
		// command text, so it must be computed here (byte offsets exist only at
		// this call site) and threaded down into checkPathSegment, which sees
		// the path string alone.
		readOnly := classifier.isReadOnly(start) && !expansionDerived

		// Colon-joined path list (PATH= assignments, -I a:b-style flags):
		// each `:`-separated segment is checked independently against the
		// workspace boundary (the same check applied to a bare candidate
		// below), rather than the whole list being skipped wholesale. See
		// colonPathListPattern's doc comment for why this shape is
		// recognized narrowly, and why an unconditional skip here would
		// have let a colon-joined list smuggle an out-of-workspace segment
		// past the guard entirely.
		//
		// Every segment of the list inherits the SAME read/write
		// classification: the list is one token in one argument position, so
		// whatever the command does with it, it does with all of it.
		if strings.Contains(raw, ":") && colonPathListPattern.MatchString(raw) {
			for _, seg := range strings.Split(raw, ":") {
				if msg := t.checkPathSegment(seg, cwdPath, mountRoots, turnPolicy, readPolicyOK, readOnly, expansionDerived); msg != "" {
					return msg
				}
			}
			continue
		}

		if strings.HasPrefix(raw, "//") && start > 0 {
			before := cmd[:start]
			isWebURL := false
			for _, scheme := range webSchemes {
				if strings.HasSuffix(before, scheme) {
					isWebURL = true
					break
				}
			}
			if isWebURL {
				continue
			}
		}

		if msg := t.checkPathSegment(raw, cwdPath, mountRoots, turnPolicy, readPolicyOK, readOnly, expansionDerived); msg != "" {
			return msg
		}
	}

	return ""
}

// checkPathSegment evaluates a single absolute-path candidate — either a
// bare candidate from the main scan loop, or one `:`-separated segment of a
// colon-joined path list — against the safePaths exemption, the
// operator-configured allowlist, the workspace-containment boundary, and the
// turn's workspace mounts (mountRoots, from ResolveTurnFSPolicy.AllowedRoots).
// Returns "" when the segment is allowed, or a rejection message otherwise.
//
// readOnly is the caller's ADR-068 classification of this candidate: true only
// when guardCommand's pathUseClassifier could PROVE, from the command text,
// that the reference is a read. It changes exactly one thing — the final
// out-of-working-directory rejection. Everything above that point (safePaths,
// the operator allowlist, containment, mounts) is identical for reads and
// writes, so no existing exemption widens or narrows because of this parameter.
func (t *ExecTool) checkPathSegment(raw, cwdPath string, mountRoots []string, turnPolicy fspolicy.FSPolicy, readPolicyOK, readOnly, expansionDerived bool) string {
	p, err := filepath.Abs(raw)
	if err != nil {
		return "Command blocked by safety guard (cannot resolve path)"
	}

	// An UNRESOLVABLE expansion (see guardCommand's scan loop): the candidate is
	// only the suffix after a `$VAR` whose value this process cannot know, so
	// every check below would reason about a path that names no real file.
	// `~`, `$HOME` and `$OMNIPUS_HOME` are resolved at the call site instead and
	// never arrive here; anything else is refused. Fail closed.
	if expansionDerived {
		return fmt.Sprintf(
			"Command blocked by safety guard (unresolvable path): %q follows a shell variable whose value "+
				"the guard cannot know, so it cannot tell which file the command will actually open. "+
				"Rule: bash workspace path guard (RestrictToWorkspace). "+
				"Fix: write the path literally, or use ~ / $HOME / $OMNIPUS_HOME, which the guard does resolve.", raw)
	}

	if safePaths[p] {
		return ""
	}
	if isAllowedPath(p, t.allowedPathPatterns) {
		return ""
	}

	rel, err := filepath.Rel(cwdPath, p)
	if err != nil {
		return "Command blocked by safety guard (cannot resolve relative path)"
	}
	if strings.HasPrefix(rel, "..") {
		// Outside the working dir — but a workspace mount may cover it. The
		// kernel policy and the app-layer path resolver both grant paths under
		// an AllowedRoots mount (matchedAllowedRoot); the guard must not reject a
		// write the kernel would permit. This is the "and no mount covers it"
		// branch finally consulting the mounts its message has always named.
		if _, ok := matchedAllowedRoot(p, mountRoots); ok {
			return ""
		}

		// ADR-068 §2.3: match the mount roots on the RESOLVED path too.
		// Mount.HostPath is realpath-resolved when the mount is created
		// (pkg/workspace/mount.go:37), while p above is only lexically absolute
		// (filepath.Abs follows no symlinks). On macOS the two forms disagree
		// constantly — /tmp is a symlink to /private/tmp, /var to /private/var —
		// so an operator could approve a mount and watch every write to it still
		// be refused with "no mount covers it", the mount plainly existing. That
		// is the single worst failure mode this guard has: a denial that
		// contradicts the operator's own explicit grant.
		//
		// resolvePathAgainstExistingAncestor (filesystem.go) is the same
		// deepest-existing-ancestor resolver resolveAbsPath uses, so a
		// not-yet-created file under a mounted directory still resolves through
		// its existing parent. NON-FATAL by design, matching the mount-root
		// resolution above: a resolve error (nothing on the path exists, an
		// unreadable ancestor, a symlink loop) leaves the candidate judged on
		// its lexical form alone — i.e. exactly the pre-ADR-068 behaviour — and
		// never aborts the command. This can only ADD a mount match, never
		// remove one, because the lexical match above has already been tried.
		if resolved, rerr := resolvePathAgainstExistingAncestor(p); rerr == nil && resolved != p {
			if _, ok := matchedAllowedRoot(resolved, mountRoots); ok {
				return ""
			}
		}

		// ADR-068 §1/§2.1 option A: reads outside the working directory are
		// allowed; writes outside it still require a mount. readOnly is true
		// only when the command text PROVES the reference is a read (see
		// pathUseClassifier) — an unproven reference arrives here as a write and
		// falls through to the rejection below, which is the pre-ADR-068
		// behaviour unchanged.
		if readOnly && readPolicyOK && !t.inTurnSecretSet(p, turnPolicy) {
			return ""
		}

		// ADR-068 §6: an unexplained denial is what drives operators to switch
		// off the entire boundary rather than the one rule in their way, so the
		// message names the rule, the field behind it, and both ways to change
		// it. `sandbox.workspace_path_guard` is the operator-facing key that
		// resolves into AgentDefaults.RestrictToWorkspace (pkg/config/sandbox.go);
		// the env var is the recovery path that outranks it. Neither is the
		// kernel sandbox (`sandbox.mode`) — the confusion between the two is
		// precisely what UAT defect 002 reported.
		return fmt.Sprintf(
			"Command blocked by safety guard (path outside working dir): %q is outside the effective working directory %q and no mount covers it. "+
				"Rule: bash workspace path guard (RestrictToWorkspace) — a WRITE outside the working directory needs an approved workspace mount; reads outside it are allowed (ADR-068). "+
				"Fixes: request a mount for that folder (request_mount), or have an operator set sandbox.workspace_path_guard=false (env OMNIPUS_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE=false). "+
				"This is NOT the kernel sandbox setting (sandbox.mode).",
			p, cwdPath)
	}
	return ""
}

// --- read/write classification (ADR-068 §1, option A) ------------------------

// readOnlyShellCommands is the allowlist of command names whose path arguments
// are provably only READ.
//
// MEMBERSHIP CRITERION, and it is deliberately severe: a name belongs here only
// if the binary has NO flag, in ANY common implementation, that writes to a
// filesystem path named on its own command line. If a reviewer has to think
// about it, the answer is no.
//
// Why an allowlist and not a blocklist of write markers. Deciding read-vs-write
// from shell TEXT is, in general, undecidable — the head can be an interpreter
// whose file access is invisible (`python3 -c`, `sh stage2.sh`), the same binary
// reads or writes depending on a flag (`sed` vs `sed -i`, `sort` vs `sort -o`,
// `xxd` vs `xxd -r`), and expansions this scanner does not model change which
// token is even a path. ADR-068 §5 criticises exactly that kind of pattern
// matching, so this classifier must never grow a "looks like a read" heuristic.
// The only safe shape is: prove a read, or call it a write.
//
// DELIBERATE EXCLUSIONS, recorded so nobody re-adds them as an "obvious" omission:
//   - sed, awk, gawk, perl, ruby — in-place flags (`-i`, `-i inplace`).
//   - sort — `-o FILE`. tee, dd, cp, mv, ln, install, rsync, truncate, touch,
//     mkdir, tar, unzip — writers, or writers under a destination flag.
//   - find — `-exec` / `-delete`. less, more, vi, view — shell escapes.
//   - xxd, od-style dumpers with a reverse mode — `xxd -r in out` writes.
//   - file — `file -C -m FILE` compiles a magic file and writes FILE.mgc, so
//     it fails this list's own membership criterion (code review, 2026-08-23).
//   - every interpreter (python*, node, sh, bash, ruby, php…), plus git, make,
//     gcc, curl, wget — arbitrary file access by construction.
//   - echo, printf — they never OPEN the path at all, so there is no read to
//     grant; leaving them out also keeps `echo x,/etc/shadow` blocked.
//
// The known, accepted cost: `gcc -I/usr/include` and `tar -C/etc` are genuine
// reads that stay blocked. Inferring "this flag names a read-only directory" is
// the fragile inference this design refuses to make.
var readOnlyShellCommands = map[string]bool{
	"cat": true, "head": true, "tail": true, "nl": true, "wc": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true,
	"ls": true, "stat": true, "du": true,
	"diff": true, "cmp": true, "strings": true,
	"readlink": true, "realpath": true, "basename": true, "dirname": true,
	"md5sum": true, "sha1sum": true, "sha256sum": true, "sha512sum": true,
	"shasum": true, "cksum": true,
}

// pathUseClassifier answers one question about one command: is the
// absolute-path candidate starting at byte offset N provably a READ?
//
// It fails CLOSED at every step. "false" is returned for anything it cannot
// prove, which reproduces the pre-ADR-068 guard exactly, so the worst outcome of
// a gap in this scanner is a command that stays blocked — never a write that
// slips out of the working directory unnoticed.
//
// A candidate is read-only only when ALL of these hold:
//
//  1. The whole command is parseable by this scanner: quotes balance, and it
//     contains no command substitution (`$( )`, backticks) or process
//     substitution (`<( )`, `>( )`). Those shapes are already judged by
//     substitutionGuard and the deny patterns, but they are re-tested here so
//     this classifier stays honest on its own terms if either layer is ever
//     relaxed — an unresolvable expansion means we do not know what runs.
//  2. The command segment containing the candidate (split at unquoted
//     `| ; & newline`) has a LITERAL head that is in readOnlyShellCommands.
//     Quote-aware splitting is load-bearing: without it `tee "a;cat" /etc/x`
//     splits into `tee "a` and `cat" /etc/x`, the second segment's head reads
//     as `cat`, and a genuine write to /etc/x would be classified as a read.
//  3. The segment contains no brace character. This scanner does not model
//     brace expansion, and bash rewrites the words before parsing
//     (`{cat,/etc/passwd}` runs `cat /etc/passwd`), so any `{` or `}` means the
//     command we are judging is not the command that will run.
//  4. The candidate is not in the segment's FIRST word. A path in command
//     position is an EXEC, not a read — `echo hi;/etc/shadow` and
//     `cat[/etc/shadow]` both land here. ADR-068 rules on reads only; exec
//     stays where it was.
//  5. The candidate's word is not the target of an output redirect (`>`, `>>`,
//     `2>`, `&>`, `>&file`, `<>`). The check scans back from the start of the
//     WORD, not from the path, so a redirect onto an expansion-prefixed target
//     (`> ~/evil`, `> $HOME/evil`, whose candidate text starts mid-word) is
//     still seen as a redirect.
//  6. Every output redirect in the segment names a LITERAL target. A target
//     containing an expansion or a glob (`> $OUT`, `> out*.txt`) is a write to
//     a path this scan cannot see, so the whole segment becomes unclassifiable
//     rather than let a read be granted beside an invisible write.
type pathUseClassifier struct {
	cmd string
	// mask[i] is true when byte i is quoting syntax, escaped, or inside quotes
	// — i.e. NOT an operator the shell would act on.
	mask []bool
	// classifiable is false when rule 1 fails, which makes every candidate in
	// the command a write.
	classifiable bool
}

func newPathUseClassifier(cmd string) pathUseClassifier {
	mask, balanced := shellQuoteMask(cmd)
	return pathUseClassifier{
		cmd:  cmd,
		mask: mask,
		classifiable: balanced &&
			!strings.Contains(cmd, "$(") &&
			!strings.Contains(cmd, "`") &&
			!strings.Contains(cmd, "<(") &&
			!strings.Contains(cmd, ">("),
	}
}

// isReadOnly reports whether the absolute-path candidate beginning at byte
// offset start is provably a read. See the type's doc comment for the rules.
func (c pathUseClassifier) isReadOnly(start int) bool {
	if !c.classifiable || start < 0 || start >= len(c.cmd) {
		return false
	}

	segStart, segEnd := c.segmentBounds(start)
	seg := c.cmd[segStart:segEnd]

	// Rule 3: unmodelled brace expansion.
	if strings.ContainsAny(seg, "{}") {
		return false
	}

	// Rule 2: literal, allowlisted head — named EXACTLY, with no directory
	// prefix and no case folding.
	//
	// shellCommandHead normalises for the DENY path: it lowercases and strips a
	// directory prefix so `/bin/rm` is judged as `rm`. That is right for a deny
	// list and a bypass for an allow list — `./cat`, `bin/cat` and `CAT` all
	// normalise onto the allowlisted head `cat`, and an agent may freely write
	// an executable named `cat` into its own working directory. It would then
	// be classified as a proven read while being an arbitrary program with the
	// account's full write access. shellCommandHeadDetailed reports whether
	// normalisation changed the token; a changed token is refused here, which
	// costs only the absolute spellings (`/bin/cat f`) and keeps the doctrine
	// intact: prove a read, or call it a write.
	head, headIsExpansion, headNormalised := shellCommandHeadDetailed(seg)
	if headIsExpansion || headNormalised || !readOnlyShellCommands[head] {
		return false
	}

	word := c.wordStart(start, segStart)

	// Rule 4: command position is exec, not read.
	if word == c.firstWordStart(segStart, segEnd) {
		return false
	}
	// Rule 5: this word is a redirect target.
	if c.precededByOutputRedirect(word, segStart) {
		return false
	}
	// Rule 6: some other redirect in this segment writes somewhere we cannot see.
	if !c.redirectTargetsAreLiteral(segStart, segEnd) {
		return false
	}
	return true
}

// segmentBounds returns the half-open byte range of the command segment
// containing idx, splitting at unquoted `| ; & newline` exactly as
// splitShellSegments does — but preserving offsets, which that helper discards.
func (c pathUseClassifier) segmentBounds(idx int) (int, int) {
	start := idx
	for start > 0 && !c.isSegmentSeparator(start-1) {
		start--
	}
	end := idx
	for end < len(c.cmd) && !c.isSegmentSeparator(end) {
		end++
	}
	return start, end
}

func (c pathUseClassifier) isSegmentSeparator(i int) bool {
	if c.mask[i] {
		return false
	}
	switch c.cmd[i] {
	case '|', ';', '\n', '\r':
		return true
	case '&':
		// `2>&1`, `<&-`: an `&` bound to a redirection operator on its left is
		// a file-descriptor duplication, not a command separator. Splitting
		// there would strand the redirect with an empty target and make the
		// ubiquitous `cmd 2>&1` shape unclassifiable for no reason.
		if i > 0 && !c.mask[i-1] && (c.cmd[i-1] == '>' || c.cmd[i-1] == '<') {
			return false
		}
		return true
	}
	return false
}

// wordStart returns the offset at which the shell word containing idx begins,
// never running past segStart. Quoted word-break characters do not break a
// word, which is what keeps `cat "/etc/my file"` one argument.
func (c pathUseClassifier) wordStart(idx, segStart int) int {
	i := idx
	for i > segStart && (c.mask[i-1] || !isShellWordBreak(c.cmd[i-1])) {
		i--
	}
	return i
}

// firstWordStart returns the offset of the segment's first word — the command
// position.
func (c pathUseClassifier) firstWordStart(segStart, segEnd int) int {
	i := segStart
	for i < segEnd && !c.mask[i] && (c.cmd[i] == ' ' || c.cmd[i] == '\t') {
		i++
	}
	return i
}

// precededByOutputRedirect reports whether the word beginning at word is the
// target of an output redirect. It covers `>`, `>>`, `N>`, `<>` (open for
// read-WRITE) and bash's `>&target` combined form; `<` alone is a read and is
// deliberately not matched.
func (c pathUseClassifier) precededByOutputRedirect(word, segStart int) bool {
	i := word - 1
	for i >= segStart && !c.mask[i] && (c.cmd[i] == ' ' || c.cmd[i] == '\t') {
		i--
	}
	if i < segStart || c.mask[i] {
		return false
	}
	if c.cmd[i] == '>' {
		return true
	}
	if c.cmd[i] == '&' && i-1 >= segStart && !c.mask[i-1] && c.cmd[i-1] == '>' {
		return true
	}
	return false
}

// redirectTargetsAreLiteral reports whether every output redirect in the
// segment names a target this text scan can actually see. A target built from
// an expansion or a glob (`> $OUT`, `> "$d"/x`, `> out*.txt`) is a write to an
// unknown path: the write itself is no more and no less guarded than it was
// before ADR-068 (this scan never resolved `$OUT`), but we refuse to hand out a
// READ exemption in the same segment as a write we cannot locate.
func (c pathUseClassifier) redirectTargetsAreLiteral(segStart, segEnd int) bool {
	for i := segStart; i < segEnd; i++ {
		if c.mask[i] || c.cmd[i] != '>' {
			continue
		}
		j := i + 1
		for j < segEnd && !c.mask[j] && c.cmd[j] == '>' { // `>>`
			j++
		}
		for j < segEnd && !c.mask[j] && (c.cmd[j] == ' ' || c.cmd[j] == '\t') {
			j++
		}
		tokStart := j
		// A leading `&` belongs to the redirection (`>&1`, `>&-`, `>&file`)
		// even though `&` is otherwise a word break, so step over it before the
		// word scan starts.
		if j < segEnd && !c.mask[j] && c.cmd[j] == '&' {
			j++
		}
		for j < segEnd && (c.mask[j] || !isShellWordBreak(c.cmd[j])) {
			j++
		}
		tok := c.cmd[tokStart:j]
		if tok == "" {
			// A redirect with no visible target (truncated command, or a
			// separator we split on). Unclassifiable.
			return false
		}
		if strings.HasPrefix(tok, "&") {
			// `>&1`, `>&-`: file-descriptor duplication, no file involved.
			// `>&file` (bash's combined redirect) IS a file, so only a pure
			// fd/close suffix short-circuits.
			if rest := tok[1:]; rest == "-" || isAllDigits(rest) {
				continue
			}
			tok = tok[1:]
		}
		if strings.ContainsAny(tok, "$`*?[") {
			return false
		}
		i = j - 1
	}
	return true
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// shellQuoteMask marks every byte of s that the shell would NOT treat as a bare
// operator: the quote characters themselves, everything inside them, a
// backslash and the byte it escapes. The second return value is false when the
// string ends inside a quote — an unbalanced command is one this scanner
// refuses to reason about at all.
//
// This exists because segment splitting and redirect detection must agree with
// bash about which `; | & >` characters are syntax and which are data. Getting
// that wrong in the permissive direction is a real bypass: `tee "a;cat" /etc/x`
// would otherwise present a segment whose head looks like `cat`.
func shellQuoteMask(s string) ([]bool, bool) {
	mask := make([]bool, len(s))
	var quote byte // 0 = unquoted, '\'' or '"' = inside that quote
	for i := 0; i < len(s); i++ {
		ch := s[i]
		// Backslash escapes everywhere except inside single quotes, where it is
		// a literal character.
		if ch == '\\' && quote != '\'' {
			mask[i] = true
			if i+1 < len(s) {
				mask[i+1] = true
				i++
			}
			continue
		}
		switch {
		case quote == 0 && (ch == '\'' || ch == '"'):
			quote = ch
			mask[i] = true
		case quote != 0 && ch == quote:
			quote = 0
			mask[i] = true
		default:
			mask[i] = quote != 0
		}
	}
	return mask, quote == 0
}

// --- audit helpers -----------------------------------------------------------

// emitAuditOrDeny writes an allow-decision audit.Entry before spawning. When
// the write fails and auditFailClosed is true, it returns a ToolResult that
// aborts execution (FR-B7). Returns nil to mean "continue".
func (t *ExecTool) emitAuditOrDeny(ctx context.Context, command, cwd string) *ToolResult {
	if t.auditLogger == nil {
		return nil
	}
	agentID := ToolAgentID(ctx)
	logErr := t.auditLogger.Log(&audit.Entry{
		Event:    audit.EventExec,
		Decision: audit.DecisionAllow,
		AgentID:  agentID,
		Tool:     t.Name(),
		Command:  command,
		Details: map[string]any{
			"cwd":      cwd,
			"god_mode": t.godMode,
		},
	})
	if logErr == nil {
		return nil
	}
	if t.auditFailClosed {
		slog.Error("bash: audit logger degraded; refusing to execute (audit_fail_closed=true)",
			"agent_id", agentID, "command", command, "error", logErr)
		return &ToolResult{
			IsError: true,
			ForLLM:  "audit log write failed; refusing to execute (audit_fail_closed=true)",
			ForUser: "bash requires audit logging; aborting",
		}
	}
	slog.Warn("bash: audit write failed", "agent_id", agentID, "error", logErr)
	return nil
}

// emitAudit writes a deny-decision audit.Entry. Used on paths that are
// already rejected — fail-closed semantics do not apply here (the command was
// never going to run). Nil logger is a no-op.
func (t *ExecTool) emitAudit(ctx context.Context, command, cwd, decision string) {
	if t.auditLogger == nil {
		return
	}
	agentID := ToolAgentID(ctx)
	if err := t.auditLogger.Log(&audit.Entry{
		Event:    audit.EventExec,
		Decision: decision,
		AgentID:  agentID,
		Tool:     t.Name(),
		Command:  command,
		Details: map[string]any{
			"cwd":      cwd,
			"god_mode": t.godMode,
		},
	}); err != nil {
		slog.Warn("bash: audit write failed", "agent_id", agentID, "error", err)
	}
}

// --- foreground execution ----------------------------------------------------

// buildShellArgv returns the platform-appropriate shell argv for a free-form
// command.
func buildShellArgv(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"powershell", "-NoProfile", "-NonInteractive", "-Command", command}
	}
	return []string{"sh", "-c", command}
}

// runForeground executes command synchronously and returns its output. Under
// god mode, hardening is skipped entirely (runUnconstrained); otherwise the
// command is routed through sandbox.Run, which applies Landlock/seccomp
// inheritance, resource limits, the egress proxy, and the wall-clock timeout
// uniformly (FR-B6). timeoutSeconds is passed explicitly (not read from
// lim.TimeoutSeconds) because sandbox.ResolveLimits returns the ZERO VALUE
// under god mode (lim.TimeoutSeconds would be 0) — workspace_shell's run()
// used the same explicit-parameter pattern for exactly this reason.
func (t *ExecTool) runForeground(
	ctx context.Context,
	command string,
	lim sandbox.Limits,
	timeoutSeconds int32,
) *ToolResult {
	argv := buildShellArgv(command)

	if t.godMode {
		return t.runUnconstrained(ctx, argv, lim.WorkspaceDir, timeoutSeconds)
	}

	res, err := sandbox.Run(ctx, argv, nil, lim)
	if err != nil {
		return ErrorResult(fmt.Sprintf("sandbox.Run failed: %v", err))
	}
	return foregroundResultFromSandbox(res, timeoutSeconds)
}

func foregroundResultFromSandbox(res sandbox.Result, timeoutSeconds int32) *ToolResult {
	output := string(res.Stdout)
	if len(res.Stderr) > 0 {
		if output != "" {
			output += "\n"
		}
		output += "STDERR:\n" + string(res.Stderr)
	}

	if res.TimedOut {
		msg := fmt.Sprintf("Command timed out after %d seconds", timeoutSeconds)
		if output != "" {
			msg += "\n\nPartial output before timeout:\n" + output
		}
		return &ToolResult{
			ForLLM:   msg,
			ForUser:  msg,
			IsError:  true,
			Err:      errors.New("command timeout"),
			TimedOut: true,
		}
	}

	// review r2 HIGH-1: capture the real exit code in the structured,
	// truncation-immune field FIRST (see ToolResult.ExitCode's doc comment) —
	// this is what judge.go's interpretBashResult reads to adjudicate a
	// machine-check criterion, never the text below. The human-readable
	// suffix is appended AFTER truncateOutput (not before, as this used to
	// do) so a large output can never truncate the AUTHORITATIVE suffix away
	// while leaving an earlier, worker-embedded fake suffix as the text's
	// last occurrence.
	exitCode := res.ExitCode
	output = truncateOutput(output, res.ExitCode)
	if res.ExitCode != 0 {
		output += fmt.Sprintf("\n\n[Command exited with code %d]", res.ExitCode)
		if res.ExitCode == -1 {
			output += " (killed by signal)"
		}
	}

	return &ToolResult{
		ForLLM:   output,
		ForUser:  output,
		IsError:  res.ExitCode != 0,
		ExitCode: &exitCode,
	}
}

// scrubbedEnv returns a copy of base with sensitive Omnipus env vars removed.
// Used only on the god-mode path (runUnconstrained / background godMode
// spawn), where sandbox.ScrubGatewayEnv's stricter allowlist is intentionally
// NOT applied (god mode preserves the operator's full environment) but the
// gateway's own credential material must still never leak to a child.
func scrubbedEnv(base []string) []string {
	blocked := map[string]bool{
		"OMNIPUS_MASTER_KEY":   true,
		"OMNIPUS_KEY_FILE":     true,
		"OMNIPUS_BEARER_TOKEN": true,
	}
	out := make([]string, 0, len(base))
	for _, kv := range base {
		key := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			key = kv[:idx]
		}
		if !blocked[key] {
			out = append(out, kv)
		}
	}
	return out
}

// runUnconstrained runs the command without ApplyChildHardening/sandbox.Run.
// Used only under the global god-mode override. The command still runs in
// the resolved cwd and honors the caller-supplied timeout via context
// cancellation (exec.CommandContext); the parent environment is inherited
// with sensitive Omnipus credentials stripped.
func (t *ExecTool) runUnconstrained(
	ctx context.Context,
	argv []string,
	cwdPath string,
	timeoutSeconds int32,
) *ToolResult {
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	if cwdPath != "" {
		cmd.Dir = cwdPath
	}
	cmd.Env = scrubbedEnv(os.Environ())

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	runErr := cmd.Run()
	dur := time.Since(start)

	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)

	exitCode := 0
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		} else if !timedOut {
			return ErrorResult(fmt.Sprintf("command failed: %v", runErr))
		} else {
			exitCode = -1
		}
	}

	output := stdoutBuf.String()
	if stderrBuf.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += "STDERR:\n" + stderrBuf.String()
	}

	if timedOut {
		msg := fmt.Sprintf("Command timed out after %d seconds", timeoutSeconds)
		if output != "" {
			msg += fmt.Sprintf("\n\nPartial output before timeout (ran %s):\n%s", dur.Round(time.Millisecond), output)
		}
		return &ToolResult{
			ForLLM:   msg,
			ForUser:  msg,
			IsError:  true,
			Err:      errors.New("command timeout"),
			TimedOut: true,
		}
	}

	// review r2 HIGH-1: same fix as foregroundResultFromSandbox above — the
	// structured field is set first (truncation-immune, authoritative for
	// the judge), and the display suffix is appended AFTER truncation.
	realExitCode := exitCode
	output = truncateOutput(output, exitCode)
	if exitCode != 0 {
		output += fmt.Sprintf("\n\n[Command exited with code %d]", exitCode)
	}

	return &ToolResult{
		ForLLM:   output,
		ForUser:  output,
		IsError:  exitCode != 0,
		ExitCode: &realExitCode,
	}
}

// Foreground output caps, aligned to the ADR-066 D4 per-surface figures
// (FR-014, B-15) so a bash result never reaches the tool-result choke point
// already larger than the cap it will be held to. A successful command
// (exit 0) may return up to the builtin-success cap; a failed one is held
// to the builtin-failure cap. There is no per-tool opt-out.
const (
	maxForegroundSuccessOutputLen = config.DefaultBuiltinSuccessCap // 64,000 chars
	maxForegroundOutputLen        = config.DefaultBuiltinFailureCap // 10,000 chars (failure path)
)

func truncateOutput(output string, exitCode int) string {
	if output == "" {
		return "(no output)"
	}
	limit := maxForegroundOutputLen
	if exitCode == 0 {
		limit = maxForegroundSuccessOutputLen
	}
	// utils.Truncate is rune-safe (never splits a multi-byte UTF-8 codepoint
	// mid-character, unlike a raw output[:limit] byte slice) — see G-1 in the
	// tool-catalog review; same bug class already fixed in
	// BuildCompressedManifest (manifest.go).
	runeCount := utf8.RuneCountInString(output)
	if runeCount > limit {
		return utils.Truncate(output, limit) + fmt.Sprintf(
			"\n... (truncated, %d more chars)",
			runeCount-limit,
		)
	}
	return output
}

// --- background execution ----------------------------------------------------

// sandboxLimitsEnv builds the environment for a background session on the
// non-god-mode path. Starts from sandbox.ScrubGatewayEnv() and layers the
// Limits-derived injections (HTTP_PROXY, npm_config_cache) on top.
func sandboxLimitsEnv(lim sandbox.Limits) []string {
	scrubbed := sandbox.ScrubGatewayEnv()
	if lim.EgressProxyAddr != "" {
		proxyURL := "http://" + lim.EgressProxyAddr
		scrubbed = append(scrubbed,
			"HTTP_PROXY="+proxyURL,
			"HTTPS_PROXY="+proxyURL,
			"http_proxy="+proxyURL,
			"https_proxy="+proxyURL,
			"NO_PROXY=127.0.0.1,localhost,::1",
			"no_proxy=127.0.0.1,localhost,::1",
		)
	}
	if lim.WorkspaceDir != "" {
		scrubbed = append(scrubbed, "npm_config_cache="+lim.WorkspaceDir+"/.npm-cache")
	}
	return scrubbed
}

// runBackground starts command detached, tracks it as a ProcessSession
// (stamped with OwnerSessionID per FR-B10 so a session-level cancel can find
// it — see pkg/agent/cancel.go's CancelHooks.KillBackgroundSessions /
// SessionManager.KillAllForSession), and returns immediately with a
// session_id. ownerSessionID is whatever the caller's context carries as
// ToolTranscriptSessionID(ctx) (see executeRun above) — under ADR-057
// (FR-027) that is the CHILD's own distinct session id when this call
// happens inside a delegated sub-turn, not the root chat session's id it
// may previously have shared; a session-level cancel that must reach this
// process therefore cascades over the resolved descendant set via
// SessionManager.KillAllForSessions rather than a single exact match. The
// completion goroutine below fires cb exactly once — on natural completion,
// failure, timeout, or explicit kill (FR-B9) — via whichever ToolResult best
// describes the final state.
func (t *ExecTool) runBackground(
	ctx context.Context,
	command, cwd string,
	timeoutSeconds int32,
	lim sandbox.Limits,
	ownerSessionID string,
	cb AsyncCallback,
) *ToolResult {
	sessionID := generateSessionID()
	session := &ProcessSession{
		ID:             sessionID,
		Command:        command,
		Background:     true,
		StartTime:      time.Now().Unix(),
		Status:         StatusRunning,
		OwnerSessionID: ownerSessionID,
	}

	argv := buildShellArgv(command)
	cmd := exec.Command(argv[0], argv[1:]...) // gosec rationale (out of gosec scope; kept as documentation): command is agent-supplied by design; guarded above
	if cwd != "" {
		cmd.Dir = cwd
	}

	// Setpgid baseline (Setpgid:true; no-op on Windows). sandbox.ApplyChildHardening
	// (below) safely EXTENDS this SysProcAttr (adds Pdeathsig) rather than
	// clobbering it — see hardened_exec_linux.go's applyPlatformHardening,
	// which only sets fields on an already-non-nil SysProcAttr.
	prepareCommandForTermination(cmd)

	if t.godMode {
		cmd.Env = scrubbedEnv(os.Environ())
	} else {
		cmd.Env = sandboxLimitsEnv(lim)
		if err := sandbox.ApplyChildHardening(cmd, lim); err != nil {
			return ErrorResult(fmt.Sprintf("sandbox hardening failed: %v", err))
		}
	}

	stdoutReader, err := cmd.StdoutPipe()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create stdout pipe: %v", err))
	}
	stderrReader, err := cmd.StderrPipe()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create stderr pipe: %v", err))
	}

	session.outputBuffer = &bytes.Buffer{}

	var startErr error
	if t.godMode {
		startErr = cmd.Start()
	} else {
		startErr = sandbox.StartLocked(cmd)
	}
	if startErr != nil {
		return ErrorResult(fmt.Sprintf("failed to start command: %v", startErr))
	}

	session.PID = cmd.Process.Pid
	// FR-011: report the spawned background child to the scheduled-run
	// process tracker (if the caller installed one) so it can be
	// force-terminated on run completion. No-op when no tracker is on ctx.
	TrackProcess(ctx, session.PID)
	t.sessionManager.Add(session)

	pipeReadFn := func(r io.Reader) {
		buf := make([]byte, 4096)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				session.mu.Lock()
				if session.outputBuffer.Len() >= maxOutputBufferSize {
					if !session.outputTruncated {
						session.outputBuffer.WriteString(outputTruncateMarker)
						session.outputTruncated = true
					}
				} else {
					session.outputBuffer.Write(buf[:n])
				}
				session.mu.Unlock()
			}
			if readErr != nil {
				return
			}
		}
	}

	var pipeWG sync.WaitGroup
	pipeWG.Add(2)
	go func() { defer pipeWG.Done(); pipeReadFn(stdoutReader) }()
	go func() { defer pipeWG.Done(); pipeReadFn(stderrReader) }()

	// FR-B3: enforce timeout_seconds identically for background as for
	// foreground. Fires session.KillAndRelabel(StatusTimeout) (the same kill
	// primitive SessionManager.KillAllForSession uses, atomically relabeled
	// to "timeout" in ONE lock acquisition — see KillAndRelabel's doc
	// comment) so pollers/AsyncNotifier can distinguish it from an explicit
	// kill or a natural exit.
	if timeoutSeconds > 0 {
		go func() {
			timer := time.NewTimer(time.Duration(timeoutSeconds) * time.Second)
			defer timer.Stop()
			<-timer.C
			if killErr := session.KillAndRelabel(StatusTimeout); killErr != nil {
				if !errors.Is(killErr, ErrSessionDone) {
					slog.Warn("bash: background timeout kill failed",
						"session_id", sessionID, "pid", session.PID, "error", killErr)
					if t.killAuditFn != nil {
						t.killAuditFn(session.PID, killErr, "bash_background_timeout")
					}
				}
			}
		}()
	}

	// Completion goroutine: the single place that knows the process has
	// actually exited and pipes are drained. It claims "done" only if no
	// other path (explicit action=kill, timeout_seconds guard, or a
	// RequestCancel kill cascade — see KillAndRelabel) already claimed a
	// terminal status — see KillAndRelabel's own atomic running-check for why
	// this is race free (MIN-002). This is also FR-B9's sole notification
	// point: it fires cb exactly once, regardless of which of the four
	// outcomes occurred (done, killed, timeout, canceled —
	// backgroundCompletionResult's switch below must handle all four
	// explicitly; a missed case previously let "canceled" fall through to a
	// misleading generic-failure summary, see that function's doc comment).
	go func() {
		pipeWG.Wait()
		waitErr := cmd.Wait()

		session.mu.Lock()
		if session.Status == StatusRunning {
			if cmd.ProcessState != nil {
				session.ExitCode = cmd.ProcessState.ExitCode()
			} else {
				if waitErr != nil {
					logger.WarnCF("bash", "background cmd.Wait returned error with nil ProcessState",
						map[string]any{"session_id": sessionID, "error": waitErr.Error()})
				}
				session.ExitCode = -1
			}
			session.Status = StatusDone
		}
		finalStatus := session.Status
		finalExitCode := session.ExitCode
		// Peek (non-destructive) at whatever output has accumulated so the
		// async notification has content to show — do NOT Reset() here: an
		// explicit action=read call after completion must still be able to
		// drain this same buffered output (Reset is session.Read()'s job).
		outputSoFar := session.outputBuffer.String()
		session.mu.Unlock()

		if cb != nil {
			cb(context.Background(), backgroundCompletionResult(sessionID, finalStatus, finalExitCode, outputSoFar))
		}
	}()

	resp := ExecResponse{
		SessionID: sessionID,
		Status:    string(StatusRunning),
	}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		logger.WarnCF("bash", "failed to marshal bash start response", map[string]any{"error": marshalErr.Error()})
		data = marshalErrorFallback(marshalErr)
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: fmt.Sprintf("Session %s started", sessionID),
		IsError: marshalErr != nil,
	}
}

// backgroundCompletionResult builds the ToolResult passed to cb when a
// background session reaches a terminal state (FR-B9). There are exactly
// four possible terminal outcomes for a background bash session — a natural
// exit (StatusDone), an explicit action=kill (StatusKilled), the
// timeout_seconds guard firing (StatusTimeout), and a RequestCancel kill
// cascade (StatusCanceled, see SessionManager.KillAllForSession) — and this
// switch MUST handle all four explicitly. Before this switch added the
// StatusCanceled case, a canceled background job fell through to the
// default branch and was misreported to the LLM/user as a generic failure
// ("finished (exit code N)", IsError:true) instead of an intentional,
// user-initiated cancellation.
func backgroundCompletionResult(sessionID string, status SessionStatus, exitCode int, output string) *ToolResult {
	if output == "" {
		output = "(no output)"
	}
	var summary string
	isError := true
	switch status {
	case StatusTimeout:
		summary = fmt.Sprintf("Background session %s timed out.\n\n%s", sessionID, output)
	case StatusKilled:
		summary = fmt.Sprintf("Background session %s was killed.\n\n%s", sessionID, output)
	case StatusCanceled:
		summary = fmt.Sprintf("Background session %s was canceled.\n\n%s", sessionID, output)
		isError = false
	case StatusDone:
		summary = fmt.Sprintf("Background session %s finished (exit code %d).\n\n%s", sessionID, exitCode, output)
		isError = false
	default:
		// Unexpected status reaching this switch (e.g. StatusRunning/
		// StatusExited, which should never be the FINAL status a completion
		// goroutine observes) — keep the same generic-failure fallback the
		// pre-existing default case used, so an unforeseen future status
		// still produces a safe (loud, not silently-successful) result.
		summary = fmt.Sprintf("Background session %s finished (exit code %d).\n\n%s", sessionID, exitCode, output)
	}
	return &ToolResult{
		ForLLM:  summary,
		ForUser: summary,
		IsError: isError,
	}
}

// --- session actions (poll / read / kill) ------------------------------------

// getSessionArg resolves the action=poll/read/kill session_id argument
// through SessionManager.GetOwned (M5 fix, live UAT 2026-07-31): the
// caller's own ToolTranscriptSessionID(ctx) must match the session's
// OwnerSessionID, or the lookup is denied with the same "session not found"
// message a genuinely-missing session_id would produce — see GetOwned's own
// doc comment for why this must not be a distinguishable "forbidden" error.
// Before this fix, ANY chat/transcript session could poll/read/kill ANY
// OTHER session's background bash job process-wide just by knowing its
// short session_id, with zero ownership check.
func (t *ExecTool) getSessionArg(ctx context.Context, args map[string]any) (*ProcessSession, string, *ToolResult) {
	sessionID, ok := args["session_id"].(string)
	if !ok || sessionID == "" {
		return nil, "", ErrorResult("session_id is required")
	}
	session, err := t.sessionManager.GetOwned(sessionID, ToolTranscriptSessionID(ctx))
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return nil, "", ErrorResult(fmt.Sprintf("session not found: %s", sessionID))
		}
		return nil, "", ErrorResult(err.Error())
	}
	return session, sessionID, nil
}

func (t *ExecTool) executePoll(ctx context.Context, args map[string]any) *ToolResult {
	session, sessionID, errResult := t.getSessionArg(ctx, args)
	if errResult != nil {
		return errResult
	}

	resp := ExecResponse{
		SessionID: sessionID,
		Status:    session.GetStatus(),
		ExitCode:  session.GetExitCode(),
	}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		logger.WarnCF("bash", "failed to marshal bash poll response", map[string]any{"error": marshalErr.Error()})
		data = marshalErrorFallback(marshalErr)
	}
	return &ToolResult{
		ForLLM:  string(data),
		IsError: marshalErr != nil,
	}
}

func (t *ExecTool) executeRead(ctx context.Context, args map[string]any) *ToolResult {
	session, sessionID, errResult := t.getSessionArg(ctx, args)
	if errResult != nil {
		return errResult
	}

	output := session.Read()

	resp := ExecResponse{
		SessionID: sessionID,
		Output:    output,
		Status:    session.GetStatus(),
	}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		logger.WarnCF("bash", "failed to marshal bash read response", map[string]any{"error": marshalErr.Error()})
		data = marshalErrorFallback(marshalErr)
	}
	return &ToolResult{
		ForLLM:  string(data),
		IsError: marshalErr != nil,
	}
}

// executeKill terminates a running background session. MIN-002: if the
// process already exited naturally (a race between an earlier poll and this
// kill call), it reports the REAL final status instead of a false "killed" —
// session.IsDone() is checked BEFORE any kill attempt, and
// session.KillAndRelabel itself atomically no-ops (ErrSessionDone) if it lost
// that race.
func (t *ExecTool) executeKill(ctx context.Context, args map[string]any) *ToolResult {
	session, sessionID, errResult := t.getSessionArg(ctx, args)
	if errResult != nil {
		return errResult
	}

	if session.IsDone() {
		return t.sessionActionResult(sessionID, session)
	}

	// KillAndRelabel kills AND relabels to StatusKilled atomically under one
	// lock acquisition — no separate SetStatus call, no gap for a concurrent
	// poller to observe the generic "done" before the more specific "killed"
	// label lands.
	if err := session.KillAndRelabel(StatusKilled); err != nil {
		if errors.Is(err, ErrSessionDone) {
			// Raced: the process exited naturally between our IsDone() check
			// and this Kill() call. Report the real final status, not an error.
			return t.sessionActionResult(sessionID, session)
		}
		if t.killAuditFn != nil {
			t.killAuditFn(session.PID, err, "bash_kill_action")
		}
		return ErrorResult(fmt.Sprintf("failed to kill session: %v", err))
	}

	return t.sessionActionResult(sessionID, session)
}

func (t *ExecTool) sessionActionResult(sessionID string, session *ProcessSession) *ToolResult {
	status := session.GetStatus()
	resp := ExecResponse{
		SessionID: sessionID,
		Status:    status,
		ExitCode:  session.GetExitCode(),
	}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		logger.WarnCF("bash", "failed to marshal bash kill response", map[string]any{"error": marshalErr.Error()})
		data = marshalErrorFallback(marshalErr)
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: fmt.Sprintf("Session %s %s", sessionID, status),
		IsError: marshalErr != nil,
	}
}

// inTurnSecretSet reports whether p is in this turn's secret set — another
// agent's home, another workspace's tree, or one of the always-secret entries.
//
// DENY-side, so it errs toward matching. IsCarveOut wants a RESOLVED path
// (its own parameter is named resolvedAbsPath) and resolves by filesystem
// identity, so both spellings of the same file are asked about: the lexical
// form the command actually wrote, and its symlink-resolved form. Checking only
// one is what made the first version of this fix inert on macOS, where
// /var/folders/... and /private/var/folders/... are the same directory and the
// guard compared them as strings.
//
// A resolve failure leaves the lexical verdict standing rather than failing
// open, and never aborts the command.
func (t *ExecTool) inTurnSecretSet(p string, policy fspolicy.FSPolicy) bool {
	if fspolicy.IsCarveOut(p, policy) {
		return true
	}
	if resolved, err := resolvePathAgainstExistingAncestor(p); err == nil && resolved != p {
		if fspolicy.IsCarveOut(resolved, policy) {
			return true
		}
	}
	return false
}

// expandCandidatePrefix inspects the boundary text absolutePathPattern consumed
// immediately before a path candidate and, when that boundary is a `~` or a
// variable this process can resolve, returns the prefix the shell will prepend.
//
// # Why this exists
//
// absolutePathPattern treats `~` and a token-start `$VAR` as boundary
// characters and captures only the suffix, so `$HOME/.omnipus/agents/x/SOUL.md`
// arrives as the candidate `/.omnipus/agents/x/SOUL.md` — a path naming no real
// file. Judging that phantom is meaningless, and it was measurably exploitable
// (code review round 3): the ADR-068 read exemption was reachable through the
// `~`/`$HOME` spelling of a file whose literal absolute spelling was correctly
// refused, because IsCarveOut cannot match a carve-out root against a path that
// never contained $OMNIPUS_HOME.
//
// # Why resolve rather than refuse
//
// Refusing every expansion-derived candidate outright also blocks `cat
// ~/notes.txt`, an ordinary read ADR-068 exists to permit — and an existing
// test asserts that it must stay permitted. Resolving the prefix keeps that
// read working while giving the secret-set and mount checks the real path.
//
// # Fail-closed remainder
//
// Only `~`, `$HOME`/`${HOME}` and `$OMNIPUS_HOME`/`${OMNIPUS_HOME}` are
// resolved, because only those have a value this process can be sure of. Any
// other `$VAR` returns resolved=false, and the caller refuses the candidate
// rather than guessing. os.UserHomeDir failing likewise yields resolved=false.
//
// Returns (prefix, resolved, isExpansion). isExpansion=false means the boundary
// was ordinary punctuation and the candidate is already a literal path.
func expandCandidatePrefix(boundary string) (string, bool, bool) {
	if boundary == "" {
		return "", false, false
	}
	// The boundary may carry leading punctuation (`="`, `,`, a flag cluster);
	// only its TAIL decides, because that is what abuts the captured path.
	switch {
	case strings.HasSuffix(boundary, "~"):
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", false, true
		}
		return home, true, true
	case strings.HasSuffix(boundary, "$HOME"), strings.HasSuffix(boundary, "${HOME}"):
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", false, true
		}
		return home, true, true
	case strings.HasSuffix(boundary, "$OMNIPUS_HOME"), strings.HasSuffix(boundary, "${OMNIPUS_HOME}"):
		if h := config.OmnipusHomeDir(); h != "" {
			return h, true, true
		}
		return "", false, true
	case strings.ContainsRune(boundary, '$'):
		// Some other variable — value unknown to this process.
		return "", false, true
	}
	return "", false, false
}
