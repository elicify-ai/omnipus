package media

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// registryFile is the persisted form of FileMediaStore. Only fields needed
// to make ResolveWithMeta and ReleaseAll work after a restart are stored —
// path-state refcount and other in-memory bookkeeping is rebuilt on load.
type registryFile struct {
	Version int                       `json:"version"`
	Entries map[string]registryRecord `json:"entries"`
}

type registryRecord struct {
	Path          string    `json:"path"`
	Filename      string    `json:"filename,omitempty"`
	ContentType   string    `json:"content_type,omitempty"`
	Source        string    `json:"source,omitempty"`
	CleanupPolicy string    `json:"cleanup_policy,omitempty"`
	Scope         string    `json:"scope,omitempty"`
	StoredAt      time.Time `json:"stored_at"`
}

const registryVersion = 1

// registryPath returns the on-disk path for the persisted registry. Empty
// string means persistence is disabled (e.g. unit tests where TempDir
// resolves to a per-test scratch directory).
func registryPath() string {
	dir := TempDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "registry.json")
}

// SaveRegistry serializes the current ref → path/meta mapping to disk so a
// subsequent process can resolve refs that outlived their original
// in-memory store. Best-effort: failures are logged but do not block
// callers. Safe to call from any code path that already holds, or does
// not hold, s.mu — it acquires its own RLock.
func (s *FileMediaStore) SaveRegistry() {
	path := registryPath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		logger.WarnCF("media", "registry: mkdir failed",
			map[string]any{"path": path, "error": err.Error()})
		return
	}

	s.mu.RLock()
	rf := registryFile{
		Version: registryVersion,
		Entries: make(map[string]registryRecord, len(s.refs)),
	}
	for ref, entry := range s.refs {
		rec := registryRecord{
			Path:          entry.path,
			Filename:      entry.meta.Filename,
			ContentType:   entry.meta.ContentType,
			Source:        entry.meta.Source,
			CleanupPolicy: string(entry.meta.CleanupPolicy),
			Scope:         s.refToScope[ref],
			StoredAt:      entry.storedAt,
		}
		rf.Entries[ref] = rec
	}
	s.mu.RUnlock()

	data, err := json.Marshal(rf)
	if err != nil {
		logger.WarnCF("media", "registry: marshal failed",
			map[string]any{"error": err.Error()})
		return
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		logger.WarnCF("media", "registry: write tmp failed",
			map[string]any{"path": tmp, "error": err.Error()})
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		logger.WarnCF("media", "registry: rename failed",
			map[string]any{"path": path, "error": err.Error()})
		_ = os.Remove(tmp)
	}
}

// LoadRegistry reads the persisted registry from disk and seeds the
// in-memory maps. Returns nil silently when the file does not exist
// (fresh install). Entries whose underlying file is missing are dropped
// so stale refs don't accumulate forever.
func (s *FileMediaStore) LoadRegistry() error {
	path := registryPath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("media registry: read %s: %w", path, err)
	}
	var rf registryFile
	if err := json.Unmarshal(data, &rf); err != nil {
		return fmt.Errorf("media registry: parse %s: %w", path, err)
	}
	if rf.Version != registryVersion {
		logger.WarnCF("media", "registry: unknown version, ignoring",
			map[string]any{"version": rf.Version, "expected": registryVersion})
		return nil
	}

	loaded := 0
	dropped := 0

	s.mu.Lock()
	defer s.mu.Unlock()
	for ref, rec := range rf.Entries {
		if _, err := os.Stat(rec.Path); err != nil {
			dropped++
			continue
		}
		policy := CleanupPolicy(rec.CleanupPolicy)
		s.refs[ref] = mediaEntry{
			path: rec.Path,
			meta: MediaMeta{
				Filename:      rec.Filename,
				ContentType:   rec.ContentType,
				Source:        rec.Source,
				CleanupPolicy: normalizeCleanupPolicy(policy),
			},
			storedAt: rec.StoredAt,
		}
		s.refToPath[ref] = rec.Path
		if rec.Scope != "" {
			s.refToScope[ref] = rec.Scope
			if s.scopeToRefs[rec.Scope] == nil {
				s.scopeToRefs[rec.Scope] = make(map[string]struct{})
			}
			s.scopeToRefs[rec.Scope][ref] = struct{}{}
		}
		ps := s.pathStates[rec.Path]
		ps.refCount++
		if rec.CleanupPolicy == string(CleanupPolicyDeleteOnCleanup) && ps.refCount == 1 {
			ps.deleteEligible = true
		}
		s.pathStates[rec.Path] = ps
		loaded++
	}

	logger.InfoCF("media", "registry: loaded persisted refs",
		map[string]any{"loaded": loaded, "dropped_missing": dropped, "path": path})
	return nil
}
