// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package audit — chain verification for the v0.2 #155 tamper-evident
// audit log. Pairs with hmac.go (write-side) and audit.go (Logger).
//
// Verification model:
//
//   ChainResult tracks "is the chain intact?" plus enough metadata for an
//   operator to investigate. It deliberately uses error values rather than
//   panics — the verifier is reachable from the CLI and from runtime
//   self-checks, so it must never crash on a malformed file.
//
//   The verifier walks each file line-by-line, recomputing the HMAC from
//   prev || canonical and comparing against the embedded `hmac` field. The
//   first mismatch is recorded and verification stops for THAT file (so the
//   error report points at the first detectable break, not the cascade of
//   subsequent breaks that follow from one tampered entry).
//
//   Cross-file chain: VerifyDir lists current + rotated files in
//   chronological order, threads the prevHMAC across rotation boundaries,
//   and reports the first break across the whole directory.

package audit

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ChainResult is the outcome of a chain integrity walk.
//
// On success: Valid=true, BrokenAt=-1, Reason="", EntriesScanned reflects
// the number of complete entries inspected (including pre-chain entries).
// FinalHMAC is set to the last entry's HMAC so callers chaining across
// rotation files can pass it as the seed for the next file.
//
// On failure: Valid=false, BrokenAt is the 1-based line index of the first
// broken entry within FailedFile, Reason is a short human-readable
// description (e.g. "hmac mismatch", "missing hmac field", "malformed
// JSON"), and EntriesScanned counts entries up to and including the
// broken one. FinalHMAC is undefined and should not be used to seed
// further verification.
type ChainResult struct {
	Valid          bool   `json:"valid"`
	BrokenAt       int    `json:"broken_at"`
	Reason         string `json:"reason,omitempty"`
	FailedFile     string `json:"failed_file,omitempty"`
	EntriesScanned int    `json:"entries_scanned"`
	FilesScanned   int    `json:"files_scanned"`
	FinalHMAC      []byte `json:"-"`
	// PreChainEntries counts entries that appear BEFORE any HMAC-bearing
	// entry. These are legacy rows from before v0.2 #155 shipped and are
	// reported (not flagged as broken) so an operator running Verify against
	// a log written by an older binary doesn't see false positives.
	PreChainEntries int `json:"pre_chain_entries"`
}

// IsValid satisfies the chainVerificationResult interface used by the
// red-team tamper test. Returns the value of r.Valid.
func (r *ChainResult) IsValid() bool {
	if r == nil {
		return false
	}
	return r.Valid
}

// String returns a one-line human-readable summary suitable for CLI output.
func (r *ChainResult) String() string {
	if r == nil {
		return "audit: chain result: <nil>"
	}
	if r.Valid {
		return fmt.Sprintf("audit: chain VALID (files=%d, entries=%d, pre-chain=%d)",
			r.FilesScanned, r.EntriesScanned, r.PreChainEntries)
	}
	return fmt.Sprintf("audit: chain BROKEN at %s line %d (%s); files=%d entries=%d pre-chain=%d",
		r.FailedFile, r.BrokenAt, r.Reason, r.FilesScanned, r.EntriesScanned, r.PreChainEntries)
}

// Verify re-reads the current audit file from start, recomputes each
// entry's HMAC, and reports whether the chain is intact.
//
// Verify is a runtime self-check — it does not walk rotated files. Use
// VerifyDir for a full directory walk including rotation history.
//
// The Logger's chain key is used; ctx is currently only checked for
// cancellation between entries (no per-entry I/O cancellation), but the
// signature accepts a Context so future implementations can stream large
// files without changing the contract.
func (l *Logger) Verify(ctx context.Context) (*ChainResult, error) {
	if l == nil {
		return nil, fmt.Errorf("audit: Verify called on nil Logger")
	}
	l.mu.Lock()
	// Flush so any buffered writes hit disk before we read.
	if l.writer != nil {
		_ = l.writer.Flush()
	}
	key := make([]byte, len(l.chainKey))
	copy(key, l.chainKey)
	path := l.auditPath()
	l.mu.Unlock()

	return VerifyFile(ctx, path, key, GenesisSeed())
}

// VerifyFile walks `path`, recomputing each entry's HMAC against the
// supplied chain key. seedHMAC is the previous-link HMAC for the first
// entry of the file (use GenesisSeed() for a fresh log; use the previous
// file's FinalHMAC when chaining across rotation files).
//
// Returns a non-nil *ChainResult and nil error for both intact and
// broken chains — the result's Valid field is the boolean. A non-nil
// error is reserved for I/O failures (file unreadable, etc.).
func VerifyFile(ctx context.Context, path string, key []byte, seedHMAC []byte) (*ChainResult, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("audit: VerifyFile requires a non-empty chain key")
	}
	if len(seedHMAC) == 0 {
		seedHMAC = GenesisSeed()
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}
	defer f.Close()

	res := &ChainResult{
		Valid:        true,
		BrokenAt:     -1,
		FilesScanned: 1,
	}
	prev := make([]byte, len(seedHMAC))
	copy(prev, seedHMAC)

	scanner := bufio.NewScanner(f)
	// Audit entries can be large (parameters with large blobs). Set a
	// generous max line size — 1 MiB matches the practical upper bound
	// observed in agent loop redactor stress tests.
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	lineNo := 0
	sawAnyHMAC := false

	for scanner.Scan() {
		lineNo++
		if ctx != nil {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
		}
		raw := scanner.Bytes()
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}

		var m map[string]any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&m); err != nil {
			res.Valid = false
			res.BrokenAt = lineNo
			res.Reason = fmt.Sprintf("malformed JSON: %v", err)
			res.FailedFile = path
			res.EntriesScanned = lineNo
			return res, nil
		}
		hmacField, hasHMAC := m["hmac"].(string)
		if !hasHMAC {
			// Pre-chain entry from before v0.2 #155 shipped. Count it but
			// do not flag — and do not advance prev, since prev should
			// chain to the next HMAC-bearing entry as if these legacy
			// rows didn't exist.
			//
			// Subtle: this means surgical-rewrite of a legacy row is NOT
			// detectable (no chain to break). That's the documented
			// limitation of operating against logs that predate the chain.
			if !sawAnyHMAC {
				res.PreChainEntries++
				continue
			}
			// If we've already seen a chain link earlier in the file and
			// THEN encounter a row without `hmac`, that's a chain break —
			// an attacker stripped the `hmac` field to mask a tampered
			// row. Flag it.
			res.Valid = false
			res.BrokenAt = lineNo
			res.Reason = "missing hmac field on entry after chain start"
			res.FailedFile = path
			res.EntriesScanned = lineNo
			return res, nil
		}
		sawAnyHMAC = true

		expected, err := hex.DecodeString(hmacField)
		if err != nil || len(expected) != 32 {
			res.Valid = false
			res.BrokenAt = lineNo
			res.Reason = fmt.Sprintf("hmac field is not 32-byte hex: %v", err)
			res.FailedFile = path
			res.EntriesScanned = lineNo
			return res, nil
		}

		canonical, err := canonicalJSONWithoutHMAC(raw)
		if err != nil {
			res.Valid = false
			res.BrokenAt = lineNo
			res.Reason = fmt.Sprintf("canonicalise: %v", err)
			res.FailedFile = path
			res.EntriesScanned = lineNo
			return res, nil
		}
		got := computeEntryHMAC(prev, canonical, key)
		if !hmac.Equal(got, expected) {
			res.Valid = false
			res.BrokenAt = lineNo
			res.Reason = "hmac mismatch (entry tampered, reordered, or wrong chain key)"
			res.FailedFile = path
			res.EntriesScanned = lineNo
			return res, nil
		}
		// Advance the chain.
		prev = got
		res.EntriesScanned = lineNo
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, fmt.Errorf("audit: read %s: %w", path, err)
	}

	res.FinalHMAC = prev
	return res, nil
}

// VerifyDir walks every audit file under dir (current + rotated) in
// chronological order, threading the chain across rotation boundaries.
// Returns the first broken-chain result encountered, or a Valid=true
// result if all files chain correctly.
//
// File ordering: rotated files match `audit-*.jsonl` and sort
// lexicographically (which matches chronological order because the
// suffix is YYYY-MM-DD); the current file `audit.jsonl` is verified
// last. If a rotation produced multiple files for the same date
// (audit-2026-05-04-1714845600000.jsonl), the millisecond suffix
// preserves the order.
func VerifyDir(ctx context.Context, dir string, key []byte) (*ChainResult, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("audit: VerifyDir requires a non-empty chain key")
	}

	rotatedPattern := filepath.Join(dir, "audit-*.jsonl")
	rotated, err := filepath.Glob(rotatedPattern)
	if err != nil {
		return nil, fmt.Errorf("audit: glob %s: %w", rotatedPattern, err)
	}
	sort.Strings(rotated)

	currentPath := filepath.Join(dir, "audit.jsonl")
	files := append([]string{}, rotated...)
	if _, err := os.Stat(currentPath); err == nil {
		files = append(files, currentPath)
	}

	if len(files) == 0 {
		return &ChainResult{Valid: true, BrokenAt: -1}, nil
	}

	seed := GenesisSeed()
	totalEntries := 0
	totalPreChain := 0
	for _, f := range files {
		res, err := VerifyFile(ctx, f, key, seed)
		if err != nil {
			return nil, err
		}
		totalEntries += res.EntriesScanned
		totalPreChain += res.PreChainEntries
		if !res.Valid {
			res.FilesScanned = len(files)
			res.EntriesScanned = totalEntries
			res.PreChainEntries = totalPreChain
			return res, nil
		}
		seed = res.FinalHMAC
	}
	return &ChainResult{
		Valid:           true,
		BrokenAt:        -1,
		FilesScanned:    len(files),
		EntriesScanned:  totalEntries,
		PreChainEntries: totalPreChain,
		FinalHMAC:       seed,
	}, nil
}

// readChainSeedFromFile returns the HMAC of the last complete entry in the
// JSONL file at path, or (nil, false) if the file has no HMAC-bearing
// entries (fresh file or pre-chain legacy log). Used by openCurrentFile
// to resume the chain across process restarts.
//
// Implementation note: unlike readLastLine, this does NOT need to handle
// the "single giant unterminated record" case — if the last byte isn't a
// newline, we treat the trailing fragment as a write that crashed mid-row
// and skip it (returning the previous complete entry's hmac).
func readChainSeedFromFile(path string) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	// Read the whole file into memory and scan from the end. Audit files
	// are bounded by the 50 MiB rotation threshold, so this is bounded.
	// On a fresh boot, the file is typically << 1 MiB.
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false
	}
	// Trim trailing newline so we don't mistake an empty trailing line
	// for the last entry.
	end := len(data)
	for end > 0 && data[end-1] == '\n' {
		end--
	}
	if end == 0 {
		return nil, false
	}
	// Walk backwards through the buffer line by line.
	for end > 0 {
		start := end - 1
		for start > 0 && data[start-1] != '\n' {
			start--
		}
		line := data[start:end]
		// Move end past this line's leading newline (if any) for the
		// next iteration.
		if start == 0 {
			end = 0
		} else {
			end = start - 1 // skip the '\n' we found
			for end > 0 && data[end-1] == '\n' {
				end--
			}
		}

		s := strings.TrimSpace(string(line))
		if s == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			// Malformed line — skip and try the previous one. This handles
			// the recoverCorruption truncate-on-malformed case where
			// recovery has not yet shrunk the file.
			continue
		}
		hexMac, ok := m["hmac"].(string)
		if !ok {
			// Pre-chain row. There may be HMAC-bearing rows before it
			// (someone hand-edited an old line back in?), but we treat
			// the first non-HMAC trailing row as "no resumable chain"
			// and chain off genesisSeed for safety.
			return nil, false
		}
		mac, err := hex.DecodeString(hexMac)
		if err != nil || len(mac) != 32 {
			return nil, false
		}
		return mac, true
	}
	return nil, false
}
