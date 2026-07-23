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
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

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

type Library struct {
	mu          sync.RWMutex
	path        string
	workspaceID string
	manifest    map[string]gen.MediaLibraryEntry
	refcounts   map[string]int
	now         func() time.Time
}

type Option func(*Library)

type OrphanGCConfig struct {
	Enabled bool
	MaxAge  time.Duration
}

type manifestFile struct {
	Version   int                              `json:"version"`
	Entries   map[string]gen.MediaLibraryEntry `json:"entries"`
	Refcounts map[string]int                   `json:"refcounts"`
}

func WithClock(now func() time.Time) Option {
	return func(library *Library) {
		if now != nil {
			library.now = now
		}
	}
}

func New(home, workspaceID string, options ...Option) (*Library, error) {
	workspaceDir, err := workspace.SafeWorkspaceDir(home, workspaceID)
	if err != nil {
		return nil, err
	}
	library := &Library{
		path:        filepath.Join(workspaceDir, "media"),
		workspaceID: workspaceID,
		manifest:    make(map[string]gen.MediaLibraryEntry),
		refcounts:   make(map[string]int),
		now:         time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(library)
		}
	}
	if err := library.Load(); err != nil {
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
		entries = append(entries, l.entryViewLocked(id))
	}
	return entries
}

func (l *Library) Upload(
	filename string,
	source gen.MediaLibraryEntrySource,
	reader io.Reader,
) (string, gen.MediaLibraryEntry, error) {
	filename, normalizeErr := normalizeFilename(filename)
	if normalizeErr != nil {
		return "", gen.MediaLibraryEntry{}, normalizeErr
	}
	if source != gen.UserUpload && source != gen.TestFixture {
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
	initialRefcount := 0
	lastRefcountSeenAt := uploadedAt
	entry := gen.MediaLibraryEntry{
		Filename:           filename,
		Id:                 uuidPointer(id),
		LastRefcountSeenAt: timePointer(lastRefcountSeenAt),
		Mime:               stringPointer(mime),
		Refcount:           intPointer(initialRefcount),
		Sha256:             stringPointer(digest),
		Size:               int64Pointer(size),
		Source:             source,
		UploadedAt:         timePointer(uploadedAt),
		WorkspaceId:        l.workspaceID,
	}

	l.mu.Lock()
	l.manifest[id.String()] = cloneEntry(entry)
	l.refcounts[id.String()] = 0
	if persistErr := l.persistLocked(); persistErr != nil {
		delete(l.manifest, id.String())
		delete(l.refcounts, id.String())
		l.mu.Unlock()
		removeErr := os.Remove(rawPath)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			rollbackErr := fmt.Errorf("media library: rollback upload: %w", removeErr)
			return "", gen.MediaLibraryEntry{}, errors.Join(persistErr, rollbackErr)
		}
		return "", gen.MediaLibraryEntry{}, persistErr
	}
	result := l.entryViewLocked(id.String())
	l.mu.Unlock()

	ref := "media://workspace/" + l.workspaceID + "/" + id.String()
	return ref, result, nil
}

func (l *Library) Read(id string) ([]byte, gen.MediaLibraryEntry, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, gen.MediaLibraryEntry{}, fmt.Errorf("%w: %q", ErrInvalidMediaID, id)
	}
	l.mu.RLock()
	_, exists := l.manifest[id]
	if !exists {
		l.mu.RUnlock()
		return nil, gen.MediaLibraryEntry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	entry := l.entryViewLocked(id)
	l.mu.RUnlock()
	if entry.Size == nil || entry.Sha256 == nil || *entry.Size < 0 || *entry.Size > MaxFileSize {
		return nil, entry, fmt.Errorf("%w: invalid integrity fields for %s", ErrIntegrityCheckFailed, id)
	}

	file, err := os.Open(l.rawPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, entry, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, entry, fmt.Errorf("media library: open %s: %w", id, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, *entry.Size+1))
	if err != nil {
		return nil, entry, fmt.Errorf("media library: read %s: %w", id, err)
	}
	actualDigest := sha256.Sum256(data)
	actualDigestHex := hex.EncodeToString(actualDigest[:])
	if int64(len(data)) != *entry.Size || actualDigestHex != *entry.Sha256 {
		logger.WarnCF("media-library", "sha256 verification failed", map[string]any{
			"workspace_id": l.workspaceID,
			"media_id":     id,
			"expected":     *entry.Sha256,
			"actual":       actualDigestHex,
		})
		return nil, entry, fmt.Errorf("%w: %s", ErrIntegrityCheckFailed, id)
	}
	return data, entry, nil
}

func (l *Library) ResolveWithWorkspace(
	ref string,
	callerWorkspaceID *string,
) ([]byte, gen.MediaLibraryEntry, error) {
	if callerWorkspaceID == nil || *callerWorkspaceID == "" {
		return nil, gen.MediaLibraryEntry{}, ErrWorkspaceContextRequired
	}
	const prefix = "media://workspace/"
	if !strings.HasPrefix(ref, prefix) {
		return nil, gen.MediaLibraryEntry{}, fmt.Errorf("%w: %q", ErrInvalidRef, ref)
	}
	parts := strings.Split(strings.TrimPrefix(ref, prefix), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, gen.MediaLibraryEntry{}, fmt.Errorf("%w: %q", ErrInvalidRef, ref)
	}
	if parts[0] != l.workspaceID || *callerWorkspaceID != parts[0] {
		return nil, gen.MediaLibraryEntry{}, fmt.Errorf(
			"%w: caller=%q ref_workspace=%q",
			ErrWorkspaceMismatch,
			*callerWorkspaceID,
			parts[0],
		)
	}
	return l.Read(parts[1])
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
	count, exists := l.refcounts[id]
	if !exists {
		return 0, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return count, nil
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
		if l.refcounts[id] != 0 {
			continue
		}
		lastSeen := entry.UploadedAt
		if entry.LastRefcountSeenAt != nil {
			lastSeen = entry.LastRefcountSeenAt
		}
		if lastSeen == nil || now.Before(*lastSeen) || now.Sub(*lastSeen) < maxAge {
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
		deleted = append(deleted, l.entryViewLocked(id))
		delete(l.manifest, id)
		delete(l.refcounts, id)
	}
	if err := l.persistLocked(); err != nil {
		for _, entry := range deleted {
			id := entry.Id.String()
			l.manifest[id] = cloneEntry(entry)
			l.refcounts[id] = 0
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

func (l *Library) Store() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.persistLocked()
}

func (l *Library) Load() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	data, err := os.ReadFile(l.manifestPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			l.manifest = make(map[string]gen.MediaLibraryEntry)
			l.refcounts = make(map[string]int)
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
	if persisted.Entries == nil {
		persisted.Entries = make(map[string]gen.MediaLibraryEntry)
	}
	if persisted.Refcounts == nil {
		persisted.Refcounts = make(map[string]int)
	}
	manifest := make(map[string]gen.MediaLibraryEntry, len(persisted.Entries))
	refcounts := make(map[string]int, len(persisted.Entries))
	for id, entry := range persisted.Entries {
		count, hasRefcount := persisted.Refcounts[id]
		if !hasRefcount {
			return fmt.Errorf("%w: missing refcount for %s", ErrInvalidManifest, id)
		}
		if err := l.validatePersistedEntry(id, entry, count); err != nil {
			return err
		}
		entry.Refcount = intPointer(count)
		manifest[id] = cloneEntry(entry)
		refcounts[id] = count
	}
	for id, count := range persisted.Refcounts {
		if count < 0 {
			return fmt.Errorf("%w: negative refcount for %s", ErrInvalidManifest, id)
		}
		if _, exists := persisted.Entries[id]; !exists {
			return fmt.Errorf("%w: refcount without entry for %s", ErrInvalidManifest, id)
		}
	}
	l.manifest = manifest
	l.refcounts = refcounts
	return nil
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
	previous := l.refcounts[id]
	if delta > 0 && previous == math.MaxInt {
		return previous, ErrRefcountOverflow
	}
	if delta < 0 && previous == 0 {
		return previous, ErrRefcountUnderflow
	}
	next := previous + delta
	previousEntry := cloneEntry(entry)
	observedAt := l.now().UTC()
	entry.LastRefcountSeenAt = timePointer(observedAt)
	entry.Refcount = intPointer(next)
	l.manifest[id] = entry
	l.refcounts[id] = next
	if err := l.persistLocked(); err != nil {
		l.manifest[id] = previousEntry
		l.refcounts[id] = previous
		return previous, err
	}
	return next, nil
}

func (l *Library) persistLocked() error {
	if err := os.MkdirAll(l.path, 0o700); err != nil {
		return fmt.Errorf("media library: create directory: %w", err)
	}
	entries := make(map[string]gen.MediaLibraryEntry, len(l.manifest))
	refcounts := make(map[string]int, len(l.refcounts))
	for id := range l.manifest {
		entries[id] = l.entryViewLocked(id)
		refcounts[id] = l.refcounts[id]
	}
	data, err := json.MarshalIndent(manifestFile{
		Version:   manifestVersion,
		Entries:   entries,
		Refcounts: refcounts,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("media library: encode manifest: %w", err)
	}
	if err := fileutil.WriteFileAtomic(l.manifestPath(), data, 0o600); err != nil {
		return fmt.Errorf("media library: persist manifest: %w", err)
	}
	return nil
}

func (l *Library) validatePersistedEntry(id string, entry gen.MediaLibraryEntry, count int) error {
	parsedID, err := uuid.Parse(id)
	if err != nil || entry.Id == nil || *entry.Id != parsedID {
		return fmt.Errorf("%w: invalid ID %q", ErrInvalidManifest, id)
	}
	if entry.WorkspaceId != l.workspaceID {
		return fmt.Errorf("%w: workspace mismatch for %s", ErrInvalidManifest, id)
	}
	if _, err := normalizeFilename(entry.Filename); err != nil {
		return fmt.Errorf("%w: filename for %s: %v", ErrInvalidManifest, id, err)
	}
	if entry.Mime == nil || *entry.Mime == "" || entry.Size == nil || *entry.Size < 0 || *entry.Size > MaxFileSize {
		return fmt.Errorf("%w: invalid MIME or size for %s", ErrInvalidManifest, id)
	}
	if entry.Sha256 == nil || !validDigest(*entry.Sha256) {
		return fmt.Errorf("%w: invalid sha256 for %s", ErrInvalidManifest, id)
	}
	if entry.UploadedAt == nil || entry.UploadedAt.IsZero() {
		return fmt.Errorf("%w: missing uploaded_at for %s", ErrInvalidManifest, id)
	}
	if entry.Source != gen.UserUpload && entry.Source != gen.TestFixture {
		return fmt.Errorf("%w: invalid source for %s", ErrInvalidManifest, id)
	}
	if count < 0 {
		return fmt.Errorf("%w: negative refcount for %s", ErrInvalidManifest, id)
	}
	if entry.Refcount != nil && *entry.Refcount != count {
		return fmt.Errorf("%w: refcount mismatch for %s", ErrInvalidManifest, id)
	}
	return nil
}

func (l *Library) entryViewLocked(id string) gen.MediaLibraryEntry {
	entry := cloneEntry(l.manifest[id])
	entry.Refcount = intPointer(l.refcounts[id])
	return entry
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

func (l *Library) manifestPath() string {
	return filepath.Join(l.path, manifestFileName)
}

func (l *Library) rawPath(id string) string {
	return filepath.Join(l.path, id)
}

func cloneEntry(entry gen.MediaLibraryEntry) gen.MediaLibraryEntry {
	entry.Id = clonePointer(entry.Id)
	entry.LastRefcountSeenAt = clonePointer(entry.LastRefcountSeenAt)
	entry.Mime = clonePointer(entry.Mime)
	entry.Refcount = clonePointer(entry.Refcount)
	entry.Sha256 = clonePointer(entry.Sha256)
	entry.Size = clonePointer(entry.Size)
	entry.UploadedAt = clonePointer(entry.UploadedAt)
	return entry
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func uuidPointer(value uuid.UUID) *uuid.UUID {
	return &value
}

func stringPointer(value string) *string {
	return &value
}

func intPointer(value int) *int {
	return &value
}

func int64Pointer(value int64) *int64 {
	return &value
}

func timePointer(value time.Time) *time.Time {
	return &value
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
