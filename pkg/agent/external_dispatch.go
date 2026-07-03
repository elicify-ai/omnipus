// external_dispatch.go — Spec-4 wiring: run a delegated sub-agent via an external
// CLI runner instead of the native Omnipus agent loop.
//
// This is the production wiring site for ExecutorKind="external-cli". The native
// path (spawnSubTurn → al.runTurn) is unchanged and remains the default; this file
// is reached ONLY when the resolved sub-agent's SubagentsConfig.Executor resolves
// to runner.DispatchKindExternalCLI (see runner.ResolveDispatch).
//
// Flow (FR-5.1 / FR-5.2 / FR-5.4; FR-5.3 as amended by ADR-032):
//
//  1. Resolve the run's working directory — the DELEGATE's own workspace
//     directory (ADR-032, 2026-07: runs execute IN the agent's real workspace,
//     not an isolated git-worktree/temp-dir copy — see
//     docs/internal/architecture/ADR-032-external-agent-workspace-execution.md).
//     The external CLI's OWN sandbox remains the kernel boundary (operator
//     decision — no new Omnipus confiner).
//  2. NewDriver — instantiate the claude-code / codex / opencode driver.
//  3. Run — drive the CLI with the delegated input + a per-run timeout and turn
//     cap derived from sub-turn config, with the delegate's own model auto-set.
//  4. Stream events through ConsentDispatcher: permission-requests route to the
//     Omnipus consent layer (best-effort post-hoc — a DENY cancels the run); all
//     events (output/tool-call/diff/error) are written to the sub-agent session
//     transcript so the SPA renders the run inline.
//
// No teardown step: the workspace directory is the agent's persistent working
// tree, not a scratch dir — it is left in place after the run.

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/agent/runner"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
	"github.com/dapicom-ai/omnipus/pkg/session"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// executorConfigOf returns the sub-agent executor config for an AgentInstance, or
// nil (→ native) when the agent declares no Subagents block. Nil is handled by
// runner.ResolveDispatch (EffectiveKind defaults to native).
func executorConfigOf(a *AgentInstance) *config.ExecutorConfig {
	if a == nil || a.Subagents == nil {
		return nil
	}
	return a.Subagents.Executor
}

// workspaceRunLocks serializes external-CLI runs that target the SAME
// workspace directory (FIX 4 / arch #2 warning). ADR-032 removed worktree
// isolation — external-CLI sub-turns now run directly in the delegate
// agent's own persistent workspace — so two concurrent sub-turns delegating
// to the same agent (or to two agents that happen to share a workspace
// path) would otherwise spawn two child CLI processes reading/writing the
// same directory concurrently: file races, a corrupted git index, clobbered
// output files. Keyed by the workspace's cleaned path; the mutex is held for
// the FULL duration of the child run (acquired near the top of
// runExternalCLISubTurn, released via defer), which is the window in which
// the child process touches the workspace tree. Runs against distinct
// workspace paths are never blocked by each other — only same-path runs
// serialize.
//
// Entries are never removed from the map: a long-running gateway
// accumulates one *sync.Mutex per distinct workspace path ever dispatched
// to. That set is bounded by the number of configured external-cli
// agents/workspaces (small, operator-controlled), not by run count, so the
// unbounded map is an accepted trade-off rather than a leak.
var workspaceRunLocks sync.Map // map[string]*sync.Mutex

// acquireWorkspaceRunLock locks the mutex for workDir's cleaned path,
// creating it on first use via LoadOrStore, and returns the unlock func.
// Callers MUST `defer` the returned func so the lock releases even on an
// early return or panic.
func acquireWorkspaceRunLock(workDir string) func() {
	key := filepath.Clean(workDir)
	muAny, _ := workspaceRunLocks.LoadOrStore(key, &sync.Mutex{})
	mu, _ := muAny.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// runExternalCLISubTurn executes a delegated sub-agent task through an external
// CLI runner. It returns a tools.ToolResult mirroring the native sub-turn return
// shape (ForLLM/ForUser carry the run's aggregated output; Err is set on failure).
//
// childTS is the sub-turn's turnState (used for transcript writes + agent ID).
// task is the delegated input prompt. timeout bounds the whole run.
//
// childTS.agent is the resolved DELEGATE's own AgentInstance (spawnSubTurn
// sources Workspace/Model/MaxIterations/Subagents from the TARGET named by
// TargetAgentID when dispatch resolves to external-cli — see subturn.go), so
// every field read below already reflects the delegate's own identity, not
// the delegating parent's.
func runExternalCLISubTurn(
	ctx context.Context,
	al *AgentLoop,
	childTS *turnState,
	task string,
	timeout time.Duration,
) (*tools.ToolResult, error) {
	agent := childTS.agent
	if agent == nil || agent.Subagents == nil || agent.Subagents.Executor == nil {
		return nil, fmt.Errorf("external-cli dispatch: missing executor config")
	}
	cli := agent.Subagents.Executor.CLI
	if cli == "" {
		return nil, fmt.Errorf("external-cli dispatch: executor.cli is empty (set claude-code, codex, or opencode)")
	}

	runID := childTS.turnID
	if runID == "" {
		runID = fmt.Sprintf("ext-%d", time.Now().UnixNano())
	}

	// 1. Resolve the run's working directory: the DELEGATE'S OWN workspace
	//    directory (ADR-032, 2026-07). External-CLI runs now execute directly
	//    in the agent's workspace — NOT an isolated git-worktree/temp-dir copy
	//    — so the CLI can see (and write to) the files the delegating
	//    conversation actually cares about. This intentionally relaxes the
	//    original Spec-4 FR-5.3 worktree-isolation boundary for external CLIs
	//    specifically; see
	//    docs/internal/architecture/ADR-032-external-agent-workspace-execution.md.
	//    The external CLI's OWN sandbox (Codex = Landlock/seccomp; Claude
	//    Code = its permission model; opencode = its own tool gating) remains
	//    the enforced security boundary — Omnipus adds no new confiner either
	//    way, isolated or not. No teardown is needed: this is the agent's
	//    persistent workspace, not a scratch directory.
	workDir := strings.TrimSpace(agent.Workspace)
	if workDir == "" {
		return nil, fmt.Errorf("external-cli dispatch: agent workspace is empty — cannot run %s", cli)
	}
	if mkErr := os.MkdirAll(workDir, 0o755); mkErr != nil {
		return nil, fmt.Errorf("external-cli dispatch: ensuring workspace dir %q: %w", workDir, mkErr)
	}

	// FIX 4 (concurrency, arch #2 warning): serialize external-CLI runs that
	// share this workspace directory — see workspaceRunLocks' doc comment.
	// Held for the whole run (driver instantiation through the drain loop
	// below) since that is the window during which the child process can
	// touch the workspace tree. A different workspace path is never blocked
	// by this.
	releaseWorkspaceLock := acquireWorkspaceRunLock(workDir)
	defer releaseWorkspaceLock()

	// 2. Build the consent handler (wraps the gateway PolicyApprover; deny-by-default
	//    when no approver is wired — loadToolApprover returns the fail-closed nop).
	consent := &policyApproverConsent{
		al:        al,
		agentID:   childTS.agentID,
		sessionID: childTS.transcriptSessionID,
		turnID:    childTS.turnID,
	}

	// 3. Instantiate the driver for the configured CLI. The factory is a package
	//    var so in-package tests can inject a fake/stub driver without a real CLI.
	driver, err := newExternalDriver(cli, consent)
	if err != nil {
		return nil, fmt.Errorf("external-cli dispatch: %w", err)
	}

	// 4. Bound the run: a per-run timeout + turn cap (FR-5.4).
	timeoutSecs := int(timeout.Seconds())
	if timeoutSecs <= 0 {
		timeoutSecs = int(defaultSubTurnTimeout.Seconds())
	}
	maxTurns := agent.MaxIterations
	if maxTurns <= 0 {
		maxTurns = DefaultExternalMaxTurns
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// FIX 5: hoist the repeated strings.TrimSpace(agent.Model) computation
	// (previously done independently for the transcript model stamp below
	// and again for RunOptions.Model further down) into a single local.
	agentModel := strings.TrimSpace(agent.Model)

	// Phase 1B FR-013: attribute the external-CLI sub-turn's transcript
	// output to the agent's configured model. The external CLI runs its
	// own LLM, but we record what model the agent would have used in the
	// non-CLI path — consistent with how chat turns are attributed.
	childTS.setLastProducedModel(agentModel)

	// SECURITY (Spec-4 FR-5.3 / SEC-23): the spawned external CLI must NOT inherit
	// the full gateway environment — that would leak OMNIPUS_MASTER_KEY (and every
	// other gateway secret) into a third-party binary. ScrubGatewayEnvForRunner
	// returns os.Environ() filtered through the generic child allowlist UNIONED with
	// the narrow runner-credential allowlist (the model-provider API keys the CLI
	// legitimately needs to authenticate). Passing it as RunOptions.Env makes the
	// driver use it as the COMPLETE child env (buildChildEnv) — no os.Environ()
	// fallback, so the master key never reaches the child.
	childEnv := sandbox.ScrubGatewayEnvForRunner()

	// FR-5.3 egress allowlist: inject HTTP_PROXY/HTTPS_PROXY pointing at the
	// runner egress proxy so the CLI's HTTP/HTTPS traffic is forced through a
	// loopback proxy with SSRF internal-CIDR blocking (prevents the CLI from
	// reaching internal services / cloud metadata). Per ADR-019 FR-5.3 there is
	// NO new confiner — the CLI self-sandboxes; Omnipus controls egress via
	// proxy injection. Graceful degradation: an empty address (proxy could not
	// start) means no injection — the run proceeds under the CLI's own sandbox +
	// the workspace FS boundary.
	childEnv = injectRunnerEgressProxy(childEnv, al.runnerEgressProxyAddr())

	// MAJ-5: consume the agent's ExecutorConfig (cli_path / cli_args /
	// env_overrides) when spawning the external CLI. cli_path overrides the
	// driver's default binary (else $PATH); cli_args is tokenised into argv
	// (execve, no shell — warn-not-reject on shell-metacharacters); env_overrides
	// merge into the scrubbed child env (OMNIPUS_* keys are dropped by the driver
	// env builder, so the master key / agent-identity vars stay protected).
	execCfg := agent.Subagents.Executor
	cliArgs := runner.ParseCLIArgs(execCfg.CLIArgs, runID)

	evCh, err := driver.Run(runCtx, runner.RunOptions{
		RunID:          runID,
		WorkDir:        workDir,
		Input:          task,
		Env:            childEnv,
		TimeoutSeconds: timeoutSecs,
		MaxTurns:       maxTurns,
		CLIPath:        execCfg.CLIPath,
		CLIArgs:        cliArgs,
		EnvOverrides:   execCfg.EnvOverrides,
		// Fix C: auto-set the delegate's own configured model on the external
		// CLI invocation. Each driver's buildArgs guards so an unmapped/empty
		// model string is simply omitted rather than passed as garbage.
		Model: agentModel,
	})
	if err != nil {
		return nil, fmt.Errorf("external-cli dispatch: driver start (%s): %w", cli, err)
	}

	slog.Info("external-cli dispatch: run started",
		"run_id", runID, "cli", cli, "work_dir", workDir,
		"timeout_s", timeoutSecs, "max_turns", maxTurns, "agent_id", childTS.agentID)

	// 5. Route events through the consent dispatcher and drain into the transcript.
	//    ConsentDispatcher answers permission-requests (Decide) and forwards every
	//    event to `out` so we can record it. It runs until evCh closes or ctx ends;
	//    we close `out` on its exit so the drain loop below terminates cleanly
	//    (ConsentDispatcher does not own `out`'s lifetime).
	out := make(chan runner.RunEvent, 64)
	go func() {
		defer close(out)
		runner.ConsentDispatcher(runCtx, evCh, driver, runID, childTS.transcriptSessionID, consent, out)
	}()

	result := drainExternalRun(runCtx, childTS, runID, cli, out, consent)
	return result, result.Err
}

// drainExternalRun consumes the runner's event stream, mirrors each event into the
// sub-agent session transcript, and aggregates the run's textual output into a
// tools.ToolResult. It returns when the stream closes (run end/error) or ctx ends.
//
// consent carries the post-hoc consent state. A DENY decision cancels the run
// (the driver kills the process and suppresses its own ctx-cancel error), so the
// stream can close with runErr==nil and ended==false. Without consulting the
// consent state we would mis-report that path as success. We therefore treat a
// recorded denial as a terminal failure: the delegating agent MUST be told the
// sub-run was aborted, not that it completed (review finding [MAJOR]).
func drainExternalRun(
	ctx context.Context,
	childTS *turnState,
	runID, cli string,
	out <-chan runner.RunEvent,
	consent *policyApproverConsent,
) *tools.ToolResult {
	var sb strings.Builder
	var runErr error
	ended := false

	for {
		select {
		case <-ctx.Done():
			runErr = ctx.Err()
			goto done
		case ev, ok := <-out:
			if !ok {
				goto done
			}
			switch ev.Kind {
			case runner.EventKindStart:
				// FR-5.6 / N3: the run's first event pins the external CLI version.
				// Log it so the run log records which stream schema the run used;
				// an unknown version (graceful-degradation) is surfaced at WARN.
				if ev.Start != nil {
					if ev.Start.VersionKnown {
						slog.Info("external-cli dispatch: run started",
							"run_id", runID, "cli", ev.Start.CLI, "version", ev.Start.Version)
					} else {
						slog.Warn(
							"external-cli dispatch: run started with unknown/unpinned CLI version — graceful degradation",
							"run_id",
							runID,
							"cli",
							ev.Start.CLI,
							"version",
							ev.Start.Version,
						)
					}
				} else {
					slog.Info("external-cli dispatch: run started", "run_id", runID, "cli", cli)
				}
			case runner.EventKindOutput:
				if ev.Output != nil && ev.Output.Text != "" {
					sb.WriteString(ev.Output.Text)
					childTS.appendIntermediateAssistantTranscript(ev.Output.Text, transcriptModelFor(childTS.agent))
				}
			case runner.EventKindToolCall:
				if ev.ToolCall != nil {
					recordExternalToolCall(childTS, ev.ToolCall)
				}
			case runner.EventKindDiff:
				if ev.Diff != nil {
					txt := fmt.Sprintf("diff %s:\n%s", ev.Diff.Path, ev.Diff.Diff)
					childTS.appendIntermediateAssistantTranscript(txt, transcriptModelFor(childTS.agent))
				}
			case runner.EventKindPermissionRequest:
				// Already routed to consent by ConsentDispatcher; record for the
				// transcript so the SPA can show the pending/decided approval.
				if ev.PermissionRequest != nil {
					recordExternalPermission(childTS, ev.PermissionRequest)
				}
			case runner.EventKindToolResult:
				// Tool result completion from the external runner: mirror it into the
				// transcript so the run shows the tool's outcome.
				if ev.ToolResult != nil {
					recordExternalToolResult(childTS, ev.ToolResult)
				}
			case runner.EventKindEnd:
				ended = true
			case runner.EventKindError:
				if ev.Err != nil {
					msg := ev.Err.Message
					childTS.appendIntermediateAssistantTranscript(
						"[external-cli error] "+msg,
						transcriptModelFor(childTS.agent),
					)
					if ev.Err.Fatal {
						runErr = fmt.Errorf("external-cli run failed: %s", msg)
					}
				}
			}
		}
	}

done:
	output := strings.TrimSpace(sb.String())
	// Persist the aggregated final content as the assistant transcript entry so a
	// replay reconstructs the run's result (mirrors the native finalContent path).
	if output != "" {
		childTS.appendAssistantTranscript(output, transcriptModelFor(childTS.agent))
	}

	// A recorded consent DENY is terminal: the driver cancels the run (and
	// suppresses its own runCtx-cancel error), so we would otherwise see
	// runErr==nil, ended==false and wrongly report success. Surface the denial
	// as the run's outcome so the delegating agent learns the sub-run was aborted.
	denied, denyReason := consent.lastDeny()

	slog.Info("external-cli dispatch: run finished",
		"run_id", runID, "cli", cli, "ended", ended, "denied", denied,
		"err", runErr, "output_len", len(output))

	if runErr != nil {
		return &tools.ToolResult{
			Err:    runErr,
			ForLLM: fmt.Sprintf("External CLI run (%s) failed: %v", cli, runErr),
		}
	}

	if denied {
		// The run did not complete on its own (a successful EventKindEnd was never
		// followed by a denial in practice — the deny cancels before/at the kill),
		// so treat any recorded denial as an aborted run regardless of `ended`.
		reason := strings.TrimSpace(denyReason)
		if reason == "" {
			reason = "permission denied"
		}
		denyErr := fmt.Errorf("external-cli run denied: %s", reason)
		msg := fmt.Sprintf(
			"External CLI run (%s) was DENIED and aborted (a permission request was rejected: %s). "+
				"The delegated task did NOT complete.", cli, reason)
		childTS.appendIntermediateAssistantTranscript(
			"[external-cli denied] "+reason,
			transcriptModelFor(childTS.agent),
		)
		return &tools.ToolResult{
			Err:     denyErr,
			ForLLM:  msg,
			ForUser: msg,
		}
	}

	if output == "" {
		output = fmt.Sprintf("External CLI run (%s) completed with no textual output.", cli)
	}
	return &tools.ToolResult{
		ForLLM:  output,
		ForUser: output,
	}
}

// recordExternalToolCall mirrors an external runner tool-call event into the
// sub-agent session transcript as a tool_call entry.
func recordExternalToolCall(childTS *turnState, tc *runner.ToolCallEvent) {
	id := tc.CallID
	if id == "" {
		id = fmt.Sprintf("ext-tool-%d", time.Now().UnixNano())
	}
	var args map[string]any
	if len(tc.ToolInput) > 0 {
		if uErr := json.Unmarshal(tc.ToolInput, &args); uErr != nil {
			slog.Debug("external-cli dispatch: tool-call input is not a JSON object",
				"tool", tc.ToolName, "call_id", id, "err", uErr)
		}
	}
	// The external CLI emits the tool-call event when the call STARTS; Omnipus
	// consent is post-hoc and we never observe the call's own success/failure from
	// the stream. Recording "success" would assert an outcome we did not verify.
	// Use "completed" — the call was transcribed/observed, with no success claim
	// (review finding [MINOR]).
	childTS.appendToolCallTranscript(session.ToolCall{
		ID:         session.ToolCallID(id),
		Tool:       tc.ToolName,
		Status:     "completed",
		Parameters: args,
	})
}

// recordExternalPermission records a permission-request as a transcript line so the
// run's gated actions are visible in replay. The decision itself is routed by the
// ConsentDispatcher; this is observability only.
func recordExternalPermission(childTS *turnState, pr *runner.PermissionRequestEvent) {
	childTS.appendIntermediateAssistantTranscript(
		fmt.Sprintf("[external-cli permission] tool=%q: %s", pr.ToolName, pr.Description),
		transcriptModelFor(childTS.agent))
}

// recordExternalToolResult mirrors a completed external tool result into the
// sub-agent session transcript. The paired tool-call event was already recorded
// (with Status="completed" + the call's Parameters) when the call started via
// recordExternalToolCall; this call updates that existing entry in-place with
// the result + final status rather than appending a second transcript line with
// the same tool-call ID (which previously produced duplicate tool_call entries
// on replay — review finding S1).
//
// If no matching pending entry is found (defensive: the start event was lost or
// this is a result without a prior call), it falls back to appending a new entry.
//
// The in-place update reads the transcript JSONL, rewrites the matching line,
// and writes the file back atomically under an advisory flock. This mirrors the
// read-modify-rewrite pattern used by UnifiedStore.MarkLastEntryTruncated.
// Because the store's AppendTranscript is guarded by an in-process mutex (not
// flock), a narrow in-process race with a concurrent append from a sibling
// sub-turn sharing this transcript session remains: in the worst case a
// sibling's appended line lands in the read→rewrite gap and is overwritten.
// That is accepted for this [MINOR] transcript-cleanliness fix; a structural
// fix would add an exported UpdateToolCallResult method on UnifiedStore.
func recordExternalToolResult(childTS *turnState, tr *runner.ToolResultEvent) {
	id := tr.CallID
	if id == "" {
		id = fmt.Sprintf("ext-tool-result-%d", time.Now().UnixNano())
	}
	status := "success"
	if tr.IsError {
		status = "error"
	}
	var result map[string]any
	if len(tr.Output) > 0 {
		if err := json.Unmarshal(tr.Output, &result); err != nil {
			// Not a JSON object — store as a raw string under "output".
			result = map[string]any{"output": string(tr.Output)}
		}
	}

	// S1: try to update the existing pending tool-call entry in-place. Only
	// attempt this when a transcript store + session are configured and the
	// turn has not been abandoned (appendToolCallTranscript applies the same
	// guards, so we mirror them here to avoid a pointless rewrite).
	if !childTS.abandoned.Load() &&
		childTS.transcriptStore != nil && childTS.transcriptSessionID != "" {
		if recordExternalToolResultUpdateInPlace(childTS, session.ToolCallID(id), status, result) {
			return
		}
	}

	// Defensive fallback: no matching pending entry found, or the in-place
	// rewrite failed. Append a new transcript line so the result is not lost.
	childTS.appendToolCallTranscript(session.ToolCall{
		ID:         session.ToolCallID(id),
		Tool:       tr.ToolName,
		Status:     status,
		Parameters: nil,
		Result:     result,
	})
}

// recordExternalToolResultUpdateInPlace finds the most recent transcript entry
// of type tool_call whose ToolCall.ID matches callID and Status is "completed"
// (the entry written by recordExternalToolCall when the call started), and
// updates that ToolCall in-place with the final status + result. It returns
// true when an entry was found and the on-disk rewrite succeeded, false
// otherwise (caller falls back to appending).
func recordExternalToolResultUpdateInPlace(
	childTS *turnState,
	callID session.ToolCallID,
	status string,
	result map[string]any,
) bool {
	store := childTS.transcriptStore
	sessionID := childTS.transcriptSessionID
	transcriptPath := filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl")

	var found bool
	rewriteErr := fileutil.WithFlock(transcriptPath, func() error {
		found = false
		data, err := os.ReadFile(transcriptPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil // no transcript yet — nothing to update
			}
			return err
		}

		// Split into raw lines (preserving malformed lines so the file layout
		// is unchanged on rewrite).
		rawLines := bytes.Split(data, []byte{'\n'})
		targetIdx := -1
		var updatedEntry session.TranscriptEntry
		for i, raw := range rawLines {
			line := bytes.TrimSpace(raw)
			if len(line) == 0 {
				continue
			}
			var entry session.TranscriptEntry
			if jErr := json.Unmarshal(line, &entry); jErr != nil {
				// Skip malformed lines (matches ReadTranscript behavior).
				continue
			}
			if entry.Type != session.EntryTypeToolCall {
				continue
			}
			// Find a ToolCall on this entry with the matching ID whose Status
			// is still "completed" (the start-of-call entry written by
			// recordExternalToolCall). Walk the entry's ToolCalls slice.
			for j := range entry.ToolCalls {
				if entry.ToolCalls[j].ID != callID {
					continue
				}
				if entry.ToolCalls[j].Status != "completed" {
					// Already has a final status — leave it (idempotent guard
					// against double-processing the same result event).
					continue
				}
				targetIdx = i
				updatedEntry = entry
				updatedEntry.ToolCalls[j].Status = status
				updatedEntry.ToolCalls[j].Result = result
				break
			}
			if targetIdx == i {
				break
			}
		}
		if targetIdx == -1 {
			return nil // no matching pending entry — signal fallback
		}

		rewritten, mErr := json.Marshal(updatedEntry)
		if mErr != nil {
			return mErr
		}
		rawLines[targetIdx] = rewritten

		var buf bytes.Buffer
		for i, line := range rawLines {
			// Skip a trailing empty line produced by a final-newline split so
			// the file does not grow a blank line on each rewrite.
			if i == len(rawLines)-1 && len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			buf.Write(line)
			buf.WriteByte('\n')
		}
		if wErr := fileutil.WriteFileAtomic(transcriptPath, buf.Bytes(), 0o600); wErr != nil {
			return wErr
		}
		found = true
		return nil
	})

	if rewriteErr != nil {
		slog.Warn("external-cli dispatch: in-place tool-result transcript update failed; falling back to append",
			"session_id", sessionID, "tool_call_id", string(callID), "err", rewriteErr)
		return false
	}
	return found
}

// DefaultExternalMaxTurns bounds an external run when the agent declares no
// MaxIterations (FR-5.4 turn cap). Exported (not just package-internal) so
// pkg/gateway's POST /api/v1/agents/executor-preview endpoint
// (rest_executor_preview.go) can default its own previewed --max-turns to
// the IDENTICAL value real dispatch applies when max_tool_iterations is
// omitted — referencing this constant directly rather than a second
// hardcoded "50" means the two can never drift apart.
const DefaultExternalMaxTurns = 50

// transcriptModelFor returns the model string to stamp on transcript entries
// produced by an external-CLI sub-turn. It mirrors the trim applied in
// setLastProducedModel so the variadic and the single-slot stamp agree
// (W4-9 — variadic surface is now USED, not just declared).
func transcriptModelFor(agent *AgentInstance) string {
	if agent == nil {
		return ""
	}
	return strings.TrimSpace(agent.Model)
}

// newExternalDriver is the driver factory used by runExternalCLISubTurn. It is a
// package var (not a direct call to runner.NewDriver) solely so in-package tests
// can inject a fake/stub ExternalAgentRunner and exercise the full dispatch flow
// (worktree → run → stream → consent → teardown) without a real external CLI on
// PATH. Production always uses runner.NewDriver.
var newExternalDriver = func(cli string, consent runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
	return runner.NewDriver(cli, consent)
}

// policyApproverConsent adapts the gateway-wired PolicyApprover (al.loadToolApprover)
// to the runner.ConsentHandler interface so external-CLI permission requests flow
// through the same human-in-the-loop approval path as native ask-policy tools.
//
// Deny-by-default: loadToolApprover never returns nil — when no approver is wired it
// returns the fail-closed nopPolicyApprover, which denies every request. So a request
// reaching here without a gateway approver is denied, satisfying FR-5.1.
type policyApproverConsent struct {
	al        *AgentLoop
	agentID   string
	sessionID string
	turnID    string

	// mu guards the deny-tracking fields. RequestConsent runs on the consent
	// dispatcher goroutine while drainExternalRun reads via lastDeny() on the
	// dispatch goroutine, so the access must be synchronized.
	mu         sync.Mutex
	denied     bool
	denyReason string
}

// RequestConsent routes an external-runner permission request to the policy approver
// and returns its verdict. Implements runner.ConsentHandler.
//
// A DENY is recorded so the dispatch path can surface the run as aborted: a denial
// cancels the run (the driver kills the process and suppresses its ctx-cancel
// error), which would otherwise look like a clean, no-output success.
func (c *policyApproverConsent) RequestConsent(ctx context.Context, req runner.ConsentRequest) (bool, string) {
	approver := c.al.loadToolApprover()
	approved, reason := approver.RequestApproval(ctx, PolicyApprovalReq{
		ToolCallID: req.RequestID,
		ToolName:   req.ToolName,
		Args:       req.Arguments,
		AgentID:    c.agentID,
		SessionID:  c.sessionID,
		TurnID:     c.turnID,
	})
	if !approved {
		c.mu.Lock()
		c.denied = true
		c.denyReason = reason
		c.mu.Unlock()
	}
	return approved, reason
}

// lastDeny reports whether any permission request in this run was denied, and the
// reason of the most recent denial. Safe for concurrent use.
func (c *policyApproverConsent) lastDeny() (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.denied, c.denyReason
}

// compile-time interface assertion
var _ runner.ConsentHandler = (*policyApproverConsent)(nil)
