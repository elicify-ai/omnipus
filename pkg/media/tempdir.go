package media

import (
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
