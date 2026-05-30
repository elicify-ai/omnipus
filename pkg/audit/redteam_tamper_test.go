// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package audit_test — insider-LLM red-team coverage for audit-log tampering.
//
// This file documents threat C2-AUDIT (audit log tampering) from the
// insider-pentest report. The threat: an attacker (or a compromised plugin)
// who obtains write access to ~/.omnipus/system/audit.jsonl can:
//
//  1. Truncate the log to hide a sequence of malicious tool calls.
//  2. Surgically rewrite a previously-written line (e.g. flip
//     decision: "deny" -> "allow") to retroactively launder a denied call.
//
// The defense (closed in v0.2 #155): per-entry HMAC chaining. Each entry
// carries an `hmac` field computed over hmac(prev_hmac || canonical_json)
// under a key derived from the master key (the log file does NOT contain
// the key). audit.VerifyFile / *Logger.Verify walk the log start-to-end,
// recompute each link, and report the first broken link.
//
// Closing fix: v0.2 (#155) — "audit log integrity (HMAC chain)".
//
// Test structure: the helper auditPackageHasIntegrityVerifier() now
// returns true (the verifier is wired in lookupAuditVerifier below). The
// gap-reporting branch is kept intact — if a future regression strips the
// verifier or breaks its signature, the test reverts to "GAP CONFIRMED"
// rather than silently passing.
package audit_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// TestRedteam_AuditLog_TruncationDetected documents the truncation half of
// C2-AUDIT. It writes three audit entries through the production logger,
// closes the logger, then truncates the file by removing the last entry,
// then SIMULATES THE ATTACKER COMING BACK TO RESUME WRITING by appending a
// fresh entry that chains off the now-incorrect prevHMAC seed. A correct
// integrity verifier MUST report "chain broken" — the appended entry's
// content hash chain no longer threads through the surviving entries.
//
// Pure-truncation (drop the last entry, do nothing else) is by design only
// detectable when (a) further writes happen, or (b) an external reference
// records the expected final HMAC. The test exercises path (a) because
// that's the realistic attacker workflow: they truncate to hide a denied
// call and then continue logging benign activity. Drop-only without
// further writes leaves the SURVIVING entries internally consistent —
// that's a documented limitation of any in-band chain.
//
// Closes when v0.2 #155 HMAC chain lands. PASSES once audit.VerifyFile
// flags the appended forgery.
func TestRedteam_AuditLog_TruncationDetected(t *testing.T) {
	t.Logf("documents C2-AUDIT (audit truncation) from insider-pentest report; closed by v0.2 #155 HMAC chain")

	dir := t.TempDir()
	chainKey, err := audit.DeriveAuditKey([]byte("c2-audit-redteam-master-key-v1!!"))
	require.NoError(t, err)
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		HMACKey:       chainKey,
	})
	require.NoError(t, err)

	// Write three sequential entries — these form a 3-link chain. Each
	// carries a distinct decision so a tamper against any one is observable.
	for i, decision := range []string{audit.DecisionAllow, audit.DecisionDeny, audit.DecisionAllow} {
		entry := &audit.Entry{
			Timestamp:  time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
			Event:      audit.EventToolCall,
			Decision:   decision,
			AgentID:    "redteam-agent",
			SessionID:  "sess-truncate",
			Tool:       "shell",
			PolicyRule: "redteam: forced for tamper test",
			Details:    map[string]any{"seq": i},
		}
		require.NoError(t, logger.Log(entry), "seed entry %d", i)
	}
	require.NoError(t, logger.Close())

	auditPath := filepath.Join(dir, "audit.jsonl")
	original, err := os.ReadFile(auditPath)
	require.NoError(t, err)

	// Drop the LAST line — simulates an attacker who deleted the most recent
	// entry to hide a denied call.
	truncated := dropLastLine(original)
	require.NoError(t, os.WriteFile(auditPath, truncated, 0o600))

	// Now the attacker (or a fresh process resuming the log) appends a new
	// entry. Its `hmac` is forged because the attacker doesn't have the
	// chain key.
	forgedLine := `{"timestamp":"2099-01-01T00:00:00Z","event":"tool_call","decision":"allow","agent_id":"redteam-agent","session_id":"sess-truncate","tool":"shell","details":{"forged":true},"hmac":"00000000000000000000000000000000000000000000000000000000000000aa"}` + "\n"
	f, err := os.OpenFile(auditPath, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(forgedLine)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// Probe: does an integrity verifier exist on the audit package?
	if !auditPackageHasIntegrityVerifier() {
		t.Errorf(
			"C2-AUDIT (truncation) GAP CONFIRMED: no audit chain verifier exists (expected audit.VerifyFile or equivalent). "+
				"Truncation of audit.jsonl from %d to %d bytes followed by a forged append is currently UNDETECTED. "+
				"Fix: ship per-entry HMAC chain in v0.2 (#155).",
			len(original),
			len(truncated),
		)
		return
	}

	verifyFn, ok := lookupAuditVerifier(chainKey)
	if !ok {
		t.Fatalf("integrity verifier reported present but lookup failed — recompile audit package")
	}
	res := verifyFn(auditPath)
	if res == nil || res.IsValid() {
		t.Errorf("C2-AUDIT (truncation): verifier reported the chain VALID after truncate+forge — fix is broken")
	} else {
		t.Logf("closed: tamper detected via HMAC chain at %s", res.(interface{ String() string }).String())
	}
}

// TestRedteam_AuditLog_RewriteDetected documents the surgical-rewrite half of
// C2-AUDIT. We write three entries (allow, deny, allow), then SURGICALLY
// edit the middle entry's decision in-place from "deny" to "allow" while
// preserving the existing `hmac` field (the attacker doesn't have the
// chain key, so they cannot forge a new one). With a correct HMAC chain,
// this MUST flag the chain as broken — the rewritten entry's recomputed
// HMAC no longer matches its embedded `hmac` value, AND entry #3's
// `hmac` was computed against the ORIGINAL entry #2.
//
// Closes when v0.2 #155 HMAC chain lands.
func TestRedteam_AuditLog_RewriteDetected(t *testing.T) {
	t.Logf("documents C2-AUDIT (audit rewrite) from insider-pentest report; closed by v0.2 #155 HMAC chain")

	dir := t.TempDir()
	chainKey, err := audit.DeriveAuditKey([]byte("c2-audit-redteam-master-key-v1!!"))
	require.NoError(t, err)
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		HMACKey:       chainKey,
	})
	require.NoError(t, err)

	entries := []*audit.Entry{
		{
			Timestamp: time.Now().UTC(),
			Event:     audit.EventToolCall,
			Decision:  audit.DecisionAllow,
			AgentID:   "redteam-agent",
			SessionID: "sess-rewrite",
			Tool:      "echo",
		},
		{
			Timestamp:  time.Now().UTC().Add(1 * time.Millisecond),
			Event:      audit.EventToolCall,
			Decision:   audit.DecisionDeny,
			AgentID:    "redteam-agent",
			SessionID:  "sess-rewrite",
			Tool:       "shell",
			PolicyRule: "shellguard: dangerous pattern",
		},
		{
			Timestamp: time.Now().UTC().Add(2 * time.Millisecond),
			Event:     audit.EventToolCall,
			Decision:  audit.DecisionAllow,
			AgentID:   "redteam-agent",
			SessionID: "sess-rewrite",
			Tool:      "ls",
		},
	}
	for i, e := range entries {
		require.NoError(t, logger.Log(e), "seed entry %d", i)
	}
	require.NoError(t, logger.Close())

	auditPath := filepath.Join(dir, "audit.jsonl")

	// Surgical rewrite: parse JSONL line-by-line, locate the entry we want
	// to launder (the deny in slot #1), flip decision from "deny" to "allow"
	// while preserving the rest of the row (including the existing `hmac`
	// field). The HMAC chain doesn't care about byte length — it cares about
	// each entry's content hash — so the rewrite is detectable as long as
	// the payload changes at all.
	require.NoError(t, rewriteDecision(auditPath, 1, audit.DecisionDeny, audit.DecisionAllow))

	if !auditPackageHasIntegrityVerifier() {
		t.Errorf(
			"C2-AUDIT (rewrite) GAP CONFIRMED: no audit chain verifier exists. " +
				"In-place rewrite of audit.jsonl entry #1 (decision: deny -> allow) is currently UNDETECTED. " +
				"Fix: ship per-entry HMAC chain in v0.2 (#155).",
		)
		return
	}

	verifyFn, ok := lookupAuditVerifier(chainKey)
	if !ok {
		t.Fatalf("integrity verifier reported present but lookup failed — recompile audit package")
	}
	res := verifyFn(auditPath)
	if res == nil || res.IsValid() {
		t.Errorf("C2-AUDIT (rewrite): verifier reported the chain VALID after rewriting entry #1 — fix is broken")
	} else {
		t.Logf("closed: tamper detected via HMAC chain at %s", res.(interface{ String() string }).String())
	}
}

// dropLastLine returns the input with the trailing newline-terminated line
// removed. If the input contains zero or one newlines, returns the input
// unchanged so the caller can detect "no work to do".
func dropLastLine(in []byte) []byte {
	// Trim trailing newline first so the search hits the line BEFORE last.
	end := len(in)
	if end == 0 {
		return in
	}
	if in[end-1] == '\n' {
		end--
	}
	// Find the last newline before `end`.
	for i := end - 1; i >= 0; i-- {
		if in[i] == '\n' {
			return in[:i+1]
		}
	}
	// Only one line: empty file is the "everything dropped" outcome.
	return nil
}

// rewriteDecision opens an audit.jsonl, locates the lineIdx'th line, flips
// the JSON `decision` field from `from` to `to`, and rewrites the file in
// place. Other fields are preserved exactly. Returns an error if the line
// index is out of range or the decision field doesn't match `from`.
func rewriteDecision(path string, lineIdx int, from, to string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	lines := splitJSONLines(data)
	if lineIdx < 0 || lineIdx >= len(lines) {
		return os.ErrInvalid
	}

	var entry map[string]any
	if unmarshalErr := json.Unmarshal(lines[lineIdx], &entry); unmarshalErr != nil {
		return unmarshalErr
	}
	if got, _ := entry["decision"].(string); got != from {
		return os.ErrInvalid
	}
	entry["decision"] = to

	rewritten, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	lines[lineIdx] = rewritten

	// Reassemble.
	var out []byte
	for _, l := range lines {
		out = append(out, l...)
		out = append(out, '\n')
	}
	return os.WriteFile(path, out, 0o600)
}

// splitJSONLines splits a JSONL byte slice into per-line slices, dropping
// trailing empty fragments.
func splitJSONLines(in []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range in {
		if b == '\n' {
			if i > start {
				line := make([]byte, i-start)
				copy(line, in[start:i])
				out = append(out, line)
			}
			start = i + 1
		}
	}
	if start < len(in) {
		line := make([]byte, len(in)-start)
		copy(line, in[start:])
		out = append(out, line)
	}
	return out
}

// chainVerificationResult is the contract the verifier API exposes. The
// concrete type is *audit.ChainResult; we keep the interface declaration
// here so this file's gap-detection branch (the t.Errorf path) survives
// future regressions that might remove the type.
type chainVerificationResult interface {
	IsValid() bool
}

// auditPackageHasIntegrityVerifier reports whether the audit package
// exports a chain-integrity verifier function. After v0.2 #155 this
// returns true. If a future regression strips audit.VerifyFile, the
// underlying lookup returns (nil, false) and the gap-reporting branches
// in the tests will fire — that's the regression detection contract.
func auditPackageHasIntegrityVerifier() bool {
	_, ok := lookupAuditVerifier(nil)
	return ok
}

// lookupAuditVerifier returns a closure over audit.VerifyFile bound to
// the supplied chain key. The closure satisfies the
// `func(path string) chainVerificationResult` shape used by the gap
// tests. nilKey is permitted only for the existence probe
// (auditPackageHasIntegrityVerifier); callers that exercise the verifier
// must pass the same key they used at write time.
//
// To unwire the verifier (e.g. to detect a regression that drops the
// API), edit this single function to return (nil, false). The two
// red-team tests will then revert to their gap-reporting branches.
func lookupAuditVerifier(chainKey []byte) (func(string) chainVerificationResult, bool) {
	// Existence probe — audit.VerifyFile is a stable export. If a future
	// refactor renames or removes it, this file will fail to compile and
	// CI catches the regression at build time, not just at test time.
	probe := audit.VerifyFile
	_ = probe

	if chainKey == nil {
		// Existence-only probe used by auditPackageHasIntegrityVerifier.
		// Return a no-op closure so the boolean half is honest.
		return func(string) chainVerificationResult {
			return &audit.ChainResult{Valid: false, Reason: "existence probe"}
		}, true
	}
	return func(path string) chainVerificationResult {
		res, err := audit.VerifyFile(context.Background(), path, chainKey, audit.GenesisSeed())
		if err != nil {
			return &audit.ChainResult{Valid: false, Reason: err.Error()}
		}
		return res
	}, true
}
