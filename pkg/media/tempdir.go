package media

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const TempDirName = "omnipus_media"

// TempDir returns the directory used for downloaded media. When
// OMNIPUS_HOME is set the dir lives inside the workspace
// ($OMNIPUS_HOME/media) so files survive across gateway restarts and
// stay inside the Landlock-allowed paths. Falls back to
// $TMPDIR/omnipus_media for tests and ad-hoc invocations.
func TempDir() string {
	if home := os.Getenv("OMNIPUS_HOME"); home != "" {
		return filepath.Join(home, "media")
	}
	return filepath.Join(os.TempDir(), TempDirName)
}

// SessionUploadsDir returns the uploads directory for the given session.
// Files placed here live as long as the session and are cascade-deleted
// when the session is removed (mirroring user-uploaded files).
//
// The returned path is always under $OMNIPUS_HOME/uploads/<sessionID>
// (or $TMPDIR/omnipus_uploads/<sessionID> when OMNIPUS_HOME is unset).
//
// Returns ("", false) when sessionID is empty or contains path-unsafe
// characters (.. / \ null byte). Callers must fall back to TempDir in
// that case.
func SessionUploadsDir(sessionID string) (string, bool) {
	if sessionID == "" {
		return "", false
	}
	// Reject anything that could escape the uploads root or resolve to a
	// dot/hidden directory. Path traversal is blocked by the slash/`..` checks;
	// the leading-dot reject (covers "." and ".hidden") gives parity with
	// session.validateSessionID, which real ULID-form session IDs always satisfy.
	if strings.ContainsAny(sessionID, "/\\\x00") || strings.Contains(sessionID, "..") ||
		strings.HasPrefix(sessionID, ".") {
		return "", false
	}
	var base string
	if home := os.Getenv("OMNIPUS_HOME"); home != "" {
		base = filepath.Join(home, "uploads")
	} else {
		base = filepath.Join(os.TempDir(), "omnipus_uploads")
	}
	return filepath.Join(base, sessionID), true
}

// RemoveSessionUploadsTree removes the uploads directory for every session id
// in ids, cascading a parent session's delete across all of its descendants
// (ADR-057 US-16 / FR-071). Under the pre-ADR-057 world a delegation shared
// its parent's transcript session id, so one delete already caught every
// upload; ADR-057 gives each child its own session id and therefore its own
// uploads directory, which nothing previously reached — this closes that gap.
//
// Each id is resolved independently via SessionUploadsDir, so a path-unsafe
// or empty id is skipped rather than aborting the whole batch, mirroring
// SessionUploadsDir's own ("", false) contract (BDD-79). A descendant that
// never uploaded media (no directory on disk) is not an error — os.RemoveAll
// is already a silent no-op against a path that does not exist.
//
// Errors actually removing an existing, resolvable directory are collected
// and returned via errors.Join rather than swallowed: this primitive's whole
// purpose is to close a silent disk leak (this spec's governing constraint —
// "almost every failure in this migration is success-shaped"), so eating a
// real removal error here would recreate that exact failure shape one layer
// up. One id's failure does not stop the rest of the batch from being
// attempted. RemoveSessionUploadsTree itself never logs; the caller (the
// cascade-delete wiring that calls this primitive with the resolved
// descendant set) decides how a non-nil return is surfaced.
func RemoveSessionUploadsTree(ids []string) error {
	var errs []error
	for _, id := range ids {
		dir, ok := SessionUploadsDir(id)
		if !ok {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			errs = append(errs, fmt.Errorf("media: remove uploads tree for session %q: %w", id, err))
		}
	}
	return errors.Join(errs...)
}
