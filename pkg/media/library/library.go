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
)

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

func New(home, workspaceID string, options ...Option) (*Library, error) {
	workspaceDir, err := safeWorkspaceDir(home, workspaceID)
	if err != nil {
		return nil, err
	}
	library := &Library{
		path:        filepath.Join(workspaceDir, "media"),
		workspaceID: workspaceID,
		manifest:    make(map[string]manifestEntry),
		now:         time.Now,
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

// fixtureSource is the internal-only MediaLibraryEntrySource used by
// the package-private UploadFixture test helper. It is NOT exposed on
// the wire — contracts/components/schemas/MediaLibraryEntry.yaml
// drops test_fixture from the production enum (Wave 1 TD-m1). Tests
// that need a non-user-upload source reach it through UploadFixture.
var fixtureSource gen.MediaLibraryEntrySource = "test_fixture"

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
	filename, normalizeErr := normalizeFilename(filename)
	if normalizeErr != nil {
		return "", gen.MediaLibraryEntry{}, normalizeErr
	}
	if source != gen.UserUpload {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("%w: %q", ErrSourceNotAllowed, source)
	}
	if reader == nil {
		return "", gen.MediaLibraryEntry{}, errors.New("media library: upload reader is nil")
	}
	if mkdirErr := os.MkdirAll(l.path, 0o700); mkdirErr != nil {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("media library: create directory: %w", mkdirErr)
	}

	id := uuid.New()
	temporary, createErr := os.CreateTemp(l.path, ".upload-*")
	if createErr != nil {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("media library: create upload: %w", createErr)
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		if keepTemporary {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
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
	if directory, openErr := os.Open(l.path); openErr == nil {
		if syncErr := directory.Sync(); syncErr != nil {
			logger.WarnCF("media-library", "upload directory sync failed", map[string]any{
				"workspace_id": l.workspaceID,
				"error":        syncErr.Error(),
			})
		}
		_ = directory.Close()
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

func (l *Library) Read(id string) ([]byte, gen.MediaLibraryEntry, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, gen.MediaLibraryEntry{}, fmt.Errorf("%w: %q", ErrInvalidMediaID, id)
	}
	l.mu.RLock()
	entry, exists := l.manifest[id]
	l.mu.RUnlock()
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
	l.mu.RUnlock()
	if !exists {
		return "", media.MediaMeta{}, fmt.Errorf("%w: %s", ErrNotFound, mediaID)
	}
	return l.rawPath(mediaID), media.MediaMeta{
		Filename:    entry.filename,
		ContentType: entry.mime,
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
// entry's filename + bytes_freed.
func (l *Library) Delete(id string) (gen.MediaLibraryEntry, error) {
	if _, err := uuid.Parse(id); err != nil {
		return gen.MediaLibraryEntry{}, fmt.Errorf("%w: %q", ErrInvalidMediaID, id)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, exists := l.manifest[id]
	if !exists {
		return gen.MediaLibraryEntry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	rawPath := l.rawPath(id)
	quarantinePath := filepath.Join(l.path, ".delete-"+id+"-"+uuid.NewString())
	quarantined := false
	if err := os.Rename(rawPath, quarantinePath); err != nil {
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
			if renameErr := os.Rename(quarantinePath, rawPath); renameErr != nil {
				logger.WarnCF("media-library", "delete rollback rename failed", map[string]any{
					"workspace_id": l.workspaceID,
					"media_id":     id,
					"error":        renameErr.Error(),
				})
			}
		}
		return gen.MediaLibraryEntry{}, err
	}
	if quarantined {
		if removeErr := os.Remove(quarantinePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			logger.WarnCF("media-library", "delete final unlink failed", map[string]any{
				"workspace_id": l.workspaceID,
				"media_id":     id,
				"error":        removeErr.Error(),
			})
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
// returned summary.
func (l *Library) CascadeDelete() (entries []gen.MediaLibraryEntry, bytesFreed int64, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ids := make([]string, 0, len(l.manifest))
	for id := range l.manifest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil, 0, nil
	}

	deleted := make([]cascadePending, 0, len(ids))
	previousManifest := make(map[string]manifestEntry, len(l.manifest))

	for _, id := range ids {
		entry := l.manifest[id]
		rawPath := l.rawPath(id)
		quarantinePath := filepath.Join(l.path, ".cascade-"+id+"-"+uuid.NewString())
		item := cascadePending{id: id, entry: entry.clone(), quarantinePath: quarantinePath}
		if renameErr := os.Rename(rawPath, quarantinePath); renameErr != nil {
			if errors.Is(renameErr, os.ErrNotExist) {
				item.quarantined = false
			} else {
				l.restoreCascadeQuarantined(deleted)
				return nil, 0, fmt.Errorf("media library: quarantine cascade %s: %w", id, renameErr)
			}
		} else {
			item.quarantined = true
		}
		previousManifest[id] = entry.clone()
		delete(l.manifest, id)
		deleted = append(deleted, item)
	}

	if persistErr := l.persistLocked(); persistErr != nil {
		l.manifest = previousManifest
		l.restoreCascadeQuarantined(deleted)
		return nil, 0, persistErr
	}

	for _, item := range deleted {
		if item.quarantined {
			if removeErr := os.Remove(item.quarantinePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				logger.WarnCF("media-library", "cascade final unlink failed", map[string]any{
					"workspace_id": l.workspaceID,
					"media_id":     item.id,
					"error":        removeErr.Error(),
				})
			}
		}
	}

	out := make([]gen.MediaLibraryEntry, 0, len(deleted))
	var totalBytes int64
	for _, item := range deleted {
		out = append(out, item.entry.projection())
		totalBytes += item.entry.size
	}
	return out, totalBytes, nil
}

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
	ids := make([]string, 0)
	for id, entry := range l.manifest {
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
		return nil, nil
	}
	sort.Strings(ids)

	deleted := make([]gen.MediaLibraryEntry, 0, len(ids))
	quarantined := make(map[string]string, len(ids))
	for _, id := range ids {
		rawPath := l.rawPath(id)
		quarantinePath := filepath.Join(l.path, ".gc-"+id+"-"+uuid.NewString())
		if err := os.Rename(rawPath, quarantinePath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				l.restoreQuarantined(quarantined)
				return nil, fmt.Errorf("media library: quarantine orphan %s: %w", id, err)
			}
		} else {
			quarantined[id] = quarantinePath
		}
	}
	for _, id := range ids {
		deleted = append(deleted, l.manifest[id].projection())
		delete(l.manifest, id)
	}
	if err := l.persistLocked(); err != nil {
		for _, projection := range deleted {
			if projection.Id == nil {
				continue
			}
			id := projection.Id.String()
			// Roll back from a projection is best-effort: rebuild the
			// manifestEntry from the projection. The projection lost
			// nothing the rollback needs (refcount + last_seen are
			// both on the projection).
			rc, _ := newRefcount(derefInt(projection.Refcount))
			l.manifest[id] = manifestEntry{
				id:                 *projection.Id,
				workspaceID:        derefString(projection.WorkspaceId),
				filename:           projection.Filename,
				mime:               derefString(projection.Mime),
				size:               derefInt64(projection.Size),
				sha256:             derefString(projection.Sha256),
				uploadedAt:         derefTime(projection.UploadedAt),
				source:             projection.Source,
				refcount:           rc,
				lastRefcountSeenAt: derefTime(projection.LastRefcountSeenAt),
			}
		}
		l.restoreQuarantined(quarantined)
		return nil, err
	}

	var removeErrors []error
	for id, quarantinePath := range quarantined {
		if err := os.Remove(quarantinePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			removeErrors = append(removeErrors, fmt.Errorf("media library: remove orphan %s: %w", id, err))
		}
	}
	if len(removeErrors) > 0 {
		return deleted, errors.Join(removeErrors...)
	}
	return deleted, nil
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
	if err := fileutil.WriteFileAtomic(l.manifestPath(), data, 0o600); err != nil {
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

func (l *Library) restoreQuarantined(quarantined map[string]string) {
	for id, quarantinePath := range quarantined {
		if err := os.Rename(quarantinePath, l.rawPath(id)); err != nil {
			logger.WarnCF("media-library", "orphan rollback failed", map[string]any{
				"workspace_id": l.workspaceID,
				"media_id":     id,
				"error":        err.Error(),
			})
		}
	}
}

func (l *Library) restoreCascadeQuarantined(deleted []cascadePending) {
	for _, item := range deleted {
		if !item.quarantined {
			continue
		}
		if err := os.Rename(item.quarantinePath, l.rawPath(item.id)); err != nil {
			logger.WarnCF("media-library", "cascade rollback failed", map[string]any{
				"workspace_id": l.workspaceID,
				"media_id":     item.id,
				"error":        err.Error(),
			})
		}
	}
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
	filename, normalizeErr := normalizeFilename(filename)
	if normalizeErr != nil {
		return "", gen.MediaLibraryEntry{}, normalizeErr
	}
	if reader == nil {
		return "", gen.MediaLibraryEntry{}, errors.New("media library: upload reader is nil")
	}
	if mkdirErr := os.MkdirAll(l.path, 0o700); mkdirErr != nil {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("media library: create directory: %w", mkdirErr)
	}

	id := uuid.New()
	temporary, createErr := os.CreateTemp(l.path, ".upload-*")
	if createErr != nil {
		return "", gen.MediaLibraryEntry{}, fmt.Errorf("media library: create upload: %w", createErr)
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		if keepTemporary {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
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
		fixtureSource,
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

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func derefTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
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
