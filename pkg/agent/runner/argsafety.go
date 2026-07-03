// argsafety.go — ADR-032 fix M-1: guard operator-supplied
// ExecutorConfig.cli_args against re-enabling a permission/sandbox bypass the
// driver deliberately avoided (FR-5.2 / FR-5.3 / US-5).
//
// All three drivers (claude, codex, opencode) append opts.CLIArgs AFTER their
// own safety flags. claude, codex, and opencode's underlying CLI arg parsers
// are all last-occurrence-wins for a repeated flag, so an operator cli_args
// value can silently replace the driver's own explicit, deliberately-chosen
// flag — e.g. re-adding "--dangerously-skip-permissions" (claude),
// escalating "--permission-mode" to "bypassPermissions" (claude),
// "--sandbox danger-full-access" (codex), or reintroducing an
// "--ask-for-approval" gate that cannot be answered in a headless spawn
// (codex). There was previously NO guard for cli_args (unlike EnvOverrides,
// which already drops OMNIPUS_* keys with a WARN — see
// mergeEnvOverrides/isProtectedEnvKey in executor_opts.go, whose
// warn-and-drop shape this file mirrors).
//
// filterDangerousCLIArgs is applied to opts.CLIArgs ONLY, in each driver's
// buildArgs, immediately before appending the (filtered) result. It never
// touches the driver's own built-in flags — those are added to argv before
// this filter ever runs.
//
// A second, narrower category was added alongside the original permission/
// sandbox-bypass guard: each driver's stream-JSON output-format flag
// (claude's --output-format stream-json, codex's --json, opencode's --format
// json). This is a CORRECTNESS guard, not a security one — an operator
// cli_args override of these flags does not escalate privilege, it silently
// breaks driver_{claude,codex,opencode}.go's NDJSON stream parser (parseLine)
// by switching the CLI to human-readable or differently-shaped output, which
// the parser cannot read, producing an empty/garbled transcript with no
// error surfaced to the operator. filterDangerousCLIArgs is the right place
// to guard it regardless: the same last-occurrence-wins argv semantics apply,
// and the same warn-and-drop shape (logDroppedCLIArgs) already covers it.

package runner

import (
	"log/slog"
	"strings"
)

// dangerousCLIFlag describes a single denylisted operator cli_args override
// flag for one CLI.
type dangerousCLIFlag struct {
	// name is the literal flag token, e.g. "--dangerously-skip-permissions".
	name string
	// boolean flags take no separate value token — the bare flag (or an
	// explicit "name=value" boolean override) is always dangerous.
	boolean bool
	// valueCheck, for a non-boolean (value-taking) flag, reports whether a
	// given value is dangerous. nil means EVERY value is dangerous — the
	// conservative default when the flag has no safe non-default value in
	// this driver's posture (see the per-CLI comments below for why each
	// flag chose targeted vs. blanket dropping).
	valueCheck func(value string) bool
	// reason is a short, human-readable explanation of why this flag is
	// denylisted, summarizing the fuller rationale in the per-CLI comments
	// below. Surfaced to operators via the executor-command-preview endpoint
	// (POST /api/v1/agents/executor-preview) so a dropped cli_args token
	// comes with a reason, not just a bare flag name.
	reason string
}

// dangerousCLIFlagsByCLI is the per-CLI denylist applied to operator-supplied
// cli_args ONLY. It is deliberately narrow: only flags that can re-enable a
// full permission/sandbox bypass, or otherwise conflict with the specific
// non-interactive posture ADR-032 fix D chose for that CLI, are listed.
// Ordinary cli_args (model overrides, logging flags, etc.) pass through
// untouched.
var dangerousCLIFlagsByCLI = map[string][]dangerousCLIFlag{
	// claude: the driver never passes --dangerously-skip-permissions
	// (FR-5.3/US-5) and sets --permission-mode acceptEdits itself (ADR-032
	// fix D). Per Claude Code's own docs, "bypassPermissions" mode "skips
	// all safety checks" and is explicitly the "never use in production"
	// mode — the direct equivalent of --dangerously-skip-permissions
	// expressed via --permission-mode. Other values ("default", "plan", or a
	// redundant "acceptEdits") are the SAME or MORE restrictive than the
	// driver's own choice, so a targeted valueCheck (not a blanket drop) is
	// used: only "bypassPermissions" is dropped, an operator narrowing to
	// "plan"/"default" is left alone.
	"claude": {
		{
			name:    "--dangerously-skip-permissions",
			boolean: true,
			reason:  "re-enables a full permission bypass the driver deliberately never passes (FR-5.3/US-5); --permission-mode acceptEdits is used instead",
		},
		{
			name: "--permission-mode",
			valueCheck: func(v string) bool {
				return strings.EqualFold(v, "bypassPermissions")
			},
			reason: `escalates --permission-mode to "bypassPermissions", which Claude Code's own docs describe as skipping all safety checks — the direct equivalent of --dangerously-skip-permissions`,
		},
		// --output-format: driver_claude.go's parseLine expects EXACTLY the
		// "claude --output-format stream-json" NDJSON event shapes (see the
		// package doc there). Any other value ("json", "text", or an
		// unrecognized format) silently corrupts the streamed transcript
		// instead of erroring (correctness guard, not security — see the
		// package doc above). Only the driver's own value is a safe match;
		// every other value is dropped.
		{
			name: "--output-format",
			valueCheck: func(v string) bool {
				return !strings.EqualFold(v, "stream-json")
			},
			reason: "the driver's NDJSON stream parser requires --output-format stream-json; any other value would silently corrupt the streamed transcript instead of erroring",
		},
	},
	// codex: the driver never passes
	// --dangerously-bypass-approvals-and-sandbox (FR-5.3) and sets --sandbox
	// workspace-write + --ask-for-approval never itself (ADR-032 fix D).
	// Per OpenAI's own docs, sandbox_mode "danger-full-access" "removes the
	// filesystem and network boundaries" — the direct escalation of the
	// confinement boundary the driver chose, so only that specific value is
	// dropped (a narrowing to "read-only" is left alone).
	//
	// --ask-for-approval is handled differently: the driver's own choice,
	// "never", is documented by codex itself as the LEAST interactive value
	// (no approval prompts at all — functionally analogous to
	// --dangerously-skip-permissions), so there is no "more dangerous" value
	// to escalate TO. Every operator override is nonetheless dropped
	// (nil valueCheck): (a) codex requires --ask-for-approval to precede the
	// `exec` subcommand as a GLOBAL flag (see buildArgs' flag-ordering
	// comment) — cli_args are appended AFTER `exec`, so a cli_args copy of
	// this flag is, at best, a parse-time error today and, at worst, could
	// silently change behavior on a future codex version; (b) any value
	// other than "never" reintroduces an approval gate with no TTY to answer
	// it in Omnipus's headless spawn, which would either hang the run until
	// the FR-5.4 timeout or (per review) risk an undefined fail-open
	// approval on a CLI version that treats "cannot prompt" as "assume
	// yes" — either outcome silently defeats the ADR-032 fix D
	// non-interactive guarantee, so the flag is dropped conservatively
	// regardless of the specific value supplied.
	"codex": {
		{
			name: "--dangerously-bypass-approvals-and-sandbox", boolean: true,
			reason: "re-enables a full sandbox/approval bypass the driver deliberately never passes (FR-5.3)",
		},
		{
			name: "--sandbox",
			valueCheck: func(v string) bool {
				return strings.EqualFold(v, "danger-full-access")
			},
			reason: `escalates --sandbox to "danger-full-access", which OpenAI's own docs describe as removing the filesystem and network boundaries — beyond the driver's own workspace-write posture`,
		},
		{
			name:   "--ask-for-approval",
			reason: `reintroduces an approval gate with no TTY to answer it in Omnipus's headless spawn; the driver's own "--ask-for-approval never" is the intended non-interactive posture and any override is dropped regardless of value`,
		},
		// --json: driver_codex.go's parseLine expects codex exec's structured
		// "--json" NDJSON event stream (item.completed / turn.completed /
		// error / turn.failed — see the package doc there). Disabling it
		// switches codex to human-readable text output the parser cannot
		// read, silently corrupting the streamed transcript (correctness
		// guard, not security — see the package doc above). Like
		// --ask-for-approval, there is no safe "narrower" variant to
		// preserve, so ANY operator-supplied --json token — a bare repeat or
		// an "--json=false"-shaped disable — is dropped conservatively
		// (boolean:true drops both forms; see filterDangerousCLIArgs).
		{
			name:    "--json",
			boolean: true,
			reason:  "the driver's NDJSON stream parser requires --json output; disabling it would silently corrupt the streamed transcript instead of erroring",
		},
	},
	// opencode: the driver's OWN --dangerously-skip-permissions is the
	// deliberate ADR-032 fix D auto-approve posture (tracked separately as
	// C-1; NOT in scope to strip here) and is added to argv before this
	// filter ever runs, so it is never touched. This entry only catches a
	// REDUNDANT copy of the same flag supplied via operator cli_args, so
	// argv never carries two copies of it and cli_args can never be mistaken
	// for the source of that decision.
	"opencode": {
		{
			name:    "--dangerously-skip-permissions",
			boolean: true,
			reason:  "redundant copy of the driver's own auto-approve flag, which is already applied unconditionally (ADR-032 fix D) — operator cli_args cannot be the source of this decision",
		},
		// --format: driver_opencode.go's parseLine expects "opencode run
		// --format json" NDJSON event shapes (message.start / content.delta /
		// tool.start / session.complete / error — see the package doc
		// there). Any other value ("text", or an unrecognized format)
		// silently corrupts the streamed transcript instead of erroring
		// (correctness guard, not security — see the package doc above).
		// Only the driver's own value is a safe match; every other value is
		// dropped.
		{
			name: "--format",
			valueCheck: func(v string) bool {
				return !strings.EqualFold(v, "json")
			},
			reason: "the driver's NDJSON stream parser requires --format json; any other value would silently corrupt the streamed transcript instead of erroring",
		},
	},
}

// droppedCLIArg pairs a dropped cli_args flag descriptor (same "--flag" or
// "--flag value" shape filterDangerousCLIArgs has always logged) with the
// dangerousCLIFlag.reason that caused it to be dropped.
type droppedCLIArg struct {
	flag   string
	reason string
}

// filterDangerousCLIArgs removes known-dangerous override flags from an
// operator-supplied cli_args argv slice for the given CLI (ADR-032 fix M-1).
// It returns the args with those flags (and, where applicable, their value
// token) removed, plus one descriptor per dropped FLAG for the caller to log
// (mirroring mergeEnvOverrides' one-WARN-per-offending-key pattern): a
// boolean or "--flag=value" token drops as itself; a two-token "--flag value"
// pair drops as a single space-joined "--flag value" entry so one WARN
// covers the whole flag, not each argv token separately.
//
// This function ONLY ever inspects opts.CLIArgs — it must never be applied to
// a driver's own built-in flags.
//
// This is a thin wrapper over filterDangerousCLIArgsDetailed, kept so the
// three drivers' buildArgs (which only need the flag strings, for
// logDroppedCLIArgs) and their existing tests are unaffected by the richer
// (flag, reason) shape that filterDangerousCLIArgsDetailed also computes.
func filterDangerousCLIArgs(cli string, args []string) (kept []string, dropped []string) {
	kept, detailed := filterDangerousCLIArgsDetailed(cli, args)
	if len(detailed) == 0 {
		return kept, nil
	}
	dropped = make([]string, len(detailed))
	for i, d := range detailed {
		dropped[i] = d.flag
	}
	return kept, dropped
}

// filterDangerousCLIArgsDetailed is the shared implementation behind both
// filterDangerousCLIArgs (flag-only, used by the drivers' own buildArgs at
// real dispatch time) and FilterDangerousCLIArgsDetailed (flag+reason,
// exported for pkg/gateway's executor-command-preview endpoint so an
// operator sees WHY a cli_args token would be dropped before saving, not
// just that it would be).
func filterDangerousCLIArgsDetailed(cli string, args []string) (kept []string, dropped []droppedCLIArg) {
	deny := dangerousCLIFlagsByCLI[cli]
	if len(deny) == 0 || len(args) == 0 {
		return args, nil
	}

	kept = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasEq := splitFlagArg(arg)

		flag, isDangerFlag := lookupDangerousFlag(deny, name)
		if !isDangerFlag {
			kept = append(kept, arg)
			continue
		}

		if flag.boolean {
			// Bare boolean flag, or an explicit "--flag=true"/"--flag=false"
			// override — either way it targets a flag the driver has
			// deliberately chosen never to pass itself; drop the token.
			dropped = append(dropped, droppedCLIArg{flag: arg, reason: flag.reason})
			continue
		}

		if hasEq {
			// "--flag=value" single-token form: evaluate the embedded value.
			if flag.valueCheck == nil || flag.valueCheck(value) {
				dropped = append(dropped, droppedCLIArg{flag: arg, reason: flag.reason})
			} else {
				kept = append(kept, arg)
			}
			continue
		}

		// "--flag value" two-token form. Only treat the NEXT token as the
		// value when it doesn't itself look like another flag; otherwise the
		// value can't be determined and, per the documented heuristic ("when
		// in doubt about a flag taking a value, err toward dropping just the
		// flag token"), only the flag token is dropped so we never
		// accidentally swallow an unrelated following flag as if it were
		// this flag's value.
		if i+1 < len(args) && !looksLikeFlagToken(args[i+1]) {
			nextVal := args[i+1]
			if flag.valueCheck == nil || flag.valueCheck(nextVal) {
				// Record the flag+value pair as ONE dropped entry (space-joined)
				// so the caller logs a single WARN per offending flag, not one
				// per argv token — mirrors mergeEnvOverrides' one-WARN-per-key
				// contract.
				dropped = append(dropped, droppedCLIArg{flag: arg + " " + nextVal, reason: flag.reason})
			} else {
				kept = append(kept, arg, nextVal)
			}
			i++ // consume the value token either way — it belongs to this flag.
			continue
		}

		dropped = append(dropped, droppedCLIArg{flag: arg, reason: flag.reason})
	}
	return kept, dropped
}

// splitFlagArg splits a "--flag=value" token into its name and value parts.
// For a token with no "=" (or one that doesn't start with "-"), value is
// empty and hasEq is false.
func splitFlagArg(arg string) (name, value string, hasEq bool) {
	if !strings.HasPrefix(arg, "-") {
		return arg, "", false
	}
	if idx := strings.IndexByte(arg, '='); idx >= 0 {
		return arg[:idx], arg[idx+1:], true
	}
	return arg, "", false
}

// looksLikeFlagToken reports whether a raw argv token looks like a flag
// (starts with "-") rather than a plain value — used so a dangerous
// value-taking flag never mistakenly consumes a following flag as its value.
func looksLikeFlagToken(s string) bool {
	return strings.HasPrefix(s, "-")
}

// lookupDangerousFlag finds the denylist entry matching name, if any.
func lookupDangerousFlag(deny []dangerousCLIFlag, name string) (dangerousCLIFlag, bool) {
	for _, f := range deny {
		if f.name == name {
			return f, true
		}
	}
	return dangerousCLIFlag{}, false
}

// logDroppedCLIArgs logs one WARN per flag token dropped by
// filterDangerousCLIArgs, mirroring the mergeEnvOverrides
// warn-per-offending-key pattern (executor_opts.go) so an operator can see
// exactly which configured cli_args value was silently ignored and why.
func logDroppedCLIArgs(logPrefix, cli, runID string, dropped []string) {
	for _, tok := range dropped {
		slog.Warn(logPrefix+": operator cli_args flag conflicts with the driver's own safety posture — dropped",
			"run_id", runID, "cli", cli, "flag", tok)
	}
}
