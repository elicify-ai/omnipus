// executor_opts.go — MAJ-5: consume the agent's ExecutorConfig (cli_path,
// cli_args, env_overrides) when spawning a subagent_3p external CLI.
//
// The contract fields (ExecutorConfig.yaml) already existed; this file is the
// backend wiring that turns them into the actual subprocess invocation:
//
//   - cli_path  — the binary to exec (falls back to the driver's default name,
//     resolved via $PATH, when empty).
//   - cli_args  — free-form extra arguments, parsed into an argv slice and
//     appended to the driver's own args. execve is used (no shell), so values
//     are passed literally; shell-injection characters are warned, not rejected
//     (per the ExecutorConfig schema).
//   - env_overrides — extra environment variables merged into the (already
//     scrubbed) child env. Omnipus-internal keys (OMNIPUS_*) and the master-key
//     env vars are NEVER overridable, so a misconfigured override cannot leak
//     the gateway master key or spoof the agent identity vars.
package runner

import (
	"log/slog"
	"strings"
)

// protectedEnvOverridePrefixes lists env-var name prefixes that env_overrides
// MUST NOT set or replace. Anything starting with "OMNIPUS_" is Omnipus-internal
// (agent identity vars OMNIPUS_AGENT_NAME / OMNIPUS_AGENT_TYPE, the master-key
// vars OMNIPUS_MASTER_KEY / OMNIPUS_KEY_FILE, the bearer token, etc.). Keeping
// the whole namespace reserved is simpler and safer than enumerating individual
// keys, and matches the schema's "OMNIPUS_AGENT_NAME/OMNIPUS_AGENT_TYPE and the
// master-key env var are NOT overridable" rule.
var protectedEnvOverridePrefixes = []string{"OMNIPUS_"}

// isProtectedEnvKey reports whether an env-override key targets a reserved
// Omnipus-internal variable that user config must not override.
func isProtectedEnvKey(key string) bool {
	for _, p := range protectedEnvOverridePrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// resolveCLIBinary returns the binary the driver should exec. When cliPath is a
// non-empty (trimmed) value it is used verbatim — an absolute path is honoured,
// a bare name is resolved via $PATH by exec. When empty, the driver's default
// binary name is used (resolved via $PATH). This implements the
// ExecutorConfig.cli_path "fall back to $PATH when empty" rule.
func resolveCLIBinary(cliPath, defaultBin string) string {
	if p := strings.TrimSpace(cliPath); p != "" {
		return p
	}
	return defaultBin
}

// shellInjectionChars are genuine shell-control characters (command separators,
// pipes, redirects, subshells, command/variable substitution, escapes). The
// drivers exec the CLI directly (no shell), so these are passed literally and
// are harmless — but their presence in cli_args usually signals the operator
// expected shell semantics, so we WARN (never reject) per the schema.
//
// Deliberately EXCLUDES glob/expansion chars that legitimately appear in
// ordinary literal arguments (e.g. `=` in `--flag=val`, and `* ? [ ] # ~` in
// paths/patterns), which would otherwise fire false-positive warnings on benign
// values.
const shellInjectionChars = "|&;<>()$`\\\"'\n"

// ParseCLIArgs is the exported entry point for the dispatch site
// (pkg/agent/external_dispatch.go) to tokenise an ExecutorConfig.cli_args string
// into an argv slice. See parseCLIArgs for the tokenisation rules and the
// warn-not-reject shell-metacharacter behaviour.
func ParseCLIArgs(raw, runLabel string) []string {
	return parseCLIArgs(raw, runLabel)
}

// parseCLIArgs splits a free-form cli_args string into an argv slice using
// POSIX-like whitespace tokenisation with single/double-quote grouping. It does
// NOT perform variable expansion, command substitution, or glob expansion — the
// result is passed straight to execve, so the tokens are literal. A value that
// contains shell-metacharacters is tokenised as-is and a single WARN is logged
// (warn-not-reject, per ExecutorConfig.yaml).
//
// runLabel is a short identifier (run ID or agent ID) used only in the warning
// log so an operator can correlate it.
func parseCLIArgs(raw, runLabel string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	// Warn (once) if the raw string carries shell-metacharacters — they will be
	// passed literally to execve, which is usually NOT what an operator who typed
	// e.g. `--flag $(whoami)` intended. We do not reject: the schema mandates
	// warn-not-reject because execve makes the literal pass-through safe.
	if strings.ContainsAny(raw, shellInjectionChars) {
		slog.Warn("runner: cli_args contains shell-metacharacters — passed literally to execve (no shell interpolation)",
			"run", runLabel, "cli_args", raw)
	}

	return tokenizeArgs(raw)
}

// tokenizeArgs splits s into argv tokens. Whitespace separates tokens; single
// and double quotes group a token (the quotes themselves are stripped). A
// backslash inside a double-quoted or unquoted region escapes the next char.
// This is a deliberately small, dependency-free tokenizer — NOT a full shell —
// because the result feeds execve directly.
func tokenizeArgs(s string) []string {
	var (
		tokens  []string
		cur     strings.Builder
		inToken bool
		quote   rune // 0, '\'' or '"'
		escaped bool
	)
	flush := func() {
		if inToken {
			tokens = append(tokens, cur.String())
			cur.Reset()
			inToken = false
		}
	}
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			inToken = true
			escaped = false
		case r == '\\' && quote != '\'':
			// Backslash escapes the next rune, except inside single quotes where
			// it is literal (POSIX single-quote semantics).
			escaped = true
			inToken = true
		case quote != 0:
			if r == quote {
				quote = 0 // closing quote
			} else {
				cur.WriteRune(r)
			}
			inToken = true
		case r == '\'' || r == '"':
			quote = r
			inToken = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			cur.WriteRune(r)
			inToken = true
		}
	}
	// A trailing backslash or unterminated quote: emit what we have so the token
	// is not silently dropped.
	flush()
	return tokens
}

// mergeEnvOverrides returns a copy of baseEnv (the already-scrubbed child env)
// with the supplied env_overrides applied. Protected Omnipus-internal keys are
// skipped with a WARN (one per offending key) so a misconfigured override cannot
// leak the master key or spoof OMNIPUS_AGENT_* identity vars. A non-protected
// key present in both baseEnv and overrides is REPLACED by the override value; a
// new key is appended.
//
// baseEnv nil/empty is preserved (the child-env "inherit nothing" contract is
// not broken — overrides only ADD to whatever base was supplied).
func mergeEnvOverrides(baseEnv []string, overrides map[string]string, runLabel string) []string {
	if len(overrides) == 0 {
		return baseEnv
	}

	// Index existing keys → position in the output slice for in-place replace.
	out := make([]string, len(baseEnv))
	copy(out, baseEnv)
	idx := make(map[string]int, len(out))
	for i, kv := range out {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			idx[kv[:eq]] = i
		}
	}

	for k, v := range overrides {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		if isProtectedEnvKey(key) {
			slog.Warn("runner: env_overrides may not set a reserved Omnipus-internal variable — ignored",
				"run", runLabel, "key", key)
			continue
		}
		entry := key + "=" + v
		if pos, ok := idx[key]; ok {
			out[pos] = entry
		} else {
			idx[key] = len(out)
			out = append(out, entry)
		}
	}
	return out
}
