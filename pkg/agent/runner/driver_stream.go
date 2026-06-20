// Package runner — shared streaming helpers for external CLI drivers.
//
// driver_stream.go provides the line-framed JSON event parser used by all three
// CLI drivers (Claude Code, Codex, opencode). It maps CLI-specific JSON events
// to the canonical RunEvent kinds defined in runner.go.
//
// Design (FR-5.2, FR-5.6):
//   - Each CLI emits newline-delimited JSON (NDJSON) on stdout.
//   - The parser reads lines, unmarshals each as a raw map[string]any, then
//     dispatches by the "type" field to produce a RunEvent.
//   - Unknown event types are silently skipped (graceful degradation per FR-5.6).
//   - Malformed JSON lines are recorded as non-fatal ErrorEvents (FR-5.2 edge case).
//   - Version detect: each driver checks the CLI version before streaming and logs
//     a warning on unknown versions (FR-5.6).
package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// stderrTail is a bounded, concurrency-safe ring buffer that retains the most
// recent stderr lines from an external-CLI child process. It exists so that a
// NON-ZERO exit can surface the real diagnostic (auth failure, CLI usage error,
// stack trace) at Warn/Error level — otherwise the lines are only logged at
// Debug and the operator sees nothing but a bare "exited: status 1".
//
// The ring keeps at most maxStderrTailLines lines; older lines are evicted.
type stderrTail struct {
	mu    sync.Mutex
	lines []string
	max   int
}

// maxStderrTailLines bounds the retained stderr so a chatty CLI cannot grow the
// buffer without limit. 40 lines is enough to carry a typical CLI error +
// usage/help block while staying small.
const maxStderrTailLines = 40

func newStderrTail() *stderrTail {
	return &stderrTail{max: maxStderrTailLines}
}

// add appends a line, evicting the oldest when the cap is reached.
func (t *stderrTail) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, line)
	if len(t.lines) > t.max {
		// Drop the oldest overflow lines.
		t.lines = t.lines[len(t.lines)-t.max:]
	}
}

// String returns the retained lines joined by newlines (oldest first). Empty
// when no stderr was captured.
func (t *stderrTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}

// buildChildEnv computes the environment for an external-CLI child process,
// shared by all three drivers (Claude Code, Codex, opencode).
//
// SECURITY (Spec-4 FR-5.3 / SEC-23): when callerEnv is non-nil it is used as the
// COMPLETE base — it is already the scrubbed runner-credential allowlist produced
// by sandbox.ScrubGatewayEnvForRunner (see pkg/agent/external_dispatch.go). The
// drivers MUST NOT fall back to os.Environ() in that case, or the spawned CLI
// would inherit the entire gateway environment — including OMNIPUS_MASTER_KEY and
// every other gateway secret. That is exactly the leak this helper prevents.
//
// callerEnv == nil means "no explicit env was supplied" (direct/test callers and
// the connection-test path). Only then do we fall back to os.Environ() so those
// paths keep working. The production dispatch path (external_dispatch.go) always
// supplies a non-nil scrubbed RunOptions.Env, so the gateway secrets never leak.
//
// A non-nil but empty callerEnv yields an empty child env (an explicit, auditable
// "inherit nothing" request) — it is NOT treated as "unset".
func buildChildEnv(callerEnv []string) []string {
	if callerEnv == nil {
		// No explicit env supplied — inherit the parent process env. Reserved for
		// direct/test callers and the version/connection-test path that do not go
		// through the scrubbed dispatch flow.
		return os.Environ()
	}
	// callerEnv is the authoritative, already-scrubbed allowlist. Return a defensive
	// copy so a driver mutating cmd.Env cannot corrupt the caller's slice.
	out := make([]string, len(callerEnv))
	copy(out, callerEnv)
	return out
}

// streamParser is a callback-based NDJSON parser.
// It reads lines from r, calls parseLine for each non-empty line,
// and sends the resulting RunEvents to out. It exits when ctx is
// cancelled or r returns EOF.
//
// parseLine must be non-nil. It receives the raw JSON bytes for a single
// NDJSON line and returns (event, ok). When ok=false the event is skipped.
//
// It returns emittedFatal=true if it sent at least one fatal EventKindError
// (either from a parseLine result or from a stream read error). Callers use
// this to suppress a duplicate terminal error event after cmd.Wait().
func streamParser(
	ctx context.Context,
	r io.Reader,
	runID string,
	parseLine func(raw []byte) (RunEvent, bool),
	out chan<- RunEvent,
) (emittedFatal bool) {
	scanner := bufio.NewScanner(r)
	// Increase the default buffer: some CLIs emit large single lines.
	scanner.Buffer(make([]byte, 512*1024), 1024*1024)
	for {
		select {
		case <-ctx.Done():
			return emittedFatal
		default:
		}
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		ev, ok := parseLine([]byte(line))
		if !ok {
			continue
		}
		if ev.Timestamp.IsZero() {
			ev.Timestamp = time.Now().UTC()
		}
		if ev.RunID == "" {
			ev.RunID = runID
		}
		if ev.Kind == EventKindError && ev.Err != nil && ev.Err.Fatal {
			emittedFatal = true
		}
		select {
		case out <- ev:
		case <-ctx.Done():
			return emittedFatal
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		slog.Warn("runner: stream scanner error", "run_id", runID, "err", err)
		select {
		case out <- RunEvent{
			Kind:      EventKindError,
			RunID:     runID,
			Timestamp: time.Now().UTC(),
			Err:       &ErrorEvent{Message: fmt.Sprintf("stream read error: %v", err), Fatal: true},
		}:
			emittedFatal = true
		default:
		}
	}
	return emittedFatal
}

// detectCLIVersion runs `<binary> --version` and returns the parsed version
// string (a clean version token, not the raw output line). On failure it
// returns ("", err). The caller SHOULD log a warning on unknown versions and
// degrade gracefully (FR-5.6).
//
// Both stdout and stderr are considered: some CLIs print their version to
// stderr, so CombinedOutput is used (mirroring conntest.probeVersion).
func detectCLIVersion(ctx context.Context, binary string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("version check for %q: %w", binary, err)
	}
	text := strings.TrimSpace(string(out))
	v := extractVersion(text)
	if v == "" {
		// The binary ran (no error) but emitted nothing version-looking. Return
		// the trimmed first line as a best-effort handshake signal so the caller
		// can still log a degraded-version warning rather than treating it as a
		// hard detection failure.
		return firstLine(text), nil
	}
	return v, nil
}

// detectAndPinVersion detects the CLI version, classifies it against the
// driver's known version prefixes, logs the result, and returns the parsed
// version string plus a known flag (FR-5.6 / N3). On detection failure it
// returns ("", false) and logs a warning — the caller proceeds with graceful
// degradation rather than aborting the run. logTag is a short driver label
// (e.g. "runner/claude") used in log lines.
func detectAndPinVersion(ctx context.Context, binary, logTag string, knownPrefixes []string) (version string, known bool) {
	ver, err := detectCLIVersion(ctx, binary)
	if err != nil {
		slog.Warn(logTag+": version check failed — proceeding with unknown version",
			"binary", binary, "err", err)
		return "", false
	}
	for _, prefix := range knownPrefixes {
		if strings.Contains(ver, prefix) {
			slog.Info(logTag+": CLI version pinned",
				"binary", binary, "version", ver)
			return ver, true
		}
	}
	slog.Warn(logTag+": unknown CLI version — proceeding with graceful degradation",
		"binary", binary, "version", ver)
	return ver, false
}

// rawJSONField extracts a string field from raw JSON bytes without full
// unmarshalling (used in hot paths where only a single discriminator field
// is needed). Returns ("", false) if the field is absent or not a string.
func rawJSONField(data []byte, field string) (string, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return "", false
	}
	raw, ok := m[field]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// safeMarshal encodes v to JSON bytes; returns nil on error.
func safeMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
