// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package audit

// Regression coverage for rotate()'s same-millisecond dst-collision bug
// (audit.go): when the bare per-day rotated name (`audit-<date>.jsonl`) is
// already taken, rotate() falls back to a millisecond-suffixed name
// (`audit-<date>-<unixmilli>.jsonl`) but, before this fix, never re-checked
// THAT name for a collision. Two rotations landing in the same millisecond
// (plausible under rapid, size-triggered rotation during a write burst, or
// under test/CI timing) computed the identical fallback name, and the
// second os.Rename silently replaced the first rotated file via POSIX
// rename(2) semantics — permanent, silent loss of an entire audit log file.
//
// Package-internal (not audit_test) because the test needs to: (1) call the
// unexported rotate() method directly to control exactly when rotation
// happens rather than relying on size/date triggers, and (2) stub the
// unexported rotateClockNow seam to force multiple rotations to observe the
// identical millisecond value, deterministically reproducing a collision
// that would otherwise only be plausible, not guaranteed, under real
// timing.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRotate_SameMillisecondCollision_NoDataLoss forces three rotations on
// the same simulated UTC day, with the collision clock stubbed to a fixed
// instant so the second AND third rotations compute the exact same
// millisecond-suffixed fallback name that the first fallback used. Before
// the fix, the third rotation's os.Rename would have silently overwritten
// the second rotation's file (both computing the bare `audit-<date>-<ms>`
// fallback). After the fix, the collision loop must probe an incrementing
// counter suffix and produce three distinct files, none of which loses its
// content.
func TestRotate_SameMillisecondCollision_NoDataLoss(t *testing.T) {
	dir := t.TempDir()

	key, err := DeriveAuditKey([]byte("rotation-collision-fixture-master-key!!!!!!"))
	require.NoError(t, err)

	logger, err := NewLogger(LoggerConfig{
		Dir:           dir,
		MaxSizeBytes:  50 * 1024 * 1024, // large — only rotate() calls below trigger rotation
		RetentionDays: 90,
		HMACKey:       key,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	// currentDate is set from the real wall clock at open time (the
	// rotateClockNow seam only governs the millis-collision fallback, not
	// the per-day bare name) — capture it instead of hardcoding "today" so
	// this test doesn't silently start failing on a UTC day rollover.
	today := logger.currentDate

	// Freeze the collision clock so every rotate() call in this test
	// computes the identical millisecond value — the deterministic
	// reproduction of "two rotations landing in the same millisecond".
	frozen := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	prevClock := rotateClockNow
	rotateClockNow = func() time.Time { return frozen }
	t.Cleanup(func() { rotateClockNow = prevClock })

	writeMarker := func(marker string) {
		t.Helper()
		require.NoError(t, logger.Log(&Entry{
			Timestamp: time.Now().UTC(),
			Event:     EventToolCall,
			Decision:  DecisionAllow,
			SessionID: marker,
			Tool:      "echo",
		}))
	}
	forceRotate := func() {
		t.Helper()
		logger.mu.Lock()
		rotErr := logger.rotate()
		logger.mu.Unlock()
		require.NoError(t, rotErr, "rotate() must not error under a same-millisecond collision")
	}

	// Rotation 1: bare name `audit-<date>.jsonl` does not exist yet, no
	// collision branch entered.
	writeMarker("marker-A")
	forceRotate()

	// Rotation 2: bare name now exists -> collision branch -> first
	// fallback attempt `audit-<date>-<millis>.jsonl` (frozen clock).
	writeMarker("marker-B")
	forceRotate()

	// Rotation 3: bare name AND the first millis fallback both exist
	// (frozen clock reproduces the identical millisecond) -> must probe
	// the counter-suffixed fallback `audit-<date>-<millis>-1.jsonl` rather
	// than silently overwriting rotation 2's file.
	writeMarker("marker-C")
	forceRotate()

	rotated, err := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
	require.NoError(t, err)
	require.Lenf(
		t,
		rotated,
		3,
		"three same-millisecond rotations must produce three distinct files, not silently overwrite one another; got: %v",
		rotated,
	)

	// Every marker must be present in exactly one rotated file, and no
	// rotated file may be empty (which would indicate a truncated/replaced
	// write from a lost race).
	markerToFile := map[string]string{}
	for _, path := range rotated {
		data, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		content := string(data)
		require.NotEmptyf(t, strings.TrimSpace(content), "rotated file %s must not be empty", path)

		for _, marker := range []string{"marker-A", "marker-B", "marker-C"} {
			if strings.Contains(content, marker) {
				if existing, ok := markerToFile[marker]; ok {
					t.Fatalf("marker %s found in more than one file (%s and %s) — data was duplicated, not just lost",
						marker, existing, path)
				}
				markerToFile[marker] = path
			}
		}
	}
	assert.Lenf(t, markerToFile, 3,
		"all three markers must survive across the three rotated files with no overwrite/loss; found: %v", markerToFile)

	// Sanity: the three filenames follow the documented shape — bare name,
	// then the millis fallback, then the counter-extended fallback. Derived
	// from the frozen clock rather than hardcoded so the test doesn't rely
	// on a magic UnixMilli() literal.
	frozenMillis := frozen.UnixMilli()
	wantBare := fmt.Sprintf("audit-%s.jsonl", today)
	wantMillis := fmt.Sprintf("audit-%s-%d.jsonl", today, frozenMillis)
	wantMillisCounter := fmt.Sprintf("audit-%s-%d-1.jsonl", today, frozenMillis)
	var haveBare, haveMillis, haveMillisCounter bool
	for _, path := range rotated {
		switch filepath.Base(path) {
		case wantBare:
			haveBare = true
		case wantMillis:
			haveMillis = true
		case wantMillisCounter:
			haveMillisCounter = true
		}
	}
	assert.Truef(t, haveBare, "expected bare-name rotated file %s", wantBare)
	assert.Truef(t, haveMillis, "expected millis-fallback rotated file %s for the frozen clock", wantMillis)
	assert.Truef(
		t,
		haveMillisCounter,
		"expected counter-extended fallback file %s after the millis fallback also collided",
		wantMillisCounter,
	)
}
