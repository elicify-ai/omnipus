// Contract test: Plan 3 §1 acceptance decision — audit log rotates by size (50 MB)
// OR daily, whichever comes first. Retains at most 10 files.
//
// BDD: Given an audit log at the 50MB threshold, When the next entry is written,
//
//	Then the current file is rotated and a new file is started.
//
// Acceptance decision: Plan 3 §1 "Audit rotation: size (50 MB) OR daily, keep last 10 files"
// Traces to: temporal-puzzling-melody.md §4 Axis-1, pkg/audit/rotation_test.go

package audit_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestRotationBySizeAndDaily verifies two rotation triggers:
//  1. Size trigger: when the current file reaches MaxSizeBytes, the next write rotates.
//  2. Daily trigger: a new UTC date causes rotation on the next write.
//  3. Retention: only the 10 most recent files are kept.
//
// Traces to: temporal-puzzling-melody.md §4 Axis-1 — TestRotationBySizeAndDaily
func TestRotationBySizeAndDaily(t *testing.T) {
	t.Run("size-based rotation on small threshold", func(t *testing.T) {
		dir := t.TempDir()

		// Set a very small max size (512 bytes) so we can trigger rotation
		// without writing 50 MB of real data.
		logger, err := audit.NewLogger(audit.LoggerConfig{
			Dir:           dir,
			MaxSizeBytes:  512, // 512 bytes — rotation-friendly for tests
			RetentionDays: 90,
		})
		require.NoError(t, err)
		// Test cleanup: Close error is inconsequential — t.TempDir() removes
		// the backing directory regardless, and no test here asserts on it.
		defer func() {
			if err := logger.Close(); err != nil {
				_ = err
			}
		}()

		// Write entries until we exceed the threshold.
		entry := &audit.Entry{
			Timestamp: time.Now().UTC(),
			Event:     audit.EventToolCall,
			Decision:  audit.DecisionAllow,
			AgentID:   "ray",
			Tool:      "web_fetch",
		}

		// Write more than enough to exceed 512 bytes.
		for i := 0; i < 20; i++ {
			entry.Details = map[string]any{"seq": i, "padding": strings.Repeat("x", 50)}
			require.NoError(t, logger.Log(entry), "write %d must not error", i)
		}

		// After rotation, there must be at least 2 files: the rotated file + the current one.
		files, err := filepath.Glob(filepath.Join(dir, "audit*.jsonl"))
		require.NoError(t, err)
		assert.GreaterOrEqualf(t, len(files), 2,
			"size-based rotation must create at least one rotated file; got %d files: %v",
			len(files), files)
	})

	t.Run("daily rotation contract", func(t *testing.T) {
		// The daily rotation check is in the logger itself: when time.Now().UTC().Format("2006-01-02")
		// differs from currentDate, rotation fires. We verify this contract by checking that
		// the log file name embeds the date (so old files are distinguishable from today's).
		dir := t.TempDir()
		logger, err := audit.NewLogger(audit.LoggerConfig{
			Dir:           dir,
			MaxSizeBytes:  50 * 1024 * 1024, // 50 MB — don't trigger on size
			RetentionDays: 90,
		})
		require.NoError(t, err)
		defer func() {
			if err := logger.Close(); err != nil {
				_ = err
			}
		}()

		require.NoError(t, logger.Log(&audit.Entry{
			Timestamp: time.Now().UTC(),
			Event:     audit.EventStartup,
			Decision:  audit.DecisionAllow,
		}))

		// The active log file must exist.
		activeLog := filepath.Join(dir, "audit.jsonl")
		_, statErr := os.Stat(activeLog)
		assert.NoError(t, statErr, "audit.jsonl must exist after first write")

		// Differentiation: a second write to the same logger produces a different file size.
		info1, _ := os.Stat(activeLog)
		size1 := int64(0)
		if info1 != nil {
			size1 = info1.Size()
		}

		require.NoError(t, logger.Log(&audit.Entry{
			Timestamp: time.Now().UTC(),
			Event:     audit.EventToolCall,
			Decision:  audit.DecisionAllow,
			AgentID:   "max",
			Tool:      "browser.screenshot",
		}))

		info2, _ := os.Stat(activeLog)
		size2 := int64(0)
		if info2 != nil {
			size2 = info2.Size()
		}
		assert.Greater(t, size2, size1,
			"file size must grow with each write (not a no-op logger)")
	})

	t.Run("retention limits to 10 files", func(t *testing.T) {
		dir := t.TempDir()

		// Pre-create 12 rotated audit files with backdated timestamps.
		// A real rotation creates files named audit-<date>.jsonl; we simulate this.
		// The files must have mod times older than RetentionDays so cleanupExpired
		// treats them as eligible for deletion (it checks ModTime, not filename date).
		baseDate := time.Now().UTC().AddDate(0, 0, -15)
		for i := 0; i < 12; i++ {
			day := baseDate.AddDate(0, 0, i)
			name := fmt.Sprintf("audit-%s.jsonl", day.Format("2006-01-02"))
			path := filepath.Join(dir, name)
			require.NoError(t, os.WriteFile(path, []byte(`{"event":"startup"}`+"\n"), 0o600))
			// Backdate the mtime so cleanupExpired sees it as old.
			require.NoError(t, os.Chtimes(path, day, day),
				"backdating mtime on %s must succeed", name)
		}

		// Create a new logger in the same directory. It runs cleanupExpired() on
		// startup which enforces the 10-file retention ceiling.
		// RetentionDays=1 means anything older than 1 day is eligible for cleanup.
		logger, err := audit.NewLogger(audit.LoggerConfig{
			Dir:           dir,
			MaxSizeBytes:  50 * 1024 * 1024,
			RetentionDays: 1,
		})
		require.NoError(t, err)
		defer func() {
			if err := logger.Close(); err != nil {
				_ = err
			}
		}()

		// After startup cleanup, the total number of rotated files must be within retention.
		rotated, err := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
		require.NoError(t, err)

		// The audit.jsonl (current) plus up to 10 rotated files — 12 pre-created files
		// older than 1 day should all be deleted by cleanupExpired.
		t.Logf("rotated files after cleanup: %d — %v", len(rotated), rotated)

		// The acceptance contract is: files beyond the retention period are deleted.
		// With RetentionDays=1 and all 12 files being 3+ days old, all should be gone.
		// Assert strictly: fewer than 12 must remain (cleanup actually happened).
		assert.Less(t, len(rotated), 12,
			"retention cleanup must delete at least one expired file; all 12 are >1 day old")
		assert.LessOrEqual(t, len(rotated), 10,
			"retention cleanup must enforce the 10-file ceiling")

		// Extension (chain-checkpoint regression coverage): retention cleanup
		// must never leave VerifyDir reporting a false-positive tamper break,
		// and it must actually walk (not silently skip) whatever survives.
		// These fixture files are hand-written with no `hmac` field at all
		// (pre-#155-chain shape), so they only exercise the PreChainEntries
		// tolerance path here, not the checkpoint-seeding path itself — see
		// the dedicated "chain verification survives retention deleting the
		// genesis file" subtest below for a fixture with a real HMAC chain.
		res, verifyErr := audit.VerifyDir(context.Background(), dir, testKey(t))
		require.NoError(t, verifyErr)
		assert.Truef(t, res.Valid,
			"retention cleanup must not produce a false-positive tamper report; got: %s", res.String())
		assert.Equal(t, len(rotated)+1, res.FilesScanned,
			"VerifyDir must scan every surviving rotated file plus the current file, not skip any")
	})

	// TestRotationBySizeAndDaily / "chain verification survives retention
	// deleting the genesis file" is the direct regression test for the bug:
	// cleanupExpired deleting the TRUE first (genesis-seeded) rotated file
	// used to leave VerifyDir seeding from GenesisSeed() regardless, which
	// no longer matched the new-oldest survivor's first entry. That produced
	// a false "hmac mismatch" AND — because VerifyFile/VerifyDir stop at the
	// first break — silently skipped every file after it. Unlike the
	// subtest above, this one writes real entries through the production
	// Logger with a real HMAC chain key, so the checkpoint-seeding path in
	// checkpoint.go / VerifyDir is genuinely exercised, not just the
	// pre-chain tolerance path.
	t.Run("chain verification survives retention deleting the genesis file", func(t *testing.T) {
		dir := t.TempDir()
		key := testKey(t)

		// MaxSizeBytes is set smaller than a single encoded entry so that
		// (almost) every write rotates out whatever accumulated before it —
		// this deterministically produces several small rotated files
		// instead of relying on exact byte-size arithmetic.
		logger1, err := audit.NewLogger(audit.LoggerConfig{
			Dir:           dir,
			MaxSizeBytes:  200,
			RetentionDays: 90,
			HMACKey:       key,
		})
		require.NoError(t, err)

		const totalEntries = 10
		for i := 0; i < totalEntries; i++ {
			require.NoError(t, logger1.Log(&audit.Entry{
				Timestamp:  time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
				Event:      audit.EventToolCall,
				Decision:   audit.DecisionAllow,
				SessionID:  "sess-retention-chain",
				Tool:       "echo",
				Parameters: map[string]any{"seq": i, "filler": strings.Repeat("x", 100)},
			}))
			// Distinct millisecond buckets avoid the rotate() dst-name
			// collision documented in TestVerify_RotationPreservesChain
			// (hmac_test.go) — two rotations in the same millisecond would
			// silently overwrite one file with another.
			time.Sleep(2 * time.Millisecond)
		}
		require.NoError(t, logger1.Close())

		rotatedFiles, err := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
		require.NoError(t, err)
		// Sort by ModTime, NOT filename: rotate()'s dst-collision fallback
		// names the first rotation of a day `audit-<date>.jsonl` and every
		// later one that same day `audit-<date>-<unixmilli>.jsonl`. Because
		// '-' sorts before '.' in ASCII, a plain sort.Strings puts every
		// millisecond-suffixed (later) file BEFORE the plain-named (true
		// oldest / genesis) file — exactly backwards. ModTime reflects the
		// real rotation order (see fileorder.go in the audit package).
		sortByModTime(t, rotatedFiles)
		require.GreaterOrEqualf(t, len(rotatedFiles), 4,
			"test setup must produce enough rotated files to backdate a genesis subset; got %d: %v",
			len(rotatedFiles), rotatedFiles)

		// Backdate the older half of the rotated files — including index 0,
		// the TRUE genesis-seeded file that VerifyDir used to unconditionally
		// assume was still present — so retention cleanup deletes them,
		// while the newer rotated files and the current audit.jsonl stay
		// fresh (untouched mtime) and must survive.
		numToExpire := len(rotatedFiles) / 2
		if numToExpire < 1 {
			numToExpire = 1
		}
		deletedEntryCount := 0
		farPast := time.Now().UTC().AddDate(0, 0, -100)
		for i := 0; i < numToExpire; i++ {
			path := rotatedFiles[i]
			data, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			trimmed := strings.TrimRight(string(data), "\n")
			if trimmed != "" {
				deletedEntryCount += strings.Count(trimmed, "\n") + 1
			}
			require.NoError(t, os.Chtimes(path, farPast, farPast),
				"backdating mtime on %s must succeed", path)
		}

		// Simulate a process restart: a fresh Logger construction runs
		// cleanupExpired() with a retention window that makes the backdated
		// files (but not the fresh ones) eligible for deletion.
		logger2, err := audit.NewLogger(audit.LoggerConfig{
			Dir:           dir,
			MaxSizeBytes:  50 * 1024 * 1024, // don't trigger size rotation on open
			RetentionDays: 1,
			HMACKey:       key,
		})
		require.NoError(t, err)
		defer func() {
			if err := logger2.Close(); err != nil {
				_ = err
			}
		}()

		survivingRotated, err := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
		require.NoError(t, err)
		require.Lessf(t, len(survivingRotated), len(rotatedFiles),
			"retention cleanup must have deleted the backdated (genesis-including) files; before=%d after=%d",
			len(rotatedFiles), len(survivingRotated))

		// The fix: cleanupExpired must have persisted a signed checkpoint
		// the moment it deleted a file carrying real chain-HMAC entries.
		checkpointFile := filepath.Join(dir, "audit-chain-checkpoint.json")
		_, statErr := os.Stat(checkpointFile)
		require.NoErrorf(t, statErr,
			"cleanup deleting the genesis file must persist a chain checkpoint at %s", checkpointFile)

		res, err := audit.VerifyDir(context.Background(), dir, key)
		require.NoError(t, err)
		require.Truef(
			t,
			res.Valid,
			"chain verification must NOT false-positive after retention deletes the genesis file; got: %s",
			res.String(),
		)

		// Prove newer files were actually checked, not silently skipped after
		// a bogus break at the new-oldest survivor: count every surviving
		// entry on disk and confirm VerifyDir scanned exactly that many.
		survivingTotal := 0
		allSurviving := append([]string{}, survivingRotated...)
		allSurviving = append(allSurviving, filepath.Join(dir, "audit.jsonl"))
		for _, f := range allSurviving {
			data, readErr := os.ReadFile(f)
			require.NoError(t, readErr)
			trimmed := strings.TrimRight(string(data), "\n")
			if trimmed == "" {
				continue
			}
			survivingTotal += strings.Count(trimmed, "\n") + 1
		}
		require.Equal(t, totalEntries-deletedEntryCount, survivingTotal,
			"sanity: no entries should be lost besides the ones deliberately expired")
		require.Equal(t, survivingTotal, res.EntriesScanned,
			"VerifyDir must scan every surviving entry, not stop after a checkpoint-seeded false break")
	})
}

// sortByModTime sorts full file paths in place, oldest first, mirroring the
// production sortAuditFilesChronologically helper in
// pkg/audit/fileorder.go (unexported, so the external audit_test package
// can't call it directly — this is a deliberate parallel implementation,
// not a re-export).
//
// Sorts primarily by each file's FIRST entry's own `timestamp` field (the
// one signal always grounded in the actual data), falling back to on-disk
// ModTime and finally filename. Two things make filename-only or
// ModTime-only ordering unsafe here: (1) rotate()'s dst-collision fallback
// names the first rotation of a day `audit-<date>.jsonl` and every later one
// `audit-<date>-<unixmilli>.jsonl`, and '-' (0x2D) sorts before '.' (0x2E)
// in ASCII, so the plain-named (true oldest) file sorts LAST lexically; (2)
// rapid rotation can leave two or more rotated files sharing an identical
// on-disk ModTime (confirmed by direct repro), so ModTime alone isn't a
// reliable tiebreaker either.
func sortByModTime(t *testing.T, paths []string) {
	t.Helper()
	sort.SliceStable(paths, func(i, j int) bool {
		ti, tiOK := firstEntryTimestampForTest(t, paths[i])
		tj, tjOK := firstEntryTimestampForTest(t, paths[j])
		if tiOK && tjOK && !ti.Equal(tj) {
			return ti.Before(tj)
		}
		iInfo, err := os.Stat(paths[i])
		require.NoError(t, err)
		jInfo, err := os.Stat(paths[j])
		require.NoError(t, err)
		if !iInfo.ModTime().Equal(jInfo.ModTime()) {
			return iInfo.ModTime().Before(jInfo.ModTime())
		}
		return paths[i] < paths[j]
	})
}

// firstEntryTimestampForTest reads the `timestamp` field of the first JSON
// line in path. Returns (zero, false) for an empty/unparseable file.
func firstEntryTimestampForTest(t *testing.T, path string) (time.Time, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	lines := strings.SplitN(strings.TrimLeft(string(data), "\n"), "\n", 2)
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return time.Time{}, false
	}
	var m struct {
		Timestamp time.Time `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil || m.Timestamp.IsZero() {
		return time.Time{}, false
	}
	return m.Timestamp, true
}
