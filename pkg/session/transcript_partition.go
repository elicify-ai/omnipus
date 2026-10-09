// transcript_partition.go: UTC day partitioning of the one append-only
// chat/model transcript archive (session-core U2, FR-004/FR-005).
//
// The archive is ONE logical append-only transcript split into UTC day files
// so a window read can be bounded to the days it needs (FR-005). The layout,
// all inside a session directory:
//
//	transcript.jsonl        the CURRENT (most recent) day's file — the live,
//	                        hot file every existing reader/writer already
//	                        opens by that name, kept as the current partition
//	                        so same-day appends, tool-call status rewrites and
//	                        provenance paired saves are unchanged.
//	<YYYY-MM-DD>.jsonl      every PRIOR day, written once when its day rolls
//	                        over (a byte-preserving rename of transcript.jsonl
//	                        to its date name) and then frozen.
//	transcript.day          the day mark: the UTC day transcript.jsonl
//	                        currently holds. Disk-only, never on the wire.
//
// Rollover rule (ADR D2 — "acceptance order governs append order even when
// source timestamps are delayed"): an entry whose UTC day is NEWER than the
// current day mark rolls the file over; an entry whose day is the SAME or
// OLDER is appended to the current file — a delayed older timestamp never
// inserts into an already-passed day file ("no old-file back-insertion").
// Rows are therefore always in acceptance order within the current file.
//
// Rollover is crash-safe by ordering: the rename happens BEFORE the day mark
// is advanced, so a crash in between leaves the old day safely in its date
// file and an unchanged mark; the next append re-derives the rollover (skip
// the rename when transcript.jsonl is already absent) and completes it.
package session

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

const (
	// transcriptFileName is the live/current day-partition file. Every reader
	// and writer that opens "transcript.jsonl" keeps working: it is always the
	// most recent partition.
	transcriptFileName = "transcript.jsonl"
	// transcriptDayMarkFile is the disk-only day mark (see the package comment).
	transcriptDayMarkFile = "transcript.day"
	// transcriptDayLayout is the UTC day key format used for file names and the
	// day mark.
	transcriptDayLayout = "2006-01-02"
)

// transcriptDayPattern matches a rolled-over date-partition file name. Only
// files this shape (and transcript.jsonl) are transcript partitions; any other
// .jsonl file in the session directory is not part of the chat/model archive.
var transcriptDayPattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}).*\.jsonl$`)

// transcriptPartitionPathLocked returns the archive path an entry dated entryDay
// must append to, performing a day rollover when entryDay is newer than the
// persisted day mark. The caller holds sessionID's shard.
func (us *UnifiedStore) transcriptPartitionPathLocked(sessionID, entryDay string) (string, error) {
	dir := filepath.Join(us.baseDir, sessionID)
	current := filepath.Join(dir, transcriptFileName)

	mark, err := us.readTranscriptDayMark(sessionID)
	if err != nil {
		return "", err
	}
	if mark == "" {
		// Fresh session, or a legacy single-file session that predates day
		// partitioning: adopt the existing transcript.jsonl as the current
		// day's file (never split a pre-existing archive) and record the mark.
		if err := us.writeTranscriptDayMark(sessionID, entryDay); err != nil {
			return "", err
		}
		return current, nil
	}
	if entryDay <= mark {
		// Same day, or a delayed older timestamp: append to the current file.
		// Comparing the date keys lexically is correct for the YYYY-MM-DD form.
		return current, nil
	}
	// A newer day: freeze the current file under its own day name, then start
	// a fresh current file. The rename is idempotent under a crash — when the
	// current file is already gone we only advance the mark.
	if _, statErr := os.Stat(current); statErr == nil {
		rolled := us.uniqueRolledPath(dir, mark)
		if err := os.Rename(current, rolled); err != nil {
			return "", fmt.Errorf("unified_store: roll transcript day %s: %w", mark, err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("unified_store: stat transcript before rollover: %w", statErr)
	}
	if err := us.writeTranscriptDayMark(sessionID, entryDay); err != nil {
		return "", err
	}
	return current, nil
}

// uniqueRolledPath returns a non-colliding "<day>.jsonl" path inside dir. A
// collision is only reachable after an out-of-band write or a partially
// completed prior rollover; a suffixed name keeps the date prefix (and so the
// read order) intact rather than overwriting retained bytes.
func (us *UnifiedStore) uniqueRolledPath(dir, day string) string {
	base := filepath.Join(dir, day+".jsonl")
	if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
		return base
	}
	for i := 1; ; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s-%d.jsonl", day, i))
		if _, err := os.Stat(cand); errors.Is(err, os.ErrNotExist) {
			return cand
		}
	}
}

// readTranscriptDayMark returns the persisted current-day mark, or "" when no
// mark exists yet (fresh or legacy session).
func (us *UnifiedStore) readTranscriptDayMark(sessionID string) (string, error) {
	b, err := os.ReadFile(filepath.Join(us.baseDir, sessionID, transcriptDayMarkFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("unified_store: read transcript day mark: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// writeTranscriptDayMark persists the current-day mark atomically.
func (us *UnifiedStore) writeTranscriptDayMark(sessionID, day string) error {
	path := filepath.Join(us.baseDir, sessionID, transcriptDayMarkFile)
	if err := fileutil.WriteFileAtomic(path, []byte(day+"\n"), 0o600); err != nil {
		return fmt.Errorf("unified_store: write transcript day mark: %w", err)
	}
	return nil
}

// transcriptPartitionPaths returns the archive's partitions in chronological
// order: rolled-over day files ascending by their date prefix, then the
// current transcript.jsonl last. A missing session directory yields no paths.
func (us *UnifiedStore) transcriptPartitionPaths(sessionID string) ([]string, error) {
	dir := filepath.Join(us.baseDir, sessionID)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("unified_store: list transcript partitions: %w", err)
	}
	type dayFile struct {
		day  string
		name string
	}
	var days []dayFile
	current := ""
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == transcriptFileName {
			current = name
			continue
		}
		if m := transcriptDayPattern.FindStringSubmatch(name); m != nil {
			days = append(days, dayFile{day: m[1], name: name})
		}
	}
	sort.Slice(days, func(i, j int) bool {
		if days[i].day != days[j].day {
			return days[i].day < days[j].day
		}
		return days[i].name < days[j].name
	})
	paths := make([]string, 0, len(days)+1)
	for _, d := range days {
		paths = append(paths, filepath.Join(dir, d.name))
	}
	if current != "" {
		paths = append(paths, filepath.Join(dir, current))
	}
	return paths, nil
}

// warnOnStrayDayPartition logs when a day-partition path cannot be listed —
// a read that silently drops a partition would hide retained history.
func warnOnStrayDayPartition(sessionID string, err error) {
	slog.Warn("unified_store: transcript partition list failed; reading current partition only",
		"session_id", sessionID, "error", err)
}

// transcriptLineCountPartitions returns the session's GLOBAL transcript record
// index — the number of nonempty records across every day partition in order.
// The index must be global (not per-file) because a TranscriptLine projection
// address identifies one record for the session's whole lifetime.
func (us *UnifiedStore) transcriptLineCountPartitions(sessionID string) (int, error) {
	paths, err := us.transcriptPartitionPaths(sessionID)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, p := range paths {
		n, err := transcriptLineCount(p)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}
