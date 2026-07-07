// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package audit

// fileorder.go — chronological ordering for cross-file HMAC chain
// operations (Logger.cleanupExpired and VerifyDir).
//
// Bug this file fixes: rotate() (audit.go) names the first rotation of a
// given UTC day `audit-<date>.jsonl`, but falls back to a
// millisecond-suffixed `audit-<date>-<unixmilli>.jsonl` for every
// SUBSEQUENT rotation on that same day, to avoid overwriting the first (see
// rotate()'s dst-collision handling). Both cleanupExpired and VerifyDir used
// to order files with a plain `sort.Strings` on the filename — but '-'
// (0x2D) sorts before '.' (0x2E) in ASCII, so
// "audit-2026-07-07-1700000000000.jsonl" sorts BEFORE
// "audit-2026-07-07.jsonl" even though the plain-named file was rotated out
// FIRST. On any day with 2+ rotations — an entirely normal high-volume
// production scenario, not a contrived edge case — this silently reorders
// the cross-file chain walk and breaks HMAC verification with NO tampering
// involved at all (confirmed by direct repro: 10 writes / 8 rotations on a
// single day, zero deletions, VerifyDir still reports "hmac mismatch").
// It also corrupts the "oldest N files" boundary that Logger.cleanupExpired
// / the chain-checkpoint fix (checkpoint.go) depends on for correctness.
//
// Fix, and why ModTime alone isn't enough: the obvious fix is to sort by
// each file's on-disk modification time instead of filename. That's an
// improvement but NOT sufficient on its own — under rapid rotation (small
// MaxSizeBytes, or simply a burst of writes), rotate()'s os.Rename can
// preserve a source mtime with the same filesystem timestamp-resolution
// bucket across two or more DIFFERENT files (confirmed by direct repro:
// several rotated files sharing an identical ModTime down to the
// nanosecond field reported by Stat), making ModTime an unreliable sole
// tiebreaker. The one signal that is always authoritative is the data
// itself: every entry carries its own `timestamp` field, so the file
// containing the chronologically-first entry IS, by construction, the
// chronologically-first file. We sort primarily by each file's first
// entry's `timestamp`, falling back to ModTime and then filename only for
// the unusual case of an empty/unparseable/legacy-pre-timestamp file.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"time"
)

// sortAuditFilesChronologically sorts full file paths in place, oldest
// first. See the package doc comment above for the three-tier ordering
// signal (first-entry timestamp, then ModTime, then filename) and why
// filename-only or ModTime-only ordering are both unsafe once rotations
// happen in quick succession or more than once within the same UTC day.
func sortAuditFilesChronologically(paths []string) {
	sort.SliceStable(paths, func(i, j int) bool {
		ti, tiOK := firstEntryTimestamp(paths[i])
		tj, tjOK := firstEntryTimestamp(paths[j])
		if tiOK && tjOK && !ti.Equal(tj) {
			return ti.Before(tj)
		}
		if tiOK != tjOK {
			// One file's first entry couldn't be parsed (empty, corrupt, or
			// a pre-timestamp legacy row) — fall through to ModTime for
			// THIS pair rather than assuming an ordering we can't ground in
			// either file's actual content.
			mi, mj := auditFileModTime(paths[i]), auditFileModTime(paths[j])
			if !mi.Equal(mj) {
				return mi.Before(mj)
			}
			return paths[i] < paths[j]
		}
		mi := auditFileModTime(paths[i])
		mj := auditFileModTime(paths[j])
		if !mi.Equal(mj) {
			return mi.Before(mj)
		}
		// Final deterministic tiebreak (both timestamp and ModTime tied, or
		// both unparseable). Filename order is used ONLY as a last resort
		// here, never as the primary or secondary sort key.
		return paths[i] < paths[j]
	})
}

// firstEntryTimestamp returns the "timestamp" field of the first JSON line
// in path and true, or the zero time.Time and false if the file is empty,
// unreadable, its first non-blank line fails to parse as JSON, or that line
// lacks (or has a zero) "timestamp" field.
func firstEntryTimestamp(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var m struct {
			Timestamp time.Time `json:"timestamp"`
		}
		if err := json.Unmarshal(line, &m); err != nil {
			return time.Time{}, false
		}
		if m.Timestamp.IsZero() {
			return time.Time{}, false
		}
		return m.Timestamp, true
	}
	return time.Time{}, false
}

// auditFileModTime returns path's modification time, or the zero time.Time
// if it cannot be stat'd (e.g. deleted between Glob and Stat — a benign
// race during cleanup). Zero sorts first, which is the safe default:
// treating an unreadable file as "oldest" keeps it out of the way of files
// we CAN verify, rather than risking it sorting as newest and silently
// pushing a real file out of position.
func auditFileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
