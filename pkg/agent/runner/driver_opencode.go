// driver_opencode.go — opencode CLI streaming driver (FR-5.2, FR-5.6, US-4)
//
// Drives `opencode run --format json` (net-new per Spec-4).
// opencode is the user's installed binary; it is not a Go dependency.
//
// opencode stream-json event shapes — two vocabularies are read:
//
// ≥ 0.1.0 (legacy, kept working):
//
//	{"type":"message.start","message":{"id":"...","role":"assistant",...}}
//	{"type":"content.delta","delta":{"type":"text_delta","text":"..."}}
//	{"type":"content.done","content":{...}}
//	{"type":"tool.start","tool":{"id":"...","name":"...","input":{...}}}
//	{"type":"tool.done","tool":{"id":"...","name":"...","output":"..."},"exitCode":0}
//	{"type":"session.complete","session_id":"...","usage":{...}}
//	{"type":"error","message":"...","code":"..."}
//
// ≥ 2.x (recorded from a real `opencode run --format json`, v2.2.6):
//
//	{"type":"step_start","part":{...,"type":"step-start",...}}
//	{"type":"text","part":{...,"type":"text","text":"..."}}
//	{"type":"tool_use","part":{...,"type":"tool","id":"...","tool":"write",
//	          "state":{"status":"completed","input":{...},"output":"..."}}}
//	{"type":"step_finish","part":{...,"type":"step-finish","reason":"stop"|"tool-calls",...}}
//	{"type":"error","error":{"type":"...","message":"..."}}
//
// One COMPLETED tool_use line carries both the call and its result, so it
// yields two public events (tool-call, then tool-result). step_finish never
// ends the run — reason "tool-calls" closes a mid-run step and "stop" the
// final one; the single End is emitted when the run (stream/process) ends.
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
// this driver (FR-5.6). "2." covers the installed CLI (v2.2.6), whose stream
// vocabulary this driver reads.
var knownOpencodeVersionPrefixes = []string{"0.", "1.", "2."}

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
	// runMaxTurns is the turn cap of the most recent Run; Resume reuses it
	// so a resumed run keeps its original cap (#904 D4). Zero until the
	// first Run, so a Resume with no prior Run is refused (FR-004).
	runMaxTurns int
	// nativeID is the opencode session id announced on the most recent Run's
	// stream (session.start / session.complete session_id). Resume passes it
	// via -s/--session so the prior native conversation is actually continued;
	// an empty id makes Resume refuse visibly instead of starting fresh
	// (FR-043).
	nativeID string
	// lastOpts is the RunOptions of the most recent Run, reused by Resume so a
	// resumed conversation preserves its runtime/workspace/model/caps (FR-043).
	// Zero value until the first Run.
	lastOpts RunOptions
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
	if err := validateMaxTurns("opencode", opts.MaxTurns); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.eventCh != nil {
		return nil, fmt.Errorf("opencode driver: Run called while a run is already active")
	}
	d.runMaxTurns = opts.MaxTurns
	d.lastOpts = opts
	if opts.resumeNativeID == "" {
		// A fresh run begins a new native conversation; drop any id captured
		// from a previous run until this run's stream announces its own.
		d.nativeID = ""
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

	// FR-029 (ADR-057 U22, W9c): put the child in its own process group and
	// make cancellation (runCtx via cancelFn/Cancel below) signal the whole
	// group — see procgroup_unix.go / procgroup_windows.go. Without this a
	// subprocess tree the CLI spawns survives as a silent orphan after
	// Cancel().
	u22SetupProcessGroup(cmd)
	u22InstallGroupCancel(cmd)

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

		// Turn cap (FR-5.4): the caller's resolved limit, validated > 0 at the
		// top of Run — no hidden default (#904 FR-004).
		maxTurns := opts.MaxTurns
		turnCount := 0

		// endEmitted tracks whether the stream itself announced the terminal
		// End (≥ 0.1.0 session.complete); the run-end synthesis below must
		// then stay silent so the consumer sees exactly one terminal outcome.
		endEmitted := false
		emittedFatal := streamParserMulti(runCtx, pr, runID, func(raw []byte) ([]RunEvent, bool) {
			events, ok := d.parseLine(raw, runID, &turnCount, maxTurns)
			if ok {
				for _, ev := range events {
					if ev.Kind == EventKindEnd {
						endEmitted = true
					}
				}
			}
			return events, ok
		}, ch)

		// Liveness: the scanner may have stopped early (e.g. an oversized
		// token) while stdout still has unread data. exec's stdout copier is
		// then blocked writing into this pipe and Cmd.Wait waits for that
		// copy — waiting on waitErr here would deadlock until the context is
		// canceled. Close the read end NOW: the blocked write fails, the
		// copier finishes, and Wait can return. Scanning has already stopped,
		// so the unread remainder is unusable regardless. Harmless on the
		// normal EOF path (the copier has already finished); the deferred
		// close below becomes a no-op.
		_ = pr.Close()

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
		} else if err == nil && runCtx.Err() == nil && !emittedFatal && !endEmitted {
			// The run ended cleanly with NO inline terminal event — the ≥ 2.x
			// vocabulary has no session.complete; its stream just stops after
			// the final step_finish. Emit the single End here, when the run
			// actually ended, so the consumer still sees exactly one terminal
			// outcome (an inline End sets endEmitted and skips this branch).
			ch <- RunEvent{
				Kind:      EventKindEnd,
				RunID:     runID,
				Timestamp: time.Now().UTC(),
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
	// Native-conversation resume (FR-043): -s/--session <id> continues an
	// EXISTING session by the id opencode announced on the prior stream
	// (opts.resumeNativeID). It is NEVER passed the fresh-dispatch opts.RunID:
	// opencode has never seen that identifier, so it would target a
	// non-existent session. Empty on a fresh run.
	if id := strings.TrimSpace(opts.resumeNativeID); id != "" {
		args = append(args, "--session", id)
	}
	// Append operator-supplied extra args (ExecutorConfig.cli_args, MAJ-5).
	// ADR-032 fix M-1: a redundant/conflicting copy of the driver's own
	// --dangerously-skip-permissions flag is stripped first — see
	// argsafety.go. This filter applies to opts.CLIArgs ONLY; the driver's
	// own --dangerously-skip-permissions flag above is never touched.
	kept, dropped := filterDangerousCLIArgs("opencode", opts.CLIArgs)
	logDroppedCLIArgs("runner/opencode", "opencode", opts.RunID, dropped)
	args = append(args, kept...)
	args = append(args, presetArgsForBuild("runner/opencode", "opencode", opts)...) // r17 §3.7 after the agent args; only a model option is dropped (#1880)
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
// expected "provider/model" form: a non-empty provider prefix, one '/', then
// a non-empty rest that may itself contain further '/' — nested model names
// such as openrouter/deepseek/deepseek-v4.1-flash (a provider, then the
// provider's own model path) are valid and passed whole. Used to guard the
// --model flag (ADR-032 fix C) so a bare model name from an agent configured
// for a different CLI (claude/codex use bare model names) is never passed to
// opencode as garbage.
func looksLikeProviderModel(model string) bool {
	idx := strings.IndexByte(model, '/')
	// A provider prefix before the FIRST '/' and a non-empty rest after it;
	// the rest may hold any number of further '/' segments.
	return idx > 0 && idx < len(model)-1
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

// parseLine maps a single NDJSON line from `opencode run --format json` to the
// RunEvents that line yields, in stream order. Most lines yield one event; a
// COMPLETED ≥ 2.x tool_use line carries both the call and its result, so it
// yields two (tool-call, then tool-result). ok=false skips the line.
func (d *OpencodeDriver) parseLine(
	raw []byte,
	runID string,
	turnCount *int,
	maxTurns int,
) ([]RunEvent, bool) {
	typ, ok := rawJSONField(raw, "type")
	if !ok {
		slog.Debug("runner/opencode: malformed JSON line", "run_id", runID, "raw", string(raw))
		return []RunEvent{{
			Kind:  EventKindError,
			RunID: runID,
			Err:   &ErrorEvent{Message: fmt.Sprintf("malformed JSON: %s", raw), Fatal: false},
		}}, true
	}

	// ≥ 2.x announces the native conversation as a top-level sessionID on
	// every event line. Capture it so Resume/ResumeWith continue THIS
	// conversation via -s/--session (FR-043); the ≥ 0.1.0 vocabulary announces
	// it as session.start/session.complete session_id, handled in their
	// branches below. A legacy line has no top-level sessionID field.
	if sessionID, ok := rawJSONField(raw, "sessionID"); ok && sessionID != "" {
		storeNativeID(&d.mu, &d.nativeID, sessionID)
	}

	switch typ {
	case "content.delta":
		if ev, emitted := d.parseContentDelta(raw, runID); emitted {
			return []RunEvent{ev}, true
		}
		return nil, false

	case "tool.start":
		if ev, emitted := d.parseToolStart(raw, runID); emitted {
			return []RunEvent{ev}, true
		}
		return nil, false

	case "message.start":
		// Track turns on message.start.
		if ev, capExceeded := d.bumpTurnCap(runID, turnCount, maxTurns); capExceeded {
			return []RunEvent{ev}, true
		}
		return nil, false

	case "step_start":
		// ≥ 2.x: opens one agent step of the run's loop — the analogue of
		// message.start, so it counts toward the same turn cap (FR-5.4).
		if ev, capExceeded := d.bumpTurnCap(runID, turnCount, maxTurns); capExceeded {
			return []RunEvent{ev}, true
		}
		return nil, false

	case "text":
		// ≥ 2.x: the assistant's answer text lives in part.text.
		return parseOpencodeTextPart(raw, runID)

	case "tool_use":
		// ≥ 2.x: one COMPLETED line carries both the call and its result.
		return parseOpencodeToolUse(raw, runID)

	case "step_finish":
		// ≥ 2.x: part.reason is "tool-calls" between steps and "stop" at the
		// final one. Neither ends the RUN — no End is emitted per step; the
		// single End is synthesized when the run (stream/process) ends.
		return nil, false

	case "session.start":
		// Session bootstrap event — carries the native session id (captured
		// below); no RunEvent is emitted for it.
		if sessionID, ok := rawJSONField(raw, "session_id"); ok && sessionID != "" {
			storeNativeID(&d.mu, &d.nativeID, sessionID)
		}
		return nil, false

	case "session.complete":
		if sessionID, _ := rawJSONField(raw, "session_id"); sessionID != "" {
			// Capture the native session id so Resume continues THIS
			// conversation via -s/--session rather than starting fresh (FR-043).
			storeNativeID(&d.mu, &d.nativeID, sessionID)
			slog.Info("runner/opencode: session complete", "run_id", runID, "session_id", sessionID)
		}
		return []RunEvent{{Kind: EventKindEnd, RunID: runID}}, true

	case "error":
		return parseOpencodeError(raw, runID), true

	default:
		slog.Debug("runner/opencode: unknown event type", "run_id", runID, "type", typ)
		return nil, false
	}
}

// bumpTurnCap counts one agent step against the run's turn cap (FR-5.4,
// #904 FR-004 — no hidden default). It returns the fatal turn-cap event with
// capExceeded=true after cancelling the run, and a zero event with false when
// the step is admitted. Shared by the message.start (≥ 0.1.0) and step_start
// (≥ 2.x) turn branches.
func (d *OpencodeDriver) bumpTurnCap(runID string, turnCount *int, maxTurns int) (ev RunEvent, capExceeded bool) {
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

// parseOpencodeTextPart handles the "text" event (opencode ≥ 2.x): the
// assistant's answer text lives in part.text. An empty text emits nothing.
func parseOpencodeTextPart(raw []byte, runID string) ([]RunEvent, bool) {
	var ev struct {
		Part struct {
			Text string `json:"text"`
		} `json:"part"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		// The line was recognized as a text event, so losing its payload to a
		// decode failure is not an ignorable unknown type (F3): surface it.
		return []RunEvent{undecodableOpencodeEvent(runID, "text", err)}, true
	}
	if ev.Part.Text == "" {
		return nil, false
	}
	return []RunEvent{{
		Kind:   EventKindOutput,
		RunID:  runID,
		Output: &OutputEvent{Text: ev.Part.Text},
	}}, true
}

// parseOpencodeToolUse handles the "tool_use" event (opencode ≥ 2.x). One
// COMPLETED line carries both the call and its result in part.state, so it
// yields two public events in stream order: the ToolCall, then the correlated
// ToolResult. status "completed" yields a successful result; status "error"
// yields the FAILED result with part.state.error's diagnostic and IsError set
// — the same public result path a success takes, so the live tool-start gets
// its matching failure outcome. The failed status prevails over any stale
// success-shaped output field, and the failure is a tool outcome, not a
// run-fatal one: the agent may recover (later records still stream). A part
// still pending/running yields the ToolCall only: no result is asserted for
// a call that has not finished.
func parseOpencodeToolUse(raw []byte, runID string) ([]RunEvent, bool) {
	var ev struct {
		Part struct {
			ID    string `json:"id"`
			Tool  string `json:"tool"`
			State struct {
				Status string          `json:"status"`
				Input  json.RawMessage `json:"input"`
				Output json.RawMessage `json:"output"`
				Error  string          `json:"error"`
			} `json:"state"`
		} `json:"part"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		// Recognized tool event with an undecodable payload — never silence
		// it (F3); the call record would otherwise vanish without a trace.
		return []RunEvent{undecodableOpencodeEvent(runID, "tool_use", err)}, true
	}
	if ev.Part.ID == "" && ev.Part.Tool == "" {
		return nil, false
	}
	events := []RunEvent{{
		Kind:  EventKindToolCall,
		RunID: runID,
		ToolCall: &ToolCallEvent{
			CallID:    ev.Part.ID,
			ToolName:  ev.Part.Tool,
			ToolInput: safeCopyRaw(ev.Part.State.Input),
		},
	}}
	switch {
	case ev.Part.State.Status == "completed" && len(ev.Part.State.Output) > 0:
		events = append(events, RunEvent{
			Kind:  EventKindToolResult,
			RunID: runID,
			ToolResult: &ToolResultEvent{
				CallID:   ev.Part.ID,
				ToolName: ev.Part.Tool,
				Output:   safeCopyRaw(ev.Part.State.Output),
			},
		})
	case ev.Part.State.Status == "error":
		// Terminal failed tool (the installed 2.2.6 emitter's
		// session.tool.failed branch sends status:"error" with
		// error:state.error.message). The diagnostic is the output, IsError
		// marks the outcome; nothing is treated as a run-fatal condition.
		events = append(events, RunEvent{
			Kind:  EventKindToolResult,
			RunID: runID,
			ToolResult: &ToolResultEvent{
				CallID:   ev.Part.ID,
				ToolName: ev.Part.Tool,
				Output:   safeMarshal(ev.Part.State.Error),
				IsError:  true,
			},
		})
	}
	return events, true
}

// undecodableOpencodeEvent builds the visible fatal error for a recognized
// event line whose typed payload cannot be decoded (F3). The content is
// lost, so the failure must be public, name the event kind for diagnosis,
// and — being fatal — prevent a later successful completion.
func undecodableOpencodeEvent(runID, kind string, err error) RunEvent {
	slog.Warn("runner/opencode: undecodable event payload", "run_id", runID, "event", kind, "err", err)
	return RunEvent{
		Kind:  EventKindError,
		RunID: runID,
		Err: &ErrorEvent{
			Message: fmt.Sprintf("opencode returned an undecodable %s event: %v", kind, err),
			Fatal:   true,
		},
	}
}

// parseOpencodeError handles the "error" event. The provider's own diagnostic
// is surfaced as a fatal error — it must reach the operator, never collapse
// into a generic message — and the error kind is logged so it is not silently
// dropped. Three payload shapes are read (opencodeErrorDiagnostic):
//
//   - opencode ≥ 2.x: error.message carries the diagnostic, error.type its kind;
//     surfaced verbatim.
//   - opencode 1.x (captured from 1.18.18): error.name carries the kind and
//     error.data the diagnostic, with data.message and a data.ref the CLI's
//     own server log is keyed by; surfaced as "<name>: <message> (ref <ref>)".
//   - the ≥ 0.1.0 vocabulary: the message at the top level; surfaced verbatim.
func parseOpencodeError(raw []byte, runID string) []RunEvent {
	msg, errKind := opencodeErrorDiagnostic(raw)
	if msg == "" {
		msg = "unknown opencode error"
	}
	if errKind != "" {
		slog.Warn("runner/opencode: stream error", "run_id", runID, "error_type", errKind, "message", msg)
	}
	return []RunEvent{{
		Kind:  EventKindError,
		RunID: runID,
		// Type is the structured error.type alone (#1857), the classifier's
		// input; the 1.x error name only names the error in msg and the log.
		Err: &ErrorEvent{Message: msg, Fatal: true, Type: rawJSONString(rawJSONObject(raw, "error"), "type")},
	}}
}

// opencodeErrorDiagnostic extracts an "error" event's diagnostic text and its
// kind (error.type, else error.name). Each field is read on its own, so one
// field of an unexpected type never discards the others: an error that is not
// an object (or a data that is not one) leaves the remaining fields readable.
// Precedence: error.message (verbatim), then error.data.message with the
// error's name and data.ref kept, then the top-level message, then the name
// and reference alone. An empty result means nothing readable was sent.
func opencodeErrorDiagnostic(raw []byte) (msg, kind string) {
	top, _ := rawJSONField(raw, "message")
	errObj := rawJSONObject(raw, "error")
	errType := rawJSONString(errObj, "type")
	name := rawJSONString(errObj, "name")
	kind = errType
	if kind == "" {
		kind = name
	}
	if typed := rawJSONString(errObj, "message"); typed != "" {
		return typed, kind
	}
	data := rawJSONObject(errObj["data"], "")
	dataMsg := rawJSONString(data, "message")
	ref := rawJSONString(data, "ref")
	switch {
	case dataMsg != "":
		msg = dataMsg
		if name != "" && !strings.Contains(dataMsg, name) {
			msg = name + ": " + dataMsg
		}
	case top != "":
		return top, kind
	default:
		msg = name
	}
	if ref != "" {
		if msg == "" {
			msg = "ref " + ref
		} else {
			msg += " (ref " + ref + ")"
		}
	}
	return msg, kind
}

// rawJSONObject decodes field of the JSON object data (or data itself when
// field is empty) as an object of raw members. Anything that is not an
// object yields nil, which rawJSONString reads as "no value".
func rawJSONObject(data []byte, field string) map[string]json.RawMessage {
	if field != "" {
		var outer map[string]json.RawMessage
		if json.Unmarshal(data, &outer) != nil {
			return nil
		}
		data = outer[field]
	}
	if len(data) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		return nil
	}
	return obj
}

// rawJSONString returns obj[field] when it is a JSON string, else "".
func rawJSONString(obj map[string]json.RawMessage, field string) string {
	raw, ok := obj[field]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
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

// Input attempts to deliver a live steering instruction to a running opencode
// conversation. `opencode run` exposes no mid-run stdin injection channel, so a
// live instruction cannot be delivered in-process — delivery is by interrupt
// plus native-conversation resume (FR-043). Input therefore NEVER silently
// discards the text: with no live conversation it refuses visibly, and with a
// live run it returns a visible error directing the caller to interrupt and
// resume (BDD-05.6, DEL-20).
func (d *OpencodeDriver) Input(_ string) error {
	d.mu.Lock()
	live := d.eventCh != nil
	d.mu.Unlock()
	if !live {
		return fmt.Errorf("opencode driver: no live conversation to steer — instruction not delivered (FR-043)")
	}
	return fmt.Errorf("opencode driver: cannot inject a live instruction into a running `opencode run` process (no mid-run stdin channel); interrupt and resume the native conversation instead (FR-043)")
}

// Resume continues the opencode native session captured on the prior Run via
// -s/--session <id> (FR-043). It reuses the prior Run's RunOptions
// (runtime/workspace/model/caps) and does NOT re-send the original prompt;
// ResumeWith is the same continuation under new options. When no native session id was captured
// it refuses VISIBLY rather than starting a fresh conversation (BDD-05.6); with
// no prior Run at all it defers to Run's ErrMaxTurnsRequired (no hidden default,
// FR-004).
func (d *OpencodeDriver) Resume(ctx context.Context, runID string, instruction ...string) (<-chan RunEvent, error) {
	d.mu.Lock()
	opts := d.lastOpts
	d.mu.Unlock()

	opts.RunID = runID
	// A delivery resume carries the new instruction S (stdin); a bare resume
	// delivers nothing and never replays the original prompt.
	opts.Input = resumeInstruction(instruction)
	return d.ResumeWith(ctx, opts)
}

// HasNativeConversation reports whether the current or most recent run has
// announced its opencode session id, so an interrupt can be followed by a
// Resume of the same conversation (#1899; see NativeConversationReporter).
func (d *OpencodeDriver) HasNativeConversation() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.nativeID != ""
}

// ResumeWith continues the same native session under opts, not the prior
// Run's options (T11 FR-9/10): see CurrentOptionsResumer.
func (d *OpencodeDriver) ResumeWith(ctx context.Context, opts RunOptions) (<-chan RunEvent, error) {
	d.mu.Lock()
	nativeID := d.nativeID
	d.mu.Unlock()

	if nativeID == "" && opts.MaxTurns > 0 {
		return nil, fmt.Errorf("opencode driver: cannot resume — no native conversation id was captured from the prior run; refusing rather than starting a fresh conversation (FR-043)")
	}
	opts.resumeNativeID = nativeID
	return d.Run(ctx, opts)
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
var (
	_ ExternalAgentRunner   = (*OpencodeDriver)(nil)
	_ CurrentOptionsResumer = (*OpencodeDriver)(nil)
)
