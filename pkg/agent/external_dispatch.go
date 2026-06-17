// external_dispatch.go — Spec-4 wiring: run a delegated sub-agent via an external
// CLI runner instead of the native Omnipus agent loop.
//
// This is the production wiring site for ExecutorKind="external-cli". The native
// path (spawnSubTurn → al.runTurn) is unchanged and remains the default; this file
// is reached ONLY when the resolved sub-agent's SubagentsConfig.Executor resolves
// to runner.DispatchKindExternalCLI (see runner.ResolveDispatch).
//
// Flow (FR-5.1 / FR-5.2 / FR-5.3 / FR-5.4):
//
//  1. PrepareWorkspace — a git worktree of the agent's workspace (or an isolated
//     temp dir if the workspace is not a git repo). This is Omnipus's FS boundary;
//     the external CLI's OWN sandbox is the kernel boundary (operator decision —
//     no new Omnipus confiner).
//  2. NewDriver — instantiate the claude-code / codex / opencode driver.
//  3. Run — drive the CLI with the delegated input + a per-run timeout and turn
//     cap derived from sub-turn config.
//  4. Stream events through ConsentDispatcher: permission-requests route to the
//     Omnipus consent layer (best-effort post-hoc — a DENY cancels the run); all
//     events (output/tool-call/diff/error) are written to the sub-agent session
//     transcript so the SPA renders the run inline.
//  5. Teardown — remove the worktree/temp dir, including on crash paths.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/agent/runner"
	"github.com/dapicom-ai/omnipus/pkg/config"
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

// runExternalCLISubTurn executes a delegated sub-agent task through an external
// CLI runner. It returns a tools.ToolResult mirroring the native sub-turn return
// shape (ForLLM/ForUser carry the run's aggregated output; Err is set on failure).
//
// childTS is the sub-turn's turnState (used for transcript writes + agent ID).
// task is the delegated input prompt. timeout bounds the whole run.
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

	// 1. Prepare an isolated filesystem boundary (worktree if the workspace is a
	//    git repo, else an isolated temp dir). Always torn down — including crash.
	ws, err := runner.PrepareWorkspace(ctx, runID, agent.Workspace)
	if err != nil {
		return nil, fmt.Errorf("external-cli dispatch: workspace prepare: %w", err)
	}
	defer func() {
		tdCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if tErr := ws.Teardown(tdCtx); tErr != nil {
			slog.Warn("external-cli dispatch: workspace teardown failed",
				"run_id", runID, "dir", ws.Dir, "err", tErr)
		}
	}()

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
		maxTurns = defaultExternalMaxTurns
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Phase 1B FR-013: attribute the external-CLI sub-turn's transcript
	// output to the agent's configured model. The external CLI runs its
	// own LLM, but we record what model the agent would have used in the
	// non-CLI path — consistent with how chat turns are attributed.
	if agent != nil {
		childTS.setLastProducedModel(strings.TrimSpace(agent.Model))
	}

	// SECURITY (Spec-4 FR-5.3 / SEC-23): the spawned external CLI must NOT inherit
	// the full gateway environment — that would leak OMNIPUS_MASTER_KEY (and every
	// other gateway secret) into a third-party binary. ScrubGatewayEnvForRunner
	// returns os.Environ() filtered through the generic child allowlist UNIONED with
	// the narrow runner-credential allowlist (the model-provider API keys the CLI
	// legitimately needs to authenticate). Passing it as RunOptions.Env makes the
	// driver use it as the COMPLETE child env (buildChildEnv) — no os.Environ()
	// fallback, so the master key never reaches the child.
	childEnv := sandbox.ScrubGatewayEnvForRunner()

	evCh, err := driver.Run(runCtx, runner.RunOptions{
		RunID:          runID,
		WorkDir:        ws.Dir,
		Input:          task,
		Env:            childEnv,
		TimeoutSeconds: timeoutSecs,
		MaxTurns:       maxTurns,
	})
	if err != nil {
		return nil, fmt.Errorf("external-cli dispatch: driver start (%s): %w", cli, err)
	}

	slog.Info("external-cli dispatch: run started",
		"run_id", runID, "cli", cli, "work_dir", ws.Dir, "mode", ws.Mode,
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

// stampExternalDispatchModel sets the child's lastProducedModel so the
// transcript writes from the external-CLI sub-turn attribute the output to
// the delegated agent's primary model. The external CLI itself runs its own
// LLM, but the model string we record is the agent's configured model —
// consistent with how the chat path records the model that was selected at
// turn start (FR-013 / Phase 1B).
func stampExternalDispatchModel(childTS *turnState, model string) {
	if childTS == nil {
		return
	}
	childTS.setLastProducedModel(model)
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
			case runner.EventKindOutput:
				if ev.Output != nil && ev.Output.Text != "" {
					sb.WriteString(ev.Output.Text)
					childTS.appendIntermediateAssistantTranscript(ev.Output.Text)
				}
			case runner.EventKindToolCall:
				if ev.ToolCall != nil {
					recordExternalToolCall(childTS, ev.ToolCall)
				}
			case runner.EventKindDiff:
				if ev.Diff != nil {
					txt := fmt.Sprintf("diff %s:\n%s", ev.Diff.Path, ev.Diff.Diff)
					childTS.appendIntermediateAssistantTranscript(txt)
				}
			case runner.EventKindPermissionRequest:
				// Already routed to consent by ConsentDispatcher; record for the
				// transcript so the SPA can show the pending/decided approval.
				if ev.PermissionRequest != nil {
					recordExternalPermission(childTS, ev.PermissionRequest)
				}
			case runner.EventKindEnd:
				ended = true
			case runner.EventKindError:
				if ev.Err != nil {
					msg := ev.Err.Message
					childTS.appendIntermediateAssistantTranscript("[external-cli error] " + msg)
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
		childTS.appendAssistantTranscript(output)
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
		childTS.appendIntermediateAssistantTranscript("[external-cli denied] " + reason)
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
		fmt.Sprintf("[external-cli permission] tool=%q: %s", pr.ToolName, pr.Description))
}

// defaultExternalMaxTurns bounds an external run when the agent declares no
// MaxIterations (FR-5.4 turn cap).
const defaultExternalMaxTurns = 50

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
		// External CLI tool calls do not carry Omnipus admin flags; treat as
		// non-admin (the CLI's own sandbox is the real boundary).
		RequiresAdmin: false,
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
