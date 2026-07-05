// driver_claude.go — Claude Code streaming driver (FR-5.2, FR-5.6, US-4)
//
// Drives `claude -p --output-format stream-json --dangerously-skip-permissions`
// — full permission bypass, unconditional (operator decision, 2026-07-05,
// reversing FR-5.3/US-5's original claude-specific stance of using
// `--permission-mode acceptEdits` instead). Without this, every tool call the
// delegated CLI makes blocks on a permission fence with no reachable approval
// UI in practice, making subagent_3p delegation to claude-code effectively
// unusable. This now matches codex (`--ask-for-approval never`) and opencode
// (`--dangerously-skip-permissions`, ADR-032 §2.4), which already ran
// permission-bypassed unconditionally — claude-code was the one outlier.
//
// SECURITY NOTE: unlike codex (which keeps `--sandbox workspace-write`, a
// real kernel-level boundary, active in this mode) and unlike claude-code's
// OWN prior acceptEdits posture, `--dangerously-skip-permissions` disables
// Claude Code's internal sandbox too — the flag bundles "don't ask" and
// "don't confine" together, not separably. Omnipus builds no separate
// confiner for external-CLI workers (ADR-032/FR-5.3's original decision:
// rely on each CLI's own sandboxing). This means claude-code-backed
// subagent_3p workers now run with NO enforced sandbox boundary at all,
// matching opencode's pre-existing situation. See issue #488 for the tracked
// follow-up evaluating whether Omnipus needs its own confiner here.
//
// Claude Code stream-json event shapes (version 1.x / 2.x):
//
//	{"type":"system","subtype":"init","session_id":"<id>","tools":[...],...}
//	{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"..."},...]},...}
//	{"type":"tool_use","id":"<call_id>","name":"<tool>","input":{...},...}
//	{"type":"tool_result","tool_use_id":"<call_id>","content":"...",...}
//	{"type":"result","subtype":"success","result":"...","session_id":"<id>","usage":{...},...}
//	{"type":"result","subtype":"error_during_execution","error":"...","session_id":"<id>",...}
//
// With permissions fully bypassed, Claude Code no longer pauses on a
// permission fence at all — every tool_use event is followed immediately by
// its tool_result with no intervening approval wait. The driver still detects
// tool_use events and emits PermissionRequestEvents (routed through the same
// ConsentHandler codex/opencode use — see consent.go's POST-HOC CONSENT
// LIMITATION note), but this is now purely observability + a kill switch, the
// same as it already was for codex/opencode: Omnipus cannot veto an
// individual call, only cancel the whole run.

package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// knownClaudeVersionPrefixes lists version-string prefixes that this driver has
// been validated against (FR-5.6). Versions outside this list degrade gracefully
// with a WARN log but are not rejected — the JSON schema is stable enough.
var knownClaudeVersionPrefixes = []string{"1.", "2.", "0."}

// claudeBinName is the executable name invoked by the Claude driver. It is a
// package var (not a const) solely so in-package tests can point the driver at a
// stub process to exercise the full run lifecycle without a real CLI. Production
// never overrides it.
var claudeBinName = "claude"

// ClaudeDriver implements ExternalAgentRunner for the `claude` CLI (Claude Code).
// It drives `claude -p --output-format stream-json` and routes permission
// requests to the wired ConsentHandler.
//
// POST-HOC CONSENT LIMITATION (external-CLI; see consent.go package doc):
// `claude -p` is non-interactive and exposes no mid-run permission fence that
// Omnipus can answer. A tool_use event is surfaced as a PermissionRequestEvent
// for observability, but by the time Omnipus sees it the CLI has already begun
// the call. A DENY therefore cannot veto the individual call — it can only
// CANCEL the whole run (kill the process). Consent here is best-effort post-hoc
// cancellation; the CLI's own sandbox plus the isolated worktree are the real
// security boundary. external-cli is ACTIVE/wired in v0.1.0 (dispatch site:
// pkg/agent/external_dispatch.go, driven from pkg/agent/subturn.go); only the
// remote-a2a executor kind stays reserved (see runner/dispatch.go).
type ClaudeDriver struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	eventCh chan RunEvent
	consent ConsentHandler
	runID   string
}

// NewClaudeDriver creates a driver for the claude CLI.
// consent may be nil; when nil all permission requests are denied by default (FR-5.1).
func NewClaudeDriver(consent ConsentHandler) *ClaudeDriver {
	return &ClaudeDriver{
		consent: consent,
	}
}

// Run starts `claude -p --output-format stream-json` with the given options.
// It returns a channel of RunEvents that is closed when the process exits.
// FR-5.2: does NOT pass --dangerously-skip-permissions.
//
//nolint:dupl // driver-specific process lifecycle; the parallel exit/stderr handling shares shape with OpencodeDriver.Run but differs in per-CLI log prefixes and error-message text — a shared helper would obscure those per-CLI differences and risk behavior changes
func (d *ClaudeDriver) Run(ctx context.Context, opts RunOptions) (<-chan RunEvent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.eventCh != nil {
		return nil, fmt.Errorf("claude driver: Run called while a run is already active")
	}

	// Resolve the CLI binary: opts.CLIPath (ExecutorConfig.cli_path) wins; else
	// the default name resolved via $PATH (MAJ-5).
	binary := resolveCLIBinary(opts.CLIPath, claudeBinName)

	// Detect and pin CLI version (FR-5.6 / N3).
	ver, verKnown := detectAndPinVersion(ctx, binary, "runner/claude", knownClaudeVersionPrefixes)

	runID := opts.RunID
	if runID == "" {
		runID = fmt.Sprintf("claude-%d", time.Now().UnixNano())
	}
	d.runID = runID

	args := d.buildArgs(opts)
	runCtx, cancelFn := context.WithCancel(ctx)
	if opts.TimeoutSeconds > 0 {
		runCtx, cancelFn = context.WithTimeout(runCtx, time.Duration(opts.TimeoutSeconds)*time.Second)
	}

	cmd := exec.CommandContext(runCtx, binary, args...)
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}
	cmd.Env = d.buildEnv(opts)

	if opts.Input != "" {
		cmd.Stdin = strings.NewReader(opts.Input)
	} else {
		cmd.Stdin = bytes.NewReader(nil)
	}

	pr, pw := io.Pipe()
	cmd.Stdout = pw

	// Capture stderr for diagnostic logging (piped to slog in the goroutine below).
	stderrR, stderrW := io.Pipe()
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		cancelFn()
		return nil, fmt.Errorf("claude driver: failed to start: %w", err)
	}

	ch := make(chan RunEvent, 64)
	d.eventCh = ch
	d.cancel = cancelFn
	d.cmd = cmd

	// Emit the Start event first so the run log / SPA can pin the CLI version
	// (FR-5.6 / N3). Buffered channel; this cannot block here.
	ch <- RunEvent{
		Kind:      EventKindStart,
		RunID:     runID,
		Timestamp: time.Now().UTC(),
		Start: &StartEvent{
			CLI:          claudeBinName,
			Version:      ver,
			VersionKnown: verKnown,
		},
	}

	// Goroutine: pipe stderr lines to slog (Debug) AND retain a bounded tail so a
	// non-zero exit can surface the real diagnostic at Warn/Error (see below).
	stderrBuf := newStderrTail()
	go func() {
		defer func() { _ = stderrR.Close() }()
		sc := newLineScanner(stderrR)
		for sc.Scan() {
			line := sc.Text()
			stderrBuf.add(line)
			slog.Debug("runner/claude: stderr", "run_id", runID, "line", line)
		}
	}()

	// Waiter goroutine: wait for the process to exit, then close the stdout/stderr
	// pipe write ends so the reader goroutines see EOF. Without this, streamParser
	// would block forever reading pr (the write end is held here, not by exec).
	waitErr := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = pw.Close()
		_ = stderrW.Close()
		waitErr <- err
	}()

	// Goroutine: parse NDJSON stdout → emit RunEvents.
	go func() {
		defer func() {
			_ = pr.Close()
			// Reset run state so a subsequent Run/Resume is accepted (resumability).
			d.mu.Lock()
			d.eventCh = nil
			d.cmd = nil
			d.cancel = nil
			d.mu.Unlock()
			close(ch)
			cancelFn()
		}()

		// Apply turn cap if requested (FR-5.4).
		maxTurns := opts.MaxTurns
		if maxTurns <= 0 {
			maxTurns = defaultMaxTurns
		}
		turnCount := 0

		emittedFatal := streamParser(runCtx, pr, runID, func(raw []byte) (RunEvent, bool) {
			return d.parseLine(raw, runID, &turnCount, maxTurns)
		}, ch)

		// Collect the process exit status and emit a final end/error event.
		err := <-waitErr
		// Suppress a second terminal error event when the stream parser already
		// emitted a fatal error (avoids a duplicate error event to the caller).
		if err != nil && runCtx.Err() == nil {
			// Surface the captured stderr tail at Warn so operators can diagnose
			// the real cause (auth failure, CLI usage error) instead of a bare
			// "exited: status 1". Logged even when emittedFatal, since the fatal
			// stream event may carry a less specific message than stderr.
			if tail := stderrBuf.String(); tail != "" {
				slog.Warn("runner/claude: process exited with error",
					"run_id", runID, "err", err, "stderr_tail", tail)
			} else {
				slog.Warn("runner/claude: process exited with error", "run_id", runID, "err", err)
			}
			if !emittedFatal {
				msg := fmt.Sprintf("claude exited: %v", err)
				if tail := stderrBuf.String(); tail != "" {
					msg = fmt.Sprintf("claude exited: %v\nstderr:\n%s", err, tail)
				}
				ch <- RunEvent{
					Kind:      EventKindError,
					RunID:     runID,
					Timestamp: time.Now().UTC(),
					Err:       &ErrorEvent{Message: msg, Fatal: true},
				}
			}
		} else if runCtx.Err() == context.DeadlineExceeded && !emittedFatal {
			ch <- RunEvent{
				Kind:      EventKindError,
				RunID:     runID,
				Timestamp: time.Now().UTC(),
				Err:       &ErrorEvent{Message: "run exceeded timeout (FR-5.4)", Fatal: true},
			}
		}
	}()

	return ch, nil
}

// buildArgs constructs the claude CLI argument list (FR-5.2, ADR-032 fix C).
// NOTE: --dangerously-skip-permissions IS passed unconditionally (operator
// decision, 2026-07-05, reversing FR-5.3/US-5's original claude-specific
// stance — see the package doc's SECURITY NOTE and issue #488). This
// disables Claude Code's own internal sandbox as well as its permission
// prompts; there is no separate Omnipus confiner for external-CLI workers.
//
// --resume is deliberately NOT passed: opts.RunID is a freshly generated
// dispatch identifier ("subturn-N" / "ext-<nanos>"), never a real prior claude
// session ID, and `claude --resume <id>` errors when no session with that ID
// exists. --session-id would need a valid UUID (opts.RunID is not one), so it
// is also omitted — every run simply starts fresh (ADR-032 fix C).
//
// No positional prompt argument is appended (not even a trailing "-"). A "-"
// token is NOT a documented stdin sentinel for this CLI — live-tested against
// the real binary (v2.1.199): `claude -p - --output-format json < /dev/null`
// succeeds and proceeds past the "input required" check, proving "-" is read
// as a literal one-character prompt string, not a stdin marker. The
// documented AND empirically-confirmed-working pattern is simpler: omit the
// positional prompt entirely and let `claude -p` consume all of stdin
// automatically when no positional prompt is given. opts.Input is piped to
// cmd.Stdin in Run() (not here) and needs no positional counterpart.
func (d *ClaudeDriver) buildArgs(opts RunOptions) []string {
	// --verbose is REQUIRED for --output-format stream-json to actually emit
	// its event stream (ADR-032 fix C) — without it the driver's NDJSON parser
	// sees nothing.
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--no-chrome"}
	if model := strings.TrimSpace(opts.Model); model != "" {
		args = append(args, "--model", model)
	}
	// Full permission bypass, unconditional (operator decision, 2026-07-05 —
	// see the package doc's SECURITY NOTE and issue #488). Without this, a
	// headless run stalls forever on a TTY permission prompt that will never
	// come; the previous acceptEdits middle ground still blocked on non-edit
	// tool calls with no reachable approval UI in practice.
	args = append(args, "--dangerously-skip-permissions")
	if opts.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", opts.MaxTurns))
	}
	// Append operator-supplied extra args (ExecutorConfig.cli_args, MAJ-5) last —
	// there is no trailing positional/stdin-sentinel token to keep after them
	// (see the buildArgs doc comment above: no "-" is appended; opts.Input
	// reaches the child exclusively via cmd.Stdin in Run()). filterDangerousCLIArgs
	// still runs here (see argsafety.go) for the correctness-only --output-format
	// guard, plus two now-repurposed (not removed) entries for claude: an
	// operator cli_args copy of --dangerously-skip-permissions is dropped as
	// redundant (the driver's own baseline above already IS the full bypass),
	// and any --permission-mode value is dropped as inert (that same
	// unconditional bypass silently overrides/ignores it) — neither is an
	// escalation guard anymore, both exist purely so a no-op operator setting
	// is still visible via dropped_args/WARN instead of silently doing nothing.
	kept, dropped := filterDangerousCLIArgs("claude", opts.CLIArgs)
	logDroppedCLIArgs("runner/claude", "claude", opts.RunID, dropped)
	args = append(args, kept...)
	return args
}

// buildEnv builds the environment for the child process. See buildChildEnv:
// a non-nil opts.Env is the COMPLETE, already-scrubbed allowlist (the gateway
// secrets, incl. OMNIPUS_MASTER_KEY, are NOT inherited); nil falls back to
// os.Environ() for direct/test callers. opts.EnvOverrides (ExecutorConfig
// .env_overrides, MAJ-5) are then merged in — protected OMNIPUS_* keys are
// dropped so the master key / agent-identity vars cannot be overridden.
func (d *ClaudeDriver) buildEnv(opts RunOptions) []string {
	return mergeEnvOverrides(buildChildEnv(opts.Env), opts.EnvOverrides, opts.RunID)
}

// parseLine maps a single NDJSON line from `claude --output-format stream-json`
// to a RunEvent. Returns (event, true) when an event should be emitted.
// Unknown types are silently skipped (graceful degradation per FR-5.6).
func (d *ClaudeDriver) parseLine(
	raw []byte,
	runID string,
	turnCount *int,
	maxTurns int,
) (RunEvent, bool) {
	typ, ok := rawJSONField(raw, "type")
	if !ok {
		// Malformed JSON — emit a non-fatal error event.
		slog.Debug("runner/claude: malformed JSON line", "run_id", runID, "raw", string(raw))
		return RunEvent{
			Kind:  EventKindError,
			RunID: runID,
			Err:   &ErrorEvent{Message: fmt.Sprintf("malformed JSON: %s", raw), Fatal: false},
		}, true
	}

	switch typ {
	case "assistant":
		return d.parseAssistantEvent(raw, runID, turnCount, maxTurns)

	case "tool_use":
		// Direct tool_use event (some stream-json versions emit this at top-level).
		return d.parseToolUseEvent(raw, runID)

	case "result":
		return d.parseResultEvent(raw, runID)

	case "system":
		// system/init event — extract session_id for resume tracking.
		if subtype, _ := rawJSONField(raw, "subtype"); subtype == "init" {
			if sessionID, ok := rawJSONField(raw, "session_id"); ok && sessionID != "" {
				slog.Info("runner/claude: session init", "run_id", runID, "session_id", sessionID)
			}
		}
		return RunEvent{}, false

	default:
		// Unknown type — skip gracefully (FR-5.6).
		slog.Debug("runner/claude: unknown event type", "run_id", runID, "type", typ)
		return RunEvent{}, false
	}
}

// parseAssistantEvent handles the "assistant" event type.
// The content array may contain "text" blocks (→ OutputEvent) or "tool_use"
// blocks (→ PermissionRequestEvent or ToolCallEvent).
func (d *ClaudeDriver) parseAssistantEvent(
	raw []byte,
	runID string,
	turnCount *int,
	maxTurns int,
) (RunEvent, bool) {
	*turnCount++
	if maxTurns > 0 && *turnCount > maxTurns {
		slog.Warn("runner/claude: turn cap exceeded — terminating", "run_id", runID,
			"turn_count", *turnCount, "max_turns", maxTurns)
		d.Cancel()
		return RunEvent{
			Kind:  EventKindError,
			RunID: runID,
			Err: &ErrorEvent{
				Message: fmt.Sprintf("turn cap exceeded: %d turns (max %d)", *turnCount, maxTurns),
				Fatal:   true,
			},
		}, true
	}

	var ev struct {
		Message struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return RunEvent{}, false
	}

	// Collect all text blocks into a single output event.
	var textParts []string
	for _, block := range ev.Message.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "tool_use":
			// Tool invocation — emit PermissionRequestEvent so the consent layer
			// can decide whether to allow it (FR-5.1, FR-5.3).
			reqID := block.ID
			if reqID == "" {
				reqID = fmt.Sprintf("tool-%d", time.Now().UnixNano())
			}
			inputBytes := safeMarshal(block.Input)
			return RunEvent{
				Kind:  EventKindPermissionRequest,
				RunID: runID,
				PermissionRequest: &PermissionRequestEvent{
					RequestID:   reqID,
					ToolName:    block.Name,
					Description: fmt.Sprintf("Claude wants to call tool %q", block.Name),
					RawInput:    inputBytes,
				},
			}, true
		}
	}

	if len(textParts) > 0 {
		return RunEvent{
			Kind:   EventKindOutput,
			RunID:  runID,
			Output: &OutputEvent{Text: strings.Join(textParts, "")},
		}, true
	}
	return RunEvent{}, false
}

// parseToolUseEvent handles a top-level "tool_use" event.
func (d *ClaudeDriver) parseToolUseEvent(raw []byte, runID string) (RunEvent, bool) {
	var ev struct {
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return RunEvent{}, false
	}
	reqID := ev.ID
	if reqID == "" {
		reqID = fmt.Sprintf("tool-%d", time.Now().UnixNano())
	}
	return RunEvent{
		Kind:  EventKindPermissionRequest,
		RunID: runID,
		PermissionRequest: &PermissionRequestEvent{
			RequestID:   reqID,
			ToolName:    ev.Name,
			Description: fmt.Sprintf("Claude wants to call tool %q", ev.Name),
			RawInput:    safeMarshal(ev.Input),
		},
	}, true
}

// parseResultEvent handles the "result" event type (final summary).
func (d *ClaudeDriver) parseResultEvent(raw []byte, runID string) (RunEvent, bool) {
	var ev struct {
		Subtype   string `json:"subtype"`
		Result    string `json:"result"`
		Error     string `json:"error"`
		SessionID string `json:"session_id"`
		// IsError is authoritative over Subtype: a live auth-failure payload
		// has been observed as {"subtype":"success","is_error":true,
		// "result":"Not logged in · Please run /login",...} — subtype claims
		// success while is_error reports the real outcome. Every caller of
		// this event MUST check IsError, not just Subtype.
		IsError bool `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return RunEvent{}, false
	}
	if ev.SessionID != "" {
		slog.Info("runner/claude: run complete", "run_id", runID, "session_id", ev.SessionID)
	}
	switch ev.Subtype {
	case "success":
		if ev.IsError {
			// subtype "success" with is_error true: the real error text lives
			// in Result for this shape (e.g. "Not logged in · Please run
			// /login"), not Error — fall back to a generic message only when
			// Result is also empty.
			msg := ev.Result
			if msg == "" {
				msg = "claude reported an error result"
			}
			return RunEvent{
				Kind:  EventKindError,
				RunID: runID,
				Err:   &ErrorEvent{Message: msg, Fatal: true},
			}, true
		}
		if ev.Result != "" {
			return RunEvent{
				Kind:   EventKindOutput,
				RunID:  runID,
				Output: &OutputEvent{Text: ev.Result},
			}, true
		}
		return RunEvent{Kind: EventKindEnd, RunID: runID}, true
	case "error_during_execution", "error":
		msg := ev.Error
		if msg == "" {
			msg = "unknown error from claude"
		}
		return RunEvent{
			Kind:  EventKindError,
			RunID: runID,
			Err:   &ErrorEvent{Message: msg, Fatal: true},
		}, true
	default:
		return RunEvent{Kind: EventKindEnd, RunID: runID}, true
	}
}

// Decide routes a permission decision back to the running claude process.
//
// `claude -p` has no bidirectional permission channel: Omnipus cannot inject a
// per-tool "allow" mid-run. An Allow is therefore a no-op (the run continues);
// a DENY cancels the whole run (kills the process). This is best-effort post-hoc
// cancellation — see the type-level POST-HOC CONSENT LIMITATION note and the
// consent.go package doc. The run ID is read under d.mu for the log only.
func (d *ClaudeDriver) Decide(decision PermissionDecision) {
	if !decision.Allow {
		d.mu.Lock()
		runID := d.runID
		d.mu.Unlock()
		slog.Info("runner/claude: permission denied — canceling run",
			"run_id", runID, "request_id", decision.RequestID, "reason", decision.Reason)
		d.Cancel()
	}
}

// Cancel terminates the external process immediately. Idempotent.
func (d *ClaudeDriver) Cancel() {
	d.mu.Lock()
	cancelFn := d.cancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
	}
}

// Input sends additional text to the running agent (best-effort for claude -p).
// Claude Code's `--print` mode does not support mid-run stdin injection —
// this is a no-op that returns nil per the interface contract.
func (d *ClaudeDriver) Input(_ string) error {
	return nil
}

// Resume re-starts the claude process with `--resume <runID>`.
func (d *ClaudeDriver) Resume(ctx context.Context, runID string) (<-chan RunEvent, error) {
	return d.Run(ctx, RunOptions{RunID: runID})
}

// Test validates the claude CLI is present and can produce version output.
// It does NOT execute any real work (FR-4.2).
func (d *ClaudeDriver) Test(ctx context.Context) ConnectionTestResult {
	ver, err := detectCLIVersion(ctx, "claude")
	if err != nil {
		return ConnectionTestResult{
			OK:      false,
			Message: fmt.Sprintf("claude binary check failed: %v", err),
		}
	}
	d.logVersionCheck("claude", ver)
	return ConnectionTestResult{
		OK:         true,
		Message:    "claude CLI found and responding",
		CLIVersion: ver,
	}
}

// logVersionCheck logs a warning for unknown version strings (FR-5.6). It
// returns true when the version matches a known prefix.
func (d *ClaudeDriver) logVersionCheck(binary, ver string) bool {
	for _, prefix := range knownClaudeVersionPrefixes {
		if strings.Contains(ver, prefix) {
			slog.Info("runner/claude: CLI version", "binary", binary, "version", ver)
			return true
		}
	}
	slog.Warn("runner/claude: unknown CLI version — proceeding with graceful degradation",
		"binary", binary, "version", ver)
	return false
}

// compile-time interface assertion
var _ ExternalAgentRunner = (*ClaudeDriver)(nil)

// defaultMaxTurns is the default turn cap applied when RunOptions.MaxTurns is 0 (FR-5.4).
const defaultMaxTurns = 50

// newLineScanner is a thin wrapper around bufio.NewScanner for use in goroutines.
func newLineScanner(r io.Reader) interface {
	Scan() bool
	Text() string
} {
	return bufio.NewScanner(r)
}
