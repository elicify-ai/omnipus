// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Adversarial coverage for the chainCheckpoint threat model documented in
// checkpoint.go: a checkpoint sidecar with a tampered/forged `sum`, and a
// validly-signed but stale/mismatched `AppliesToFile`, must both be rejected
// — VerifyDir must fall back to genesis-seeding and log a warning, never
// silently trust either. Package-internal (not audit_test) so these tests
// can construct and mutate the unexported chainCheckpoint wire shape
// directly rather than hand-rolling JSON.
//
// TestRotationBySizeAndDaily's "chain verification survives retention
// deleting the genesis file" subtest (rotation_test.go) only exercises the
// happy path — a checkpoint written by cleanupExpired that VerifyDir then
// trusts. These tests cover the two adversarial branches in VerifyDir
// (verify.go) that subtest never reaches.

package audit

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTamperCheckpointFixture builds an audit directory with several small
// rotated files chained under a real HMAC key, then simulates a process
// restart with a short retention window so cleanupExpired deletes the
// oldest half — including the true genesis-seeded file — and persists a
// legitimate signed chainCheckpoint pointing at the new-oldest survivor.
//
// Mirrors TestRotationBySizeAndDaily/"chain verification survives retention
// deleting the genesis file" in rotation_test.go, but lives in this
// (internal) package so the adversarial tests below can read/mutate the
// chainCheckpoint struct and call computeCheckpointSum directly instead of
// re-deriving the wire format from scratch.
func setupTamperCheckpointFixture(t *testing.T) (dir string, key []byte) {
	t.Helper()
	dir = t.TempDir()

	var err error
	key, err = DeriveAuditKey([]byte("checkpoint-adversarial-fixture-master-key!!"))
	require.NoError(t, err)

	logger1, err := NewLogger(LoggerConfig{
		Dir:           dir,
		MaxSizeBytes:  200,
		RetentionDays: 90,
		HMACKey:       key,
	})
	require.NoError(t, err)

	const totalEntries = 10
	for i := 0; i < totalEntries; i++ {
		require.NoError(t, logger1.Log(&Entry{
			Timestamp:  time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
			Event:      EventToolCall,
			Decision:   DecisionAllow,
			SessionID:  "sess-adversarial-checkpoint",
			Tool:       "echo",
			Parameters: map[string]any{"seq": i, "filler": strings.Repeat("x", 100)},
		}))
		// Distinct millisecond buckets avoid rotate()'s dst-name collision
		// (see TestVerify_RotationPreservesChain in hmac_test.go).
		time.Sleep(2 * time.Millisecond)
	}
	require.NoError(t, logger1.Close())

	rotatedFiles, err := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
	require.NoError(t, err)
	sortAuditFilesChronologically(rotatedFiles)
	require.GreaterOrEqualf(t, len(rotatedFiles), 4,
		"fixture must produce enough rotated files to backdate a genesis subset; got %d: %v",
		len(rotatedFiles), rotatedFiles)

	// Backdate the older half — including index 0, the true genesis-seeded
	// file — so retention cleanup deletes them and the new-oldest survivor's
	// first entry no longer chains from GenesisSeed().
	numToExpire := len(rotatedFiles) / 2
	if numToExpire < 1 {
		numToExpire = 1
	}
	farPast := time.Now().UTC().AddDate(0, 0, -100)
	for i := 0; i < numToExpire; i++ {
		require.NoError(t, os.Chtimes(rotatedFiles[i], farPast, farPast),
			"backdating mtime on %s must succeed", rotatedFiles[i])
	}

	// Simulate a process restart: a fresh Logger runs cleanupExpired() with
	// a retention window that makes the backdated files eligible.
	logger2, err := NewLogger(LoggerConfig{
		Dir:           dir,
		MaxSizeBytes:  50 * 1024 * 1024,
		RetentionDays: 1,
		HMACKey:       key,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger2.Close() })

	checkpointFile := filepath.Join(dir, checkpointFileName)
	_, statErr := os.Stat(checkpointFile)
	require.NoErrorf(t, statErr,
		"fixture setup must persist a chain checkpoint at %s", checkpointFile)

	// Sanity: the checkpoint as originally written must be trustworthy on
	// its own, so the tamper tests below are isolating the tampered field
	// and not just observing a fixture that was already broken.
	res, err := VerifyDir(context.Background(), dir, key)
	require.NoError(t, err)
	require.Truef(t, res.Valid,
		"fixture's untampered checkpoint must verify clean before tampering; got: %s", res.String())

	return dir, key
}

// captureWarnLogs redirects slog's default logger to buf for the duration
// of the test (restored via t.Cleanup) so assertions can check that a
// specific warning fired.
func captureWarnLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestVerifyDir_TamperedCheckpointSum_FallsBackToGenesis proves the checkpoint
// threat model documented in checkpoint.go: an attacker who can write to the
// audit directory but does NOT have the chain key cannot forge a checkpoint
// that VerifyDir will trust. Here we tamper ONLY the `sum` field (leaving the
// still-correct `final_hmac` and `applies_to_file` untouched) — the scenario
// where trusting the checkpoint's FinalHMAC anyway (ignoring the bad sum)
// would silently produce a Valid=true result, which is exactly the bypass
// this test must catch.
func TestVerifyDir_TamperedCheckpointSum_FallsBackToGenesis(t *testing.T) {
	dir, key := setupTamperCheckpointFixture(t)

	checkpointFile := filepath.Join(dir, checkpointFileName)
	raw, err := os.ReadFile(checkpointFile)
	require.NoError(t, err)
	var cp chainCheckpoint
	require.NoError(t, json.Unmarshal(raw, &cp))

	// Flip the last hex character of sum. Any single hex-digit change
	// alters the decoded byte value, so this is guaranteed to invalidate
	// the integrity check regardless of what character was originally there.
	require.NotEmpty(t, cp.Sum)
	tampered := []byte(cp.Sum)
	last := len(tampered) - 1
	if tampered[last] == '0' {
		tampered[last] = '1'
	} else {
		tampered[last] = '0'
	}
	cp.Sum = string(tampered)

	data, err := json.MarshalIndent(&cp, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(checkpointFile, data, 0o600))

	buf := captureWarnLogs(t)

	res, err := VerifyDir(context.Background(), dir, key)
	require.NoError(t, err)

	assert.Contains(t, buf.String(), "chain checkpoint failed integrity check",
		"a tampered sum must be rejected and logged, not silently trusted")
	assert.Falsef(t, res.Valid,
		"rejecting the forged sum must fall back to genesis-seeding, which correctly reports a break "+
			"at the new-oldest survivor (a real bypass would instead have used the checkpoint's still-"+
			"correct final_hmac despite the bad sum and reported Valid=true); got: %s", res.String())
	assert.Equal(t, 1, res.BrokenAt,
		"genesis-seeded fallback must break at the new-oldest survivor's FIRST entry")
	assert.Contains(t, res.Reason, "hmac mismatch")
}

// TestCleanupExpired_RePointsStaleCheckpoint_WhenDeletedFileHasNoHMAC is the
// direct regression test for the cleanupExpired staleness fix: deleting a
// pre-chain/HMAC-less file that happens to be exactly the file an existing
// checkpoint's AppliesToFile currently names must re-point the checkpoint at
// the new oldest survivor (preserving FinalHMAC unchanged), rather than
// leaving a now-stale checkpoint referencing a file that no longer exists.
func TestCleanupExpired_RePointsStaleCheckpoint_WhenDeletedFileHasNoHMAC(t *testing.T) {
	dir := t.TempDir()
	key, err := DeriveAuditKey([]byte("checkpoint-staleness-fixture-master-key!!!!"))
	require.NoError(t, err)

	// Two pre-chain (no `hmac` field) rotated files simulate a directory
	// state where neither carries chain information — deleting either
	// contributes nothing to lastDeletedFinalHMAC, the exact condition
	// Finding 2 addresses.
	older := filepath.Join(dir, "audit-2020-01-05.jsonl")
	newer := filepath.Join(dir, "audit-2020-01-06.jsonl")
	require.NoError(t, os.WriteFile(older, []byte(`{"event":"startup"}`+"\n"), 0o600))
	require.NoError(t, os.WriteFile(newer, []byte(`{"event":"startup"}`+"\n"), 0o600))

	// older is eligible for retention deletion (mtime far in the past);
	// newer is fresh (mtime now) and must survive.
	farPast := time.Now().UTC().AddDate(0, 0, -100)
	require.NoError(t, os.Chtimes(older, farPast, farPast))

	// Simulate a checkpoint left over from an earlier cleanup pass, currently
	// naming `older` as the oldest survivor. The exact FinalHMAC bytes don't
	// matter for this test (we only assert they are preserved unchanged) —
	// 32 arbitrary non-zero bytes round-trip fine.
	fakeFinalHMAC := bytes.Repeat([]byte{0x42}, 32)
	require.NoError(t, writeChainCheckpoint(dir, filepath.Base(older), fakeFinalHMAC, key))

	preCP, err := readChainCheckpoint(dir, key)
	require.NoError(t, err)
	require.NotNil(t, preCP)
	require.Equal(t, filepath.Base(older), preCP.AppliesToFile)

	// Constructing a Logger against this directory runs cleanupExpired() once
	// at startup — this must delete `older` (past retention) and, because
	// `older` carries no chain HMAC, exercise the staleness re-point branch:
	// the checkpoint's AppliesToFile must move from `older` to `newer`.
	logger, err := NewLogger(LoggerConfig{
		Dir:           dir,
		MaxSizeBytes:  50 * 1024 * 1024,
		RetentionDays: 1,
		HMACKey:       key,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	_, statErr := os.Stat(older)
	assert.Truef(t, os.IsNotExist(statErr), "older pre-chain file must have been deleted by retention cleanup")
	_, statErr = os.Stat(newer)
	assert.NoError(t, statErr, "newer pre-chain file must survive (not yet past retention)")

	postCP, err := readChainCheckpoint(dir, key)
	require.NoError(t, err)
	require.NotNilf(t, postCP, "checkpoint must still exist after cleanup re-points it")
	assert.Equal(
		t,
		filepath.Base(newer),
		postCP.AppliesToFile,
		"a checkpoint naming a just-deleted, HMAC-less file must be re-pointed at the new oldest survivor, not left stale",
	)
	assert.Equal(t, preCP.FinalHMAC, postCP.FinalHMAC,
		"FinalHMAC must be preserved unchanged — an HMAC-less deleted file doesn't move the chain seed")
}

// TestVerifyDir_StaleCheckpointAppliesToFile_FallsBackToGenesis proves the
// second adversarial branch: a checkpoint that is validly signed with the
// real chain key (so its integrity check passes) but whose AppliesToFile
// names a file that is NOT the actual oldest file currently on disk — e.g.
// files deleted manually outside cleanupExpired, or a checkpoint left over
// from a different retention run. VerifyDir must detect the mismatch and
// fall back to genesis rather than seeding from a boundary that no longer
// matches reality.
func TestVerifyDir_StaleCheckpointAppliesToFile_FallsBackToGenesis(t *testing.T) {
	dir, key := setupTamperCheckpointFixture(t)

	checkpointFile := filepath.Join(dir, checkpointFileName)
	raw, err := os.ReadFile(checkpointFile)
	require.NoError(t, err)
	var cp chainCheckpoint
	require.NoError(t, json.Unmarshal(raw, &cp))

	finalMAC, err := hex.DecodeString(cp.FinalHMAC)
	require.NoError(t, err)
	require.Len(t, finalMAC, 32)

	// Re-point AppliesToFile at a file that does not exist, re-signing with
	// the real key so the integrity check (Sum) passes cleanly — isolating
	// the AppliesToFile-mismatch branch from the sum-tamper branch tested
	// above.
	fakeAppliesToFile := "audit-1999-01-01.jsonl"
	require.NotEqual(t, cp.AppliesToFile, fakeAppliesToFile)
	sum := computeCheckpointSum(fakeAppliesToFile, finalMAC, key)
	cp.AppliesToFile = fakeAppliesToFile
	cp.Sum = hex.EncodeToString(sum)

	data, err := json.MarshalIndent(&cp, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(checkpointFile, data, 0o600))

	buf := captureWarnLogs(t)

	res, err := VerifyDir(context.Background(), dir, key)
	require.NoError(t, err)

	assert.Contains(t, buf.String(), "chain checkpoint does not match current oldest file",
		"a checkpoint whose AppliesToFile doesn't match the actual oldest survivor must be rejected "+
			"and logged, not silently trusted")
	assert.Falsef(t, res.Valid,
		"a stale/mismatched checkpoint must not be used as a seed — verification must fall back to "+
			"genesis and correctly report the break at the true new-oldest survivor; got: %s", res.String())
	assert.Equal(t, 1, res.BrokenAt,
		"genesis-seeded fallback must break at the new-oldest survivor's FIRST entry")
	assert.Contains(t, res.Reason, "hmac mismatch")
}
