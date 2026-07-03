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
