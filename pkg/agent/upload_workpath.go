// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/elicify-ai/omnipus/pkg/pathsafe"
)

// libraryDirName is the workspace work-tree subdirectory chat uploads are
// dual-written into (library-spec D-1, operator directive 2026-07-29). It
// is a DOT directory — hidden from casual listing, namespaced as
// library-managed rather than mixed in with the agent's own working files
// — but it lives INSIDE work/, so the per-turn os.Root confinement
// (ADR-046) already reaches it with no carve-out: read_file(".library/
// <name>") and the library_list/library_read tools (pkg/tools) both
// resolve through the exact same rooted policy every other file tool uses.
//
// Do NOT filter this directory (or any dotfile) out of a directory listing
// — the whole point of D-1 is that the agent can find what was just
// uploaded; a listing helper that hides dot-entries by convention would
// silently defeat the fix.
const libraryDirName = ".library"

// LibraryDirName returns the workspace work-tree subdirectory chat uploads
// are dual-written into. Exported so pkg/gateway's upload handler and
// pkg/tools' library_list/library_read tools share one literal instead of
// hardcoding ".library" independently in three places and drifting apart.
func LibraryDirName() string {
	return libraryDirName
}

// maxUploadWorkPathEntries bounds the in-process upload-work-path registry
// below so a long-running gateway with heavy upload traffic cannot grow
// this map unboundedly. Entries are evicted oldest-first once the cap is
// hit; a lookup miss on an evicted entry falls back to
// FallbackAnnouncedUploadPath's best-effort plain-name formula (correct in
// the overwhelmingly common no-collision case), so eviction degrades
// gracefully rather than corrupting the announced path.
const maxUploadWorkPathEntries = 4096

// uploadWorkPathRegistry maps a workspace media ref
// ("media://workspace/<ws>/<id>") to the EXACT workspace-relative path
// (".library/<name>", possibly de-duplicated with a numeric " (N)" suffix)
// that the chat-upload REST handler (pkg/gateway/rest.go HandleUpload)
// wrote the raw bytes to inside that SAME workspace's work/ tree (D-1).
//
// It exists to bridge two independent request lifecycles that otherwise
// have no way to share this fact: the upload POST (which alone knows the
// true, collision-resolved destination name — a numeric suffix is only
// decided at write time, based on what else already exists on disk) and
// the later WS chat turn that references the ref (pkg/agent/loop_media.go's
// resolveMediaRefsWithOffload, which synthesizes the "[user uploaded: ...]"
// announcement for D1b).
//
// Bounded FIFO eviction rather than unbounded growth or a TTL: uploads are
// user-driven, not attacker-scaled, and a missed lookup degrades to a
// documented best-effort fallback rather than an error, so a coarse cap is
// sufficient here — this is a convenience cache, not a durability
// guarantee. D2's actual durability requirement (a later turn, or a
// different agent after a handoff, seeing that a file was attached) is met
// by session.TranscriptEntry.Attachments, which is persisted to disk —
// this registry is not, and does not need to be.
type uploadWorkPathRegistry struct {
	mu    sync.Mutex
	byRef map[string]string
	order []string // insertion order, oldest first, for FIFO eviction
}

var globalUploadWorkPaths = &uploadWorkPathRegistry{
	byRef: make(map[string]string),
}

// RecordUploadWorkPath registers the workspace-relative path (e.g.
// ".library/report.pptx") that ref's bytes were ALSO written to inside the
// workspace's work/ tree. Called once, by the chat-upload REST handler,
// immediately after it stages the dual-write copy. A no-op for an empty
// ref or relPath (defensive; callers are not expected to pass either).
func RecordUploadWorkPath(ref, relPath string) {
	if ref == "" || relPath == "" {
		return
	}
	globalUploadWorkPaths.record(ref, relPath)
}

// LookupUploadWorkPath returns the previously-recorded workspace-relative
// path for ref, if one is still in the registry. ok is false when ref was
// never recorded, or was evicted (see maxUploadWorkPathEntries) — callers
// MUST treat that as "no exact record" and fall back to
// FallbackAnnouncedUploadPath instead of skipping the announcement
// entirely.
func LookupUploadWorkPath(ref string) (string, bool) {
	return globalUploadWorkPaths.lookup(ref)
}

func (r *uploadWorkPathRegistry) record(ref, relPath string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byRef[ref]; !exists {
		r.order = append(r.order, ref)
	}
	r.byRef[ref] = relPath
	for len(r.order) > maxUploadWorkPathEntries {
		oldest := r.order[0]
		r.order = r.order[1:]
		delete(r.byRef, oldest)
	}
}

func (r *uploadWorkPathRegistry) lookup(ref string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	relPath, ok := r.byRef[ref]
	return relPath, ok
}

// resetUploadWorkPathRegistryForTest clears the global registry. Test-only
// (unexported) — production code never needs to reset it; tests need
// isolation from whatever a previous test recorded.
func resetUploadWorkPathRegistryForTest() {
	globalUploadWorkPaths.mu.Lock()
	defer globalUploadWorkPaths.mu.Unlock()
	globalUploadWorkPaths.byRef = make(map[string]string)
	globalUploadWorkPaths.order = nil
}

// SanitizeUploadFilename validates and trims a user-supplied filename for
// use as a single path COMPONENT (never a path) inside the workspace's
// .library/ dual-write directory. It duplicates — independently of —
// pkg/media/library's own normalizeFilename validation: pkg/agent does not
// import pkg/media/library, and the D-1 dual-write must not trust another
// layer's validation for its own separate filesystem write (matching the
// same "duplicate the check, do not trust the neighbor" posture
// pkg/media/library/library.go's safeWorkspaceDir already uses for
// pkg/workspace's safeID, for the identical import-cycle reason). Rejects
// empty, over-long, path-separator-bearing, control-character-bearing,
// "."/".." exact matches, or any name STARTING WITH ".." (UAT Issue 6: such
// a name isn't a traversal — it is validated as a single component, never a
// path — but pkg/library's "hidden" heuristic (name begins with a dot) also
// matches it, so an upload named e.g. "..sneaky.pdf" would land, then
// silently vanish from the default Library listing the instant it's
// created. Rejecting it here closes the gap at both dual-write call sites
// (pkg/gateway/rest.go's chat upload and rest_library.go's Library upload,
// the two callers of this shared sanitizer) rather than only at rename.
//
// It also runs the result through pkg/pathsafe.SanitizeRules — the STRICT
// rule set, on every platform (Windows reserved device names, NTFS-illegal
// characters, a trailing dot/space Win32 silently strips, and a
// conservative length cap). Since ADR-067 Stage 0 those rules apply only
// where a Windows filesystem will see the file, so "unconditional" now has
// to be asked for by name; see the comment at the call site for why this
// caller is one of the few that must ask. This replaces the previous local length cap
// (maxUploadFilenameRunes, 256, measured in bytes not runes) with
// pathsafe.MaxComponentNameLength (100, measured in runes) — a 210-rune
// filename used to pass here and is now correctly rejected; see
// pathsafe's doc for the Windows MAX_PATH budget that cap is derived from.
func SanitizeUploadFilename(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("upload filename is empty")
	}
	if strings.ContainsAny(trimmed, `/\`) {
		return "", fmt.Errorf("upload filename must not contain a path separator")
	}
	if trimmed == "." || strings.HasPrefix(trimmed, "..") {
		return "", fmt.Errorf("invalid upload filename %q", trimmed)
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("upload filename contains a control character")
		}
	}
	// SanitizeRules, NOT the package-level ValidateComponent: this call site
	// must stay strict on every platform, and the package-level function is
	// selected by GOOS since ADR-067 Stage 0.
	//
	// Stage 0's argument for relaxing name-shape rules on Linux and macOS is
	// "these are the operator's own files, and we never chose their names".
	// That argument is FALSE here. An upload filename arrives from a remote
	// party — a browser, or an inbound attachment from Discord, Telegram,
	// Feishu or QQ — and Omnipus then creates a file with it. So nothing here
	// may relax, on any platform.
	//
	// pkg/pathsafe names SanitizeRules for exactly this reason and warns that
	// "the most natural implementation of Stage 0 relaxes the sanitizer too,
	// as a side effect". That is precisely what happened: this line kept
	// compiling, kept reading correctly, and quietly started accepting "CON",
	// "a<b.txt", "report." and a 210-rune name on every non-Windows build.
	// pathsafe's own guard test could not see it — it guards
	// SanitizeComponent, which lives in that package; this caller does not.
	if err := pathsafe.SanitizeRules.ValidateComponent(trimmed); err != nil {
		return "", fmt.Errorf("invalid upload filename: %w", err)
	}
	return trimmed, nil
}

// FallbackAnnouncedUploadPath returns the best-effort workspace-relative
// path to announce for a workspace media ref when RecordUploadWorkPath's
// registry has no exact entry for it (a gateway restart between the
// upload and the chat turn, or a ref uploaded before this process started).
// It assumes the common case — no filename collision occurred, so the D-1
// dual-write used the plain sanitized name with no numeric suffix. Returns
// "" (no announcement, rather than a malformed one) for an invalid
// filename.
func FallbackAnnouncedUploadPath(filename string) string {
	sanitized, err := SanitizeUploadFilename(filename)
	if err != nil {
		return ""
	}
	return libraryDirName + "/" + sanitized
}
