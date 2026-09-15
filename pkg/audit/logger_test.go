// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package audit_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestAuditLogger_WriteAndRotate is an integration test covering JSONL append,
// 50MB rotation, retention cleanup, and crash-recovery validation of last line.
// Traces to: wave2-security-layer-spec.md line 807 (TestAuditLogger_WriteAndRotate)
// BDD: Scenario: Tool call produces audit entry + Audit log rotation at 50MB (spec line 577, 587)
func TestAuditLogger_WriteAndRotate(t *testing.T) {
	t.Run("tool call appends valid JSON line to audit.jsonl", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 577 (Scenario: Tool call produces audit entry)
		dir := t.TempDir()
		logger, err := audit.NewLogger(audit.LoggerConfig{
			Dir:           dir,
			RetentionDays: 90,
		})
		require.NoError(t, err)
		// Test cleanup: Close error is inconsequential here and throughout
		// this file — t.TempDir() removes the backing directory regardless,
		// and no test in this file asserts on a cleanup Close.
		defer func() {
			if closeErr := logger.Close(); closeErr != nil {
				_ = closeErr
			}
		}()

		entry := audit.Entry{
			Timestamp:  time.Now().UTC(),
			Event:      audit.EventToolCall,
			Decision:   "allow",
			AgentID:    "general-assistant",
			SessionID:  "sess-abc123",
			Tool:       "web_search",
			Parameters: map[string]any{"query": "AWS pricing"},
			PolicyRule: "tools.allow matched 'web_search' for agent 'general-assistant'",
		}
		require.NoError(t, logger.Log(&entry))

		// Read back and validate
		logPath := filepath.Join(dir, "audit.jsonl")
		data, err := os.ReadFile(logPath)
		require.NoError(t, err)
		require.NotEmpty(t, data)

		var parsed map[string]any
		err = json.Unmarshal(data, &parsed)
		require.NoError(t, err, "audit entry must be valid JSON: %s", string(data))

		assert.Equal(t, "tool_call", parsed["event"])
		assert.Equal(t, "allow", parsed["decision"])
		assert.Equal(t, "general-assistant", parsed["agent_id"])
		assert.Equal(t, "web_search", parsed["tool"])
		assert.NotEmpty(t, parsed["timestamp"], "timestamp field must be present")
		assert.NotEmpty(t, parsed["policy_rule"], "policy_rule field must be present")
		assert.NotEmpty(t, parsed["session_id"], "session_id field must be present")
	})

	// The rotation threshold is INJECTED via LoggerConfig.MaxSizeBytes rather
	// than simulated by writing 50 MB of real bytes into the temp dir. It is
	// the same field production reads, and writeLine's size check
	// (`l.currentSize >= l.maxSize`) is the same branch either way, so the
	// coverage is identical — but it runs in milliseconds and needs no free
	// disk. The last two subtests pin the shipped 50 MiB default separately,
	// also at no disk cost.
	t.Run("rotation triggered when the file reaches the configured threshold", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 587 (Scenario: Audit log rotation at 50MB)
		dir := t.TempDir()
		logPath := filepath.Join(dir, "audit.jsonl")
		const threshold = 8 * 1024

		prefillAuditLog(t, logPath, threshold)

		logger, err := audit.NewLogger(audit.LoggerConfig{
			Dir:           dir,
			MaxSizeBytes:  threshold,
			RetentionDays: 90,
		})
		require.NoError(t, err)
		defer func() {
			if closeErr := logger.Close(); closeErr != nil {
				_ = closeErr
			}
		}()

		require.NoError(t, logger.Log(rotationProbeEntry()))

		// Rotated file should exist with date suffix.
		files, err := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
		require.NoError(t, err)
		require.NotEmpty(t, files, "rotated audit file with date suffix should exist")

		rotated, err := os.Stat(files[0])
		require.NoError(t, err)
		assert.Equal(t, int64(threshold), rotated.Size(),
			"the rotated file must be the pre-filled log moved aside intact, not a truncated or fresh one")

		// New audit.jsonl should be below the rotation threshold — and must
		// actually contain the entry that triggered the rotation.
		info, err := os.Stat(logPath)
		require.NoError(t, err)
		assert.Less(t, info.Size(), int64(threshold),
			"the new audit.jsonl after rotation must be below the threshold")
		assert.NotZero(t, info.Size(),
			"the entry that triggered rotation must land in the NEW file, not be dropped by the rotation")
	})

	t.Run("no rotation while the file is below the configured threshold", func(t *testing.T) {
		// The partner half of the boundary. Without it, a logger that rotated
		// on EVERY write would sail through the subtest above.
		dir := t.TempDir()
		logPath := filepath.Join(dir, "audit.jsonl")
		const threshold = 8 * 1024

		prefillAuditLog(t, logPath, threshold-1)

		logger, err := audit.NewLogger(audit.LoggerConfig{
			Dir:           dir,
			MaxSizeBytes:  threshold,
			RetentionDays: 90,
		})
		require.NoError(t, err)
		defer func() {
			if closeErr := logger.Close(); closeErr != nil {
				_ = closeErr
			}
		}()

		// The arrange must still be intact AFTER NewLogger: its crash-recovery
		// pass truncates a torn final line, and a truncated pre-fill would make
		// "no rotation" pass because the file shrank, not because the logger
		// respected the threshold.
		requireAuditLogSize(t, logPath, threshold-1)

		require.NoError(t, logger.Log(rotationProbeEntry()))

		files, err := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
		require.NoError(t, err)
		assert.Empty(t, files,
			"one byte below the threshold must NOT rotate — rotation fires at >= maxSize, not on every write")
	})

	// The two subtests below pin the SHIPPED default: LoggerConfig.MaxSizeBytes
	// left unset must resolve to 50 MiB (pkg/audit's defaultMaxSize, which is
	// unexported and so not directly readable from this external test package).
	// They assert it behaviourally, from both sides of the boundary, so a
	// default that silently drifted to another value fails here.
	const defaultThreshold = 50 * 1024 * 1024 // 50 MiB

	t.Run("the unset threshold defaults to 50MiB and rotates there", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 587 (Scenario: Audit log rotation at 50MB)
		dir := t.TempDir()
		logPath := filepath.Join(dir, "audit.jsonl")

		prefillAuditLogSparse(t, logPath, defaultThreshold)

		// MaxSizeBytes deliberately omitted — this is the shipped default.
		logger, err := audit.NewLogger(audit.LoggerConfig{
			Dir:           dir,
			RetentionDays: 90,
		})
		require.NoError(t, err)
		defer func() {
			if closeErr := logger.Close(); closeErr != nil {
				_ = closeErr
			}
		}()

		require.NoError(t, logger.Log(rotationProbeEntry()))

		files, err := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
		require.NoError(t, err)
		assert.NotEmpty(t, files,
			"a logger left on the default threshold must rotate once audit.jsonl reaches 50 MiB")
	})

	t.Run("the unset threshold defaults to 50MiB and does not rotate one byte short", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "audit.jsonl")

		prefillAuditLogSparse(t, logPath, defaultThreshold-1)

		logger, err := audit.NewLogger(audit.LoggerConfig{
			Dir:           dir,
			RetentionDays: 90,
		})
		require.NoError(t, err)
		defer func() {
			if closeErr := logger.Close(); closeErr != nil {
				_ = closeErr
			}
		}()

		// Same guard as above: prove the sparse arrange survived NewLogger, so
		// this cannot pass on a file the logger quietly truncated to nothing.
		requireAuditLogSize(t, logPath, defaultThreshold-1)

		require.NoError(t, logger.Log(rotationProbeEntry()))

		files, err := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
		require.NoError(t, err)
		assert.Empty(t, files,
			"one byte below 50 MiB must not rotate — a default that had drifted smaller would rotate here")
	})

	t.Run("retention cleanup removes files older than retention_days", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 154 (User Story 9, Acceptance Scenario 4)
		dir := t.TempDir()

		// Create a "stale" rotated file with a date 8 days ago (past 7-day retention)
		staleDate := time.Now().AddDate(0, 0, -8).Format("2006-01-02")
		stalePath := filepath.Join(dir, "audit-"+staleDate+".jsonl")
		err := os.WriteFile(stalePath, []byte(`{"event":"old"}`+"\n"), 0o644)
		require.NoError(t, err)

		// Set file modification time to 8 days ago so cleanup picks it up
		oldTime := time.Now().AddDate(0, 0, -8)
		require.NoError(t, os.Chtimes(stalePath, oldTime, oldTime))

		// Opening a logger with 7-day retention triggers cleanup
		logger, err := audit.NewLogger(audit.LoggerConfig{
			Dir:           dir,
			RetentionDays: 7,
		})
		require.NoError(t, err)
		defer func() {
			if closeErr := logger.Close(); closeErr != nil {
				_ = closeErr
			}
		}()

		_, statErr := os.Stat(stalePath)
		assert.True(t, os.IsNotExist(statErr),
			"stale audit file (8 days old with 7-day retention) should be deleted")
	})

	t.Run("crash recovery: malformed last line is truncated on startup", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 301 (Edge: audit log corruption)
		// FR-032: validate last line of audit.jsonl on startup, truncate if malformed
		dir := t.TempDir()
		logPath := filepath.Join(dir, "audit.jsonl")

		// Write a valid line then a partial/corrupt line (simulates crash mid-write)
		content := `{"event":"tool_call","decision":"allow"}` + "\n" +
			`{"event":"partial` // truncated JSON — simulates crash
		err := os.WriteFile(logPath, []byte(content), 0o644)
		require.NoError(t, err)

		// Opening logger should recover by truncating the bad last line
		logger, err := audit.NewLogger(audit.LoggerConfig{
			Dir:           dir,
			RetentionDays: 90,
		})
		require.NoError(t, err)
		defer func() {
			if closeErr := logger.Close(); closeErr != nil {
				_ = closeErr
			}
		}()

		// File should now contain only the valid first line
		data, err := os.ReadFile(logPath)
		require.NoError(t, err)

		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		var lines []string
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				lines = append(lines, line)
			}
		}
		require.Equal(t, 1, len(lines), "only the valid line should remain after crash recovery")

		var parsed map[string]any
		err = json.Unmarshal([]byte(lines[0]), &parsed)
		assert.NoError(t, err, "remaining line should be valid JSON")
	})
}

// TestAuditLogger_RedactionPipeline is an integration test validating redaction is
// applied to all entries before they are written to disk.
// Traces to: wave2-security-layer-spec.md line 808 (TestAuditLogger_RedactionPipeline)
// BDD: Scenario: API key pattern is redacted (spec line 601) + audit writing
func TestAuditLogger_RedactionPipeline(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 808 (TestAuditLogger_RedactionPipeline)
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		RedactEnabled: true,
	})
	require.NoError(t, err)
	defer func() {
		if closeErr := logger.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	entry := audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  "allow",
		AgentID:   "researcher",
		Tool:      "web_search",
		Parameters: map[string]any{
			"api_key": "sk-ant-abc123def456ghi789jkl012mno345",
			"query":   "safe search query",
		},
		PolicyRule: "tools.allow matched 'web_search'",
	}
	require.NoError(t, logger.Log(&entry))

	// Read back and verify API key was redacted
	logPath := filepath.Join(dir, "audit.jsonl")
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)

	logContent := string(data)
	assert.NotContains(t, logContent, "sk-ant-abc123def456ghi789jkl012mno345",
		"API key must be redacted before writing to disk")
	assert.Contains(t, logContent, "[REDACTED]",
		"redacted placeholder must appear in log output")
	assert.Contains(t, logContent, "safe search query",
		"non-sensitive parameters should be preserved")
	assert.Contains(t, logContent, "web_search",
		"tool name should be preserved")
}

// TestLogger_Log_NilReceiver_DoesNotPanic asserts B1.2(a): calling Log on a
// nil *Logger is a no-op and never panics. The audit logger is reached
// through deeply-nested call chains — egress proxy denials, per-thread
// restrict failures, web_serve fail-closed — where the logger may be nil
// because boot continued without audit (sandbox.audit_log=false branch in
// the gateway, or audit construction failed and the operator chose
// log-and-continue). A panic here would crash the gateway on every denied
// egress request.
func TestLogger_Log_NilReceiver_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil *Logger.Log panicked: %v", r)
		}
	}()

	var logger *audit.Logger // explicitly nil
	err := logger.Log(&audit.Entry{
		Event:    audit.EventToolCall,
		Decision: audit.DecisionDeny,
		AgentID:  "test-agent",
		Tool:     "shell",
	})
	if err != nil {
		t.Errorf("nil *Logger.Log returned error %v; want nil (silent no-op)", err)
	}

	// Also assert the documented type-conversion form behaves identically.
	if err := (*audit.Logger)(nil).Log(&audit.Entry{Event: "any"}); err != nil {
		t.Errorf("(*audit.Logger)(nil).Log returned error %v; want nil", err)
	}

	// Nil entry is also tolerated — defensive double-guard for the rare
	// case where a caller forgets to check before passing.
	if err := logger.Log(nil); err != nil {
		t.Errorf("nil *Logger.Log(nil entry) returned error %v; want nil", err)
	}
}

// prefillAuditLog writes exactly size bytes of newline-terminated filler to
// path and PROVES the arrange took.
//
// The arrange is asserted, never assumed. If the fill comes up short — a full
// disk, a truncated write, a read-only mount — this fails here naming that
// cause, so it can never be mistaken further down the test for "the logger
// failed to rotate".
//
// That is the lesson of the fixture these helpers replace. It pre-filled
// audit.jsonl with 51,199 real kilobyte lines and discarded every write error
// from the loop. On a CI worker whose /cache volume was full, the pre-fill
// silently stopped short, rotation correctly did not fire, and the test
// reported the symptom ("rotated audit file with date suffix should exist")
// rather than the cause (ENOSPC in its own setup).
func prefillAuditLog(t *testing.T, path string, size int64) {
	t.Helper()

	const lineLen = 128 // 127 filler bytes + '\n'
	rem := size % lineLen
	require.NotEqual(t, int64(1), rem,
		"arrange: a pre-fill of %d bytes cannot end on a line boundary (it would need a 1-byte line, "+
			"which is a bare newline); pick a size whose remainder mod %d is 0 or >= 2", size, lineLen)

	buf := make([]byte, 0, size)
	for int64(len(buf))+lineLen <= size {
		buf = append(buf, strings.Repeat("x", lineLen-1)+"\n"...)
	}
	// The file MUST end on an exact line boundary. NewLogger runs a
	// crash-recovery pass that truncates a torn (unterminated) final line, so
	// a pre-fill that stopped mid-line would be silently shortened before the
	// threshold is ever evaluated — and a "does not rotate" assertion would
	// then pass because the arrange shrank, not because the logger held the
	// line. Complete-but-non-JSON lines are preserved, so plain filler is fine.
	if rem > 0 {
		buf = append(buf, strings.Repeat("x", int(rem)-1)+"\n"...)
	}
	require.NoError(t, os.WriteFile(path, buf, 0o600), "arrange: pre-fill %s", path)
	requireAuditLogSize(t, path, size)
}

// prefillAuditLogSparse gives path an apparent size of exactly size bytes
// without allocating the blocks to back it.
//
// NewLogger learns the current file size from a stat, and a stat reports the
// apparent size, so a sparse file drives the threshold check exactly as a full
// one would — at a fraction of the cost and with no dependency on free disk.
// This is what lets the 50 MiB default be pinned without writing 50 MiB.
func prefillAuditLogSparse(t *testing.T, path string, size int64) {
	t.Helper()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	require.NoError(t, err, "arrange: create %s", path)
	truncErr := f.Truncate(size)
	closeErr := f.Close()
	require.NoError(t, truncErr, "arrange: truncate %s to %d bytes", path, size)
	require.NoError(t, closeErr, "arrange: close %s", path)
	requireAuditLogSize(t, path, size)
}

// requireAuditLogSize is the shared arrange assertion: the pre-fill must have
// produced exactly the size the caller asked for, or the test stops here with
// the real reason instead of going on to measure a broken setup.
func requireAuditLogSize(t *testing.T, path string, want int64) {
	t.Helper()

	info, err := os.Stat(path)
	require.NoError(t, err, "arrange: stat %s", path)
	require.Equal(t, want, info.Size(),
		"arrange did not take: %s is %d bytes, want exactly %d. The pre-fill came up short, so every "+
			"rotation assertion below would be measuring the broken arrange rather than the logger.",
		path, info.Size(), want)
}

// rotationProbeEntry is the single entry each rotation subtest writes in order
// to make the logger evaluate its size threshold.
func rotationProbeEntry() *audit.Entry {
	return &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  "allow",
		AgentID:   "test-agent",
		Tool:      "web_search",
	}
}
