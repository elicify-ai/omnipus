// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// version.go — optimistic concurrency for writes into an operator's own notes
// (US-14 P0, FR-106, FR-107, FR-108, ADR-067 D14).
//
// # The problem, stated plainly
//
// The reference collection has five writers: Obsidian, Syncthing, git, the `ev`
// CLI, and now Omnipus. Four of them cannot be made to take a lock. So the
// question is not "how do we stop the others writing" — we cannot — it is
// "how does Omnipus know the file changed under it, and what does it do then?"
//
// The answer is a version token and a compare-and-swap. Every read hands back a
// token; every write hands the token back; a write whose token no longer
// matches the file is REFUSED with a typed error naming the path. Never
// overwritten, never merged, never "last writer wins". A lost note is
// undetectable after the fact, which is what makes US-14 a P0.
//
// # Why the token is a content hash and NOT the modification time
//
// D14 and FR-107 say mtime alone is insufficient. That is worth spelling out,
// because the reason is not theoretical:
//
//   - Several filesystems still stamp mtime at 1-second granularity (ext3,
//     HFS+, many FUSE and network mounts, and FAT-family volumes at 2 s). Two
//     writes inside one tick are indistinguishable. On a coarse-granularity
//     volume that window is not small — it is most of a second, every second.
//   - Syncthing PRESERVES the source mtime when it replicates a file. A note
//     that arrives from another machine can have an OLDER mtime than the copy
//     it replaced, so "mtime unchanged" and even "mtime not newer" are both
//     false negatives on a real, common setup.
//   - `git checkout`, `rsync -a`, `cp -p` and restore-from-backup all set mtime
//     to a value that says nothing about when the content became what it is.
//
// So the hash is the decision. The corollary matters just as much and is the
// half implementations usually get wrong: mtime is NOT mixed INTO the token
// either. A token that moved when mtime moved would refuse a write after a
// bare `touch`, a `git checkout` that restored identical bytes, or a backup
// restore — refusing to save an agent's work to protect content that was never
// at risk, because nothing changed. False conflicts are not the safe direction;
// they train the operator to reach for a force flag, and then the real conflict
// is force-flagged too. Detection power comes from the hash; mtime adds none
// (an external change the hash misses is a SHA-256 collision, which mtime would
// not reliably catch either) and subtracts precision. Size is folded in because
// it is free and it makes a truncated digest strictly harder to collide.
//
// mtime and size are still READ and still carried on NoteVersion — as a cheap
// pre-filter and for display — they are simply not the decision. That is
// exactly D14's "mtime/size are a fast pre-filter; the hash is the decision".
//
// # Why a lock is still needed
//
// A compare-and-swap that reads the token, compares it and then writes is not
// atomic on its own: two Omnipus writers can both read the same token, both
// pass the comparison, and both write — and one write is lost, silently, which
// is the precise failure US-14 exists to prevent. The comparison and the write
// therefore happen inside a lock (D14 tier 1: in-process striped mutex +
// advisory file lock + atomic temp-and-rename). Tier 3 writers — Obsidian,
// Syncthing, git, an editor — take no lock and are caught by the token
// comparison instead. Tier 2, coordinating with `ev`, is deferred to stage 4.
//
// Platform posture, stated rather than assumed: fileutil.WithFlock is a
// documented no-op on Windows, so the cross-process half of tier 1 is
// POSIX-only, exactly as it is for pkg/entity (ADR-054 §5). On Windows the
// in-process striped mutex still serialises writers inside one gateway, and the
// version token still catches every writer outside it — including a second
// Omnipus process. Windows loses mutual exclusion, not loss-detection.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// VersionToken is an opaque identifier for one exact state of one note.
//
// OPAQUE IS A CONTRACT, not a suggestion (see KnowledgeConflictError.yaml):
// callers never parse it, never order it, never construct one. The encoding
// below is free to change without a contract change precisely because nothing
// outside this file may depend on it.
type VersionToken string

const (
	// versionTokenPrefix versions the ENCODING, so a future change of hash or
	// layout is distinguishable from a corrupt token rather than being
	// mistaken for one.
	versionTokenPrefix = "v1:"

	// versionTokenHexLen is how much of the SHA-256 digest the token carries:
	// 32 hex characters, i.e. 128 bits. Full-length would be no more correct
	// and twice as long in every log line and wire payload. 128 bits is far
	// beyond the point where accidental collision between two versions of one
	// note is a consideration, and this is not an adversarial primitive — the
	// operator's other editors are not trying to forge a token.
	versionTokenHexLen = 32

	// TokenAbsent is the token a caller sends to say "I believe this note does
	// not exist". It makes CREATE a compare-and-swap too, which matters: two
	// agents creating the same note at the same moment is the same lost-write
	// bug as two agents editing it, and without an absent-token it would be the
	// one case that silently overwrote.
	TokenAbsent VersionToken = versionTokenPrefix + "absent"
)

// DefaultLockBound is how long a write waits for the note's lock before giving
// up with a loud error (FR-108). Five seconds, matching D14 and the
// boltOpenTimeout precedent in pkg/memrooms/index.
//
// The requirement is "bound it and error", not "wait long enough" — an
// operation that hangs on a lock is indistinguishable, from the outside, from
// an agent that has stopped working.
const DefaultLockBound = 5 * time.Second

// lockPollInterval is how often a bounded acquire re-tries the in-process
// mutex. Short enough that the wait is not perceptibly longer than the
// contention, long enough not to spin a core.
const lockPollInterval = 2 * time.Millisecond

// Sentinel errors for the write path.
var (
	// ErrVersionConflict is the class behind *ConflictError, so a caller that
	// only needs "was this a conflict?" can use errors.Is (FR-106).
	ErrVersionConflict = errors.New("knowledge: note changed on disk since it was read")

	// ErrLockTimeout is the class behind *LockTimeoutError (FR-108).
	ErrLockTimeout = errors.New("knowledge: timed out waiting for the note lock")

	// ErrNotRegularFile means the target path exists but is a directory, a
	// symlink or a device. Writing through a symlink is refused outright: a
	// symlink inside the collection can point anywhere on the disk, and
	// following one would turn a contained write into an arbitrary one
	// (FR-043, FR-044).
	ErrNotRegularFile = errors.New("knowledge: path is not a regular file")

	// ErrWriterMisconfigured means NewWriter was given an unusable
	// configuration. Returned at construction so a misconfigured writer cannot
	// exist.
	ErrWriterMisconfigured = errors.New("knowledge: writer configuration is incomplete")
)

// ConflictError is the typed refusal of a write whose version token no longer
// matches the file (FR-106). It mirrors contracts/components/schemas/
// KnowledgeConflictError.yaml field for field — see Wire.
//
// It NAMES THE PATH. "Conflict" without a path is not actionable in a
// collection of several thousand notes, and the caller's next move — re-read,
// merge, retry with ActualVersion — is impossible without it.
type ConflictError struct {
	// Path is the collection-relative path that was NOT written.
	Path string
	// Expected is the token the caller sent. Empty when it sent none, which is
	// itself a refusal: FR-106 requires a token on every write.
	Expected VersionToken
	// Actual is the token of the file as it now stands. Empty when the file no
	// longer exists, matching the contract's "absent when the file has been
	// deleted since".
	Actual VersionToken
}

// Error implements error.
func (e *ConflictError) Error() string {
	switch {
	case e.Expected == "":
		return fmt.Sprintf("knowledge: %s: a write must carry the version token it read (FR-106)", e.Path)
	case e.Actual == "":
		return fmt.Sprintf("knowledge: %s changed on disk since you opened it: it has been deleted", e.Path)
	default:
		return fmt.Sprintf("knowledge: %s changed on disk since you opened it", e.Path)
	}
}

// Unwrap exposes the sentinel so errors.Is(err, ErrVersionConflict) works for
// every shape of conflict, including the missing-token one.
func (e *ConflictError) Unwrap() error { return ErrVersionConflict }

// ConflictCode is the machine-readable discriminator carried by every
// KnowledgeConflictError body. A single value, so a client branches on it
// without matching on the message text.
const ConflictCode = string(generated.KnowledgeVersionConflict)

// Wire renders the conflict as the generated contract type (Hard Constraint
// #8). It lives here rather than in the gateway so there is exactly one place
// that decides which fields are present — in particular, that an empty token
// is OMITTED rather than sent as "".
func (e *ConflictError) Wire() generated.KnowledgeConflictError {
	body := generated.KnowledgeConflictError{
		Error: e.Error(),
		Code:  generated.KnowledgeVersionConflict,
		Path:  e.Path,
	}
	if e.Expected != "" {
		expected := string(e.Expected)
		body.ExpectedVersion = &expected
	}
	if e.Actual != "" {
		actual := string(e.Actual)
		body.ActualVersion = &actual
	}
	return body
}

// LockTimeoutError reports that a write gave up waiting for a note's lock
// (FR-108). It names the path and the bound so the operator can tell "this is
// contended" from "this is wedged".
type LockTimeoutError struct {
	Path  string
	Bound time.Duration
}

// Error implements error.
func (e *LockTimeoutError) Error() string {
	return fmt.Sprintf("knowledge: %s: gave up waiting %s for the note lock", e.Path, e.Bound)
}

// Unwrap exposes the sentinel.
func (e *LockTimeoutError) Unwrap() error { return ErrLockTimeout }

// NoteVersion is the state of one note at one moment.
type NoteVersion struct {
	// Path is the collection-relative, slash-separated path.
	Path string
	// Exists is false when there is no file there. Token is then TokenAbsent.
	Exists bool
	// Token is the opaque version token (TokenAbsent when Exists is false).
	Token VersionToken
	// Size is the file's size in bytes. Carried for display and as a cheap
	// pre-filter — never as the decision.
	Size int64
	// ModTime is the file's modification time. Same status as Size: carried,
	// never decisive (FR-107).
	ModTime time.Time
}

// ComputeVersionToken derives the token for a note's content.
//
// The digest covers the bytes and then an 8-byte little-endian length suffix.
// The length suffix is domain separation: without it, a truncated digest over
// content alone is very slightly easier to collide across contents of different
// lengths, and folding the length in costs eight bytes of hashing.
func ComputeVersionToken(content []byte) VersionToken {
	h := sha256.New()
	_, _ = h.Write(content)
	return finishVersionToken(h, int64(len(content)))
}

// computeVersionTokenFrom streams r, so a 50 MB note (H-7) is tokenised in
// constant memory rather than being read whole into the heap.
func computeVersionTokenFrom(r io.Reader) (VersionToken, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", 0, err
	}
	return finishVersionToken(h, n), n, nil
}

// finishVersionToken appends the length suffix and encodes.
func finishVersionToken(h hash.Hash, size int64) VersionToken {
	var lenBuf [8]byte
	binary.LittleEndian.PutUint64(lenBuf[:], uint64(size)) //nolint:gosec // size is never negative
	_, _ = h.Write(lenBuf[:])
	return VersionToken(versionTokenPrefix + hex.EncodeToString(h.Sum(nil))[:versionTokenHexLen])
}

// ReadNoteVersion reports the current version of one note inside a collection.
//
// A missing file is NOT an error: it returns Exists=false and TokenAbsent, so a
// caller that intends to create the note has a token to send back and the
// create is a compare-and-swap like any other write.
//
// A path that is not a regular file — a directory, or a symlink of any kind —
// is an error. The symlink case is deliberate and is not about tidiness: a
// symlink inside the collection can point at anything on the disk, and reading
// or writing through one converts a contained operation into an uncontained
// one (FR-043, FR-044).
func ReadNoteVersion(c *Collection, rel string) (NoteVersion, error) {
	if c == nil {
		return NoteVersion{}, fmt.Errorf("%w: collection", ErrWriterMisconfigured)
	}
	cleaned, err := library.CleanRelPath(rel)
	if err != nil {
		return NoteVersion{}, fmt.Errorf("%w: %q: %v", ErrOutsideCollection, rel, err)
	}
	abs, err := c.ResolveInside(cleaned)
	if err != nil {
		return NoteVersion{}, err
	}
	return readNoteVersionAbs(cleaned, abs)
}

// readNoteVersionAbs is the resolved-path half, shared with the write path so
// that the token compared under the lock is produced by exactly the same code
// as the token handed out by a read.
func readNoteVersionAbs(rel, abs string) (NoteVersion, error) {
	info, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return NoteVersion{Path: rel, Exists: false, Token: TokenAbsent}, nil
	case err != nil:
		return NoteVersion{}, fmt.Errorf("knowledge: stat %q: %w", rel, err)
	case !info.Mode().IsRegular():
		return NoteVersion{}, fmt.Errorf("%w: %q is %s", ErrNotRegularFile, rel, info.Mode().Type())
	}

	f, err := os.Open(abs) //nolint:gosec // abs is contained by Collection.ResolveInside
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return NoteVersion{Path: rel, Exists: false, Token: TokenAbsent}, nil
		}
		return NoteVersion{}, fmt.Errorf("knowledge: open %q: %w", rel, err)
	}
	defer func() { _ = f.Close() }()

	token, size, err := computeVersionTokenFrom(f)
	if err != nil {
		return NoteVersion{}, fmt.Errorf("knowledge: read %q: %w", rel, err)
	}
	return NoteVersion{
		Path:    rel,
		Exists:  true,
		Token:   token,
		Size:    size,
		ModTime: info.ModTime(),
	}, nil
}

// LockDirFor is where a collection's per-note lock files live: under
// $OMNIPUS_HOME, alongside the index, NOT inside the operator's collection.
//
// Outside the collection on purpose. A lock file inside the vault would be
// replicated by Syncthing to other machines (where it means nothing and where a
// stale one would be actively misleading), shown by Obsidian's file explorer,
// and committed by anyone running `git add -A`. FR-030 already puts the index
// outside for the same family of reasons.
func LockDirFor(home, collectionRoot string) (string, error) {
	dir, err := IndexDirFor(home, collectionRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "locks"), nil
}

// noteWriteLocks is the PROCESS-WIDE in-process half of D14 tier 1.
//
// Package-level, not per-Writer, and that is the whole point: two Writer values
// in one gateway — one held by the REST handler, one by a tool — must exclude
// each other. A per-Writer lock would look identical in every single-writer
// test and provide nothing in production.
//
//nolint:gochecknoglobals // deliberately process-wide; see above.
var noteWriteLocks = &task.StripedLock{}

// WriterConfig configures a Writer.
type WriterConfig struct {
	// Collection is the knowledge base being written to. Required.
	Collection *Collection
	// LockDir is where per-note lock files live — LockDirFor. Required.
	LockDir string
	// Auditor records every mutation and every refusal. Required: FR-090 is
	// enforced by making an unaudited writer unconstructible.
	Auditor *Auditor
	// Actor is who the writes are attributed to. At least one of AgentID and
	// User must be set — a knowledge base cannot be mutated anonymously
	// (US-15 AS-1).
	Actor Actor
	// WorkspaceID is recorded on each audit entry when known. Optional.
	WorkspaceID string
	// LockBound is how long to wait for a lock before erroring. Zero means
	// DefaultLockBound (FR-108).
	LockBound time.Duration
}

// Writer performs compare-and-swap writes into one collection.
//
// Every mutating method: takes the note's lock (bounded), re-reads the note's
// version INSIDE the lock, compares it with the token the caller supplied,
// writes atomically, and records an audit entry — including when it refuses.
type Writer struct {
	col         *Collection
	lockDir     string
	auditor     *Auditor
	actor       Actor
	workspaceID string
	bound       time.Duration
}

// NewWriter validates the configuration and returns a Writer.
//
// Every requirement here is a requirement that would otherwise have to be
// re-checked on every call and would eventually be missed on one: no auditor
// means FR-090 cannot hold, no actor means US-15 AS-1 cannot hold, no lock dir
// means tier-1 mutual exclusion cannot hold.
func NewWriter(cfg WriterConfig) (*Writer, error) {
	if cfg.Collection == nil {
		return nil, fmt.Errorf("%w: collection", ErrWriterMisconfigured)
	}
	if cfg.Auditor == nil || cfg.Auditor.sink == nil {
		return nil, ErrAuditUnavailable
	}
	if !cfg.Actor.named() {
		return nil, fmt.Errorf("%w: actor (a knowledge base cannot be mutated anonymously)", ErrWriterMisconfigured)
	}
	if strings.TrimSpace(cfg.LockDir) == "" {
		return nil, fmt.Errorf("%w: lock directory", ErrWriterMisconfigured)
	}
	bound := cfg.LockBound
	if bound <= 0 {
		bound = DefaultLockBound
	}
	return &Writer{
		col:         cfg.Collection,
		lockDir:     cfg.LockDir,
		auditor:     cfg.auditorOrNil(),
		actor:       cfg.Actor,
		workspaceID: strings.TrimSpace(cfg.WorkspaceID),
		bound:       bound,
	}, nil
}

// auditorOrNil exists only so NewWriter reads as one expression; the nil case
// is already refused above.
func (cfg WriterConfig) auditorOrNil() *Auditor { return cfg.Auditor }

// Collection returns the collection this writer is bound to.
func (w *Writer) Collection() *Collection { return w.col }

// LockBound returns the configured lock wait bound (FR-108).
func (w *Writer) LockBound() time.Duration { return w.bound }

// WriteRequest is one compare-and-swap write.
type WriteRequest struct {
	// Path is the collection-relative, slash-separated note path.
	Path string
	// Content is the complete new content of the note.
	Content []byte
	// ExpectedVersion is the token the caller read. TokenAbsent means "I
	// believe this note does not exist yet". Empty is refused (FR-106).
	ExpectedVersion VersionToken
	// Operation overrides the audited event name. Empty picks
	// EventKnowledgeNoteCreate or EventKnowledgeNoteWrite from what was found
	// on disk.
	Operation string
	// Reason is an optional short token recorded on the audit entry.
	Reason string
	// Details are optional small scalars for the audit entry.
	Details map[string]any
}

// WriteResult describes an applied write.
type WriteResult struct {
	// Path is the collection-relative path written.
	Path string
	// Version is the token of the note as it now stands — send this back on
	// the next write.
	Version VersionToken
	// Size is the number of bytes written.
	Size int64
	// Created is true when the note did not exist before this write.
	Created bool
}

// WriteNote applies a compare-and-swap write, or refuses it.
//
// The order is the requirement, not an implementation detail:
//
//  1. resolve and contain the path (a refusal here never touches disk);
//  2. take the note's lock, bounded (FR-108);
//  3. re-read the note's CURRENT version inside the lock — the token the caller
//     sent is compared against what is on disk NOW, not against anything
//     cached, because the whole point is to catch a writer we never saw;
//  4. refuse on mismatch with a typed *ConflictError naming the path (FR-106);
//  5. write atomically (temp in the same directory, then rename);
//  6. audit the outcome, whichever it was (FR-090).
//
// A refusal leaves the file on disk byte-for-byte unchanged. An audit failure
// does not undo a completed write — it cannot — but it is joined onto the
// returned error rather than swallowed, so a caller is never told a mutation
// succeeded and audited when it was only the first of those.
func (w *Writer) WriteNote(req WriteRequest) (WriteResult, error) {
	if w == nil {
		return WriteResult{}, fmt.Errorf("%w: writer", ErrWriterMisconfigured)
	}

	raw := strings.TrimSpace(req.Path)
	cleaned, err := library.CleanRelPath(raw)
	if err != nil || cleaned == "" {
		// The refused path is still recorded, so an operator can see WHAT was
		// refused. normalizeAuditPaths drops empty strings, so an empty request
		// path needs a stand-in or the refusal would fail validation and go
		// unrecorded — which is the exact FR-090 gap this file exists to close.
		refused := raw
		if refused == "" {
			refused = "(empty path)"
		}
		outside := fmt.Errorf("%w: %q", ErrOutsideCollection, req.Path)
		return WriteResult{}, w.joinAudit(outside, Mutation{
			Operation: w.operationOr(req.Operation, EventKnowledgeNoteWrite),
			Outcome:   MutationRefused,
			Paths:     []string{refused},
			Reason:    "outside_collection",
			Details:   req.Details,
		})
	}
	abs, err := w.col.ResolveInside(cleaned)
	if err != nil {
		return WriteResult{}, w.joinAudit(err, Mutation{
			Operation: w.operationOr(req.Operation, EventKnowledgeNoteWrite),
			Outcome:   MutationRefused,
			Paths:     []string{cleaned},
			Reason:    "outside_collection",
			Details:   req.Details,
		})
	}

	var result WriteResult
	lockErr := w.withNoteLock(cleaned, func() error {
		current, err := readNoteVersionAbs(cleaned, abs)
		if err != nil {
			return w.joinAudit(err, Mutation{
				Operation: w.operationOr(req.Operation, EventKnowledgeNoteWrite),
				Outcome:   MutationFailed,
				Paths:     []string{cleaned},
				Reason:    "read_current_version",
				Details:   req.Details,
			})
		}

		operation := w.operationOr(req.Operation, defaultOperationFor(current.Exists))

		if conflict := checkVersion(cleaned, req.ExpectedVersion, current); conflict != nil {
			details := mergeDetails(req.Details, map[string]any{
				"expected_version": string(conflict.Expected),
				"actual_version":   string(conflict.Actual),
			})
			return w.joinAudit(conflict, Mutation{
				Operation: operation,
				Outcome:   MutationRefused,
				Paths:     []string{cleaned},
				Reason:    "version_conflict",
				Details:   details,
			})
		}

		if hookBeforeApplyWrite != nil {
			hookBeforeApplyWrite()
		}

		written, writeErr := writeFileAtomicPreservingMode(abs, req.Content, current.Exists)
		if writeErr != nil {
			return w.joinAudit(writeErr, Mutation{
				Operation: operation,
				Outcome:   MutationFailed,
				Paths:     []string{cleaned},
				Reason:    "write_failed",
				Details:   req.Details,
			})
		}

		result = WriteResult{
			Path:    cleaned,
			Version: ComputeVersionToken(req.Content),
			Size:    written,
			Created: !current.Exists,
		}
		details := mergeDetails(req.Details, map[string]any{
			"bytes":   written,
			"created": result.Created,
			"version": string(result.Version),
		})
		return w.audit(Mutation{
			Operation: operation,
			Outcome:   MutationApplied,
			Paths:     []string{cleaned},
			Reason:    req.Reason,
			Details:   details,
		})
	})
	if lockErr != nil {
		if errors.Is(lockErr, ErrLockTimeout) {
			return WriteResult{}, w.joinAudit(lockErr, Mutation{
				Operation: w.operationOr(req.Operation, EventKnowledgeNoteWrite),
				Outcome:   MutationRefused,
				Paths:     []string{cleaned},
				Reason:    "lock_timeout",
				Details:   req.Details,
			})
		}
		return WriteResult{}, lockErr
	}
	return result, nil
}

// hookBeforeApplyWrite runs between the version comparison and the write, and
// is nil in production.
//
// It exists because the bug this file prevents — two writers that both read the
// same token, both pass the comparison, and both write — has a race window
// measured in microseconds. A concurrency test that does not widen that window
// passes just as happily against an implementation with no lock at all, which
// is the false green this seam removes. Same reasoning, and same shape, as
// pkg/session's sessionLockAcquireFn seams (ADR-057 FR-101).
//
//nolint:gochecknoglobals // a test seam, nil in production.
var hookBeforeApplyWrite func()

// checkVersion is the compare half of compare-and-swap. It returns nil when the
// write may proceed and a *ConflictError otherwise.
//
// Four refusals, and each is a real event:
//
//   - no token at all — FR-106 requires one on every write. Accepting a write
//     with no token would make the whole mechanism opt-in, and every caller
//     that forgot would silently be back to last-writer-wins.
//   - the note exists and its token differs — someone wrote it (US-14 AS-1).
//   - the caller expected an absent note and one is there — two creators of the
//     same note; without this, the second silently overwrites the first.
//   - the caller expected a note and it is gone — the file was deleted or moved
//     underneath us. Recreating it silently would resurrect a note the operator
//     deleted.
func checkVersion(rel string, expected VersionToken, current NoteVersion) *ConflictError {
	actual := current.Token
	if !current.Exists {
		// The contract omits actual_version when the file is gone.
		actual = ""
	}
	if strings.TrimSpace(string(expected)) == "" {
		return &ConflictError{Path: rel, Expected: "", Actual: actual}
	}
	if current.Exists {
		if expected == current.Token {
			return nil
		}
		return &ConflictError{Path: rel, Expected: expected, Actual: current.Token}
	}
	if expected == TokenAbsent {
		return nil
	}
	return &ConflictError{Path: rel, Expected: expected, Actual: ""}
}

// defaultOperationFor picks the audited event name from what was on disk.
func defaultOperationFor(exists bool) string {
	if exists {
		return EventKnowledgeNoteWrite
	}
	return EventKnowledgeNoteCreate
}

// operationOr uses the caller's operation name when it supplied one.
func (w *Writer) operationOr(supplied, fallback string) string {
	if s := strings.TrimSpace(supplied); s != "" {
		return s
	}
	return fallback
}

// Audit records a mutation this writer performed, filling in the actor,
// collection and workspace. Exported so the rename/journal path (which touches
// many files in one operation, US-15 AS-2) records through exactly the same
// code as a single write, rather than assembling its own entry.
func (w *Writer) Audit(m Mutation) error { return w.audit(m) }

// audit fills in the writer-owned fields and records.
func (w *Writer) audit(m Mutation) error {
	m.Actor = w.actor
	m.CollectionRoot = w.col.Root()
	m.WorkspaceID = w.workspaceID
	return w.auditor.Record(m)
}

// joinAudit records m and returns opErr with any audit failure joined onto it.
//
// Joined, never swallowed: FR-090 makes the audit record part of the
// operation's contract, so "the write was refused" and "the refusal was not
// recorded" are two separate facts the caller needs. errors.Is still finds
// ErrVersionConflict (or ErrLockTimeout) through the join, so conflict handling
// is unaffected.
func (w *Writer) joinAudit(opErr error, m Mutation) error {
	if auditErr := w.audit(m); auditErr != nil {
		return errors.Join(opErr, auditErr)
	}
	return opErr
}

// mergeDetails overlays extra onto base without mutating either.
func mergeDetails(base, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// withNoteLock runs fn holding both halves of D14 tier 1 for one note, and
// gives up with a *LockTimeoutError once the bound elapses (FR-108).
//
// It is a thin adapter onto WithNoteWriteLock. There is deliberately ONE
// implementation of the tier-1 lock in this package: the cross-process test
// that proves AC-14.1 must prove it for every write path, and it can only do
// that if every write path takes the same lock through the same code. Two
// lock implementations would mean the tested one and the used one, which is
// the exact defect this file's verifier found in the first cut of stage 3.
func (w *Writer) withNoteLock(rel string, fn func() error) error {
	return WithNoteWriteLock(NoteLockConfig{
		CollectionRoot: w.col.Root(),
		LockDir:        w.lockDir,
		Bound:          w.bound,
	}, rel, fn)
}

// NoteLockConfig says which note population a lock belongs to and how long a
// writer may wait for it.
type NoteLockConfig struct {
	// CollectionRoot namespaces the in-process striped mutex, so two
	// collections that happen to contain a note with the same relative path
	// do not serialise against each other.
	CollectionRoot string

	// LockDir is where the per-note advisory lock files live — LockDirFor,
	// under $OMNIPUS_HOME and never inside the operator's collection.
	//
	// EMPTY IS A DEGRADED MODE, NOT A DISABLED ONE. The in-process striped
	// mutex is always taken; only the cross-process advisory lock is skipped.
	// A caller with no $OMNIPUS_HOME (a unit test, a direct library call)
	// therefore still gets mutual exclusion inside its own process, which is
	// what makes the single-process silent-loss defect impossible; it does
	// NOT get D14 tier 1's cross-process half, and must not be described as
	// if it does.
	LockDir string

	// Bound is the lock wait bound (FR-108). Zero means DefaultLockBound.
	Bound time.Duration
}

func (c NoteLockConfig) bound() time.Duration {
	if c.Bound > 0 {
		return c.Bound
	}
	return DefaultLockBound
}

// WithNoteWriteLock runs fn holding D14 tier 1's mutual exclusion for one
// note, and gives up with a *LockTimeoutError once the bound elapses (FR-108).
//
// Both halves, in order:
//
//	in-process striped mutex — serialises writers inside one gateway process.
//	advisory file lock       — serialises writers across OS processes (POSIX;
//	                           a documented no-op on Windows, see the header).
//
// The mutex is acquired first so that the common case — contention inside one
// process — never reaches the filesystem at all.
//
// This is THE write-safety layer author.go's header refers to. Every mutating
// path in this package runs inside it: Writer.WriteNote, author.CreateNote,
// author.EditNote and the journal's per-step rewrite. A write path that does
// not is a write path where two callers can both be told they succeeded and
// only one survive (US-14 AS-2).
func WithNoteWriteLock(cfg NoteLockConfig, rel string, fn func() error) error {
	bound := cfg.bound()
	deadline := time.Now().Add(bound)

	mu := noteWriteLocks.Get(noteLockKey(cfg.CollectionRoot, rel))
	if !acquireMutexByDeadline(mu, deadline) {
		return &LockTimeoutError{Path: rel, Bound: bound}
	}
	defer mu.Unlock()

	if strings.TrimSpace(cfg.LockDir) == "" {
		return fn()
	}
	lockPath, err := noteLockPathFor(cfg.LockDir, rel)
	if err != nil {
		return err
	}
	return withFileLockByDeadline(lockPath, rel, bound, deadline, fn)
}

// noteLockKey is the striped-mutex key. It includes the collection root so two
// collections that happen to contain a note with the same relative path do not
// serialise against each other.
func noteLockKey(collectionRoot, rel string) string {
	return collectionRoot + "\x00" + path.Clean(rel)
}

// noteLockPathFor is the advisory lock file for one note.
//
// The name is a digest of the relative path rather than the path itself: note
// names contain slashes, colons and characters that are legal in a note name
// and illegal in a filename on some platform or other, and a lock file that
// cannot be created is a lock that is silently not taken.
func noteLockPathFor(lockDir, rel string) (string, error) {
	if err := os.MkdirAll(lockDir, markerDirPerm); err != nil {
		return "", fmt.Errorf("knowledge: create lock directory: %w", err)
	}
	sum := sha256.Sum256([]byte(path.Clean(rel)))
	return filepath.Join(lockDir, hex.EncodeToString(sum[:])[:32]+".lock"), nil
}

// acquireMutexByDeadline takes mu, or reports false once the deadline passes.
//
// sync.Mutex has TryLock but no timed Lock, so this polls. Polling is the right
// shape here anyway: the bound exists to convert a hang into an error, and the
// wait it bounds is a file write, so a couple of milliseconds of granularity is
// invisible.
func acquireMutexByDeadline(mu *sync.Mutex, deadline time.Time) bool {
	for {
		if mu.TryLock() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(lockPollInterval)
	}
}

// withFileLockByDeadline runs fn while holding the advisory lock on lockPath,
// or returns *LockTimeoutError if the lock cannot be taken within the deadline.
//
// fileutil.WithFlock blocks indefinitely (LOCK_EX with no LOCK_NB), which is
// precisely what FR-108 forbids. Rather than fork a second flock helper into
// this package — which would need per-platform build tags this file cannot
// have — the blocking acquire runs on its own goroutine and the DEADLINE is
// applied here:
//
//   - if the lock arrives in time, fn runs on the calling goroutine, inside the
//     lock, and the helper goroutine is released afterwards;
//   - if the deadline passes first, `release` is closed immediately. The helper
//     goroutine is still parked in flock; when the lock eventually frees it
//     acquires, finds `release` already closed, and returns at once, releasing.
//     It never runs fn and never holds the lock for work.
//
// So the timeout costs one parked goroutine until the current holder finishes,
// and nothing else. It cannot leak past that, and it cannot run fn twice.
func withFileLockByDeadline(lockPath, rel string, bound time.Duration, deadline time.Time, fn func() error) error {
	acquired := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- fileutil.WithFlock(lockPath, func() error {
			close(acquired)
			<-release
			return nil
		})
	}()

	wait := time.Until(deadline)
	if wait < 0 {
		wait = 0
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-acquired:
	case <-timer.C:
		close(release)
		return &LockTimeoutError{Path: rel, Bound: bound}
	case err := <-done:
		// WithFlock failed before acquiring (could not open the lock file).
		close(release)
		if err == nil {
			err = errors.New("knowledge: lock helper exited without acquiring")
		}
		return fmt.Errorf("knowledge: lock %q: %w", rel, err)
	}

	fnErr := fn()
	close(release)
	if lockErr := <-done; lockErr != nil {
		return errors.Join(fnErr, fmt.Errorf("knowledge: release lock %q: %w", rel, lockErr))
	}
	return fnErr
}

// writeFileAtomicPreservingMode writes content to abs via a temp file in the
// SAME directory and a rename (D14: "atomic temp + rename in the target
// folder").
//
// Same directory because rename is only atomic within a filesystem, and a
// collection can easily be a mount point, a network share or a synced folder
// distinct from wherever the OS puts temporary files.
//
// The temp name is dot-prefixed so that, in the microseconds it exists, neither
// Obsidian's file explorer nor Omnipus's own indexer treats it as a note.
//
// An existing note keeps its permission bits — the operator's files stay the
// operator's shape. A new note is created 0600, the same posture the rest of
// this package takes towards files it creates.
func writeFileAtomicPreservingMode(abs string, content []byte, exists bool) (int64, error) {
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, markerDirPerm); err != nil {
		return 0, fmt.Errorf("knowledge: create %q: %w", dir, err)
	}

	perm := markerFilePerm
	if exists {
		if info, err := os.Lstat(abs); err == nil && info.Mode().IsRegular() {
			perm = info.Mode().Perm()
		}
	}

	tmp, err := os.CreateTemp(dir, ".omnipus-write-*.tmp")
	if err != nil {
		return 0, fmt.Errorf("knowledge: create temp file in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	n, err := tmp.Write(content)
	if err != nil {
		cleanup()
		return 0, fmt.Errorf("knowledge: write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return 0, fmt.Errorf("knowledge: chmod temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return 0, fmt.Errorf("knowledge: sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return 0, fmt.Errorf("knowledge: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, abs); err != nil {
		_ = os.Remove(tmpName)
		return 0, fmt.Errorf("knowledge: rename temp file over %q: %w", abs, err)
	}
	syncDir(dir)
	return int64(n), nil
}

// syncDir fsyncs a directory so a rename survives a power loss. Best effort:
// several platforms and filesystems refuse to open a directory for this, and a
// note that is durable-on-next-sync is not worth failing an otherwise good
// write over.
func syncDir(dir string) {
	d, err := os.Open(dir) //nolint:gosec // dir is the parent of a contained path
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
