// Package audit — boot-abort stderr fallback (FR-063).
//
// When the audit subsystem itself is unavailable during a boot-abort path
// (`agent.config.corrupt`, `agent.config.invalid_policy_value`,
// `gateway.config.invalid_value`), the gateway MUST print a structured
// stderr line BEFORE exiting non-zero so log shippers / systemd journals
// retain the failure cause even in the worst case.
//
// The line shape is documented in the spec and is part of the operator
// contract; do not change it without spec amendment.

package audit

import (
	"fmt"
	"io"
	"os"
)

// EmitBootAbortStderr writes one structured stderr line of the form:
//
//	BOOT_ABORT_REASON=<event> agent_id=<id> path=<path> error=<msg>
//
// then returns. The caller is responsible for the actual os.Exit. Calling
// this function is safe even when the regular audit logger has not been
// constructed yet — it deliberately bypasses the Logger.
//
// `extra` may be nil; when populated, additional key=value pairs are
// appended in deterministic insertion order (relying on Go 1.21+ slice
// order). Values containing spaces or `=` are quoted with %q so log
// parsers do not split mid-field.
//
// Returns the number of bytes written, primarily so tests can assert
// stderr was actually touched. The default writer is os.Stderr; tests
// inject an alternative via the package-level BootAbortWriter variable.
//
// FR-063.
func EmitBootAbortStderr(event, agentID, path string, err error, extra []KV) int {
	w := bootAbortWriterOrDefault()
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	line := fmt.Sprintf("BOOT_ABORT_REASON=%s agent_id=%s path=%s error=%q",
		nonEmpty(event, "<unknown>"),
		nonEmpty(agentID, "-"),
		nonEmpty(path, "-"),
		errMsg,
	)
	for _, kv := range extra {
		line += " " + kv.Key + "=" + quoteIfNeeded(kv.Value)
	}
	line += "\n"
	n, werr := io.WriteString(w, line)
	if werr != nil {
		// Last-ditch: try the real os.Stderr in case `w` was a broken test
		// double. Best-effort; the gateway is about to exit anyway.
		_, _ = io.WriteString(os.Stderr, line)
	}
	return n
}

// KV is a key/value pair for EmitBootAbortStderr's `extra` slice.
type KV struct {
	Key   string
	Value string
}

// BootAbortWriter overrides the destination for EmitBootAbortStderr.
// Tests set this to capture output; nil means "use os.Stderr".
var BootAbortWriter io.Writer

func bootAbortWriterOrDefault() io.Writer {
	if BootAbortWriter != nil {
		return BootAbortWriter
	}
	return os.Stderr
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// quoteIfNeeded returns `v` verbatim when it has no whitespace, `=`, or `"`,
// otherwise it wraps it with %q-style quoting.
func quoteIfNeeded(v string) string {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == ' ' || c == '\t' || c == '=' || c == '"' || c == '\n' {
			return fmt.Sprintf("%q", v)
		}
	}
	return v
}
