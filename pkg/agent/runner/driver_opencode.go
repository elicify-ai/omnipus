// driver_opencode.go — opencode CLI streaming driver (FR-5.2, FR-5.6, US-4)
//
// Drives `opencode run --format json` (net-new per Spec-4).
// opencode is the user's installed binary; it is not a Go dependency.
//
// opencode stream-json event shapes (opencode ≥ 0.1.0):
//
//	{"type":"message.start","message":{"id":"...","role":"assistant",...}}
//	{"type":"content.delta","delta":{"type":"text_delta","text":"..."}}
//	{"type":"content.done","content":{...}}
//	{"type":"tool.start","tool":{"id":"...","name":"...","input":{...}}}
//	{"type":"tool.done","tool":{"id":"...","name":"...","output":"..."},"exitCode":0}
//	{"type":"session.complete","session_id":"...","usage":{...}}
//	{"type":"error","message":"...","code":"..."}
//
// The driver gracefully skips any unknown event types (FR-5.6).

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

// knownOpencodeVersionPrefixes lists version-string prefixes validated against
// this driver (FR-5.6).
var knownOpencodeVersionPrefixes = []string{"0.", "1."}

// opencodeBinName is the executable name invoked by the opencode driver. It is a
// package var only so in-package tests can point at a stub process; production
// never overrides it.
var opencodeBinName = "opencode"

// OpencodeDriver implements ExternalAgentRunner for the `opencode` CLI.
type OpencodeDriver struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	eventCh chan RunEvent
	consent ConsentHandler
	runID   string
}

// NewOpencodeDriver creates a driver for the opencode CLI.
// consent may be nil; when nil all permission requests are denied by default.
func NewOpencodeDriver(consent ConsentHandler) *OpencodeDriver {
	return &OpencodeDriver{consent: consent}
}

// Run starts `opencode run --format json` with the given options.
//
//nolint:dupl // driver-specific process lifecycle; the parallel exit/stderr handling shares shape with ClaudeDriver.Run but differs in per-CLI log prefixes and error-message text — a shared helper would obscure those per-CLI differences and risk behavior changes
func (d *OpencodeDriver) Run(ctx context.Context, opts RunOptions) (<-chan RunEvent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.eventCh != nil {
		return nil, fmt.Errorf("opencode driver: Run called while a run is already active")
	}

	// Detect and pin CLI version (FR-5.6 / N3).
	// Resolve the CLI binary: opts.CLIPath (ExecutorConfig.cli_path) wins; else
	// the default name resolved via $PATH (MAJ-5).
	binary := resolveCLIBinary(opts.CLIPath, opencodeBinName)

	ver, verKnown := detectAndPinVersion(ctx, binary, "runner/opencode", knownOpencodeVersionPrefixes)

	runID := opts.RunID
	if runID == "" {
		runID = fmt.Sprintf("opencode-%d", time.Now().UnixNano())
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
	// M3: the prompt is delivered exactly once, as the POSITIONAL argument
	// after the "--" end-of-options separator (see buildArgs; opencode's
	// `run` command has no `--prompt` flag — that flag never existed on this
	// CLI). It MUST NOT also be written to stdin — feeding it on both
	// channels makes opencode process the prompt twice. stdin is always an empty
	// reader so the child does not block waiting on it.
	cmd.Stdin = bytes.NewReader(nil)

	pr, pw := io.Pipe()
	cmd.Stdout = pw

	stderrR, stderrW := io.Pipe()
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		cancelFn()
		return nil, fmt.Errorf("opencode driver: failed to start: %w", err)
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
			CLI:          opencodeBinName,
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
			slog.Debug("runner/opencode: stderr", "run_id", runID, "line", line)
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
				slog.Warn("runner/opencode: process exited with error",
					"run_id", runID, "err", err, "stderr_tail", tail)
			} else {
				slog.Warn("runner/opencode: process exited with error", "run_id", runID, "err", err)
			}
			if !emittedFatal {
				msg := fmt.Sprintf("opencode exited: %v", err)
				if tail := stderrBuf.String(); tail != "" {
					msg = fmt.Sprintf("opencode exited: %v\nstderr:\n%s", err, tail)
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

// buildArgs constructs the opencode CLI argument list (ADR-032 fix C/D, N-1,
// M-1).
//
// IMPORTANT (ADR-032 defect fix): `opencode run` takes the message as a
// POSITIONAL argument (`opencode run [message..]`) — there is no `--prompt`
// flag (verified against a real `opencode run --help`, CLI v1.16.0; the prior
// `--prompt` flag never existed on this CLI and every invocation would have
// failed with an "unknown option" error). The message is delivered exactly
// once; cmd.Stdin is always an empty reader (see Run()) so the child never
// blocks on stdin and never sees the prompt twice (M3 invariant preserved).
//
// N-1 (arg-injection): opts.Input is delegated task text that may be
// LLM-authored / prompt-injection-influenced. opencode's yargs-based parser
// recognizes registered --flags regardless of their position in argv, so a
// naive positional message beginning with e.g. "--model" or "--session"
// would be parsed as a FLAG, not message text, potentially altering the run
// (session hijack, model override, or losing the message entirely). Verified
// against the installed opencode v1.16.0 binary: `opencode run
// --nonexistent-flag` fails immediately at argv-parse time with a usage
// error (exit 1), while `opencode run -- --nonexistent-flag` proceeds past
// parsing into actual message handling — confirming yargs honors the
// standard `--` end-of-options separator. The message is therefore placed
// LAST, after a literal "--", with every driver flag (including the
// filtered opts.CLIArgs) preceding it — see driver_env_test.go and
// argsafety_test.go for the regression coverage.
func (d *OpencodeDriver) buildArgs(opts RunOptions) []string {
	args := []string{"run", "--format", "json"}
	if model := strings.TrimSpace(opts.Model); model != "" {
		if looksLikeProviderModel(model) {
			// ADR-032 fix C: opencode's --model expects "provider/model" —
			// guard so a bare model name (not in that shape) is omitted
			// rather than passed as garbage; the CLI falls back to its own
			// configured default.
			args = append(args, "--model", model)
		} else {
			// Unlike every other omission path in this file, a non-empty
			// but wrongly-shaped model was previously dropped with ZERO
			// logging — silently leaving the CLI on its own default model,
			// which could be a materially different (and differently
			// priced/capable) model than the operator configured. Warn so
			// this is visible in the run log.
			slog.Warn(
				"runner/opencode: configured model not in provider/model shape — omitting --model; CLI will use its own default",
				"run_id",
				opts.RunID,
				"configured_model",
				model,
			)
		}
	}
	// ADR-032 fix D: this opencode CLI version exposes no middle-ground
	// "auto-accept edits" flag — only the interactive default (which blocks
	// forever with no TTY to answer it) or a full bypass. Without SOME
	// auto-approve mechanism a headless `run` invocation stalls indefinitely,
	// so --dangerously-skip-permissions is used as the "auto" posture the
	// operator decided on. This does not regress Omnipus's OWN consent
	// routing for opencode, which is already best-effort/post-hoc regardless
	// of this flag — see the POST-HOC note on OpencodeDriver.Decide: a
	// tool.start event is only observed after the tool call has already
	// begun, whether or not opencode itself was told to auto-approve.
	args = append(args, "--dangerously-skip-permissions")
	// NOTE: -s/--session <RunID> is deliberately NOT passed. opencode
	// documents it as "session id to continue" — it resumes an EXISTING
	// session, not creates a new one with a caller-chosen ID. opts.RunID here
	// is always a freshly generated dispatch identifier opencode has never
	// seen, so passing it would target a non-existent session.
	// Append operator-supplied extra args (ExecutorConfig.cli_args, MAJ-5).
	// ADR-032 fix M-1: a redundant/conflicting copy of the driver's own
	// --dangerously-skip-permissions flag is stripped first — see
	// argsafety.go. This filter applies to opts.CLIArgs ONLY; the driver's
	// own --dangerously-skip-permissions flag above is never touched.
	kept, dropped := filterDangerousCLIArgs("opencode", opts.CLIArgs)
	logDroppedCLIArgs("runner/opencode", "opencode", opts.RunID, dropped)
	args = append(args, kept...)
	// ADR-032 fix N-1: the literal "--" end-of-options separator forces
	// everything after it to be treated as positional message content,
	// regardless of what it looks like — see the buildArgs doc comment above.
	args = append(args, "--")
	if opts.Input != "" {
		args = append(args, opts.Input)
	}
	return args
}

// looksLikeProviderModel reports whether model is shaped like opencode's
// expected "provider/model" form: exactly one '/' separator with non-empty
// content on both sides. Used to guard the --model flag (ADR-032 fix C) so a
// bare model name from an agent configured for a different CLI (claude/codex
// use bare model names) is never passed to opencode as garbage.
func looksLikeProviderModel(model string) bool {
	idx := strings.IndexByte(model, '/')
	if idx <= 0 || idx == len(model)-1 {
		return false
	}
	// Reject a second '/' — "provider/model" is exactly two segments.
	return strings.IndexByte(model[idx+1:], '/') == -1
}

// buildEnv builds the environment for the child process. See buildChildEnv:
// a non-nil opts.Env is the COMPLETE, already-scrubbed allowlist (the gateway
// secrets, incl. OMNIPUS_MASTER_KEY, are NOT inherited); nil falls back to
// os.Environ() for direct/test callers. opts.EnvOverrides (ExecutorConfig
// .env_overrides, MAJ-5) are merged in afterwards; protected OMNIPUS_* keys are
// dropped.
func (d *OpencodeDriver) buildEnv(opts RunOptions) []string {
	return mergeEnvOverrides(buildChildEnv(opts.Env), opts.EnvOverrides, opts.RunID)
}

// parseLine maps a single NDJSON line from `opencode run --format json` to a RunEvent.
func (d *OpencodeDriver) parseLine(
	raw []byte,
	runID string,
	turnCount *int,
	maxTurns int,
) (RunEvent, bool) {
	typ, ok := rawJSONField(raw, "type")
	if !ok {
		slog.Debug("runner/opencode: malformed JSON line", "run_id", runID, "raw", string(raw))
		return RunEvent{
			Kind:  EventKindError,
			RunID: runID,
			Err:   &ErrorEvent{Message: fmt.Sprintf("malformed JSON: %s", raw), Fatal: false},
		}, true
	}

	switch typ {
	case "content.delta":
		return d.parseContentDelta(raw, runID)

	case "tool.start":
		return d.parseToolStart(raw, runID)

	case "message.start":
		// Track turns on message.start.
		*turnCount++
		if maxTurns > 0 && *turnCount > maxTurns {
			slog.Warn("runner/opencode: turn cap exceeded — terminating", "run_id", runID,
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

	case "session.complete":
		if sessionID, _ := rawJSONField(raw, "session_id"); sessionID != "" {
			slog.Info("runner/opencode: session complete", "run_id", runID, "session_id", sessionID)
		}
		return RunEvent{Kind: EventKindEnd, RunID: runID}, true

	case "error":
		msg, _ := rawJSONField(raw, "message")
		if msg == "" {
			msg = "unknown opencode error"
		}
		return RunEvent{
			Kind:  EventKindError,
			RunID: runID,
			Err:   &ErrorEvent{Message: msg, Fatal: true},
		}, true

	default:
		slog.Debug("runner/opencode: unknown event type", "run_id", runID, "type", typ)
		return RunEvent{}, false
	}
}

// parseContentDelta handles the "content.delta" event.
func (d *OpencodeDriver) parseContentDelta(raw []byte, runID string) (RunEvent, bool) {
	var ev struct {
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return RunEvent{}, false
	}
	if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
		return RunEvent{
			Kind:   EventKindOutput,
			RunID:  runID,
			Output: &OutputEvent{Text: ev.Delta.Text},
		}, true
	}
	return RunEvent{}, false
}

// parseToolStart handles the "tool.start" event (permission-request path).
func (d *OpencodeDriver) parseToolStart(raw []byte, runID string) (RunEvent, bool) {
	var ev struct {
		Tool struct {
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"tool"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return RunEvent{}, false
	}
	reqID := ev.Tool.ID
	if reqID == "" {
		reqID = fmt.Sprintf("opencode-tool-%d", time.Now().UnixNano())
	}
	return RunEvent{
		Kind:  EventKindPermissionRequest,
		RunID: runID,
		PermissionRequest: &PermissionRequestEvent{
			RequestID:   reqID,
			ToolName:    ev.Tool.Name,
			Description: fmt.Sprintf("opencode wants to call tool %q", ev.Tool.Name),
			RawInput:    safeMarshal(ev.Tool.Input),
		},
	}, true
}

// Decide routes a permission decision (FR-5.1).
// opencode does not have a bidirectional stdin channel; a deny cancels the run,
// an allow is a no-op. Best-effort post-hoc cancellation — see the consent.go
// package doc. The run ID is read under d.mu for the log only.
func (d *OpencodeDriver) Decide(decision PermissionDecision) {
	if !decision.Allow {
		d.mu.Lock()
		runID := d.runID
		d.mu.Unlock()
		slog.Info("runner/opencode: permission denied — canceling run",
			"run_id", runID, "request_id", decision.RequestID, "reason", decision.Reason)
		d.Cancel()
	}
}

// Cancel terminates the external process immediately. Idempotent.
func (d *OpencodeDriver) Cancel() {
	d.mu.Lock()
	cancelFn := d.cancel
	d.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
	}
}

// Input is best-effort for opencode (no mid-run stdin injection).
func (d *OpencodeDriver) Input(_ string) error {
	return nil
}

// Resume re-starts opencode with `--session <runID>`.
func (d *OpencodeDriver) Resume(ctx context.Context, runID string) (<-chan RunEvent, error) {
	return d.Run(ctx, RunOptions{RunID: runID})
}

// Test runs the binary-present → handshake → authed health check (FR-4.2) by
// delegating to runner.TestConnection, so the missing-binary vs unauthed
// distinction is uniform across all external CLIs and the auth leg is not
// skipped. It does NOT execute any real work.
func (d *OpencodeDriver) Test(ctx context.Context) ConnectionTestResult {
	result := TestConnection(ctx, "opencode")
	if result.OK {
		d.logVersionCheck(result.CLIVersion)
	}
	return result
}

// logVersionCheck logs a warning for unknown version strings (FR-5.6). It
// returns true when the version matches a known prefix.
func (d *OpencodeDriver) logVersionCheck(ver string) bool {
	for _, prefix := range knownOpencodeVersionPrefixes {
		if strings.Contains(ver, prefix) {
			slog.Info("runner/opencode: CLI version", "version", ver)
			return true
		}
	}
	slog.Warn("runner/opencode: unknown CLI version — proceeding with graceful degradation", "version", ver)
	return false
}

// compile-time interface assertion
var _ ExternalAgentRunner = (*OpencodeDriver)(nil)
