// Omnipus — ADR-067 US-16: the lifecycle edges of the knowledge write path.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS, AND WHY IT IS NOT A COPY OF scope.go
//
// scope.go resolves what an agent may READ. It deliberately treats a broken
// mount as contributing nothing:
//
//	"A broken mount contributes nothing and is not an error here: D13 surfaces
//	 broken mounts on the operator's surface, not through an agent's search
//	 call."
//
// That is the right answer for retrieval — a search over a folder that is no
// longer there honestly returns nothing. It is the WRONG answer for a write.
// A create whose collection has vanished must not report "created nothing,
// successfully". US-16's whole point is that the awkward moments are handled
// rather than absorbed, and §12's Lifecycle feature asks for a mount SHOWN as
// broken with an action, not for silence.
//
// So the write path re-asks the question this file answers, at the moment of
// the write rather than at scope-resolution time, and gets one of three
// answers back — active, revoked, broken — each of which the caller can act
// on. Two facts make the re-ask load-bearing rather than belt-and-braces:
//
//   - Scope is resolved at the top of a tool call. An operator can revoke a
//     mount, or rename the collection's folder, between that resolution and
//     the write. That window is small and it is real, and the failure it
//     produces without this check is a write to a path that no longer has a
//     grant behind it.
//   - MOUNT STATE IS COMPUTED FROM THE RECORDED PATH, NEVER FROM A FRESH
//     RESOLUTION. workspace.Mount.HostPath is realpath-resolved once, at
//     create time, and never re-resolved (FR-8.5: a mount is never silently
//     re-bound). Re-resolving here would turn "the operator renamed the
//     folder" into "no grant found", i.e. report REVOKED for something that is
//     BROKEN — and the two carry different operator actions. Broken offers
//     "point it at the new location"; revoked offers nothing, because the
//     operator meant it.
//
// The third edge, eviction, is not about mounts at all. A cloud provider that
// has dematerialised a file leaves a directory entry with a plausible size
// whose contents cannot be read. The failure that matters is not the error —
// it is the NON-error: a read that returns zero bytes for a file whose stat
// says otherwise, which indexes and writes back as an empty note. FR-111
// requires that to fail loudly. ClassifyContentFailure is where "loudly"
// lives.
// ---------------------------------------------------------------------------

// MountState is the live answer to "may this collection be written to right
// now, and if not, why not".
//
// It is deliberately three-valued. A boolean would collapse revoked and
// broken, and those are the two cases whose operator remedies differ.
type MountState string

const (
	// MountStateActive means a grant covering the collection exists and its
	// folder is on disk. Writes may proceed.
	MountStateActive MountState = "active"
	// MountStateRevoked means no grant in this workspace covers the
	// collection any more — the mount record is gone, or never existed for
	// this workspace. There is nothing for the operator to repair; the
	// remedy is to mount it again if that was not intended.
	MountStateRevoked MountState = "revoked"
	// MountStateBroken means the grant is still recorded but its target is
	// not a directory on disk — the classic "I renamed the folder" case
	// (US-16 AS-4). The remedy is to re-point the mount, which is why this is
	// not folded into revoked.
	MountStateBroken MountState = "broken"
)

// Lifecycle sentinels. Every one is returned wrapped inside a LifecycleError,
// so a caller can errors.Is on the class and still read the specific detail.
var (
	// ErrMountRevoked is returned when the workspace no longer holds a grant
	// covering the collection (US-16 AS-2, and the mid-operation revoke).
	ErrMountRevoked = errors.New("knowledge: the mount granting this collection has been revoked")
	// ErrMountBroken is returned when the grant exists but its target folder
	// does not (US-16 AS-4, FR-110).
	ErrMountBroken = errors.New("knowledge: the mount granting this collection is broken")
	// ErrNoteEvicted is returned when a note's bytes are not on local disk —
	// the cloud provider has dematerialised it (US-16 AS-5, FR-111). It is
	// NEVER silently treated as an empty note.
	ErrNoteEvicted = errors.New("knowledge: this note's content is not on disk (evicted by a cloud provider)")
	// ErrNoteUnreadable is returned when a note exists but cannot be read for
	// a reason that is not eviction (permissions, I/O). Separate from
	// eviction because the operator remedies differ.
	ErrNoteUnreadable = errors.New("knowledge: this note cannot be read")
)

// LifecycleError is the typed refusal every lifecycle edge produces.
//
// It carries what an operator or a calling agent needs to act: which
// operation was refused, which collection and path it concerned, what state
// the mount was in, and a sentinel to match on. A bare error string here
// would be a refusal nobody can route.
type LifecycleError struct {
	// Op is the tool or operation that was refused, e.g. "knowledge_create".
	Op string
	// State is the mount state that caused the refusal. Empty for a
	// content-level failure (eviction), which says nothing about the mount.
	State MountState
	// Collection is the operator-facing collection name, empty when the
	// refusal happened before one was resolved.
	Collection string
	// Path is the collection-relative note path, empty when the refusal is
	// not about one particular note.
	Path string
	// Detail is a human-readable specific — the recorded host path, the
	// underlying I/O error — safe to show.
	Detail string
	// Err is the sentinel this error wraps.
	Err error
}

// Error renders the refusal. It always names the collection or the path when
// one is known, because "conflict"-style errors with no subject are not
// actionable in a collection of thousands of notes — the same reasoning
// KnowledgeConflictError's contract gives for requiring a path.
func (e *LifecycleError) Error() string {
	var b strings.Builder
	if e.Op != "" {
		b.WriteString(e.Op)
		b.WriteString(": ")
	}
	if e.Err != nil {
		b.WriteString(e.Err.Error())
	} else {
		b.WriteString("knowledge: lifecycle refusal")
	}
	switch {
	case e.Collection != "" && e.Path != "":
		fmt.Fprintf(&b, " (collection %q, note %q)", e.Collection, e.Path)
	case e.Collection != "":
		fmt.Fprintf(&b, " (collection %q)", e.Collection)
	case e.Path != "":
		fmt.Fprintf(&b, " (note %q)", e.Path)
	}
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	return b.String()
}

// Unwrap exposes the sentinel to errors.Is.
func (e *LifecycleError) Unwrap() error { return e.Err }

// Remedy is the operator action this refusal admits, as a stable machine
// token the UI and the calling agent can branch on. FR-110 requires a broken
// mount to be surfaced "with an action to re-point it"; a free-text message
// is not an action.
func (e *LifecycleError) Remedy() string {
	switch {
	case errors.Is(e.Err, ErrMountBroken):
		return "repoint_mount"
	case errors.Is(e.Err, ErrMountRevoked):
		return "remount_collection"
	case errors.Is(e.Err, ErrNoteEvicted):
		return "materialize_file"
	case errors.Is(e.Err, ErrNoteUnreadable):
		return "check_permissions"
	default:
		return ""
	}
}

// ResolveMountState answers, live, whether workspaceID still holds a usable
// grant covering collectionRoot, and under what name.
//
// collectionRoot is the collection's resolved real path — the identity the
// index and the scope layer both use (D3/FR-031).
//
// The returned name is the operator-facing mount name when a grant was found
// (whatever its state), or WorkTreeOrigin for a collection inside the
// workspace's own work tree, or "" when no grant covers the path at all.
//
// A missing home or workspace id resolves to REVOKED, never to active. Every
// failure path in this function narrows, matching Scope's zero value granting
// nothing: a bug that skips a step can only ever refuse a legitimate write,
// never permit an illegitimate one.
func ResolveMountState(home, workspaceID, collectionRoot string) (MountState, string) {
	home = strings.TrimSpace(home)
	workspaceID = strings.TrimSpace(workspaceID)
	collectionRoot = strings.TrimSpace(collectionRoot)
	if home == "" || workspaceID == "" || collectionRoot == "" {
		return MountStateRevoked, ""
	}

	// The workspace's own work tree (D11). A collection Omnipus created lives
	// here and has no mount record at all, so a mounts-only check would call
	// every Omnipus-authored knowledge base revoked.
	if workDir, err := workspace.SafeWorkDir(home, workspaceID); err == nil {
		if pathWithinOrEqual(collectionRoot, workDir) || pathWithinOrEqual(collectionRoot, resolveOrSelf(workDir)) {
			return collectionDirState(collectionRoot), WorkTreeOrigin
		}
	}

	mounts, ok := workspace.LoadMounts(home, workspaceID)
	if !ok {
		// The mount store could not be trusted (unreadable, malformed). That
		// is not evidence of a grant, so it is not one.
		return MountStateRevoked, ""
	}
	for _, m := range mounts {
		if m.Validate() != nil {
			continue
		}
		// LEXICAL comparison against the RECORDED path — see this file's
		// header. Re-resolving would report a renamed folder as revoked.
		if !pathWithinOrEqual(collectionRoot, m.HostPath) {
			continue
		}
		if workspace.MountStatus(m) != "ok" {
			return MountStateBroken, m.Name
		}
		return collectionDirState(collectionRoot), m.Name
	}
	return MountStateRevoked, ""
}

// collectionDirState reports whether the collection's own folder is still a
// directory. A mount can be perfectly healthy while the collection two levels
// below it has been moved.
func collectionDirState(collectionRoot string) MountState {
	fi, err := os.Stat(collectionRoot)
	if err != nil || !fi.IsDir() {
		return MountStateBroken
	}
	return MountStateActive
}

// pathWithinOrEqual reports whether candidate is root or lies beneath it.
// Both are compared cleaned and separator-normalised; neither is resolved.
func pathWithinOrEqual(candidate, root string) bool {
	if candidate == "" || root == "" {
		return false
	}
	c := filepath.Clean(candidate)
	r := filepath.Clean(root)
	if c == r {
		return true
	}
	return strings.HasPrefix(c, r+string(filepath.Separator))
}

// resolveOrSelf returns the realpath of p, or p unchanged when it cannot be
// resolved. Used only for the work-tree comparison, where the caller-supplied
// root is a path this process just constructed rather than an operator record.
func resolveOrSelf(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(p)
}

// RequireWritableCollection is the gate every mutation passes through.
//
// It returns nil when the collection may be written to, and a *LifecycleError
// otherwise. Callers MUST NOT downgrade a non-nil return into an empty
// success — that is precisely the failure US-16 exists to prevent, and the
// reason this returns an error rather than a bool.
func RequireWritableCollection(op, home, workspaceID string, col ScopedCollection) error {
	state, name := ResolveMountState(home, workspaceID, col.Root)
	if name == "" {
		name = col.Origin
	}
	label := col.Name
	if label == "" {
		label = name
	}
	switch state {
	case MountStateActive:
		return nil
	case MountStateBroken:
		return &LifecycleError{
			Op: op, State: state, Collection: label, Err: ErrMountBroken,
			Detail: fmt.Sprintf("the folder recorded for mount %q is not on disk; point the mount at its new location", name),
		}
	default:
		return &LifecycleError{
			Op: op, State: MountStateRevoked, Collection: label, Err: ErrMountRevoked,
			Detail: "this workspace no longer has a mount covering that folder",
		}
	}
}

// ---------------------------------------------------------------------------
// Eviction (US-16 AS-5, FR-111)
// ---------------------------------------------------------------------------

// ReadNoteContent reads one note's bytes and classifies every way that can go
// wrong into a typed LifecycleError.
//
// It exists because the dangerous case is the one that does not error. A
// dematerialised file on iCloud Drive, OneDrive Files-On-Demand or an rclone
// VFS mount presents a directory entry with the real file's size and returns
// nothing when opened — sometimes with an errno, sometimes with a clean EOF.
// The clean-EOF variant is what FR-111 names: indexed, present, and empty,
// with nothing anywhere saying so. Writing a note back after such a read
// would replace the operator's file with whatever was appended to nothing.
//
// The oracle is the SIZE DISAGREEMENT, not the errno, because the errno is
// platform- and provider-specific and the disagreement is not.
func ReadNoteContent(fsys LinkFS, absPath string) ([]byte, error) {
	if fsys == nil {
		fsys = OSLinkFS()
	}
	fi, err := fsys.Lstat(absPath)
	if err != nil {
		return nil, &LifecycleError{
			Path: absPath, Err: ErrNoteUnreadable, Detail: err.Error(),
		}
	}
	if fi.IsDir() {
		return nil, &LifecycleError{
			Path: absPath, Err: ErrNoteUnreadable, Detail: "is a directory, not a note",
		}
	}
	f, err := fsys.Open(absPath)
	if err != nil {
		return nil, ClassifyContentFailure(absPath, fi.Size(), 0, err)
	}
	defer func() { _ = f.Close() }()

	data, readErr := io.ReadAll(f)
	if cErr := ClassifyContentFailure(absPath, fi.Size(), len(data), readErr); cErr != nil {
		return nil, cErr
	}
	return data, nil
}

// ClassifyContentFailure turns "how a read of a note ended" into either nil
// or a typed LifecycleError.
//
//   - declaredSize is what stat reported.
//   - readBytes is how many bytes actually came back.
//   - readErr is the error the read ended with, nil on a clean EOF.
//
// Exported so the indexer and the write path classify eviction the same way.
// Two independent classifications would drift, and the direction they drift
// in is "one of them starts calling an evicted file empty".
func ClassifyContentFailure(absPath string, declaredSize int64, readBytes int, readErr error) error {
	dataless := declaredSize > 0 && readBytes == 0
	switch {
	case errors.Is(readErr, fs.ErrNotExist):
		// Checked BEFORE the size disagreement, and the order is the
		// requirement rather than style. A file deleted between the stat and
		// the read also disagrees with its own size, so the eviction branch
		// would claim it — and the two carry different operator remedies:
		// "wait for the provider to materialise it" against "it is gone".
		return &LifecycleError{Path: absPath, Err: ErrNoteUnreadable, Detail: "the file no longer exists"}
	case readErr != nil && dataless:
		// Errored AND produced nothing for a file stat says has content.
		return &LifecycleError{
			Path: absPath, Err: ErrNoteEvicted,
			Detail: fmt.Sprintf("stat reports %d bytes but the read returned none: %v", declaredSize, readErr),
		}
	case readErr != nil:
		return &LifecycleError{Path: absPath, Err: ErrNoteUnreadable, Detail: readErr.Error()}
	case dataless:
		// The quiet one, and the reason this function exists: no error at
		// all, and a file that stat says is not empty read as empty.
		return &LifecycleError{
			Path: absPath, Err: ErrNoteEvicted,
			Detail: fmt.Sprintf("stat reports %d bytes but the read returned none and no error — the content is not on local disk", declaredSize),
		}
	default:
		return nil
	}
}
