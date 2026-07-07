// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for Logger.Verify hardcoding GenesisSeed() (verify.go):
// rotate() (audit.go) chains a freshly-rotated file's first entry from the
// PREVIOUS file's last HMAC, not genesis — but Verify() ignored that and
// always seeded from GenesisSeed(), producing a false "chain BROKEN" report
// for the current file on any gateway that had rotated at least once. This
// is reachable via a live REST endpoint (pkg/gateway/rest_settings.go calls
// logger.Verify(ctx) directly), so the false positive is operator-visible.

package audit_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestLoggerVerify_AfterRotation_SeedsFromPredecessorNotGenesis reproduces
// the exact bug scenario: one rotation happens, the current (post-rotation)
// file has one real chained entry whose stored `hmac` was computed against
// the ROTATED-OUT file's last HMAC (via rotate()'s in-memory prevHMAC
// carry), and Verify() is called on the still-open Logger — mirroring the
// live rest_settings.go call site, not a fresh-process restart.
//
// MaxSizeBytes is set to 1 so that ANY single completed write pushes
// currentSize past the threshold, guaranteeing rotation fires on the very
// next write regardless of exact per-entry byte size — no fragile "~280
// bytes per entry" size guessing required.
func TestLoggerVerify_AfterRotation_SeedsFromPredecessorNotGenesis(t *testing.T) {
	dir := t.TempDir()
	key := testKey(t)

	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		MaxSizeBytes:  1, // forces rotation on the write immediately after any prior write
		RetentionDays: 90,
		HMACKey:       key,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	// Entry 1: written to the original file, no rotation yet (currentSize
	// starts at 0, which is not >= the 1-byte threshold).
	require.NoError(t, logger.Log(&audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  audit.DecisionAllow,
		SessionID: "sess-verify-seed",
		Tool:      "first",
	}))

	// Entry 2: currentSize (from entry 1) is now >= 1, so this write
	// rotates FIRST (entry 1's file becomes the rotated-out predecessor),
	// then entry 2 is written into a fresh audit.jsonl, chaining its hmac
	// from entry 1's last HMAC (rotate()'s l.prevHMAC carryover) — NOT
	// from genesis.
	require.NoError(t, logger.Log(&audit.Entry{
		Timestamp: time.Now().UTC().Add(time.Millisecond),
		Event:     audit.EventToolCall,
		Decision:  audit.DecisionAllow,
		SessionID: "sess-verify-seed",
		Tool:      "second-after-rotation",
	}))

	// Confirm the setup actually rotated and the current file has exactly
	// the one post-rotation entry (not empty — an empty file would trivially
	// "verify" regardless of seed correctness, defeating the test).
	rotated, globErr := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
	require.NoError(t, globErr)
	require.NotEmptyf(t, rotated, "test setup must produce at least one rotated file; got none")

	// This is the exact call the bug affects: Verify() on the live Logger,
	// checking only the current (post-rotation) file — not VerifyDir.
	res, err := logger.Verify(context.Background())
	require.NoError(t, err)
	require.Truef(t, res.Valid,
		"Verify() must seed the current file's first entry from the rotated-out predecessor's "+
			"last HMAC, not GenesisSeed() — a hardcoded genesis seed here produces a false "+
			"'chain BROKEN' on any installation that has rotated at least once; got: %s", res.String())
	require.Equal(t, 1, res.EntriesScanned,
		"Verify() must scan exactly the current file's one post-rotation entry")
}
