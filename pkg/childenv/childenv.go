// Package childenv holds the closed allowlist that decides which environment
// variables a gateway-spawned child process may inherit (FR-239; #155 item 3).
//
// It is a leaf package on purpose: the allowlist has exactly one definition,
// and two packages that must both apply it — pkg/sandbox (ScrubGatewayEnv,
// the hardened-exec and tool spawn path) and pkg/security (the exec proxy's
// nil-environment fallback) — cannot import each other (pkg/sandbox's egress
// proxy imports pkg/security for the SSRF checker, so the reverse edge is an
// import cycle). Both expose the allowlist through their own public surface;
// this package owns the data and the filter so the two can never drift.
//
// The allowlist itself is unchanged since #155 item 3 moved the child
// environment from a 3-key denylist to this closed allowlist; FR-239 (WU-19)
// extended its REACH — every spawn site must start from it — not its content.
package childenv

import (
	"os"
	"sort"
	"strings"
)

// allowedEnvKeys is the EXPLICIT ALLOWLIST of environment variable names
// that may be inherited by a gateway-spawned child process. Any key not
// present here (and not matched by allowedEnvPrefixes) is stripped before the
// child sees the environment.
//
// Threat model (#155 item 3): the previous implementation maintained a
// 3-key denylist (OMNIPUS_MASTER_KEY, OMNIPUS_KEY_FILE, OMNIPUS_BEARER_TOKEN).
// That model fails open: any newly-introduced sensitive env var (a future
// API key, an upstream provider token, a third-party secret loaded by a
// dependency) leaks to children until someone remembers to add it to the
// denylist. The allowlist inverts the default — unknown keys are stripped,
// new sensitive keys are safe by default.
//
// The allowlist covers exactly what a generic build/run child needs to find
// libraries, locate the user's home, render localized output, and write to
// a scratch directory:
//
//	PATH    — required to locate executables (npm, node, python, sh, ...)
//	HOME    — npm/pip/cargo write per-user caches under here; node uses it for
//	          ~/.npmrc; many tools blow up if HOME is unset
//	USER    — git, ssh, and many CLI tools query it for default user
//	LOGNAME — equivalent to USER on systemd-managed Linux distros
//	SHELL   — npm spawns scripts via $SHELL; subprocess shells use it
//	TZ      — controls log timestamp rendering and date-sensitive build steps
//	LANG    — UTF-8 locale required for non-ASCII filenames in builds
//	TMPDIR  — Go's ioutil.TempFile and many test runners honor this
//	TERM    — terminal capability detection; npm/yarn use it for color output
//
// The two prefix-allowlists cover broad families:
//
//	LC_*  — C locale family (LC_ALL, LC_CTYPE, LC_NUMERIC, ...) — required
//	        for correct sorting, formatting, and case-insensitive filename
//	        comparison in build tools.
//	XDG_* — XDG Base Directory spec (XDG_CONFIG_HOME, XDG_CACHE_HOME, ...) —
//	        per-user dirs that respectful CLI tools use instead of fixed
//	        ~/.config / ~/.cache; passing through keeps user dotfile setups
//	        working in builds.
//
// And one operator escape hatch:
//
//	OMNIPUS_CHILD_*  — narrow, namespaced pass-through for callers that
//	                   intentionally want to forward a value to a child.
//	                   Operators wanting to pass FOO=bar to a child must
//	                   rename it to OMNIPUS_CHILD_FOO=bar at the gateway
//	                   layer. The rename is the trust boundary — it's loud
//	                   and grep-able, so a future audit can find every
//	                   intentional pass-through with one search.
//
// Build with care: anything you add here is information that reaches every
// child process for the rest of the gateway's life. If a key contains a
// secret, do NOT add it; use the OMNIPUS_CHILD_* rename pattern instead.
var allowedEnvKeys = map[string]struct{}{
	"PATH":    {},
	"HOME":    {},
	"USER":    {},
	"LOGNAME": {},
	"SHELL":   {},
	"TZ":      {},
	"LANG":    {},
	"TMPDIR":  {},
	"TERM":    {},
	// Standard proxy variables. Forwarded so operator-set or
	// EgressProxy-injected proxies route child traffic correctly. Values
	// are operator-controlled (egress proxy address) or operator-inherited
	// from the parent, never secrets. Required by TestExecProxy_*.
	"HTTP_PROXY":  {},
	"HTTPS_PROXY": {},
	"NO_PROXY":    {},
	"http_proxy":  {},
	"https_proxy": {},
	"no_proxy":    {},
}

// allowedEnvPrefixes are key prefixes whose entire family is allowed.
// See allowedEnvKeys for the rationale on each prefix.
var allowedEnvPrefixes = []string{
	"LC_",
	"XDG_",
	"OMNIPUS_CHILD_",
}

// runnerCredentialEnvKeys is the SEPARATE, security-sensitive allowlist of
// credential / config-home environment variable names that an EXTERNAL-AGENT
// CLI RUNNER child (Claude Code, Codex, opencode — Spec-4 FR-5.3) is permitted
// to inherit, ON TOP OF the generic allowedEnvKeys set.
//
// Threat model (Spec-4 FR-5.3, security-sensitive):
//
//	These keys CARRY SECRETS (API keys, OAuth bearer tokens). They are
//	DELIBERATELY ABSENT from the generic allowedEnvKeys allowlist because
//	the generic allowlist feeds EVERY gateway child — every
//	`bash` (ADR-036 merge of the former exec/workspace_shell/workspace_shell_bg),
//	`web_serve`, and `build_static` subprocess. Leaking an
//	Anthropic / OpenAI key into an arbitrary build script is exactly the
//	fail-open class the #155 allowlist rework closed.
//
//	The external-CLI runner is the ONE caller that legitimately needs these:
//	the CLI authenticates to its upstream model provider on the operator's
//	behalf. The runner's dispatch site
//	(pkg/agent/external_dispatch.go::runExternalCLISubTurn) sets
//	RunOptions.Env = sandbox.ScrubGatewayEnvForRunner() (below), which unions
//	THIS narrow set with the generic allowlist; the driver then uses that
//	slice as the COMPLETE child env (runner.buildChildEnv) with NO
//	os.Environ() fallback. No other spawn path sees these keys, and the
//	gateway secrets (e.g. OMNIPUS_MASTER_KEY) never reach the child.
//
// Each key is grounded in the respective CLI's documented auth contract:
//
//	ANTHROPIC_API_KEY    — Claude Code + opencode (Anthropic mode): the
//	                       sk-ant-api03 model key. Without it the CLI 403s.
//	ANTHROPIC_AUTH_TOKEN — Claude Code: OAuth / corporate-proxy bearer token.
//	ANTHROPIC_BASE_URL   — Claude Code + opencode: provider endpoint override
//	                       (not itself a secret, but auth is meaningless if the
//	                       child can't reach the configured endpoint).
//	CLAUDE_CONFIG_DIR    — Claude Code: config-dir override; the OAuth creds
//	                       live as files under it (default ~/.claude, reached
//	                       via HOME, already allowlisted). Passed through so an
//	                       operator-isolated config dir resolves in the child.
//	OPENAI_API_KEY       — Codex + opencode: the OpenAI model key. NOTE: as of
//	                       this writing, `codex exec`'s own auth-resolution
//	                       path (codex-rs/login/src/auth/manager.rs::
//	                       load_auth) never reads this var for the default
//	                       provider — implicit OPENAI_API_KEY login was
//	                       deliberately removed (an OpenAI engineer confirmed
//	                       on github.com/openai/codex#5212 it will not come
//	                       back). Left allowlisted regardless: opencode's
//	                       Codex-compatible mode and other tooling may still
//	                       consult it, and forwarding it is not itself unsafe.
//	OPENAI_BASE_URL      — Codex + opencode: provider endpoint override.
//	CODEX_HOME           — Codex: override for ~/.codex (holds auth.json).
//	                       Default ~/.codex is reached via HOME; this honors a
//	                       custom location. See providers.CodexHomeEnvVar.
//	CODEX_API_KEY        — Codex: the env var `codex exec` actually checks
//	                       first for non-interactive auth (load_auth, gated by
//	                       `enable_codex_api_key_env: true`, which `codex exec`
//	                       sets). Source-grounded against codex-rs/login/src/
//	                       auth/manager.rs on the openai/codex main branch —
//	                       NOT documented end-user-facing behavior, but the
//	                       actual code path this runner's headless spawns hit.
//	CODEX_ACCESS_TOKEN   — Codex: the env var load_auth checks next, before
//	                       falling back to a persisted `auth.json`/keyring from
//	                       a prior `codex login`. Same source grounding as
//	                       CODEX_API_KEY above.
//	CLAUDE_CODE_OAUTH_TOKEN — Claude Code: subscription/OAuth headless-auth
//	                       token, minted once via `claude setup-token` and
//	                       exported for unattended use — documented, standard
//	                       Claude Code behavior (distinct from ANTHROPIC_API_KEY,
//	                       a raw model API key; this is a subscription-backed
//	                       OAuth bearer). Empirically confirmed: with only this
//	                       var set (ANTHROPIC_* absent), `claude auth status`
//	                       reports {"loggedIn":true,"authMethod":"oauth_token"}.
//	                       Without it in this allowlist, an operator whose
//	                       Claude Code auth is subscription/OAuth-token-based
//	                       (rather than a raw API key) would have every
//	                       external-CLI runner child silently fail with
//	                       "Not logged in" even though the operator's own
//	                       interactive `claude` sessions work fine — the exact
//	                       gap this entry closes.
//
// DO NOT add a key here unless an external-CLI runner provably needs it to
// authenticate. Anything added is a secret that reaches every external-CLI
// child for the life of the gateway. This set is intentionally exact — not a
// prefix family — so a future audit can enumerate every leak path by reading
// this map.
var runnerCredentialEnvKeys = map[string]struct{}{
	"ANTHROPIC_API_KEY":       {},
	"ANTHROPIC_AUTH_TOKEN":    {},
	"ANTHROPIC_BASE_URL":      {},
	"CLAUDE_CONFIG_DIR":       {},
	"CLAUDE_CODE_OAUTH_TOKEN": {},
	"OPENAI_API_KEY":          {},
	"OPENAI_BASE_URL":         {},
	"CODEX_HOME":              {},
	"CODEX_API_KEY":           {},
	"CODEX_ACCESS_TOKEN":      {},
}

// AllowedKey reports whether name passes the child-environment allowlist.
// Applied to every entry of os.Environ() by Filter, and usable by tests that
// exercise the boundary directly.
func AllowedKey(name string) bool {
	if _, ok := allowedEnvKeys[name]; ok {
		return true
	}
	for _, prefix := range allowedEnvPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// Filter returns a copy of os.Environ() filtered through the child process
// allowlist (see allowedEnvKeys). Entries with empty values pass through (a
// present-but-empty key like LANG="" is part of legitimate user config).
// Malformed entries (no '=', or '=' at index 0) are dropped — those are
// kernel-injected curiosities like `BASH_FUNC_foo%%=...` that no child needs.
//
// This is the function behind sandbox.ScrubGatewayEnv; pkg/security's exec
// proxy applies it directly because importing pkg/sandbox would be an import
// cycle (see the package comment).
func Filter() []string {
	parent := os.Environ()
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		if !AllowedKey(kv[:eq]) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// RunnerCredentialKeys returns a sorted snapshot of the external-CLI runner
// credential-env allowlist (see runnerCredentialEnvKeys). Exported for the
// runner package's tests and for audit tooling that needs to enumerate the
// exact secret-bearing keys that may reach a runner child. The returned slice
// is a copy; mutating it does not affect the allowlist.
func RunnerCredentialKeys() []string {
	out := make([]string, 0, len(runnerCredentialEnvKeys))
	for k := range runnerCredentialEnvKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// isRunnerCredentialKey reports whether name is in the external-CLI runner
// credential allowlist. It is NOT consulted by AllowedKey — only by
// FilterForRunner — so these secret-bearing keys never leak into a generic
// bash / build child.
func isRunnerCredentialKey(name string) bool {
	_, ok := runnerCredentialEnvKeys[name]
	return ok
}

// FilterForRunner is Filter with the runner credential keys additionally
// permitted. Same drop rules for malformed entries.
//
// This is the function behind sandbox.ScrubGatewayEnvForRunner — the ONLY
// path that lets credential-bearing env vars through, reserved for the
// Spec-4 external-agent CLI runner spawn path (FR-5.3). Generic children
// (bash, web_serve, build_static, and every FR-239 spawn site) MUST keep
// using Filter (via sandbox.ScrubGatewayEnv), which excludes these keys.
func FilterForRunner() []string {
	parent := os.Environ()
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		key := kv[:eq]
		if !AllowedKey(key) && !isRunnerCredentialKey(key) {
			continue
		}
		out = append(out, kv)
	}
	return out
}
