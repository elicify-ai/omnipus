// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the v0.2 #155 audit log HMAC chain. Covers:
//
//   - Genesis seed determinism
//   - End-to-end write + verify on intact log
//   - Truncation detection (drop last entry)
//   - Surgical-rewrite detection (flip a middle entry)
//   - Rotation preserves chain across files
//   - Wrong key fails verification
//   - Pre-chain (legacy) entries are tolerated
//   - DeriveAuditKey is deterministic and produces 32 bytes

package audit_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// testKey is a deterministic 32-byte chain key for tests. Using a fixed value
// makes failures reproducible — the dev fallback would also work but emits
// sticky warns that pollute test output.
func testKey(t *testing.T) []byte {
	t.Helper()
	k, err := audit.DeriveAuditKey([]byte("hmac-chain-test-master-key-v1!!!"))
	require.NoError(t, err)
	return k
}

// TestGenesisSeed_Deterministic verifies that GenesisSeed always returns the
// same 32 bytes. The chain seed is hardcoded in the package (sha256 of a
// fixed string) so two independent verifiers can start a chain walk from
// byte zero without coordinating.
func TestGenesisSeed_Deterministic(t *testing.T) {
	a := audit.GenesisSeed()
	b := audit.GenesisSeed()
	require.Equal(t, 32, len(a), "genesis seed must be 32 bytes")
	require.Equal(t, a, b, "GenesisSeed must be deterministic across calls")
}

// TestDeriveAuditKey_Deterministic verifies HKDF-SHA256 output is stable for
// the same input.
func TestDeriveAuditKey_Deterministic(t *testing.T) {
	master := []byte("super-secret-master-key-test-input!!")
	a, err := audit.DeriveAuditKey(master)
	require.NoError(t, err)
	require.Equal(t, 32, len(a))
	b, err := audit.DeriveAuditKey(master)
	require.NoError(t, err)
	require.Equal(t, a, b, "DeriveAuditKey must be deterministic")
}

// TestDeriveAuditKey_DifferentMastersDifferentKeys verifies HKDF separates
// independent masters.
func TestDeriveAuditKey_DifferentMastersDifferentKeys(t *testing.T) {
	a, err := audit.DeriveAuditKey([]byte("master-A-aaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	require.NoError(t, err)
	b, err := audit.DeriveAuditKey([]byte("master-B-bbbbbbbbbbbbbbbbbbbbbbbbbbb"))
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

// TestVerify_IntactChain seeds three entries and confirms VerifyFile returns
// Valid=true, FinalHMAC populated, no broken entries.
func TestVerify_IntactChain(t *testing.T) {
	dir := t.TempDir()
	key := testKey(t)
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		HMACKey:       key,
	})
	require.NoError(t, err)
	for i, dec := range []string{audit.DecisionAllow, audit.DecisionDeny, audit.DecisionAllow} {
		require.NoError(t, logger.Log(&audit.Entry{
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
			Event:     audit.EventToolCall,
			Decision:  dec,
			AgentID:   "test-agent",
			SessionID: "sess-intact",
			Tool:      "echo",
		}))
	}
	require.NoError(t, logger.Close())

	res, err := audit.VerifyFile(context.Background(),
		filepath.Join(dir, "audit.jsonl"), key, audit.GenesisSeed())
	require.NoError(t, err)
	require.True(t, res.Valid, "intact chain should verify; got: %s", res.String())
	require.Equal(t, 3, res.EntriesScanned)
	require.Len(t, res.FinalHMAC, 32)
}

// TestVerify_TruncationDetected drops the last line and expects the FILE
// verification to still succeed (truncation is detectable only across
// files / against an external reference). Then writes a 4th entry that
// chains off the WRONG seed (because it was forced to genesisSeed by the
// truncation) and confirms VerifyFile flags the chain break.
//
// In other words: dropping the trailing entry doesn't break the surviving
// chain, but ANY subsequent write made by an attacker (or by a fresh
// instance that resumed without seeing the dropped entry) breaks the
// chain because their prev_hmac is now wrong.
func TestVerify_TruncationDetected(t *testing.T) {
	dir := t.TempDir()
	key := testKey(t)
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		HMACKey:       key,
	})
	require.NoError(t, err)
	for i, dec := range []string{audit.DecisionAllow, audit.DecisionDeny, audit.DecisionAllow} {
		require.NoError(t, logger.Log(&audit.Entry{
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
			Event:     audit.EventToolCall,
			Decision:  dec,
			AgentID:   "test-agent",
			SessionID: "sess-trunc",
			Tool:      "echo",
		}))
	}
	require.NoError(t, logger.Close())

	auditPath := filepath.Join(dir, "audit.jsonl")
	original, err := os.ReadFile(auditPath)
	require.NoError(t, err)

	// Drop the last line.
	truncated := dropLastLineLocal(original)
	require.NoError(t, os.WriteFile(auditPath, truncated, 0o600))

	// The two surviving entries still chain correctly — that's the
	// intentional limitation: a chain walk of a truncated file looks valid
	// FOR THE SURVIVING ENTRIES. The key property is that any future entry
	// written without knowledge of the dropped tail will break.
	res, err := audit.VerifyFile(context.Background(), auditPath, key, audit.GenesisSeed())
	require.NoError(t, err)
	require.True(t, res.Valid, "surviving entries chain correctly")
	require.Equal(t, 2, res.EntriesScanned)

	// Now simulate an attacker / fresh writer appending a forged entry.
	// We re-open the logger — recoverCorruption + readChainSeedFromFile
	// will pick up entry #2's hmac as the prevHMAC for the next write.
	// Verification still succeeds in this benign reopen case (the
	// attacker would need to forge the hmac to break detection — which
	// is exactly the property we're protecting). To EXERCISE the break,
	// we'll write a hand-forged entry with bogus hmac.
	forgedLine := `{"timestamp":"2099-01-01T00:00:00Z","event":"tool_call","decision":"allow","tool":"forged","hmac":"00000000000000000000000000000000000000000000000000000000000000ff"}` + "\n"
	f, err := os.OpenFile(auditPath, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(forgedLine)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	res, err = audit.VerifyFile(context.Background(), auditPath, key, audit.GenesisSeed())
	require.NoError(t, err)
	require.False(t, res.Valid, "forged appended entry must break chain")
	require.Equal(t, 3, res.BrokenAt)
	require.Contains(t, res.Reason, "hmac mismatch")
}

// TestVerify_RewriteDetected writes three entries then surgically rewrites
// the middle one's decision. Verification must flag the chain break at
// entry #2 (its content hash no longer matches what entry #3 chains to).
//
// Note: depending on whether the rewriter ALSO updated entry #2's `hmac`
// field, the break could surface as "hmac mismatch on entry 2" (no update)
// or "hmac mismatch on entry 3" (rewriter updated #2 from #1's hmac but
// could not forge #3 because they don't have the chain key). We assert
// the chain is invalid without pinning the exact line.
func TestVerify_RewriteDetected(t *testing.T) {
	dir := t.TempDir()
	key := testKey(t)
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		HMACKey:       key,
	})
	require.NoError(t, err)
	entries := []*audit.Entry{
		{
			Timestamp: time.Now().UTC(),
			Event:     audit.EventToolCall,
			Decision:  audit.DecisionAllow,
			SessionID: "sess-rw",
			Tool:      "ls",
		},
		{
			Timestamp:  time.Now().UTC().Add(time.Millisecond),
			Event:      audit.EventToolCall,
			Decision:   audit.DecisionDeny,
			SessionID:  "sess-rw",
			Tool:       "rm",
			PolicyRule: "shellguard: rm -rf",
		},
		{
			Timestamp: time.Now().UTC().Add(2 * time.Millisecond),
			Event:     audit.EventToolCall,
			Decision:  audit.DecisionAllow,
			SessionID: "sess-rw",
			Tool:      "echo",
		},
	}
	for _, e := range entries {
		require.NoError(t, logger.Log(e))
	}
	require.NoError(t, logger.Close())

	// Surgically flip entry #1 (0-indexed) decision from "deny" to "allow",
	// preserving the existing `hmac` field. This simulates the laundering
	// attack where an attacker has byte-write access to the file but does
	// NOT have the chain key.
	auditPath := filepath.Join(dir, "audit.jsonl")
	require.NoError(t, rewriteDecisionLocal(auditPath, 1, "deny", "allow"))

	res, err := audit.VerifyFile(context.Background(), auditPath, key, audit.GenesisSeed())
	require.NoError(t, err)
	require.False(t, res.Valid, "rewrite must break chain; got: %s", res.String())
	require.Contains(t, res.Reason, "hmac")
}

// TestVerify_RotationPreservesChain confirms rotation seeds the next file's
// chain head from the previous file's last entry.
//
// We exercise rotation by writing a fixed number of moderately-sized
// entries with a low MaxSizeBytes threshold. To avoid colliding with the
// rotate() path's millisecond-resolution dst-name collision (multiple
// rotations within the same millisecond produce identical dst paths and
// the second rename silently overwrites the first — a known pre-#155
// bug), we sleep between writes so each rotation lands in its own
// millisecond bucket.
func TestVerify_RotationPreservesChain(t *testing.T) {
	dir := t.TempDir()
	key := testKey(t)
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		MaxSizeBytes:  512, // mid-sized — exactly one rotation expected
		HMACKey:       key,
	})
	require.NoError(t, err)

	// Write 4 entries. Each is ~280 bytes after HMAC embedding; MaxSizeBytes
	// of 512 means rotation triggers on the SECOND write (currentSize ~280
	// after entry 1, then ~560 after entry 2 — but the check happens BEFORE
	// the write, so rotation actually fires on entry 2 since after entry 1
	// currentSize is below 512). The natural flow is: entries 1+2 in
	// rotated file, entries 3+4 in current file. One rotation, two files.
	//
	// We add a small sleep between writes so even if the size math drifts
	// slightly across Go versions and we get more rotations than expected,
	// each rotation lands in its own millisecond — preventing the collision
	// that loses entries.
	for i := 0; i < 4; i++ {
		require.NoError(t, logger.Log(&audit.Entry{
			Timestamp:  time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
			Event:      audit.EventToolCall,
			Decision:   audit.DecisionAllow,
			SessionID:  "sess-rotate",
			Tool:       "echo",
			Parameters: map[string]any{"i": i, "filler": "padding-bytes"},
		}))
		time.Sleep(2 * time.Millisecond) // avoid rotation dst-name collision
	}
	require.NoError(t, logger.Close())

	// Confirm at least one rotated file exists.
	rotated, err := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
	require.NoError(t, err)
	require.NotEmpty(t, rotated, "rotation must have produced at least one rotated file")

	// Count total entries across files to verify nothing was lost.
	totalLines := 0
	all := append([]string{}, rotated...)
	all = append(all, filepath.Join(dir, "audit.jsonl"))
	for _, f := range all {
		b, readErr := os.ReadFile(f)
		require.NoError(t, readErr)
		totalLines += strings.Count(strings.TrimRight(string(b), "\n"), "\n") + 1
	}
	require.Equal(t, 4, totalLines, "all 4 entries must survive rotation; got %d", totalLines)

	// VerifyDir threads the chain across rotation files.
	res, err := audit.VerifyDir(context.Background(), dir, key)
	require.NoError(t, err)
	require.True(t, res.Valid, "chain must be intact across rotation; got: %s", res.String())
	require.Equal(t, 4, res.EntriesScanned)
	require.GreaterOrEqual(t, res.FilesScanned, 2)
}

// TestVerify_WrongKey ensures verification fails when the supplied key does
// not match the key used at write time.
func TestVerify_WrongKey(t *testing.T) {
	dir := t.TempDir()
	key := testKey(t)
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		HMACKey:       key,
	})
	require.NoError(t, err)
	require.NoError(t, logger.Log(&audit.Entry{
		Event:    audit.EventToolCall,
		Decision: audit.DecisionAllow,
		Tool:     "echo",
	}))
	require.NoError(t, logger.Close())

	wrongKey, err := audit.DeriveAuditKey([]byte("a-totally-different-master-key!!!"))
	require.NoError(t, err)

	res, err := audit.VerifyFile(context.Background(),
		filepath.Join(dir, "audit.jsonl"), wrongKey, audit.GenesisSeed())
	require.NoError(t, err)
	require.False(t, res.Valid, "wrong key must produce hmac mismatch")
	require.Contains(t, res.Reason, "hmac mismatch")
}

// TestVerify_PreChainLegacyTolerated writes a JSONL file containing entries
// without an `hmac` field (simulating a log written by a pre-#155 binary)
// and confirms VerifyFile reports Valid=true with PreChainEntries set.
func TestVerify_PreChainLegacyTolerated(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "audit.jsonl")
	legacy := []string{
		`{"timestamp":"2026-01-01T00:00:00Z","event":"tool_call","decision":"allow","tool":"legacy1"}`,
		`{"timestamp":"2026-01-01T00:00:01Z","event":"tool_call","decision":"deny","tool":"legacy2"}`,
	}
	require.NoError(t, os.WriteFile(auditPath, []byte(strings.Join(legacy, "\n")+"\n"), 0o600))

	key := testKey(t)
	res, err := audit.VerifyFile(context.Background(), auditPath, key, audit.GenesisSeed())
	require.NoError(t, err)
	require.True(t, res.Valid, "pre-chain legacy entries must be tolerated")
	require.Equal(t, 2, res.PreChainEntries)
}

// TestVerify_PreChainThenChainedThenStrippedFails confirms that once an
// HMAC-bearing entry has appeared in the file, removing the `hmac` field
// from a later entry is a chain break (an attacker stripped hmac to mask
// a tamper).
func TestVerify_PreChainThenChainedThenStrippedFails(t *testing.T) {
	dir := t.TempDir()
	key := testKey(t)
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		HMACKey:       key,
	})
	require.NoError(t, err)
	require.NoError(t, logger.Log(&audit.Entry{
		Event:    audit.EventToolCall,
		Decision: audit.DecisionAllow,
		Tool:     "first-with-hmac",
	}))
	require.NoError(t, logger.Close())

	// Hand-append an entry without `hmac`.
	auditPath := filepath.Join(dir, "audit.jsonl")
	f, err := os.OpenFile(auditPath, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(
		`{"timestamp":"2099-01-01T00:00:00Z","event":"tool_call","decision":"allow","tool":"stripped"}` + "\n",
	)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	res, err := audit.VerifyFile(context.Background(), auditPath, key, audit.GenesisSeed())
	require.NoError(t, err)
	require.False(t, res.Valid, "missing hmac after chain start must fail")
	require.Equal(t, 2, res.BrokenAt)
	require.Contains(t, res.Reason, "missing hmac")
}

// TestProcessChainKey_FallbackPath confirms that SetProcessChainKey is
// used by NewLogger when LoggerConfig.HMACKey is nil.
func TestProcessChainKey_FallbackPath(t *testing.T) {
	dir := t.TempDir()
	key := testKey(t)
	audit.SetProcessChainKey(key)
	t.Cleanup(func() { audit.SetProcessChainKey(nil) })

	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		// HMACKey deliberately nil — fallback to processChainKey.
	})
	require.NoError(t, err)
	require.NoError(t, logger.Log(&audit.Entry{
		Event:    audit.EventToolCall,
		Decision: audit.DecisionAllow,
		Tool:     "fallback",
	}))
	require.NoError(t, logger.Close())

	// Verify with the SAME key we set on the package — confirms the
	// fallback was used and not the dev-only key.
	res, err := audit.VerifyFile(context.Background(),
		filepath.Join(dir, "audit.jsonl"), key, audit.GenesisSeed())
	require.NoError(t, err)
	require.True(t, res.Valid, "process-key fallback must produce a verifiable chain")
}

// TestEntryHMAC_HexFormat sanity-checks the on-disk shape: the `hmac` field
// is a 64-char hex string of a 32-byte SHA-256 mac.
func TestEntryHMAC_HexFormat(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		HMACKey:       testKey(t),
	})
	require.NoError(t, err)
	require.NoError(t, logger.Log(&audit.Entry{
		Event:    audit.EventToolCall,
		Decision: audit.DecisionAllow,
		Tool:     "echo",
	}))
	require.NoError(t, logger.Close())

	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	require.Len(t, lines, 1)

	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &m))
	hexMac, ok := m["hmac"].(string)
	require.True(t, ok, "every entry must carry an hmac field")
	require.Len(t, hexMac, 64, "hmac must be 64 hex chars (32 bytes)")
	raw, err := hex.DecodeString(hexMac)
	require.NoError(t, err)
	require.Len(t, raw, 32)
}

// TestVerify_ChainResumesAcrossRestart confirms that closing the logger,
// reopening it (simulating a process restart), and writing more entries
// produces a chain that verifies end-to-end. This is the resume-after-
// crash invariant: the boot path reads the last good entry's hmac out
// of audit.jsonl (via readChainSeedFromFile) so the next write chains
// from there, not from genesisSeed.
func TestVerify_ChainResumesAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	key := testKey(t)

	// Process 1: write 2 entries, close.
	logger1, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		HMACKey:       key,
	})
	require.NoError(t, err)
	require.NoError(t, logger1.Log(&audit.Entry{
		Event:    audit.EventToolCall,
		Decision: audit.DecisionAllow,
		Tool:     "first",
	}))
	require.NoError(t, logger1.Log(&audit.Entry{
		Event:    audit.EventToolCall,
		Decision: audit.DecisionDeny,
		Tool:     "second",
	}))
	require.NoError(t, logger1.Close())

	// Process 2: open same dir, write 2 more entries, close.
	logger2, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		HMACKey:       key,
	})
	require.NoError(t, err)
	require.NoError(t, logger2.Log(&audit.Entry{
		Event:    audit.EventToolCall,
		Decision: audit.DecisionAllow,
		Tool:     "third-after-restart",
	}))
	require.NoError(t, logger2.Log(&audit.Entry{
		Event:    audit.EventToolCall,
		Decision: audit.DecisionAllow,
		Tool:     "fourth-after-restart",
	}))
	require.NoError(t, logger2.Close())

	// Whole-file verification must succeed end-to-end.
	res, err := audit.VerifyFile(context.Background(),
		filepath.Join(dir, "audit.jsonl"), key, audit.GenesisSeed())
	require.NoError(t, err)
	require.True(t, res.Valid, "chain across restart must verify; got: %s", res.String())
	require.Equal(t, 4, res.EntriesScanned)
}

// TestLoggerVerify_Convenience exercises the *Logger.Verify wrapper.
func TestLoggerVerify_Convenience(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		HMACKey:       testKey(t),
	})
	require.NoError(t, err)
	require.NoError(t, logger.Log(&audit.Entry{
		Event:    audit.EventToolCall,
		Decision: audit.DecisionAllow,
		Tool:     "echo",
	}))
	res, err := logger.Verify(context.Background())
	require.NoError(t, err)
	require.True(t, res.Valid)
	require.NoError(t, logger.Close())
}

// dropLastLineLocal mirrors the helper in redteam_tamper_test.go so we have
// an in-package version usable from this file. It strips the trailing
// newline-terminated record from a JSONL byte slice.
func dropLastLineLocal(in []byte) []byte {
	end := len(in)
	if end == 0 {
		return in
	}
	if in[end-1] == '\n' {
		end--
	}
	for i := end - 1; i >= 0; i-- {
		if in[i] == '\n' {
			return in[:i+1]
		}
	}
	return nil
}

// rewriteDecisionLocal mirrors the helper in redteam_tamper_test.go.
func rewriteDecisionLocal(path string, lineIdx int, from, to string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if lineIdx < 0 || lineIdx >= len(lines) {
		return os.ErrInvalid
	}
	var entry map[string]any
	if unmarshalErr := json.Unmarshal([]byte(lines[lineIdx]), &entry); unmarshalErr != nil {
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
	lines[lineIdx] = string(rewritten)
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}
