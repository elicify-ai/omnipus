// driver_codex.go — Codex CLI streaming driver (FR-5.2, FR-5.6, US-4)
//
// Drives `codex exec --json` without the
// `--dangerously-bypass-approvals-and-sandbox` flag (FR-5.3 / US-5 requirement).
//
// Codex stream-json event shapes (from pkg/providers/codex_cli_provider.go):
//
//	{"type":"item.completed","item":{"id":"...","type":"agent_message","text":"...",...},...}
//	{"type":"turn.completed","usage":{"input_tokens":N,"output_tokens":N},...}
//	{"type":"error","message":"..."}
//	{"type":"turn.failed","error":{"message":"..."}}
//
// NOTE: The shapes are defined in pkg/providers/codex_cli_provider.go.
// Per the spec, these are REUSED here as a reference; that file is NOT modified.
// The driver re-defines equivalent local types to avoid a dependency on the
// providers package (which would create an import cycle: runner → providers → ...).

package runner

import (
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

// knownCodexVersionPrefixes lists version-string prefixes validated against
// this driver (FR-5.6).
var knownCodexVersionPrefixes = []string{"0.", "1.", "2."}

// codexBinName is the executable name invoked by the Codex driver. It is a
// package var only so in-package tests can point at a stub process; production
// never overrides it.
var codexBinName = "codex"

// codexStreamEvent mirrors the shape from pkg/providers/codex_cli_provider.go.
// It is defined locally to avoid an import cycle and is kept in sync with the
// provider's codexEvent type by convention (both unmarshal the same NDJSON).
type codexStreamEvent struct {
	Type     string               `json:"type"`
	ThreadID string               `json:"thread_id,omitempty"`
	Message  string               `json:"message,omitempty"`
	Item     *codexStreamItem     `json:"item,omitempty"`
	Usage    *codexStreamUsage    `json:"usage,omitempty"`
	Error    *codexStreamEventErr `json:"error,omitempty"`
}

type codexStreamItem struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Command  string `json:"command,omitempty"`
	Status   string `json:"status,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Output   string `json:"output,omitempty"`
}

type codexStreamUsage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
}

type codexStreamEventErr struct {
	Message string `json:"message"`
}

// CodexDriver implements ExternalAgentRunner for the `codex` CLI.
// It drives `codex exec --json` and routes permission requests to the
// wired ConsentHandler (deny-by-default when nil, per FR-5.1).
type CodexDriver struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	eventCh chan RunEvent
	consent ConsentHandler
	runID   string
}

// NewCodexDriver creates a driver for the codex CLI.
// consent may be nil; when nil all permission requests are denied by default.
func NewCodexDriver(consent ConsentHandler) *CodexDriver {
	return &CodexDriver{consent: consent}
}

// Run starts `codex exec --json` with the given options.
// FR-5.2: does NOT pass --dangerously-bypass-approvals-and-sandbox.
// FR-5.3: codex's own sandbox handles confinement.
func (d *CodexDriver) Run(ctx context.Context, opts RunOptions) (<-chan RunEvent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.eventCh != nil {
		return nil, fmt.Errorf("codex driver: Run called while a run is already active")
	}

	// Resolve the CLI binary: opts.CLIPath (ExecutorConfig.cli_path) wins; else
	// the default name resolved via $PATH (MAJ-5).
	binary := resolveCLIBinary(opts.CLIPath, codexBinName)

	// Detect and pin CLI version (FR-5.6 / N3).
	ver, verKnown := detectAndPinVersion(ctx, binary, "runner/codex", knownCodexVersionPrefixes)

	runID := opts.RunID
	if runID == "" {
		runID = fmt.Sprintf("codex-%d", time.Now().UnixNano())
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

	prompt := opts.Input
	if prompt == "" {
		prompt = " " // codex requires non-empty stdin
	}
	cmd.Stdin = bytes.NewReader([]byte(prompt))

	pr, pw := io.Pipe()
	cmd.Stdout = pw

	stderrR, stderrW := io.Pipe()
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		cancelFn()
		return nil, fmt.Errorf("codex driver: failed to start: %w", err)
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
			CLI:          codexBinName,
			Version:      ver,
			VersionKnown: verKnown,
		},
	}

	// Pipe stderr to slog (Debug) AND retain a bounded tail so a non-zero exit
	// can surface the real diagnostic at Warn/Error (see below).
	stderrBuf := newStderrTail()
	go func() {
		defer func() { _ = stderrR.Close() }()
		sc := newLineScanner(stderrR)
		for sc.Scan() {
			line := sc.Text()
			stderrBuf.add(line)
			slog.Debug("runner/codex: stderr", "run_id", runID, "line", line)
		}
	}()

	// Waiter goroutine: wait for the process to exit, then close the pipe write
	// ends so the reader goroutines see EOF (the write end is held here, not by exec).
	waitErr := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = pw.Close()
		_ = stderrW.Close()
		waitErr <- err
	}()

	// Parse stdout NDJSON → emit RunEvents.
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

		maxTurns := opts.MaxTurns
		if maxTurns <= 0 {
			maxTurns = defaultMaxTurns
		}
		turnCount := 0

		emittedFatal := streamParser(runCtx, pr, runID, func(raw []byte) (RunEvent, bool) {
			return d.parseLine(raw, runID, &turnCount, maxTurns)
		}, ch)

		err := <-waitErr
		if err != nil && runCtx.Err() == nil {
			// Surface the captured stderr tail at Warn so operators can diagnose
			// the real cause instead of a bare "exited: status 1".
			if tail := stderrBuf.String(); tail != "" {
				slog.Warn("runner/codex: process exited with error",
					"run_id", runID, "err", err, "stderr_tail", tail)
			} else {
				slog.Warn("runner/codex: process exited with error", "run_id", runID, "err", err)
			}
			if !emittedFatal {
				msg := fmt.Sprintf("codex exited: %v", err)
				if tail := stderrBuf.String(); tail != "" {
					msg = fmt.Sprintf("codex exited: %v\nstderr:\n%s", err, tail)
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
		} else if !emittedFatal && runCtx.Err() == nil {
			// M4: emit the single terminal End once, at true completion — the
			// codex stream drained cleanly with no fatal error. turn.completed no
			// longer emits End (it fires once per turn), so this is the only place
			// a successful End is produced.
			ch <- RunEvent{Kind: EventKindEnd, RunID: runID, Timestamp: time.Now().UTC()}
		}
	}()

	return ch, nil
}

// buildArgs constructs the codex CLI argument list (FR-5.2, ADR-032 fix C/D).
// NOTE: --dangerously-bypass-approvals-and-sandbox is deliberately omitted
// (FR-5.3); --sandbox workspace-write + --ask-for-approval never is used
// instead (ADR-032 fix D — workspace-write, non-interactive posture without a
// full bypass).
//
// IMPORTANT flag ordering: -a/--ask-for-approval is a GLOBAL codex flag and
// errors ("unexpected argument '--ask-for-approval' found") when placed AFTER
// the `exec` subcommand — it MUST precede `exec` (`codex --ask-for-approval
// never exec ...`). --sandbox, by contrast, IS accepted as an exec-subcommand
// flag and is placed after `exec` alongside the other exec flags.
func (d *CodexDriver) buildArgs(opts RunOptions) []string {
	// --ask-for-approval must come before the "exec" subcommand (see doc above).
	args := []string{"--ask-for-approval", "never", "exec", "--json", "--sandbox", "workspace-write",
		"--skip-git-repo-check", "--color", "never"}
	if model := strings.TrimSpace(opts.Model); model != "" {
		args = append(args, "-m", model)
	}
	if opts.WorkDir != "" {
		args = append(args, "-C", opts.WorkDir)
	}
	// Append operator-supplied extra args (ExecutorConfig.cli_args, MAJ-5) before
	// the trailing "-" so the stdin sentinel stays last. ADR-032 fix M-1: a
	// flag that could re-enable a full sandbox bypass (--sandbox
	// danger-full-access) or reintroduce an unanswerable approval gate
	// (--ask-for-approval) is stripped first — see argsafety.go. This filter
	// applies to opts.CLIArgs ONLY; the driver's own --sandbox workspace-write
	// / --ask-for-approval never flags above are never touched.
	kept, dropped := filterDangerousCLIArgs("codex", opts.CLIArgs)
	logDroppedCLIArgs("runner/codex", "codex", opts.RunID, dropped)
	args = append(args, kept...)
	args = append(args, "-") // read prompt from stdin
	return args
}

// buildEnv builds the environment for the child process. See buildChildEnv:
// a non-nil opts.Env is the COMPLETE, already-scrubbed allowlist (the gateway
// secrets, incl. OMNIPUS_MASTER_KEY, are NOT inherited); nil falls back to
// os.Environ() for direct/test callers. opts.EnvOverrides (ExecutorConfig
// .env_overrides, MAJ-5) are merged in afterwards; protected OMNIPUS_* keys are
// dropped.
func (d *CodexDriver) buildEnv(opts RunOptions) []string {
	return mergeEnvOverrides(buildChildEnv(opts.Env), opts.EnvOverrides, opts.RunID)
}

// parseLine maps a single NDJSON line from `codex exec --json` to a RunEvent.
// Uses the same event shape as pkg/providers/codex_cli_provider.go (spec: reuse
// the shape, do NOT modify that file).
func (d *CodexDriver) parseLine(
	raw []byte,
	runID string,
	turnCount *int,
	maxTurns int,
) (RunEvent, bool) {
	var ev codexStreamEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		slog.Debug("runner/codex: malformed JSON line", "run_id", runID, "raw", string(raw))
		return RunEvent{
			Kind:  EventKindError,
			RunID: runID,
			Err:   &ErrorEvent{Message: fmt.Sprintf("malformed JSON: %s", raw), Fatal: false},
		}, true
	}

	switch ev.Type {
	case "item.completed":
		if ev.Item == nil {
			return RunEvent{}, false
		}
		switch ev.Item.Type {
		case "agent_message":
			if ev.Item.Text != "" {
				return RunEvent{
					Kind:   EventKindOutput,
					RunID:  runID,
					Output: &OutputEvent{Text: ev.Item.Text},
				}, true
			}
		case "tool_call":
			// Codex tool_call item — emit as PermissionRequestEvent (FR-5.1).
			reqID := ev.Item.ID
			if reqID == "" {
				reqID = fmt.Sprintf("codex-tool-%d", time.Now().UnixNano())
			}
			// Build RawInput with json.Marshal so commands containing control
			// characters or quotes produce valid JSON (a %q-built string can be
			// invalid JSON for control chars).
			rawInput := safeMarshal(map[string]string{"command": ev.Item.Command})
			// ToolName MUST be a stable, short identifier so audit logs stay
			// compact and policy-by-tool-name matching is meaningful. Codex only
			// surfaces a raw shell command for tool_call items, so we derive the
			// invoked binary (first token) as the tool name and keep the FULL
			// command in RawInput + Description.
			return RunEvent{
				Kind:  EventKindPermissionRequest,
				RunID: runID,
				PermissionRequest: &PermissionRequestEvent{
					RequestID:   reqID,
					ToolName:    codexCommandToolName(ev.Item.Command),
					Description: fmt.Sprintf("Codex wants to run command %q", ev.Item.Command),
					RawInput:    rawInput,
				},
			}, true
		}
		return RunEvent{}, false

	case "turn.completed":
		// M4: `codex exec --json` emits one turn.completed PER TURN — it is NOT the
		// terminal signal for the run. Emitting EventKindEnd here fired a spurious
		// "end" after every turn and corrupted the streamed transcript. The single
		// terminal End is now emitted once when the stdout stream is exhausted (see
		// Run() and ParseCodexStreamJSON). Here we only count the turn and enforce
		// the cap; the event itself is consumed (ok=false).
		*turnCount++
		if maxTurns > 0 && *turnCount > maxTurns {
			slog.Warn("runner/codex: turn cap exceeded — terminating", "run_id", runID,
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
		return RunEvent{}, false

	case "error":
		msg := ev.Message
		if msg == "" {
			msg = "unknown codex error"
		}
		return RunEvent{
			Kind:  EventKindError,
			RunID: runID,
			Err:   &ErrorEvent{Message: msg, Fatal: true},
		}, true

	case "turn.failed":
		msg := "codex turn failed"
		if ev.Error != nil && ev.Error.Message != "" {
			msg = ev.Error.Message
		}
		return RunEvent{
			Kind:  EventKindError,
			RunID: runID,
			Err:   &ErrorEvent{Message: msg, Fatal: true},
		}, true

	default:
		slog.Debug("runner/codex: unknown event type", "run_id", runID, "type", ev.Type)
		return RunEvent{}, false
	}
}

// codexCommandToolName derives a stable, short tool identifier from a codex
// shell command string. It returns the invoked binary (the first whitespace-
// separated token, basename only) so audit logs stay compact and policy
// matching by tool name is meaningful. Falls back to "shell" when the command
// is empty or unparseable. The full command is preserved by the caller in the
// permission event's RawInput and Description.
func codexCommandToolName(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "shell"
	}
	tok := fields[0]
	// Strip any leading env-assignments like FOO=bar that precede the binary.
	for strings.Contains(tok, "=") && len(fields) > 1 {
		fields = fields[1:]
		tok = fields[0]
	}
	// Basename only — drop any directory component (e.g. /usr/bin/python → python).
	if idx := strings.LastIndexAny(tok, "/\\"); idx >= 0 && idx < len(tok)-1 {
		tok = tok[idx+1:]
	}
	if tok == "" {
		return "shell"
	}
	return tok
}

// Decide routes a permission decision (FR-5.1).
// Codex in streaming mode does not have a bidirectional stdin channel for
// permission responses; a deny cancels the run, an allow is a no-op (the
// run continues unless canceled). Best-effort post-hoc cancellation — see
// the consent.go package doc. The run ID is read under d.mu for the log only.
func (d *CodexDriver) Decide(decision PermissionDecision) {
	if !decision.Allow {
		d.mu.Lock()
		runID := d.runID
		d.mu.Unlock()
		slog.Info("runner/codex: permission denied — canceling run",
			"run_id", runID, "request_id", decision.RequestID, "reason", decision.Reason)
		d.Cancel()
	}
}

// Cancel terminates the external process immediately. Idempotent.
func (d *CodexDriver) Cancel() {
	d.mu.Lock()
	cancelFn := d.cancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
	}
}

// Input is best-effort for codex (no mid-run stdin injection in --json mode).
func (d *CodexDriver) Input(_ string) error {
	return nil
}

// Resume for codex: codex exec does not have native --resume support in all
// versions; start a fresh run with the given runID as a label.
func (d *CodexDriver) Resume(ctx context.Context, runID string) (<-chan RunEvent, error) {
	return d.Run(ctx, RunOptions{RunID: runID})
}

// Test validates the codex CLI is present.
func (d *CodexDriver) Test(ctx context.Context) ConnectionTestResult {
	ver, err := detectCLIVersion(ctx, "codex")
	if err != nil {
		return ConnectionTestResult{
			OK:      false,
			Message: fmt.Sprintf("codex binary check failed: %v", err),
		}
	}
	d.logVersionCheck(ver)
	return ConnectionTestResult{
		OK:         true,
		Message:    "codex CLI found and responding",
		CLIVersion: ver,
	}
}

// logVersionCheck logs a warning for unknown version strings (FR-5.6). It
// returns true when the version matches a known prefix.
func (d *CodexDriver) logVersionCheck(ver string) bool {
	for _, prefix := range knownCodexVersionPrefixes {
		if strings.Contains(ver, prefix) {
			slog.Info("runner/codex: CLI version", "version", ver)
			return true
		}
	}
	slog.Warn("runner/codex: unknown CLI version — proceeding with graceful degradation", "version", ver)
	return false
}

// compile-time interface assertion
var _ ExternalAgentRunner = (*CodexDriver)(nil)
