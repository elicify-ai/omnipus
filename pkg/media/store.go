package media

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// CleanupPolicy controls how the MediaStore treats the underlying file when
// a ref is released or expires.
type CleanupPolicy string

const (
	// CleanupPolicyDeleteOnCleanup means the file is store-managed and may be
	// deleted once the final ref for that path is gone.
	CleanupPolicyDeleteOnCleanup CleanupPolicy = "delete_on_cleanup"
	// CleanupPolicyForgetOnly means the store should only drop ref mappings and
	// must never delete the underlying file.
	CleanupPolicyForgetOnly CleanupPolicy = "forget_only"
)

// SessionInlineScopePrefix is the media-store scope prefix for tool-generated
// inline media (browser screenshots, charts, etc.) that belongs to a persisted
// chat session. Entries under this scope are SESSION-PINNED: the TTL cleaner
// (CleanExpired) never expires them — they stay resolvable so chat history keeps
// rendering — and they are freed only by ReleaseAll when the owning session is
// deleted. The full scope is SessionInlineScopePrefix + "<sessionID>" so a single
// ReleaseAll frees all of a session's inline media. Other forget_only scopes
// (user uploads, send_file) are NOT pinned and still age out of the in-memory
// index via CleanExpired (their files are preserved; only the ref is dropped).
const SessionInlineScopePrefix = "tool:inline:session:"

// MediaMeta holds metadata about a stored media file.
type MediaMeta struct {
	Filename      string
	ContentType   string
	SHA256        string        // hex-encoded sha256 digest; empty when unavailable (legacy refs)
	Source        string        // "telegram", "discord", "tool:image-gen", etc.
	CleanupPolicy CleanupPolicy // defaults to CleanupPolicyDeleteOnCleanup
}

// MediaStore manages the lifecycle of media files associated with processing scopes.
type MediaStore interface {
	// Store registers an existing local file under the given scope.
	// Returns a ref identifier (e.g. "media://<id>").
	// Store does not move or copy the file; it only records the mapping.
	// If meta.CleanupPolicy is empty, CleanupPolicyDeleteOnCleanup is assumed.
	Store(localPath string, meta MediaMeta, scope string) (ref string, err error)

	// Resolve returns the local file path for a given ref.
	Resolve(ref string) (localPath string, err error)

	// ResolveWithMeta returns the local file path and metadata for a given ref.
	ResolveWithMeta(ref string) (localPath string, meta MediaMeta, err error)

	// ResolveWithOpts is the workspace-aware resolver (FR-028/FR-028a).
	// Legacy "media://<uuid>" refs resolve via the global registry
	// regardless of opts (a nil CallerWorkspace bypasses the membership
	// check — FR-029 sunset v0.1.2). "media://workspace/<ws>/<id>" refs
	// require a non-nil CallerWorkspace matching the ref's workspace and
	// are routed to the owning workspace library (FR-028a Spoofing guard).
	ResolveWithOpts(ref string, opts ResolveOpts) (localPath string, err error)

	// ResolveWithMetaOpts is the metadata-returning counterpart of
	// ResolveWithOpts. See ResolveWithOpts for the FR-028/FR-028a rules.
	ResolveWithMetaOpts(ref string, opts ResolveOpts) (localPath string, meta MediaMeta, err error)

	// ReleaseAll deletes all files registered under the given scope
	// and removes the mapping entries. File-not-exist errors are ignored.
	ReleaseAll(scope string) error

	// RefByPath returns an existing ref for the given local path, if one is
	// registered. Used to short-circuit duplicate registrations when the
	// same on-disk file would otherwise get a second ref (e.g. send_file
	// invoked on a path that browser.screenshot already stored inline).
	RefByPath(localPath string) (ref string, ok bool)
}

// mediaEntry holds the path and metadata for a stored media file.
type mediaEntry struct {
	path     string
	meta     MediaMeta
	storedAt time.Time
}

type pathRefState struct {
	refCount       int
	deleteEligible bool
}

// MediaCleanerConfig configures the background TTL cleanup.
type MediaCleanerConfig struct {
	Enabled  bool
	MaxAge   time.Duration
	Interval time.Duration
}

// FileMediaStore is a pure in-memory implementation of MediaStore.
// Files are expected to already exist on disk (e.g. in /tmp/omnipus_media/).
type FileMediaStore struct {
	mu          sync.RWMutex
	refs        map[string]mediaEntry
	scopeToRefs map[string]map[string]struct{}
	refToScope  map[string]string
	refToPath   map[string]string
	pathStates  map[string]pathRefState

	cleanerCfg MediaCleanerConfig
	stop       chan struct{}
	startOnce  sync.Once
	stopOnce   sync.Once
	nowFunc    func() time.Time // for testing

	// libraryProvider optionally resolves "media://workspace/<ws>/<id>" refs
	// via the owning workspace's media library (FR-028). It is nil on a
	// fresh store (legacy-only posture) and wired by the gateway once the
	// workspace-library cache is available. libProvMu guards the field.
	libProvMu       sync.RWMutex
	libraryProvider WorkspaceLibraryProvider

	// saveMu guards the debounced-save state. saveTimer coalesces multiple
	// Store/ReleaseAll calls into one disk write per saveDebounce window;
	// see scheduleSave.
	saveMu      sync.Mutex
	saveTimer   *time.Timer
	saveStopped bool
}

// saveDebounce is the coalescing window for SaveRegistry. Calls landing
// within this interval collapse into one disk write — under burst load
// (mass send_file or ReleaseAll on session end) this prevents N concurrent
// goroutines from serializing the registry N times. Tests may override the
// timer directly via the lock if they need synchronous behavior.
const saveDebounce = 200 * time.Millisecond

// NewFileMediaStore creates a new FileMediaStore without background cleanup.
func NewFileMediaStore() *FileMediaStore {
	return &FileMediaStore{
		refs:        make(map[string]mediaEntry),
		scopeToRefs: make(map[string]map[string]struct{}),
		refToScope:  make(map[string]string),
		refToPath:   make(map[string]string),
		pathStates:  make(map[string]pathRefState),
		nowFunc:     time.Now,
	}
}

// NewFileMediaStoreWithCleanup creates a FileMediaStore with TTL-based background cleanup.
func NewFileMediaStoreWithCleanup(cfg MediaCleanerConfig) *FileMediaStore {
	return &FileMediaStore{
		refs:        make(map[string]mediaEntry),
		scopeToRefs: make(map[string]map[string]struct{}),
		refToScope:  make(map[string]string),
		refToPath:   make(map[string]string),
		pathStates:  make(map[string]pathRefState),
		cleanerCfg:  cfg,
		stop:        make(chan struct{}),
		nowFunc:     time.Now,
	}
}

// Store registers a local file under the given scope. The file must exist.
func (s *FileMediaStore) Store(localPath string, meta MediaMeta, scope string) (string, error) {
	if _, err := os.Stat(localPath); err != nil {
		return "", fmt.Errorf("media store: %s: %w", localPath, err)
	}

	ref := "media://" + uuid.New().String()
	meta.CleanupPolicy = normalizeCleanupPolicy(meta.CleanupPolicy)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.refs[ref] = mediaEntry{path: localPath, meta: meta, storedAt: s.nowFunc()}
	if s.scopeToRefs[scope] == nil {
		s.scopeToRefs[scope] = make(map[string]struct{})
	}
	s.scopeToRefs[scope][ref] = struct{}{}
	s.refToScope[ref] = scope
	s.refToPath[ref] = localPath

	pathState := s.pathStates[localPath]
	if pathState.refCount == 0 {
		pathState.deleteEligible = meta.CleanupPolicy == CleanupPolicyDeleteOnCleanup
	} else if meta.CleanupPolicy == CleanupPolicyForgetOnly {
		// Be conservative: once a path is borrowed externally, never let this
		// lifecycle auto-delete it even if store-managed refs also exist.
		pathState.deleteEligible = false
	}
	pathState.refCount++
	s.pathStates[localPath] = pathState

	s.scheduleSave()
	return ref, nil
}

// RefByPath returns the most-recently-registered ref pointing at localPath,
// or "", false if no live ref exists for that path.
func (s *FileMediaStore) RefByPath(localPath string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var (
		best     string
		bestTime time.Time
	)
	for ref, entry := range s.refs {
		if entry.path != localPath {
			continue
		}
		if best == "" || entry.storedAt.After(bestTime) {
			best = ref
			bestTime = entry.storedAt
		}
	}
	return best, best != ""
}

// SetWorkspaceLibraryProvider wires the workspace-library resolver used to
// resolve "media://workspace/<ws>/<id>" refs (FR-028). It is idempotent and
// safe to call once at gateway boot; passing nil restores the legacy-only
// posture (workspace refs return "unknown ref" after the guard). The guard
// itself (FR-028a) fires regardless of whether a provider is wired.
func (s *FileMediaStore) SetWorkspaceLibraryProvider(p WorkspaceLibraryProvider) {
	s.libProvMu.Lock()
	s.libraryProvider = p
	s.libProvMu.Unlock()
}

// Resolve returns the local path for the given ref. It is the legacy
// call-site entry point and delegates to ResolveWithOpts with a zero
// (nil-context) ResolveOpts: legacy media://<uuid> refs resolve unchanged,
// and workspace refs still hit the FR-028a guard (they cannot resolve
// without caller context, which is the correct posture for a legacy
// caller that has no workspace to claim).
func (s *FileMediaStore) Resolve(ref string) (string, error) {
	return s.ResolveWithOpts(ref, ResolveOpts{})
}

// ResolveWithMeta returns the local path and metadata for the given ref. It
// is the legacy call-site entry point; see Resolve for the delegation rule.
func (s *FileMediaStore) ResolveWithMeta(ref string) (string, MediaMeta, error) {
	return s.ResolveWithMetaOpts(ref, ResolveOpts{})
}

// ResolveWithOpts is the workspace-aware resolver (FR-028/FR-028a). It
// discriminates on the ref prefix: "media://workspace/<ws>/<id>" routes to
// the owning workspace library after the membership guard, every other
// "media://<uuid>" ref resolves via the legacy global registry. A nil
// CallerWorkspace bypasses the guard for legacy refs (FR-029) and is
// rejected for workspace refs (FR-028a).
func (s *FileMediaStore) ResolveWithOpts(ref string, opts ResolveOpts) (string, error) {
	path, _, err := s.ResolveWithMetaOpts(ref, opts)
	return path, err
}

// ResolveWithMetaOpts implements the two-tier resolution (FR-028) and the
// cross-workspace Spoofing guard (FR-028a). See ResolveWithOpts.
func (s *FileMediaStore) ResolveWithMetaOpts(ref string, opts ResolveOpts) (string, MediaMeta, error) {
	if IsWorkspaceRef(ref) {
		return s.resolveWorkspaceRef(ref, opts)
	}
	return s.resolveLegacyWithMeta(ref)
}

// resolveWorkspaceRef enforces the FR-028a guard and, when a library
// provider is wired, delegates to the owning workspace's library. Without a
// provider the ref is unresolvable from the global registry (workspace refs
// are never registered there), so it surfaces as the standard unknown-ref
// error — after the guard has already authorized the caller.
func (s *FileMediaStore) resolveWorkspaceRef(ref string, opts ResolveOpts) (string, MediaMeta, error) {
	workspaceID, _, ok := ParseWorkspaceRef(ref)
	if !ok {
		return "", MediaMeta{}, fmt.Errorf("%w: %q", ErrInvalidWorkspaceRef, ref)
	}
	if err := ValidateCallerWorkspace(workspaceID, opts); err != nil {
		return "", MediaMeta{}, err
	}
	s.libProvMu.RLock()
	provider := s.libraryProvider
	s.libProvMu.RUnlock()
	if provider == nil {
		return "", MediaMeta{}, fmt.Errorf("media store: unknown ref: %s", ref)
	}
	resolver, err := provider(workspaceID)
	if err != nil {
		return "", MediaMeta{}, fmt.Errorf("media store: workspace library %q unavailable: %w", workspaceID, err)
	}
	if resolver == nil {
		return "", MediaMeta{}, fmt.Errorf("media store: unknown ref: %s", ref)
	}
	return resolver.ResolvePathWithCaller(ref, opts.CallerWorkspace)
}

// resolveLegacyWithMeta is the pre-Rev4 global-registry lookup. It performs
// no membership check (legacy refs carry no workspace), preserving the
// exact behavior every legacy call-site relies on (FR-029).
func (s *FileMediaStore) resolveLegacyWithMeta(ref string) (string, MediaMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.refs[ref]
	if !ok {
		return "", MediaMeta{}, fmt.Errorf("media store: unknown ref: %s", ref)
	}
	return entry.path, entry.meta, nil
}

// ReleaseAll removes all files under the given scope and cleans up mappings.
// Phase 1 (under lock): remove entries from maps.
// Phase 2 (no lock): delete store-managed files from disk once their final
// path ref is gone.
func (s *FileMediaStore) ReleaseAll(scope string) error {
	// Phase 1: collect paths and remove from maps under lock
	var paths []string

	s.mu.Lock()
	refs, ok := s.scopeToRefs[scope]
	if !ok {
		s.mu.Unlock()
		return nil
	}

	for ref := range refs {
		fallbackPath := ""
		if entry, exists := s.refs[ref]; exists {
			fallbackPath = entry.path
		}
		if removablePath, shouldDelete := s.releaseRefLocked(ref, fallbackPath); shouldDelete {
			paths = append(paths, removablePath)
		}
	}
	delete(s.scopeToRefs, scope)
	s.mu.Unlock()

	// Phase 2: delete files without holding the lock
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			logger.WarnCF("media", "release: failed to remove file", map[string]any{
				"path":  p,
				"error": err.Error(),
			})
		}
	}

	s.scheduleSave()
	return nil
}

// CleanExpired removes all entries older than MaxAge that use
// CleanupPolicyDeleteOnCleanup. Entries with CleanupPolicyForgetOnly are
// intentionally skipped — they are session-bound and must remain fully
// resolvable until their session is released via ReleaseAll. Releasing them
// here would cause 404s when revisiting chat history that contains
// tool-generated screenshots or charts.
//
// Phase 1 (under lock): identify expired delete_on_cleanup entries and remove
// from maps.
// Phase 2 (no lock): delete store-managed files from disk to minimize lock
// contention.
func (s *FileMediaStore) CleanExpired() int {
	if s.cleanerCfg.MaxAge <= 0 {
		return 0
	}

	// Phase 1: collect expired entries under lock
	type expiredEntry struct {
		ref        string
		deletePath string
	}

	s.mu.Lock()
	cutoff := s.nowFunc().Add(-s.cleanerCfg.MaxAge)
	var expired []expiredEntry

	for ref, entry := range s.refs {
		if !entry.storedAt.Before(cutoff) {
			continue
		}
		// Never TTL-expire SESSION-PINNED inline media (scope prefix
		// SessionInlineScopePrefix): it must stay resolvable so chat history keeps
		// rendering tool-generated screenshots/charts, and it is freed explicitly
		// by ReleaseAll on session delete. Other forget_only entries (user uploads,
		// send_file) are NOT pinned — they still age out of the in-memory index
		// here as before (their files are preserved; only the ref is dropped),
		// which avoids an unbounded ref leak for those non-session scopes.
		if scope, ok := s.refToScope[ref]; ok && strings.HasPrefix(scope, SessionInlineScopePrefix) {
			continue
		}

		if scope, ok := s.refToScope[ref]; ok {
			if scopeRefs, ok := s.scopeToRefs[scope]; ok {
				delete(scopeRefs, ref)
				if len(scopeRefs) == 0 {
					delete(s.scopeToRefs, scope)
				}
			}
		}

		expiredItem := expiredEntry{ref: ref}
		if deletePath, shouldDelete := s.releaseRefLocked(ref, entry.path); shouldDelete {
			expiredItem.deletePath = deletePath
		}
		expired = append(expired, expiredItem)
	}
	s.mu.Unlock()

	// Phase 2: delete files without holding the lock
	for _, e := range expired {
		if e.deletePath == "" {
			continue
		}
		if err := os.Remove(e.deletePath); err != nil && !os.IsNotExist(err) {
			logger.WarnCF("media", "cleanup: failed to remove file", map[string]any{
				"path":  e.deletePath,
				"error": err.Error(),
			})
		}
	}

	return len(expired)
}

func normalizeCleanupPolicy(policy CleanupPolicy) CleanupPolicy {
	switch policy {
	case "", CleanupPolicyDeleteOnCleanup:
		return CleanupPolicyDeleteOnCleanup
	case CleanupPolicyForgetOnly:
		return CleanupPolicyForgetOnly
	default:
		return CleanupPolicyDeleteOnCleanup
	}
}

func (s *FileMediaStore) releaseRefLocked(ref, fallbackPath string) (string, bool) {
	path := fallbackPath
	if storedPath, ok := s.refToPath[ref]; ok {
		path = storedPath
		delete(s.refToPath, ref)
	}

	delete(s.refs, ref)
	delete(s.refToScope, ref)

	if path == "" {
		return "", false
	}

	pathState, ok := s.pathStates[path]
	if !ok {
		return "", false
	}
	if pathState.refCount <= 1 {
		delete(s.pathStates, path)
		return path, pathState.deleteEligible
	}

	pathState.refCount--
	s.pathStates[path] = pathState
	return "", false
}

// Start begins the background cleanup goroutine if cleanup is enabled.
// Safe to call multiple times; only the first call starts the goroutine.
func (s *FileMediaStore) Start() {
	if !s.cleanerCfg.Enabled || s.stop == nil {
		return
	}
	if s.cleanerCfg.Interval <= 0 || s.cleanerCfg.MaxAge <= 0 {
		logger.WarnCF("media", "cleanup: skipped due to invalid config", map[string]any{
			"interval": s.cleanerCfg.Interval.String(),
			"max_age":  s.cleanerCfg.MaxAge.String(),
		})
		return
	}

	s.startOnce.Do(func() {
		logger.InfoCF("media", "cleanup enabled", map[string]any{
			"interval": s.cleanerCfg.Interval.String(),
			"max_age":  s.cleanerCfg.MaxAge.String(),
		})

		go func() {
			ticker := time.NewTicker(s.cleanerCfg.Interval)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					if n := s.CleanExpired(); n > 0 {
						logger.InfoCF("media", "cleanup: removed expired entries", map[string]any{
							"count": n,
						})
					}
				case <-s.stop:
					return
				}
			}
		}()
	})
}

// Stop terminates the background cleanup goroutine and flushes any pending
// registry save synchronously, ensuring writes scheduled just before exit
// are not lost.
// Safe to call multiple times; only the first call closes the channel.
func (s *FileMediaStore) Stop() {
	s.flushPendingSave()
	if s.stop == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stop)
	})
}

// scheduleSave coalesces registry persistence: callers within saveDebounce
// of each other share one write. The timer fires in its own short-lived
// goroutine so the caller never blocks. After Stop, scheduleSave is a
// no-op — the final flush already happened in flushPendingSave.
func (s *FileMediaStore) scheduleSave() {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if s.saveStopped {
		return
	}
	if s.saveTimer != nil {
		// A save is already pending; the in-flight timer will pick up the
		// state at fire-time. SaveRegistry takes its own RLock snapshot so
		// concurrent map mutations between schedule and fire are safe.
		return
	}
	s.saveTimer = time.AfterFunc(saveDebounce, func() {
		s.SaveRegistry()
		s.saveMu.Lock()
		s.saveTimer = nil
		s.saveMu.Unlock()
	})
}

// flushPendingSave runs any pending registry write synchronously and blocks
// further scheduling. Called from Stop on graceful shutdown.
func (s *FileMediaStore) flushPendingSave() {
	s.saveMu.Lock()
	hadPending := s.saveTimer != nil
	if hadPending {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	s.saveStopped = true
	s.saveMu.Unlock()
	if hadPending {
		s.SaveRegistry()
	}
}
