// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package audit

// query.go — read-back support for the audit log.
//
// The audit.Logger is otherwise a write-only, append-only JSONL writer (see
// audit.go). This file adds the one narrow read capability the codebase
// needs today: "who was the last agent to successfully perform event X
// against path P" (LastWriterForPath). It backs the write_file cross-agent
// overwrite-attribution note (pkg/tools/path_audit.go's emitFileWriteAudit /
// lastWriterForPath) — CoreTeam-shared workspace directories let two
// different agents write the same relative filename into the same physical
// directory, and the audit log is the authoritative write-history record to
// answer "did someone else touch this path before me".
//
// This is NOT a general query engine: it is a single bounded backward scan
// used purely for a best-effort, informational note, never for a security
// or blocking decision. See LastWriterForPath's doc comment for the scope
// bound (current file only) and why that's an acceptable trade-off here.

import (
	"bufio"
	"encoding/json"
	"os"
)

// LastWriterForPath scans the audit log for the most recent entry with the
// given event name, Decision == "allow", and Details["path"] == path,
// returning the AgentID that produced it.
//
// Scope bound (deliberate, documented — not a silent gap): only the
// CURRENTLY OPEN (not yet rotated) audit file is consulted. A write
// recorded in an already-rotated file (typically: from an earlier day) is
// treated the same as no prior writer on record (found=false). This keeps
// the lookup a single bounded file scan instead of an unbounded walk across
// the full, potentially-multi-day audit history on every overwrite. It is
// acceptable because every caller of this method uses the result purely
// informationally (see pkg/tools/path_audit.go) — a missed
// same-file-different-day collision simply produces no note, never a false
// one, and never blocks the write.
//
// Best-effort: returns ("", false) on a nil logger, an empty path, a
// degraded logger, an unreadable audit file, or no match. Never returns an
// error — callers must not block on this lookup, matching the nil-safe,
// best-effort contract the rest of this package follows (see Logger.Log's
// doc comment).
func (l *Logger) LastWriterForPath(event, path string) (agentID string, found bool) {
	if l == nil || path == "" {
		return "", false
	}

	l.mu.Lock()
	degraded := l.degraded
	auditFile := l.auditPath()
	l.mu.Unlock()

	if degraded {
		return "", false
	}

	f, err := os.Open(auditFile)
	if err != nil {
		return "", false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Default bufio.Scanner token cap (64KB) can be exceeded by unrelated
	// entries in the same file (e.g. large llm_call payloads). Raise it so a
	// single oversized line elsewhere in the file cannot abort the scan
	// before it reaches the entries we actually care about.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	// Entries are append-only, oldest first: keep overwriting on every
	// match so the last one standing after the scan is the most recent.
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if jsonErr := json.Unmarshal(line, &e); jsonErr != nil {
			continue
		}
		if e.Event != event || e.Decision != DecisionAllow || e.AgentID == "" {
			continue
		}
		p, _ := e.Details["path"].(string)
		if p != path {
			continue
		}
		agentID = e.AgentID
		found = true
	}

	return agentID, found
}
