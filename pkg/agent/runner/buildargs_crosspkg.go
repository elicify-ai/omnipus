// buildargs_crosspkg.go — minimal cross-package export of each driver's
// unexported buildArgs(), added SOLELY so pkg/gateway's executor-defaults
// drift-check test (rest_executor_defaults_test.go) can assert the REAL argv
// each driver constructs matches what GET /api/v1/agents/executor-defaults
// advertises to operators — instead of the two independently hand-maintained
// hardcoded string slices the endpoint previously shipped with, which proved
// nothing about the real drivers (agent-system-fixes-2 review, FIX 1:
// silent-failure/test-coverage/architect/correctness reviewers all flagged
// this as a drift risk). Production code never calls these — buildArgs()
// itself remains unexported and is invoked internally by each driver's own
// Run(). These wrappers exist purely to make that otherwise-private
// implementation detail testable from pkg/gateway, mirroring how
// driver_buildargs_test.go already exercises buildArgs() in-package for
// runner's own regression coverage.

package runner

// BuildClaudeArgs returns the argv ClaudeDriver.Run would pass to the
// `claude` binary for the given RunOptions, without starting any process.
func BuildClaudeArgs(opts RunOptions) []string {
	return NewClaudeDriver(nil).buildArgs(opts)
}

// BuildCodexArgs returns the argv CodexDriver.Run would pass to the `codex`
// binary for the given RunOptions, without starting any process.
func BuildCodexArgs(opts RunOptions) []string {
	return NewCodexDriver(nil).buildArgs(opts)
}

// BuildOpencodeArgs returns the argv OpencodeDriver.Run would pass to the
// `opencode` binary for the given RunOptions, without starting any process.
func BuildOpencodeArgs(opts RunOptions) []string {
	return NewOpencodeDriver(nil).buildArgs(opts)
}

// DroppedCLIArg pairs a dropped operator cli_args flag descriptor (the same
// "--flag" or "--flag value" shape filterDangerousCLIArgs has always logged)
// with a short, human-readable Reason explaining why it was denylisted.
// Exported for pkg/gateway's POST /api/v1/agents/executor-preview endpoint
// (rest_executor_preview.go), which surfaces both fields to the operator
// instead of silently dropping the token as the real dispatch path does.
type DroppedCLIArg struct {
	Flag   string
	Reason string
}

// FilterDangerousCLIArgsDetailed exports argsafety.go's
// filterDangerousCLIArgsDetailed — the same denylist filtering each driver's
// buildArgs applies to operator-supplied cli_args (ADR-032 fix M-1), but
// returning a (flag, reason) pair per dropped token instead of just the flag
// string filterDangerousCLIArgs (unexported, used internally by the drivers)
// returns. Added so the executor-command-preview endpoint can report WHY a
// cli_args token would be dropped at real dispatch time, not just that it
// would be. Calling this does not start any process and has no side effects
// beyond computing the same filtering decision each driver's buildArgs
// already makes.
func FilterDangerousCLIArgsDetailed(cli string, args []string) (kept []string, dropped []DroppedCLIArg) {
	kept, detailed := filterDangerousCLIArgsDetailed(cli, args)
	if len(detailed) == 0 {
		return kept, nil
	}
	dropped = make([]DroppedCLIArg, len(detailed))
	for i, d := range detailed {
		dropped[i] = DroppedCLIArg{Flag: d.flag, Reason: d.reason}
	}
	return kept, dropped
}

// LooksLikeProviderModel exports driver_opencode.go's looksLikeProviderModel
// so the executor-command-preview endpoint can independently detect the same
// "not shaped like provider/model" condition BuildOpencodeArgs already acts
// on internally (omitting --model), in order to populate the response's
// model_dropped_reason field. Calling this has no side effects.
func LooksLikeProviderModel(model string) bool {
	return looksLikeProviderModel(model)
}

// DefaultCLIBinary returns the bare executable name the given driver spawns
// when ExecutorConfig.cli_path is empty (resolved via the OS $PATH at real
// spawn time) — i.e. each driver's own claudeBinName / codexBinName /
// opencodeBinName package var, the single source of truth resolveCLIBinary
// falls back to. cli must be one of "claude-code", "codex", "opencode"
// (ExternalCliTool); any other value returns "". Exported so pkg/gateway's
// executor-command-preview endpoint can populate the response's "binary"
// field for the "cli_path empty" case without redeclaring these names.
func DefaultCLIBinary(cli string) string {
	switch cli {
	case "claude-code":
		return claudeBinName
	case "codex":
		return codexBinName
	case "opencode":
		return opencodeBinName
	default:
		return ""
	}
}

// FilterKey maps the wire ExternalCliTool enum value ("claude-code") to the
// internal denylist key argsafety.go's dangerousCLIFlagsByCLI map is keyed by
// ("claude") — codex and opencode's wire and internal names happen to
// coincide, so only claude-code needs remapping. Exported so pkg/gateway's
// executor-preview and executor-smoke-test endpoints share ONE
// implementation of this special case instead of each independently
// re-deriving it via a bare string comparison on the untyped `string(cli)`
// — a duplicated "claude-code" -> "claude" mapping in two files is a
// footgun for a future edit that drops it from just one, silently turning
// argsafety's filter into a no-op for claude-code with zero error surfaced.
// Deliberately string-typed (not the generated gen.ExternalCliTool) to avoid
// adding pkg/api/generated as a new dependency of this package, matching the
// sibling exports above (DefaultCLIBinary, FilterDangerousCLIArgsDetailed).
// cli values other than "claude-code" are returned unchanged — including any
// future/unrecognized value, since FilterDangerousCLIArgsDetailed already
// no-ops safely on an unrecognized key via dangerousCLIFlagsByCLI's
// zero-value map lookup, so passing one through here is not itself unsafe.
func FilterKey(cli string) string {
	if cli == "claude-code" {
		return "claude"
	}
	return cli
}
