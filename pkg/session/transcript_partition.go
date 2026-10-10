// transcript_partition.go: the file names of a session's one append-only
// archive and the read-side partition lister (session-core U2, FR-004/FR-005).
//
// The archive is ONE logical append-only record stream split into UTC day files
// inside the session directory. Writing, rollover and addressing live in
// archive_day_store.go (ArchiveDayStore); this file only names the files and
// lists them for readers:
//
//	transcript.jsonl   the CURRENT day's file
//	<YYYY-MM-DD>.jsonl every prior day, frozen once rolled
//	transcript.day     the day mark: the UTC day transcript.jsonl holds
//	                   (disk-only, never on the wire)
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
