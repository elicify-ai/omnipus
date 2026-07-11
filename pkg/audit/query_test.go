// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for Logger.LastWriterForPath (query.go) — the "who last wrote path
// P" read-back used by write_file's cross-agent overwrite-attribution note
// (pkg/tools/path_audit.go).

package audit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestLastWriterForPath_Basic confirms the straightforward case (no
// rotation involved): the most recent matching entry wins, entries for a
// different path are ignored, and entries missing Details["op"]=="write"
// (e.g. a hypothetical non-write file_op) are excluded from the match.
func TestLastWriterForPath_Basic(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 1})
	require.NoError(t, err)
	defer logger.Close()

	require.NoError(t, logger.Log(&audit.Entry{
		Event:    audit.EventFileOp,
		Decision: audit.DecisionAllow,
		AgentID:  "agent-jim",
		Tool:     "write_file",
		Details:  map[string]any{"path": "/shared/identity.txt", "op": "write"},
	}))
	// A non-"write" op on the same path must never satisfy the match.
	require.NoError(t, logger.Log(&audit.Entry{
		Event:    audit.EventFileOp,
		Decision: audit.DecisionAllow,
		AgentID:  "agent-imposter",
		Tool:     "write_file",
		Details:  map[string]any{"path": "/shared/identity.txt", "op": "not-a-write"},
	}))
	require.NoError(t, logger.Log(&audit.Entry{
		Event:    audit.EventFileOp,
		Decision: audit.DecisionAllow,
		AgentID:  "agent-worker",
		Tool:     "write_file",
		Details:  map[string]any{"path": "/shared/identity.txt", "op": "write"},
	}))

	agentID, found := logger.LastWriterForPath(audit.EventFileOp, "/shared/identity.txt")
	require.True(t, found)
	assert.Equal(t, "agent-worker", agentID, "most recent write op must win; the non-write op entry must be ignored")

	_, found = logger.LastWriterForPath(audit.EventFileOp, "/shared/does-not-exist.txt")
	assert.False(t, found)
}

// TestLastWriterForPath_DegradedLogger confirms a degraded logger produces
// found=false rather than panicking or blocking.
func TestLastWriterForPath_DegradedLogger(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 1})
	require.NoError(t, err)
	require.NoError(t, logger.Close()) // latches degraded=true

	_, found := logger.LastWriterForPath(audit.EventFileOp, "/shared/identity.txt")
	assert.False(t, found)
}

// TestLastWriterForPath_RotationBoundary confirms the fix for the
// current-file-only scope bound: a write recorded BEFORE a rotation must
// still be found via the single most-recently-rotated file, not just the
// live audit.jsonl.
//
// Rotation-forcing recipe mirrors hmac_test.go's
// TestVerify_RotationPreservesChain: a small MaxSizeBytes (512) plus a
// short sleep between writes (to land each rotation in its own millisecond
// bucket, avoiding rotate()'s dst-name collision on rapid same-millisecond
// rotations) reliably produces "entries 1+2 rotated out, entries 3+4 in the
// new current file" for ~280-byte entries.
func TestLastWriterForPath_RotationBoundary(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		MaxSizeBytes:  512, // mid-sized — exactly one rotation expected
		RetentionDays: 90,
	})
	require.NoError(t, err)
	defer logger.Close()

	// Entry 1: the write we want to find. Recorded BEFORE rotation.
	require.NoError(t, logger.Log(&audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventFileOp,
		Decision:  audit.DecisionAllow,
		AgentID:   "agent-jim",
		Tool:      "write_file",
		Details:   map[string]any{"path": "/shared/identity.txt", "op": "write", "filler": strings.Repeat("x", 40)},
	}))
	time.Sleep(2 * time.Millisecond) // avoid rotation dst-name collision

	// Entries 2-4: unrelated filler, sized/timed to push the logger through
	// exactly one rotation, per the recipe above.
	for i := 0; i < 3; i++ {
		require.NoError(t, logger.Log(&audit.Entry{
			Timestamp:  time.Now().UTC(),
			Event:      audit.EventToolCall,
			Decision:   audit.DecisionAllow,
			AgentID:    "agent-filler",
			Tool:       "echo",
			Parameters: map[string]any{"i": i, "filler": "padding-bytes-padding-bytes"},
		}))
		time.Sleep(2 * time.Millisecond)
	}
	require.NoError(t, logger.Close())

	// Sanity: rotation actually happened (else this test would trivially
	// pass without exercising the rotation-boundary code path at all).
	rotated, err := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
	require.NoError(t, err)
	require.NotEmpty(t, rotated, "test setup must force at least one rotation")

	// Sanity: agent-jim's write must NOT be in the current (post-rotation)
	// file — this is what makes the test a genuine proof that the ROTATED
	// file is being consulted, not a no-op that happens to pass either way.
	currentContent, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)
	require.NotContains(t, string(currentContent), "agent-jim",
		"test setup must rotate agent-jim's entry OUT of the current file")

	// Re-open a fresh (non-degraded) logger against the same dir the way a
	// running gateway would, and confirm the write survives the rotation
	// boundary.
	logger2, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	defer logger2.Close()

	agentID, found := logger2.LastWriterForPath(audit.EventFileOp, "/shared/identity.txt")
	require.True(t, found, "LastWriterForPath must find a write recorded in the most-recently-rotated file, not just the current one")
	assert.Equal(t, "agent-jim", agentID)
}
