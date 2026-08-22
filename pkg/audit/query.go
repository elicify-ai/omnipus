// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package audit

// query.go — "who last wrote path P" read-back support for the audit log.
//
// The audit package already has read paths — Logger.Verify / VerifyFile /
// VerifyDir (verify.go) walk the JSONL files to check the HMAC chain. This
// file adds a DIFFERENT kind of read: a content query ("find the most
// recent entry matching this event+path"), not a chain-integrity check.
// LastWriterForPath does NOT verify the HMAC chain over the lines it reads
// (a tampered or truncated entry can still be "found") — that's an
// accepted trade-off, not an oversight: the result is used purely for a
// best-effort, informational note (see pkg/tools/path_audit.go), never for
// a security or blocking decision, so paying VerifyDir's chain-walk cost
// here would be a mismatch between the check's cost and its stakes.
//
// It backs the write_file cross-agent overwrite-attribution note —
// CoreTeam-shared workspace directories let two different agents write the
// same relative filename into the same physical directory, and the audit
// log is the authoritative write-history record to answer "did someone
// else touch this path before me".
//
// Multi-file scope: VerifyDir's chronological multi-file walk (via
// fileorder.go's sortAuditFilesChronologically) is the prior art this file
// reuses for file ordering — but LastWriterForPath deliberately does NOT
// walk the full directory the way VerifyDir does. See its doc comment for
// the current-file + single-most-recent-rotated-file bound and why that's
// the right cost/benefit line for an informational lookup performed on
// every overwrite call.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// LastWriterForPath scans the audit log for the most recent entry with the
// given event name, Decision == "allow", Details["op"] == "write", and
// Details["path"] == path, returning the AgentID that produced it.
//
// Scope bound (deliberate, documented — not a silent gap): only the
// CURRENTLY OPEN (not yet rotated) audit file and the single MOST-RECENT
// rotated file are consulted — not an unbounded walk across the full,
// potentially-multi-day audit history. A write recorded further back than
// that (typically: two or more rotations ago) is treated the same as no
// prior writer on record (found=false). This is acceptable because every
// caller of this method uses the result purely informationally (see
// pkg/tools/path_audit.go) — a missed older collision simply produces no
// note, never a false one, and never blocks the write. This covers the
// common case directly: a same-day rotation (small MaxSizeBytes or a burst
// of writes) no longer defeats the lookup the way a current-file-only scan
// would have.
//
// Match predicate detail: Details["op"] == "write" is checked (not just
// Details["path"]) so a future file_op emitter recording a different
// operation against the same path (e.g. a hypothetical "delete" op) can
// never be mistaken for a writer. AgentID == "" entries are skipped — an
// audit entry with no attributable agent cannot answer "which agent wrote
// this".
//
// Windows note: reading a file while Logger.rotate() concurrently renames
// it is safe on Linux/macOS (POSIX rename-with-open-fd semantics) but can
// fail the rename on Windows if a share-mode-incompatible handle is still
// open, which would latch the ENTIRE audit logger into degraded mode (see
// rotate()'s error path) — not just this lookup. To minimize that window,
// each file is opened, read fully into memory, and closed (os.ReadFile)
// rather than held open for the duration of the scan; the race is narrowed
// to "os.ReadFile in flight when rotate() renames", not eliminated. A full
// Windows-safe fix (e.g. Logger coordinating reads and rotation under one
// lock) is out of scope here — this is a known, documented limitation.
//
// Best-effort, never blocking: returns ("", false) on a nil logger, an
// empty path, a degraded logger, an unreadable/unscannable audit file, or
// no match. Unlike Log (which returns an error the caller can act on),
// LastWriterForPath has no error return at all — callers must not, and
// structurally cannot, block or fail a write on this lookup. Failures are
// still traceable, though: each of the three ways this can come back empty
// due to a problem (rather than a genuine "no prior writer") logs at
// Debug/Warn — see the degraded-skip, per-file read/scan failure sites
// below.
func (l *Logger) LastWriterForPath(event, path string) (agentID string, found bool) {
	if l == nil || path == "" {
		return "", false
	}

	l.mu.Lock()
	degraded := l.degraded
	dir := l.dir
	auditFile := l.auditPath()
	l.mu.Unlock()

	if degraded {
		slog.Debug("audit: LastWriterForPath skipped — logger is degraded",
			"event", event, "path", path)
		return "", false
	}

	// Oldest-to-newest: current file's entries are always more recent than
	// any rotated file's, so scanning in this order and letting each file's
	// result overwrite the previous one naturally keeps the most recent match.
	files := make([]string, 0, 2)
	if rotated, ok := mostRecentRotatedFile(dir); ok {
		files = append(files, rotated)
	}
	files = append(files, auditFile)

	for _, filePath := range files {
		fileAgent, fileFound, scanErr := scanFileForLastWriter(filePath, event, path)
		if scanErr != nil {
			slog.Warn(
				"audit: LastWriterForPath could not scan a file — attribution for this path may be stale or missing",
				"file",
				filePath,
				"event",
				event,
				"path",
				path,
				"error",
				scanErr,
			)
			continue
		}
		if fileFound {
			agentID = fileAgent
			found = true
		}
	}

	return agentID, found
}

// mostRecentRotatedFile returns the chronologically-last rotated audit file
// under dir (per sortAuditFilesChronologically, the same ordering VerifyDir
// uses), or ("", false) if none exist or the glob fails.
func mostRecentRotatedFile(dir string) (string, bool) {
	matches, err := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
	if err != nil || len(matches) == 0 {
		return "", false
	}
	sortAuditFilesChronologically(matches)
	return matches[len(matches)-1], true
}

// scanFileForLastWriter reads filePath fully into memory — closing the file
// handle before scanning begins, per LastWriterForPath's Windows note above
// — and returns the LAST entry matching event / Decision=="allow" /
// Details["op"]=="write" / Details["path"]==path, plus any read or scan
// error encountered (wrapped with "read:"/"scan:" so the caller's log line
// distinguishes the two failure classes without needing separate call sites).
func scanFileForLastWriter(filePath, event, path string) (agentID string, found bool, err error) {
	// #nosec G304 -- filePath is always either l.auditPath() or the result of
	// mostRecentRotatedFile(l.dir) (see LastWriterForPath above), both rooted
	// in the deployment-configured audit directory, never request-derived.
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", false, fmt.Errorf("read: %w", err)
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Default bufio.Scanner token cap (64KB) can be exceeded by unrelated
	// entries in the same file — e.g. pkg/tools/shell.go's EventExec entries
	// embed the full shell Command string (arbitrary length, no truncation),
	// a real currently-emitted case. Raise the cap so one oversized line
	// elsewhere in the file can never abort the scan before it reaches the
	// entries we actually care about.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	// Entries are append-only, oldest first: keep overwriting on every
	// match so the last one standing after the loop is the most recent.
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
		op, opOK := e.Details["op"].(string)
		if !opOK || op != "write" {
			continue
		}
		p, pathOK := e.Details["path"].(string)
		if !pathOK || p != path {
			continue
		}
		agentID = e.AgentID
		found = true
	}

	if scanErr := scanner.Err(); scanErr != nil {
		// A truncated scan (e.g. a line exceeding the buffer cap) can hide
		// the TRUE most recent match, not just fail to find one — surface
		// this distinctly from a clean "not found" via the wrapped error so
		// the caller logs it, rather than silently returning whatever
		// partial result the scan reached.
		return "", false, fmt.Errorf("scan: %w", scanErr)
	}

	return agentID, found, nil
}
