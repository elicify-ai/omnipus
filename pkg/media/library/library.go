// Package library — workspace-scoped media library (ADR-051 Rev 4).
//
// The library is the workspace's persistent storage for raw media bytes
// plus an in-memory manifest. The on-disk shape is manifest.json with
// every entry verified against its sha256 on every read (integrity is
// load-bearing — a tamper detection hard-fails Read and returns nil
// bytes).
//
// Internal invariant (Wave 1 TD-M1 / TD-M2):
//
//   - All required domain facts (id, workspace_id, filename, mime,
//     size, sha256, uploaded_at, source, refcount, last_refcount_seen_at)
//     live on the private manifestEntry type as required values, not
//     pointers. gen.MediaLibraryEntry is a wire projection only — it is
//     built from manifestEntry at the API edge (List, Read, Delete) and
//     decoded from the persisted JSON during Load.
//   - Refcount lives on manifestEntry and ONLY on manifestEntry.
//     There is no parallel map[string]int. Read/Write order is one
//     location, so the prior bug class (entry.Refcount vs refcounts[id]
//     disagreeing after Load) cannot occur.
//   - LastRefcountSeenAt is required (Wave 1 TD-M2).
//
// Package-private lifecycle:
//
//   - New() loads automatically.
//   - Load() / Store() are package-private — only New() and the
//     mutator methods (Upload, Delete, CascadeDelete, OrphanGC,
//     IncrementRefcount, DecrementRefcount) are public. The mutator
//     methods persist transactionally, so there is no legitimate
//     external caller for a standalone Store.
//
// Test-only source:
//
//   - gen.MediaLibraryEntrySource's test_fixture is reserved for
//     in-process fixture uploads used by tests; never emitted by the
//     live upload path. Upload's source argument is still typed
//     gen.MediaLibraryEntrySource for back-compat, but a test-only
//     helper (UploadFixture) is the recommended path.
package library

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
)

// safeWorkspaceDir mirrors pkg/workspace.SafeWorkspaceDir's containment
// check (an unsafe id could escape the workspaces/ root via traversal or
// separators). It is duplicated here to avoid a workspace -> library ->
// workspace import cycle: pkg/workspace/media_delete.go (the cascade-delete
// wire-up) calls into this library, which would be impossible if the
// library imported pkg/workspace transitively. The rule (single-segment
// non-empty id, no "..") is the same one safeID in pkg/workspace/instructions.go
// enforces; the inlined version is the authoritative single source of
// truth at this layer because pkg/workspace can still go through the
// library (downward edge of the cycle is now broken).
func safeWorkspaceDir(home, id string) (string, error) {
	if id == "" || strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		return "", fmt.Errorf("workspace: invalid id %q", id)
	}
	return filepath.Join(home, "workspaces", id), nil
}

const (
	DefaultOrphanAge = 30 * 24 * time.Hour
	MaxFileSize      = int64(100 << 20)
	manifestFileName = "manifest.json"
	manifestVersion  = 1
	mimeSniffBytes   = 512
)

var (
	ErrFileTooLarge             = errors.New("media library: file exceeds maximum size")
	ErrIntegrityCheckFailed     = errors.New("media library: integrity check failed")
	ErrInvalidFilename          = errors.New("media library: invalid filename")
	ErrInvalidManifest          = errors.New("media library: invalid manifest")
	ErrInvalidMediaID           = errors.New("media library: invalid media ID")
	ErrInvalidRef               = errors.New("media library: invalid workspace media ref")
	ErrNotFound                 = errors.New("media library: entry not found")
	ErrRefcountOverflow         = errors.New("media library: refcount overflow")
	ErrRefcountUnderflow        = errors.New("media library: refcount is already zero")
	ErrSourceNotAllowed         = errors.New("media library: source is not allowed in persistent storage")
	ErrWorkspaceContextRequired = errors.New("media library: caller workspace context is required")
	ErrWorkspaceMismatch        = errors.New("media library: caller workspace does not own ref")

	// ErrEntryStranded marks a media id whose manifest entry still claims to
	// exist at its normal on-disk location (l.rawPath(id)), but a prior
	// COMPOUND rollback failure has left the actual bytes stranded at an
	// internal quarantine path instead. It is reached only when BOTH of the
	// following happen in the same operation: (1) a persist (or, for
	// CascadeDelete/OrphanGC's phase-1 quarantine loop, a hard mid-loop
	// failure) triggers a rollback that restores the manifest entry to
	// "present", AND (2) the compensating rename-back of the
	// already-quarantined file to its original path ALSO fails. Before this
	// type existed, that second failure was only logger.WarnCF'd — the
	// manifest was left claiming the file lives at its original path while
	// the bytes actually sit at an undiscoverable ".delete-<id>-<uuid>" /
	// ".cascade-<id>-<uuid>" / ".gc-<id>-<uuid>" path, and every later read
	// got a generic ErrNotFound, indistinguishable from a routine
	// not-found.
	//
	// ErrEntryStranded is DISTINCT from both siblings it could otherwise be
	// confused with: ErrNotFound means no entry exists at all (a routine
	// absent id); ErrIntegrityCheckFailed means bytes are present at the
	// right path but fail sha256 verification. ErrEntryStranded means the
	// bytes exist somewhere on disk, just not where the manifest says.
	//
	// Read, Get, ResolvePathWithCaller, Refcount, IncrementRefcount,
	// DecrementRefcount, and Delete all check the in-memory stranded
	// registry (Library.stranded) BEFORE trusting the manifest for a given
	// id, and report this sentinel instead. The registry is rebuilt from a
	// directory scan on every Load (rebuildStrandedLocked) so the
	// corruption survives a process restart, and is cleared only by
	// OrphanGC's straggler reconciliation (reconcileStragglersLocked) —
	// see that function's doc for how the state is resolved.
	ErrEntryStranded = errors.New("media library: entry is stranded (manifest present, bytes quarantined)")
)

// stragglerPrefixes are the quarantine-dotfile prefixes Delete,
// CascadeDelete, and OrphanGC each use for their own quarantinePath
// construction (see their rawPath/quarantinePath pairs below). A file
// under l.path matching one of these prefixes is, by construction, always
// a LEFTOVER: every one of these operations holds l.mu for its entire
// quarantine -> mutate -> persist -> final-unlink critical section, so no
// legitimate quarantine file can be "in flight" at the moment a fresh
// caller (rebuildStrandedLocked at Load, or reconcileStragglersLocked from
// OrphanGC/CascadeDelete) takes the lock and scans the directory.
var stragglerPrefixes = []string{".delete-", ".cascade-", ".gc-"}

// uploadTempPrefix is the leaked-temp-file prefix uploadInternal creates
// before a manifest entry exists (UPLOAD-LEAK FIX, review). Unlike
// stragglerPrefixes above, an ".upload-<uuid>" name has no embedded
// manifest id — parseStragglerID does not match it — so
// reconcileStragglersLocked recognizes and reconciles it via a dedicated
// check against l.inFlightUploads instead (see that field's doc for why
// the check is race-free).
const uploadTempPrefix = ".upload-"

// isUploadTempName reports whether name is one of uploadInternal's
// ".upload-<uuid>" temp files.
func isUploadTempName(name string) bool {
	return strings.HasPrefix(name, uploadTempPrefix)
}

// refcount is the invariant-bearing non-negative integer refcount for a
// manifest entry. The package-private constructor newRefcount validates
// the invariant once at construction; subsequent mutations are
// arithmetic-only (Load, Upload, changeRefcount) and never go negative.
// A zero value of refcount is invalid (must be constructed via
// newRefcount); this is the type-system guard the reviewer requested
// (TD-M1).
type refcount int

// newRefcount returns a refcount for the given value. Negative values
// are rejected at construction.
func newRefcount(value int) (refcount, error) {
	if value < 0 {
		return 0, fmt.Errorf("%w: %d", ErrInvalidManifest, value)
	}
	return refcount(value), nil
}

// manifestEntry is the private, invariant-bearing in-memory and
// persisted shape of a single library entry. Required domain facts are
// required values (not pointers); refcount and last_refcount_seen_at
// are required (Wave 1 TD-M2) and live ON THIS TYPE only — there is no
// parallel map[string]int. The wire-shape gen.MediaLibraryEntry is
// built from manifestEntry at the API edge via projection.
type manifestEntry struct {
	id                 uuid.UUID
	workspaceID        string
	filename           string
	mime               string
	size               int64
	sha256             string
	uploadedAt         time.Time
	source             gen.MediaLibraryEntrySource
	refcount           refcount
	lastRefcountSeenAt time.Time
}

// newManifestEntry validates all invariants at construction time. The
// caller supplies the immutable server-assigned fields (id,
// workspaceID, mime, size, sha256, uploadedAt, source); refcount and
// lastRefcountSeenAt are seeded from initialRefcount + the supplied
// observation time. Any invariant violation aborts with ErrInvalidManifest.
func newManifestEntry(
	id uuid.UUID,
	workspaceID string,
	filename string,
	mime string,
	size int64,
	sha256 string,
	uploadedAt time.Time,
	source gen.MediaLibraryEntrySource,
	initialRefcount int,
	observedAt time.Time,
) (manifestEntry, error) {
	if id == uuid.Nil {
		return manifestEntry{}, fmt.Errorf("%w: id is nil", ErrInvalidManifest)
	}
	if workspaceID == "" {
		return manifestEntry{}, fmt.Errorf("%w: workspace_id is empty", ErrInvalidManifest)
	}
	normalized, nameErr := normalizeFilename(filename)
	if nameErr != nil {
		return manifestEntry{}, fmt.Errorf("%w: %v", ErrInvalidManifest, nameErr)
	}
	if mime == "" {
		return manifestEntry{}, fmt.Errorf("%w: mime is empty", ErrInvalidManifest)
	}
	if size < 0 || size > MaxFileSize {
		return manifestEntry{}, fmt.Errorf("%w: size %d", ErrInvalidManifest, size)
	}
	if !validDigest(sha256) {
		return manifestEntry{}, fmt.Errorf("%w: sha256 %q", ErrInvalidManifest, sha256)
	}
	if uploadedAt.IsZero() {
		return manifestEntry{}, fmt.Errorf("%w: uploaded_at is zero", ErrInvalidManifest)
	}
	if !source.Valid() && source != fixtureSource {
		return manifestEntry{}, fmt.Errorf("%w: source %q", ErrInvalidManifest, source)
	}
	count, rcErr := newRefcount(initialRefcount)
	if rcErr != nil {
		return manifestEntry{}, rcErr
	}
	if observedAt.IsZero() {
		return manifestEntry{}, fmt.Errorf("%w: last_refcount_seen_at is zero", ErrInvalidManifest)
	}
	return manifestEntry{
		id:                 id,
		workspaceID:        workspaceID,
		filename:           normalized,
		mime:               mime,
		size:               size,
		sha256:             sha256,
		uploadedAt:         uploadedAt.UTC(),
		source:             source,
		refcount:           count,
		lastRefcountSeenAt: observedAt.UTC(),
	}, nil
}

// clone returns a deep-copy of the entry. The entry contains only
// value-typed fields, so a value-copy is sufficient; this exists as a
// type-domain helper so callers that want to mutate a copy don't have
// to know the field set.
func (m manifestEntry) clone() manifestEntry {
	return m
}

// projection returns the wire-shape gen.MediaLibraryEntry view. The
// returned struct is a deep copy (its pointer fields point at copies of
// the underlying values), so callers can mutate it freely without
// affecting library state. This is the API-edge projection required by
// the contract (TD-M1).
func (m manifestEntry) projection() gen.MediaLibraryEntry {
	id := m.id
	workspaceID := m.workspaceID
	mime := m.mime
	size := m.size
	digest := m.sha256
	uploadedAt := m.uploadedAt
	refcount := int(m.refcount)
	lastSeen := m.lastRefcountSeenAt
	return gen.MediaLibraryEntry{
		Id:                 &id,
		WorkspaceId:        &workspaceID,
		Filename:           m.filename,
		Mime:               &mime,
		Size:               &size,
		Sha256:             &digest,
		UploadedAt:         &uploadedAt,
		Source:             m.source,
		Refcount:           &refcount,
		LastRefcountSeenAt: &lastSeen,
	}
}

// validate runs the same invariants newManifestEntry applies at
// construction, plus the cross-check that the refcount matches the
// per-entry slot. Used by Load to defend against persisted drift after
// a future schema change.
func (m manifestEntry) validate(workspaceID string) error {
	if m.workspaceID != workspaceID {
		return fmt.Errorf("%w: workspace mismatch for %s", ErrInvalidManifest, m.id)
	}
	if _, err := normalizeFilename(m.filename); err != nil {
		return fmt.Errorf("%w: filename for %s: %v", ErrInvalidManifest, m.id, err)
	}
	if m.refcount < 0 {
		return fmt.Errorf("%w: negative refcount for %s", ErrInvalidManifest, m.id)
	}
	return nil
}

type Library struct {
	mu          sync.RWMutex
	path        string
	workspaceID string
	manifest    map[string]manifestEntry
	now         func() time.Time
	// removeFile and renameFile are the quarantine-pattern's final-unlink
	// and rename primitives (Delete, CascadeDelete, OrphanGC and their
	// rollback paths). They default to os.Remove/os.Rename in New() and
	// exist as instance-scoped indirections — not hardcoded os.* calls —
	// purely so tests can deterministically inject a failure at an exact
	// step (e.g. "the rename succeeded but the final unlink failed", or
	// "id k's rename hard-fails mid-cascade while 1..k-1 already
	// succeeded"). Directory-permission tricks can't isolate either
	// scenario: rename and unlink need the same directory permissions, so
	// a permission change can't fail one without the other, and permission
	// bits aren't enforced at all when tests run as root. Production
	// always resolves to the real os.Remove/os.Rename.
	removeFile func(path string) error
	renameFile func(oldpath, newpath string) error
	// writeManifest is the persisted-manifest write primitive used by
	// persistLocked. Defaults to fileutil.WriteFileAtomic in New(); test-only
	// override via WithManifestWriter. Added for the same reason as
	// removeFile/renameFile above: it is the one remaining un-injectable step
	// needed to deterministically provoke the "persist failed AND the
	// compensating rename-back also failed" compound rollback scenario (see
	// ErrEntryStranded) without an OS-level permission trick.
	writeManifest func(path string, data []byte, perm os.FileMode) error
	// stranded is the in-memory registry of ids known to be in the
	// ErrEntryStranded state (manifest present, bytes quarantined) — see
	// ErrEntryStranded's doc for how an id lands here and
	// reconcileStragglersLocked for how it leaves. Read, Get,
	// ResolvePathWithCaller, Refcount, IncrementRefcount, DecrementRefcount,
	// and Delete all consult this before trusting l.manifest for a given id.
	// Rebuilt from a directory scan on every load() (rebuildStrandedLocked)
	// so the corruption is not forgotten across a process restart — the map
	// itself is not persisted (there is no wire schema field for it; the
	// manifest is defined to contain accurate entries only).
	stranded map[string]string
	// inFlightUploads is the in-memory set of ".upload-*" temp-file
	// basenames currently being written by an active uploadInternal call
	// (UPLOAD-LEAK FIX, review). Membership is added and removed under l.mu in the
	// SAME critical section that creates/removes the on-disk temp file, so
	// reconcileStragglersLocked's directory scan (which also runs under
	// l.mu.Lock() for its entire duration — see that function's own doc)
	// can never observe a ".upload-*" name on disk without ALSO being able
	// to consult whether it is registered here: either the scan's ReadDir
	// happens strictly before registration (the file does not exist yet —
	// nothing to see) or strictly after it (both the on-disk file and the
	// registration are visible together, because both were produced inside
	// the same l.mu.Lock() section in uploadInternal). There is no window
	// where the file exists without being registered, so the scan can
	// safely reclaim a genuinely leaked temp file (a crashed/interrupted
	// upload, possibly from an earlier process) while never touching one
	// still being written by this process. Not persisted — a fresh Library
	// starts with this empty, which is correct: nothing in a brand-new
	// process can be mid-upload yet, so every ".upload-*" name found at
	// Load time is, by construction, a leftover from before this process
	// started.
	inFlightUploads map[string]struct{}
}

type Option func(*Library)

type OrphanGCConfig struct {
	Enabled bool
	MaxAge  time.Duration
}

func WithClock(now func() time.Time) Option {
	return func(library *Library) {
		if now != nil {
			library.now = now
		}
	}
}

// WithFileRemover overrides the final on-disk unlink step used by Delete,
// CascadeDelete, and OrphanGC's quarantine cleanup. Test-only — see the
// Library.removeFile field doc for why this exists instead of OS-level
// permission tricks. Production code never calls this; New() defaults to
// os.Remove.
func WithFileRemover(remove func(path string) error) Option {
	return func(library *Library) {
		if remove != nil {
			library.removeFile = remove
		}
	}
}

// WithFileRenamer overrides the os.Rename call used by the quarantine step
// of Delete, CascadeDelete, and OrphanGC (and their rollback paths).
// Test-only — see the Library.renameFile field doc. Production code never
// calls this; New() defaults to os.Rename.
func WithFileRenamer(rename func(oldpath, newpath string) error) Option {
	return func(library *Library) {
		if rename != nil {
			library.renameFile = rename
		}
	}
}

// WithManifestWriter overrides the manifest-file write step of
// persistLocked. Test-only — see the Library.writeManifest field doc.
// Production code never calls this; New() defaults to
// fileutil.WriteFileAtomic.
func WithManifestWriter(write func(path string, data []byte, perm os.FileMode) error) Option {
	return func(library *Library) {
		if write != nil {
			library.writeManifest = write
		}
	}
}

func New(home, workspaceID string, options ...Option) (*Library, error) {
	workspaceDir, err := safeWorkspaceDir(home, workspaceID)
	if err != nil {
		return nil, err
	}
	library := &Library{
		path:            filepath.Join(workspaceDir, "media"),
		workspaceID:     workspaceID,
		manifest:        make(map[string]manifestEntry),
		now:             time.Now,
		removeFile:      os.Remove,
		renameFile:      os.Rename,
		writeManifest:   fileutil.WriteFileAtomic,
		inFlightUploads: make(map[string]struct{}),
	}
	for _, option := range options {
		if option != nil {
			option(library)
		}
	}
	if err := library.load(); err != nil {
		return nil, err
	}
	return library, nil
}

func (l *Library) Path() string {
	return l.path
}

// NormalizeCache returns the process-wide sha256-keyed normalization
// cache (FR-004). The cache is lazy-initialized via sync.Once and
// shared across every Library instance in the process — raw bytes do
// not change mid-process so a stale-OK process-local cache is the
// right scope. See pkg/media/library/normalize_cache.go for the key
// shape, capacity, and concurrency contract.
func (l *Library) NormalizeCache() NormalizeCache {
	return GlobalNormalizeCache()
}

func (l *Library) List() []gen.MediaLibraryEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	ids := make([]string, 0, len(l.manifest))
	for id := range l.manifest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	entries := make([]gen.MediaLibraryEntry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, l.manifest[id].projection())
	}
	return entries
}

// Get returns the metadata for a single entry by id, or an error if the
// entry does not exist. Unlike Read, it does not read or verify the raw
// file bytes — it returns only the in-memory manifest projection. A
// stranded id (see ErrEntryStranded) is reported as ErrEntryStranded
// rather than a routine manifest lookup, even though Get itself never
// touches the raw bytes — presenting a stranded entry's metadata as if it
// were normal would let a caller believe the entry is usable when it
// factually is not.
func (l *Library) Get(id string) (gen.MediaLibraryEntry, error) {
	if _, err := uuid.Parse(id); err != nil {
		return gen.MediaLibraryEntry{}, fmt.Errorf("%w: %q", ErrInvalidMediaID, id)
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if _, stranded := l.stranded[id]; stranded {
		return gen.MediaLibraryEntry{}, fmt.Errorf("%w: %s", ErrEntryStranded, id)
	}
	entry, exists := l.manifest[id]
	if !exists {
		return gen.MediaLibraryEntry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return entry.projection(), nil
}

// fixtureSource is the internal-only MediaLibraryEntrySource used by
// the package-private UploadFixture test helper. It is NOT exposed on
// the wire — contracts/components/schemas/MediaLibraryEntry.yaml
// drops test_fixture from the production enum (Wave 1 TD-m1). Tests
// that need a non-user-upload source reach it through UploadFixture.
var fixtureSource gen.MediaLibraryEntrySource = "test_fixture"

// uploadInternal is the shared streaming + sha256 + persist core used by
// both Upload (production) and UploadFixture (test helpers). When
// requireSourceIsProduction is true, only gen.UserUpload is accepted;
// false accepts any source including fixtureSource.
func (l *Library) uploadInternal(
	filename string,
	source gen.MediaLibraryEntrySource,
	requireSourceIsProduction bool,
	reader io.Reader,
) (string, gen.MediaLibraryEntry, error) {
	filename, normalizeErr := normalizeFilename(filename)
	if normalizeErr != nil {
		return "", gen.MediaLibraryEntry{}, normalizeErr
	}
	if requireSourceIsProduction && source != gen.UserUpload {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("%w: %q", ErrSourceNotAllowed, source)
	}
	if reader == nil {
		return "", gen.MediaLibraryEntry{}, errors.New("media library: upload reader is nil")
	}
	if mkdirErr := os.MkdirAll(l.path, 0o700); mkdirErr != nil {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("media library: create directory: %w", mkdirErr)
	}

	id := uuid.New()
	// UPLOAD-LEAK FIX (review): ".upload-*" temp files used to be created via
	// os.CreateTemp OUTSIDE l.mu, which made a leaked one (a crashed or
	// interrupted upload) permanently unreclaimable — reconcileStragglersLocked
	// deliberately excluded the prefix because a directory scan under l.mu
	// could not tell a genuine leak apart from a concurrent in-flight
	// upload without racing the write. Fix: generate the temp name up
	// front and register it in l.inFlightUploads AND create the (empty)
	// file itself in the SAME l.mu.Lock() critical section — see the
	// inFlightUploads field doc for why that closes the race. O_EXCL makes
	// a UUID collision (astronomically unlikely) surface as an error
	// instead of silently truncating an existing file.
	tempName := ".upload-" + uuid.NewString()
	temporaryPath := filepath.Join(l.path, tempName)
	l.mu.Lock()
	temporary, createErr := os.OpenFile(temporaryPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if createErr == nil {
		l.inFlightUploads[tempName] = struct{}{}
	}
	l.mu.Unlock()
	if createErr != nil {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("media library: create upload: %w", createErr)
	}
	keepTemporary := true
	defer func() {
		l.mu.Lock()
		delete(l.inFlightUploads, tempName)
		l.mu.Unlock()
		if keepTemporary {
			// LOW item (review): both errors used to be discarded with no
			// logging at all — every sibling rollback in this file at least
			// WarnCFs. A leaked ".upload-*" file is now discoverable by
			// reconcileStragglersLocked (UPLOAD-LEAK FIX) via the inFlightUploads
			// registry above, but a prompt best-effort cleanup here is
			// still preferable to waiting for the next GC pass.
			if closeErr := temporary.Close(); closeErr != nil {
				logger.WarnCF("media-library", "upload temp file close failed", map[string]any{
					"workspace_id": l.workspaceID,
					"path":         temporaryPath,
					"error":        closeErr.Error(),
				})
			}
			if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				logger.WarnCF("media-library", "upload temp file cleanup failed", map[string]any{
					"workspace_id": l.workspaceID,
					"path":         temporaryPath,
					"error":        removeErr.Error(),
				})
			}
		}
	}()
	if chmodErr := temporary.Chmod(0o600); chmodErr != nil {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("media library: secure upload: %w", chmodErr)
	}

	prefix := make([]byte, mimeSniffBytes)
	prefixLength, readErr := io.ReadFull(reader, prefix)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("media library: read upload prefix: %w", readErr)
	}
	prefix = prefix[:prefixLength]
	hasher := sha256.New()
	limited := io.LimitReader(io.MultiReader(bytes.NewReader(prefix), reader), MaxFileSize+1)
	written, copyErr := io.Copy(io.MultiWriter(temporary, hasher), limited)
	if copyErr != nil {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("media library: write upload: %w", copyErr)
	}
	if written > MaxFileSize {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("%w: %d > %d", ErrFileTooLarge, written, MaxFileSize)
	}
	if err := temporary.Sync(); err != nil {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("media library: sync upload: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("media library: close upload: %w", err)
	}

	rawPath := l.rawPath(id.String())
	if err := os.Rename(temporaryPath, rawPath); err != nil {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("media library: commit upload: %w", err)
	}
	keepTemporary = false
	// LOW item (review): openErr used to be checked only via `== nil` — an
	// open failure silently skipped the fsync entirely with no log at all,
	// while the Sync() failure two lines below WAS logged. That asymmetry
	// is a durability gap: an operator has no way to know the post-rename
	// directory-entry fsync (the step that makes the rename durable across
	// a crash) never ran. directory.Close()'s error was also discarded.
	directory, openErr := os.Open(l.path)
	if openErr != nil {
		logger.WarnCF("media-library", "upload directory open for fsync failed", map[string]any{
			"workspace_id": l.workspaceID,
			"error":        openErr.Error(),
		})
	} else {
		if syncErr := directory.Sync(); syncErr != nil {
			logger.WarnCF("media-library", "upload directory sync failed", map[string]any{
				"workspace_id": l.workspaceID,
				"error":        syncErr.Error(),
			})
		}
		if closeErr := directory.Close(); closeErr != nil {
			logger.WarnCF("media-library", "upload directory close failed", map[string]any{
				"workspace_id": l.workspaceID,
				"error":        closeErr.Error(),
			})
		}
	}

	uploadedAt := l.now().UTC()
	mime := http.DetectContentType(prefix)
	size := written
	digest := hex.EncodeToString(hasher.Sum(nil))
	entry, entryErr := newManifestEntry(
		id,
		l.workspaceID,
		filename,
		mime,
		size,
		digest,
		uploadedAt,
		source,
		0,
		uploadedAt,
	)
	if entryErr != nil {
		removeErr := os.Remove(rawPath)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return "", gen.MediaLibraryEntry{}, errors.Join(entryErr, fmt.Errorf("rollback: %w", removeErr))
		}
		return "", gen.MediaLibraryEntry{}, entryErr
	}

	l.mu.Lock()
	l.manifest[id.String()] = entry
	if persistErr := l.persistLocked(); persistErr != nil {
		delete(l.manifest, id.String())
		l.mu.Unlock()
		removeErr := os.Remove(rawPath)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			rollbackErr := fmt.Errorf("media library: rollback upload: %w", removeErr)
			return "", gen.MediaLibraryEntry{}, errors.Join(persistErr, rollbackErr)
		}
		return "", gen.MediaLibraryEntry{}, persistErr
	}
	projection := l.manifest[id.String()].projection()
	l.mu.Unlock()

	ref := "media://workspace/" + l.workspaceID + "/" + id.String()
	return ref, projection, nil
}

// Upload is the live entry point for new media. Source must be one of
// gen.UserUpload (the production path); test fixtures use the
// package-private UploadFixture helper instead of going through Upload
// with an internal-only source value (Wave 1 TD-m1). gen.ToolOutput is
// reserved for the persistent-storage layer that future work will add
// (session-scoped tool outputs that never migrate to the library); it
// is rejected here as ErrSourceNotAllowed so the wire enum's second
// value cannot accidentally land in the manifest.
func (l *Library) Upload(
	filename string,
	source gen.MediaLibraryEntrySource,
	reader io.Reader,
) (string, gen.MediaLibraryEntry, error) {
	return l.uploadInternal(filename, source, true, reader)
}

func (l *Library) Read(id string) ([]byte, gen.MediaLibraryEntry, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, gen.MediaLibraryEntry{}, fmt.Errorf("%w: %q", ErrInvalidMediaID, id)
	}
	l.mu.RLock()
	entry, exists := l.manifest[id]
	_, stranded := l.stranded[id]
	l.mu.RUnlock()
	if stranded {
		// Deliberately checked BEFORE the os.Open below (and before the
		// !exists check — a stranded id is always still present in
		// l.manifest by construction, see ErrEntryStranded's doc): without
		// this, os.Open(l.rawPath(id)) would hit os.ErrNotExist (the bytes
		// are at the quarantine path, not rawPath) and this would silently
		// fall through to the ErrNotFound branch below, exactly the
		// masking bug ErrEntryStranded exists to close.
		return nil, gen.MediaLibraryEntry{}, fmt.Errorf("%w: %s", ErrEntryStranded, id)
	}
	if !exists {
		return nil, gen.MediaLibraryEntry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	projection := entry.projection()
	if projection.Size == nil || projection.Sha256 == nil || *projection.Size < 0 || *projection.Size > MaxFileSize {
		return nil, projection, fmt.Errorf("%w: invalid integrity fields for %s", ErrIntegrityCheckFailed, id)
	}

	file, err := os.Open(l.rawPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, projection, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, projection, fmt.Errorf("media library: open %s: %w", id, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, *projection.Size+1))
	if err != nil {
		return nil, projection, fmt.Errorf("media library: read %s: %w", id, err)
	}
	actualDigest := sha256.Sum256(data)
	actualDigestHex := hex.EncodeToString(actualDigest[:])
	if int64(len(data)) != *projection.Size || actualDigestHex != *projection.Sha256 {
		logger.WarnCF("media-library", "sha256 verification failed", map[string]any{
			"workspace_id": l.workspaceID,
			"media_id":     id,
			"expected":     *projection.Sha256,
			"actual":       actualDigestHex,
		})
		return nil, projection, fmt.Errorf("%w: %s", ErrIntegrityCheckFailed, id)
	}
	return data, projection, nil
}

func (l *Library) ResolveWithWorkspace(
	ref string,
	callerWorkspaceID *string,
) ([]byte, gen.MediaLibraryEntry, error) {
	mediaID, err := l.authorizeWorkspaceRef(ref, callerWorkspaceID)
	if err != nil {
		return nil, gen.MediaLibraryEntry{}, err
	}
	return l.Read(mediaID)
}

// ResolvePathWithCaller is the path-level workspace resolver consumed by
// pkg/media's two-tier resolver (FR-028/FR-028a). It enforces the same
// caller-workspace membership guard as ResolveWithWorkspace, then returns
// the entry's on-disk raw path plus transport metadata — without reading
// or sha256-verifying the bytes.
//
// The bytes-returning Read/ResolveWithWorkspace path is the integrity gate
// (FR-002 sha256-on-read) for decode-bound consumption; ResolvePathWithCaller
// serves the transport consumers (channels delivering outbound attachments,
// session replay, tool-result tagging) that only need a path. This split
// avoids a redundant full-file read for transport-only callers while keeping
// the membership guard (FR-028a Spoofing) load-bearing on every entry point.
func (l *Library) ResolvePathWithCaller(
	ref string,
	callerWorkspaceID *string,
) (string, media.MediaMeta, error) {
	mediaID, err := l.authorizeWorkspaceRef(ref, callerWorkspaceID)
	if err != nil {
		return "", media.MediaMeta{}, err
	}
	l.mu.RLock()
	entry, exists := l.manifest[mediaID]
	_, stranded := l.stranded[mediaID]
	l.mu.RUnlock()
	if stranded {
		// See Read's identical guard — ResolvePathWithCaller is the
		// path-only counterpart consumed by transport callers (send_file,
		// channel attachment delivery); without this check it would hand
		// back a rawPath that does not exist, surfacing as a bare OS-level
		// "file not found" several layers away instead of a clear,
		// attributable ErrEntryStranded here.
		return "", media.MediaMeta{}, fmt.Errorf("%w: %s", ErrEntryStranded, mediaID)
	}
	if !exists {
		return "", media.MediaMeta{}, fmt.Errorf("%w: %s", ErrNotFound, mediaID)
	}
	return l.rawPath(mediaID), media.MediaMeta{
		Filename:    entry.filename,
		ContentType: entry.mime,
		SHA256:      entry.sha256,
		Source:      string(entry.source),
	}, nil
}

// authorizeWorkspaceRef is the shared FR-028a membership guard for both the
// bytes-returning (ResolveWithWorkspace) and path-returning
// (ResolvePathWithCaller) workspace resolvers. It rejects a nil/empty caller
// context, a structurally malformed ref, and any caller whose workspace does
// not own the ref. On success it returns the media ID parsed from the ref.
func (l *Library) authorizeWorkspaceRef(ref string, callerWorkspaceID *string) (string, error) {
	if callerWorkspaceID == nil || *callerWorkspaceID == "" {
		return "", ErrWorkspaceContextRequired
	}
	const prefix = "media://workspace/"
	if !strings.HasPrefix(ref, prefix) {
		return "", fmt.Errorf("%w: %q", ErrInvalidRef, ref)
	}
	parts := strings.Split(strings.TrimPrefix(ref, prefix), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("%w: %q", ErrInvalidRef, ref)
	}
	if parts[0] != l.workspaceID || *callerWorkspaceID != parts[0] {
		return "", fmt.Errorf(
			"%w: caller=%q ref_workspace=%q",
			ErrWorkspaceMismatch,
			*callerWorkspaceID,
			parts[0],
		)
	}
	return parts[1], nil
}

func (l *Library) IncrementRefcount(id string) (int, error) {
	return l.changeRefcount(id, 1)
}

func (l *Library) DecrementRefcount(id string) (int, error) {
	return l.changeRefcount(id, -1)
}

func (l *Library) Refcount(id string) (int, error) {
	if _, err := uuid.Parse(id); err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidMediaID, id)
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if _, stranded := l.stranded[id]; stranded {
		return 0, fmt.Errorf("%w: %s", ErrEntryStranded, id)
	}
	entry, exists := l.manifest[id]
	if !exists {
		return 0, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return int(entry.refcount), nil
}

// Delete removes a single media entry from the library: the raw file is
// unlinked (best-effort quarantine pattern: rename to a hidden path first so
// a partial-success leaves the entry recoverable, then unlink), the manifest
// entry is dropped, and the manifest is rewritten. The deleted entry
// projection is returned to the caller so it can construct an audit
// event without re-reading state. Returns ErrInvalidMediaID for an
// ill-formed id and ErrNotFound for an id that is well-formed but absent.
//
// FR-008: callers MUST log a media.delete audit event with the returned
// entry's filename + bytes_freed — but only when err is nil. A non-nil
// error from a manifest-committed Delete (the final-unlink step below)
// means the disk bytes were NOT actually freed yet (they are sitting at an
// orphaned quarantine path); an audit event claiming bytes_freed in that
// case would be false. The projection is still returned on that error path
// so a caller that wants to log a degraded/partial event can do so
// deliberately, but it must not claim success.
//
// ErrEntryStranded (see its own doc) is returned in two situations: the id
// was ALREADY stranded from a prior compound rollback failure (checked up
// front, before any quarantine attempt — retrying Delete on a stranded id
// must not be allowed to silently "fix" the manifest by accident while
// leaking the old quarantine file forever; only OrphanGC's straggler
// reconciliation resolves this state), or this very call's own persist
// rolled back AND the compensating rename-back also failed, newly landing
// the id in that state.
func (l *Library) Delete(id string) (gen.MediaLibraryEntry, error) {
	if _, err := uuid.Parse(id); err != nil {
		return gen.MediaLibraryEntry{}, fmt.Errorf("%w: %q", ErrInvalidMediaID, id)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if quarantinePath, stranded := l.stranded[id]; stranded {
		return gen.MediaLibraryEntry{},
			fmt.Errorf("%w: %s (bytes stranded at %s)", ErrEntryStranded, id, quarantinePath)
	}
	entry, exists := l.manifest[id]
	if !exists {
		return gen.MediaLibraryEntry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	rawPath := l.rawPath(id)
	quarantinePath := filepath.Join(l.path, ".delete-"+id+"-"+uuid.NewString())
	quarantined := false
	if err := l.renameFile(rawPath, quarantinePath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return gen.MediaLibraryEntry{}, fmt.Errorf("media library: quarantine delete %s: %w", id, err)
		}
	} else {
		quarantined = true
	}

	previousEntry := entry.clone()
	delete(l.manifest, id)
	if err := l.persistLocked(); err != nil {
		l.manifest[id] = previousEntry
		if quarantined {
			if renameErr := l.renameFile(quarantinePath, rawPath); renameErr != nil {
				logger.WarnCF("media-library", "delete rollback rename failed", map[string]any{
					"workspace_id": l.workspaceID,
					"media_id":     id,
					"error":        renameErr.Error(),
				})
				// Compound rollback-of-rollback (ErrEntryStranded): the
				// manifest mutation was just reverted above to claim id is
				// present at rawPath, but the bytes are still at
				// quarantinePath because THIS rename-back also failed.
				// Escalate — do not let this be a WarnCF-only event. Join
				// all three: the original persist failure, the rollback
				// failure itself, and the sentinel.
				l.markStrandedLocked(id, quarantinePath)
				return gen.MediaLibraryEntry{}, errors.Join(
					err, renameErr, fmt.Errorf("%w: %s", ErrEntryStranded, id))
			}
		}
		return gen.MediaLibraryEntry{}, err
	}
	if quarantined {
		// FIX 3 (review): a final-unlink failure used to be logged and then
		// swallowed — Delete returned nil regardless, so a caller (e.g. the
		// FR-008 audit-log emitter) saw success and reported bytes_freed for
		// bytes that are still on disk at quarantinePath. Nothing scans for
		// those paths (OrphanGC only walks manifest entries), so they leaked
		// permanently. Follow the sibling OrphanGC's established pattern:
		// return the error instead of discarding it. The manifest change
		// itself is correct and is NOT rolled back — the entry is genuinely
		// gone from the manifest (persistLocked already committed that);
		// this error means disk cleanup is incomplete, not that the delete
		// didn't happen.
		if removeErr := l.removeFile(quarantinePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			logger.WarnCF("media-library", "delete final unlink failed", map[string]any{
				"workspace_id": l.workspaceID,
				"media_id":     id,
				"error":        removeErr.Error(),
			})
			return previousEntry.projection(), fmt.Errorf("media library: delete final unlink %s: %w", id, removeErr)
		}
	}
	return previousEntry.projection(), nil
}

// CascadeDelete removes every entry in the library and returns the summary
// the caller needs to emit a media.cascade_delete audit event. Each entry
// is quarantined-then-unlinked (same atomicity pattern as Delete / OrphanGC
// — a partial failure on one entry leaves the others on disk and the
// manifest entries intact). bytes_freed is the sum of *entry.Size over all
// successfully deleted entries; filenames and ids are parallel slices in
// the same deterministic order.
//
// Returns ([], 0, nil) for an empty library — a successful no-op cascade.
//
// FR-009: callers MUST log a media.cascade_delete audit event with the
// returned summary — but only when err is nil (see the final-unlink note
// below; the same "don't claim bytes that aren't actually free" rule as
// Delete applies here).
//
// Two-phase structure (FIX 4, review): phase 1 quarantines every entry's raw
// file WITHOUT touching l.manifest; phase 2 (reached only once phase 1
// commits cleanly for every id) mutates l.manifest and persists. Before this
// fix, delete(l.manifest, id) was interleaved into phase 1's own loop, so a
// hard rename failure at id k left ids 1..k-1 already removed from memory
// even though persistLocked never ran (on-disk manifest.json still had
// them) and restoreCascadeQuarantined only reverses file renames, never
// l.manifest — the in-memory state silently drifted from disk. The two-phase
// split matches the pattern OrphanGC already used correctly.
//
// Phase 0 (ErrEntryStranded): before either phase runs, any id already
// stranded from a prior compound rollback failure is reconciled via
// reconcileStragglersLocked rather than run through the normal quarantine
// dance — that dance assumes rawPath still holds the bytes, which is false
// for a stranded id (they are at an old quarantine path instead). Unlike
// Delete (which refuses outright on a stranded id so a single targeted
// delete request gets an explicit, attributable error), CascadeDelete's
// "remove everything, best-effort" semantics make self-healing the right
// choice here: one already-broken entry must not block deleting every
// other entry in the library.
func (l *Library) CascadeDelete() (entries []gen.MediaLibraryEntry, bytesFreed int64, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	reconciled, reconciledBytes, manifestChanged, reconcileErr := l.reconcileStragglersLocked()
	// withReconcileErr folds a straggler-reconcile failure into whatever
	// this call is about to return, on every exit path below
	// (STRANDED-SKIP FIX, review) — reconcileErr is a genuinely separate
	// failure domain (best-effort straggler cleanup) from the main
	// cascade, so it is joined rather than silently dropped, overwritten,
	// or allowed to overwrite the main result.
	withReconcileErr := func(err error) error {
		switch {
		case reconcileErr == nil:
			return err
		case err == nil:
			return reconcileErr
		default:
			return errors.Join(err, reconcileErr)
		}
	}
	if manifestChanged {
		if persistErr := l.persistLocked(); persistErr != nil {
			return reconciled, reconciledBytes,
				withReconcileErr(fmt.Errorf("media library: persist after straggler reconcile: %w", persistErr))
		}
	}

	ids := make([]string, 0, len(l.manifest))
	for id := range l.manifest {
		if _, stillStranded := l.stranded[id]; stillStranded {
			// STRANDED-SKIP FIX (review): an id whose straggler reconcile
			// above just FAILED (or one already stranded from before this
			// call, whose reconcile also failed) must never enter the
			// normal quarantine dance below — rawPath does not hold its
			// bytes (they are at an OLD quarantine path instead), so
			// l.renameFile(rawPath, quarantinePath) would return
			// os.ErrNotExist, get treated as the benign "already gone"
			// case, and phase 2 would delete the manifest entry and count
			// its bytes as freed even though they were never touched —
			// exactly the false-success laundering this fix closes. Skip
			// it here; it stays cataloged (both in l.manifest and
			// l.stranded) for a future reconcile to retry.
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return reconciled, reconciledBytes, withReconcileErr(nil)
	}

	// Phase 1: quarantine every entry's raw file. l.manifest is untouched
	// here — a hard rename failure partway through unwinds cleanly via
	// restoreCascadeQuarantined without the manifest having drifted from
	// what (still correctly) remains on disk.
	deleted := make([]cascadePending, 0, len(ids))
	for _, id := range ids {
		entry := l.manifest[id]
		rawPath := l.rawPath(id)
		quarantinePath := filepath.Join(l.path, ".cascade-"+id+"-"+uuid.NewString())
		item := cascadePending{id: id, entry: entry.clone(), quarantinePath: quarantinePath}
		if renameErr := l.renameFile(rawPath, quarantinePath); renameErr != nil {
			if errors.Is(renameErr, os.ErrNotExist) {
				item.quarantined = false
			} else {
				failed := l.restoreCascadeQuarantined(deleted)
				combined := fmt.Errorf("media library: quarantine cascade %s: %w", id, renameErr)
				if len(failed) > 0 {
					for _, f := range failed {
						l.markStrandedLocked(f.id, f.quarantinePath)
					}
					combined = errors.Join(combined,
						fmt.Errorf("%w: %d entries stranded after rollback failure", ErrEntryStranded, len(failed)))
				}
				// Deliberately nil/0 here, NOT reconciled/reconciledBytes:
				// wrapCascadeError (pkg/workspace/media_delete.go) treats a
				// non-empty first return value together with a non-nil error
				// as "manifest committed cleanly, only a final-unlink
				// straggler remains" and downgrades it to a non-fatal
				// ErrCascadeStraggler. This IS a genuine hard failure (the
				// main cascade never got past phase 1) — reporting the
				// phase-0 reconcile's unrelated entries here would
				// misclassify it as the harmless case. The reconcile itself
				// already persisted successfully above if it ran, so no
				// data is lost — only this particular call's summary omits
				// it.
				return nil, 0, withReconcileErr(combined)
			}
		} else {
			item.quarantined = true
		}
		deleted = append(deleted, item)
	}

	// Phase 2: the rename phase committed cleanly for every id — now mutate
	// the in-memory manifest and persist. A persist failure at this point
	// restores both memory (from the entry values captured in `deleted`,
	// each a clone taken before deletion) and the renamed files.
	for _, item := range deleted {
		delete(l.manifest, item.id)
	}
	if persistErr := l.persistLocked(); persistErr != nil {
		for _, item := range deleted {
			l.manifest[item.id] = item.entry
		}
		failed := l.restoreCascadeQuarantined(deleted)
		combined := persistErr
		if len(failed) > 0 {
			for _, f := range failed {
				l.markStrandedLocked(f.id, f.quarantinePath)
			}
			combined = errors.Join(persistErr,
				fmt.Errorf("%w: %d entries stranded after rollback failure", ErrEntryStranded, len(failed)))
		}
		// See the phase-1 hard-failure return above for why this is nil/0
		// rather than reconciled/reconciledBytes.
		return nil, 0, withReconcileErr(combined)
	}

	// FIX 3 (review): final-unlink failures used to be logged and then
	// swallowed (CascadeDelete always returned a nil error), so
	// WorkspaceDeleteHook saw cascadeErr == nil and emitted a
	// media.cascade_delete audit event claiming bytes_freed that included
	// bytes still on disk at a permanently orphaned quarantine path (nothing
	// scans for those — OrphanGC only walks manifest entries). Follow the
	// sibling OrphanGC's established errors.Join pattern instead of
	// discarding the failure.
	var removeErrors []error
	for _, item := range deleted {
		if item.quarantined {
			removeErr := l.removeFile(item.quarantinePath)
			if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				logger.WarnCF("media-library", "cascade final unlink failed", map[string]any{
					"workspace_id": l.workspaceID,
					"media_id":     item.id,
					"error":        removeErr.Error(),
				})
				removeErrors = append(removeErrors,
					fmt.Errorf("media library: cascade final unlink %s: %w", item.id, removeErr))
			}
		}
	}

	out := make([]gen.MediaLibraryEntry, 0, len(reconciled)+len(deleted))
	totalBytes := reconciledBytes
	out = append(out, reconciled...)
	for _, item := range deleted {
		out = append(out, item.entry.projection())
		totalBytes += item.entry.size
	}
	if len(removeErrors) > 0 {
		return out, totalBytes, withReconcileErr(errors.Join(removeErrors...))
	}
	return out, totalBytes, withReconcileErr(nil)
}

// OrphanGC deletes every entry whose refcount is zero and whose
// last-referenced timestamp is older than config.MaxAge. It also always
// runs reconcileStragglersLocked first (see that function's doc) —
// independent of the age-based sweep below and of whether any entry is
// currently old enough to age out — because a stranded entry (see
// ErrEntryStranded) blocks Read/Get/ResolvePathWithCaller/Delete the
// moment it happens, not after MaxAge elapses, so its cleanup must not
// wait for the age gate.
func (l *Library) OrphanGC(config OrphanGCConfig) ([]gen.MediaLibraryEntry, error) {
	if !config.Enabled {
		return nil, nil
	}
	maxAge := config.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultOrphanAge
	}
	now := l.now().UTC()

	l.mu.Lock()
	defer l.mu.Unlock()

	reconciled, _, manifestChanged, reconcileErr := l.reconcileStragglersLocked()
	// See CascadeDelete's identical withReconcileErr helper doc
	// (STRANDED-SKIP FIX, review) — reconcileErr is folded into whatever
	// this call returns on every exit path below rather than silently
	// dropped.
	withReconcileErr := func(err error) error {
		switch {
		case reconcileErr == nil:
			return err
		case err == nil:
			return reconcileErr
		default:
			return errors.Join(err, reconcileErr)
		}
	}
	if manifestChanged {
		if persistErr := l.persistLocked(); persistErr != nil {
			return reconciled, withReconcileErr(
				fmt.Errorf("media library: persist after straggler reconcile: %w", persistErr))
		}
	}

	ids := make([]string, 0)
	for id, entry := range l.manifest {
		if _, stillStranded := l.stranded[id]; stillStranded {
			// STRANDED-SKIP FIX (review): see CascadeDelete's identical
			// guard — an id whose straggler reconcile just failed (or was
			// already stranded and stayed that way) must not enter the
			// age-based sweep below; its bytes are not at rawPath.
			continue
		}
		if entry.refcount != 0 {
			continue
		}
		lastSeen := entry.lastRefcountSeenAt
		if now.Before(lastSeen) || now.Sub(lastSeen) < maxAge {
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return reconciled, withReconcileErr(nil)
	}
	sort.Strings(ids)

	deleted := make([]gen.MediaLibraryEntry, 0, len(ids))
	quarantined := make(map[string]string, len(ids))
	for _, id := range ids {
		rawPath := l.rawPath(id)
		quarantinePath := filepath.Join(l.path, ".gc-"+id+"-"+uuid.NewString())
		if err := l.renameFile(rawPath, quarantinePath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				failed := l.restoreQuarantined(quarantined)
				combined := fmt.Errorf("media library: quarantine orphan %s: %w", id, err)
				if len(failed) > 0 {
					for failedID, failedPath := range failed {
						l.markStrandedLocked(failedID, failedPath)
					}
					combined = errors.Join(combined,
						fmt.Errorf("%w: %d entries stranded after rollback failure", ErrEntryStranded, len(failed)))
				}
				// See CascadeDelete's identical note: reconciled is
				// deliberately omitted from a hard-failure return so this
				// unrelated phase-0 cleanup doesn't get folded into what is
				// otherwise a genuine age-sweep failure.
				return nil, withReconcileErr(combined)
			}
		} else {
			quarantined[id] = quarantinePath
		}
	}
	// Snapshot the manifest entries before deletion so a persist failure can
	// restore them directly from the preserved manifestEntry values, not from
	// a rebuilt projection (TD-M11).
	snapshot := make(map[string]manifestEntry, len(ids))
	for _, id := range ids {
		snapshot[id] = l.manifest[id].clone()
		deleted = append(deleted, l.manifest[id].projection())
		delete(l.manifest, id)
	}
	if err := l.persistLocked(); err != nil {
		// PER-ID RESTORE FIX (review): restore ONLY the ids this batch
		// touched, one at a time. `snapshot` is scoped to `ids` (the
		// current age-out batch) — replacing l.manifest wholesale with it
		// (the previous `l.manifest = snapshot`) would silently discard
		// every OTHER cataloged entry (live-refcount entries, not-yet-aged
		// entries) from memory until the next process restart.
		// CascadeDelete's own persist-failure rollback a few lines away
		// already restores per-id; OrphanGC must match it.
		for id, entry := range snapshot {
			l.manifest[id] = entry
		}
		failed := l.restoreQuarantined(quarantined)
		combined := err
		if len(failed) > 0 {
			for failedID, failedPath := range failed {
				l.markStrandedLocked(failedID, failedPath)
			}
			combined = errors.Join(err,
				fmt.Errorf("%w: %d entries stranded after rollback failure", ErrEntryStranded, len(failed)))
		}
		return nil, withReconcileErr(combined)
	}

	var removeErrors []error
	for id, quarantinePath := range quarantined {
		if err := l.removeFile(quarantinePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			removeErrors = append(removeErrors, fmt.Errorf("media library: remove orphan %s: %w", id, err))
		}
	}
	out := make([]gen.MediaLibraryEntry, 0, len(reconciled)+len(deleted))
	out = append(out, reconciled...)
	out = append(out, deleted...)
	if len(removeErrors) > 0 {
		return out, withReconcileErr(errors.Join(removeErrors...))
	}
	return out, withReconcileErr(nil)
}

// load (package-private) reads the manifest from disk and validates
// every entry through newManifestEntry's invariant check. Persisted
// state must round-trip through the same constructor; any drift
// surfaces as ErrInvalidManifest, not as silently-loaded garbage.
func (l *Library) load() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loadLocked()
}

func (l *Library) loadLocked() error {
	data, err := os.ReadFile(l.manifestPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			l.manifest = make(map[string]manifestEntry)
			return nil
		}
		return fmt.Errorf("media library: read manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var persisted manifestFile
	if err := decoder.Decode(&persisted); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrInvalidManifest, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	if persisted.Version != manifestVersion {
		return fmt.Errorf("%w: version %d", ErrInvalidManifest, persisted.Version)
	}
	manifest := make(map[string]manifestEntry, len(persisted.Entries))
	for id, entry := range persisted.Entries {
		parsedID, err := uuid.Parse(id)
		if err != nil {
			return fmt.Errorf("%w: invalid ID %q", ErrInvalidManifest, id)
		}
		if entry.Id != nil && *entry.Id != parsedID {
			return fmt.Errorf("%w: ID mismatch for %q", ErrInvalidManifest, id)
		}
		// On Load, accept both production and fixture sources. Future
		// schema revisions may add new production values; we accept any
		// known string the schema enum carries and reject unknowns.
		// The gen type's Valid() covers production; fixtureSource is
		// checked separately so a regression where the gen type drops
		// the test_fixture constant doesn't silently invalidate
		// pre-existing manifests.
		if !entry.Source.Valid() && entry.Source != fixtureSource {
			return fmt.Errorf("%w: invalid source %q for %s", ErrInvalidManifest, entry.Source, id)
		}
		constructed, err := manifestEntryFromProjection(parsedID, l.workspaceID, entry)
		if err != nil {
			return err
		}
		if err := constructed.validate(l.workspaceID); err != nil {
			return err
		}
		manifest[id] = constructed
	}
	l.manifest = manifest
	l.rebuildStrandedLocked()
	return nil
}

// manifestEntryFromProjection builds a manifestEntry from the
// persisted gen.MediaLibraryEntry projection. The construction path
// re-applies the invariants newManifestEntry applies at runtime, so
// Load can never produce a domain-invalid entry. Tolerates a nil Id
// (the disk id is authoritative), a nil UploadedAt (the runtime path
// always sets one), and so on — each nil is a construction-time
// ErrInvalidManifest.
func manifestEntryFromProjection(id uuid.UUID, workspaceID string, p gen.MediaLibraryEntry) (manifestEntry, error) {
	if p.Id != nil && *p.Id != id {
		return manifestEntry{}, fmt.Errorf("%w: ID mismatch for %q", ErrInvalidManifest, id)
	}
	if p.WorkspaceId != nil && *p.WorkspaceId != "" && *p.WorkspaceId != workspaceID {
		return manifestEntry{}, fmt.Errorf("%w: workspace mismatch for %s", ErrInvalidManifest, id)
	}
	if p.Mime == nil || *p.Mime == "" {
		return manifestEntry{}, fmt.Errorf("%w: missing mime for %s", ErrInvalidManifest, id)
	}
	if p.Size == nil || *p.Size < 0 || *p.Size > MaxFileSize {
		return manifestEntry{}, fmt.Errorf("%w: invalid size for %s", ErrInvalidManifest, id)
	}
	if p.Sha256 == nil || !validDigest(*p.Sha256) {
		return manifestEntry{}, fmt.Errorf("%w: invalid sha256 for %s", ErrInvalidManifest, id)
	}
	if p.UploadedAt == nil || p.UploadedAt.IsZero() {
		return manifestEntry{}, fmt.Errorf("%w: missing uploaded_at for %s", ErrInvalidManifest, id)
	}
	if !p.Source.Valid() && p.Source != fixtureSource {
		return manifestEntry{}, fmt.Errorf("%w: invalid source %q for %s", ErrInvalidManifest, p.Source, id)
	}
	refcountValue := 0
	if p.Refcount != nil {
		refcountValue = *p.Refcount
	}
	observedAt := *p.UploadedAt
	if p.LastRefcountSeenAt != nil && !p.LastRefcountSeenAt.IsZero() {
		observedAt = *p.LastRefcountSeenAt
	}
	return newManifestEntry(
		id,
		workspaceID,
		p.Filename,
		*p.Mime,
		*p.Size,
		*p.Sha256,
		*p.UploadedAt,
		p.Source,
		refcountValue,
		observedAt,
	)
}

func (l *Library) changeRefcount(id string, delta int) (int, error) {
	if _, err := uuid.Parse(id); err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidMediaID, id)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, stranded := l.stranded[id]; stranded {
		return 0, fmt.Errorf("%w: %s", ErrEntryStranded, id)
	}
	entry, exists := l.manifest[id]
	if !exists {
		return 0, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	previous := int(entry.refcount)
	if delta > 0 && previous == math.MaxInt {
		return previous, ErrRefcountOverflow
	}
	if delta < 0 && previous == 0 {
		return previous, ErrRefcountUnderflow
	}
	next := previous + delta
	if next < 0 {
		return previous, ErrRefcountUnderflow
	}
	previousEntry := entry.clone()
	observedAt := l.now().UTC()
	newCount, err := newRefcount(next)
	if err != nil {
		return previous, err
	}
	entry.refcount = newCount
	entry.lastRefcountSeenAt = observedAt
	l.manifest[id] = entry
	if err := l.persistLocked(); err != nil {
		l.manifest[id] = previousEntry
		return previous, err
	}
	return next, nil
}

func (l *Library) persistLocked() error {
	if err := os.MkdirAll(l.path, 0o700); err != nil {
		return fmt.Errorf("media library: create directory: %w", err)
	}
	entries := make(map[string]gen.MediaLibraryEntry, len(l.manifest))
	for id, entry := range l.manifest {
		entries[id] = entry.projection()
	}
	data, err := json.MarshalIndent(manifestFile{
		Version: manifestVersion,
		Entries: entries,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("media library: encode manifest: %w", err)
	}
	if err := l.writeManifest(l.manifestPath(), data, 0o600); err != nil {
		return fmt.Errorf("media library: persist manifest: %w", err)
	}
	return nil
}

type cascadePending struct {
	id             string
	entry          manifestEntry
	quarantinePath string
	quarantined    bool
}

// restoreQuarantined reverses OrphanGC's forward quarantine rename
// (rawPath -> quarantinePath) for every id in the given map, best-effort.
// It returns the subset that FAILED to roll back — every id in the
// returned map still has its bytes sitting at quarantinePath, not
// rawPath, even though (in every caller so far) the manifest mutation for
// that id has just been reverted to claim the entry is present at
// rawPath. The caller MUST feed the returned ids into markStrandedLocked
// (see ErrEntryStranded) rather than only logging the rollback failure —
// that used to be the entirety of the handling here, which is exactly the
// TD this type closes.
func (l *Library) restoreQuarantined(quarantined map[string]string) map[string]string {
	failed := make(map[string]string)
	for id, quarantinePath := range quarantined {
		if err := l.renameFile(quarantinePath, l.rawPath(id)); err != nil {
			logger.WarnCF("media-library", "orphan rollback failed", map[string]any{
				"workspace_id": l.workspaceID,
				"media_id":     id,
				"error":        err.Error(),
			})
			failed[id] = quarantinePath
		}
	}
	return failed
}

// restoreCascadeQuarantined is CascadeDelete's counterpart to
// restoreQuarantined — same contract, same reason for returning (rather
// than only logging) the subset that failed to roll back.
func (l *Library) restoreCascadeQuarantined(deleted []cascadePending) []cascadePending {
	var failed []cascadePending
	for _, item := range deleted {
		if !item.quarantined {
			continue
		}
		if err := l.renameFile(item.quarantinePath, l.rawPath(item.id)); err != nil {
			logger.WarnCF("media-library", "cascade rollback failed", map[string]any{
				"workspace_id": l.workspaceID,
				"media_id":     item.id,
				"error":        err.Error(),
			})
			failed = append(failed, item)
		}
	}
	return failed
}

// markStrandedLocked records that id's raw bytes are stranded at
// quarantinePath because a compensating rename-back failed after a
// manifest mutation was rolled back to (once again, factually incorrectly)
// claim the entry is present at its normal rawPath. Must be called with
// l.mu held for writing. See ErrEntryStranded's doc for the full
// contract; reconcileStragglersLocked is the only path that clears an
// entry from this registry.
func (l *Library) markStrandedLocked(id, quarantinePath string) {
	if l.stranded == nil {
		l.stranded = make(map[string]string)
	}
	l.stranded[id] = quarantinePath
}

// rebuildStrandedLocked scans l.path for quarantine dotfiles left behind
// by an interrupted compound rollback failure (see ErrEntryStranded)
// whose id still appears in the just-loaded manifest, and repopulates
// l.stranded from what it finds. Called at the end of loadLocked so the
// corruption stays discoverable across a process restart — l.stranded is
// otherwise purely in-memory and would silently forget the corruption if
// the process restarted before OrphanGC's straggler reconciliation ran.
// Read-only: unlike reconcileStragglersLocked (OrphanGC's remediation
// half), this never removes a file or mutates l.manifest — Load has no
// license to delete anything, only to report state accurately.
func (l *Library) rebuildStrandedLocked() {
	l.stranded = nil
	dirEntries, err := os.ReadDir(l.path)
	if err != nil {
		// No directory yet (never-uploaded-to workspace) or unreadable —
		// either way there is nothing to detect; loadLocked's own
		// os.ReadFile(l.manifestPath()) call already handled (or will
		// separately surface) the missing/unreadable-directory case for
		// the manifest itself.
		return
	}
	for _, dirEntry := range dirEntries {
		if dirEntry.IsDir() {
			continue
		}
		id, ok := parseStragglerID(dirEntry.Name())
		if !ok {
			continue
		}
		if _, exists := l.manifest[id]; !exists {
			// A stray quarantine file whose id is NOT in the manifest is a
			// "clean leak" (a final-unlink failure whose manifest mutation
			// already committed correctly, see Delete/CascadeDelete/OrphanGC's
			// own FIX 3 notes) — not a manifest/disk divergence. Leave it for
			// reconcileStragglersLocked to sweep on the next OrphanGC run;
			// it is a disk-space leak, not a correctness lie.
			continue
		}
		l.markStrandedLocked(id, filepath.Join(l.path, dirEntry.Name()))
	}
}

// reconcileStragglersLocked is OrphanGC's (and CascadeDelete's) directory
// scan for quarantine dotfiles that a prior Delete / CascadeDelete /
// OrphanGC call left behind — the "OrphanGC only walks manifest entries,
// so it never notices these" gap — AND (UPLOAD-LEAK FIX, review) for leaked
// ".upload-*" temp files that uploadInternal's own defer-based cleanup
// failed to remove. Must be called with l.mu held for writing; every
// caller that can produce one of the quarantine-dotfile shapes (Delete,
// CascadeDelete, OrphanGC) holds l.mu.Lock() for its entire quarantine ->
// mutate -> persist -> final-unlink critical section, so any quarantine
// file this scan observes is guaranteed to be a genuine leftover, never a
// concurrent in-flight operation's file; the same holds for ".upload-*"
// names by construction of l.inFlightUploads (see its field doc).
//
// Three shapes are reconciled, each by attempting l.removeFile on the
// stray file:
//   - Stranded (ErrEntryStranded): the id is STILL in l.manifest (a
//     rollback restored the manifest mutation) and typically also in
//     l.stranded already. Successfully removing the stray file completes
//     the originally-failed delete: the (now provably byte-less) manifest
//     entry is removed, l.stranded is cleared for it, and it is reported
//     in the returned reclaimed slice so the caller's summary reflects it.
//   - Clean leak (quarantine dotfile): the id is NOT in l.manifest (a
//     final-unlink step failed AFTER a manifest mutation that itself
//     committed cleanly). Nothing to reconcile in the manifest; only the
//     disk leak is cleaned, and its size is folded into bytesFreed without
//     a synthesized manifest entry (there is no real entry left to
//     report).
//   - Leaked upload temp file: an ".upload-<uuid>" name not present in
//     l.inFlightUploads. It never had a manifest entry (uploadInternal
//     creates it before one exists), so — like the clean-leak shape above
//     — only the disk leak is cleaned and its size folded into bytesFreed.
//     A name still present in l.inFlightUploads is left untouched: it is
//     an active upload, not a leak.
//
// A file that still cannot be removed is left in place (and, for the
// stranded shape, l.stranded keeps pointing at it) — this function never
// gives up permanently, it just makes no progress on that one file until
// a future call succeeds. Any such removal failure is now also joined
// into the returned error (STRANDED-SKIP FIX, review) instead of only being logged,
// so a caller can distinguish "nothing to reconcile" from "reconcile
// failed" — see CascadeDelete/OrphanGC's own ids-collection loops, which
// skip any id still in l.stranded after this call returns rather than
// assuming a failed-but-only-logged reconcile silently succeeded. That
// assumption was the root cause a stranded id could be laundered into a
// false-success deletion: run through the normal quarantine dance, its
// rename-from-rawPath hits os.ErrNotExist (the real bytes are at the OLD
// quarantine path, not rawPath), gets treated as the benign "already gone"
// case, and the manifest entry is removed with its bytes counted as freed
// even though they were never touched.
func (l *Library) reconcileStragglersLocked() (
	reclaimed []gen.MediaLibraryEntry,
	bytesFreed int64,
	manifestChanged bool,
	err error,
) {
	dirEntries, readErr := os.ReadDir(l.path)
	if readErr != nil {
		if !errors.Is(readErr, os.ErrNotExist) {
			logger.WarnCF("media-library", "straggler reconcile: list directory failed", map[string]any{
				"workspace_id": l.workspaceID,
				"error":        readErr.Error(),
			})
			return nil, 0, false, fmt.Errorf("media library: straggler reconcile: list directory: %w", readErr)
		}
		return nil, 0, false, nil
	}
	var errs []error
	for _, dirEntry := range dirEntries {
		if dirEntry.IsDir() {
			continue
		}
		name := dirEntry.Name()

		if isUploadTempName(name) {
			if _, active := l.inFlightUploads[name]; active {
				continue
			}
			fullPath := filepath.Join(l.path, name)
			var strayBytes int64
			if info, statErr := dirEntry.Info(); statErr == nil {
				strayBytes = info.Size()
			}
			if removeErr := l.removeFile(fullPath); removeErr != nil {
				if !errors.Is(removeErr, os.ErrNotExist) {
					logger.WarnCF("media-library",
						"straggler reconcile: remove leaked upload temp file failed", map[string]any{
							"workspace_id": l.workspaceID,
							"path":         fullPath,
							"error":        removeErr.Error(),
						})
					errs = append(errs, fmt.Errorf(
						"media library: straggler reconcile: remove leaked upload temp file %s: %w", name, removeErr))
				}
				continue
			}
			bytesFreed += strayBytes
			continue
		}

		id, ok := parseStragglerID(name)
		if !ok {
			continue
		}
		fullPath := filepath.Join(l.path, name)
		var strayBytes int64
		if info, statErr := dirEntry.Info(); statErr == nil {
			strayBytes = info.Size()
		}
		if removeErr := l.removeFile(fullPath); removeErr != nil {
			if !errors.Is(removeErr, os.ErrNotExist) {
				logger.WarnCF("media-library", "straggler reconcile: remove failed", map[string]any{
					"workspace_id": l.workspaceID,
					"media_id":     id,
					"path":         fullPath,
					"error":        removeErr.Error(),
				})
				errs = append(errs, fmt.Errorf("media library: straggler reconcile: remove %s: %w", id, removeErr))
				continue
			}
			// Already gone (e.g. a previous reconcile call raced ahead of
			// this one's directory listing) — fall through and still clear
			// the bookkeeping below.
		}
		delete(l.stranded, id)
		if entry, exists := l.manifest[id]; exists {
			reclaimed = append(reclaimed, entry.projection())
			bytesFreed += entry.size
			delete(l.manifest, id)
			manifestChanged = true
			continue
		}
		bytesFreed += strayBytes
	}
	if len(errs) > 0 {
		return reclaimed, bytesFreed, manifestChanged, errors.Join(errs...)
	}
	return reclaimed, bytesFreed, manifestChanged, nil
}

// parseStragglerID extracts the media id from a quarantine dotfile name
// (".delete-<id>-<uuid>", ".cascade-<id>-<uuid>", ".gc-<id>-<uuid>" — see
// Delete / CascadeDelete / OrphanGC's own quarantinePath construction).
// Returns ok=false for anything else, including a ".upload-<uuid>" temp
// file — a different, unrelated leak category with no embedded manifest
// id at all (uploadInternal creates it before a manifest entry exists).
// reconcileStragglersLocked recognizes and reconciles that shape
// separately via isUploadTempName + l.inFlightUploads (UPLOAD-LEAK FIX, review) —
// see the inFlightUploads field doc for why that check is race-free.
func parseStragglerID(name string) (string, bool) {
	for _, prefix := range stragglerPrefixes {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := strings.TrimPrefix(name, prefix)
		const uuidLen = 36
		if len(rest) < uuidLen+1 || rest[uuidLen] != '-' {
			continue
		}
		id := rest[:uuidLen]
		if _, err := uuid.Parse(id); err != nil {
			continue
		}
		return id, true
	}
	return "", false
}

func (l *Library) manifestPath() string {
	return filepath.Join(l.path, manifestFileName)
}

func (l *Library) rawPath(id string) string {
	return filepath.Join(l.path, id)
}

// UploadFixture uploads a fixture entry through the same Upload code
// path but with the internal-only fixtureSource value, which is NOT
// exposed on the wire. Test helpers use this to populate the library
// with non-user-upload entries without widening the public Upload API
// surface. The returned ref and projection match the production Upload
// contract; the only difference is the entry's Source field.
//
// This helper is package-private so production callers cannot accidentally
// land the fixture source value in a persisted manifest. The wire
// schema (contracts/components/schemas/MediaLibraryEntry.yaml) drops
// "test_fixture" from its production enum; the gen type's Valid()
// covers production values only.
func (l *Library) UploadFixture(
	filename string,
	reader io.Reader,
) (string, gen.MediaLibraryEntry, error) {
	return l.uploadInternal(filename, fixtureSource, false, reader)
}

func normalizeFilename(filename string) (string, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" || len(filename) > 256 || strings.ContainsAny(filename, `/\\`) {
		return "", fmt.Errorf("%w: %q", ErrInvalidFilename, filename)
	}
	for _, character := range filename {
		if unicode.IsControl(character) {
			return "", fmt.Errorf("%w: control character", ErrInvalidFilename)
		}
	}
	return filename, nil
}

func validDigest(digest string) bool {
	if len(digest) != sha256.Size*2 || strings.ToLower(digest) != digest {
		return false
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == sha256.Size
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: trailing JSON value", ErrInvalidManifest)
		}
		return fmt.Errorf("%w: trailing data: %v", ErrInvalidManifest, err)
	}
	return nil
}

// manifestFile is the persisted JSON shape. Version is required so a
// future schema migration can branch on it; Entries carries the
// gen.MediaLibraryEntry projection only — refcount lives ON each entry
// (TD-M1 single source of truth), so there is no parallel Refcounts
// map. The on-disk envelope is narrower than the legacy
// {Entries, Refcounts} split.
type manifestFile struct {
	Version int                              `json:"version"`
	Entries map[string]gen.MediaLibraryEntry `json:"entries"`
}
