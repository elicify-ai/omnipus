// rest_executor_smoketest.go — POST /api/v1/agents/executor-smoke-test.
//
// Stateless, agent-agnostic: actually RUNS a trivial, real prompt through an
// external-CLI worker's real dispatch path (the same driver.Run() a genuine
// subagent_3p delegation uses — pkg/agent/runner/driver_{claude,codex,
// opencode}.go via runner.NewDriver) and returns the real response. Distinct
// from:
//   - POST /agents/executor-preview (rest_executor_preview.go): computes argv
//     only, never spawns anything.
//   - POST /agents/{id}/runner/test and POST /system/cli-validate
//     (rest_clivalidate.go): binary-present → version handshake →
//     credential-file-presence only, WITHOUT running any real work (no model
//     tokens spent) — see runner/conntest.go's doc.
//
// This endpoint DOES spend real model tokens and DOES run a real,
// authenticated subprocess, so it is meaningfully more sensitive than either
// of the above. It is hardened the same way rest_clivalidate.go hardens its
// own caller-influenced spawn, PLUS the additional controls a real (not
// --version-only) run needs:
//
//   - Auth: withAuth at CREATE-PARITY, inherited from HandleAgents'
//     registration (api.withAuth(api.HandleAgents) in gateway.go) — same as
//     executor-preview and executor-defaults, which are carved out of the
//     same route.
//   - Rate limit + per-caller in-flight cap: a DEDICATED pair
//     (smokeTestLimiter, smokeTestInflight), more conservative than
//     cli-validate's (cliValidateLimiter, cliValidateInflight) — see those
//     vars' doc comments for the justification (this endpoint is materially
//     more expensive per call: real tokens + a real subprocess held for up to
//     smokeTestTimeoutSeconds, vs. a ~15s zero-token --version probe).
//   - Pre-spawn guard: the resolved target (cli_path override, else the
//     driver's own default binary name) MUST be a regular, executable file on
//     $PATH — reuses isRegularExecutableFile (rest_clivalidate.go) — before
//     any subprocess is started.
//   - Bounded run: a fixed, hardcoded trivial prompt (not operator-
//     configurable in this pass), a small MaxTurns cap, and a short
//     TimeoutSeconds — see the consts below for the exact values and
//     rationale.
//   - Dangerous cli_args are filtered via the SAME denylist executor-preview
//     already surfaces (runner.FilterDangerousCLIArgsDetailed) before ever
//     reaching RunOptions — defense in depth on top of each driver's own
//     internal filtering in buildArgs().
//   - Consent: every event is routed through the REAL runner.ConsentDispatcher
//     / runner.RouteConsent, with a nil ConsentHandler — deliberately DIFFERENT
//     from production dispatch (pkg/agent/external_dispatch.go's
//     policyApproverConsent, which auto-approves every request unconditionally,
//     issue #488). A smoke test's whole purpose is validating a binary+auth+
//     handshake without letting a real tool call run, so it intentionally keeps
//     the nil-handler deny-by-default path: RouteConsent denies AND audit-logs
//     every permission request by default (FR-5.1), and ConsentDispatcher calls
//     driver.Decide(decision) so the driver actually cancels the run on a
//     denial (see runner/consent.go's POST-HOC CONSENT LIMITATION doc) instead
//     of the denial being silently dropped until the outer timeout fires — see
//     drainSmokeTestRun's EventKindPermissionRequest case below.
//   - Scrubbed child env: sandbox.ScrubGatewayEnvForRunner() — the same
//     allowlist real dispatch uses (pkg/agent/external_dispatch.go) — so the
//     spawned CLI never inherits gateway secrets (OMNIPUS_MASTER_KEY, bearer
//     tokens, ...).
//   - Working directory: when the request's agent_id names a real, saved
//     agent, the run happens in THAT agent's own dedicated directory
//     ($OMNIPUS_HOME/agents/{id}/, AgentConfig.Home — the same one
//     agent.ResolveAgentHome / a real subagent_3p delegation would
//     use) — UNLESS that agent is a member of a real pkg/workspace.Workspace's
//     CoreTeam (workspace.FindForAgent), in which case the smoke-test runs in
//     that Workspace's dedicated project-work subdirectory
//     (workspaces/{id}/work/, workspace.SafeWorkDir) instead, mirroring
//     exactly what a genuine dispatch to that agent does today (both
//     pkg/agent/loop.go's
//     runTurn and pkg/agent/external_dispatch.go's runExternalCLISubTurn
//     apply the same CoreTeam-membership override — see either's doc comment
//     for the design). Using the agent's real resolved directory makes the
//     test representative of real behavior (project files,
//     AGENTS.md/CLAUDE.md/opencode.json context) — that directory is
//     NEVER removed by this handler. Otherwise (agent_id omitted, blank, or
//     naming an agent that doesn't resolve — e.g. the create wizard before
//     the agent has been saved) falls back to a fresh, dedicated ephemeral
//     scratch directory under $OMNIPUS_HOME/executor-smoke-test-runs,
//     removed (defer os.RemoveAll) once the run ends, success or failure. See
//     runExecutorSmokeTest's isEphemeralWorkspace local for how the two paths
//     stay mutually exclusive.
//   - Audit: exactly one audit event {cli, binary, ok} per call
//     (audit.EventExecutorSmokeTest) — deliberately NEVER response_text (the
//     model's actual answer) or cli_args, either of which could plausibly
//     carry operator-sensitive content; a boolean outcome + the CLI identity
//     is enough for an audit trail's purpose here.
//
// Note: this endpoint is reachable before an agent exists (create wizard) or
// is saved, and for opencode specifically this means one click grants the
// CLI's fully-bypassed permission mode (opencode's own always-on
// --dangerously-skip-permissions, ADR-032 fix D — see driver_opencode.go) on
// a real, credentialed subprocess — bounded by the rate limiter, the
// smokeTestTimeoutSeconds timeout, and the smokeTestMaxTurns cap, but a
// deliberate tradeoff consistent with this endpoint's stateless,
// agent-agnostic design, not an oversight.

package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// smokeTestPrompt is the fixed, hardcoded trivial test prompt run through the
// real dispatch path. Deliberately NOT operator-configurable in this pass
// (out of scope) — a predictable, cheap arithmetic question any working
// CLI+model combination answers in one turn, so a non-answer is a meaningful
// signal rather than a prompt-design artifact.
const smokeTestPrompt = "What is 1+1? Reply with only the digit, nothing else."

// smokeTestMaxTurns bounds the run to a small number of agentic turns. 3 is
// ample for one-line arithmetic; needing more turns to answer it signals
// something is wrong (a misbehaving CLI/model looping), and the cap cutting
// the run short there is the cap doing its job, not a bug.
const smokeTestMaxTurns = 3

// smokeTestTimeoutSeconds bounds the whole run's wall-clock time, passed as
// RunOptions.TimeoutSeconds (each driver enforces it internally via its own
// context.WithTimeout — see e.g. driver_claude.go's Run). 30s is generous for
// a trivial prompt (process startup + one real model round trip) while
// keeping a caller-triggered spawn short-lived; a var (not const) so tests
// can shorten it to exercise the timeout path without a real 30s wait.
var smokeTestTimeoutSeconds = 30

// smokeTestDrainGrace is added on top of smokeTestTimeoutSeconds for the
// HANDLER's own context deadline — a belt-and-suspenders backstop on top of
// RunOptions.TimeoutSeconds (which each driver enforces internally): enough
// slack for the driver to notice its own timeout, emit its terminal
// EventKindError, and close the channel, without the handler's own deadline
// racing the driver's cleanup. If a driver ever fails to close its channel at
// all, this is what still bounds the handler's response time.
const smokeTestDrainGrace = 10 * time.Second

// smokeTestCancelGrace bounds how long runExecutorSmokeTest waits, after the
// outer context ends (client disconnect, or the smokeTestDrainGrace
// backstop), for the driver to confirm its subprocess has actually exited
// (its raw event channel closing) before proceeding to `defer
// os.RemoveAll(workDir)`. See drainUntilClosedOrGrace's doc for why this
// matters and why `out` (the consent-routed channel) is not itself a
// reliable signal for this wait.
const smokeTestCancelGrace = 3 * time.Second

// smokeTestRunsSubdir is the $OMNIPUS_HOME subdirectory holding ephemeral
// per-run scratch directories for executor-smoke-test runs. Deliberately a
// SIBLING of runner.RunsRoot()'s "runner-runs" (not reused): the reaper
// (pkg/agent/runner/reaper.go) scans specifically "runner-runs" for orphans
// left behind by a crashed REAL dispatch, so this namespace must not be
// swept into it. A smoke-test run is PRIMARILY torn down by this handler
// itself (defer os.RemoveAll, success or failure, plus drainUntilClosedOrGrace
// waiting out a client-disconnect race — see runExecutorSmokeTest) — but
// that assumption was proven not airtight (a crash, or a wait that outruns
// smokeTestCancelGrace, can still leak a dir), so gateway.go additionally
// runs sweepSmokeTestOrphans() as a boot-time backstop for this namespace
// specifically, mirroring ReapOrphans' "everything present at boot is an
// orphan" reasoning without needing the git-worktree-aware teardown that
// function has (every entry here is a plain os.MkdirTemp dir, never a
// worktree).
const smokeTestRunsSubdir = "executor-smoke-test-runs"

// smokeTestLimiter is a DEDICATED, more conservative rate limiter than
// cliValidateLimiter (20/min): this endpoint spends real model tokens and
// holds a real subprocess for up to smokeTestTimeoutSeconds, materially more
// expensive per call than cli-validate's zero-token `--version` probe (~15s
// cap, no tokens). 5/min per IP is ample for an operator manually verifying a
// config change while remaining well short of a meaningful cost-abuse
// budget.
var smokeTestLimiter = newAPIRateLimiter(5, 1*time.Minute)

// smokeTestNewDriver is the driver factory used by runExecutorSmokeTest. It
// is a package var (not a direct runner.NewDriver call) SOLELY so in-package
// tests can inject a fake/stub runner.ExternalAgentRunner and exercise the
// handler's rate-limit/in-flight-cap logic deterministically, without a real
// subprocess's timing. This mirrors pkg/agent/external_dispatch.go's own
// newExternalDriver package var, added for the identical reason. Production
// always uses runner.NewDriver. Tests that need to prove the REAL dispatch
// path (argv construction, env scrubbing, NDJSON parsing) leave this at its
// default and drive a real stub-script binary via cli_path instead — see
// rest_executor_smoketest_test.go's happy-path/error/timeout tests.
var smokeTestNewDriver = runner.NewDriver

// smokeTestInflight caps concurrent in-flight smoke tests PER CALLER at 1 —
// tighter than cliValidateInflight's 2, since one held slot here occupies a
// real subprocess + real token spend for up to smokeTestTimeoutSeconds (vs.
// cli-validate's ~15s zero-token version probe). A DISTINCT instance from
// cliValidateInflight is used deliberately rather than sharing it: the two
// endpoints have very different cost profiles, and coupling their budgets
// would let cheap cli-validate traffic from one caller starve that same
// caller's smoke-test slot (or vice versa) for no operational benefit — each
// gets its own, independently tunable budget instead.
var smokeTestInflight = newInflightLimiter(1)

// postAgentsExecutorSmokeTest handles POST /api/v1/agents/executor-smoke-test
// (operationId postAgentsExecutorSmokeTest). Decodes and validates the
// request body, enforces the dedicated rate limit + per-caller in-flight cap,
// runs a real bounded test turn through the real driver dispatch path in an
// ephemeral scratch workspace, audits the outcome, and returns the result.
// Always responds 200 once a verdict is reached (including a bounded-out or
// failed run) — the outcome rides in the body's ok/error fields, matching
// cli-validate's convention of using the body for domain-level failure.
func (a *restAPI) postAgentsExecutorSmokeTest(w http.ResponseWriter, r *http.Request) {
	// No method check here: this handler's only call site (HandleAgents in
	// rest.go) already gates on POST before ever reaching this function —
	// mirrors its sibling postAgentsExecutorPreview, which correctly omits
	// the same redundant re-check.

	// Dedicated rate limit (smokeTestLimiter — see its doc for why this is a
	// separate, more conservative budget than cliValidateLimiter). Applied
	// inline rather than via the withRateLimit registration wrapper other
	// dedicated-limiter endpoints use, because this endpoint shares its
	// route's registration (api.withAuth(api.HandleAgents)) with the rest of
	// /api/v1/agents/* — it is carved out of agentID inside the handler body,
	// not given its own top-level route like /system/cli-validate.
	ip := clientIP(r)
	if !smokeTestLimiter.allow(ip) {
		retryAfter := smokeTestLimiter.retryAfter(ip)
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		slog.Warn("executor-smoke-test: rate limit exceeded", "ip", ip, "retry_after", retryAfter)
		jsonErr(w, http.StatusTooManyRequests, fmt.Sprintf("rate limit exceeded, retry after %d seconds", retryAfter))
		return
	}

	var req gen.ExecutorSmokeTestRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "ExecutorSmokeTestRequest", &req, validateEnabled) {
		return
	}

	cli := string(req.Cli)
	if !requireSupportedCLI(w, cli) {
		return
	}

	// Per-caller in-flight cap (mirrors cli-validate's FR-013 pattern) —
	// acquire BEFORE any spawn; a caller already at the cap is rejected fast
	// (no queue, no silent wait).
	key := cliValidateCallerKey(r)
	if !smokeTestInflight.acquire(key) {
		w.Header().Set("Retry-After", "1")
		jsonErr(w, http.StatusTooManyRequests, "too many concurrent smoke tests in flight; retry shortly")
		return
	}
	defer smokeTestInflight.release(key)

	model := derefTrimStr(req.Model)
	cliPath := derefTrimStr(req.CliPath)
	cliArgsRaw := derefStr(req.CliArgs)
	agentID := derefTrimStr(req.AgentId)

	resp, resolvedBinary := a.runExecutorSmokeTest(r.Context(), cli, model, cliPath, cliArgsRaw, agentID)
	a.auditExecutorSmokeTest(r, cli, resolvedBinary, resp)
	jsonOK(w, resp)
}

// smokeTestOK builds a successful ExecutorSmokeTestResponse: ok=true,
// response_text populated, error left nil. Paired with smokeTestFail so the
// invariant "exactly one of response_text/error is populated, gated by ok"
// — which nothing in the generated type itself enforces — is maintained at
// ONE pair of construction sites instead of six independent struct literals
// that each had to get it right by hand. usedAgentWorkspace threads through
// to the response's required used_agent_workspace field so every call site
// (both helpers, all callers) carries it — see runExecutorSmokeTest's
// isEphemeralWorkspace local for how the value is derived.
func smokeTestOK(responseText string, durationMs int, usedAgentWorkspace bool) gen.ExecutorSmokeTestResponse {
	return gen.ExecutorSmokeTestResponse{
		Ok:                 true,
		ResponseText:       strPtr(responseText),
		DurationMs:         durationMs,
		UsedAgentWorkspace: usedAgentWorkspace,
	}
}

// smokeTestFail builds a failed ExecutorSmokeTestResponse: ok=false, error
// populated, response_text left nil. See smokeTestOK's doc.
func smokeTestFail(errMsg string, durationMs int, usedAgentWorkspace bool) gen.ExecutorSmokeTestResponse {
	return gen.ExecutorSmokeTestResponse{
		Ok:                 false,
		Error:              strPtr(errMsg),
		DurationMs:         durationMs,
		UsedAgentWorkspace: usedAgentWorkspace,
	}
}

// runExecutorSmokeTest performs the actual dispatch without any HTTP
// concern, so it is directly unit-testable. It returns the wire response
// plus the resolved binary path/name (for the caller's audit record — the
// wire response schema deliberately does not carry this).
func (a *restAPI) runExecutorSmokeTest(
	ctx context.Context,
	cli, model, cliPath, cliArgsRaw, agentID string,
) (gen.ExecutorSmokeTestResponse, string) {
	start := time.Now()
	durationMs := func() int { return int(time.Since(start).Milliseconds()) }

	// Pre-spawn guard (mirrors validateCLI's fail-closed pre-spawn check in
	// rest_clivalidate.go): resolve the spawn target the SAME way the runtime
	// spawn does, before guarding it, so the guard is authoritative over what
	// would actually be exec'd. Unlike cli-validate (which treats an empty
	// cli_path as missing-binary WITHOUT a spawn — it validates a
	// caller-typed override specifically), an empty cli_path here is the
	// NORMAL "use the configured default" case, matching executor-preview and
	// real dispatch — so it resolves to the driver's own default binary name
	// via $PATH instead of short-circuiting.
	target := cliPath
	if target == "" {
		target = runner.DefaultCLIBinary(cli)
	}
	resolvedTarget, lpErr := exec.LookPath(target)
	if lpErr != nil || !isRegularExecutableFile(resolvedTarget) {
		return smokeTestFail(
			fmt.Sprintf("%q CLI binary not found or not executable (%s)", cli, target),
			durationMs(), false,
		), target
	}
	resolvedBinary := absResolvePath(target)

	// Working-directory resolution: when agentID names a real, saved agent,
	// run in THAT agent's own dedicated directory — the same place a genuine
	// subagent_3p delegation to it would run (agent.ResolveAgentHome,
	// the same helper NewAgentInstance uses in-package). This is
	// AgentConfig.Home, the separate, multi-agent pkg/workspace.Workspace
	// feature (CoreTeam/REST-CRUD/delegation graph). If agentID is ALSO a
	// member of a real Workspace's CoreTeam, that Workspace's own shared
	// directory overrides the agent's private one below — mirroring the exact
	// override real dispatch applies (pkg/agent/loop.go's runTurn,
	// pkg/agent/external_dispatch.go's runExternalCLISubTurn), so the smoke
	// test stays representative of where a genuine turn would actually run.
	// Otherwise (agentID empty, or naming an agent that no longer resolves —
	// e.g. the create wizard before the agent has been saved, or a
	// stale/deleted id) fall back to a disposable ephemeral scratch
	// directory, exactly as before this feature existed.
	//
	// isEphemeralWorkspace is set EXACTLY ONCE, right here, at the point
	// workDir itself is decided — every later use (the cleanup defer just
	// below, and the usedAgentWorkspace value threaded into every
	// smokeTestOK/smokeTestFail call for the rest of this function) reads
	// this same bool rather than re-deriving "was an agent used" from some
	// other proxy (e.g. re-checking agentID != ""), so the cleanup-vs-no-
	// cleanup decision cannot drift out of sync with which directory is
	// actually in workDir. A real agent's own directory must NEVER be
	// deleted by this handler, under any circumstance (success, error,
	// timeout, or the context-cancellation race smokeTestCancelGrace exists
	// to wait out below).
	var (
		workDir              string
		isEphemeralWorkspace bool
	)
	cfg := a.agentLoop.GetConfig()
	if agentCfg := findAgentConfig(cfg, agentID); agentCfg != nil {
		resolved := agent.ResolveAgentHome(agentCfg, &cfg.Agents.Defaults)
		// CoreTeam override: an agent that belongs to a Workspace's team runs
		// in the Workspace's dedicated project-work subdirectory
		// (workspaces/<id>/work/, workspace.EnsureWorkDir) instead of its
		// private one — same rule real dispatch applies (see the file-level
		// doc above). workspace.EnsureWorkDir (not the bare SafeWorkDir this
		// used before) also idempotently auto-inits the git evidence repo for
		// that work dir, same as the real native/external-cli dispatch paths
		// (pkg/agent/workspace_reroot.go), so a smoke-test run that lands in
		// a real workspace's work/ dir arms the same evidence layer a genuine
		// turn would. Deliberately not the workspace's own root directory:
		// that also holds AGENT.md and the shared memory room, which a
		// generic write_file/edit_file confined here must not be able to
		// reach. Not found, an unsafe workspace id, or a directory-creation
		// failure is not a hard error here: it just means the agent's own
		// directory (already resolved above) is used, same as before this
		// override existed — the shared os.MkdirAll(resolved, ...) below
		// still hard-fails the smoke test if THAT directory is unusable too.
		if wsID, found := workspace.FindForAgent(config.OmnipusHomeDir(), agentID); found {
			if wsDir, wsErr := workspace.EnsureWorkDir(config.OmnipusHomeDir(), wsID); wsErr == nil {
				resolved = wsDir
			} else {
				slog.Warn("executor-smoke-test: workspace-team dir resolution failed; using agent's own directory",
					"agent_id", agentID, "workspace_id", wsID, "error", wsErr)
			}
		}
		// Defensive MkdirAll: agent.ResolveAgentHome is pure path
		// computation (see its doc) — the directory is normally already
		// created, either at agent-creation time (rest.go's
		// agentWorkspacePath, called from createAgent/getAgent/listAgents) or
		// at first AgentInstance construction (pkg/agent.NewAgentInstance),
		// both of which os.MkdirAll(…, 0o755) the SAME path this resolves to.
		// Neither is guaranteed to have run yet for every config-present
		// agent (e.g. an agent added via a raw config edit, or tested
		// immediately after a fresh boot before its AgentInstance has been
		// constructed), so this mirrors that exact mode rather than inventing
		// a new convention.
		if mkErr := os.MkdirAll(resolved, 0o755); mkErr != nil {
			return smokeTestFail(
				fmt.Sprintf("could not prepare agent directory: %v", mkErr),
				durationMs(), false,
			), resolvedBinary
		}
		workDir = resolved
		isEphemeralWorkspace = false
	} else {
		root := filepath.Join(config.OmnipusHomeDir(), smokeTestRunsSubdir)
		if mkRootErr := os.MkdirAll(root, 0o700); mkRootErr != nil {
			return smokeTestFail(
				fmt.Sprintf("could not prepare scratch workspace: %v", mkRootErr),
				durationMs(), false,
			), resolvedBinary
		}
		dir, mkErr := os.MkdirTemp(root, "run-")
		if mkErr != nil {
			return smokeTestFail(
				fmt.Sprintf("could not create scratch workspace: %v", mkErr),
				durationMs(),
				false,
			), resolvedBinary
		}
		workDir = dir
		isEphemeralWorkspace = true
	}
	usedAgentWorkspace := !isEphemeralWorkspace
	// Ephemeral scratch workspace — NEVER a real agent's persistent
	// workspace — removed unconditionally once the run ends, success or
	// failure. Gated strictly on isEphemeralWorkspace: when a real agent's
	// workspace is in use, this is a deliberate no-op.
	defer func() {
		if !isEphemeralWorkspace {
			return
		}
		if rmErr := os.RemoveAll(workDir); rmErr != nil {
			slog.Warn("executor-smoke-test: failed to remove ephemeral workspace",
				"dir", workDir, "err", rmErr)
		}
	}()

	runID := fmt.Sprintf("smoke-%d", time.Now().UnixNano())

	// filterKey is the internal denylist key each driver's OWN buildArgs
	// passes to filterDangerousCLIArgs — "claude", not the wire
	// "claude-code"; codex/opencode's wire and internal names coincide.
	// Shared with executor-preview via the ONE runner.FilterKey helper (see
	// its doc) instead of each endpoint re-deriving this mapping
	// independently.
	filterKey := runner.FilterKey(cli)
	parsedArgs := runner.ParseCLIArgs(cliArgsRaw, runID)
	// Defense in depth: filter dangerous cli_args here too, on top of each
	// driver's own internal filterDangerousCLIArgs call in buildArgs() — a
	// dangerous arg cannot sneak into a REAL run through either path. Dropped
	// tokens are already logged by the driver's own internal call
	// (logDroppedCLIArgs), so the detail return here is intentionally
	// discarded — this call's only job is to make sure the ARGV actually
	// passed to Run never carries a denylisted flag.
	safeArgs, _ := runner.FilterDangerousCLIArgsDetailed(filterKey, parsedArgs)

	// Belt-and-suspenders context: RunOptions.TimeoutSeconds is what each
	// driver enforces internally (its own context.WithTimeout, firing at
	// exactly smokeTestTimeoutSeconds and emitting its own EventKindError —
	// see driver_claude.go's Run). This outer deadline is strictly WIDER
	// (+smokeTestDrainGrace) so the driver's own timeout always fires first
	// in the normal case; it only matters as a backstop if a driver were to
	// fail to close its event channel after its own timeout fires.
	runCtx, cancel := context.WithTimeout(
		ctx, time.Duration(smokeTestTimeoutSeconds)*time.Second+smokeTestDrainGrace)
	defer cancel()

	// Consent: a trivial arithmetic prompt should never need to call a tool.
	// NewDriver's consent parameter only stores a reference on the returned
	// driver for the driver's OWN later use (e.g. a future interactive
	// resume path) — no driver reads it internally to gate anything today.
	// Deny-by-default is NOT implemented here; it is enforced below by
	// routing every event through the real runner.ConsentDispatcher with a
	// nil handler (see the file-header "Consent" bullet). Passing nil here
	// too keeps the driver's own stored field consistent with that posture.
	driver, err := smokeTestNewDriver(cli, nil)
	if err != nil {
		return smokeTestFail(
			fmt.Sprintf("could not construct driver: %v", err),
			durationMs(),
			usedAgentWorkspace,
		), resolvedBinary
	}

	evCh, runErr := driver.Run(runCtx, runner.RunOptions{
		RunID:          runID,
		WorkDir:        workDir,
		Input:          smokeTestPrompt,
		Env:            sandbox.ScrubGatewayEnvForRunner(),
		TimeoutSeconds: smokeTestTimeoutSeconds,
		MaxTurns:       smokeTestMaxTurns,
		CLIPath:        cliPath,
		CLIArgs:        safeArgs,
		Model:          model,
	})
	if runErr != nil {
		return smokeTestFail(
			fmt.Sprintf("failed to start %s: %v", cli, runErr),
			durationMs(),
			usedAgentWorkspace,
		), resolvedBinary
	}

	slog.Info("executor-smoke-test: run started",
		"run_id", runID, "cli", cli, "timeout_s", smokeTestTimeoutSeconds, "max_turns", smokeTestMaxTurns)

	// Route every event through the REAL runner.ConsentDispatcher (the same
	// wiring pkg/agent/external_dispatch.go uses for production dispatch) —
	// NOT a bespoke drain that only understands output/error events and
	// silently drops permission requests. A nil handler makes RouteConsent
	// deny + audit-log every request (FR-5.1); ConsentDispatcher then calls
	// driver.Decide(decision), which the driver treats as a run-canceling
	// denial. drainSmokeTestRun's EventKindPermissionRequest case (below)
	// surfaces this as a specific error the instant the (already-decided)
	// event is forwarded, rather than waiting for that cancellation to
	// unwind into the generic timeout path.
	out := make(chan runner.RunEvent, 64)
	go func() {
		defer close(out)
		runner.ConsentDispatcher(runCtx, evCh, driver, runID, "", nil, out)
	}()

	responseText, ok, errMsg := drainSmokeTestRun(runCtx, runID, out)

	// If runCtx ended (client disconnect, or this handler's own
	// smokeTestDrainGrace backstop — NOT the normal "the run finished"
	// path), the subprocess's exec.CommandContext is a CHILD of this same
	// ctx so a kill has been REQUESTED, but "requested" is not "has
	// exited" — drainSmokeTestRun's ctx.Done() branch returns without
	// waiting for that. Returning now would let the `defer
	// os.RemoveAll(workDir)` above race a still-alive subprocess that may
	// still be writing into the very directory about to be removed.
	// Explicitly Cancel() (idempotent; belt-and-suspenders on top of the
	// already-propagated ctx cancellation) and wait — bounded by
	// smokeTestCancelGrace so a driver that never closes its channel still
	// cannot hang the handler forever — for evCh (the driver's OWN raw
	// channel, not `out`: ConsentDispatcher stops reading evCh as soon as
	// ITS ctx is Done too, so `out` closing is not a reliable "the
	// subprocess is gone" signal here) to actually close before proceeding.
	if runCtx.Err() != nil {
		driver.Cancel()
		drainUntilClosedOrGrace(evCh, smokeTestCancelGrace)
	}

	var resp gen.ExecutorSmokeTestResponse
	if ok {
		resp = smokeTestOK(responseText, durationMs(), usedAgentWorkspace)
	} else {
		resp = smokeTestFail(errMsg, durationMs(), usedAgentWorkspace)
	}

	// The response body's Error field is already sanitized (never raw
	// provider/CLI text — see sanitizeSmokeTestErrorMessage's doc), so it is
	// safe to fold into this log line even though gateway.log can end up in
	// operator hands more broadly than the HTTP response. It is what lets
	// "see gateway.log for details" (the sanitized copy told to the caller)
	// actually resolve to something here; the RAW message (when it differs)
	// is logged separately, tied to the same run_id, inside
	// drainSmokeTestRun's EventKindError case below.
	finishArgs := []any{"run_id", runID, "cli", cli, "ok", ok, "duration_ms", resp.DurationMs}
	if !ok && resp.Error != nil {
		finishArgs = append(finishArgs, "error", *resp.Error)
	}
	slog.Info("executor-smoke-test: run finished", finishArgs...)

	return resp, resolvedBinary
}

// smokeTestPermissionDeniedErr is the specific, non-generic error surfaced
// when a smoke-test run attempts a tool call — deny-by-default (FR-5.1)
// means this always aborts the run, so a caller must be told exactly why the
// run failed rather than seeing the generic timeout message a silently
// dropped permission-request event would otherwise fall through to.
const smokeTestPermissionDeniedErr = "the CLI attempted a tool call, which is denied by default for this test"

// sanitizeSmokeTestErrorMessage is the fail-closed sanitizer between a
// runner's raw error text and the smoke-test response body. The smoke
// test's response body is operator-visible (POST /api/v1/agents/executor-smoke-test
// returns it directly) — Wave 1 (ADR-051 §RD5 MAJ-005) requires it to
// carry only generic, classifier-routed copy, NEVER raw provider/CLI text.
//
// The classifier runs on a synthetic ProviderError carrying only the
// message (no status — runner ev.Err has no HTTP context); the result's
// Message is one of the userMessages entries, generic over server text.
// For the unclassified case (CodeUnknown or empty classifier output) the
// function emits a fixed, deliberately generic fallback — the load-bearing
// invariant: the smoke-test response NEVER carries raw provider/CLI text,
// regardless of classification outcome.
//
// Two shapes are short-circuited before the classifier (both indicate
// smoke-test-internal sentinels, not external provider text):
//   - the literal smokeTestPermissionDeniedErr passes through verbatim
//     (deny-by-default — the operator must be told exactly why).
//   - any message containing "timeout" is emitted as a generic timeout
//     copy (the prior "The AI service encountered an error." generic
//     message swallowed the load-bearing timing signal).
func sanitizeSmokeTestErrorMessage(rawMessage string) string {
	if rawMessage == "" {
		return "The external CLI failed. See gateway.log for details."
	}
	if rawMessage == smokeTestPermissionDeniedErr {
		return smokeTestPermissionDeniedErr
	}
	if strings.Contains(strings.ToLower(rawMessage), "timeout") {
		return "The smoke test hit the timeout bound before completing. See gateway.log for details."
	}
	llm := agent.TranslateLLMError(&agent.ProviderError{Body: rawMessage, Err: nil}, rawMessage)
	if llm.Message == "" {
		return "The external CLI failed. See gateway.log for details."
	}
	return llm.Message
}

// drainSmokeTestRun consumes the CONSENT-ROUTED event stream (ch is the `out`
// channel runner.ConsentDispatcher forwards to — see runExecutorSmokeTest),
// concatenating every EventKindOutput chunk into the final response text
// (mirrors external_dispatch.go's drainExternalRun aggregation pattern — a
// strings.Builder accumulating ev.Output.Text). It returns as soon as a
// terminal condition is reached:
//
//   - EventKindPermissionRequest: by the time this event reaches ch,
//     ConsentDispatcher has ALREADY routed it through RouteConsent (denied,
//     since runExecutorSmokeTest wires a nil ConsentHandler — FR-5.1) and
//     called driver.Decide(false), which the driver treats as a
//     run-canceling denial (see runner/consent.go's POST-HOC CONSENT
//     LIMITATION doc). Return the specific smokeTestPermissionDeniedErr
//     immediately instead of waiting for that cancellation to unwind into
//     the generic timeout/EOF path below.
//   - EventKindError: ok=false, errMsg from the event (or a generic fallback
//     when the event carries no message). A "run exceeded timeout (FR-5.4)"
//     message from the driver's OWN internal timeout naturally satisfies the
//     "timeout-shaped error" requirement without any special-casing here.
//   - The channel closes (run ended, with or without an explicit
//     EventKindEnd — NOTE: a successful claude-code run's final "result" line
//     is itself emitted as EventKindOutput, not followed by a separate
//     EventKindEnd; external_dispatch.go's own drain loop treats channel
//     close as the authoritative "the run is over" signal for exactly this
//     reason, and this mirrors that): ok=true only when the accumulated text
//     is non-empty (the whole point of a smoke test is confirming a REAL
//     response came back — an empty-output "success" is not proof of
//     anything and is reported as a failure).
//   - ctx.Done() fires before any of the above: ok=false with a
//     timeout-shaped error (the backstop path — see smokeTestDrainGrace's
//     doc).
func drainSmokeTestRun(
	ctx context.Context,
	runID string,
	ch <-chan runner.RunEvent,
) (responseText string, ok bool, errMsg string) {
	var sb strings.Builder
	for {
		select {
		case <-ctx.Done():
			return strings.TrimSpace(
				sb.String(),
			), false, "executor smoke test timeout: run did not complete before the bound was reached"
		case ev, chOk := <-ch:
			if !chOk {
				out := strings.TrimSpace(sb.String())
				if out == "" {
					return out, false, "run completed with no textual output"
				}
				return out, true, ""
			}
			switch ev.Kind {
			case runner.EventKindOutput:
				if ev.Output != nil {
					sb.WriteString(ev.Output.Text)
				}
			case runner.EventKindError:
				msg := "run failed"
				if ev.Err != nil && ev.Err.Message != "" {
					msg = ev.Err.Message
				}
				sanitized := sanitizeSmokeTestErrorMessage(msg)
				if sanitized != msg {
					// The response body is sanitized on purpose (Wave 1 /
					// ADR-051 §RD5 MAJ-005 — never leak raw provider/CLI text
					// to the operator-visible response), but that sanitizer's
					// whole job is throwing the raw text away — if it is
					// thrown away here too, "See gateway.log for details"
					// (the sanitized copy the caller is told) is a dead end:
					// there is nothing in gateway.log to find. Log the raw
					// message server-side only, tied to run_id so it
					// correlates with the "run finished" completion log line
					// in runExecutorSmokeTest.
					//
					// Re-review FIX 1: this was a bare slog.Warn — self-
					// defeating, since slog.SetDefault is never called
					// anywhere in this repo, so log/slog.Default() never
					// reaches $OMNIPUS_HOME/logs/gateway.log on a
					// backgrounded gateway. The response's "See gateway.log
					// for details" pointed nowhere. Route through pkg/logger
					// instead — see rest_executor_smoketest_rawlog_test.go.
					logger.WarnCF("executor-smoketest", "run failed (raw error, server-side only)",
						map[string]any{"run_id": runID, "cli_error_raw": msg})
				}
				return strings.TrimSpace(sb.String()), false, sanitized
			case runner.EventKindPermissionRequest:
				return strings.TrimSpace(sb.String()), false, smokeTestPermissionDeniedErr
			case runner.EventKindStart, runner.EventKindToolCall, runner.EventKindDiff,
				runner.EventKindEnd, runner.EventKindToolResult:
				// Ignored for the smoke test's purposes: it is a trivial Q&A
				// that carries no further signal for a pass/fail verdict.
			}
		}
	}
}

// drainUntilClosedOrGrace discards every remaining event on ch — the
// driver's OWN raw event channel (evCh), NOT the consent-routed `out`
// runner.ConsentDispatcher writes to; ConsentDispatcher's own select also has
// a `<-ctx.Done()` case that returns immediately without draining evCh, so
// `out` closing is not a reliable "the subprocess actually exited" signal —
// until ch closes or graceTimeout elapses, whichever comes first. Uses its
// own time.Timer rather than deriving from the (already-canceled) outer ctx,
// so the wait itself is not immediately short-circuited by the very
// cancellation it exists to wait out.
func drainUntilClosedOrGrace(ch <-chan runner.RunEvent, graceTimeout time.Duration) {
	timer := time.NewTimer(graceTimeout)
	defer timer.Stop()
	for {
		select {
		case _, chOk := <-ch:
			if !chOk {
				return
			}
		case <-timer.C:
			return
		}
	}
}

// sweepSmokeTestOrphans removes every directory under
// $OMNIPUS_HOME/executor-smoke-test-runs left behind by a prior gateway
// process that never got to run its own `defer os.RemoveAll(workDir)` (see
// runExecutorSmokeTest) — a crash, SIGKILL, or a client-disconnect race that
// outran even drainUntilClosedOrGrace's bounded wait. Mirrors
// runner.ReapOrphans' "everything present at boot is an orphan" reasoning
// (pkg/agent/runner/reaper.go): this directory is a dedicated root nothing
// else writes to, and postAgentsExecutorSmokeTest only creates entries AFTER
// boot, so there is no live run to mis-classify by sweeping at boot, before
// any new smoke-test run can be dispatched. Unlike ReapOrphans, no
// git-worktree-aware teardown is needed: every entry here is a plain
// os.MkdirTemp scratch dir, never a git worktree, so a flat os.RemoveAll per
// entry suffices. Resilient: a single entry's removal failure is recorded and
// the sweep continues; a missing root is not an error (nothing to sweep).
func sweepSmokeTestOrphans() (removed int, errs []error) {
	root := filepath.Join(config.OmnipusHomeDir(), smokeTestRunsSubdir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, []error{fmt.Errorf("reading executor-smoke-test-runs root %q: %w", root, err)}
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			errs = append(errs, fmt.Errorf("removing %q: %w", dir, rmErr))
			slog.Warn("gateway: executor-smoke-test orphan sweep failed to remove dir", "dir", dir, "error", rmErr)
			continue
		}
		removed++
		slog.Info("gateway: executor-smoke-test orphan sweep removed dir", "dir", dir)
	}
	return removed, errs
}

// auditExecutorSmokeTest emits exactly one audit event per call carrying the
// authenticated caller (Entry.User, mirroring cli-validate's M-1
// attribution), the cli, the resolved binary, and the ok outcome (FR-013-
// style minimal record). Deliberately excludes response_text (the model's
// actual answer) and cli_args — either could plausibly carry
// operator-sensitive content, and a boolean outcome + the CLI identity is
// enough for an audit trail's purpose here. Best-effort: a nil auditor or a
// write error is logged and swallowed — audit failure must never fail the
// diagnostic.
func (a *restAPI) auditExecutorSmokeTest(
	r *http.Request,
	cli, resolvedBinary string,
	resp gen.ExecutorSmokeTestResponse,
) {
	if a.auditor == nil {
		return
	}
	decision := audit.DecisionDeny
	if resp.Ok {
		decision = audit.DecisionAllow
	}
	if err := a.auditor.Log(&audit.Entry{
		Event:    audit.EventExecutorSmokeTest,
		Decision: decision,
		User:     cliValidateAuditUser(r),
		Details: map[string]any{
			"cli":    cli,
			"binary": resolvedBinary,
			"ok":     resp.Ok,
		},
	}); err != nil {
		slog.Warn("audit write failed", "event", audit.EventExecutorSmokeTest, "error", err)
	}
}
