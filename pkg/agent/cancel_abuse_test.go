// Package agent — cancel_abuse_test.go
//
// Tests for the cancelAbuseDetector (FR-25a).
// Moved from pkg/gateway/cancel_handler_test.go where they tested a now-deleted
// gateway-local duplicate of this type.

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// newAuditLoggerInDir creates a fresh audit logger writing to dir.
func newAuditLoggerInDir(t *testing.T, dir string) *audit.Logger {
	t.Helper()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 1})
	require.NoError(t, err)
	return logger
}

// splitAuditLines splits JSONL data on newlines.
func splitAuditLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

// T20a — 10 attempts in <60s from same canceller emits cancel_abuse_pattern
// WARNING exactly once; counter resets so the next burst also emits once.
func TestCancelAbuse_BurstEmitsOnceAndResets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := newAuditLoggerInDir(t, dir)

	d := newCancelAbuseDetector()
	d.burstAt = 3 // lower threshold so we don't need 10 iterations
	d.window = 10 * time.Second

	ctx := context.Background()
	now := time.Now()

	// First burst: 3 attempts → exactly one cancel_abuse_pattern emitted.
	for i := 0; i < 3; i++ {
		d.recordAttempt(ctx, "alice", "web", now.Add(time.Duration(i)*100*time.Millisecond), logger)
	}

	type row struct {
		Event    string `json:"event"`
		Severity string `json:"severity"`
	}

	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)
	var rows []row
	for _, line := range splitAuditLines(data) {
		if len(line) == 0 {
			continue
		}
		var r row
		if json.Unmarshal(line, &r) == nil && r.Event != "" {
			rows = append(rows, r)
		}
	}

	abuseEvents := 0
	for _, r := range rows {
		if r.Event == audit.EventCancelAbusePattern {
			abuseEvents++
			assert.Equal(t, "WARN", r.Severity, "cancel_abuse_pattern must be WARN severity")
		}
	}
	assert.Equal(t, 1, abuseEvents, "first burst must emit exactly one cancel_abuse_pattern")

	// Second burst: 3 more attempts → one more event (window was reset after first burst).
	for i := 0; i < 3; i++ {
		d.recordAttempt(ctx, "alice", "web", now.Add(time.Duration(3+i)*100*time.Millisecond), logger)
	}

	data2, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)
	var rows2 []row
	for _, line := range splitAuditLines(data2) {
		if len(line) == 0 {
			continue
		}
		var r row
		if json.Unmarshal(line, &r) == nil && r.Event != "" {
			rows2 = append(rows2, r)
		}
	}

	abuseEvents2 := 0
	for _, r := range rows2 {
		if r.Event == audit.EventCancelAbusePattern {
			abuseEvents2++
		}
	}
	assert.Equal(t, 2, abuseEvents2, "second burst must emit one more cancel_abuse_pattern (total 2)")
}

// T20a extra — attempts from different users must not cross-count.
func TestCancelAbuse_DifferentUsersAreIndependent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := newAuditLoggerInDir(t, dir)

	d := newCancelAbuseDetector()
	d.burstAt = 3
	d.window = 10 * time.Second

	ctx := context.Background()
	now := time.Now()

	// 2 attempts from alice, 2 from bob — neither crosses the threshold of 3.
	for i := 0; i < 2; i++ {
		d.recordAttempt(ctx, "alice", "web", now.Add(time.Duration(i)*100*time.Millisecond), logger)
		d.recordAttempt(ctx, "bob", "web", now.Add(time.Duration(i)*100*time.Millisecond), logger)
	}

	// No audit file should be written (no burst threshold crossed).
	_, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if err == nil {
		// File exists — make sure no abuse event was written.
		data, readErr := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
		require.NoError(t, readErr)
		for _, line := range splitAuditLines(data) {
			if len(line) == 0 {
				continue
			}
			var r struct {
				Event string `json:"event"`
			}
			if json.Unmarshal(line, &r) == nil {
				assert.NotEqual(t, audit.EventCancelAbusePattern, r.Event,
					"no abuse event should fire when neither user crosses the threshold")
			}
		}
	}
	// If file doesn't exist — that's fine too.
}
